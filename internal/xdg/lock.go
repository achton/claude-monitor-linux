package xdg

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// ErrLocked is returned when the lock is already held by another process.
var ErrLocked = errors.New("lock is held by another process")

// RestartMarker is the line MarkRestartable writes and packaging/deb/postinst
// looks for. TestPostinstReadsTheRestartMarker keeps the two in step.
const RestartMarker = "restart=sighup"

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
	l := &Lock{f: f}
	// The PID alone is enough for diagnostics. MarkRestartable adds the
	// restart marker later, once the tray can act on a signal.
	_ = l.writeBody(fmt.Sprintf("%d\n", os.Getpid()))
	return l, nil
}

// MarkRestartable tells the .deb postinst that this process re-execs itself on
// SIGHUP instead of dying. The postinst cannot test for the handler, so the
// tray must declare it. See decision 21 in docs/DESIGN.md. Call this only once
// the handler is installed.
func (l *Lock) MarkRestartable() error {
	if l == nil || l.f == nil {
		return nil
	}
	return l.writeBody(fmt.Sprintf("%d\n%s\n", os.Getpid(), RestartMarker))
}

// writeBody replaces the contents of the lock file. The format is a contract
// with packaging/deb/postinst. The PID is on the first line, and RestartMarker
// is on the second.
func (l *Lock) writeBody(body string) error {
	if err := l.f.Truncate(0); err != nil {
		return fmt.Errorf("truncate lock file: %w", err)
	}
	if _, err := l.f.WriteAt([]byte(body), 0); err != nil {
		return fmt.Errorf("write lock file: %w", err)
	}
	return nil
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
