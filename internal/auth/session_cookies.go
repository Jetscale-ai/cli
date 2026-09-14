package auth

import (
	"errors"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

// StoredCookie retains attributes that cookiejar.Cookies intentionally omits.
// Max-Age is converted to an absolute expiry on receipt, never restarted on load.
// Session cookies (zero expiry) survive CLI invocations until deleted/sign-out.
// Values are opaque authentication material, not decrypted token payloads.
type StoredCookie struct {
	Name     string        `yaml:"name"`
	Value    string        `yaml:"value"`
	Domain   string        `yaml:"domain,omitempty"`
	Path     string        `yaml:"path"`
	Expires  time.Time     `yaml:"expires,omitempty"`
	Secure   bool          `yaml:"secure,omitempty"`
	HTTPOnly bool          `yaml:"http_only,omitempty"`
	HostOnly bool          `yaml:"host_only"`
	SameSite http.SameSite `yaml:"same_site,omitempty"`
	Quoted   bool          `yaml:"quoted,omitempty"`
	// Recognized only to fail closed: the standard jar cannot preserve CHIPS semantics.
	Partitioned bool `yaml:"partitioned,omitempty"`
}

// No public suffix dependency is needed: both jar access and the transport are
// confined to one exact origin, even for accepted parent-domain cookies.
type sessionJar struct {
	mu      sync.Mutex
	origin  string
	jar     *cookiejar.Jar
	cookies []StoredCookie
}

func newSessionJar(origin string) *sessionJar {
	jar, _ := cookiejar.New(nil)
	return &sessionJar{origin: origin, jar: jar}
}

func (j *sessionJar) Cookies(u *url.URL) []*http.Cookie {
	origin, err := exactOrigin(u)
	if err != nil || origin != j.origin {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.jar.Cookies(u)
}

func (j *sessionJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	origin, err := exactOrigin(u)
	if err != nil || origin != j.origin {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, c := range cookies {
		stored, ok := acceptedCookie(u, c)
		if !ok {
			continue
		}
		// Domain and host-only cookies at the same effective host share a key.
		index := -1
		for i, previous := range j.cookies {
			if previous.Name == stored.Name && previous.Path == stored.Path && previous.Domain == stored.Domain {
				index = i
				break
			}
		}
		deleted := c.MaxAge < 0 || (!stored.Expires.IsZero() && !stored.Expires.After(time.Now()))
		if deleted {
			if index >= 0 {
				j.cookies = append(j.cookies[:index], j.cookies[index+1:]...)
			}
		} else if index >= 0 {
			j.cookies[index] = stored
		} else {
			j.cookies = append(j.cookies, stored)
		}
		cookie := stored.httpCookie()
		if deleted {
			cookie.MaxAge = -1
		}
		j.jar.SetCookies(u, []*http.Cookie{cookie})
	}
}

func acceptedCookie(u *url.URL, cookie *http.Cookie) (StoredCookie, bool) {
	if cookie == nil || cookie.Partitioned || cookie.Valid() != nil {
		return StoredCookie{}, false
	}
	c := *cookie
	if strings.HasPrefix(c.Name, "__Secure-") && (!c.Secure || u.Scheme != "https") {
		return StoredCookie{}, false
	}
	if strings.HasPrefix(c.Name, "__Host-") && (!c.Secure || u.Scheme != "https" || c.Domain != "" || c.Path != "/") {
		return StoredCookie{}, false
	}
	if c.Path == "" || c.Path[0] != '/' {
		c.Path = "/"
		if i := strings.LastIndex(u.Path, "/"); i > 0 {
			c.Path = u.Path[:i]
		}
	}
	// Ask the standard jar to validate domain acceptance independently of expiry
	// and Secure filtering, so rejected cookies never reappear after persistence.
	probe, _ := cookiejar.New(nil)
	validation := c
	validation.MaxAge, validation.Expires = 0, time.Time{}
	probe.SetCookies(u, []*http.Cookie{&validation})
	probeURL := *u
	probeURL.Scheme, probeURL.Path = "https", c.Path
	if len(probe.Cookies(&probeURL)) == 0 {
		return StoredCookie{}, false
	}
	host := strings.ToLower(u.Hostname())
	hostOnly := c.Domain == "" || net.ParseIP(host) != nil
	domain := strings.TrimPrefix(strings.ToLower(c.Domain), ".")
	if hostOnly {
		domain = host
	}
	if c.MaxAge > 0 {
		// Clamp only values beyond time.Time's serializable range.
		now := time.Now()
		latest := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
		seconds := int64(c.MaxAge)
		if seconds > latest.Unix()-now.Unix() {
			c.Expires = latest
		} else {
			c.Expires = time.Unix(now.Unix()+seconds, int64(now.Nanosecond()))
		}
	}
	return StoredCookie{Name: c.Name, Value: c.Value, Domain: domain, Path: c.Path, Expires: c.Expires, Secure: c.Secure, HTTPOnly: c.HttpOnly, HostOnly: hostOnly, SameSite: c.SameSite, Quoted: c.Quoted}, true
}

func (c StoredCookie) httpCookie() *http.Cookie {
	domain := c.Domain
	if c.HostOnly {
		domain = ""
	}
	return &http.Cookie{Name: c.Name, Value: c.Value, Domain: domain, Path: c.Path, Expires: c.Expires, Secure: c.Secure, HttpOnly: c.HTTPOnly, SameSite: c.SameSite, Quoted: c.Quoted, Partitioned: c.Partitioned}
}

func (j *sessionJar) snapshot() []StoredCookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	var cookies []StoredCookie
	for _, c := range j.cookies {
		if c.Expires.IsZero() || c.Expires.After(time.Now()) {
			cookies = append(cookies, c)
		}
	}
	return cookies
}

func (j *sessionJar) restore(cookies []StoredCookie) error {
	u, _ := url.Parse(j.origin)
	for _, c := range cookies {
		if c.HostOnly && c.Domain != strings.ToLower(u.Hostname()) {
			return errors.New("invalid stored cookie host")
		}
		if c.Path == "" || !strings.HasPrefix(c.Path, "/") {
			return errors.New("invalid stored cookie path")
		}
		validated, ok := acceptedCookie(u, c.httpCookie())
		if !ok || validated.Domain != c.Domain || validated.HostOnly != c.HostOnly {
			return errors.New("invalid stored cookie metadata")
		}
		j.SetCookies(u, []*http.Cookie{c.httpCookie()})
	}
	return nil
}

func (j *sessionJar) clear() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.jar, _ = cookiejar.New(nil)
	j.cookies = nil
}
