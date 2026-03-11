//go:build integration

package generated_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/Jetscale-ai/cli/internal/api/generated"
	"github.com/Jetscale-ai/cli/internal/auth"
	"github.com/Jetscale-ai/cli/internal/config"
)

func mustResolveLocal(t *testing.T) (baseURL, token string) {
	t.Helper()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	name, url, err := config.Resolve(cfg, "", true, "")
	if err != nil {
		t.Skipf("no local backend: %v", err)
	}

	tok, err := auth.ResolveToken(name)
	if err != nil || tok == "" {
		t.Skip("not logged in to local — run: jetscale --local auth login")
	}

	return url, tok
}

func bearerAuth(token string) generated.RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
}

func TestGeneratedClient_AuthMe(t *testing.T) {
	baseURL, token := mustResolveLocal(t)

	client, err := generated.NewClientWithResponses(baseURL,
		generated.WithRequestEditorFn(bearerAuth(token)),
	)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	resp, err := client.MeApiV2AuthMeGetWithResponse(context.Background())
	if err != nil {
		t.Fatalf("call /auth/me: %v", err)
	}

	fmt.Fprintf(os.Stderr, "GET /api/v2/auth/me → HTTP %d\n", resp.StatusCode())
	fmt.Fprintf(os.Stderr, "Body: %s\n", string(resp.Body))

	if resp.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode(), string(resp.Body))
	}
}

func TestGeneratedClient_ListCloudAccounts(t *testing.T) {
	baseURL, token := mustResolveLocal(t)

	client, err := generated.NewClientWithResponses(baseURL,
		generated.WithRequestEditorFn(bearerAuth(token)),
	)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	resp, err := client.ListCloudAccountsApiV2CloudCloudAccountsGetWithResponse(context.Background())
	if err != nil {
		t.Fatalf("call /cloud/cloud-accounts: %v", err)
	}

	fmt.Fprintf(os.Stderr, "GET /api/v2/cloud/cloud-accounts → HTTP %d\n", resp.StatusCode())
	fmt.Fprintf(os.Stderr, "Body (first 500 chars): %.500s\n", string(resp.Body))

	if resp.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode(), string(resp.Body))
	}
}

func TestGeneratedClient_ListBusinessUnits(t *testing.T) {
	baseURL, token := mustResolveLocal(t)

	client, err := generated.NewClientWithResponses(baseURL,
		generated.WithRequestEditorFn(bearerAuth(token)),
	)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	resp, err := client.ListBusinessUnitsApiV2OrganizationBusinessUnitsGetWithResponse(context.Background())
	if err != nil {
		t.Fatalf("call /organization/business-units: %v", err)
	}

	fmt.Fprintf(os.Stderr, "GET /api/v2/organization/business-units → HTTP %d\n", resp.StatusCode())
	fmt.Fprintf(os.Stderr, "Body (first 500 chars): %.500s\n", string(resp.Body))

	if resp.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode(), string(resp.Body))
	}
}
