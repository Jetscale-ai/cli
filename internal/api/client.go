package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Jetscale-ai/cli/internal/api/generated"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
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

type Recommendation struct {
	ID                         string                `json:"id"`
	AwsRecommendationID        string                `json:"aws_recommendation_id"`
	ResourceName               string                `json:"resource_name"`
	Region                     string                `json:"region"`
	CurrentResourceType        string                `json:"current_resource_type"`
	RecommendedResourceType    string                `json:"recommended_resource_type"`
	ActionType                 string                `json:"action_type"`
	Reason                     string                `json:"reason"`
	ImplementationEffort       string                `json:"implementation_effort"`
	RestartNeeded              bool                  `json:"restart_needed"`
	RollbackPossible           string                `json:"rollback_possible"`
	ProcessingStatus           string                `json:"processing_status"`
	SavingsPercentage          float64               `json:"savings_percentage"`
	Savings                    SavingsInfo           `json:"savings"`
	Cost                       SavingsInfo           `json:"cost"`
	CurrentResourceSummary     []ResourceSummaryItem `json:"current_resource_summary"`
	RecommendedResourceSummary []ResourceSummaryItem `json:"recommended_resource_summary"`
	GenerationID               string                `json:"generation_id"`
	CreatedAt                  string                `json:"created_at"`
	UpdatedAt                  string                `json:"updated_at"`
}

type SavingsInfo struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
	Cycle    string  `json:"cycle"`
}

type ResourceSummaryItem struct {
	ResourceType string `json:"resource_type"`
	Type         string `json:"type"`
	ActionType   string `json:"action_type,omitempty"`
	Arn          string `json:"arn,omitempty"`
	ResourceID   string `json:"resource_id,omitempty"`
	ResourceName string `json:"resource_name,omitempty"`
}

type RecommendationListMeta struct {
	Count      int `json:"count"`
	TotalCount int `json:"total_count"`
	Limit      int `json:"limit"`
	Offset     int `json:"offset"`
}

type RecommendationListResult struct {
	Items []Recommendation
	Meta  RecommendationListMeta
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

// NewSessionClient uses the supplied HTTP client's session cookies and transport.
// It does not inject an Authorization header.
func NewSessionClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	gen, _ := generated.NewClientWithResponses(baseURL, generated.WithHTTPClient(httpClient))
	return &Client{gen: gen, baseURL: baseURL}
}

