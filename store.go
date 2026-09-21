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

// SessionStoreTimeout bounds one store call a sibling makes on a host's behalf.
const SessionStoreTimeout = 10 * time.Second

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
	mu       sync.Mutex
	sessions map[string]*storedSession
	// deleted holds every session id whose main record was deleted. A deleted
	// session never comes back: a later Replace is a no-op and Load reports it
	// missing, so a delete that races the session's first write wins.
	deleted map[string]struct{}
}

// storedSession is one session's live generation, keyed by subpath.
type storedSession struct {
	subpaths  map[string][]SessionStoreEntry
	updatedAt int64
}

var _ SessionStore = (*InMemorySessionStore)(nil)

// NewInMemorySessionStore creates an empty process-local store.
func NewInMemorySessionStore() *InMemorySessionStore {
	return &InMemorySessionStore{
		sessions: make(map[string]*storedSession),
		deleted:  make(map[string]struct{}),
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

	record := s.sessions[sessionID]
	if record == nil {
		return nil, nil //nolint:nilnil // A missing or deleted session has no generation.
	}

	generation := make(map[string][]SessionStoreEntry, len(record.subpaths))
	for subpath, entries := range record.subpaths {
		generation[subpath] = cloneEntries(entries)
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

	if _, gone := s.deleted[main.SessionID]; gone {
		return nil
	}

	record := &storedSession{subpaths: make(map[string][]SessionStoreEntry, len(replacements)), updatedAt: time.Now().UnixMilli()}
	for _, replacement := range replacements {
		record.subpaths[replacement.Key.Subpath] = cloneEntries(replacement.Entries)
	}

	s.sessions[main.SessionID] = record

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

// ListSessions lists committed sessions, newest first.
func (s *InMemorySessionStore) ListSessions(ctx context.Context) ([]SessionSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	summaries := make([]SessionSummary, 0, len(s.sessions))

	for sessionID, record := range s.sessions {
		summaries = append(summaries, SessionSummary{SessionID: sessionID, UpdatedAtUnixMilli: record.updatedAt})
	}

	slices.SortFunc(summaries, func(left, right SessionSummary) int {
		if byTime := cmp.Compare(right.UpdatedAtUnixMilli, left.UpdatedAtUnixMilli); byTime != 0 {
			return byTime
		}

		return strings.Compare(left.SessionID, right.SessionID)
	})

	return summaries, nil
}

// Delete removes one subrecord, or the whole session when the main key is
// named; a deleted session stays deleted. Deleting a key with an empty
// SessionID is a no-op.
func (s *InMemorySessionStore) Delete(ctx context.Context, key SessionKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if key.SessionID == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if key.Subpath == SessionStoreMainSubpath {
		delete(s.sessions, key.SessionID)
		s.deleted[key.SessionID] = struct{}{}

		return nil
	}

	if record := s.sessions[key.SessionID]; record != nil {
		delete(record.subpaths, key.Subpath)
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
