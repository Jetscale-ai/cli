package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jetscale-ai/cli/internal/api"
	"github.com/Jetscale-ai/cli/internal/auth"
	"github.com/Jetscale-ai/cli/internal/buildinfo"
	"github.com/Jetscale-ai/cli/internal/output"
	"github.com/spf13/cobra"
)

func prepareCommandTest(t *testing.T) string {
	t.Helper()
	previous := buildinfo.Channel
	buildinfo.Channel = "unstable"
	t.Cleanup(func() { buildinfo.Channel = previous })
	dir := t.TempDir()
	t.Setenv("JETSCALE_UNSTABLE_CONFIG_DIR", dir)
	t.Setenv("JETSCALE_UNSTABLE_API_URL", "")
	t.Setenv("JETSCALE_UNSTABLE_INSTANCE", "")
	t.Setenv("JETSCALE_UNSTABLE_TOKEN", "")
	return dir
}

func prepareManifestFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type prepareCountingInput struct{ reads int }

func (r *prepareCountingInput) Read([]byte) (int, error) {
	r.reads++
	return 0, errors.New("synthetic password must not be read")
}

const prepareManifestFixture = `{"company":{"name":"Unstable Jetscale","slug":"unstable-jetscale","is_root":false,"domain_emails":["jetscale.ai"]},"license":{"pricing_plan_id":"10000000-0000-0000-0000-000000000001"},"business_unit":{"name":"Unstable Jetscale Clouds","slug":"unstable-jetscale-clouds"}}`

