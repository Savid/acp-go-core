package wire

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
)

// NativeLineMaxBytes is the family bound on one LF-delimited record a native
// harness writes to its stdout. Records may carry base64 images and large
// tool output; a longer record breaks the stream.
const NativeLineMaxBytes = 64 << 20

// lineReaderBuffer is the fixed read buffer behind every LineReader.
const lineReaderBuffer = 64 << 10

// ErrLineTooLong reports a record longer than the reader's bound.
var ErrLineTooLong = errors.New("native record exceeds its line bound")

// LineReader reads LF-delimited records from a native stream. LF is the only
// delimiter, so U+2028 and U+2029 inside JSON strings never split a record,
// and one trailing CR is removed. Its read buffer is fixed: each record is a
// fresh allocation the caller owns, so an oversized record never leaves a
// grown buffer behind.
type LineReader struct {
	reader   *bufio.Reader
	maxBytes int
}

// NewLineReader reads records of at most maxBytes from r; a non-positive
// maxBytes means NativeLineMaxBytes.
func NewLineReader(r io.Reader, maxBytes int) *LineReader {
	if maxBytes <= 0 {
		maxBytes = NativeLineMaxBytes
	}

	return &LineReader{reader: bufio.NewReaderSize(r, lineReaderBuffer), maxBytes: maxBytes}
}

// Next returns the next record without its delimiter. It returns io.EOF at a
// record boundary, io.ErrUnexpectedEOF for an unterminated final record, and
// ErrLineTooLong for a record past the bound; both end the stream.
func (l *LineReader) Next() ([]byte, error) {
	var line []byte

	for {
		chunk, err := l.reader.ReadSlice('\n')
		line = append(line, chunk...)

		length := len(line)
		if err == nil {
			length--
		}

		if length > l.maxBytes {
			return nil, fmt.Errorf("%w of %d bytes", ErrLineTooLong, l.maxBytes)
		}

		switch {
		case err == nil:
			line = bytes.TrimSuffix(line[:len(line)-1], []byte("\r"))

			return line, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(line) > 0:
			return nil, io.ErrUnexpectedEOF
		default:
			return nil, err
		}
	}
}
