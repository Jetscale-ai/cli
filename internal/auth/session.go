package auth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Jetscale-ai/cli/internal/api/generated"
	"github.com/Jetscale-ai/cli/internal/buildinfo"
)

// Session owns one origin-bound authentication mode. Use HTTPClient for generated
// API calls, then Save after the command (including failures) to retain rotations
// and deletions. Do not replace its Transport, Jar, or redirect policy.
// Session methods return sanitized errors. HTTPClient itself retains net/http's
// URL-bearing errors; its callers must sanitize them before display or logging.
type Session struct {
	HTTPClient  *http.Client
	gen         *generated.Client
	origin      string
	channel     string
	jar         *sessionJar
	entry       TokenEntry
	environment bool
}

// LoginSession signs in using cookies, then verifies the session with auth/me.
// The response's encrypted cookie values are opaque; bearer tokens in a login
// response are intentionally ignored. Nothing is persisted until Save is called.
func LoginSession(baseURL, login, password, caFile string) (*Session, error) {
	s, err := newSession(baseURL, caFile, "")
	if err != nil {
		return nil, err
	}
	resp, err := s.gen.SignInApiV2AuthSignInPost(context.Background(), generated.SignInApiV2AuthSignInPostJSONRequestBody{Login: login, Password: password})
	if err != nil {
		return nil, errors.New("sign-in outcome uncertain (network, TLS, or redirect policy): a server session may exist; cleanup unverified; no automatic retry")
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("sign-in failed (HTTP %d)", resp.StatusCode)
	}
	meRequest, _ := generated.NewMeApiV2AuthMeGetRequest(s.origin + "/")
	hasVerificationCookie := len(s.jar.Cookies(meRequest.URL)) != 0
	_, verifyErr := s.Whoami()
	if verifyErr == nil && (!hasVerificationCookie || len(s.jar.Cookies(meRequest.URL)) == 0) {
		verifyErr = errors.New("sign-in did not establish a cookie session")
	}
	if verifyErr != nil {
		if cleanupErr := s.SignOut(); cleanupErr != nil {
			return nil, fmt.Errorf("session verification failed: %w; cleanup sign-out failed: %v", verifyErr, cleanupErr)
		}
		return nil, fmt.Errorf("session verification failed: %w; cleanup sign-out completed", verifyErr)
	}
	return s, nil
}

// OpenSession loads by exact origin and channel without making network calls.
// instanceName is only a migration fallback for matching bound legacy entries.
// An explicitly set
// channel TOKEN environment variable wins, including invalid/empty values (which
// fail without fallback). Stored bearer credentials must already be origin- and
// channel-bound; unbound legacy entries require a fresh login. Expired bearer
// credentials are rejected rather than silently switching authentication modes.
func OpenSession(instanceName, baseURL, caFile string) (*Session, error) {
	if token, ok := os.LookupEnv(buildinfo.EnvVar("TOKEN")); ok {
		if !validBearer(token) {
			return nil, errors.New("invalid explicit TOKEN environment value")
		}
		s, err := newSession(baseURL, caFile, token)
		if err != nil {
			return nil, err
		}
		s.environment = true
		return s, nil
	}
	s, err := newSession(baseURL, caFile, "")
	if err != nil {
		return nil, err
	}
	store, err := LoadTokens()
	if err != nil {
		return nil, fmt.Errorf("load session store: %w", err)
	}
	entry, ok := store.Sessions[s.storeKey()]
	if !ok {
		// Only an explicitly bound alias entry can be migrated. A present
		// canonical entry (even empty/expired) must never fall back to old state.
		entry, ok = store.Instances[instanceName]
	}
	if !ok {
		return nil, errors.New("not authenticated — run auth login")
	}
	if entry.Origin != s.origin || entry.Channel != s.channel {
		return nil, errors.New("stored credentials are not bound to this API origin and channel — run auth login")
	}
	if len(entry.Cookies) != 0 && (entry.AccessToken != "" || entry.RefreshToken != "") {
		return nil, errors.New("stored session mixes cookie and bearer authentication — run auth login")
	}
	if entry.AccessToken != "" {
		if !validBearer(entry.AccessToken) || (!entry.ExpiresAt.IsZero() && entry.Expired()) {
			return nil, errors.New("stored bearer token invalid or expired — run auth login")
		}
		s, err = newSession(baseURL, caFile, entry.AccessToken)
		if err != nil {
			return nil, err
		}
		s.entry = entry
		return s, nil
	}
	if err := s.jar.restore(entry.Cookies); err != nil {
		return nil, err
	}
	if len(s.jar.snapshot()) == 0 {
		return nil, errors.New("cookie session expired or empty — run auth login")
	}
	return s, nil
}

