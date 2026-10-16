package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Jetscale-ai/cli/internal/api/generated"
	"github.com/google/uuid"
)

// Resource commands share one bounded contract: no redirects, 15-second
// requests, bounded bodies and pages, one attempt per write, and errors that
// carry the Backend's stable identity but never its free-text causes.
const (
	resourcePageSize = 100
	resourceMaxPages = 20
	resourceMaxBody  = 4 << 20
	resourceTimeout  = 15 * time.Second
)

// APIError is a definitive Backend rejection.
type APIError struct {
	Status             int    `json:"status"`
	Name               string `json:"api_error_name,omitempty"`
	RequiredPermission string `json:"required_permission,omitempty"`
}

func (e *APIError) Error() string {
	parts := []string{fmt.Sprintf("HTTP %d", e.Status)}
	if e.Name != "" {
		parts = append(parts, e.Name)
	}
	if e.RequiredPermission != "" {
		parts = append(parts, "requires "+e.RequiredPermission)
	}
	return "backend rejected the request (" + strings.Join(parts, ", ") + ")"
}

// ErrUncertain marks a write whose outcome could not be observed (transport
// failure or timeout after sending). Reconcile by reading; never resend.
var ErrUncertain = errors.New("write outcome uncertain")

// ErrTransport marks a read that failed before any Backend answer.
var ErrTransport = errors.New("request failed before a backend response")

// ErrInvalidResponse marks a response that is not the expected bounded JSON.
var ErrInvalidResponse = errors.New("invalid backend response")

// NotFoundError reports a selector that matched nothing.
type NotFoundError struct{ Resource, Selector string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s %q not found", e.Resource, e.Selector)
}

// AmbiguousError reports a selector that matched more than one resource.
type AmbiguousError struct {
	Resource, Selector string
	Count              int
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%s %q matches %d resources; select by ID", e.Resource, e.Selector, e.Count)
}

// IsNotFound reports a missing resource from either a selector or the Backend.
func IsNotFound(err error) bool {
	var nf *NotFoundError
	var apiErr *APIError
	return errors.As(err, &nf) || (errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound)
}

// Record is one Backend object. Commands read the few fields they reason about
// and print a curated projection; nothing else leaves the process.
type Record map[string]any

func (r Record) ID() string { return r.String("id") }

func (r Record) String(key string) string {
	switch v := r[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case map[string]any:
		if id, ok := v["id"].(string); ok {
			return id
		}
	}
	return ""
}

func (r Record) Bool(key string) (bool, bool) {
	v, ok := r[key].(bool)
	return v, ok
}

func (r Record) Strings(key string) []string {
	raw, _ := r[key].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// Project keeps only the named keys that are present.
func (r Record) Project(keys ...string) Record {
	out := Record{}
	for _, key := range keys {
		if v, ok := r[key]; ok {
			out[key] = v
		}
	}
	return out
}

// Resources returns a client whose transport cannot follow redirects and whose
// requests are capped, sharing the session cookies and TLS configuration.
func (c *Client) Resources() (*Client, error) {
	bounded, err := c.flagsBoundedClient()
	if err != nil {
		return nil, errors.New("authenticated transport unavailable")
	}
	return bounded, nil
}

type call func(context.Context) (*http.Response, error)

func (c *Client) read(ctx context.Context, do call) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, resourceTimeout)
	defer cancel()
	response, err := do(ctx)
	if err != nil || response == nil {
		if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: %w", ErrTransport, context.DeadlineExceeded)
		}
		return nil, ErrTransport
	}
	return decodeResponse(response)
}

// write sends one mutation. A transport failure is uncertain, not a rejection.
func (c *Client) write(ctx context.Context, do call) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, resourceTimeout)
	defer cancel()
	response, err := do(ctx)
	if err != nil || response == nil {
		return nil, ErrUncertain
	}
	data, err := decodeResponse(response)
	var apiErr *APIError
	if err != nil && !errors.As(err, &apiErr) {
		return nil, ErrUncertain
	}
	// A gateway error may follow a request the Backend already received.
	if apiErr != nil && (apiErr.Status == http.StatusBadGateway || apiErr.Status == http.StatusGatewayTimeout) {
		return nil, ErrUncertain
	}
	return data, err
}

func decodeResponse(response *http.Response) (json.RawMessage, error) {
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, resourceMaxBody+1))
	if err != nil || len(body) > resourceMaxBody {
		return nil, ErrInvalidResponse
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, apiError(response.StatusCode, body)
	}
	if !json.Valid(body) {
		return nil, ErrInvalidResponse
	}
	return body, nil
}

