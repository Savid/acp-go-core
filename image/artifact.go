package image

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	acpcore "github.com/savid/acp-go-core"
)

// ArtifactPrefix is the subpath prefix every stored artifact lives under.
const ArtifactPrefix = "images/"

// ArtifactTTL bounds how long an artifact stays replayable.
const ArtifactTTL = 24 * time.Hour

const artifactVersion = 1

// Artifact is one stored image output.
type Artifact struct {
	Version            int    `json:"version"`
	NativeID           string `json:"nativeId"`
	Fingerprint        string `json:"fingerprint"`
	MimeType           string `json:"mimeType"`
	Data               string `json:"data"`
	CreatedAtUnixMilli int64  `json:"createdAtUnixMilli"`
}

// ArtifactStore keeps emitted image output under a session in the session
// store, keyed by native identity plus content fingerprint, for replay.
type ArtifactStore struct {
	store     acpcore.SessionStore
	sessionID string
	now       func() time.Time
	mu        sync.Mutex
}

// NewArtifactStore binds the store for one session.
func NewArtifactStore(store acpcore.SessionStore, sessionID string) *ArtifactStore {
	return &ArtifactStore{store: store, sessionID: sessionID, now: time.Now}
}

// Subpath is the store subpath for one artifact.
func Subpath(nativeID string, data []byte) string {
	fingerprint := sha256.Sum256(data)
	idHash := sha256.Sum256([]byte(nativeID))

	return ArtifactPrefix + hex.EncodeToString(idHash[:8]) + "/" + hex.EncodeToString(fingerprint[:]) + ".json"
}

// Store persists one artifact and returns its subpath. Storing the same bytes
// under the same native id twice is idempotent; different bytes under an
// existing subpath is a conflict.
func (s *ArtifactStore) Store(ctx context.Context, nativeID string, data []byte, mimeType string) (string, error) {
	fingerprint := sha256.Sum256(data)
	subpath := Subpath(nativeID, data)
	record := Artifact{
		Version:            artifactVersion,
		NativeID:           nativeID,
		Fingerprint:        hex.EncodeToString(fingerprint[:]),
		MimeType:           mimeType,
		Data:               base64.StdEncoding.EncodeToString(data),
		CreatedAtUnixMilli: s.now().UnixMilli(),
	}

	entry, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("encode image artifact: %w", err)
	}

	key := acpcore.SessionKey{SessionID: s.sessionID, Subpath: subpath}

	// Serialize sweep, load, and append so concurrent handlers storing the same
	// native id never write the key twice.
	s.mu.Lock()
	defer s.mu.Unlock()

	if sweepErr := s.Sweep(ctx); sweepErr != nil {
		return "", sweepErr
	}

	existing, err := s.store.Load(ctx, key)
	if err != nil {
		return "", err
	}

	if len(existing) > 0 {
		var stored Artifact
		if len(existing) != 1 || json.Unmarshal(existing[0], &stored) != nil ||
			stored.Fingerprint != record.Fingerprint || stored.MimeType != record.MimeType || stored.Data != record.Data {
			return "", errors.New("stored image artifact conflicts with native identity")
		}

		return subpath, nil
	}

	if err := s.store.Append(ctx, key, []acpcore.SessionStoreEntry{entry}); err != nil {
		return "", err
	}

	return subpath, nil
}

// Load reads and verifies one artifact. An expired artifact is deleted and
// reported as an error.
func (s *ArtifactStore) Load(ctx context.Context, subpath string) (Artifact, error) {
	if !strings.HasPrefix(subpath, ArtifactPrefix) {
		return Artifact{}, errors.New("image artifact reference is invalid")
	}

	key := acpcore.SessionKey{SessionID: s.sessionID, Subpath: subpath}

	entries, err := s.store.Load(ctx, key)
	if err != nil {
		return Artifact{}, err
	}

	if len(entries) != 1 {
		return Artifact{}, errors.New("image artifact bytes are unavailable")
	}

	var artifact Artifact
	if decodeErr := json.Unmarshal(entries[0], &artifact); decodeErr != nil {
		return Artifact{}, fmt.Errorf("decode image artifact: %w", decodeErr)
	}

	if artifact.Version != artifactVersion || artifact.Data == "" || artifact.MimeType == "" || artifact.CreatedAtUnixMilli <= 0 {
		return Artifact{}, errors.New("image artifact record is incomplete")
	}

	if expired(artifact.CreatedAtUnixMilli, s.now()) {
		if deleteErr := s.store.Delete(ctx, key); deleteErr != nil {
			return Artifact{}, fmt.Errorf("delete expired image artifact: %w", deleteErr)
		}

		return Artifact{}, errors.New("image artifact bytes expired")
	}

	data, err := base64.StdEncoding.DecodeString(artifact.Data)
	if err != nil {
		return Artifact{}, fmt.Errorf("decode image artifact bytes: %w", err)
	}

	fingerprint := sha256.Sum256(data)
	if hex.EncodeToString(fingerprint[:]) != artifact.Fingerprint {
		return Artifact{}, errors.New("image artifact checksum does not match")
	}

	return artifact, nil
}

// Sweep deletes every expired or corrupt artifact under the session.
func (s *ArtifactStore) Sweep(ctx context.Context) error {
	subpaths, err := s.store.ListSubkeys(ctx, acpcore.SessionKey{SessionID: s.sessionID})
	if err != nil {
		return err
	}

	now := s.now()

	for _, subpath := range subpaths {
		if !strings.HasPrefix(subpath, ArtifactPrefix) {
			continue
		}

		key := acpcore.SessionKey{SessionID: s.sessionID, Subpath: subpath}

		entries, err := s.store.Load(ctx, key)
		if err != nil {
			return err
		}

		stale := len(entries) != 1
		if !stale {
			var artifact Artifact

			stale = json.Unmarshal(entries[0], &artifact) != nil || expired(artifact.CreatedAtUnixMilli, now)
		}

		if !stale {
			continue
		}

		if err := s.store.Delete(ctx, key); err != nil {
			return err
		}
	}

	return nil
}

func expired(createdAtUnixMilli int64, now time.Time) bool {
	return createdAtUnixMilli <= 0 || !now.Before(time.UnixMilli(createdAtUnixMilli).Add(ArtifactTTL))
}
