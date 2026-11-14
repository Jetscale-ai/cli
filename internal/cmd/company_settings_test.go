package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Jetscale-ai/cli/internal/api"
)

func runCompanySettings(t *testing.T, server *httptest.Server, args ...string) (string, string, int) {
	t.Helper()
	root := newRootCmd()

	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append([]string{"--api-url", server.URL, "company-settings"}, args...))
	cmd, err := root.ExecuteC()
	code := finish(root, cmd, err, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func commandSetting(key, kind string, value any) map[string]any {
	return map[string]any{"id": testUnitID, "company": testCompanyID, "key": key, "value_type": kind, "value": map[string]any{"value": value}, "secret": "synthetic-secret-not-for-output"}
}

func TestCompanySettingsCommandCreateThenNoop(t *testing.T) {
	f, server := newFakeBackend(t)
	f.withCompany()
	var row map[string]any
	f.on("GET /api/v2/system/company-settings/by-key/"+api.CompanySettingWorkOSOrg, func(r *http.Request, _ []byte) (int, any) {
		if r.URL.Query().Get("company_id") != testCompanyID {
			t.Error("read not scoped")
		}
		if row == nil {
			return backendError(404, "NOT_FOUND")
		}
		return item(row)
	})
	f.on("POST /api/v2/system/company-settings", func(_ *http.Request, body []byte) (int, any) {
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		if len(payload) != 4 || payload["value_type"] != "string" || payload["company"] != testCompanyID || payload["value"].(map[string]any)["value"] != "org_fixture" {
			t.Errorf("wrong payload: %v", payload)
		}
		row = commandSetting(api.CompanySettingWorkOSOrg, "string", "org_fixture")
		return 201, map[string]any{"data": row}
	})
	for _, status := range []string{"created", "noop"} {
		stdout, stderr, code := runCompanySettings(t, server, "ensure", api.CompanySettingWorkOSOrg, "--company", "unstable-jetscale", "--value", "org_fixture", "--yes", "-o", "json")
		result := decode(t, stdout).(map[string]any)
		if code != 0 || result["status"] != status || result["id"] != testUnitID || stderr != "" || strings.Contains(stdout, "synthetic-secret") {
			t.Fatalf("code=%d out=%s err=%s", code, stdout, stderr)
		}
	}
	if f.count("POST /api/v2/system/company-settings") != 1 {
		t.Fatal("rerun wrote twice")
	}
}

func TestCompanySettingsCommandPolicyGet(t *testing.T) {
	for _, format := range []string{"json", "yaml", "table", "quiet"} {
		t.Run(format, func(t *testing.T) {
			f, server := newFakeBackend(t)
			f.withCompany()
			f.on("GET /api/v2/system/company-settings/by-key/"+api.CompanySettingWorkOSRequired, func(*http.Request, []byte) (int, any) {
				return item(commandSetting(api.CompanySettingWorkOSRequired, "boolean", false))
			})
			args := []string{"get", api.CompanySettingWorkOSRequired, "--company", "unstable-jetscale"}
			if format == "quiet" {
				args = append(args, "-q")
			} else {
				args = append(args, "-o", format)
			}
			stdout, stderr, code := runCompanySettings(t, server, args...)
			if code != 0 || stderr != "" || strings.Contains(stdout, "synthetic-secret") {
				t.Fatalf("code=%d out=%s err=%s", code, stdout, stderr)
			}
			if format == "json" {
				row := decode(t, stdout).(map[string]any)
				if row["required"] != false || row["value"] != false {
					t.Fatalf("wrong policy: %v", row)
				}
			}
			if format == "quiet" && stdout != testUnitID+"\n" {
				t.Fatalf("quiet=%q", stdout)
			}
			if format == "table" && !strings.Contains(stdout, "required:") {
				t.Fatalf("table=%q", stdout)
			}
		})
	}
}

func TestCompanySettingsCommandPlansRefusesAndDrifts(t *testing.T) {
	for _, mode := range []string{"dry-run", "unconfirmed", "drift"} {
		t.Run(mode, func(t *testing.T) {
			f, server := newFakeBackend(t)
			f.withCompany()
			f.on("GET /api/v2/system/company-settings/by-key/"+api.CompanySettingWorkOSOrg, func(*http.Request, []byte) (int, any) {
				if mode == "drift" {
					return item(commandSetting(api.CompanySettingWorkOSOrg, "string", "other_org"))
				}
				return backendError(404, "NOT_FOUND")
			})
			args := []string{"ensure", api.CompanySettingWorkOSOrg, "--company", "unstable-jetscale", "--value", "org_fixture", "-o", "json"}
			wantCode, wantStatus := 0, "planned"
			switch mode {
			case "dry-run":
				args = append(args, "--dry-run")
			case "unconfirmed":
				wantCode = ExitForbidden
			case "drift":
				args = append(args, "--yes")
				wantCode, wantStatus = ExitDrift, "drift"
			}
			stdout, _, code := runCompanySettings(t, server, args...)
			result := decode(t, stdout).(map[string]any)
			if code != wantCode || result["status"] != wantStatus {
				t.Fatalf("code=%d out=%s", code, stdout)
			}
			if f.count("POST /api/v2/system/company-settings") != 0 {
				t.Fatal("unexpected create")
			}
		})
	}
}

func TestCompanySettingsCommandPreflightAndOfflineHelp(t *testing.T) {
	for _, args := range [][]string{
		{"get", "synthetic-secret-key"},
		{"ensure", api.CompanySettingWorkOSRequired, "--value", "true"},
		{"ensure", api.CompanySettingWorkOSOrg},
		{"ensure", api.CompanySettingWorkOSOrg, "--value", ""},
		{"ensure", api.CompanySettingWorkOSOrg, "--value", "value\n"},
		{"ensure", api.CompanySettingWorkOSOrg, "--value", "org_fixture", "--yes", "--dry-run"},
	} {
		f, server := newFakeBackend(t)
		stdout, stderr, code := runCompanySettings(t, server, append(args, "-o", "json")...)
		if code != ExitUsage || strings.Contains(stdout+stderr, "synthetic-secret-key") {
			t.Fatalf("code=%d out=%s err=%s", code, stdout, stderr)
		}
		if len(f.requests) != 0 {
			t.Fatal("preflight reached API")
		}
	}
	f, server := newFakeBackend(t)
	stdout, stderr, code := runCompanySettings(t, server, "ensure", "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "--value") || len(f.requests) != 0 {
		t.Fatalf("help code=%d out=%s err=%s", code, stdout, stderr)
	}
}

func TestCompanySettingsPreflightDoesNotReadLoginStdin(t *testing.T) {
	_, server := newFakeBackend(t)
	root := newRootCmd()

	input := &flagsCountingInput{}
	root.SetIn(input)
	root.SetArgs([]string{"--api-url", server.URL, "company-settings", "ensure", "unsupported", "--login", "fixture@example.invalid", "--password-stdin", "--value", "org_fixture"})
	if err := root.Execute(); err == nil || input.reads != 0 {
		t.Fatalf("err=%v reads=%d", err, input.reads)
	}
}

func TestCompanySettingsCommandPersistedThenServerError(t *testing.T) {
	for _, status := range []int{500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			f, server := newFakeBackend(t)
			f.withCompany()
			var row map[string]any
			f.on("GET /api/v2/system/company-settings/by-key/"+api.CompanySettingWorkOSOrg, func(*http.Request, []byte) (int, any) {
				if row == nil {
					return backendError(404, "NOT_FOUND")
				}
				return item(row)
			})
			f.on("POST /api/v2/system/company-settings", func(*http.Request, []byte) (int, any) {
				row = commandSetting(api.CompanySettingWorkOSOrg, "string", "org_fixture")
				return backendError(status, "INTERNAL_SERVER_ERROR")
			})
			stdout, stderr, code := runCompanySettings(t, server, "ensure", api.CompanySettingWorkOSOrg, "--company", "unstable-jetscale", "--value", "org_fixture", "--yes", "-o", "json")
			errBody := decode(t, stdout).(map[string]any)["error"].(map[string]any)
			if code != ExitFailure || errBody["reason"] != "outcome_uncertain" || errBody["transient"] != false || f.count("POST /api/v2/system/company-settings") != 1 || strings.Contains(stdout+stderr, "internal detail") {
				t.Fatalf("unsafe mutation outcome: code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			stdout, stderr, code = runCompanySettings(t, server, "get", api.CompanySettingWorkOSOrg, "--company", "unstable-jetscale", "-o", "json")
			if code != 0 || decode(t, stdout).(map[string]any)["value"] != "org_fixture" || f.count("POST /api/v2/system/company-settings") != 1 {
				t.Fatalf("persisted setting not readable without retry: code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
		})
	}
}

func TestCompanySettingsCommandMissingAndUncertain(t *testing.T) {
	for _, mode := range []string{"missing", "uncertain"} {
		f, server := newFakeBackend(t)
		f.withCompany()
		f.on("GET /api/v2/system/company-settings/by-key/"+api.CompanySettingWorkOSOrg, func(*http.Request, []byte) (int, any) { return backendError(404, "NOT_FOUND") })
		args := []string{"get", api.CompanySettingWorkOSOrg, "--company", "unstable-jetscale", "-o", "json"}
		wantCode, reason := ExitNotFound, "not_found"
		if mode == "uncertain" {
			f.on("POST /api/v2/system/company-settings", func(*http.Request, []byte) (int, any) { return 504, map[string]any{"secret": "synthetic-secret"} })
			args[0] = "ensure"
			args = append(args, "--value", "org_fixture", "--yes")
			wantCode, reason = ExitFailure, "outcome_uncertain"
		}
		stdout, stderr, code := runCompanySettings(t, server, args...)
		result := decode(t, stdout).(map[string]any)["error"].(map[string]any)
		if code != wantCode || result["reason"] != reason || strings.Contains(stdout+stderr, "synthetic-secret") {
			t.Fatalf("code=%d out=%s err=%s", code, stdout, stderr)
		}
	}
}
