package api

import (
	"net/http"
	"time"

	"github.com/Jetscale-ai/cli/internal/api/generated"
)

// Use an isolated HTTP client so redirects cannot move a flag write to another
// destination or turn a POST into an apparent successful GET. Retain session
// cookies and transport, but never change the caller's shared client.
func (c *Client) flagsBoundedClient() (*Client, error) {
	if c == nil || c.gen == nil {
		return nil, flagsFailure("transport_unproved")
	}
	base, ok := c.gen.ClientInterface.(*generated.Client)
	if !ok {
		return nil, flagsFailure("transport_unproved")
	}
	httpClient, ok := base.Client.(*http.Client)
	if !ok || httpClient == nil {
		return nil, flagsFailure("transport_unproved")
	}
	bounded := *httpClient
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if bounded.Timeout == 0 || bounded.Timeout > 15*time.Second {
		bounded.Timeout = 15 * time.Second
	}
	gen := *base
	gen.Client = &bounded
	return &Client{gen: &generated.ClientWithResponses{ClientInterface: &gen}, baseURL: c.baseURL}, nil
}
