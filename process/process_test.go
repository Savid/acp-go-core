package process

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEnvironmentBuildMergeOrder(t *testing.T) {
	t.Parallel()

	env := Environment{
		Process:        []string{"HOME=/home/me", "PATH=/usr/bin:/bin", "KEEP=1", "ACP_GO_X_INTERNAL_MARK=1"},
		Agent:          map[string]string{"HOME": "/agent", "AGENT": "a"},
		Session:        map[string]string{"HOME": "/session", "EMPTY": "", "PATH": "/opt/bin"},
		Owned:          map[string]string{"X_HOME": "/owned", "ACP_GO_X_INTERNAL_MODE": "ask"},
		InternalPrefix: "ACP_GO_X_INTERNAL_",
		ExtraPathDirs:  []string{"/ops/first", "/ops/second"},
	}

	got, err := env.Build()
	require.NoError(t, err)
	require.Equal(t, []string{
		"ACP_GO_X_INTERNAL_MODE=ask",
		"AGENT=a",
		"EMPTY=",
		"HOME=/session",
		"KEEP=1",
		"PATH=/ops/first:/ops/second:/opt/bin",
		"X_HOME=/owned",
	}, got)

	base, err := env.Base()
	require.NoError(t, err)
	require.Equal(t, []string{"AGENT=a", "HOME=/agent", "KEEP=1", "PATH=/usr/bin:/bin"}, base)
}

func TestEnvironmentValidation(t *testing.T) {
	t.Parallel()

	for _, bad := range []map[string]string{{"": "x"}, {"A=B": "x"}, {"A\x00": "x"}, {"A": "x\x00"}} {
		var nameErr *NameError

		require.ErrorAs(t, ValidateNames(bad), &nameErr)
	}

	require.NoError(t, ValidateNames(map[string]string{"lower": "", "https_proxy": "http://x"}))

	var dirErr *PathDirError

	require.ErrorAs(t, ValidateExtraPathDirs([]string{"/ok", "relative"}), &dirErr)
	require.Equal(t, 1, dirErr.Index)
	require.ErrorAs(t, ValidateExtraPathDirs([]string{"/a:/b"}), &dirErr)
	require.ErrorAs(t, ValidateExtraPathDirs([]string{""}), &dirErr)

	_, err := Environment{Session: map[string]string{"": "x"}}.Build()
	require.Error(t, err)
}

func TestResolveExecutableUsesBasePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	shadow := t.TempDir()

	for _, root := range []string{dir, shadow} {
		require.NoError(t, os.WriteFile(filepath.Join(root, "harness"), []byte("#!/bin/sh\n"), 0o755))
	}

	resolved, err := ResolveExecutable("harness", []string{"PATH=" + dir})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "harness"), resolved)

	_, err = ResolveExecutable("harness", []string{"PATH=" + t.TempDir()})
	require.Error(t, err)

	_, err = ResolveExecutable("harness", nil)
	require.Error(t, err)

	direct, err := ResolveExecutable(filepath.Join(shadow, "harness"), nil)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(shadow, "harness"), direct)

	_, err = ResolveExecutable(dir, nil)
	require.Error(t, err, "a directory is not an executable")
}

func TestStartWaitAndShutdown(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	child, err := Start(ctx, Request{
		Executable: "/bin/sh",
		Args:       []string{"-c", "echo $GREETING; echo err >&2; cat"},
		Env:        []string{"GREETING=hello", "PATH=/usr/bin:/bin"},
		Dir:        t.TempDir(),
	})
	require.NoError(t, err)

	out := make(chan string, 1)

	go func() {
		data, _ := io.ReadAll(child.Stdout())
		out <- string(data)
	}()

	require.NoError(t, child.Stdin().Close())

	result, err := child.Wait(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)
	require.Equal(t, "hello\n", <-out)
	require.Equal(t, "err", child.StderrLastLine())
	require.NoError(t, child.Close())
}

