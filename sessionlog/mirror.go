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
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode session record: %w", err)
	}

	entries := make([]acpcore.SessionStoreEntry, len(rows))
	for index, row := range rows {
		entries[index] = row
	}

	main := acpcore.SessionKey{SessionID: sessionID}

	return store.Replace(ctx, main, []acpcore.SessionStoreReplacement{
		{Key: main, Entries: entries},
		{Key: acpcore.SessionKey{SessionID: sessionID, Subpath: ConfigSubpath}, Entries: []acpcore.SessionStoreEntry{encoded}},
	})
}

// Load reads the native rows and decodes the required configuration into
// record. No native rows means the session has no recoverable conversation.
func Load(ctx context.Context, store acpcore.SessionStore, sessionID string, record any) ([][]byte, error) {
	entries, err := store.Load(ctx, acpcore.SessionKey{SessionID: sessionID})
	if err != nil || len(entries) == 0 {
		return nil, err
	}

	records, err := store.Load(ctx, acpcore.SessionKey{SessionID: sessionID, Subpath: ConfigSubpath})
	if err != nil {
		return nil, err
	}

	if len(records) != 1 {
		return nil, errors.New("session requires one configuration record")
	}

	if err := decodeRecord(records[0], record); err != nil {
		return nil, fmt.Errorf("decode session record: %w", err)
	}

	rows := make([][]byte, len(entries))
	for index, entry := range entries {
		if !json.Valid(entry) {
			return nil, fmt.Errorf("invalid native row %d", index)
		}

		rows[index] = bytes.Clone(entry)
	}

	return rows, nil
}

func decodeRecord(data []byte, record any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	if err := uniqueValue(decoder); err != nil {
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

func uniqueValue(decoder *json.Decoder) error {
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

		if valueErr := uniqueValue(decoder); valueErr != nil {
			return valueErr
		}
	}

	_, err = decoder.Token()

	return err
}

// Reconcile returns the longer log when both agree at every shared position.
// The native log may contain rows written outside ACP.
func Reconcile(native, stored [][]byte) ([][]byte, error) {
	for index := 0; index < len(native) && index < len(stored); index++ {
		if !bytes.Equal(native[index], stored[index]) {
			return nil, fmt.Errorf("native row %d disagrees with the store", index)
		}
	}

	if len(native) >= len(stored) {
		return native, nil
	}

	return stored, nil
}
