package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Jetscale-ai/cli/internal/buildinfo"
	"github.com/spf13/cobra"
)

var resourceGroups = []string{"companies", "business-units", "accounts", "recommendations", "cost-sources", "feature-flags", "plans", "licenses", "api"}

func TestHelpShowsGroupsAndHidesDeprecatedCommands(t *testing.T) {
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := out.String()
	for _, want := range []string{"Core Commands:", "Billing Commands:", "Additional Commands:", "companies", "feature-flags", "api"} {
		if !strings.Contains(help, want) {
			t.Errorf("root help lacks %q", want)
		}
	}
	for _, hidden := range []string{"\n  prepare", "\n  flags "} {
		if strings.Contains(help, hidden) {
			t.Errorf("deprecated command %q is listed", hidden)
		}
	}
}

func TestEveryResourceCommandHasExamples(t *testing.T) {
	root := newRootCmd()
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Hidden {
			return
		}
		if c.Runnable() && c.Example == "" {
			t.Errorf("%q has no Examples", c.CommandPath())
		}
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	for _, name := range resourceGroups {
		cmd, _, err := root.Find([]string{name})
		if err != nil || cmd.Name() != name {
			t.Fatalf("missing command %q", name)
		}
		walk(cmd)
	}
}

func TestDeprecatedCommandsStillRun(t *testing.T) {
	for _, args := range [][]string{{"prepare", "basics", "--help"}, {"flags", "plan", "--help"}} {
		root := newRootCmd()
		root.SetOut(io.Discard)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestGetAcceptsShowAndViewAliases(t *testing.T) {
	f, server := newFakeBackend(t)
	f.withCompany()
	for _, verb := range []string{"get", "show", "view"} {
		stdout, _, code := run(t, server, "-o", "json", "companies", verb, "unstable-jetscale")
		if code != 0 || decode(t, stdout).(map[string]any)["id"] != testCompanyID {
			t.Fatalf("companies %s: %d %s", verb, code, stdout)
		}
	}
}

func TestQuietPrintsOnlyIDs(t *testing.T) {
	f, server := newFakeBackend(t)
	f.withCompany()
	stdout, _, code := run(t, server, "companies", "list", "-q")
	if code != 0 || strings.TrimSpace(stdout) != testCompanyID {
		t.Fatalf("-q must print the ID alone: %d %q", code, stdout)
	}
}

func TestErrorsCarryTryHintAndTransient(t *testing.T) {
	f, server := newFakeBackend(t)
	f.on("GET /api/v2/organization/companies", func(*http.Request, []byte) (int, any) { return list() })
	stdout, stderr, code := run(t, server, "-o", "json", "companies", "get", "absent-co")
	body := decode(t, stdout).(map[string]any)["error"].(map[string]any)
	if code != ExitNotFound || body["transient"] != false || !strings.Contains(body["try"].(string), "companies list") {
		t.Fatalf("not found must hint the list command: %d %v", code, body)
	}
	if !strings.Contains(stderr, "Error: ") || !strings.Contains(stderr, "Try: ") {
		t.Fatalf("stderr must show Error and Try lines: %q", stderr)
	}
	f.on("GET /api/v2/organization/companies", func(*http.Request, []byte) (int, any) {
		return backendError(http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE")
	})
	stdout, _, code = run(t, server, "-o", "json", "companies", "list")
	if body := decode(t, stdout).(map[string]any)["error"].(map[string]any); code != ExitFailure || body["transient"] != true {
		t.Fatalf("503 on a read must be transient: %d %v", code, body)
	}
	_, stderr, code = run(t, server, "business-units", "list")
	if code != ExitUsage || !strings.Contains(stderr, "--company <company>") {
		t.Fatalf("missing company must hint --company: %d %q", code, stderr)
	}
}

func TestCompanyDefaultsFromEnvironment(t *testing.T) {
	f, server := newFakeBackend(t)
	f.withCompany()
	f.withUnit()
	t.Setenv(buildinfo.EnvVar("COMPANY"), "unstable-jetscale")
	stdout, _, code := run(t, server, "business-units", "list", "-q")
	if code != 0 || strings.TrimSpace(stdout) != testUnitID {
		t.Fatalf("company default must apply: %d %q", code, stdout)
	}
}

// runInteractive runs with stdin treated as a terminal.
func runInteractive(t *testing.T, server *httptest.Server, input string, args ...string) (string, string, int) {
	t.Helper()
	previous := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return true }
	t.Setenv("CI", "")
	defer func() { stdinIsTerminal = previous }()
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(input))
	root.SetArgs(append([]string{"--api-url", server.URL}, args...))
	cmd, err := root.ExecuteC()
	return stdout.String(), stderr.String(), finish(root, cmd, err, &stdout, &stderr)
}

func TestFanOutWritesPromptOnATerminal(t *testing.T) {
	f, server := newFakeBackend(t)
	f.withCompany()
	value := false
	f.on("GET /api/v2/system/feature-flags/company/by-key/CLOUD.FOCUS", func(*http.Request, []byte) (int, any) {
		return item(map[string]any{"id": "77777777-7777-4777-8777-777777777777", "key": "CLOUD.FOCUS", "value": value})
	})
	f.on("PATCH /api/v2/system/feature-flags/company/action/set/CLOUD.FOCUS", func(*http.Request, []byte) (int, any) {
		value = true
		return item(map[string]any{"value": true})
	})
	const patch = "PATCH /api/v2/system/feature-flags/company/action/set/CLOUD.FOCUS"
	_, stderr, code := runInteractive(t, server, "n\n", "-o", "json", "feature-flags", "set", "CLOUD.FOCUS=true", "--company", "unstable-jetscale")
	if code != ExitForbidden || f.count(patch) != 0 || !strings.Contains(stderr, "[y/N]") {
		t.Fatalf("declined prompt must not write: %d %q", code, stderr)
	}
	stdout, stderr, code := runInteractive(t, server, "y\n", "-o", "json", "feature-flags", "set", "CLOUD.FOCUS=true", "--company", "unstable-jetscale")
	if code != 0 || f.count(patch) != 1 || decode(t, stdout).([]any)[0].(map[string]any)["status"] != "updated" {
		t.Fatalf("confirmed prompt must write once and print one JSON document: %d %q %q", code, stdout, stderr)
	}
}

func TestRegenerateNeedsConfirmationWithoutATerminal(t *testing.T) {
	_, server := newFakeBackend(t)
	stdout, _, code := run(t, server, "-o", "json", "recommendations", "regenerate", "3f2c0a6e-0000-4000-8000-000000000000")
	if code != ExitForbidden || !strings.Contains(stdout, "confirmation_required") {
		t.Fatalf("regenerate without -y must refuse: %d %s", code, stdout)
	}
}

func TestAPIRequestsUseTheSessionAndPrintResponses(t *testing.T) {
	f, server := newFakeBackend(t)
	f.on("GET /api/v2/organization/companies", func(r *http.Request, _ []byte) (int, any) {
		if r.URL.Query().Get("slug") != "eq unstable-jetscale" {
			t.Errorf("GET fields must become query parameters: %s", r.URL.RawQuery)
		}
		return list(unstableCompany)
	})
	stdout, _, code := run(t, server, "api", "-X", "GET", "organization/companies", "-f", "slug=eq unstable-jetscale")
	if code != 0 || !strings.Contains(stdout, testCompanyID) {
		t.Fatalf("GET must print the response: %d %s", code, stdout)
	}
	f.on("PATCH /api/v2/system/feature-flags/root/action/set/CLOUD.FOCUS", func(_ *http.Request, body []byte) (int, any) {
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		if payload["value"] != true {
			t.Errorf("-F must keep booleans typed: %s", body)
		}
		return item(map[string]any{"value": true})
	})
	if _, _, code := run(t, server, "api", "-X", "PATCH", "system/feature-flags/root/action/set/CLOUD.FOCUS", "-F", "value=true"); code != 0 {
		t.Fatalf("PATCH failed: %d", code)
	}
	f.on("GET /api/v2/auth/me", func(*http.Request, []byte) (int, any) {
		return backendError(http.StatusForbidden, "GENERIC_PERMISSION_DENIED")
	})
	stdout, _, code = run(t, server, "-o", "json", "api", "/api/v2/auth/me")
	if code != ExitForbidden || strings.Count(stdout, "GENERIC_PERMISSION_DENIED") != 1 {
		t.Fatalf("non-2xx must print the reply once and exit by status: %d %s", code, stdout)
	}
	f.on("POST /api/v2/organization/business-units", func(_ *http.Request, body []byte) (int, any) {
		if !strings.Contains(string(body), `"name":"Acme Clouds"`) {
			t.Errorf("fields without -X must POST a JSON body: %s", body)
		}
		return item(map[string]any{"id": testUnitID})
	})
	if _, _, code := run(t, server, "api", "organization/business-units", "-f", "name=Acme Clouds"); code != 0 {
		t.Fatalf("POST with fields failed: %d", code)
	}
	if _, _, code := run(t, server, "api", "https://evil.example/api/v2/auth/me"); code != ExitUsage {
		t.Fatalf("URLs must be refused, got %d", code)
	}
}

func TestAPIPaginateMergesPages(t *testing.T) {
	f, server := newFakeBackend(t)
	f.on("GET /api/v2/cloud/cloud-accounts", func(r *http.Request, _ []byte) (int, any) {
		page := make([]map[string]any, 0, apiPageSize)
		count := apiPageSize
		if r.URL.Query().Get("api_offset") != "0" {
			count = 1
		}
		for i := 0; i < count; i++ {
			page = append(page, map[string]any{"id": r.URL.Query().Get("api_offset") + "-" + string(rune('a'+i%26))})
		}
		return http.StatusOK, map[string]any{"data": page, "meta": map[string]any{"total_count": apiPageSize + 1}}
	})
	stdout, _, code := run(t, server, "api", "cloud/cloud-accounts", "--paginate")
	var items []any
	if code != 0 || json.Unmarshal([]byte(stdout), &items) != nil || len(items) != apiPageSize+1 {
		t.Fatalf("pages must merge into one array: %d (%d items)", code, len(items))
	}
}
