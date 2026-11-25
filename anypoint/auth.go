package anypoint

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/muratcanozdemir/anypoint-go-client/internal/atomicfile"
)

// ErrNoCredentials is returned when a request needs a token and the client
// has neither a token nor credentials to obtain one.
var ErrNoCredentials = errors.New("no credentials")

type cachedToken struct {
	Token   string    `json:"token"`
	Expires time.Time `json:"expires"`
}

func (c *Client) cachePath() string {
	if c.cacheDir == "" {
		return ""
	}
	h := sha256.Sum256([]byte(c.base + "\x00" + c.clientID + "\x00" + c.username))
	return filepath.Join(c.cacheDir, "token-"+hex.EncodeToString(h[:8])+".json")
}

func (c *Client) currentToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

// Token returns the access token the client sends, logging in (or reading
// the token cache) first when it has none. Use it to hand the session to
// other Anypoint tools.
func (c *Client) Token(ctx context.Context) (string, error) {
	if err := c.ensureToken(ctx); err != nil {
		return "", err
	}
	return c.currentToken(), nil
}

func (c *Client) ensureToken(ctx context.Context) error {
	if c.currentToken() != "" {
		return nil
	}
	if path := c.cachePath(); path != "" {
		if b, err := os.ReadFile(path); err == nil {
			var t cachedToken
			if json.Unmarshal(b, &t) == nil && t.Token != "" && time.Now().Before(t.Expires) {
				c.mu.Lock()
				c.token = t.Token
				c.mu.Unlock()
				return nil
			}
		}
	}
	return c.Login(ctx)
}

// Login obtains a fresh token, and caches it when a token cache is configured.
// It does nothing for a client built WithToken.
//
// Flow selection mirrors the official Terraform provider:
//   - client ID + secret                  → OAuth2 client_credentials
//   - client ID + secret + username/pass  → OAuth2 password grant, falling back to
//     /accounts/login when the issued token cannot read /accounts/api/me
//   - username + password only            → /accounts/login
func (c *Client) Login(ctx context.Context) error {
	if c.staticToken {
		return nil
	}
	var tok string
	var ttl time.Duration
	var err error
	userLogin := map[string]string{"username": c.username, "password": c.password}
	switch {
	case c.clientID != "" && c.clientSecret != "":
		form := map[string]string{"client_id": c.clientID, "client_secret": c.clientSecret, "grant_type": "client_credentials"}
		if c.username != "" {
			form["grant_type"], form["username"], form["password"] = "password", c.username, c.password
		}
		tok, ttl, err = c.tokenRequest(ctx, "/accounts/api/v2/oauth2/token", form)
		// Some control planes issue an opaque password-grant token that cannot read
		// /accounts/api/me; /accounts/login always yields a user-context token.
		if err == nil && c.username != "" && !c.canReadMe(ctx, tok) {
			tok, ttl, err = c.tokenRequest(ctx, "/accounts/login", userLogin)
		}
	case c.username != "" && c.password != "":
		tok, ttl, err = c.tokenRequest(ctx, "/accounts/login", userLogin)
	default:
		return ErrNoCredentials
	}
	if err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}
	c.mu.Lock()
	c.token, c.me = tok, nil
	c.mu.Unlock()
	if path := c.cachePath(); path != "" {
		b, _ := json.Marshal(cachedToken{Token: tok, Expires: time.Now().Add(ttl - time.Minute)})
		if os.MkdirAll(filepath.Dir(path), 0o700) == nil {
			_ = atomicfile.Write(path, b, 0o600)
		}
	}
	return nil
}

// Logout forgets the access token, including its cached copy.
func (c *Client) Logout() error {
	c.mu.Lock()
	if !c.staticToken {
		c.token = ""
	}
	c.me = nil
	c.mu.Unlock()
	path := c.cachePath()
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (c *Client) canReadMe(ctx context.Context, tok string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/accounts/api/me", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.send(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (c *Client) tokenRequest(ctx context.Context, path string, body map[string]string) (string, time.Duration, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(b))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.send(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", 0, &HTTPError{Status: resp.StatusCode, Method: http.MethodPost, URL: c.base + path, Body: data}
	}
	var r struct {
		AccessToken string  `json:"access_token"`
		ExpiresIn   float64 `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &r); err != nil || r.AccessToken == "" {
		return "", 0, fmt.Errorf("no access_token in response from %s", path)
	}
	ttl := time.Duration(r.ExpiresIn) * time.Second
	if ttl <= 2*time.Minute {
		ttl = 30 * time.Minute
	}
	return r.AccessToken, ttl, nil
}
