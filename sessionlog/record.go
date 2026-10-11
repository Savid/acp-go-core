// Package sessionlog mirrors native rows and their session configuration as
// one durable store generation, and reads and writes append-only native JSONL
// files.
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

// Load reads the native rows and decodes the required configuration into
// record. found is false when the session is missing or tombstoned; a found
// session with no rows is a committed conversation whose native history is
// still empty. The rows are the store's caller-owned copies.
func Load(ctx context.Context, store acpcore.SessionStore, sessionID string, record any) (rows [][]byte, found bool, err error) {
	generation, err := loadGeneration(ctx, store, sessionID, record)
	if err != nil || generation == nil {
		return nil, false, err
	}

	entries := generation[acpcore.SessionStoreMainSubpath]

	rows = make([][]byte, len(entries))
	for index, entry := range entries {
		if !validRow(entry) {
			return nil, false, fmt.Errorf("invalid native row %d", index)
		}

		rows[index] = entry
	}

	return rows, true, nil
}

// LoadRecord decodes only the required configuration into record, for callers
// such as session listing that need no native rows.
func LoadRecord(ctx context.Context, store acpcore.SessionStore, sessionID string, record any) (found bool, err error) {
	generation, err := loadGeneration(ctx, store, sessionID, record)

	return generation != nil && err == nil, err
}

// loadGeneration reads one generation and decodes its configuration into
// record; it returns nil for a missing or tombstoned session.
func loadGeneration(ctx context.Context, store acpcore.SessionStore, sessionID string, record any) (map[string][]acpcore.SessionStoreEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, acpcore.SessionStoreTimeout)
	defer cancel()

	generation, err := store.Load(ctx, sessionID)
	if err != nil || generation == nil {
		return nil, err
	}

	if _, present := generation[acpcore.SessionStoreMainSubpath]; !present {
		return nil, errors.New("session requires a main record")
	}

	records := generation[ConfigSubpath]
	if len(records) != 1 {
		return nil, errors.New("session requires one configuration record")
	}

	if err := decodeRecord(records[0], record); err != nil {
		return nil, fmt.Errorf("decode session record: %w", err)
	}

	return generation, nil
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
