package anypoint

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	orgID = "11111111-1111-1111-1111-111111111111"
	envID = "22222222-2222-2222-2222-222222222222"
)

// server is a minimal control plane: tokens, /me, environments.
func server(t *testing.T, tokens *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		js := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		switch r.URL.Path {
		case "/accounts/api/v2/oauth2/token", "/accounts/login":
			tokens.Add(1)
			js(map[string]any{"access_token": "tok", "expires_in": 3600})
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			js(map[string]any{"message": "unauthorized"})
			return
		}
		switch r.URL.Path {
		case "/accounts/api/me":
			js(map[string]any{"client": map[string]any{"org_id": orgID}})
		case "/accounts/api/organizations/" + orgID + "/environments":
			js(map[string]any{"data": []any{map[string]any{"id": envID, "name": "Sandbox"}}})
		default:
			w.WriteHeader(http.StatusNotFound)
			js(map[string]any{"message": "no route " + r.URL.Path})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestBearerNotSentToForeignHosts(t *testing.T) {
	var got string
	other := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
	}))
	defer other.Close()
	c := NewClient("https://anypoint.example", WithToken("secret"))
	resp, err := c.Do(context.Background(), http.MethodGet, other.URL+"/file.jar", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got != "" {
		t.Fatalf("token leaked to foreign host: %q", got)
	}
}

func TestExpandResolvesNamesAndCachesToken(t *testing.T) {
	var tokens atomic.Int32
	srv := server(t, &tokens)
	dir := t.TempDir()
	ctx := context.Background()
	c := NewClient(srv.URL, WithClientCredentials("id", "secret"), WithEnv("sandbox"), WithTokenCache(dir))
	got, err := c.Expand(ctx, "/x/{org}/y/{env}")
	if err != nil {
		t.Fatal(err)
	}
	if want := "/x/" + orgID + "/y/" + envID; got != want {
		t.Fatalf("Expand = %q, want %q", got, want)
	}
	// A second client with the same identity reuses the cached token.
	c2 := NewClient(srv.URL, WithClientCredentials("id", "secret"), WithTokenCache(dir))
	if _, err := c2.Me(ctx); err != nil {
		t.Fatal(err)
	}
	if n := tokens.Load(); n != 1 {
		t.Fatalf("want 1 token request, got %d", n)
	}
	if err := c2.Logout(); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("logout left %d cache files", len(entries))
	}
}

func TestErrors(t *testing.T) {
	var tokens atomic.Int32
	srv := server(t, &tokens)
	ctx := context.Background()

	if _, err := NewClient(srv.URL).Me(ctx); !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("want ErrNoCredentials, got %v", err)
	}
	// No environment configured: the default (no production one here, so the first).
	if id, err := NewClient(srv.URL, WithToken("tok")).EnvID(ctx); err != nil || id != envID {
		t.Fatalf("default environment: got %q, %v", id, err)
	}
	_, err := NewClient(srv.URL, WithToken("tok"), WithEnv("prod")).EnvID(ctx)
	if !IsNotFound(err) || !strings.Contains(err.Error(), "available: Sandbox") {
		t.Fatalf("want list of available environments, got %v", err)
	}
	_, err = NewClient(srv.URL, WithToken("tok")).Get(ctx, "/nope")
	if !IsStatus(err, http.StatusNotFound) || !strings.Contains(err.Error(), "no route /nope") {
		t.Fatalf("want 404 HTTPError with server message, got %v", err)
	}
	// A static token is never refreshed: a 401 surfaces without a login attempt.
	_, err = NewClient(srv.URL, WithToken("bad")).Me(ctx)
	if !IsStatus(err, http.StatusUnauthorized) || tokens.Load() != 0 {
		t.Fatalf("want 401 and no token requests, got %v (%d)", err, tokens.Load())
	}
}

func TestHostURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "https://anypoint.mulesoft.com",
		"EU":                     "https://eu1.anypoint.mulesoft.com",
		"gov":                    "https://gov.anypoint.mulesoft.com",
		"ca":                     "https://ca1.platform.mulesoft.com",
		"jp":                     "https://jp1.platform.mulesoft.com",
		"cp.example.com":         "https://cp.example.com",
		"http://localhost:8080/": "http://localhost:8080",
	} {
		if got := HostURL(in); got != want {
			t.Errorf("HostURL(%q) = %q, want %q", in, got, want)
		}
	}
}
