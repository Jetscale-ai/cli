package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const usersCompanyID = "11111111-1111-4111-8111-111111111111"
const usersUserID = "22222222-2222-4222-8222-222222222222"
const usersInvitationID = "33333333-3333-4333-8333-333333333333"
const usersEmail = "agent@jetscale.ai"

func usersRecord(super bool, status string) Record {
	return Record{"id": usersUserID, "email": usersEmail, "is_superuser": super, "status": status, "company": usersCompanyID, "signup_completed_at": "2026-11-01T00:00:00Z"}
}

type usersFixture struct {
	users       []Record
	invitations []Record
	writes      int
	hook        func(http.ResponseWriter, *http.Request) bool
}

func (f *usersFixture) client(t *testing.T) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "GET" {
			f.writes++
		}
		if f.hook != nil && f.hook(w, r) {
			return
		}
		var data any
		switch r.URL.Path {
		case "/api/v2/auth/me":
			data = map[string]any{"user": usersRecord(true, "ACTIVE")}
		case "/api/v2/auth/users":
			if r.Method != "GET" || (r.URL.Query().Get("email") != "eq "+usersEmail && r.URL.Query().Get("username") != "eq "+usersEmail) {
				t.Errorf("lookup must use exact email: %s", r.URL)
			}
			if f.users == nil {
				f.users = []Record{}
			}
			data = f.users
		case "/api/v2/auth/user-invitations":
			if r.Method != "GET" || r.URL.Query().Get("invitee_email") != "eq "+usersEmail || r.URL.Query().Has("company") {
				t.Errorf("invitation lookup must use exact email across companies: %s", r.URL)
			}
			if f.invitations == nil {
				f.invitations = []Record{}
			}
			data = f.invitations
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(server.Close)
	return NewSessionClient(server.URL, server.Client())
}

func TestUsersGetCuratedProjection(t *testing.T) {
	record := usersRecord(true, "ACTIVE")
	record["company"] = map[string]any{"id": usersCompanyID, "token": "synthetic-secret"}
	record["password"] = "synthetic-secret"
	f := &usersFixture{users: []Record{record}}
	user, err := f.client(t).GetUser(context.Background(), usersEmail)
	if err != nil {
		t.Fatal(err)
	}
	projection := user.Project()
	if len(projection) != 6 || projection["company"] != usersCompanyID || projection["signup_completed_at"] != "2026-11-01T00:00:00Z" || projection["is_superuser"] != true {
		t.Fatalf("unexpected projection: %#v", projection)
	}
}

func TestUsersEnsureActive(t *testing.T) {
	for _, super := range []bool{true, false} {
		t.Run(map[bool]string{true: "noop", false: "promote"}[super], func(t *testing.T) {
			f := &usersFixture{users: []Record{usersRecord(super, "ACTIVE")}}
			f.hook = func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method != "PATCH" {
					return false
				}
				if r.URL.Path != "/api/v2/auth/users/"+usersUserID {
					t.Errorf("wrong patch target: %s", r.URL)
				}
				var payload Record
				_ = json.NewDecoder(r.Body).Decode(&payload)
				if len(payload) != 1 || payload["is_superuser"] != true {
					t.Errorf("elevation must contain ONLY is_superuser:true: %#v", payload)
				}
				f.users = []Record{usersRecord(true, "ACTIVE")}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": f.users[0]})
				return true
			}
			result, err := f.client(t).EnsureSuperuser(context.Background(), usersEmail, usersCompanyID, false)
			wantStatus, wantWrites := StatusUpdated, 1
			if super {
				wantStatus, wantWrites = StatusNoop, 0
			}
			if err != nil || result.Status != wantStatus || result.Details["is_superuser"] != true || f.writes != wantWrites {
				t.Fatalf("wrong result: %#v err=%v writes=%d", result, err, f.writes)
			}
		})
	}
}

func usersInvitation() Record {
	return Record{"id": usersInvitationID, "invitee_email": usersEmail, "company": usersCompanyID, "invitation_type": "SIGNUP_SUPERUSER", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), "used_at": nil, "token": "synthetic-secret"}
}

