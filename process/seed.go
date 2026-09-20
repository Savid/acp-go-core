package process

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	// seedManifestFileName lists the relative paths the adapter manages inside
	// a seed root so seed writes never clobber an operator-authored file.
	seedManifestFileName = ".seed-manifest.json"
)

// SeedFileError reports an invalid or unwritable seed file; the root package
// maps it to the uniform unsupported error naming seedFiles.
type SeedFileError struct {
	Name string
}

func (e *SeedFileError) Error() string {
	return fmt.Sprintf("invalid seed file %q", e.Name)
}

// SeedFileFlag collects repeatable -seed-file <relpath>=<hostpath> values,
// reading each host file's contents into Files keyed by the relative path.
type SeedFileFlag struct {
	Files map[string]string
}

func (s *SeedFileFlag) String() string {
	if s == nil || len(s.Files) == 0 {
		return ""
	}

	return strings.Join(slices.Sorted(maps.Keys(s.Files)), ",")
}

// Set parses one flag value and reads the named host file.
func (s *SeedFileFlag) Set(value string) error {
	relPath, hostPath, ok := strings.Cut(value, "=")

	relPath = strings.TrimSpace(relPath)
	hostPath = strings.TrimSpace(hostPath)

	if !ok || relPath == "" || hostPath == "" {
		return fmt.Errorf("invalid -seed-file %q: expected <relpath>=<hostpath>", value)
	}

	contents, err := os.ReadFile(hostPath)
	if err != nil {
		return fmt.Errorf("read seed file %q: %w", hostPath, err)
	}

	if s.Files == nil {
		s.Files = make(map[string]string)
	}

	s.Files[relPath] = string(contents)

	return nil
}

// WriteSeedFiles writes each file into dir under an ownership manifest so the
// adapter never overwrites a file it did not create: a pre-existing unmanaged
// target fails closed before anything is written, and every new path is
// recorded in the manifest before its file exists, so an interrupted write
// leaves only managed files behind. Every path is opened relative to dir with
// os.Root, and a symlink at a seed name is refused, so no write can leave the
// seed root.
func WriteSeedFiles(dir string, files map[string]string) error {
	if len(files) == 0 {
		return nil
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create native home: %w", err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open native home: %w", err)
	}
	defer root.Close()

	names := slices.Sorted(func(yield func(string) bool) {
		for name := range files {
			if !yield(name) {
				return
			}
		}
	})

	manifest, err := loadSeedManifest(root)
	if err != nil {
		return err
	}

	type target struct {
		name   string
		path   string
		exists bool
	}

	targets := make([]target, 0, len(names))

	for _, name := range names {
		if !validSeedFilePath(name) {
			return &SeedFileError{Name: name}
		}

		path := filepath.FromSlash(name)

		info, statErr := root.Lstat(path)
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("stat seed file: %w", statErr)
		}

		exists := statErr == nil
		if exists && !info.Mode().IsRegular() {
			return &SeedFileError{Name: name}
		}

		if _, managed := manifest[filepath.ToSlash(name)]; exists && !managed {
			return &SeedFileError{Name: name}
		}

		targets = append(targets, target{name: name, path: path, exists: exists})
	}

	added := false

	for _, item := range targets {
		if _, managed := manifest[filepath.ToSlash(item.name)]; !managed {
			manifest[filepath.ToSlash(item.name)] = struct{}{}
			added = true
		}
	}

	if added {
		if err := writeSeedManifest(root, manifest); err != nil {
			return err
		}
	}

	for _, item := range targets {
		contents := []byte(files[item.name])

		if item.exists {
			current, readErr := root.ReadFile(item.path)
			if readErr != nil {
				return fmt.Errorf("read managed seed file: %w", readErr)
			}

			if bytes.Equal(current, contents) {
				continue
			}
		}

		if parent := filepath.Dir(item.path); parent != "." {
			if err := root.MkdirAll(parent, 0o700); err != nil {
				return fmt.Errorf("create seed file directory: %w", err)
			}
		}

		if err := root.WriteFile(item.path, contents, 0o600); err != nil {
			return fmt.Errorf("write seed file: %w", err)
		}
	}

	return nil
}

func loadSeedManifest(root *os.Root) (map[string]struct{}, error) {
	data, err := root.ReadFile(seedManifestFileName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return make(map[string]struct{}), nil
		}

		return nil, fmt.Errorf("read seed manifest: %w", err)
	}

	var entries []string
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("decode seed manifest: %w", err)
	}

	manifest := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		manifest[entry] = struct{}{}
	}

	return manifest, nil
}

// writeSeedManifest publishes the manifest atomically: a private staging file
// is written and synced, then renamed over the manifest, so a crash leaves
// either the previous manifest or the complete new one.
func writeSeedManifest(root *os.Root, manifest map[string]struct{}) error {
	entries := slices.Sorted(func(yield func(string) bool) {
		for entry := range manifest {
			if !yield(entry) {
				return
			}
		}
	})

	// A string slice cannot fail to marshal.
	data, _ := json.Marshal(entries)

	staging := seedManifestFileName + "." + rand.Text()

	file, err := root.OpenFile(staging, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("stage seed manifest: %w", err)
	}

	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}

	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}

	if writeErr == nil {
		writeErr = root.Rename(staging, seedManifestFileName)
	}

	if writeErr != nil {
		_ = root.Remove(staging)

		return fmt.Errorf("write seed manifest: %w", writeErr)
	}

	if directory, openErr := root.Open("."); openErr == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}

	return nil
}

func validSeedFilePath(name string) bool {
	cleanName := filepath.Clean(filepath.FromSlash(name))

	if strings.TrimSpace(name) == "" ||
		filepath.IsAbs(name) ||
		strings.HasPrefix(name, "/") ||
		strings.Contains(name, "\x00") ||
		cleanName == seedManifestFileName ||
		strings.HasPrefix(cleanName, seedManifestFileName+".") {
		return false
	}

	for part := range strings.SplitSeq(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}

	return true
}
