package xdg

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// The .deb postinst reads the first line as a PID and greps for the marker
// line, so the on-disk format is a contract with packaging/deb/postinst.
func TestLockFileFormat(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	lock, err := AcquireLock()
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	defer lock.Release()

	body, err := os.ReadFile(LockPath())
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	if got, want := strings.TrimSpace(string(body)), fmt.Sprint(os.Getpid()); got != want {
		t.Errorf("fresh lock: got %q, want just the pid %q", got, want)
	}

	if err := lock.MarkRestartable(); err != nil {
		t.Fatalf("MarkRestartable: %v", err)
	}

	body, err = os.ReadFile(LockPath())
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 2 {
		t.Fatalf("marked lock: got %d lines (%q), want 2", len(lines), body)
	}
	if lines[0] != fmt.Sprint(os.Getpid()) {
		t.Errorf("first line: got %q, want the pid %q", lines[0], fmt.Sprint(os.Getpid()))
	}
	if lines[1] != RestartMarker {
		t.Errorf("second line: got %q, want %q", lines[1], RestartMarker)
	}
}

func TestAcquireLockRejectsSecondHolder(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	first, err := AcquireLock()
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	defer first.Release()

	// flock is per-open-file-description, so a second Acquire in this process
	// still contends, which is what the tray's singleton check relies on.
	if _, err := AcquireLock(); err == nil {
		t.Fatal("second AcquireLock succeeded, want ErrLocked")
	}
}

// The postinst greps for the marker as a literal string, so a rename here has
// to reach the packaging script too.
func TestPostinstReadsTheRestartMarker(t *testing.T) {
	const script = "../../packaging/deb/postinst"

	body, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("read %s: %v", script, err)
	}
	if !strings.Contains(string(body), RestartMarker) {
		t.Errorf("%s does not look for %q; the tray would never be restarted on upgrade", script, RestartMarker)
	}
}
