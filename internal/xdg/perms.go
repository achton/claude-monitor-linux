package xdg

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// EnsureSecureDir creates dir if missing and verifies it has mode 0700.
// Returns an error if perms are too permissive.
func EnsureSecureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	st, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat %s: %w", dir, err)
	}
	mode := st.Mode().Perm()
	if mode&0o077 != 0 {
		return fmt.Errorf(
			"refusing to use %s: mode %#o is too permissive — run: chmod 0700 %s",
			dir, mode, dir,
		)
	}
	return nil
}

// EnsureSecureFile verifies that a file (if it exists) has mode 0600.
// If the file does not exist, returns nil — the creator is expected to use 0600.
// If the file exists with wider perms AND is owned by the current user, it is
// auto-fixed to 0600. If owned by another user, the call refuses.
func EnsureSecureFile(path string) error {
	st, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	mode := st.Mode().Perm()
	if mode&0o077 == 0 {
		return nil
	}
	// File is too permissive. Auto-fix if we own it; otherwise refuse.
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) == os.Getuid() {
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("chmod %s to 0600: %w", path, err)
		}
		return nil
	}
	return fmt.Errorf(
		"refusing to use %s: mode %#o is too permissive and the file is not owned by the current user — run: chmod 0600 %s",
		path, mode, path,
	)
}

// EnsureAllPaths ensures all directory paths exist with 0700 perms, returning
// the first error encountered. Used at startup.
func EnsureAllPaths() error {
	for _, d := range []string{DataDir(), ConfigDir(), StateDir()} {
		if err := EnsureSecureDir(d); err != nil {
			return err
		}
	}
	// Verify DB file perms if it exists.
	if err := EnsureSecureFile(DBPath()); err != nil {
		return err
	}
	if err := EnsureSecureFile(ConfigPath()); err != nil {
		return err
	}
	return nil
}

// ParentDir is a small helper to extract the directory of a path.
func ParentDir(p string) string { return filepath.Dir(p) }