func TestPrepareBasicsValidateOnlyOffline(t *testing.T) {
	dir := prepareCommandTest(t)
	// Invalid ambient credentials must not be read during offline validation.
	t.Setenv("JETSCALE_UNSTABLE_TOKEN", "")
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(prepareManifestFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"prepare", "basics", "--manifest", path, "--validate-only", "--output", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("offline preflight failed: %v", err)
	}
	var result struct {
		Accepted     bool   `json:"accepted"`
		Target       string `json:"target"`
		ValidateOnly bool   `json:"validate_only"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if !result.Accepted || !result.ValidateOnly || result.Target != "https://unstable.jetscale.ai" {
		t.Fatalf("unexpected preflight: %+v", result)
	}
	if stderr.Len() != 0 {
		t.Fatalf("offline preflight emitted auth diagnostics: %q", stderr.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("offline preflight wrote session/config files: %v, %v", entries, err)
	}
}

func TestPrepareBasicsRejectsBeforeAuthentication(t *testing.T) {
	cases := []struct {
		name, body, want string
		args             []string
	}{
		{"positional", prepareManifestFixture, "unknown command", []string{"other-tenant"}},
		{"missing-manifest", "", "--manifest is required", nil},
		{"empty-manifest", "", "--manifest is required", []string{"--manifest="}},
		{"malformed", `{"company":`, "invalid JSON manifest", nil},
		{"null", `null`, "manifest must target", nil},
		{"trailing-object", prepareManifestFixture + `{}`, "invalid JSON manifest", nil},
		{"trailing-garbage", prepareManifestFixture + `sensitive-sentinel`, "invalid JSON manifest", nil},
		{"unknown-top", strings.Replace(prepareManifestFixture, `"company":`, `"sensitive-sentinel":true,"company":`, 1), "invalid JSON manifest", nil},
		{"unknown-nested", strings.Replace(prepareManifestFixture, `"name":"Unstable Jetscale"`, `"name":"Unstable Jetscale","sensitive-sentinel":true`, 1), "invalid JSON manifest", nil},
		{"bu-id", strings.Replace(prepareManifestFixture, `"name":"Unstable Jetscale Clouds"`, `"id":"sensitive-sentinel","name":"Unstable Jetscale Clouds"`, 1), "invalid JSON manifest", nil},
		{"company-name", strings.Replace(prepareManifestFixture, "Unstable Jetscale\"", "sensitive-sentinel\"", 1), "manifest must target", nil},
		{"company-slug", strings.Replace(prepareManifestFixture, `"unstable-jetscale"`, `"other-tenant"`, 1), "manifest must target", nil},
		{"root-company", strings.Replace(prepareManifestFixture, `"is_root":false`, `"is_root":true`, 1), "manifest must target", nil},
		{"domain", strings.Replace(prepareManifestFixture, `"jetscale.ai"`, `"other.test"`, 1), "manifest must target", nil},
		{"extra-domain", strings.Replace(prepareManifestFixture, `["jetscale.ai"]`, `["jetscale.ai","other.test"]`, 1), "manifest must target", nil},
		{"bu-name", strings.Replace(prepareManifestFixture, "Unstable Jetscale Clouds", "Other Clouds", 1), "manifest must target", nil},
		{"bu-slug", strings.Replace(prepareManifestFixture, "unstable-jetscale-clouds", "other-clouds", 1), "manifest must target", nil},
		{"missing-bu", strings.Replace(prepareManifestFixture, `,"business_unit":{"name":"Unstable Jetscale Clouds","slug":"unstable-jetscale-clouds"}`, "", 1), "manifest must target", nil},
		{"null-bu", strings.Replace(prepareManifestFixture, `{"name":"Unstable Jetscale Clouds","slug":"unstable-jetscale-clouds"}`, `null`, 1), "manifest must target", nil},
		{"nil-plan", strings.Replace(prepareManifestFixture, "10000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000000", 1), "invalid manifest", nil},
		{"invalid-license", strings.Replace(prepareManifestFixture, `"pricing_plan_id":`, `"valid_until":"2020-01-01T00:00:00Z","pricing_plan_id":`, 1), "invalid manifest", nil},
		{"flag-disable", strings.Replace(prepareManifestFixture, `"license":`, `"flags":{"root":{"CLOUD.FOCUS":false}},"license":`, 1), "invalid manifest", nil},
		{"unknown-flag", strings.Replace(prepareManifestFixture, `"license":`, `"flags":{"company":{"sensitive-sentinel":true}},"license":`, 1), "invalid manifest", nil},
		{"unknown-setting", strings.Replace(prepareManifestFixture, `"license":`, `"settings":{"root":{"sensitive-sentinel":{"value_type":"boolean","value":true}}},"license":`, 1), "invalid manifest", nil},
		{"bad-setting-type", strings.Replace(prepareManifestFixture, `"license":`, `"settings":{"company":{"enable_caching":{"value_type":"string","value":"sensitive-sentinel"}}},"license":`, 1), "invalid manifest", nil},
		{"bad-setting-value", strings.Replace(prepareManifestFixture, `"license":`, `"settings":{"root":{"recommendation_temperature":{"value_type":"number","value":2}}},"license":`, 1), "invalid manifest", nil},
		{"output", prepareManifestFixture, "output must be", []string{"--output", "sensitive-sentinel"}},
		{"credential-file", prepareManifestFixture, "not supported", []string{"--credentials-file", "sensitive-sentinel"}},
		{"conflicting-modes", prepareManifestFixture, "cannot be combined", []string{"--validate-only", "--apply"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := prepareCommandTest(t)
			root := newRootCmd()
			input := &prepareCountingInput{}
			var stdout, stderr bytes.Buffer
			root.SetIn(input)
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			args := []string{"prepare", "basics", "--login", "synthetic@example.invalid", "--password-stdin"}
			if tc.body != "" {
				args = append(args, "--manifest", prepareManifestFile(t, tc.body))
			}
			root.SetArgs(append(args, tc.args...))
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
			if input.reads != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("preflight reached auth/output: reads=%d stdout=%q stderr=%q", input.reads, stdout.String(), stderr.String())
			}
			if strings.Contains(err.Error(), "sensitive-sentinel") {
				t.Fatalf("manifest or flag data leaked in error: %v", err)
			}
			entries, readErr := os.ReadDir(dir)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("preflight wrote files: %v %v", entries, readErr)
			}
		})
	}
}

func TestPrepareBasicsManifestFileBounds(t *testing.T) {
	for _, scenario := range []string{"missing", "directory", "symlink", "oversized", "device"} {
		t.Run(scenario, func(t *testing.T) {
			prepareCommandTest(t)
			path := filepath.Join(t.TempDir(), "manifest.json")
			switch scenario {
			case "directory":
				path = t.TempDir()
			case "symlink":
				if err := os.Symlink(prepareManifestFile(t, prepareManifestFixture), path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			case "oversized":
				path = prepareManifestFile(t, prepareManifestFixture+strings.Repeat(" ", (1<<20)+1))
			case "device":
				if _, err := os.Stat("/dev/null"); err != nil {
					t.Skip("no /dev/null on this platform")
				}
				path = "/dev/null"
			}
			root := newRootCmd()
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs([]string{"prepare", "basics", "--manifest", path, "--validate-only"})
			if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "regular JSON file") {
				t.Fatalf("unbounded/non-regular manifest accepted: %v", err)
			}
		})
	}
}

func TestPrepareBasicsValidateOnlyRejectsAuthFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--login", "synthetic@example.invalid"}, {"--login="},
		{"--password-stdin"}, {"--password-stdin=false"},
		{"--credentials-file", "not-read"}, {"--credentials-file="},
		{"--ca-file", "not-read"}, {"--ca-file="},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			prepareCommandTest(t)
			root := newRootCmd()
			input := &prepareCountingInput{}
			root.SetIn(input)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs(append([]string{"prepare", "basics", "--manifest", prepareManifestFile(t, prepareManifestFixture), "--validate-only"}, args...))
			if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "authentication flags") {
				t.Fatalf("offline mode accepted authentication flags: %v", err)
			}
			if input.reads != 0 {
				t.Fatal("offline mode read password input")
			}
		})
	}
}

func TestPrepareBasicsRequiresUnstableTarget(t *testing.T) {
	for _, channel := range []string{"live", "local", "unstable"} {
		for _, target := range []string{
			"https://unstable.jetscale.ai", "https://unstable.jetscale.ai/", "https://unstable.jetscale.ai:443",
			"http://unstable.jetscale.ai", "https://console.jetscale.ai", "https://unstable.jetscale.ai.other.test",
			"https://synthetic-sentinel@unstable.jetscale.ai", "https://unstable.jetscale.ai?synthetic-sentinel", "https://unstable.jetscale.ai/#synthetic-sentinel",
		} {
			for _, offline := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/offline=%t", channel, target, offline), func(t *testing.T) {
					prepareCommandTest(t)
					buildinfo.Channel = channel
					t.Setenv(buildinfo.EnvVar("CONFIG_DIR"), t.TempDir())
					root := newRootCmd()
					root.SetOut(io.Discard)
					root.SetErr(io.Discard)
					args := []string{"prepare", "basics", "--manifest", prepareManifestFile(t, prepareManifestFixture), "--api-url", target}
					if offline {
						args = append(args, "--validate-only")
					}
					root.SetArgs(args)
					err := root.Execute()
					allowed := channel == "unstable" && target == "https://unstable.jetscale.ai"
					if allowed && offline {
						if err != nil {
							t.Fatalf("exact offline target rejected: %v", err)
						}
					} else if allowed {
						if err == nil || !strings.Contains(err.Error(), "TOKEN") {
							t.Fatalf("normal mode did not reach the synthetic invalid auth: %v", err)
						}
					} else if err == nil || (!strings.Contains(err.Error(), "unstable-channel") && !strings.Contains(err.Error(), "exact https://unstable.jetscale.ai")) {
						t.Fatalf("wrong channel/origin not rejected: %v", err)
					}
					if err != nil && strings.Contains(err.Error(), "synthetic-sentinel") {
						t.Fatal("untrusted target leaked in error")
					}
				})
			}
		}
	}
}

func TestPrepareBasicsOfflineResolvesTargetWithoutAutoProbe(t *testing.T) {
	for _, scenario := range []string{"default", "instance", "environment", "auto", "local", "unknown-instance"} {
		t.Run(scenario, func(t *testing.T) {
			dir := prepareCommandTest(t)
			root := newRootCmd()
			var stdout bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(io.Discard)
			args := []string{"prepare", "basics", "--manifest", prepareManifestFile(t, prepareManifestFixture), "--validate-only"}
			switch scenario {
			case "instance":
				args = append(args, "--instance", "unstable")
			case "environment":
				t.Setenv("JETSCALE_UNSTABLE_API_URL", "https://console.jetscale.ai")
			case "auto":
				if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("default_instance: probe\ninstances:\n  probe:\n    api_url: auto\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "local":
				args = append(args, "--local")
			case "unknown-instance":
				args = append(args, "--instance", "synthetic-sentinel")
			}
			root.SetArgs(args)
			err := root.Execute()
			if scenario == "default" || scenario == "instance" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "exact https://unstable.jetscale.ai") || stdout.Len() != 0 {
				t.Fatalf("wrong resolved target accepted: %v, %q", err, stdout.String())
			}
		})
	}
}

func TestPrepareBasicsOfflineOutputSanitized(t *testing.T) {
	for _, format := range []string{"json", "yaml", "table"} {
		t.Run(format, func(t *testing.T) {
			prepareCommandTest(t)
			t.Setenv("NO_COLOR", "1")
			t.Setenv("TERM", "dumb")
			body := strings.Replace(prepareManifestFixture, `"license":`, `"settings":{"root":{"enable_caching":{"value_type":"boolean","value":true}}},"license":`, 1)
			body = strings.Replace(body, `"name":"Unstable Jetscale"`, `"name":"Unstable Jetscale","contact_email":"synthetic-contact-not-output"`, 1)
			root := newRootCmd()
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetArgs([]string{"prepare", "basics", "--manifest", prepareManifestFile(t, body), "--validate-only", "--output", format})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			out := stdout.String()
			if !strings.Contains(out, "accepted") || !strings.Contains(out, "validate_only") || !strings.Contains(out, "https://unstable.jetscale.ai") || stderr.Len() != 0 {
				t.Fatalf("bad output contract: stdout=%q stderr=%q", out, stderr.String())
			}
			for _, forbidden := range []string{"synthetic-contact", "contact_email", "settings", "value_type", "enable_caching", "pricing_plan_id", "\x1b", "\r"} {
				if strings.Contains(out, forbidden) {
					t.Fatalf("unsafe output contains %q", forbidden)
				}
			}
		})
	}
}

func TestPrepareBasicsHelpIsOffline(t *testing.T) {
	prepareCommandTest(t)
	for _, args := range [][]string{{"prepare", "--help"}, {"prepare", "basics", "--help"}} {
		root := newRootCmd()
		var stdout bytes.Buffer
		root.SetOut(&stdout)
		root.SetArgs(args)
		if err := root.Execute(); err != nil || !strings.Contains(stdout.String(), "basics") {
			t.Fatalf("help not available offline: %v %q", err, stdout.String())
		}
	}
}

func TestPrepareBasicsResultExitStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result api.BasicsResult
		err    error
		fail   bool
	}{
		{"ready", api.BasicsResult{Ready: true}, nil, false},
		{"plan-not-ready", api.BasicsResult{Actions: []api.BasicsAction{{Resource: "setting", Operation: "create", Status: "planned"}}}, nil, false},
		{"blocker", api.BasicsResult{Blockers: []api.BasicsBlocker{{Resource: "managed_agent", Reason: "external_state_unknown"}}}, nil, true},
		{"blocked-action", api.BasicsResult{Actions: []api.BasicsAction{{Resource: "setting", Status: "blocked"}}}, nil, true},
		{"uncertain", api.BasicsResult{OutcomeUncertain: true}, nil, true},
		{"uncertain-action", api.BasicsResult{Actions: []api.BasicsAction{{Resource: "setting", Status: "outcome_uncertain"}}}, nil, true},
		{"request-error", api.BasicsResult{}, errors.New("synthetic-body-not-output"), true},
		{"error-action", api.BasicsResult{Actions: []api.BasicsAction{{Resource: "setting", Status: "error"}}}, nil, true},
	} {
		for _, format := range []output.Format{output.JSON, output.YAML, output.Table} {
			t.Run(tc.name+"/"+string(format), func(t *testing.T) {
				var stdout bytes.Buffer
				err := printBasicsResult(output.Printer{Format: format, Out: &stdout}, tc.result, tc.err)
				if (err != nil) != tc.fail {
					t.Fatalf("incorrect exit result: %v", err)
				}
				if !strings.Contains(stdout.String(), "outcome_uncertain") || !strings.Contains(stdout.String(), "actions") {
					t.Fatalf("sanitized result missing: %q", stdout.String())
				}
				if err != nil && strings.Contains(err.Error(), "synthetic-body") {
					t.Fatal("request error body leaked")
				}
			})
		}
	}
}

func TestPrepareBasicsDeadlineBeforeEagerAuthAndCleanup(t *testing.T) {
	prepareCommandTest(t)
	root := newRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"prepare", "basics", "--manifest", prepareManifestFile(t, prepareManifestFixture), "--login", "synthetic@example.invalid", "--password-stdin"})
	// Explicit empty TOKEN is rejected by the eager lifecycle, before child RunE.
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "TOKEN") {
		t.Fatalf("expected eager auth failure, got %v", err)
	}
	cmd, _, err := root.Find([]string{"prepare", "basics"})
	if err != nil {
		t.Fatal(err)
	}
	deadline, ok := cmd.Context().Deadline()
	if !ok || time.Until(deadline) > 3*time.Minute || time.Until(deadline) < 2*time.Minute {
		t.Fatalf("missing pre-auth bound: deadline=%v", deadline)
	}
	if !errors.Is(cmd.Context().Err(), context.Canceled) {
		t.Fatalf("eager auth failure did not release deadline: %v", cmd.Context().Err())
	}
}

func TestPrepareBasicsPreflightPinsTarget(t *testing.T) {
	prepareCommandTest(t)
	root := newRootCmd()
	leaf, _, err := root.Find([]string{"prepare", "basics"})
	if err != nil {
		t.Fatal(err)
	}
	preflight := leaf.PreRunE
	leaf.PreRunE = func(cmd *cobra.Command, args []string) error {
		if err := preflight(cmd, args); err != nil {
			return err
		}
		// Neither changed flags nor ambient auto configuration may retarget auth.
		flagAPIURL = ""
		flagLocal = true
		t.Setenv(buildinfo.EnvVar("API_URL"), "http://not-contacted.invalid")
		instance, origin, err := resolveSessionTarget(cmd)
		if err != nil || instance != "unstable" || origin != buildinfo.UnstableURL {
			t.Fatalf("preflight target not pinned: %q %q %v", instance, origin, err)
		}
		return nil
	}
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"prepare", "basics", "--manifest", prepareManifestFile(t, prepareManifestFixture), "--login", "synthetic@example.invalid", "--password-stdin"})
	// Empty explicit TOKEN prevents any network or password read.
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "TOKEN") {
		t.Fatalf("expected offline auth rejection: %v", err)
	}
}

func TestPrepareBasicsPreservesShorterParentDeadline(t *testing.T) {
	prepareCommandTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := newRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"prepare", "basics", "--manifest", prepareManifestFile(t, prepareManifestFixture)})
	if err := root.ExecuteContext(ctx); err == nil || !strings.Contains(err.Error(), "TOKEN") {
		t.Fatalf("expected synthetic auth failure: %v", err)
	}
	cmd, _, err := root.Find([]string{"prepare", "basics"})
	if err != nil {
		t.Fatal(err)
	}
	deadline, _ := cmd.Context().Deadline()
	parentDeadline, _ := ctx.Deadline()
	if !deadline.Equal(parentDeadline) || ctx.Err() != nil || !errors.Is(cmd.Context().Err(), context.Canceled) {
		t.Fatalf("incorrect deadline/cleanup: child=%v parent=%v error=%v", deadline, parentDeadline, cmd.Context().Err())
	}
}

func TestPrepareBasicsCanceledBeforeSession(t *testing.T) {
	prepareCommandTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := newRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"prepare", "basics", "--manifest", prepareManifestFile(t, prepareManifestFixture)})
	if err := root.ExecuteContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled command reached session acquisition: %v", err)
	}
}

type prepareFailedOutput struct{}

func (prepareFailedOutput) Write([]byte) (int, error) {
	return 0, errors.New("synthetic output unavailable")
}

func TestPrepareBasicsOutputFailureIsNonzero(t *testing.T) {
	for _, format := range []output.Format{output.JSON, output.YAML, output.Table} {
		if err := printBasicsResult(output.Printer{Format: format, Out: prepareFailedOutput{}}, api.BasicsResult{Ready: true}, nil); err == nil {
			t.Fatalf("%s output failure silently succeeded", format)
		}
	}
}

// An in-memory session transport exercises real API behavior without changing
// the allowed origin, opening a socket, or using credentials.
type prepareFixtureTransport struct {
	t       *testing.T
	apply   bool
	reads   int
	writes  int
	caching bool
}

func (f *prepareFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Helper()
	if r.URL.Scheme != "https" || r.URL.Host != "unstable.jetscale.ai" {
		f.t.Fatalf("wrong fixture destination: %s", r.URL)
	}
	deadline, ok := r.Context().Deadline()
	if !ok || time.Until(deadline) > 3*time.Minute {
		f.t.Fatal("API request has no overall deadline")
	}

	if r.Method == http.MethodGet {
		f.reads++
	} else {
		f.writes++
		if !f.apply {
			f.t.Fatal("default assessment attempted a write")
		}
	}
	const company = "20000000-0000-0000-0000-000000000001"
	var body string
	switch r.URL.Path {
	case "/api/v2/auth/me":
		body = `{"data":{"user":{"id":"50000000-0000-0000-0000-000000000001","is_superuser":true}}}`
	case "/api/v2/organization/companies":
		if r.URL.Query().Get("slug") != "eq unstable-jetscale" {
			f.t.Fatal("unscoped company read")
		}
		body = `{"data":[{"id":"` + company + `","name":"Unstable Jetscale","slug":"unstable-jetscale","is_root":false,"domain_emails":["jetscale.ai"],"status":"ACTIVE"}]}`
	case "/api/v2/license-and-billing/pricing-plans":
		body = `{"data":[{"id":"10000000-0000-0000-0000-000000000001","is_active":true,"max_accounts":3,"max_business_units":1,"max_seats":1,"max_granted_tokens":100,"performance_fee_enabled":false,"performance_fee_rate_bps":0,"price_per_month":0,"price_per_year":0}]}`
	case "/api/v2/license-and-billing/licenses":
		body = `{"data":[{"id":"30000000-0000-0000-0000-000000000001","company":"` + company + `","pricing_plan":"10000000-0000-0000-0000-000000000001","status":"ACTIVE","max_accounts":3,"max_business_units":1,"max_seats":1,"max_granted_tokens":100,"available_tokens":100,"valid_from":"2020-01-01T00:00:00Z","valid_until":null,"billing_interval":"YEARLY","performance_fee_enabled":null,"performance_fee_rate_bps":null,"price_per_month":null,"price_per_year":null}]}`
	case "/api/v2/organization/business-units":
		if r.URL.Query().Get("company") != "eq "+company || r.URL.Query().Get("slug") != "eq unstable-jetscale-clouds" {
			f.t.Fatal("unscoped BU read")
		}
		body = `{"data":[{"id":"40000000-0000-0000-0000-000000000001","company":"` + company + `","name":"Unstable Jetscale Clouds","slug":"unstable-jetscale-clouds","managed_agent":"50000000-0000-0000-0000-000000000001"}]}`
	case "/api/v2/system/root-settings/by-key/enable_caching":
		body = fmt.Sprintf(`{"data":{"id":"70000000-0000-0000-0000-000000000001","key":"enable_caching","value_type":"boolean","value":{"value":%t}}}`, f.caching)
	case "/api/v2/settings/effective/enable_caching":
		body = fmt.Sprintf(`{"data":{"key":"enable_caching","value_type":"boolean","value":%t,"scope":"root","scope_chain":[{"scope":"root","value":%t,"source":"root","setting_id":"70000000-0000-0000-0000-000000000001"}]}}`, f.caching, f.caching)
	case "/api/v2/system/root-settings/70000000-0000-0000-0000-000000000001":
		if r.Method != http.MethodPatch {
			f.t.Fatal("setting mutation must use PATCH")
		}
		var payload struct {
			ValueType string          `json:"value_type"`
			Value     map[string]bool `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.ValueType != "boolean" || !payload.Value["value"] || len(payload.Value) != 1 {
			f.t.Fatalf("incorrect generated setting body: %+v %v", payload, err)
		}
		f.caching = true
		body = `{"data":{}}`
	default:
		f.t.Fatalf("unexpected API request: %s %s", r.Method, r.URL.Path)
	}
	if strings.Contains(body, `"data":[`) {
		body = strings.TrimSuffix(body, "}") + `,"meta":{"count":1,"total_count":1,"offset":0,"limit":100}}`
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func TestPrepareBasicsAssessmentAndApply(t *testing.T) {
	for _, apply := range []bool{false, true} {
		t.Run(fmt.Sprintf("apply=%t", apply), func(t *testing.T) {
			prepareCommandTest(t)
			body := strings.Replace(prepareManifestFixture, `"license":`, `"settings":{"root":{"enable_caching":{"value_type":"boolean","value":true}}},"license":`, 1)
			root := newRootCmd()
			existing, _, err := root.Find([]string{"prepare"})
			if err != nil {
				t.Fatal(err)
			}
			root.RemoveCommand(existing)
			prepare := newPrepareCmd()
			root.AddCommand(prepare)
			leaf := prepare.Commands()[0]
			fixture := &prepareFixtureTransport{t: t, apply: apply}
			t.Setenv(buildinfo.EnvVar("TOKEN"), "synthetic-token")
			session, err := auth.OpenSession("unstable", buildinfo.UnstableURL, "")
			if err != nil {
				t.Fatal(err)
			}
			session.HTTPClient.CloseIdleConnections()
			session.HTTPClient.Transport = fixture
			// Only acquisition/persistence is bypassed by an injected test session.
			preflight := leaf.PreRunE
			leaf.PreRunE = func(cmd *cobra.Command, args []string) error {
				if err := preflight(cmd, args); err != nil {
					return err
				}
				cmd.SetContext(context.WithValue(cmd.Context(), sessionContextKey{}, &commandSessionState{session: session}))
				return nil
			}
			wrapPrepareDeadlineCleanup(prepare)
			var stdout bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(io.Discard)
			args := []string{"prepare", "basics", "--manifest", prepareManifestFile(t, body), "--output", "json"}
			if apply {
				args = append(args, "--apply")
			}
			root.SetArgs(args)
			err = root.Execute()
			if err == nil || !strings.Contains(err.Error(), "blocked") {
				t.Fatalf("external-agent blocker must return nonzero: %v, %s", err, stdout.String())
			}
			var result api.BasicsResult
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if fixture.reads == 0 || result.Apply != apply || result.Ready || len(result.Blockers) != 1 || result.Blockers[0].Reason != "external_state_unknown" {
				t.Fatalf("unexpected assessment: %+v", result)
			}
			wantStatus, wantWrites := "planned", 0
			if apply {
				wantStatus, wantWrites = "applied", 1
			}
			if fixture.writes != wantWrites || len(result.Actions) != 4 || result.Actions[3].Status != wantStatus {
				t.Fatalf("wrong explicit apply behavior: writes=%d actions=%+v", fixture.writes, result.Actions)
			}
			if !errors.Is(leaf.Context().Err(), context.Canceled) {
				t.Fatal("command did not cancel its deadline")
			}
		})
	}
}
