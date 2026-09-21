// Package sessionlog mirrors native JSONL rows and their session configuration
// as one durable store generation.
package sessionlog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	acpcore "github.com/savid/acp-go-core"
)

// ConfigSubpath holds the current session configuration.
const ConfigSubpath = "config"

// Commit publishes the native rows and configuration atomically. Failure
// leaves the previous generation intact, including its configuration.
func Commit(ctx context.Context, store acpcore.SessionStore, sessionID string, rows [][]byte, record any) error {
	ctx, cancel := context.WithTimeout(ctx, acpcore.SessionStoreTimeout)
	defer cancel()

	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode session record: %w", err)
	}

	entries := make([]acpcore.SessionStoreEntry, len(rows))
	for index, row := range rows {
		if !validRow(row) {
			return fmt.Errorf("invalid native row %d", index)
		}

		entries[index] = row
	}

	main := acpcore.SessionKey{SessionID: sessionID}

	return store.Replace(ctx, main, []acpcore.SessionStoreReplacement{
		{Key: main, Entries: entries},
		{Key: acpcore.SessionKey{SessionID: sessionID, Subpath: ConfigSubpath}, Entries: []acpcore.SessionStoreEntry{encoded}},
	})
}

// Load reads the native rows and decodes the required configuration into
// record. found is false when the session is missing or tombstoned; a found
// session with no rows is a committed conversation whose native history is
// still empty.
func Load(ctx context.Context, store acpcore.SessionStore, sessionID string, record any) (rows [][]byte, found bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, acpcore.SessionStoreTimeout)
	defer cancel()

	generation, err := store.Load(ctx, sessionID)
	if err != nil || generation == nil {
		return nil, false, err
	}

	entries, present := generation[acpcore.SessionStoreMainSubpath]
	if !present {
		return nil, false, errors.New("session requires a main record")
	}

	records := generation[ConfigSubpath]
	if len(records) != 1 {
		return nil, false, errors.New("session requires one configuration record")
	}

	if err := decodeRecord(records[0], record); err != nil {
		return nil, false, fmt.Errorf("decode session record: %w", err)
	}

	rows = make([][]byte, len(entries))
	for index, entry := range entries {
		if !validRow(entry) {
			return nil, false, fmt.Errorf("invalid native row %d", index)
		}

		rows[index] = bytes.Clone(entry)
	}

	return rows, true, nil
}

func validRow(row []byte) bool {
	trimmed := bytes.TrimSpace(row)

	return len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(trimmed)
}

func decodeRecord(data []byte, record any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	if err := uniqueValue(decoder, 0); err != nil {
		return err
	}

	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(record); err != nil {
		return err
	}

	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing session record data")
	}

	return nil
}

// maxRecordDepth bounds record nesting so a hostile record cannot exhaust
// the stack; it matches the depth encoding/json accepts.
const maxRecordDepth = 10000

func uniqueValue(decoder *json.Decoder, depth int) error {
	if depth > maxRecordDepth {
		return errors.New("record nesting exceeds its bound")
	}

	token, err := decoder.Token()
	if err != nil {
		return err
	}

	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}

	seen := make(map[string]struct{})

	for decoder.More() {
		if delimiter == '{' {
			key, keyErr := decoder.Token()
			if keyErr != nil {
				return keyErr
			}

			name, ok := key.(string)
			if !ok {
				return errors.New("object key is not a string")
			}

			if _, duplicate := seen[name]; duplicate {
				return fmt.Errorf("duplicate record key %q", name)
			}

			seen[name] = struct{}{}
		}

		if valueErr := uniqueValue(decoder, depth+1); valueErr != nil {
			return valueErr
		}
	}

	_, err = decoder.Token()

	return err
}

// Reconcile returns the longer log when both agree at every shared position
// and reports whether the native log is the one to keep. The native log may
// contain rows written outside ACP; a shorter native log is replaced by the
// store's copy.
func Reconcile(native, stored [][]byte) (rows [][]byte, nativeWins bool, err error) {
	for index := 0; index < len(native) && index < len(stored); index++ {
		if !bytes.Equal(native[index], stored[index]) {
			return nil, false, fmt.Errorf("native row %d disagrees with the store", index)
		}
	}

	if len(native) >= len(stored) {
		return native, true, nil
	}

	return stored, false, nil
}
