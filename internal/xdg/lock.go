package xdg

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// ErrLocked is returned when the lock is already held by another process.
var ErrLocked = errors.New("lock is held by another process")

// Lock is an advisory exclusive flock on LockPath().
type Lock struct {
	f *os.File
}

// AcquireLock attempts to acquire the single-instance lock.
// Returns ErrLocked if another process already holds it.
func AcquireLock() (*Lock, error) {
	path := LockPath()
	if err := os.MkdirAll(ParentDir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("flock: %w", err)
	}
	// Write our PID for diagnostic purposes.
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(fmt.Sprintf("%d\n", os.Getpid())), 0)
	return &Lock{f: f}, nil
}

// Release releases the lock and removes the lock file.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	_ = unix.Flock(int(l.f.Fd()), unix.LOCK_UN)
	path := l.f.Name()
	if err := l.f.Close(); err != nil {
		return err
	}
	_ = os.Remove(path)
	return nil
}