func TestUsersEnsureInvitationQueuedAndRerun(t *testing.T) {
	f := &usersFixture{}
	f.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != "POST" {
			return false
		}
		if r.URL.Path != "/api/v2/auth/action/send-signup-invitation" {
			t.Errorf("must use company-scoped signup invitation: %s", r.URL)
		}
		var payload Record
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if len(payload) != 3 || payload["company_id"] != usersCompanyID || payload["invitee_email"] != usersEmail || payload["invitation_type"] != "SIGNUP_SUPERUSER" {
			t.Errorf("wrong invitation payload: %#v", payload)
		}
		u := usersRecord(true, "PENDING_ACTIVATION")
		u["signup_completed_at"] = nil
		f.users, f.invitations = []Record{u}, []Record{usersInvitation()}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": f.invitations[0]})
		return true
	}
	client := f.client(t)
	for i := 0; i < 2; i++ {
		result, err := client.EnsureSuperuser(context.Background(), usersEmail, usersCompanyID, false)
		if err != nil || result.Status != "pending" || result.ID != usersUserID || result.Details["company"] != usersCompanyID || result.Details["is_superuser"] != true || result.Details["signup_completed_at"] != nil || f.writes != 1 {
			t.Fatalf("must remain pending without resend: %#v err=%v writes=%d", result, err, f.writes)
		}
		raw, _ := json.Marshal(result)
		if strings.Contains(string(raw), "synthetic-secret") || len(result.Details) != 6 {
			t.Fatalf("invitation leaked into result: %s", raw)
		}
	}
}

func TestUsersEnsureConflictsNeverWrite(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		change       func(*usersFixture)
	}{
		{"company", "company_mismatch", func(f *usersFixture) { f.users[0]["company"] = usersInvitationID }},
		{"no_company", "company_mismatch", func(f *usersFixture) { f.users[0]["company"] = nil }},
		{"signup_hidden", "signup_completion_unproved", func(f *usersFixture) { delete(f.users[0], "signup_completed_at") }},
		{"signup_null", "signup_completion_unproved", func(f *usersFixture) { f.users[0]["signup_completed_at"] = nil }},
		{"pending", "signup_reconciliation_required", func(f *usersFixture) {
			f.users[0]["status"] = "PENDING_ACTIVATION"
			f.users[0]["signup_completed_at"] = nil
		}},
		{"disabled", "signup_reconciliation_required", func(f *usersFixture) { f.users[0]["status"] = "DEACTIVATED" }},
		{"superuser_hidden", "superuser_state_unproved", func(f *usersFixture) { delete(f.users[0], "is_superuser") }},
		{"wrong_email", "email_mismatch", func(f *usersFixture) { f.users[0]["email"] = "someone@jetscale.ai" }},
		{"invitation_company", "invitation_conflict", func(f *usersFixture) { i := usersInvitation(); i["company"] = usersUserID; f.invitations = []Record{i} }},
		{"invitation_type", "invitation_conflict", func(f *usersFixture) {
			i := usersInvitation()
			i["invitation_type"] = "SIGNUP_MEMBER"
			f.invitations = []Record{i}
		}},
		{"invite_no_user", "invitation_without_user", func(f *usersFixture) { f.users = nil; f.invitations = []Record{usersInvitation()} }},
		{"expired_no_user", "invitation_without_user", func(f *usersFixture) {
			f.users = nil
			i := usersInvitation()
			i["expires_at"] = "2000-01-01T00:00:00Z"
			f.invitations = []Record{i}
		}},
		{"used_no_user", "invitation_without_user", func(f *usersFixture) {
			f.users = nil
			i := usersInvitation()
			i["used_at"] = "2026-01-01T00:00:00Z"
			f.invitations = []Record{i}
		}},
		{"active_with_invite", "invitation_reconciliation_required", func(f *usersFixture) { f.invitations = []Record{usersInvitation()} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, dryRun := range []bool{true, false} {
				f := &usersFixture{users: []Record{usersRecord(false, "ACTIVE")}}
				tc.change(f)
				_, err := f.client(t).EnsureSuperuser(context.Background(), usersEmail, usersCompanyID, dryRun)
				var conflict *ConflictError
				if !errors.As(err, &conflict) || conflict.Reason != tc.reason || f.writes != 0 {
					t.Fatalf("must block %s without writes (dry=%v): err=%v writes=%d", tc.reason, dryRun, err, f.writes)
				}
			}
		})
	}
}

