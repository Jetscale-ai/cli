package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const usersTestEmail = "agent@jetscale.ai"

func runUsers(t *testing.T, server *httptest.Server, args ...string) (string, string, int) {
	t.Helper()
	root := newRootCmd()

	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append([]string{"--api-url", server.URL}, args...))
	cmd, err := root.ExecuteC()
	returnCode := finish(root, cmd, err, &stdout, &stderr)
	return stdout.String(), stderr.String(), returnCode
}

func usersCommandRecord(super bool, status string) map[string]any {
	return map[string]any{"id": testUserID, "email": usersTestEmail, "is_superuser": super, "status": status, "company": testCompanyID, "signup_completed_at": "2026-11-01T00:00:00Z", "password": "synthetic-secret"}
}

func usersBackend(t *testing.T, records ...map[string]any) (*fakeBackend, *httptest.Server) {
	t.Helper()
	f, server := newFakeBackend(t)
	f.withCompany()
	f.on("GET /api/v2/auth/me", func(*http.Request, []byte) (int, any) {
		return item(map[string]any{"user": usersCommandRecord(true, "ACTIVE")})
	})
	f.on("GET /api/v2/auth/users", func(r *http.Request, _ []byte) (int, any) {
		if r.URL.Query().Get("username") == "eq "+usersTestEmail {
			return list()
		}
		if r.URL.Query().Get("email") != "eq "+usersTestEmail {
			t.Errorf("not an exact lookup: %s", r.URL)
		}
		return list(records...)
	})
	f.on("GET /api/v2/auth/user-invitations", func(r *http.Request, _ []byte) (int, any) {
		if r.URL.Query().Get("invitee_email") != "eq "+usersTestEmail || r.URL.Query().Has("company") {
			t.Errorf("unsafe invitation scope: %s", r.URL)
		}
		return list()
	})
	return f, server
}

func TestUsersCommandGetProjection(t *testing.T) {
	record := usersCommandRecord(true, "ACTIVE")
	record["company"] = map[string]any{"id": testCompanyID, "token": "synthetic-secret"}
	_, server := usersBackend(t, record)
	stdout, stderr, code := runUsers(t, server, "users", "get", usersTestEmail, "-o", "json")
	if code != 0 {
		t.Fatalf("get failed: %d %s %s", code, stdout, stderr)
	}
	body := decode(t, stdout).(map[string]any)
	if len(body) != 6 || body["company"] != testCompanyID || body["is_superuser"] != true || strings.Contains(stdout+stderr, "synthetic-secret") {
		t.Fatalf("unsafe projection: %s %s", stdout, stderr)
	}
}

func TestUsersCommandDryRunAndNoop(t *testing.T) {
	for _, tc := range []struct {
		name    string
		records []map[string]any
		status  string
	}{
		{"missing", nil, "planned"},
		{"promote", []map[string]any{usersCommandRecord(false, "ACTIVE")}, "planned"},
		{"noop", []map[string]any{usersCommandRecord(true, "ACTIVE")}, "noop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, server := usersBackend(t, tc.records...)
			stdout, stderr, code := runUsers(t, server, "users", "ensure-superuser", usersTestEmail, "--company", "unstable-jetscale", "--dry-run", "-o", "json")
			if code != 0 {
				t.Fatalf("failed: %d %s %s", code, stdout, stderr)
			}
			body := decode(t, stdout).(map[string]any)
			details := body["details"].(map[string]any)
			if body["status"] != tc.status || details["company"] != testCompanyID || details["is_superuser"] != true || f.count("POST /api/v2/auth/action/send-signup-invitation") != 0 || f.count("PATCH /api/v2/auth/users/"+testUserID) != 0 {
				t.Fatalf("unsafe plan: %s", stdout)
			}
		})
	}
}

func TestUsersCommandUsageBeforeAnyRequest(t *testing.T) {
	for _, args := range [][]string{
		{"users", "get", "Agent <agent@jetscale.ai>"},
		{"users", "get", " agent@jetscale.ai"},
		{"users", "ensure-superuser", usersTestEmail, "--yes"},
		{"users", "ensure-superuser", usersTestEmail, "--company", " Unstable Jetscale ", "--yes"},
		{"users", "ensure-superuser", usersTestEmail, "--company", "unstable-jetscale", "--yes", "--dry-run"},
	} {
		f, server := usersBackend(t)
		stdout, stderr, code := runUsers(t, server, append(args, "-o", "json")...)
		if code != ExitUsage || len(f.requests) != 0 {
			t.Fatalf("must reject before API: %v => %d %s %s requests=%v", args, code, stdout, stderr, f.requests)
		}
	}
}