func TestShutdownSignalsTheGroup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	child, err := Start(ctx, Request{
		Executable: "/bin/sh",
		Args:       []string{"-c", "sleep 30 & wait"},
		Env:        []string{"PATH=/usr/bin:/bin"},
	})
	require.NoError(t, err)

	go func() { _, _ = io.Copy(io.Discard, child.Stdout()) }()

	start := time.Now()
	require.NoError(t, child.Shutdown(ctx, 5*time.Second))
	require.Less(t, time.Since(start), 5*time.Second, "SIGTERM to the group ends the shell without waiting for the grace period")

	result, err := child.Wait(ctx)
	require.NoError(t, err)
	require.True(t, result.Signal != 0 || result.ExitCode != 0)
	require.NoError(t, child.Close())
}

func TestWaitDetachesOnCancel(t *testing.T) {
	t.Parallel()

	child, err := Start(context.Background(), Request{Executable: "/bin/sh", Args: []string{"-c", "sleep 5"}, Env: []string{"PATH=/bin"}})
	require.NoError(t, err)

	go func() { _, _ = io.Copy(io.Discard, child.Stdout()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = child.Wait(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	require.NoError(t, child.Kill())

	result, err := child.Wait(context.Background())
	require.NoError(t, err)
	require.NotZero(t, result.Signal)
	require.NoError(t, child.Close())
}

func TestKillReapsTheChild(t *testing.T) {
	t.Parallel()

	child, err := Start(context.Background(), Request{Executable: "/bin/sh", Args: []string{"-c", "echo dying >&2; sleep 5"}, Env: []string{"PATH=/bin"}})
	require.NoError(t, err)

	go func() { _, _ = io.Copy(io.Discard, child.Stdout()) }()

	require.Eventually(t, func() bool { return child.StderrLastLine() == "dying" }, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, child.Kill())

	// Kill itself must begin the reap: nothing here calls Wait or Done.
	select {
	case <-child.done:
	case <-time.After(5 * time.Second):
		t.Fatal("killed child was not reaped")
	}

	require.Equal(t, "dying", child.StderrLastLine())
	require.NoError(t, child.Close())
}

func TestStderrTailKeepsTheLastLine(t *testing.T) {
	t.Parallel()

	child, err := Start(context.Background(), Request{Executable: "/bin/sh", Args: []string{"-c", "i=0; while [ $i -lt 2000 ]; do echo line-$i >&2; i=$((i+1)); done; echo final >&2"}, Env: []string{"PATH=/bin"}})
	require.NoError(t, err)

	go func() { _, _ = io.Copy(io.Discard, child.Stdout()) }()

	_, err = child.Wait(context.Background())
	require.NoError(t, err)
	require.Equal(t, "final", child.StderrLastLine())
	require.NoError(t, child.Close())
}

func TestMinimumVersion(t *testing.T) {
	t.Parallel()

	require.NoError(t, checkMinimumVersion("acme", "v1.2.3", "1.2.3"))
	require.NoError(t, checkMinimumVersion("acme", "1.10.0-beta", "1.9.9"))
	require.Error(t, checkMinimumVersion("acme", "1.2", "1.2.1"))
	require.Error(t, checkMinimumVersion("acme", "", "1.0.0"))
	require.Error(t, checkMinimumVersion("acme", "1.x", "1.0.0"))
}

func TestValidateOptionalAbsolutePath(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateOptionalAbsolutePath(""))
	require.NoError(t, ValidateOptionalAbsolutePath("/abs"))
	require.Error(t, ValidateOptionalAbsolutePath("relative"))
}

func TestSeedFileFlag(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	host := filepath.Join(dir, "config.toml")
	require.NoError(t, os.WriteFile(host, []byte("key = 1"), 0o600))

	var flag SeedFileFlag

	require.Equal(t, "", flag.String())
	require.Error(t, flag.Set("no-equals"))
	require.Error(t, flag.Set("=/x"))
	require.Error(t, flag.Set("a="+filepath.Join(dir, "missing")))
	require.NoError(t, flag.Set("b/config.toml="+host))
	require.NoError(t, flag.Set("a.toml="+host))
	require.Equal(t, "a.toml,b/config.toml", flag.String())
	require.Equal(t, "key = 1", flag.Files["a.toml"])
}

func TestStartRefusesMissingExecutable(t *testing.T) {
	t.Parallel()

	_, err := Start(context.Background(), Request{Executable: filepath.Join(t.TempDir(), "missing")})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "start"))
}
