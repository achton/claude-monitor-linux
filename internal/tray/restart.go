package tray

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	cmlog "github.com/achton/claude-monitor-linux/internal/log"
)

// restartEnv marks a process as the result of a re-exec. Such a process must
// wait for the bus name that the previous image held.
const restartEnv = "CLAUDE_MONITOR_RESTARTED"

// wasRestarted reports whether a re-exec started this process. It clears the
// marker, so the next generation starts clean.
func wasRestarted() bool {
	v := os.Getenv(restartEnv)
	_ = os.Unsetenv(restartEnv)
	return v != ""
}

// reexec replaces the running process with the binary now on disk. It keeps
// argv and the session environment (DISPLAY, WAYLAND_DISPLAY,
// DBUS_SESSION_BUS_ADDRESS), because the postinst runs as root and cannot
// rebuild a user session.
//
// The lock file descriptor and the bus socket are both O_CLOEXEC, so execve
// releases them and the new image takes both again. Returns only on failure.
func reexec() error {
	target, err := selfPath()
	if err != nil {
		return err
	}
	argv := append([]string{target}, os.Args[1:]...)
	// The logger writes straight to the file, so this line survives the exec.
	cmlog.Logger().Info("tray: restarting into the installed binary", "path", target)
	return syscall.Exec(target, argv, append(os.Environ(), restartEnv+"=1"))
}

// selfPath resolves the binary to re-exec. It falls back to $PATH when the
// running image is no longer on disk.
func selfPath() (string, error) {
	exe, exeErr := os.Executable()
	if exeErr != nil {
		exe = ""
	}
	target, err := restartTarget(os.Getenv("APPIMAGE"), exe, func(p string) bool {
		_, statErr := os.Stat(p)
		return statErr == nil
	})
	if err == nil {
		return target, nil
	}
	if p, lookErr := exec.LookPath("claude-monitor"); lookErr == nil {
		return p, nil
	}
	if exeErr != nil {
		return "", fmt.Errorf("resolve own path: %w", exeErr)
	}
	return "", err
}

// restartTarget picks the path to exec from $APPIMAGE, the value of
// /proc/self/exe, and a test for whether a path exists.
func restartTarget(appImage, procExe string, exists func(string) bool) (string, error) {
	// Under an AppImage, /proc/self/exe points into a squashfs mount that
	// disappears on exec. $APPIMAGE names the .AppImage file, which stays.
	if appImage != "" && exists(appImage) {
		return appImage, nil
	}
	// dpkg renames the new binary over ours and unlinks the running inode, so
	// /proc/self/exe reads "<path> (deleted)". Remove the suffix to exec it.
	procExe = strings.TrimSuffix(procExe, " (deleted)")
	if procExe != "" && exists(procExe) {
		return procExe, nil
	}
	return "", errors.New("cannot locate the claude-monitor binary on disk")
}
