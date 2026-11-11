package cmd

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

func TestRegenerateHelpDescribesInPlaceOverwriteOffline(t *testing.T) {
	for _, args := range [][]string{
		{"recommendations", "--help"},
		{"recommendations", "regenerate", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := newRootCmd()
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("help must work offline: %v", err)
			}
			help := strings.Join(strings.Fields(stdout.String()), " ")
			for _, text := range []string{"in place", "same ID/version"} {
				if !strings.Contains(help, text) {
					t.Errorf("help missing %q: %s", text, help)
				}
			}
			for _, text := range []string{"Request a new recommendation version", "Request a new version"} {
				if strings.Contains(help, text) {
					t.Errorf("help must not promise a new version: %s", help)
				}
			}
			if len(args) == 3 {
				for _, text := range []string{"Capture", "next successful run", "eligible evidence sources", "--exclude-source", "do not retry"} {
					if !strings.Contains(help, text) {
						t.Errorf("regeneration help missing %q: %s", text, help)
					}
				}
			}
			if stderr.Len() != 0 {
				t.Fatalf("help emitted diagnostics: %q", stderr.String())
			}
		})
	}
}

func TestRegenerateCanExcludeFocusForOneRequest(t *testing.T) {
	f, server := newFakeBackend(t)
	const recommendationID = "3f2c0a6e-0000-4000-8000-000000000000"
	const endpoint = "POST /api/v2/cloud/custom-recommendations/" + recommendationID + "/action/regenerate"
	f.on(endpoint, func(_ *http.Request, body []byte) (int, any) {
		if got, want := string(body), `{"excluded_source_keys":["focus-cost-attribution"]}`; got != want {
			t.Errorf("regeneration must exclude only the requested source: got %s, want %s", got, want)
		}
		return item(map[string]any{"id": recommendationID, "status": "processing"})
	})

	stdout, stderr, code := run(t, server, "-o", "json", "recommendations", "regenerate", recommendationID,
		"--exclude-source", "focus-cost-attribution", "-y")
	if code != 0 || decode(t, stdout).(map[string]any)["id"] != recommendationID {
		t.Fatalf("exclude FOCUS regeneration failed: %d %s %s", code, stdout, stderr)
	}
	if f.count(endpoint) != 1 {
		t.Fatalf("regeneration must be submitted exactly once, got %d", f.count(endpoint))
	}
}

func TestRegenerateSourceSelectionIsNotPersisted(t *testing.T) {
	f, server := newFakeBackend(t)
	const recommendationID = "3f2c0a6e-0000-4000-8000-000000000000"
	const endpoint = "POST /api/v2/cloud/custom-recommendations/" + recommendationID + "/action/regenerate"
	for _, tc := range []struct {
		name  string
		flags []string
		body  string
	}{
		{"without FOCUS", []string{"--exclude-source", "focus-cost-attribution"}, `{"excluded_source_keys":["focus-cost-attribution"]}`},
		{"default sources after exclusion", nil, `{}`},
		{"repeatable exclusions", []string{"--exclude-source", "focus-cost-attribution", "--exclude-source", "resource-inventory"}, `{"excluded_source_keys":["focus-cost-attribution","resource-inventory"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.on(endpoint, func(_ *http.Request, body []byte) (int, any) {
				if string(body) != tc.body {
					t.Errorf("source selection must match this request only: got %s, want %s", body, tc.body)
				}
				return item(map[string]any{"id": recommendationID, "version": 7})
			})
			args := append([]string{"-o", "json", "recommendations", "regenerate", recommendationID, "-y"}, tc.flags...)
			stdout, stderr, code := run(t, server, args...)
			if code != 0 {
				t.Fatalf("regeneration failed: %d %s %s", code, stdout, stderr)
			}
			rec := decode(t, stdout).(map[string]any)
			if rec["id"] != recommendationID || rec["version"] != float64(7) {
				t.Fatalf("regeneration must return the same recommendation ID and version: %s", stdout)
			}
		})
	}
	if f.count(endpoint) != 3 {
		t.Fatalf("each request must be submitted once, got %d", f.count(endpoint))
	}
}

func TestRegenerateExclusionStillRequiresConfirmation(t *testing.T) {
	f, server := newFakeBackend(t)
	stdout, _, code := run(t, server, "-o", "json", "recommendations", "regenerate", "3f2c0a6e-0000-4000-8000-000000000000",
		"--exclude-source", "focus-cost-attribution")
	if code != ExitForbidden || !strings.Contains(stdout, "confirmation_required") {
		t.Fatalf("excluding a source must not bypass confirmation: %d %s", code, stdout)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) != 0 {
		t.Fatalf("unconfirmed regeneration must not access the Backend: %v", f.requests)
	}
}

func TestRegenerateExclusionsHonorBackendErrorsWithoutRetry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		code   int
	}{
		{"unsupported source", http.StatusBadRequest, ExitFailure},
		{"superuser required", http.StatusForbidden, ExitForbidden},
		{"uncertain outcome", http.StatusBadGateway, ExitFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, server := newFakeBackend(t)
			const endpoint = "POST /api/v2/cloud/custom-recommendations/3f2c0a6e-0000-4000-8000-000000000000/action/regenerate"
			f.on(endpoint, func(*http.Request, []byte) (int, any) {
				return backendError(tc.status, "EXCLUSION_REJECTED")
			})
			stdout, _, code := run(t, server, "-o", "json", "recommendations", "regenerate", "3f2c0a6e-0000-4000-8000-000000000000",
				"--exclude-source", "focus-cost-attribution", "-y")
			if code != tc.code || decode(t, stdout).(map[string]any)["error"].(map[string]any)["transient"] != false {
				t.Fatalf("must preserve rejection or uncertainty without recommending retries: %d %s", code, stdout)
			}
			if f.count(endpoint) != 1 {
				t.Fatalf("a failed submission must not be retried or fall back to default sources: %d", f.count(endpoint))
			}
		})
	}
}

func TestRegenerateRejectsMalformedExclusionsBeforeAccess(t *testing.T) {
	tooMany := []string{}
	for i := 0; i < 33; i++ {
		tooMany = append(tooMany, "--exclude-source", strings.Repeat("x", i+1))
	}
	for _, tc := range []struct {
		name  string
		flags []string
	}{
		{"empty", []string{"--exclude-source", ""}},
		{"blank", []string{"--exclude-source", "  "}},
		{"duplicate", []string{"--exclude-source", "focus-cost-attribution", "--exclude-source", "focus-cost-attribution"}},
		{"too many", tooMany},
		{"before stdin authentication", []string{"--exclude-source", "", "--login", "test@example.invalid", "--password-stdin"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, server := newFakeBackend(t)
			args := append([]string{"-o", "json", "recommendations", "regenerate", "3f2c0a6e-0000-4000-8000-000000000000", "-y"}, tc.flags...)
			stdout, stderr, code := run(t, server, args...)
			if code != ExitUsage {
				t.Fatalf("invalid exclusions must be a usage error: %d %s %s", code, stdout, stderr)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.requests) != 0 {
				t.Fatalf("invalid exclusions must not access the Backend: %v", f.requests)
			}
		})
	}
}
