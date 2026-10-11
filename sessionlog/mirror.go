package sessionlog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"sync"

	acpcore "github.com/savid/acp-go-core"
)

// Mirror publishes one session's native rows and configuration to a store and
// remembers what the store holds: the row count, a digest of the rows, a
// digest of the encoded configuration, and the source cursor after the last
// committed row. When the native history still extends the committed rows, a
// commit appends only the new rows, replacing the configuration record only
// when its encoding changed. Otherwise it republishes the whole generation.
// Every write to the session's store generation goes through its one Mirror;
// a write that bypasses it with a different row count fails the next append,
// which a full republish repairs.
type Mirror struct {
	store     acpcore.SessionStore
	sessionID string

	mu        sync.Mutex
	committed bool
	rows      int
	digest    hash.Hash
	record    [sha256.Size]byte
	cursor    Cursor
}

// NewMirror returns the mirror of sessionID in store. It holds nothing, so its
// first commit publishes the whole generation unless Adopt or Hydrate tells it
// what the store already holds.
func NewMirror(store acpcore.SessionStore, sessionID string) *Mirror {
	return &Mirror{store: store, sessionID: sessionID, digest: sha256.New()}
}

// Reset forgets what the mirror committed, so its next commit republishes the
// whole generation and may shorten the history, as a native binding change
// requires.
func (m *Mirror) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.forget()
}

// Adopt records that the store already holds exactly rows and record, as Load
// returned them, with next the source cursor after the last row. It writes
// nothing; the next commit continues from next.
func (m *Mirror) Adopt(rows [][]byte, record any, next Cursor) error {
	encoded, err := encodeRecord(record)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.adopt(rows, encoded, next)

	return nil
}

