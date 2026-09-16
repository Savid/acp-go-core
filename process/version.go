package process

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Executable caches a resolved binary and its completed version verdict. The
// zero value is ready. Every call MUST describe the same binary configuration.
// Interrupted probes are retried by the next caller.
type Executable struct {
	mu       sync.Mutex
	running  chan struct{}
	complete bool
	path     string
	err      error
}

// Resolve serializes version probes without tying waiting callers to the
// probing caller's cancellation. Probe commands and version floors belong to
// the caller; resolution and completed-verdict caching are shared.
func (e *Executable) Resolve(ctx context.Context, env Environment, selector, name, minimum string, probe func(context.Context, string, []string) (string, error)) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		e.mu.Lock()
		if e.complete {
			path, err := e.path, e.err
			e.mu.Unlock()

			return path, err
		}

		if running := e.running; running != nil {
			e.mu.Unlock()

			select {
			case <-running:
				continue
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}

		e.running = make(chan struct{})
		e.mu.Unlock()

		path, complete, err := probeExecutable(ctx, env, selector, name, minimum, probe)

		e.mu.Lock()
		e.path, e.complete, e.err = path, complete, err
		close(e.running)
		e.running = nil
		e.mu.Unlock()

		return path, err
	}
}

func probeExecutable(ctx context.Context, env Environment, selector, name, minimum string, probe func(context.Context, string, []string) (string, error)) (string, bool, error) {
	base, err := env.Base()
	if err != nil {
		return "", false, err
	}

	if selector == "" {
		selector = name
	}

	path, err := ResolveExecutable(selector, base)
	if err != nil {
		return "", true, err
	}

	version, err := probe(ctx, path, base)
	if err != nil {
		return "", false, err
	}

	if err := ctx.Err(); err != nil {
		return "", false, err
	}

	if err := checkMinimumVersion(name, version, minimum); err != nil {
		return "", true, err
	}

	return path, true, nil
}

// checkMinimumVersion fails when a harness version sorts below the minimum
// the sibling was verified against. name labels the harness in the error.
func checkMinimumVersion(name, version, minimum string) error {
	comparison, err := compareVersions(version, minimum)
	if err != nil {
		return err
	}

	if comparison < 0 {
		return fmt.Errorf("%s version %s is below the minimum supported version %s", name, version, minimum)
	}

	return nil
}

func compareVersions(left, right string) (int, error) {
	leftParts, err := versionParts(left)
	if err != nil {
		return 0, err
	}

	rightParts, err := versionParts(right)
	if err != nil {
		return 0, err
	}

	for index := range max(len(leftParts), len(rightParts)) {
		leftValue := 0
		if index < len(leftParts) {
			leftValue = leftParts[index]
		}

		rightValue := 0
		if index < len(rightParts) {
			rightValue = rightParts[index]
		}

		if leftValue != rightValue {
			if leftValue < rightValue {
				return -1, nil
			}

			return 1, nil
		}
	}

	return 0, nil
}

func versionParts(version string) ([]int, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if release, _, found := strings.Cut(trimmed, "-"); found {
		trimmed = release
	}

	if trimmed == "" {
		return nil, fmt.Errorf("invalid version %q", version)
	}

	segments := strings.Split(trimmed, ".")
	parts := make([]int, 0, len(segments))

	for _, segment := range segments {
		value, err := strconv.Atoi(segment)
		if err != nil || value < 0 {
			return nil, fmt.Errorf("invalid version %q", version)
		}

		parts = append(parts, value)
	}

	return parts, nil
}
