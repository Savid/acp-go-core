package acpcore

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// SessionStoreMainSubpath addresses a session's main record.
const SessionStoreMainSubpath = ""

// ErrSessionIDRequired reports a write addressed to an empty session id.
var ErrSessionIDRequired = errors.New("session id is required")

// SessionStoreEntry is one JSON object in a session's main record or one of its
// subrecords. Implementations preserve the raw bytes; the sibling validates
// entries before publishing them.
type SessionStoreEntry = json.RawMessage

// SessionKey addresses one session-store record.
type SessionKey struct {
	// SessionID is the ACP-visible session ID being stored.
	SessionID string
	// Subpath is empty for the main record or names a session-owned subrecord.
	Subpath string
}

// SessionSummary is a lightweight entry returned by session-store listers.
type SessionSummary struct {
	SessionID          string
	UpdatedAtUnixMilli int64
}

// SessionStoreReplacement is one record written during an atomic replace.
type SessionStoreReplacement struct {
	Key     SessionKey
	Entries []SessionStoreEntry
}

// SessionStore is the durability boundary a host provides. Replace publishes
// one complete generation and is durable before it returns; it copies the
// entries it receives, so the caller keeps them. Load atomically reads every
// live subpath of one session; the returned map, slices, and bytes belong to
// the caller.
type SessionStore interface {
	Load(ctx context.Context, sessionID string) (map[string][]SessionStoreEntry, error)
	Replace(ctx context.Context, main SessionKey, replacements []SessionStoreReplacement) error
	Delete(ctx context.Context, key SessionKey) error
	ListSessions(ctx context.Context) ([]SessionSummary, error)
}

// InMemorySessionStore is the default store when a host provides none, and the
// reference implementation the store contract battery is written against.
type InMemorySessionStore struct {
	mu         sync.Mutex
	entries    map[SessionKey][]SessionStoreEntry
	updatedAt  map[SessionKey]int64
	tombstones map[SessionKey]int64
}

var _ SessionStore = (*InMemorySessionStore)(nil)

// NewInMemorySessionStore creates an empty process-local store.
func NewInMemorySessionStore() *InMemorySessionStore {
	return &InMemorySessionStore{
		entries:    make(map[SessionKey][]SessionStoreEntry),
		updatedAt:  make(map[SessionKey]int64),
		tombstones: make(map[SessionKey]int64),
	}
}

// Load returns one complete session generation keyed by subpath. A live empty
// main record is present under the empty key; a missing session returns nil.
func (s *InMemorySessionStore) Load(ctx context.Context, sessionID string) (map[string][]SessionStoreEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if sessionID == "" || s.isTombstonedLocked(SessionKey{SessionID: sessionID}) {
		return nil, nil //nolint:nilnil // Missing or tombstoned sessions have no generation.
	}

	var generation map[string][]SessionStoreEntry

	for key, entries := range s.entries {
		if key.SessionID != sessionID || s.isTombstonedLocked(key) {
			continue
		}

		if generation == nil {
			generation = make(map[string][]SessionStoreEntry)
		}

		generation[key.Subpath] = cloneEntries(entries)
	}

	return generation, nil
}

// Replace atomically publishes one session's complete generation.
func (s *InMemorySessionStore) Replace(ctx context.Context, main SessionKey, replacements []SessionStoreReplacement) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if main.SessionID == "" {
		return ErrSessionIDRequired
	}

	if main.Subpath != SessionStoreMainSubpath {
		return fmt.Errorf("main subpath must be %q", SessionStoreMainSubpath)
	}

	if err := validateReplacements(main, replacements); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// A tombstone this write did not create is final. The store enforces that
	// itself rather than trusting a sibling-level deletion marker.
	if s.isTombstonedLocked(main) {
		return nil
	}

	now := time.Now().UnixMilli()

	for candidate := range s.entries {
		if candidate.SessionID == main.SessionID {
			delete(s.entries, candidate)
			delete(s.updatedAt, candidate)
			s.tombstones[candidate] = now
		}
	}

	for _, replacement := range replacements {
		s.entries[replacement.Key] = cloneEntries(replacement.Entries)
		s.updatedAt[replacement.Key] = now
		delete(s.tombstones, replacement.Key)
	}

	return nil
}

