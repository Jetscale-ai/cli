//go:build !windows

package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func preparePrivateTestDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
}

func assertPrivateTokenFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("token file is not 0600")
	}
}

func TestTokenStoreRejectsUnsafeLock(t *testing.T) {
	for _, kind := range []string{"permissions", "symlink", "directory", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			isolateSessionStore(t)
			p, _ := tokensPath()
			path := filepath.Join(filepath.Dir(p), tokensLockName)
			var err error
			switch kind {
			case "permissions":
				err = os.WriteFile(path, nil, 0o600)
				if err == nil {
					err = os.Chmod(path, 0o644)
				}
			case "symlink":
				target := filepath.Join(privateTempDir(t), "lock-target")
				if err = os.WriteFile(target, nil, 0o600); err == nil {
					err = os.Symlink(target, path)
				}
			case "directory":
				err = os.Mkdir(path, 0o700)
			case "fifo":
				err = unix.Mkfifo(path, 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, write := range []func() error{
				func() error { return SetToken("fixture", TokenEntry{AccessToken: "synthetic"}) },
				func() error { return SaveTokens(TokenStore{}) },
			} {
				if err := write(); err == nil || strings.Contains(err.Error(), filepath.Dir(p)) {
					t.Fatal("unsafe lock accepted or storage path leaked")
				}
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatal("unsafe lock was modified or replaced")
			}
		})
	}
}

func TestPrivateStoreTempBeforeWrite(t *testing.T) {
	f, err := createPrivateStoreTemp(privateTempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	assertPrivateTokenFile(t, f.Name())
	info, err := f.Stat()
	if err != nil || info.Size() != 0 {
		t.Fatal("temporary file is not empty")
	}
}
