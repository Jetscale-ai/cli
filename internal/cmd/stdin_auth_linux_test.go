//go:build linux

package cmd

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestStdinAuthSIGTERMHelper(t *testing.T) {
	if os.Getenv("JETSCALE_STDIN_SIGNAL_HELPER") != "1" {
		return
	}
	stdinCommandTest(t)
	root := newRootCmd()
	fixture := &cobra.Command{Use: "fixture", RunE: func(cmd *cobra.Command, _ []string) error {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "ready")
		<-cmd.Context().Done()
		return cmd.Context().Err()
	}}
	wrapSessionLifecycle(fixture)
	root.AddCommand(fixture)
	root.SetIn(strings.NewReader("synthetic-password\n"))
	root.SetArgs([]string{"--api-url", os.Getenv("JETSCALE_STDIN_SIGNAL_URL"), "--login", "test@example.invalid", "--password-stdin", "fixture"})
	err := root.Execute()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("signal cancellation error: %v", err)
	}
	if os.Getenv("JETSCALE_STDIN_SIGNAL_DURING_LOGIN") == "1" && (!strings.Contains(err.Error(), "outcome uncertain") || !strings.Contains(err.Error(), "no automatic retry")) {
		t.Fatalf("ambiguous signal-interrupted sign-in not reported: %v", err)
	}
}

func TestStdinAuthSIGTERMSignsOut(t *testing.T) {
	var logouts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/auth/sign-in":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-cookie", Path: "/"})
			_, _ = fmt.Fprint(w, `{}`)
		case "/api/v2/auth/me":
			_, _ = fmt.Fprint(w, `{"data":{"user":{"email":"test@example.invalid"}}}`)
		case "/api/v2/auth/sign-out":
			logouts.Add(1)
			_, _ = fmt.Fprint(w, `{}`)
		default:
			t.Error("unexpected request")
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStdinAuthSIGTERMHelper$")
	child.Env = append(os.Environ(), "JETSCALE_STDIN_SIGNAL_HELPER=1", "JETSCALE_STDIN_SIGNAL_URL="+server.URL)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	child.Stderr = &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "ready\n" {
		_ = child.Process.Kill()
		_ = child.Wait()
		t.Fatal("signal helper did not establish session")
	}
	if err := child.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("signal helper failed: %v", err)
	}
	if logouts.Load() != 1 || !strings.Contains(stderr.String(), "Temporary session signed out") {
		t.Fatal("SIGTERM did not clean up temporary session")
	}
	if strings.Contains(stderr.String(), "synthetic-password") || strings.Contains(stderr.String(), "synthetic-cookie") {
		t.Fatal("signal cleanup leaked secret")
	}
}

func TestStdinAuthSIGTERMDuringLogin(t *testing.T) {
	started := make(chan struct{})
	var logins atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/auth/sign-in" || logins.Add(1) != 1 {
			t.Error("unexpected request or retried sign-in")
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStdinAuthSIGTERMHelper$")
	child.Env = append(os.Environ(), "JETSCALE_STDIN_SIGNAL_HELPER=1", "JETSCALE_STDIN_SIGNAL_DURING_LOGIN=1", "JETSCALE_STDIN_SIGNAL_URL="+server.URL)
	var output bytes.Buffer
	child.Stdout, child.Stderr = &output, &output
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		_ = child.Wait()
		t.Fatal("signal helper did not start sign-in")
	}
	start := time.Now()
	if err := child.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil || time.Since(start) > 3*time.Second || logins.Load() != 1 {
		t.Fatalf("sign-in did not cancel promptly: %v", err)
	}
	if strings.Contains(output.String(), "synthetic-password") || strings.Contains(output.String(), "synthetic-cookie") || strings.Contains(output.String(), "ready") {
		t.Fatal("interrupted sign-in leaked or ran the command")
	}
}

func TestStdinAuthRejectsTerminalWithoutReading(t *testing.T) {
	terminal, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skip("pseudo-terminal unavailable")
	}
	defer func() { _ = terminal.Close() }()
	start := time.Now()
	_, err = readStdinPassword(context.Background(), terminal, time.Second)
	if err == nil || !strings.Contains(err.Error(), "not a terminal") || time.Since(start) >= time.Second {
		t.Fatal("interactive password input was not rejected immediately")
	}
}