func apiError(status int, body []byte) *APIError {
	e := &APIError{Status: status}
	var envelope struct {
		Detail struct {
			Meta struct {
				Security struct {
					RequiredPermission string `json:"required_permission"`
				} `json:"security"`
			} `json:"meta"`
			Data struct {
				Error struct {
					Name string `json:"api_error_name"`
				} `json:"error"`
			} `json:"data"`
		} `json:"detail"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		e.Name = boundedToken(envelope.Detail.Data.Error.Name)
		if p := boundedToken(envelope.Detail.Meta.Security.RequiredPermission); p != "UNKNOWN" {
			e.RequiredPermission = p
		}
	}
	return e
}

// boundedToken keeps identifier-like Backend labels and drops anything else.
func boundedToken(s string) string {
	if len(s) == 0 || len(s) > 120 {
		return ""
	}
	for _, r := range s {
		if !isTokenRune(r) {
			return ""
		}
	}
	return s
}

func isTokenRune(r rune) bool {
	switch {
	case r == '_', r == '.', r == ':', r == '-':
		return true
	case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		return true
	}
	return false
}

// dataRecord unwraps {"data": {...}}.
func dataRecord(body json.RawMessage) (Record, error) {
	var envelope struct {
		Data Record `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Data == nil {
		return nil, ErrInvalidResponse
	}
	return envelope.Data, nil
}

// dataRecords unwraps {"data": [...]} for endpoints that return a plain list.
func dataRecords(body json.RawMessage) ([]Record, error) {
	var envelope struct {
		Data []Record `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Data == nil {
		return nil, ErrInvalidResponse
	}
	return envelope.Data, nil
}

type listCall func(context.Context, ...generated.RequestEditorFn) (*http.Response, error)

// listAll pages through a filtered Backend collection. A collection that
// changes size mid-read, repeats an ID or exceeds the page bound fails rather
// than returning a partial answer that could be mistaken for absence.
func (c *Client) listAll(ctx context.Context, fetch listCall, filters map[string]string) ([]Record, error) {
	items := []Record{}
	seen := map[string]bool{}
	total := -1
	for page := 0; page < resourceMaxPages; page++ {
		offset := page * resourcePageSize
		body, err := c.read(ctx, func(ctx context.Context) (*http.Response, error) {
			return fetch(ctx, listQuery(filters, offset))
		})
		if err != nil {
			return nil, err
		}
		var envelope struct {
			Data []Record `json:"data"`
			Meta struct {
				Total *int `json:"total_count"`
			} `json:"meta"`
		}
		if json.Unmarshal(body, &envelope) != nil || envelope.Data == nil {
			return nil, ErrInvalidResponse
		}
		if envelope.Meta.Total != nil {
			if total >= 0 && total != *envelope.Meta.Total {
				return nil, errors.New("collection changed while reading; retry the read")
			}
			total = *envelope.Meta.Total
		}
		for _, item := range envelope.Data {
			id := item.ID()
			if id == "" || seen[id] {
				return nil, ErrInvalidResponse
			}
			seen[id] = true
			items = append(items, item)
		}
		if total >= 0 && len(items) >= total {
			return items, nil
		}
		if len(envelope.Data) < resourcePageSize {
			if total >= 0 && len(items) != total {
				return nil, errors.New("incomplete collection")
			}
			return items, nil
		}
	}
	return nil, errors.New("collection exceeds the read bound; narrow the filter")
}

func listQuery(filters map[string]string, offset int) generated.RequestEditorFn {
	return func(_ context.Context, request *http.Request) error {
		q := request.URL.Query()
		keys := make([]string, 0, len(filters))
		for key := range filters {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			q.Set(key, filters[key])
		}
		q.Set("api_limit", strconv.Itoa(resourcePageSize))
		q.Set("api_offset", strconv.Itoa(offset))
		q.Set("api_sort", "id")
		request.URL.RawQuery = q.Encode()
		return nil
	}
}

func jsonBody(v any) (io.Reader, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(raw), nil
}

func resourceUUID(s string) (uuid.UUID, bool) {
	id, err := uuid.Parse(s)
	return id, err == nil && id != uuid.Nil
}

// selectOne resolves a name-or-slug selector against candidates already
// filtered by the Backend; exactly one match is required.
func selectOne(resource, selector string, matches []Record) (Record, error) {
	switch len(matches) {
	case 0:
		return nil, &NotFoundError{Resource: resource, Selector: selector}
	case 1:
		return matches[0], nil
	default:
		return nil, &AmbiguousError{Resource: resource, Selector: selector, Count: len(matches)}
	}
}

func filterRecords(records []Record, keep func(Record) bool) []Record {
	out := []Record{}
	for _, r := range records {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}

// Actor reads the signed-in user from /auth/me.
func (c *Client) Actor(ctx context.Context) (Record, error) {
	body, err := c.read(ctx, func(ctx context.Context) (*http.Response, error) {
		return c.gen.MeApiV2AuthMeGet(ctx)
	})
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data struct {
			User Record `json:"user"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Data.User == nil {
		return nil, ErrInvalidResponse
	}
	return envelope.Data.User, nil
}

// RawResponse is one undecoded Backend reply for the api command.
type RawResponse struct {
	Status int
	Body   []byte
}

// Raw sends one request to an /api/ path with the client's session, transport
// and request editors. It follows no redirects. A non-GET transport failure is
// ErrUncertain; it is never resent.
func (c *Client) Raw(ctx context.Context, method, pathAndQuery string, body []byte) (RawResponse, error) {
	base, ok := c.gen.ClientInterface.(*generated.Client)
	if !ok {
		return RawResponse{}, errors.New("authenticated transport unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, resourceTimeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(base.Server, "/")+pathAndQuery, reader)
	if err != nil {
		return RawResponse{}, err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for _, edit := range base.RequestEditors {
		if err := edit(ctx, request); err != nil {
			return RawResponse{}, err
		}
	}
	response, err := base.Client.Do(request)
	if err != nil {
		if method != http.MethodGet {
			return RawResponse{}, ErrUncertain
		}
		return RawResponse{}, ErrTransport
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, resourceMaxBody+1))
	if err != nil || len(data) > resourceMaxBody {
		if method != http.MethodGet {
			return RawResponse{}, ErrUncertain
		}
		return RawResponse{}, ErrInvalidResponse
	}
	return RawResponse{Status: response.StatusCode, Body: data}, nil
}

// ResponseError classifies a non-2xx raw reply like any other Backend rejection.
func ResponseError(status int, body []byte) *APIError { return apiError(status, body) }
