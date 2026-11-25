// Package anypoint is a client for the MuleSoft Anypoint Platform REST APIs.
//
// It handles authentication (connected apps and users, with the same flow
// selection as the official Terraform provider), token caching and refresh,
// and organization / environment resolution by name or ID. Requests to any
// endpoint go through [Client.Do] or [Client.JSON].
//
//	c := anypoint.NewClient("eu",
//		anypoint.WithClientCredentials(id, secret),
//		anypoint.WithEnv("Sandbox"))
//	envs, err := c.Envs(ctx)
package anypoint

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Client talks to the Anypoint Platform REST APIs. It is safe for concurrent use.
type Client struct {
	base      string
	http      *http.Client
	userAgent string
	debug     io.Writer
	cacheDir  string

	clientID, clientSecret string
	username, password     string
	org, env               string // as configured: name or ID

	mu          sync.Mutex
	token       string
	staticToken bool // supplied with WithToken; never refreshed
	me          map[string]any
	orgID       string
	envID       string
}

// Option configures a Client.
type Option func(*Client)

// WithClientCredentials authenticates as a connected app (OAuth2 client_credentials).
// Combined with WithUserCredentials it uses the password grant instead.
func WithClientCredentials(clientID, clientSecret string) Option {
	return func(c *Client) { c.clientID, c.clientSecret = clientID, clientSecret }
}

// WithUserCredentials authenticates as an Anypoint user.
func WithUserCredentials(username, password string) Option {
	return func(c *Client) { c.username, c.password = username, password }
}

// WithToken uses a pre-issued bearer token. It is never refreshed or cached.
func WithToken(token string) Option {
	return func(c *Client) {
		if token != "" {
			c.token, c.staticToken = token, true
		}
	}
}

// WithOrg selects the organization / business group by name or ID.
// Without it, the caller's home organization is used.
func WithOrg(nameOrID string) Option { return func(c *Client) { c.org = nameOrID } }

// WithEnv selects the environment by name (case-insensitive) or ID.
func WithEnv(nameOrID string) Option { return func(c *Client) { c.env = nameOrID } }

// WithHTTPClient replaces the default HTTP client (5 minute timeout).
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) Option { return func(c *Client) { c.userAgent = ua } }

// WithDebug logs each request line and response status to w.
func WithDebug(w io.Writer) Option { return func(c *Client) { c.debug = w } }

// WithTokenCache caches access tokens as files in dir (mode 0600), keyed by
// host and identity, so separate processes can reuse them until they expire.
func WithTokenCache(dir string) Option { return func(c *Client) { c.cacheDir = dir } }

// NewClient returns a client for the control plane host: a region shortcut
// (us, eu, gov, ca, jp), a hostname or a base URL. See [HostURL].
func NewClient(host string, opts ...Option) *Client {
	c := &Client{
		base:      HostURL(host),
		http:      &http.Client{Timeout: 5 * time.Minute},
		userAgent: "anypoint-go-client/" + Version,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// BaseURL is the control plane base URL, without a trailing slash.
func (c *Client) BaseURL() string { return c.base }

func (c *Client) send(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", c.userAgent)
	if c.debug != nil {
		fmt.Fprintf(c.debug, "> %s %s\n", req.Method, req.URL.Redacted())
	}
	resp, err := c.http.Do(req)
	if c.debug != nil && err == nil {
		fmt.Fprintf(c.debug, "< %s\n", resp.Status)
	}
	return resp, err
}

// Do performs an authenticated request and returns the response of a 2xx
// status; any other status is returned as an [*HTTPError]. path may be
// absolute ("/accounts/...") or a full URL. The bearer token is only sent to
// the configured Anypoint host, so pre-signed download links are safe to
// follow. A 401 on a cached token triggers one re-login and retry.
func (c *Client) Do(ctx context.Context, method, path string, body []byte, hdr http.Header) (*http.Response, error) {
	u := path
	if !strings.Contains(path, "://") {
		u = c.base + path
	}
	sameHost := strings.HasPrefix(u, c.base+"/") || strings.HasPrefix(u, c.ExchangeMavenURL()+"/")
	if sameHost {
		if err := c.ensureToken(ctx); err != nil {
			return nil, err
		}
	}
	for attempt := 0; ; attempt++ {
		var r io.Reader
		if body != nil {
			r = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, r)
		if err != nil {
			return nil, err
		}
		maps.Copy(req.Header, hdr)
		if body != nil && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if sameHost {
			req.Header.Set("Authorization", "Bearer "+c.currentToken())
		}
		resp, err := c.send(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized && sameHost && attempt == 0 && !c.staticToken {
			resp.Body.Close()
			if err := c.Login(ctx); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode/100 != 2 {
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, &HTTPError{Status: resp.StatusCode, Method: method, URL: u, Body: data}
		}
		return resp, nil
	}
}

// JSON sends in (nil, []byte, or any JSON-marshalable value) and decodes the
// response into out, which may be nil to discard it.
func (c *Client) JSON(ctx context.Context, method, path string, in, out any) error {
	return c.jsonH(ctx, method, path, nil, in, out)
}

// jsonH is JSON with extra request headers.
func (c *Client) jsonH(ctx context.Context, method, path string, hdr http.Header, in, out any) error {
	var body []byte
	switch v := in.(type) {
	case nil:
	case []byte:
		body = v
	default:
		var err error
		if body, err = json.Marshal(v); err != nil {
			return err
		}
	}
	resp, err := c.Do(ctx, method, path, body, hdr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	return decode(data, out)
}

// Get decodes a GET response into untyped JSON.
func (c *Client) Get(ctx context.Context, path string) (any, error) {
	var v any
	return v, c.JSON(ctx, http.MethodGet, path, nil, &v)
}
