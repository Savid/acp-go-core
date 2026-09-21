package process

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// FileLock owns an exclusive advisory file lock.
type FileLock struct {
	file *os.File
	once sync.Once
	err  error
}

// Close releases the lock explicitly before closing its descriptor. A child
// between fork and exec may still hold an inherited descriptor to this file.
func (l *FileLock) Close() error {
	l.once.Do(func() {
		l.err = errors.Join(syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN), l.file.Close())
	})

	return l.err
}

// LockFile holds an exclusive nonblocking lock until the returned lock is
// closed. The lock file stays in place so every contender uses the same inode.
func LockFile(path string) (*FileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("lock file is already in use: %w", err)
	}

	return &FileLock{file: file}, nil
}