func TestUsersCompletedMemberInvitationHistoryDoesNotBlockPromotion(t *testing.T) {
	for _, inviteType := range []string{"SIGNUP_MEMBER", "SIGNUP_OWNER"} {
		f := &usersFixture{users: []Record{usersRecord(false, "ACTIVE")}}
		invitation := usersInvitation()
		invitation["invitation_type"] = inviteType
		invitation["used_at"] = "2026-11-01T00:00:00Z"
		f.invitations = []Record{invitation}
		res, err := f.client(t).EnsureSuperuser(context.Background(), usersEmail, usersCompanyID, true)
		if err != nil || res.Status != StatusPlanned || f.writes != 0 {
			t.Fatalf("completed historical signup must not force resend or prevent promotion: %#v %v", res, err)
		}
	}
}

func TestUsersEnsurePromotionDryRun(t *testing.T) {
	f := &usersFixture{users: []Record{usersRecord(false, "ACTIVE")}}
	r, err := f.client(t).EnsureSuperuser(context.Background(), usersEmail, usersCompanyID, true)
	if err != nil || r.Status != StatusPlanned || r.ID != usersUserID || r.Details["is_superuser"] != true || r.Details["status"] != "ACTIVE" || f.writes != 0 {
		t.Fatalf("incorrect promotion plan: %#v err=%v writes=%d", r, err, f.writes)
	}
}

