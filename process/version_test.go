package process

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"
)

func TestExecutableCachesCompletedVerdicts(t *testing.T) {
	t.Parallel()
	path, err := os.Executable()
	require.NoError(t, err)
	for _, version := range []string{"2.0.0", "0.9.0", "invalid"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			var executable Executable
			calls := 0
			probe := func(context.Context, string, []string) (string, error) {
				calls++

				return version, nil
			}
			for range 2 {
				resolved, probeErr := executable.Resolve(t.Context(), Environment{}, path, "test", "1.0.0", probe)
				if version == "2.0.0" {
					require.NoError(t, probeErr)
					require.Equal(t, path, resolved)
				} else {
					require.Error(t, probeErr)
				}
			}
			require.Equal(t, 1, calls)
		})
	}
}

func TestExecutableRetriesInterruptedProbeForWaitingCaller(t *testing.T) {
	path, err := os.Executable()
	require.NoError(t, err)
	synctest.Test(t, func(t *testing.T) {
		var executable Executable
		var calls atomic.Int32
		probe := func(ctx context.Context, _ string, _ []string) (string, error) {
			if calls.Add(1) == 1 {
				<-ctx.Done()

				return "", ctx.Err()
			}

			return "2.0.0", nil
		}
		ctx, cancel := context.WithCancel(t.Context())
		first := make(chan error, 1)
		go func() {
			_, resolveErr := executable.Resolve(ctx, Environment{}, path, "test", "1.0.0", probe)
			first <- resolveErr
		}()
		synctest.Wait()
		second := make(chan error, 1)
		go func() {
			_, resolveErr := executable.Resolve(t.Context(), Environment{}, path, "test", "1.0.0", probe)
			second <- resolveErr
		}()
		synctest.Wait()
		require.EqualValues(t, 1, calls.Load())
		cancel()
		require.ErrorIs(t, <-first, context.Canceled)
		require.NoError(t, <-second)
		require.EqualValues(t, 2, calls.Load())
	})
}

func TestExecutableWaitingCallerCanCancel(t *testing.T) {
	path, err := os.Executable()
	require.NoError(t, err)
	synctest.Test(t, func(t *testing.T) {
		var executable Executable
		release := make(chan struct{})
		probe := func(context.Context, string, []string) (string, error) {
			<-release

			return "2.0.0", nil
		}
		first := make(chan error, 1)
		go func() {
			_, resolveErr := executable.Resolve(t.Context(), Environment{}, path, "test", "1.0.0", probe)
			first <- resolveErr
		}()
		synctest.Wait()
		ctx, cancel := context.WithCancel(t.Context())
		second := make(chan error, 1)
		go func() {
			_, resolveErr := executable.Resolve(ctx, Environment{}, path, "test", "1.0.0", probe)
			second <- resolveErr
		}()
		synctest.Wait()
		cancel()
		require.ErrorIs(t, <-second, context.Canceled)
		close(release)
		require.NoError(t, <-first)
	})
}

func TestExecutableRetriesProbeWithoutVerdict(t *testing.T) {
	t.Parallel()
	path, err := os.Executable()
	require.NoError(t, err)
	var executable Executable
	calls := 0
	probe := func(context.Context, string, []string) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("probe interrupted")
		}

		return "2.0.0", nil
	}
	_, err = executable.Resolve(t.Context(), Environment{}, path, "test", "1.0.0", probe)
	require.Error(t, err)
	_, err = executable.Resolve(t.Context(), Environment{}, path, "test", "1.0.0", probe)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
}
