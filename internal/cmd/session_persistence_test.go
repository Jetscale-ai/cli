package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jetscale-ai/cli/internal/auth"
	"github.com/spf13/cobra"
)

func TestInteractiveLoginRejectsTokenOverrideBeforePrompt(t *testing.T) {
	localCommandTest(t)
	for _, token := range []string{"", "fake-override"} {
		t.Setenv("JETSCALE_LOCAL_TOKEN", token)
		root := newRootCmd()
		var stdout, stderr bytes.Buffer
		root.SetOut(&stdout)
		root.SetErr(&stderr)
		root.SetArgs([]string{"auth", "login"})
		err := root.Execute()
		if err == nil || !strings.Contains(err.Error(), "TOKEN") {
			t.Fatalf("override not rejected: %v", err)
		}
		if strings.Contains(stderr.String(), "Email:") {
			t.Error("prompted despite token override")
		}
	}
}

func TestPersistenceFailureCleansUpOnlyFreshLogin(t *testing.T) {
	for _, fresh := range []bool{true, false} {
		t.Run(fmt.Sprint(fresh), func(t *testing.T) {
			dir := localCommandTest(t)
			// A corrupt store must not be overwritten, nor strand a fresh login.
			if err := os.WriteFile(filepath.Join(dir, "tokens.yaml"), []byte("[invalid"), 0o600); err != nil {
				t.Fatal(err)
			}
			logoutCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v2/auth/sign-in":
					http.SetCookie(w, &http.Cookie{Name: "session", Value: "fake-session", Path: "/"})
					_, _ = fmt.Fprint(w, `{}`)
				case "/api/v2/auth/me":
					_, _ = fmt.Fprint(w, `{"data":{"user":{"email":"test@example.invalid"}}}`)
				case "/api/v2/auth/sign-out":
					logoutCalls++
					_, _ = fmt.Fprint(w, `{}`)
				default:
					t.Error("unexpected route")
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			command := &cobra.Command{Use: "fixture", RunE: func(cmd *cobra.Command, _ []string) error {
				session, err := auth.LoginSession(server.URL, "test@example.invalid", "fake-password", "")
				if err != nil {
					return err
				}
				rememberSession(cmd, session, "fixture", false)
				cmd.Context().Value(sessionContextKey{}).(*commandSessionState).fresh = fresh
				return nil
			}}
			wrapSessionLifecycle(command)
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)
			command.SetArgs(nil)
			if err := command.Execute(); err == nil {
				t.Error("save failure swallowed")
			}
			if fresh && logoutCalls != 1 {
				t.Error("fresh login stranded after save failure")
			}
			if !fresh && logoutCalls != 0 {
				t.Error("existing session invalidated after save failure")
			}
		})
	}
}