func TestUsersCommandPromotionPayloadAndConfirmation(t *testing.T) {
	f, server := usersBackend(t, usersCommandRecord(false, "ACTIVE"))
	f.on("PATCH /api/v2/auth/users/"+testUserID, func(_ *http.Request, body []byte) (int, any) {
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		if len(payload) != 1 || payload["is_superuser"] != true {
			t.Errorf("elevation payload leaked extra fields: %s", body)
		}
		return item(usersCommandRecord(true, "ACTIVE"))
	})
	stdout, stderr, code := runUsers(t, server, "users", "ensure-superuser", usersTestEmail, "--company", "Unstable Jetscale", "-o", "json")
	if code != ExitForbidden || !strings.Contains(stdout, "confirmation_required") || f.count("PATCH /api/v2/auth/users/"+testUserID) != 0 {
		t.Fatalf("must refuse without --yes: %d %s %s", code, stdout, stderr)
	}
	stdout, stderr, code = runUsers(t, server, "users", "ensure-superuser", usersTestEmail, "--company", "Unstable Jetscale", "--yes", "-o", "json")
	if code != 0 || decode(t, stdout).(map[string]any)["status"] != "updated" || f.count("PATCH /api/v2/auth/users/"+testUserID) != 1 || strings.Contains(stdout+stderr, "synthetic-secret") {
		t.Fatalf("unsafe promotion: %d %s %s", code, stdout, stderr)
	}
}

func TestUsersCommandQueuedInvitationDuplicateAndConflict(t *testing.T) {
	f, server := usersBackend(t)
	pending := usersCommandRecord(true, "PENDING_ACTIVATION")
	pending["signup_completed_at"] = nil
	invitation := map[string]any{"id": testAccountID, "invitee_email": usersTestEmail, "company": testCompanyID, "invitation_type": "SIGNUP_SUPERUSER", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), "used_at": nil, "token": "synthetic-secret"}
	f.on("POST /api/v2/auth/action/send-signup-invitation", func(_ *http.Request, body []byte) (int, any) {
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		if len(payload) != 3 || payload["company_id"] != testCompanyID || payload["invitee_email"] != usersTestEmail || payload["invitation_type"] != "SIGNUP_SUPERUSER" {
			t.Errorf("unsafe invite payload: %s", body)
		}
		f.on("GET /api/v2/auth/users", func(*http.Request, []byte) (int, any) { return list(pending) })
		f.on("GET /api/v2/auth/user-invitations", func(*http.Request, []byte) (int, any) { return list(invitation) })
		return item(invitation)
	})
	args := []string{"users", "ensure-superuser", usersTestEmail, "--company", "unstable-jetscale", "--yes", "-o", "json"}
	for i := 0; i < 2; i++ {
		stdout, stderr, code := runUsers(t, server, args...)
		if code != 0 {
			t.Fatalf("failed: %d %s %s", code, stdout, stderr)
		}
		result := decode(t, stdout).(map[string]any)
		if result["status"] != "pending" || result["details"].(map[string]any)["signup_completed_at"] != nil || f.count("POST /api/v2/auth/action/send-signup-invitation") != 1 || strings.Contains(stdout+stderr, "synthetic-secret") {
			t.Fatalf("must remain pending without resend: %s %s", stdout, stderr)
		}
	}
	pending["company"] = testOtherUnit
	stdout, stderr, code := runUsers(t, server, args...)
	if code != ExitConflict || !strings.Contains(stdout, "company_mismatch") || f.count("POST /api/v2/auth/action/send-signup-invitation") != 1 {
		t.Fatalf("must block company mismatch: %d %s %s", code, stdout, stderr)
	}
}

func TestUsersCommandMissingSignupEvidenceAndSanitizedErrors(t *testing.T) {
	record := usersCommandRecord(true, "ACTIVE")
	delete(record, "signup_completed_at")
	f, server := usersBackend(t, record)
	stdout, stderr, code := runUsers(t, server, "users", "ensure-superuser", usersTestEmail, "--company", "unstable-jetscale", "--yes", "-o", "json")
	if code != ExitConflict || !strings.Contains(stdout, "signup_completion_unproved") || strings.Contains(stdout+stderr, "synthetic-secret") {
		t.Fatalf("unproved completion must block: %d %s %s", code, stdout, stderr)
	}
	f.on("GET /api/v2/auth/users", func(*http.Request, []byte) (int, any) { return backendError(403, "DENIED") })
	stdout, stderr, code = runUsers(t, server, "users", "get", usersTestEmail, "-o", "json")
	if code != ExitForbidden || strings.Contains(stdout+stderr, "internal detail") {
		t.Fatalf("unsafe error: %d %s %s", code, stdout, stderr)
	}
}

