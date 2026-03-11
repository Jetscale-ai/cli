package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Jetscale-ai/cli/internal/api/generated"
)

// TokenPayload holds the token pair returned by sign-in and refresh.
type TokenPayload struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// WhoamiResult is the public-facing result of a whoami call.
type WhoamiResult struct {
	Email    string
	Username string
}

// Client wraps the generated API client with auth-specific business logic.
type Client struct {
	gen     *generated.ClientWithResponses
	baseURL string
}

func NewClient(baseURL string) *Client {
	gen, _ := generated.NewClientWithResponses(baseURL)
	return &Client{gen: gen, baseURL: baseURL}
}

// SignIn exchanges credentials for a token pair.
func (c *Client) SignIn(login, password string) (TokenPayload, error) {
	resp, err := c.gen.SignInApiV2AuthSignInPostWithResponse(context.Background(),
		generated.SignInApiV2AuthSignInPostJSONRequestBody{
			Login:    login,
			Password: password,
		},
	)
	if err != nil {
		return TokenPayload{}, fmt.Errorf("connect to %s: %w", c.baseURL, err)
	}

	if resp.StatusCode() == http.StatusUnauthorized || resp.StatusCode() == http.StatusForbidden {
		return TokenPayload{}, fmt.Errorf("invalid credentials (HTTP %d)", resp.StatusCode())
	}
	if resp.StatusCode() >= 300 {
		return TokenPayload{}, fmt.Errorf("sign-in failed (HTTP %d): %s", resp.StatusCode(), truncateBytes(resp.Body, 200))
	}

	return extractTokens(resp.Body, "sign-in")
}

// Refresh exchanges a refresh token for a new token pair.
func (c *Client) Refresh(refreshToken string) (TokenPayload, error) {
	resp, err := c.gen.RefreshTokenApiV2AuthTokenRefreshTokenPostWithResponse(context.Background(),
		generated.RefreshTokenApiV2AuthTokenRefreshTokenPostJSONRequestBody{
			RefreshToken: refreshToken,
		},
	)
	if err != nil {
		return TokenPayload{}, fmt.Errorf("connect to %s: %w", c.baseURL, err)
	}

	if resp.StatusCode() == http.StatusUnauthorized {
		return TokenPayload{}, fmt.Errorf("refresh token expired — please run: jetscale auth login")
	}
	if resp.StatusCode() >= 300 {
		return TokenPayload{}, fmt.Errorf("token refresh failed (HTTP %d): %s", resp.StatusCode(), truncateBytes(resp.Body, 200))
	}

	return extractTokens(resp.Body, "refresh")
}

// Whoami returns the currently authenticated user.
func (c *Client) Whoami(accessToken string) (WhoamiResult, error) {
	resp, err := c.gen.MeApiV2AuthMeGetWithResponse(context.Background(),
		bearerAuth(accessToken),
	)
	if err != nil {
		return WhoamiResult{}, fmt.Errorf("connect to %s: %w", c.baseURL, err)
	}

	if resp.StatusCode() == http.StatusUnauthorized {
		return WhoamiResult{}, fmt.Errorf("not authenticated — run: jetscale auth login")
	}
	if resp.StatusCode() >= 300 {
		return WhoamiResult{}, fmt.Errorf("whoami failed (HTTP %d): %s", resp.StatusCode(), truncateBytes(resp.Body, 200))
	}

	var envelope struct {
		Data struct {
			User struct {
				Email    string `json:"email"`
				Username string `json:"username"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &envelope); err != nil {
		return WhoamiResult{}, fmt.Errorf("decode whoami response: %w", err)
	}
	return WhoamiResult{
		Email:    envelope.Data.User.Email,
		Username: envelope.Data.User.Username,
	}, nil
}

// SignOut invalidates the current session server-side.
func (c *Client) SignOut(accessToken string) error {
	resp, err := c.gen.SignOutApiV2AuthSignOutPostWithResponse(context.Background(),
		bearerAuth(accessToken),
	)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", c.baseURL, err)
	}
	if resp.StatusCode() >= 300 && resp.StatusCode() != http.StatusUnauthorized {
		return fmt.Errorf("sign-out failed (HTTP %d): %s", resp.StatusCode(), truncateBytes(resp.Body, 200))
	}
	return nil
}

// TokenEntryFromPayload converts an API token response into a storable entry.
func TokenEntryFromPayload(p TokenPayload) TokenEntry {
	return TokenEntry{
		AccessToken:  p.AccessToken,
		RefreshToken: p.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(p.ExpiresIn) * time.Second),
	}
}

// EnsureFreshToken checks the stored token and refreshes if expired.
// Returns the valid access token, or empty string if not logged in.
func EnsureFreshToken(instanceName, baseURL string) (string, error) {
	if v := os.Getenv("JETSCALE_TOKEN"); v != "" {
		return v, nil
	}

	entry, ok, err := GetToken(instanceName)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}

	if !entry.Expired() {
		return entry.AccessToken, nil
	}

	if entry.RefreshToken == "" {
		return "", fmt.Errorf("access token expired and no refresh token available — run: jetscale auth login")
	}

	client := NewClient(baseURL)
	tokens, err := client.Refresh(entry.RefreshToken)
	if err != nil {
		return "", err
	}

	newEntry := TokenEntryFromPayload(tokens)
	if err := SetToken(instanceName, newEntry); err != nil {
		return "", fmt.Errorf("save refreshed token: %w", err)
	}
	return newEntry.AccessToken, nil
}

// --- helpers ---

func bearerAuth(token string) generated.RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
}

// extractTokens decodes the {data: {tokens: {...}}} envelope from raw JSON.
func extractTokens(body []byte, label string) (TokenPayload, error) {
	var envelope struct {
		Data struct {
			Tokens TokenPayload `json:"tokens"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return TokenPayload{}, fmt.Errorf("decode %s response: %w", label, err)
	}
	return envelope.Data.Tokens, nil
}

func truncateBytes(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "…"
}
