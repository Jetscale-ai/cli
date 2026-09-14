//go:build !windows

package auth

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func createStoreDir(dir string) error { return os.MkdirAll(dir, 0o700) }

func openStoreFile(path string) (*os.File, error) { return os.Open(path) }

func openStoreLockFile(path string) (*os.File, error) {
	// O_NONBLOCK avoids hanging on an existing FIFO before type validation.
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func tryStoreLock(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EINTR) {
		return false, nil
	}
	return err == nil, err
}

// Unix saves retain the existing atomic permission-repair behavior.
func validateStoreReplacement(string) error { return nil }

func createPrivateStoreTemp(dir string) (*os.File, error) {
	return os.CreateTemp(dir, ".tokens-*")
}

func privateStoreFile(f *os.File) error {
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("token store must be a private regular file (0600)")
	}
	return nil
}

func privateStoreDir(dir string) error {
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return os.ErrNotExist
	}
	if err != nil {
		return errors.New("inspect token store directory failed")
	}
	// Existing 0755 config directories are safe for a 0600 file; only the
	// owner may replace it. New directories are created as 0700.
	if !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return errors.New("token store directory must not be group/other-writable or a symlink")
	}
	return nil
}