func (s *Session) Whoami() (WhoamiResult, error) {
	resp, err := s.gen.MeApiV2AuthMeGet(context.Background())
	if err != nil {
		return WhoamiResult{}, errors.New("whoami request failed (network, TLS, or redirect policy)")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return WhoamiResult{}, fmt.Errorf("whoami failed (HTTP %d)", resp.StatusCode)
	}
	const maxBody = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		return WhoamiResult{}, errors.New("read whoami response failed")
	}
	var envelope struct {
		Data struct {
			User *struct {
				Email    string `json:"email"`
				Username string `json:"username"`
			} `json:"user"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Data.User == nil || (envelope.Data.User.Email == "" && envelope.Data.User.Username == "") {
		return WhoamiResult{}, errors.New("invalid whoami response")
	}
	return WhoamiResult{Email: envelope.Data.User.Email, Username: envelope.Data.User.Username}, nil
}

// SignOut requires a 2xx response to confirm server-side invalidation, then
// clears local credentials; call Save to persist that clearing. A 401 does not
// prove invalidation. Failed sign-out retains credentials unless the server
// explicitly deleted cookies; Save must still persist those deletions.
func (s *Session) SignOut() error {
	resp, err := s.gen.SignOutApiV2AuthSignOutPost(context.Background())
	if err != nil {
		return errors.New("sign-out cleanup unverified (network, TLS, or redirect policy): server session may still be active")
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("sign-out cleanup unverified (HTTP %d): server session may still be active", resp.StatusCode)
	}
	if s.jar != nil {
		s.jar.clear()
	}
	s.entry = TokenEntry{}
	if transport, ok := s.HTTPClient.Transport.(*originTransport); ok {
		transport.token = ""
	}
	return nil
}

// Save persists current cookie metadata, including Set-Cookie rotations and
// deletions from ordinary API calls. Environment bearer credentials are never
// persisted, and their Save is a no-op. Storage is keyed by origin and channel,
// not the alias; instanceName is retained for API compatibility. Call only after
// requests have completed.
func (s *Session) Save(_ string) error {
	if s.environment {
		return nil
	}
	entry := s.entry
	entry.Origin, entry.Channel = s.origin, s.channel
	if s.jar != nil {
		entry = TokenEntry{Origin: s.origin, Channel: s.channel, Cookies: s.jar.snapshot()}
	}
	if err := updateTokens(func(store *TokenStore) {
		store.Sessions[s.storeKey()] = entry
		// Remove all aliases for this binding under the same lock. Keep the
		// canonical empty entry after sign-out to prevent stale legacy fallback.
		for alias, legacy := range store.Instances {
			if legacy.Origin == s.origin && legacy.Channel == s.channel {
				delete(store.Instances, alias)
			}
		}
	}); err != nil {
		return fmt.Errorf("save session store: %w", err)
	}
	return nil
}

func (s *Session) storeKey() string { return s.channel + "|" + s.origin }

func newSession(baseURL, caFile, token string) (*Session, error) {
	if buildinfo.Validate() != nil {
		return nil, errors.New("invalid compiled channel")
	}
	u, err := url.Parse(baseURL)
	if err != nil || u == nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("API URL must be an HTTP(S) origin without credentials, query, fragment, or path")
	}
	origin, err := exactOrigin(u)
	if err != nil {
		return nil, err
	}
	if err := requireSecureOrigin(u); err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, errors.New("read supplied CA file failed")
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, errors.New("load system CA certificates failed")
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("supplied CA file contains no valid certificates")
		}
		tlsConfig.RootCAs = roots
	}
	// Construct a private transport rather than inheriting a caller-modified
	// DefaultTransport (which might disable TLS verification).
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	dial := dialer.DialContext
	if u.Scheme == "http" && strings.EqualFold(u.Hostname(), "localhost") {
		// Never trust DNS/hosts-file resolution to keep plaintext localhost
		// credentials on loopback. IPv6 callers can explicitly use [::1].
		dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, errors.New("invalid loopback address")
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
		}
	}
	base := &http.Transport{
		// Deliberately no Proxy: credentials never use ambient proxy settings.
		DialContext: dial, TLSClientConfig: tlsConfig,
		TLSHandshakeTimeout: 10 * time.Second, IdleConnTimeout: 90 * time.Second,
		ForceAttemptHTTP2: true,
	}
	s := &Session{origin: origin, channel: buildinfo.Channel, entry: TokenEntry{AccessToken: token}}
	t := &originTransport{origin: origin, base: base, token: token}
	s.HTTPClient = &http.Client{Transport: t, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirects prohibited") }}
	if token == "" {
		s.jar = newSessionJar(origin)
		t.jar = s.jar
		s.HTTPClient.Jar = s.jar
	}
	s.gen, err = generated.NewClient(origin, generated.WithHTTPClient(s.HTTPClient))
	if err != nil {
		return nil, errors.New("initialize session client failed")
	}
	return s, nil
}

func exactOrigin(u *url.URL) (string, error) {
	invalid := errors.New("invalid API origin")
	if u == nil || u.User != nil || u.Opaque != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", invalid
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || strings.ContainsAny(host, "%\\ \t\r\n") || strings.HasSuffix(u.Host, ":") {
		return "", invalid
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", invalid
		}
		port = strconv.Itoa(n)
	}
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + host, nil
}

func requireSecureOrigin(u *url.URL) error {
	if u.Scheme != "http" {
		return nil
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return errors.New("credentials require HTTPS except for loopback HTTP")
}

func validBearer(token string) bool {
	if token == "" {
		return false
	}
	for _, c := range token {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~+/=", c) {
			continue
		}
		return false
	}
	return true
}

type originTransport struct {
	origin string
	base   *http.Transport
	token  string
	jar    *sessionJar
}

func (t *originTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	origin, err := exactOrigin(req.URL)
	if err != nil || origin != t.origin {
		return nil, errors.New("request blocked: different API origin")
	}
	if err := requireSecureOrigin(req.URL); err != nil {
		return nil, err
	}
	if req.Host != "" && req.Host != req.URL.Host {
		return nil, errors.New("request blocked: overridden Host")
	}
	r := req.Clone(req.Context())
	for name := range r.Header {
		if strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "Cookie") {
			delete(r.Header, name)
		}
	}
	if t.token != "" {
		r.Header.Set("Authorization", "Bearer "+t.token)
	} else if t.jar != nil {
		for _, cookie := range t.jar.Cookies(req.URL) {
			r.AddCookie(cookie)
		}
	}
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, errors.New("request failed (network or TLS verification)")
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		// The client won't process cookies when RoundTrip returns an error.
		// Preserve invalidations/rotation without following even same-origin redirects.
		if t.jar != nil {
			t.jar.SetCookies(req.URL, resp.Cookies())
		}
		_ = resp.Body.Close()
		return nil, errors.New("redirects prohibited")
	}
	return resp, nil
}

func (t *originTransport) CloseIdleConnections() { t.base.CloseIdleConnections() }