func (c *Client) Me() (MeUser, error) {
	resp, err := c.gen.MeApiV2AuthMeGetWithResponse(context.Background())
	if err != nil {
		return MeUser{}, fmt.Errorf("API request failed")
	}
	if resp.StatusCode() == http.StatusUnauthorized {
		return MeUser{}, fmt.Errorf("not authenticated — run: jetscale auth login")
	}
	if resp.StatusCode() >= 300 {
		return MeUser{}, fmt.Errorf("HTTP %d", resp.StatusCode())
	}

	var envelope struct {
		Data struct {
			User MeUser `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &envelope); err != nil {
		return MeUser{}, fmt.Errorf("decode /auth/me: invalid response")
	}
	return envelope.Data.User, nil
}

func (c *Client) ListBusinessUnits(companyID string) ([]BusinessUnit, error) {
	resp, err := c.gen.ListBusinessUnitsApiV2OrganizationBusinessUnitsGetWithResponse(context.Background())
	if err != nil {
		return nil, fmt.Errorf("API request failed")
	}
	if resp.StatusCode() >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode())
	}

	var envelope struct {
		Data []BusinessUnit `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &envelope); err != nil {
		return nil, fmt.Errorf("decode business-units: invalid response")
	}
	return envelope.Data, nil
}

// ListCloudAccounts lists accounts authorized by the backend; unitIDs is a legacy,
// unused parameter and does not restrict discovery.
func (c *Client) ListCloudAccounts(unitIDs []string) ([]CloudAccount, error) {

	resp, err := c.gen.ListCloudAccountsApiV2CloudCloudAccountsGetWithResponse(context.Background(), nil)
	if err != nil {
		return nil, fmt.Errorf("API request failed")
	}
	if resp.StatusCode() >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode())
	}

	var envelope struct {
		Data []CloudAccount `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &envelope); err != nil {
		return nil, fmt.Errorf("decode cloud-accounts: invalid response")
	}
	return envelope.Data, nil
}

// FetchAccountTree discovers accessible accounts, with business-unit labels when
// the user has a company. Company membership does not determine account access.
func (c *Client) FetchAccountTree() (AccountTree, error) {
	me, err := c.Me()
	if err != nil {
		return AccountTree{}, err
	}
	var bus []BusinessUnit
	if me.Company != nil {
		bus, err = c.ListBusinessUnits(*me.Company)
		if err != nil {
			return AccountTree{}, err
		}
	}

	accounts, err := c.ListCloudAccounts(nil)
	if err != nil {
		return AccountTree{}, err
	}

	return AccountTree{
		User:          me,
		BusinessUnits: bus,
		Accounts:      accounts,
	}, nil
}

func (c *Client) ListRecommendations(limit, offset int) (RecommendationListResult, error) {
	resp, err := c.gen.ListAwsSourcedRecommendationsApiV2CloudAwsSourcedRecommendationsGetWithResponse(
		context.Background(),
		c.withPagination(limit, offset),
	)
	if err != nil {
		return RecommendationListResult{}, fmt.Errorf("API request failed")
	}
	if resp.StatusCode() == http.StatusUnauthorized {
		return RecommendationListResult{}, fmt.Errorf("not authenticated — run: jetscale auth login")
	}
	if resp.StatusCode() >= 300 {
		return RecommendationListResult{}, fmt.Errorf("HTTP %d", resp.StatusCode())
	}

	var envelope struct {
		Data []Recommendation       `json:"data"`
		Meta RecommendationListMeta `json:"meta"`
	}
	if err := json.Unmarshal(resp.Body, &envelope); err != nil {
		return RecommendationListResult{}, fmt.Errorf("decode recommendations: invalid response")
	}
	return RecommendationListResult{Items: envelope.Data, Meta: envelope.Meta}, nil
}

func (c *Client) GetRecommendation(id string) (Recommendation, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return Recommendation{}, err
	}

	resp, err := c.gen.GetAwsSourcedRecommendationByIdApiV2CloudAwsSourcedRecommendationsAwsSourcedRecommendationIdGetWithResponse(
		context.Background(), uid,
	)
	if err != nil {
		return Recommendation{}, fmt.Errorf("API request failed")
	}
	if resp.StatusCode() == http.StatusUnauthorized {
		return Recommendation{}, fmt.Errorf("not authenticated — run: jetscale auth login")
	}
	if resp.StatusCode() == http.StatusNotFound {
		return Recommendation{}, fmt.Errorf("recommendation %q not found", id)
	}
	if resp.StatusCode() >= 300 {
		return Recommendation{}, fmt.Errorf("HTTP %d", resp.StatusCode())
	}

	var envelope struct {
		Data Recommendation `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &envelope); err != nil {
		return Recommendation{}, fmt.Errorf("decode recommendation: invalid response")
	}
	return envelope.Data, nil
}

// withPagination adds limit/offset query params to the request.
func (c *Client) withPagination(limit, offset int) generated.RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		q := req.URL.Query()
		if limit > 0 {
			q.Set("limit", fmt.Sprintf("%d", limit))
		}
		if offset > 0 {
			q.Set("offset", fmt.Sprintf("%d", offset))
		}
		req.URL.RawQuery = q.Encode()
		return nil
	}
}

func parseUUID(s string) (openapi_types.UUID, error) {
	uid, err := uuid.Parse(s)
	if err != nil {
		return openapi_types.UUID{}, fmt.Errorf("invalid ID %q: expected a UUID", s)
	}
	return uid, nil
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
