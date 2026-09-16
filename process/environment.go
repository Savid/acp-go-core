// Package process launches a harness as a plain child: the merged environment,
// the session cwd, its own process group, and three dedicated pipes.
package process

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Environment is the merge that produces a harness environment. Later layers
// win. Nothing is scrubbed except inherited names under InternalPrefix; the
// Owned layer is applied after that drop, so the sibling's own markers reach
// the child.
type Environment struct {
	// Process is the sibling's own environment, in KEY=value form, read once at
	// construction.
	Process []string
	// Agent is the static host overlay from WithEnv.
	Agent map[string]string
	// Session is the per-session overlay from the session's env option.
	Session map[string]string
	// Owned are the keys the sibling sets because of how it launches the harness,
	// such as the home variable.
	Owned map[string]string
	// InternalPrefix names the sibling's private process markers, dropped from
	// every layer before the merge.
	InternalPrefix string
	// ExtraPathDirs are prepended, in order, to the PATH the merge produced.
	ExtraPathDirs []string
}

// NameError reports an environment entry that cannot be a variable.
type NameError struct {
	Key string
}

func (e *NameError) Error() string {
	return fmt.Sprintf("environment key %q is not a variable name", e.Key)
}

// PathDirError reports an extra path directory that is not an absolute
// separator-free path.
type PathDirError struct {
	Index int
	Dir   string
}

func (e *PathDirError) Error() string {
	return fmt.Sprintf("extra path directory %d %q is not an absolute directory", e.Index, e.Dir)
}

// ValidateNames refuses a map whose entries could not be environment entries:
// an empty name, a name containing = or NUL, or a value containing NUL. Keys
// are checked in sorted order so the first refusal is deterministic.
func ValidateNames(env map[string]string) error {
	for _, key := range slices.Sorted(maps.Keys(env)) {
		if !validName(key) || strings.ContainsRune(env[key], '\x00') {
			return &NameError{Key: key}
		}
	}

	return nil
}

func validName(key string) bool {
	return key != "" && !strings.ContainsAny(key, "=\x00")
}

// ValidateOptionalAbsolutePath accepts an unset path and refuses a relative
// one. It guards the home, scratch, and executable path options.
func ValidateOptionalAbsolutePath(path string) error {
	if path == "" || filepath.IsAbs(path) {
		return nil
	}

	return errors.New("path must be absolute")
}

// ValidateExtraPathDirs refuses an entry that is empty, relative, or contains
// the platform list separator.
func ValidateExtraPathDirs(dirs []string) error {
	for index, dir := range dirs {
		if dir == "" || !filepath.IsAbs(dir) || strings.ContainsRune(dir, os.PathListSeparator) {
			return &PathDirError{Index: index, Dir: dir}
		}
	}

	return nil
}

// Build merges the layers into the KEY=value list the child receives, sorted by
// key. PATH is composed last from ExtraPathDirs and the merged PATH.
func (e Environment) Build() ([]string, error) {
	for _, layer := range []map[string]string{e.Agent, e.Session, e.Owned} {
		if err := ValidateNames(layer); err != nil {
			return nil, err
		}
	}

	if err := ValidateExtraPathDirs(e.ExtraPathDirs); err != nil {
		return nil, err
	}

	values := parse(e.Process)
	for _, layer := range []map[string]string{e.Agent, e.Session} {
		for _, key := range slices.Sorted(maps.Keys(layer)) {
			values[key] = layer[key]
		}
	}

	if e.InternalPrefix != "" {
		for key := range values {
			if strings.HasPrefix(key, e.InternalPrefix) {
				delete(values, key)
			}
		}
	}

	for _, key := range slices.Sorted(maps.Keys(e.Owned)) {
		values[key] = e.Owned[key]
	}

	if _, present := values["PATH"]; present || len(e.ExtraPathDirs) > 0 {
		values["PATH"] = composePath(e.ExtraPathDirs, values["PATH"])
	}

	return list(values), nil
}

// Base is the environment executable resolution and version probing use: the
// process environment and the agent overlay, with internal markers dropped and
// no session or owned keys applied.
func (e Environment) Base() ([]string, error) {
	base := Environment{Process: e.Process, Agent: e.Agent, InternalPrefix: e.InternalPrefix}

	return base.Build()
}

func composePath(dirs []string, base string) string {
	parts := make([]string, 0, len(dirs)+1)
	parts = append(parts, dirs...)

	for component := range strings.SplitSeq(base, string(os.PathListSeparator)) {
		if component != "" {
			parts = append(parts, component)
		}
	}

	return strings.Join(parts, string(os.PathListSeparator))
}

func parse(entries []string) map[string]string {
	values := make(map[string]string, len(entries))

	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			values[key] = value
		}
	}

	return values
}

func list(values map[string]string) []string {
	keys := slices.Sorted(maps.Keys(values))
	entries := make([]string, 0, len(keys))

	for _, key := range keys {
		entries = append(entries, key+"="+values[key])
	}

	return entries
}

// Lookup reads one variable from a KEY=value list.
func Lookup(env []string, key string) (string, bool) {
	value, ok := parse(env)[key]

	return value, ok
}

// ResolveExecutable resolves the configured executable selector against env. A
// name containing a path separator is used as given; a bare name is searched on
// env's PATH. The result is an existing regular file with an execute bit.
func ResolveExecutable(path string, env []string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("executable path is empty")
	}

	if strings.ContainsRune(path, filepath.Separator) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}

		return candidate(absolute)
	}

	search, _ := Lookup(env, "PATH")
	if search == "" {
		return "", fmt.Errorf("find %s: PATH is empty", path)
	}

	for directory := range strings.SplitSeq(search, string(os.PathListSeparator)) {
		if directory == "" {
			directory = "."
		}

		absolute, err := filepath.Abs(filepath.Join(directory, path))
		if err != nil {
			return "", err
		}

		resolved, err := candidate(absolute)
		if err == nil {
			return resolved, nil
		}

		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, exec.ErrNotFound) {
			return "", err
		}
	}

	return "", fmt.Errorf("find %s in PATH: %w", path, exec.ErrNotFound)
}

func candidate(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}

	if info.IsDir() || info.Mode()&0o111 == 0 {
		return "", exec.ErrNotFound
	}

	return path, nil
}