func TestUsersCommandYAMLUsesStableKeys(t *testing.T) {
	_, server := usersBackend(t, usersCommandRecord(true, "ACTIVE"))
	stdout, stderr, code := runUsers(t, server, "users", "ensure-superuser", usersTestEmail, "--company", "unstable-jetscale", "--dry-run", "-o", "yaml")
	if code != 0 || !strings.Contains(stdout, "is_superuser: true") || !strings.Contains(stdout, "signup_completed_at:") || strings.Contains(stdout+stderr, "synthetic-secret") {
		t.Fatalf("unstable YAML keys: %d %s %s", code, stdout, stderr)
	}
}

func TestUsersCommandCompanyResponseMustMatchSelector(t *testing.T) {
	f, server := usersBackend(t)
	f.on("GET /api/v2/organization/companies", func(*http.Request, []byte) (int, any) {
		return list(map[string]any{"id": testCompanyID, "name": "Other Company", "slug": "other"})
	})
	stdout, stderr, code := runUsers(t, server, "users", "ensure-superuser", usersTestEmail, "--company", "unstable-jetscale", "--yes", "-o", "json")
	if code != ExitConflict || !strings.Contains(stdout, "selector_mismatch") || f.count("GET /api/v2/auth/users") != 0 {
		t.Fatalf("wrong company allowed: %d %s %s", code, stdout, stderr)
	}
}

func TestUsersCommandPersistedThenServerError(t *testing.T) {
	for _, operation := range []string{"promotion", "invitation"} {
		for _, status := range []int{500, 503} {
			t.Run(operation+"/"+http.StatusText(status), func(t *testing.T) {
				var records []map[string]any
				if operation == "promotion" {
					records = []map[string]any{usersCommandRecord(false, "ACTIVE")}
				}
				f, server := usersBackend(t, records...)
				write := "PATCH /api/v2/auth/users/" + testUserID
				if operation == "invitation" {
					write = "POST /api/v2/auth/action/send-signup-invitation"
				}
				f.on(write, func(*http.Request, []byte) (int, any) {
					user := usersCommandRecord(true, "ACTIVE")
					if operation == "invitation" {
						user["status"], user["signup_completed_at"] = "PENDING_ACTIVATION", nil
						invitation := map[string]any{"id": testAccountID, "invitee_email": usersTestEmail, "company": testCompanyID, "invitation_type": "SIGNUP_SUPERUSER", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), "used_at": nil}
						f.on("GET /api/v2/auth/user-invitations", func(*http.Request, []byte) (int, any) { return list(invitation) })
					}
					f.on("GET /api/v2/auth/users", func(*http.Request, []byte) (int, any) { return list(user) })
					return backendError(status, "INTERNAL_SERVER_ERROR")
				})
				stdout, stderr, code := runUsers(t, server, "users", "ensure-superuser", usersTestEmail, "--company", "unstable-jetscale", "--yes", "-o", "json")
				errBody := decode(t, stdout).(map[string]any)["error"].(map[string]any)
				if code != ExitFailure || errBody["reason"] != "outcome_uncertain" || errBody["transient"] != false || f.count(write) != 1 || strings.Contains(stdout+stderr, "internal detail") {
					t.Fatalf("unsafe mutation outcome: code=%d stdout=%s stderr=%s", code, stdout, stderr)
				}
				stdout, stderr, code = runUsers(t, server, "users", "ensure-superuser", usersTestEmail, "--company", "unstable-jetscale", "--dry-run", "-o", "json")
				want := "noop"
				if operation == "invitation" {
					want = "pending"
				}
				result := decode(t, stdout).(map[string]any)
				if code != 0 || result["status"] != want || result["details"].(map[string]any)["is_superuser"] != true || f.count(write) != 1 {
					t.Fatalf("persisted user not readable without retry: code=%d stdout=%s stderr=%s", code, stdout, stderr)
				}
			})
		}
	}
}

func TestUsersCommandHelpOffline(t *testing.T) {
	f, server := usersBackend(t)
	stdout, _, code := runUsers(t, server, "users", "ensure-superuser", "--help")
	if code != 0 || !strings.Contains(stdout, "platform-wide") || len(f.requests) != 0 {
		t.Fatalf("offline help failed: %d %s", code, stdout)
	}
}
