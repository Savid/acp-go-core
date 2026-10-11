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
// remembers what it committed. When the store implements
// acpcore.SessionStoreAppender and the native history still extends the
// committed rows, a commit appends only the new rows, replacing the
// configuration record only when it changed. Otherwise it republishes the
// whole generation. Every write to the session's store generation goes
// through its one Mirror; a write that bypasses it is detected by the
// append base and repaired by a full republish.
type Mirror struct {
	store     acpcore.SessionStore
	sessionID string

	mu        sync.Mutex
	committed bool
	rows      int
	digest    hash.Hash
	record    []byte
	cursor    Cursor
}

// NewMirror returns the mirror of sessionID in store. It has committed
// nothing, so its first commit publishes the whole generation.
func NewMirror(store acpcore.SessionStore, sessionID string) *Mirror {
	return &Mirror{store: store, sessionID: sessionID, digest: sha256.New()}
}

// Committed reports whether the store holds a generation this mirror
// committed or hydrated.
func (m *Mirror) Committed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.committed
}

// Reset forgets what the mirror committed, so its next commit republishes the
// whole generation and may shorten the history, as a native binding change
// requires.
func (m *Mirror) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.forget()
}

// Commit publishes the native rows source reads with record as the session's
// configuration. It is durable when it returns; a failure leaves the previous
// generation intact. A history shorter than the committed one fails unless
// the mirror was Reset.
func (m *Mirror) Commit(ctx context.Context, source Source, record any) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode session record: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	appender, appends := m.store.(acpcore.SessionStoreAppender)

	from := Cursor{}
	if appends && m.committed {
		from = m.cursor
	}

	tail, err := source(from)
	if err != nil {
		return fmt.Errorf("read native rows: %w", err)
	}

	appendable := appends && m.committed

	if !tail.Full {
		appendErr := m.appendTail(ctx, appender, tail, encoded)
		if appendErr == nil || errors.Is(appendErr, errInvalidRow) {
			return appendErr
		}

		// The base check refuses an append to a generation the mirror does
		// not hold; republishing the whole history repairs either case.
		appendable = false

		if tail, err = source(Cursor{}); err != nil {
			return fmt.Errorf("read native rows: %w", err)
		}
	}

	return m.commitFull(ctx, appender, appendable, tail, encoded)
}

// Hydrate reconciles the native JSONL file at path with the rows the store
// holds for the session before a load or resume, as sessionlog.Reconcile
// decides. Native rows that extend the stored rows are committed with record;
// a missing or shorter native file is rewritten from the store. validate,
// when set, sees the reconciled history before either happens. stored and
// record MUST be the generation Load returned from this mirror's store. It
// returns the rows the session now holds.
func (m *Mirror) Hydrate(ctx context.Context, path string, stored [][]byte, record any, validate func(rows [][]byte) error) ([][]byte, error) {
	encoded, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("encode session record: %w", err)
	}

	native, err := ReadJSONL(path, Cursor{})
	if err != nil {
		return nil, fmt.Errorf("read native rows: %w", err)
	}

	rows, nativeWins, err := Reconcile(native.Rows, stored)
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

	m.forget()
	m.committed = true
	m.record = encoded
	m.extend(stored)

	if nativeWins {
		if len(rows) > len(stored) {
			appender, appends := m.store.(acpcore.SessionStoreAppender)
			if commitErr := m.commitFull(ctx, appender, appends, Tail{Rows: rows, Full: true}, encoded); commitErr != nil {
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

// appendTail appends a continuation of the committed rows.
func (m *Mirror) appendTail(ctx context.Context, appender acpcore.SessionStoreAppender, tail Tail, encoded []byte) error {
	if err := validRows(tail.Rows); err != nil {
		return err
	}

	if len(tail.Rows) == 0 && bytes.Equal(encoded, m.record) {
		m.cursor = tail.Next

		return nil
	}

	if err := m.append(ctx, appender, tail.Rows, encoded); err != nil {
		return err
	}

	m.extend(tail.Rows)
	m.record = encoded
	m.cursor = tail.Next

	return nil
}

// commitFull publishes a complete history: skipped when the store already
// holds it, appended when it extends the committed rows and appendable is set,
// otherwise republished. A failure keeps what the mirror remembers, since the
// store keeps its previous generation.
func (m *Mirror) commitFull(ctx context.Context, appender acpcore.SessionStoreAppender, appendable bool, tail Tail, encoded []byte) error {
	if err := validRows(tail.Rows); err != nil {
		return err
	}

	if m.committed && len(tail.Rows) < m.rows {
		return fmt.Errorf("native log shrank from %d to %d rows", m.rows, len(tail.Rows))
	}

	if m.committed && m.extendsCommitted(tail.Rows) {
		added := tail.Rows[m.rows:]
		if len(added) == 0 && bytes.Equal(encoded, m.record) {
			m.cursor = tail.Next

			return nil
		}

		if appendable && m.append(ctx, appender, added, encoded) == nil {
			m.extend(added)
			m.record = encoded
			m.cursor = tail.Next

			return nil
		}
	}

	if err := m.replace(ctx, tail.Rows, encoded); err != nil {
		return err
	}

	m.forget()
	m.committed = true
	m.record = encoded
	m.cursor = tail.Next
	m.extend(tail.Rows)

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

func (m *Mirror) append(ctx context.Context, appender acpcore.SessionStoreAppender, rows [][]byte, encoded []byte) error {
	ctx, cancel := context.WithTimeout(ctx, acpcore.SessionStoreTimeout)
	defer cancel()

	var replacements []acpcore.SessionStoreReplacement
	if !bytes.Equal(encoded, m.record) {
		replacements = []acpcore.SessionStoreReplacement{m.configReplacement(encoded)}
	}

	return appender.Append(ctx, acpcore.SessionKey{SessionID: m.sessionID}, m.rows, entries(rows), replacements)
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
	m.record = nil
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
