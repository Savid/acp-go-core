package sessionlog

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// cursorCheckBytes is how much of the file at each end of the read prefix a
// cursor's checksums cover.
const cursorCheckBytes = 4096

// Cursor records how far a source has read. A JSONL cursor holds the offset
// after the last complete line, the file's identity, and checksums of the
// first and last bytes before the offset. A non-file source carries its own
// position. The zero Cursor is the start of any history. Cursors are not
// comparable: a position may hold a map.
type Cursor struct {
	_        [0]func()
	offset   int64
	file     os.FileInfo
	head     [sha256.Size]byte
	check    [sha256.Size]byte
	position any
}

// Position is the cursor of a non-file source that has read through v, such
// as the last row id or sequence it returned.
func Position(v any) Cursor { return Cursor{position: v} }

// Position returns the value a non-file source stored with Position, or nil.
func (c Cursor) Position() any { return c.position }

// Offset is the byte offset after the last complete line a JSONL read
// returned.
func (c Cursor) Offset() int64 { return c.offset }

// Tail is what one read returned: the rows after the cursor it was given, or
// every row when Full is set.
type Tail struct {
	// Rows are trimmed, non-empty lines. A JSONL read slices them from one
	// buffer.
	Rows [][]byte
	// Next is where the following read continues.
	Next Cursor
	// Full reports that Rows are the complete history rather than a
	// continuation of the cursor.
	Full bool
}

// Source reads native rows after from. A source returns a Full tail when from
// is the zero Cursor, and may return one whenever it cannot continue from.
// A continuation returns only rows after from, with the cursor after them.
type Source func(from Cursor) (Tail, error)

// File is the source for an append-only JSONL file at path.
func File(path string) Source {
	return func(from Cursor) (Tail, error) { return ReadJSONL(path, from) }
}

// Rows is the source for a history the caller already holds in full.
func Rows(rows [][]byte) Source {
	return func(Cursor) (Tail, error) { return Tail{Rows: rows, Full: true}, nil }
}

// ReadJSONL reads the complete lines appended to the JSONL file at path after
// from. It reads every line from the start, and reports Full, when from is the
// zero Cursor or the file no longer extends what from read: it was replaced,
// truncated, or rewritten within the first or last 4 KiB before the cursor. A
// rewrite confined to the bytes between those windows is not detected; a
// source that rewrites its file in place resets its mirror. A trailing line
// without its newline is left for a later read. A missing file reads as an
// empty history.
func ReadJSONL(path string, from Cursor) (Tail, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Tail{Full: true}, nil
	}

	if err != nil {
		return Tail{}, err
	}

	defer func() { _ = file.Close() }()

	return ReadJSONLFile(file, from, -1)
}

// ReadJSONLFile is ReadJSONL over an open file, reading no further than size
// bytes; a negative size reads to the file's current end. A size beyond the
// file's end fails.
func ReadJSONLFile(file *os.File, from Cursor, size int64) (Tail, error) {
	info, err := file.Stat()
	if err != nil {
		return Tail{}, err
	}

	if !info.Mode().IsRegular() {
		return Tail{}, errors.New("native log is not a regular file")
	}

	switch {
	case size < 0:
		size = info.Size()
	case size > info.Size():
		return Tail{}, fmt.Errorf("native log holds %d bytes, fewer than the %d requested", info.Size(), size)
	}

	start := int64(0)

	if from.offset > 0 && from.offset <= size && from.file != nil && os.SameFile(from.file, info) {
		head, check, checkErr := checksums(file, from.offset)
		if checkErr != nil {
			return Tail{}, checkErr
		}

		if head == from.head && check == from.check {
			start = from.offset
		}
	}

	data := make([]byte, size-start)

	read, err := file.ReadAt(data, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return Tail{}, err
	}

	data = data[:read]

	rows, consumed := splitLines(data)
	next := Cursor{offset: start + int64(consumed), file: info}

	if next.offset > 0 {
		if next.head, next.check, err = checksums(file, next.offset); err != nil {
			return Tail{}, err
		}
	}

	return Tail{Rows: rows, Next: next, Full: start == 0}, nil
}

