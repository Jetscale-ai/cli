package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Jetscale-ai/cli/internal/buildinfo"
)

func TestSystemDiagnosticsUsesBackendDefaultFormat(t *testing.T) {
	t.Setenv(buildinfo.EnvVar("CONFIG_DIR"), t.TempDir())
	t.Setenv(buildinfo.EnvVar("TOKEN"), "offline-test-token")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/api/v2/system/diagnostics" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("optional format must be omitted, got query %q", r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer offline-test-token" {
			t.Error("missing test bearer authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"offline-fixture"}`)
	}))
	defer server.Close()

	root := newRootCmd()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"--api-url", server.URL, "--output", "json", "system", "diagnostics"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("expected one diagnostics request, got %d", requests.Load())
	}
	if !strings.Contains(output.String(), "offline-fixture") {
		t.Fatalf("diagnostics response not printed: %q", output.String())
	}
}
