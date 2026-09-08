package tray

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"testing"
)

func TestRestartTarget(t *testing.T) {
	exists := func(paths ...string) func(string) bool {
		set := make(map[string]bool, len(paths))
		for _, p := range paths {
			set[p] = true
		}
		return func(p string) bool { return set[p] }
	}

	tests := []struct {
		name      string
		appImage  string
		procExe   string
		onDisk    func(string) bool
		want      string
		wantError bool
	}{
		{
			name:    "plain binary",
			procExe: "/usr/bin/claude-monitor",
			onDisk:  exists("/usr/bin/claude-monitor"),
			want:    "/usr/bin/claude-monitor",
		},
		{
			name:    "binary replaced by dpkg",
			procExe: "/usr/bin/claude-monitor (deleted)",
			onDisk:  exists("/usr/bin/claude-monitor"),
			want:    "/usr/bin/claude-monitor",
		},
		{
			// The squashfs mount is still there while we look, but it goes
			// away on exec, so $APPIMAGE has to win.
			name:     "appimage wins over squashfs mount",
			appImage: "/home/u/Apps/claude-monitor.AppImage",
			procExe:  "/tmp/.mount_abc123/usr/bin/claude-monitor",
			onDisk:   exists("/home/u/Apps/claude-monitor.AppImage", "/tmp/.mount_abc123/usr/bin/claude-monitor"),
			want:     "/home/u/Apps/claude-monitor.AppImage",
		},
		{
			name:     "moved appimage falls back to proc",
			appImage: "/home/u/Apps/claude-monitor.AppImage",
			procExe:  "/tmp/.mount_abc123/usr/bin/claude-monitor",
			onDisk:   exists("/tmp/.mount_abc123/usr/bin/claude-monitor"),
			want:     "/tmp/.mount_abc123/usr/bin/claude-monitor",
		},
		{
			name:      "nothing on disk",
			procExe:   "/usr/bin/claude-monitor (deleted)",
			onDisk:    exists(),
			wantError: true,
		},
		{
			name:      "no paths at all",
			onDisk:    exists(),
			wantError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := restartTarget(tc.appImage, tc.procExe, tc.onDisk)
			if tc.wantError {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("restartTarget: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestReexecKeepsTheProcess exercises the real execve: it re-runs this test in a
// child, has the child call reexec() once, and checks that the second
// generation is the same process (execve replaces the image, keeping the PID)
// with argv and the environment carried over.
func TestReexecKeepsTheProcess(t *testing.T) {
	const (
		childEnv = "CM_TEST_REEXEC_CHILD"
		genEnv   = "CM_TEST_REEXEC_GEN"
	)

	if os.Getenv(childEnv) == "" {
		cmd := exec.Command(os.Args[0], "-test.run=TestReexecKeepsTheProcess")
		cmd.Env = append(os.Environ(), childEnv+"=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("child: %v\n%s", err, out)
		}
		pids := reexecPIDs.FindAllStringSubmatch(string(out), -1)
		if len(pids) != 2 {
			t.Fatalf("want two generations, got %d:\n%s", len(pids), out)
		}
		if pids[0][1] != pids[1][1] {
			t.Errorf("pid changed across reexec: %s then %s", pids[0][1], pids[1][1])
		}
		return
	}

	fmt.Printf("generation pid=%d\n", os.Getpid())
	if os.Getenv(genEnv) != "" {
		// The second generation must be able to tell it was restarted, which
		// is what arms requestName's wait for the old bus name to be released.
		if !wasRestarted() {
			t.Error("second generation does not report itself as restarted")
		}
		return
	}
	// reexec passes os.Environ() through, so this marker stops the next
	// generation from re-execing again.
	t.Setenv(genEnv, "1")
	if err := reexec(); err != nil {
		t.Fatalf("reexec: %v", err)
	}
	t.Fatal("reexec returned instead of replacing the process")
}

var reexecPIDs = regexp.MustCompile(`generation pid=(\d+)`)