// Commit reads the native rows source returns and publishes them with the
// configuration record returns for that read. record runs under the mirror's
// lock after the read, so the configuration matches the rows it commits. When
// an append fails, Commit reads the whole history again and computes its
// configuration for that read, so source and record may each run more than
// once in one Commit; a caller applies anything they decide only after Commit
// returns nil. The commit is durable when it returns; a failure leaves the
// previous generation intact. A history shorter than the committed one fails
// unless the mirror was Reset.
func (m *Mirror) Commit(ctx context.Context, source Source, record func(Tail) (any, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	from := Cursor{}
	if m.committed {
		from = m.cursor
	}

	tail, encoded, err := read(source, from, record)
	if err != nil {
		return err
	}

	if !tail.Full {
		appendErr := m.appendTail(ctx, tail, encoded)
		if appendErr == nil || errors.Is(appendErr, errInvalidRow) {
			return appendErr
		}

		// An append fails when the store holds a generation the mirror did
		// not commit or cannot write; republishing the whole history repairs
		// the first and reports the second.
		if tail, encoded, err = read(source, Cursor{}, record); err != nil {
			return err
		}

		return m.commitFull(ctx, false, tail, encoded)
	}

	return m.commitFull(ctx, true, tail, encoded)
}

// Hydrate reconciles the native JSONL file at path with the rows the store
// holds for the session before a load or resume. Native rows that extend the
// stored rows win and are committed with record; a missing or shorter native
// file is rewritten from the store; a disagreement at a shared position fails.
// Bytes after the native file's last complete line are cut first. validate,
// when set, sees the reconciled history before anything is written. stored
// and record MUST be the generation Load returned from this mirror's store.
// It returns the rows the session now holds.
func (m *Mirror) Hydrate(ctx context.Context, path string, stored [][]byte, record any, validate func(rows [][]byte) error) ([][]byte, error) {
	encoded, err := encodeRecord(record)
	if err != nil {
		return nil, err
	}

	if cutErr := CutTornTail(path); cutErr != nil {
		return nil, cutErr
	}

	native, err := ReadJSONL(path, Cursor{})
	if err != nil {
		return nil, fmt.Errorf("read native rows: %w", err)
	}

	rows, nativeWins, err := reconcile(native.Rows, stored)
	if err != nil {
		return nil, err
	}

	if validate != nil && len(rows) > 0 {
		if validateErr := validate(rows); validateErr != nil {
			return nil, validateErr
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.adopt(stored, encoded, Cursor{})

	if nativeWins {
		if len(rows) > len(stored) {
			if commitErr := m.commitFull(ctx, true, Tail{Rows: rows, Next: native.Next, Full: true}, encoded); commitErr != nil {
				return nil, commitErr
			}
		}

		m.cursor = native.Next

		return rows, nil
	}

	cursor, err := WriteJSONL(path, rows)
	if err != nil {
		return nil, err
	}

	m.cursor = cursor

	return rows, nil
}

var errInvalidRow = errors.New("invalid native row")

// read runs source from from and encodes the configuration for what it read.
func read(source Source, from Cursor, record func(Tail) (any, error)) (Tail, []byte, error) {
	tail, err := source(from)
	if err != nil {
		return Tail{}, nil, fmt.Errorf("read native rows: %w", err)
	}

	value, err := record(tail)
	if err != nil {
		return Tail{}, nil, err
	}

	encoded, err := encodeRecord(value)

	return tail, encoded, err
}

func encodeRecord(record any) ([]byte, error) {
	encoded, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("encode session record: %w", err)
	}

	return encoded, nil
}

// appendTail appends a continuation of the committed rows.
func (m *Mirror) appendTail(ctx context.Context, tail Tail, encoded []byte) error {
	if err := validRows(tail.Rows); err != nil {
		return err
	}

	if len(tail.Rows) == 0 && sha256.Sum256(encoded) == m.record {
		m.cursor = tail.Next

		return nil
	}

	if err := m.append(ctx, tail.Rows, encoded); err != nil {
		return err
	}

	m.extend(tail.Rows)
	m.record = sha256.Sum256(encoded)
	m.cursor = tail.Next

	return nil
}

// commitFull publishes a complete history: skipped when the store already
// holds it, appended when it extends the committed rows and appendable is set,
// otherwise republished. Only rows past a matching committed prefix are
// validated. A failure keeps what the mirror remembers, since the store keeps
// its previous generation.
func (m *Mirror) commitFull(ctx context.Context, appendable bool, tail Tail, encoded []byte) error {
	if m.committed && len(tail.Rows) < m.rows {
		return fmt.Errorf("native log shrank from %d to %d rows", m.rows, len(tail.Rows))
	}

	extends := m.committed && m.extendsCommitted(tail.Rows)

	if extends {
		added := tail.Rows[m.rows:]
		if err := validRows(added); err != nil {
			return err
		}

		if len(added) == 0 && sha256.Sum256(encoded) == m.record {
			m.cursor = tail.Next

			return nil
		}

		if appendable && m.append(ctx, added, encoded) == nil {
			m.extend(added)
			m.record = sha256.Sum256(encoded)
			m.cursor = tail.Next

			return nil
		}
	} else if err := validRows(tail.Rows); err != nil {
		return err
	}

	if err := m.replace(ctx, tail.Rows, encoded); err != nil {
		return err
	}

	m.adopt(tail.Rows, encoded, tail.Next)

	return nil
}

// extendsCommitted reports whether rows begin with exactly the committed rows.
func (m *Mirror) extendsCommitted(rows [][]byte) bool {
	digest := sha256.New()
	for _, row := range rows[:m.rows] {
		writeDigest(digest, row)
	}

	return bytes.Equal(digest.Sum(nil), m.digest.Sum(nil))
}

func (m *Mirror) append(ctx context.Context, rows [][]byte, encoded []byte) error {
	ctx, cancel := context.WithTimeout(ctx, acpcore.SessionStoreTimeout)
	defer cancel()

	var replacements []acpcore.SessionStoreReplacement
	if sha256.Sum256(encoded) != m.record {
		replacements = []acpcore.SessionStoreReplacement{m.configReplacement(encoded)}
	}

	return m.store.Append(ctx, acpcore.SessionKey{SessionID: m.sessionID}, m.rows, entries(rows), replacements)
}

func (m *Mirror) replace(ctx context.Context, rows [][]byte, encoded []byte) error {
	ctx, cancel := context.WithTimeout(ctx, acpcore.SessionStoreTimeout)
	defer cancel()

	main := acpcore.SessionKey{SessionID: m.sessionID}

	return m.store.Replace(ctx, main, []acpcore.SessionStoreReplacement{
		{Key: main, Entries: entries(rows)},
		m.configReplacement(encoded),
	})
}

func (m *Mirror) configReplacement(encoded []byte) acpcore.SessionStoreReplacement {
	return acpcore.SessionStoreReplacement{
		Key:     acpcore.SessionKey{SessionID: m.sessionID, Subpath: ConfigSubpath},
		Entries: []acpcore.SessionStoreEntry{encoded},
	}
}

// adopt records rows and encoded as the whole committed generation.
func (m *Mirror) adopt(rows [][]byte, encoded []byte, next Cursor) {
	m.forget()
	m.committed = true
	m.record = sha256.Sum256(encoded)
	m.cursor = next
	m.extend(rows)
}

// extend records rows as committed after the current ones.
func (m *Mirror) extend(rows [][]byte) {
	for _, row := range rows {
		writeDigest(m.digest, row)
	}

	m.rows += len(rows)
}

func (m *Mirror) forget() {
	m.committed = false
	m.rows = 0
	m.digest.Reset()
	m.record = [sha256.Size]byte{}
	m.cursor = Cursor{}
}

// writeDigest adds one length-prefixed row to digest.
func writeDigest(digest hash.Hash, row []byte) {
	var length [binary.MaxVarintLen64]byte

	digest.Write(length[:binary.PutUvarint(length[:], uint64(len(row)))])
	digest.Write(row)
}

func validRows(rows [][]byte) error {
	for index, row := range rows {
		if !validRow(row) {
			return fmt.Errorf("%w %d", errInvalidRow, index)
		}
	}

	return nil
}

func entries(rows [][]byte) []acpcore.SessionStoreEntry {
	converted := make([]acpcore.SessionStoreEntry, len(rows))
	for index, row := range rows {
		converted[index] = row
	}

	return converted
}