func TestUsersEnsureWriteErrorsSanitizedAndNotRetried(t *testing.T) {
	for _, status := range []int{403, 409, 500, 502, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			f := &usersFixture{}
			f.hook = func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method != "POST" {
					return false
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"detail":{"data":{"error":{"api_error_name":"DENIED","causes":["synthetic-secret"]}}}}`))
				return true
			}
			_, err := f.client(t).EnsureSuperuser(context.Background(), usersEmail, usersCompanyID, false)
			if err == nil || strings.Contains(err.Error(), "synthetic-secret") || f.writes != 1 {
				t.Fatalf("unsafe error/retry: %v writes=%d", err, f.writes)
			}
			if status >= 500 && !errors.Is(err, ErrUncertain) {
				t.Fatalf("partial invitation creation must be uncertain: %v", err)
			}
		})
	}
}

func TestUsersGetUnknownStatusRejected(t *testing.T) {
	f := &usersFixture{users: []Record{usersRecord(true, "synthetic-secret")}}
	_, err := f.client(t).GetUser(context.Background(), usersEmail)
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("unknown status must not leak: %v", err)
	}
}

func TestUsersActorVisibilityAndIdentityAmbiguity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		actor Record
	}{
		{"ordinary", usersRecord(false, "ACTIVE")},
		{"pending_actor", usersRecord(true, "PENDING_ACTIVATION")},
		{"actor_no_id", Record{"is_superuser": true, "status": "ACTIVE"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &usersFixture{}
			f.hook = func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/api/v2/auth/me" {
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"user": tc.actor}})
					return true
				}
				t.Errorf("lookup must not continue without global visibility: %s", r.URL)
				return true
			}
			_, err := f.client(t).EnsureSuperuser(context.Background(), usersEmail, usersCompanyID, false)
			var denied *APIError
			if !errors.As(err, &denied) || denied.Status != 403 || f.writes != 0 {
				t.Fatalf("unproved actor allowed: %v", err)
			}
		})
	}
	u1, u2 := usersRecord(true, "ACTIVE"), usersRecord(true, "ACTIVE")
	u2["id"] = usersInvitationID
	f := &usersFixture{users: []Record{u1, u2}}
	_, err := f.client(t).EnsureSuperuser(context.Background(), usersEmail, usersCompanyID, false)
	var ambiguous *AmbiguousError
	if !errors.As(err, &ambiguous) || f.writes != 0 {
		t.Fatalf("ambiguous email allowed: %v", err)
	}
}

func TestUsersUsernameCollisionNeverInvites(t *testing.T) {
	f := &usersFixture{}
	f.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Query().Get("username") == "eq "+usersEmail {
			other := usersRecord(false, "ACTIVE")
			other["email"] = "someone@jetscale.ai"
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []Record{other}})
			return true
		}
		return false
	}
	_, err := f.client(t).EnsureSuperuser(context.Background(), usersEmail, usersCompanyID, false)
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.Reason != "username_in_use" || f.writes != 0 {
		t.Fatalf("collision allowed: %v writes=%d", err, f.writes)
	}
}

func TestUsersWriteResponseMustProveRequestedState(t *testing.T) {
	for _, mutation := range []string{"id", "email", "company", "is_superuser", "status", "signup_completed_at", "malformed"} {
		t.Run(mutation, func(t *testing.T) {
			f := &usersFixture{users: []Record{usersRecord(false, "ACTIVE")}}
			f.hook = func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method != "PATCH" {
					return false
				}
				after := usersRecord(true, "ACTIVE")
				switch mutation {
				case "id", "company":
					after[mutation] = usersInvitationID
				case "email":
					after[mutation] = "someone@jetscale.ai"
				case "is_superuser":
					after[mutation] = false
				case "status":
					after[mutation] = "DEACTIVATED"
				case "signup_completed_at":
					delete(after, mutation)
				case "malformed":
					_, _ = w.Write([]byte(`{"data":null}`))
					return true
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": after})
				return true
			}
			_, err := f.client(t).EnsureSuperuser(context.Background(), usersEmail, usersCompanyID, false)
			if !errors.Is(err, ErrUncertain) || f.writes != 1 {
				t.Fatalf("must be uncertain, not successful/retried: %v writes=%d", err, f.writes)
			}
		})
	}
}

func TestUsersInvitationMetadataRequired(t *testing.T) {
	for _, mutation := range []string{"email", "expires", "used", "duplicate"} {
		t.Run(mutation, func(t *testing.T) {
			u := usersRecord(true, "PENDING_ACTIVATION")
			u["signup_completed_at"] = nil
			f := &usersFixture{users: []Record{u}, invitations: []Record{usersInvitation()}}
			switch mutation {
			case "email":
				f.invitations[0]["invitee_email"] = "someone@jetscale.ai"
			case "expires":
				f.invitations[0]["expires_at"] = "synthetic-secret"
			case "used":
				delete(f.invitations[0], "used_at")
			case "duplicate":
				other := usersInvitation()
				other["id"] = usersCompanyID
				f.invitations = append(f.invitations, other)
			}
			_, err := f.client(t).EnsureSuperuser(context.Background(), usersEmail, usersCompanyID, false)
			if err == nil || strings.Contains(err.Error(), "synthetic-secret") || f.writes != 0 {
				t.Fatalf("invalid invitation allowed: %v writes=%d", err, f.writes)
			}
		})
	}
}

func TestUsersArgumentsRejectedBeforeTransport(t *testing.T) {
	client := &Client{}
	for _, email := range []string{"", "agent", "Agent <agent@jetscale.ai>", " agent@jetscale.ai", "agent@jetscale.ai\n"} {
		if _, err := client.GetUser(context.Background(), email); err == nil {
			t.Fatalf("invalid email accepted: %q", email)
		}
	}
	for _, company := range []string{"", "unstable-jetscale", "00000000-0000-0000-0000-000000000000", "11111111111141118111111111111111"} {
		if _, err := client.EnsureSuperuser(context.Background(), usersEmail, company, true); err == nil {
			t.Fatalf("invalid company accepted: %q", company)
		}
	}
}

func TestUsersEnsureDryRun(t *testing.T) {
	f := &usersFixture{}
	result, err := f.client(t).EnsureSuperuser(context.Background(), usersEmail, usersCompanyID, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusPlanned || result.Details["company"] != usersCompanyID || result.Details["is_superuser"] != true || result.Details["status"] != "PENDING_ACTIVATION" || f.writes != 0 {
		t.Fatalf("unsafe or incorrect plan: %#v; writes=%d", result, f.writes)
	}
}