// splitLines returns the trimmed non-empty complete lines of data and the
// number of bytes they span, through the last newline.
func splitLines(data []byte) ([][]byte, int) {
	end := bytes.LastIndexByte(data, '\n') + 1
	rows := make([][]byte, 0, bytes.Count(data[:end], []byte{'\n'}))

	for line := range bytes.Lines(data[:end]) {
		if row := bytes.TrimSpace(line); len(row) > 0 {
			rows = append(rows, row[:len(row):len(row)])
		}
	}

	return rows, end
}

// checksums digest the first and the last cursorCheckBytes of file before
// offset.
func checksums(file *os.File, offset int64) (head, tail [sha256.Size]byte, err error) {
	if head, err = digestRange(file, 0, min(offset, cursorCheckBytes)); err != nil {
		return head, tail, err
	}

	tail, err = digestRange(file, max(offset-cursorCheckBytes, 0), offset)

	return head, tail, err
}

func digestRange(file *os.File, start, end int64) ([sha256.Size]byte, error) {
	window := make([]byte, end-start)

	if _, err := file.ReadAt(window, start); err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("read native log: %w", err)
	}

	return sha256.Sum256(window), nil
}

// CutTornTail ends the JSONL file at path on a line boundary, so the next line
// a harness appends starts on a line of its own. A trailing line without its
// newline that is one complete JSON object is a record whose newline was not
// written, and is terminated; any other unterminated tail is bytes a writer
// that died mid-record left behind, and is cut. A harness that writes a record
// without its newline in the same write can leave a record that still parses
// as complete while more was due; that harness must not rely on this cut. A
// missing file is left missing.
func CutTornTail(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	defer func() { _ = file.Close() }()

	return cutTornTail(file)
}

func cutTornTail(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}

	end := info.Size()
	window := make([]byte, min(end, cursorCheckBytes))

	for end > 0 {
		start := max(end-int64(len(window)), 0)
		chunk := window[:end-start]

		if _, err := file.ReadAt(chunk, start); err != nil {
			return fmt.Errorf("read native log: %w", err)
		}

		if index := bytes.LastIndexByte(chunk, '\n'); index >= 0 {
			end = start + int64(index) + 1

			break
		}

		end = start
	}

	if end == info.Size() {
		return nil
	}

	tail := make([]byte, info.Size()-end)
	if _, err := file.ReadAt(tail, end); err != nil {
		return fmt.Errorf("read native log: %w", err)
	}

	if validRow(tail) {
		if _, err := file.WriteAt([]byte{'\n'}, info.Size()); err != nil {
			return fmt.Errorf("terminate native record: %w", err)
		}

		return file.Sync()
	}

	if err := file.Truncate(end); err != nil {
		return fmt.Errorf("cut torn native line: %w", err)
	}

	return file.Sync()
}

// WriteJSONL publishes rows as the JSONL file at path, one row per line,
// creating its directory. The file is staged beside its final name and renamed
// into place, so a reader sees the previous file or the complete new one. It
// returns the cursor after the last row.
func WriteJSONL(path string, rows [][]byte) (Cursor, error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return Cursor{}, fmt.Errorf("create native log directory: %w", err)
	}

	staging, err := os.CreateTemp(directory, "."+filepath.Base(path)+".*.staging")
	if err != nil {
		return Cursor{}, fmt.Errorf("stage native log: %w", err)
	}

	defer func() { _ = os.Remove(staging.Name()) }()

	cursor, err := writeRows(staging, rows)
	if closeErr := staging.Close(); err == nil {
		err = closeErr
	}

	if err != nil {
		return Cursor{}, fmt.Errorf("write native log: %w", err)
	}

	if err := os.Rename(staging.Name(), path); err != nil {
		return Cursor{}, fmt.Errorf("publish native log: %w", err)
	}

	return cursor, nil
}

func writeRows(file *os.File, rows [][]byte) (Cursor, error) {
	buffered := bufio.NewWriter(file)

	for index, row := range rows {
		if bytes.IndexByte(row, '\n') >= 0 {
			return Cursor{}, fmt.Errorf("native row %d spans lines", index)
		}

		_, _ = buffered.Write(row)
		_ = buffered.WriteByte('\n')
	}

	if err := buffered.Flush(); err != nil {
		return Cursor{}, err
	}

	if err := file.Sync(); err != nil {
		return Cursor{}, err
	}

	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return Cursor{}, err
	}

	head, check, err := checksums(file, info.Size())

	return Cursor{offset: info.Size(), file: info, head: head, check: check}, err
}
