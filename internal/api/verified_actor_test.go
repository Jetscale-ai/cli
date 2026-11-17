package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func actorServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var meCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/auth/me" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		meCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"user": map[string]any{"id": "fetched"}}})
	}))
	t.Cleanup(server.Close)
	return server, &meCalls
}

func TestVerifiedActorSurvivesCloneWithoutRequest(t *testing.T) {
	server, meCalls := actorServer(t)
	base := NewSessionClient(server.URL, server.Client()).WithVerifiedActor(json.RawMessage(`{"id":"verified","is_superuser":true}`))
	client, err := base.Resources()
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.Actor(context.Background())
	if err != nil || first.ID() != "verified" {
		t.Fatalf("verified actor not reused: %v %v", first, err)
	}
	first["id"] = "mutated"
	second, err := client.Actor(context.Background())
	if err != nil || second.ID() != "verified" || meCalls.Load() != 0 {
		t.Fatalf("actor must be an unshared copy and cost no request: %v %v calls=%d", second, err, meCalls.Load())
	}
}

func TestUnusableVerifiedActorFallsBackToAuthMe(t *testing.T) {
	for _, raw := range []string{``, `null`, `[]`, `"user"`, `{`} {
		t.Run(raw, func(t *testing.T) {
			server, meCalls := actorServer(t)
			client := NewSessionClient(server.URL, server.Client()).WithVerifiedActor(json.RawMessage(raw))
			actor, err := client.Actor(context.Background())
			if err != nil || actor.ID() != "fetched" || meCalls.Load() != 1 {
				t.Fatalf("unusable verified user must fall back to /auth/me: %v %v calls=%d", actor, err, meCalls.Load())
			}
		})
	}
}

func TestWithoutVerifiedActorReadsAuthMeAgain(t *testing.T) {
	server, meCalls := actorServer(t)
	verified := NewSessionClient(server.URL, server.Client()).WithVerifiedActor(json.RawMessage(`{"id":"verified"}`))
	actor, err := verified.WithoutVerifiedActor().Actor(context.Background())
	if err != nil || actor.ID() != "fetched" || meCalls.Load() != 1 {
		t.Fatalf("cleared actor must be re-read: %v %v calls=%d", actor, err, meCalls.Load())
	}
	if again, _ := verified.Actor(context.Background()); again.ID() != "verified" || meCalls.Load() != 1 {
		t.Fatal("clearing a copy changed the original client")
	}
}