// validateReplacements checks the whole set before any key is written: every
// key names the addressed session, no key appears twice, and the main key
// appears exactly once.
func validateReplacements(main SessionKey, replacements []SessionStoreReplacement) error {
	mainCount := 0
	seen := make(map[SessionKey]struct{}, len(replacements))

	for _, replacement := range replacements {
		if replacement.Key.SessionID != main.SessionID {
			return fmt.Errorf("replacement key %s does not belong to session %q",
				keyLabel(replacement.Key), main.SessionID)
		}

		if _, duplicate := seen[replacement.Key]; duplicate {
			return fmt.Errorf("duplicate replacement key %s", keyLabel(replacement.Key))
		}

		seen[replacement.Key] = struct{}{}

		if replacement.Key.Subpath == SessionStoreMainSubpath {
			mainCount++
		}
	}

	if mainCount != 1 {
		return errors.New("replacements must include the main key exactly once")
	}

	return nil
}

// ListSessions lists committed, non-tombstoned main sessions, newest first.
func (s *InMemorySessionStore) ListSessions(ctx context.Context) ([]SessionSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	summaries := make([]SessionSummary, 0)

	for key := range s.entries {
		if key.SessionID == "" || key.Subpath != SessionStoreMainSubpath || s.isTombstonedLocked(key) {
			continue
		}

		summaries = append(summaries, SessionSummary{
			SessionID:          key.SessionID,
			UpdatedAtUnixMilli: s.updatedAt[key],
		})
	}

	slices.SortFunc(summaries, func(left, right SessionSummary) int {
		if byTime := cmp.Compare(right.UpdatedAtUnixMilli, left.UpdatedAtUnixMilli); byTime != 0 {
			return byTime
		}

		return strings.Compare(left.SessionID, right.SessionID)
	})

	return summaries, nil
}

// Delete writes a tombstone. Deleting the main key cascades to subpaths.
// Deleting a key with an empty SessionID is a no-op.
func (s *InMemorySessionStore) Delete(ctx context.Context, key SessionKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if key.SessionID == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UnixMilli()
	matched := false

	for candidate := range s.entries {
		if candidate.SessionID != key.SessionID {
			continue
		}

		if key.Subpath != SessionStoreMainSubpath && candidate.Subpath != key.Subpath {
			continue
		}

		delete(s.entries, candidate)
		delete(s.updatedAt, candidate)
		s.tombstones[candidate] = now
		matched = true
	}

	if !matched {
		s.tombstones[key] = now
	}

	if key.Subpath == SessionStoreMainSubpath {
		s.tombstones[mainKey(key.SessionID)] = now
	}

	return nil
}

func keyLabel(key SessionKey) string {
	return fmt.Sprintf("{sessionId:%q, subpath:%q}", key.SessionID, key.Subpath)
}

func cloneEntry(entry SessionStoreEntry) SessionStoreEntry {
	return append(SessionStoreEntry(nil), entry...)
}

func cloneEntries(entries []SessionStoreEntry) []SessionStoreEntry {
	if len(entries) == 0 {
		return nil
	}

	clone := make([]SessionStoreEntry, 0, len(entries))
	for _, entry := range entries {
		clone = append(clone, cloneEntry(entry))
	}

	return clone
}

func (s *InMemorySessionStore) isTombstonedLocked(key SessionKey) bool {
	if _, ok := s.tombstones[key]; ok {
		return true
	}

	if key.Subpath != SessionStoreMainSubpath {
		_, ok := s.tombstones[mainKey(key.SessionID)]

		return ok
	}

	return false
}

func mainKey(sessionID string) SessionKey {
	return SessionKey{SessionID: sessionID, Subpath: SessionStoreMainSubpath}
}
