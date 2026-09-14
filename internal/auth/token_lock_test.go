package auth

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Each child uses only the parent's isolated store and synthetic credentials.
// No helper operation sends HTTP requests.
func TestTokenStoreProcess(t *testing.T) {
	mode := os.Getenv("JETSCALE_AUTH_LOCK_TEST_MODE")
	if mode == "" {
		return
	}
	id := os.Getenv("JETSCALE_AUTH_LOCK_TEST_ID")
	fmt.Println("ready")
	var err error
	switch mode {
	case "set":
		err = SetToken("child", TokenEntry{AccessToken: "synthetic"})
	case "delete":
		err = DeleteToken("remove")
	case "session", "clear":
		s := fixtureSession(t, "https://child.example.test", "synthetic")
		if mode == "clear" {
			s.jar.clear()
		}
		err = s.Save("child")
	case "exit":
		p, _ := tokensPath()
		lock, lockErr := lockTokenStore(p, storeLockTimeout)
		if lockErr != nil || lock == nil {
			t.Fatal("helper could not acquire lock")
		}
		// Simulate termination without defers or explicit handle cleanup.
		os.Exit(0)
	case "replace":
		err = SaveTokens(TokenStore{Instances: map[string]TokenEntry{"child": {AccessToken: "synthetic"}}})
	case "independent":
		for i := range 4 {
			key := fmt.Sprintf("child-%s-%d", id, i)
			if err = SetToken(key, TokenEntry{AccessToken: "synthetic"}); err != nil {
				break
			}
			s := fixtureSession(t, "https://"+key+".example.test", "synthetic")
			if err = s.Save(key); err != nil {
				break
			}
			if err = DeleteToken("remove-" + key); err != nil {
				break
			}
		}
	default:
		t.Fatal("unknown helper mode")
	}
	if err != nil {
		t.Fatal(err)
	}
}

func startTokenStoreProcess(t *testing.T, mode, id string) <-chan error {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestTokenStoreProcess$", "-test.timeout=12s")
	cmd.Env = append(os.Environ(), "JETSCALE_AUTH_LOCK_TEST_MODE="+mode, "JETSCALE_AUTH_LOCK_TEST_ID="+id, "GORACE=atexit_sleep_ms=0")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "ready" {
		cancel()
		_ = cmd.Wait()
		t.Fatal("store helper failed before readiness")
	}
	done := make(chan error, 1)
	go func() {
		// Drain test output without echoing paths or credential-shaped values.
		for scanner.Scan() {
		}
		done <- errors.Join(scanner.Err(), cmd.Wait())
	}()
	return done
}

func TestTokenStoreLockSpansReadModifyWrite(t *testing.T) {
	for _, mode := range []string{"set", "delete", "session", "clear", "replace"} {
		t.Run(mode, func(t *testing.T) {
			isolateSessionStore(t)
			if err := SetToken("remove", TokenEntry{AccessToken: "synthetic"}); err != nil {
				t.Fatal(err)
			}
			s := fixtureSession(t, "https://child.example.test", "synthetic-old")
			if err := s.Save("child"); err != nil {
				t.Fatal(err)
			}
			var done <-chan error
			err := updateTokens(func(store *TokenStore) {
				// Pause after the read, not merely before the final rename.
				done = startTokenStoreProcess(t, mode, "")
				select {
				case err := <-done:
					t.Fatalf("writer bypassed the held transaction lock: %v", err)
				case <-time.After(150 * time.Millisecond):
				}
				store.Instances["parent"] = TokenEntry{AccessToken: "synthetic-parent"}
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatalf("store helper failed: %v", err)
			}
			store, err := LoadTokens()
			if err != nil {
				t.Fatal(err)
			}
			if mode != "replace" && store.Instances["parent"].AccessToken != "synthetic-parent" {
				t.Fatal("child saved a stale snapshot over the parent's update")
			}
			switch mode {
			case "set", "replace":
				if store.Instances["child"].AccessToken != "synthetic" {
					t.Fatal("child token was not saved")
				}
			case "delete":
				if _, ok := store.Instances["remove"]; ok {
					t.Fatal("deleted token resurrected")
				}
			case "session", "clear":
				entry, ok := store.Sessions[s.storeKey()]
				if !ok || entry.Origin != s.origin || entry.Channel != s.channel {
					t.Fatal("session binding or deletion tombstone lost")
				}
				if mode == "clear" && len(entry.Cookies) != 0 {
					t.Fatal("session deletion lost")
				}
				if mode == "session" && (len(entry.Cookies) != 1 || entry.Cookies[0].Value != "synthetic") {
					t.Fatal("session update lost")
				}
			}
		})
	}
}

func TestTokenStoreConcurrentIndependentProcesses(t *testing.T) {
	isolateSessionStore(t)
	for child := range 4 {
		for i := range 4 {
			if err := SetToken(fmt.Sprintf("remove-child-%d-%d", child, i), TokenEntry{AccessToken: "synthetic"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	p, _ := tokensPath()
	lock, err := lockTokenStore(p, storeLockTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	var children []<-chan error
	for child := range 4 {
		children = append(children, startTokenStoreProcess(t, "independent", fmt.Sprint(child)))
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	for _, done := range children {
		if err := <-done; err != nil {
			t.Fatalf("store helper failed: %v", err)
		}
	}
	store, err := LoadTokens()
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Instances) != 16 || len(store.Sessions) != 16 {
		t.Fatal("independent concurrent entries or deletions were lost")
	}
	for child := range 4 {
		for i := range 4 {
			key := fmt.Sprintf("child-%d-%d", child, i)
			s := fixtureSession(t, "https://"+key+".example.test", "synthetic")
			entry := store.Sessions[s.storeKey()]
			if store.Instances[key].AccessToken != "synthetic" || len(entry.Cookies) != 1 || entry.Cookies[0].Value != "synthetic" {
				t.Fatal("independent entry missing or changed")
			}
		}
	}
}

func TestTokenStoreLockTimeoutAndReuse(t *testing.T) {
	isolateSessionStore(t)
	p, _ := tokensPath()
	first, err := lockTokenStore(p, storeLockTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	before, err := first.Stat()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	second, err := lockTokenStore(p, 60*time.Millisecond)
	if second != nil {
		_ = second.Close()
		t.Fatal("second handle acquired a held lock")
	}
	if err == nil || !strings.Contains(err.Error(), "lock wait timed out") || strings.Contains(err.Error(), filepath.Dir(p)) {
		t.Fatal("lock timeout missing or leaked storage path")
	}
	if elapsed := time.Since(start); elapsed < 60*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("lock wait did not respect its bound: %s", elapsed)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err = lockTokenStore(p, storeLockTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	after, err := second.Stat()
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("lock file was replaced instead of reused")
	}
	if err := privateStoreFile(second); err != nil {
		t.Fatal(err)
	}
}

func TestTokenStoreLockReleasedOnProcessExit(t *testing.T) {
	isolateSessionStore(t)
	if err := <-startTokenStoreProcess(t, "exit", ""); err != nil {
		t.Fatalf("store helper failed: %v", err)
	}
	p, _ := tokensPath()
	path := filepath.Join(filepath.Dir(p), tokensLockName)
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal("exited helper did not leave its lock file")
	}
	if err := SetToken("after-exit", TokenEntry{AccessToken: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("process-exit recovery replaced the lock file")
	}
}
