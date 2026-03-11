package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Jetscale-ai/cli/internal/api/generated"
)

// Business-logic types that the rest of the CLI uses.

type MeUser struct {
	ID        string  `json:"id"`
	Email     string  `json:"email"`
	Username  string  `json:"username"`
	Company   *string `json:"company"`
	FirstName string  `json:"first_name"`
	LastName  string  `json:"last_name"`
}

type BusinessUnit struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type CloudAccount struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	CloudProviderType string `json:"cloud_provider_type"`
	BusinessUnit      string `json:"business_unit"`
	Description       string `json:"description"`
}

type AccountTree struct {
	User          MeUser
	BusinessUnits []BusinessUnit
	Accounts      []CloudAccount
}

// Client wraps the generated API client with account-discovery business logic.
type Client struct {
	gen     *generated.ClientWithResponses
	baseURL string
}

func NewClient(baseURL, accessToken string) *Client {
	gen, _ := generated.NewClientWithResponses(baseURL,
		generated.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+accessToken)
			return nil
		}),
	)
	return &Client{gen: gen, baseURL: baseURL}
}

func (c *Client) Me() (MeUser, error) {
	resp, err := c.gen.MeApiV2AuthMeGetWithResponse(context.Background())
	if err != nil {
		return MeUser{}, fmt.Errorf("connect to %s: %w", c.baseURL, err)
	}
	if resp.StatusCode() == http.StatusUnauthorized {
		return MeUser{}, fmt.Errorf("not authenticated — run: jetscale auth login")
	}
	if resp.StatusCode() >= 300 {
		return MeUser{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode(), truncate(string(resp.Body), 200))
	}

	var envelope struct {
		Data struct {
			User MeUser `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &envelope); err != nil {
		return MeUser{}, fmt.Errorf("decode /auth/me: %w", err)
	}
	return envelope.Data.User, nil
}

func (c *Client) ListBusinessUnits(companyID string) ([]BusinessUnit, error) {
	resp, err := c.gen.ListBusinessUnitsApiV2OrganizationBusinessUnitsGetWithResponse(context.Background())
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", c.baseURL, err)
	}
	if resp.StatusCode() >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode(), truncate(string(resp.Body), 200))
	}

	var envelope struct {
		Data []BusinessUnit `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &envelope); err != nil {
		return nil, fmt.Errorf("decode business-units: %w", err)
	}
	return envelope.Data, nil
}

func (c *Client) ListCloudAccounts(unitIDs []string) ([]CloudAccount, error) {
	if len(unitIDs) == 0 {
		return nil, nil
	}

	resp, err := c.gen.ListCloudAccountsApiV2CloudCloudAccountsGetWithResponse(context.Background())
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", c.baseURL, err)
	}
	if resp.StatusCode() >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode(), truncate(string(resp.Body), 200))
	}

	var envelope struct {
		Data []CloudAccount `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &envelope); err != nil {
		return nil, fmt.Errorf("decode cloud-accounts: %w", err)
	}
	return envelope.Data, nil
}

// FetchAccountTree performs the 3-call chain to discover all accessible accounts.
func (c *Client) FetchAccountTree() (AccountTree, error) {
	me, err := c.Me()
	if err != nil {
		return AccountTree{}, err
	}
	if me.Company == nil {
		return AccountTree{User: me}, nil
	}

	bus, err := c.ListBusinessUnits(*me.Company)
	if err != nil {
		return AccountTree{}, err
	}

	unitIDs := make([]string, len(bus))
	for i, bu := range bus {
		unitIDs[i] = bu.ID
	}

	accounts, err := c.ListCloudAccounts(unitIDs)
	if err != nil {
		return AccountTree{}, err
	}

	return AccountTree{
		User:          me,
		BusinessUnits: bus,
		Accounts:      accounts,
	}, nil
}

// FindAccountByName returns the account matching the given name (case-insensitive).
func FindAccountByName(accounts []CloudAccount, name string) (CloudAccount, bool) {
	lower := strings.ToLower(name)
	for _, a := range accounts {
		if strings.ToLower(a.Name) == lower {
			return a, true
		}
	}
	for _, a := range accounts {
		if strings.HasPrefix(a.ID, name) {
			return a, true
		}
	}
	return CloudAccount{}, false
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
