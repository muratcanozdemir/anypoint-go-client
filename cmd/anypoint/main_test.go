package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const (
	orgID = "11111111-1111-1111-1111-111111111111"
	envID = "22222222-2222-2222-2222-222222222222"
)

// fake is a minimal Anypoint control plane.
type fake struct {
	t        *testing.T
	mu       sync.Mutex
	calls    []string
	bodies   map[string][]byte
	headers  map[string]http.Header
	tokens   int
	opaque   bool // password-grant tokens cannot read /me
	reject   string
	deployed bool
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	key := r.Method + " " + r.URL.Path
	f.calls = append(f.calls, key)
	f.bodies[key], f.headers[key] = body, r.Header.Clone()
	js := func(v any) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(v) }

	switch r.URL.Path {
	case "/accounts/api/v2/oauth2/token":
		var m map[string]string
		_ = json.Unmarshal(body, &m)
		f.tokens++
		tok := "cc-token"
		if m["grant_type"] == "password" {
			tok = "pw-token"
		}
		js(map[string]any{"access_token": tok, "expires_in": 3600})
		return
	case "/accounts/login":
		f.tokens++
		js(map[string]any{"access_token": "login-token"})
		return
	}
	auth := r.Header.Get("Authorization")
	if auth == "" || auth == "Bearer "+f.reject || (f.opaque && auth == "Bearer pw-token") {
		w.WriteHeader(401)
		js(map[string]any{"message": "unauthorized"})
		return
	}
	base := "/amc/application-manager/api/v2/organizations/" + orgID + "/environments/" + envID + "/deployments"
	switch {
	case r.URL.Path == "/accounts/api/me":
		js(map[string]any{"client": map[string]any{"org_id": orgID, "name": "ci", "client_id": "cid"}})
	case r.URL.Path == "/accounts/api/organizations/"+orgID+"/hierarchy":
		js(map[string]any{"id": orgID, "name": "Root", "subOrganizations": []any{
			map[string]any{"id": "33333333-3333-3333-3333-333333333333", "name": "Team BG", "subOrganizations": []any{}}}})
	case r.URL.Path == "/accounts/api/organizations/"+orgID+"/environments":
		js(map[string]any{"data": []any{map[string]any{"id": envID, "name": "Sandbox", "type": "sandbox"}}})
	case r.URL.Path == base && r.Method == "GET":
		var list []any
		if f.deployed {
			list = append(list, map[string]any{"id": "dep-1", "name": "hello", "status": "APPLIED"})
		}
		js(map[string]any{"items": list})
	case r.URL.Path == base+"/dep-1" && r.Method == "GET":
		js(map[string]any{"id": "dep-1", "name": "hello", "target": map[string]any{"deploymentSettings": map[string]any{"runtimeVersion": "4.8.0", "clustered": false}},
			"application": map[string]any{"configuration": map[string]any{"mule.agent.application.properties.service": map[string]any{
				"properties": map[string]any{"keep": "me"}}}}})
	case strings.HasPrefix(r.URL.Path, base):
		js(map[string]any{"id": "dep-1", "name": "hello"})
	case strings.HasPrefix(r.URL.Path, "/exchange/api/v2/organizations/"):
		js(map[string]any{"status": "completed"})
	default:
		w.WriteHeader(404)
		js(map[string]any{"message": "no route " + key})
	}
}

func setup(t *testing.T) (*fake, *httptest.Server) {
	t.Helper()
	t.Setenv("ANYPOINT_CONFIG_DIR", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, k := range []string{"ANYPOINT_CLIENT_ID", "ANYPOINT_CLIENT_SECRET", "ANYPOINT_USERNAME", "ANYPOINT_PASSWORD",
		"ANYPOINT_ORG", "ANYPOINT_ENV", "ANYPOINT_BEARER", "ANYPOINT_TOKEN", "ANYPOINT_PROFILE"} {
		t.Setenv(k, "")
	}
	f := &fake{t: t, bodies: map[string][]byte{}, headers: map[string]http.Header{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv("ANYPOINT_HOST", srv.URL)
	return f, srv
}

func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := run(args, &out)
	return out.String(), err
}

func TestClientCredentialsAndTokenCache(t *testing.T) {
	f, _ := setup(t)
	t.Setenv("ANYPOINT_CLIENT_ID", "cid")
	t.Setenv("ANYPOINT_CLIENT_SECRET", "sec")
	out, err := runCLI(t, "whoami")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "connected app ci") || !strings.Contains(out, orgID) {
		t.Fatalf("unexpected whoami output:\n%s", out)
	}
	if _, err := runCLI(t, "whoami"); err != nil {
		t.Fatal(err)
	}
	if f.tokens != 1 {
		t.Fatalf("token should be cached across runs; got %d token requests", f.tokens)
	}
}

func TestPasswordGrantFallsBackToLogin(t *testing.T) {
	f, _ := setup(t)
	f.opaque = true
	t.Setenv("ANYPOINT_CLIENT_ID", "cid")
	t.Setenv("ANYPOINT_CLIENT_SECRET", "sec")
	t.Setenv("ANYPOINT_USERNAME", "u")
	t.Setenv("ANYPOINT_PASSWORD", "p")
	if _, err := runCLI(t, "whoami"); err != nil {
		t.Fatal(err)
	}
	if got := f.headers["GET /accounts/api/me"].Get("Authorization"); got != "Bearer login-token" {
		t.Fatalf("expected fallback login token, got %q (calls %v)", got, f.calls)
	}
}

func TestExpiredTokenIsRefreshedOnce(t *testing.T) {
	f, _ := setup(t)
	t.Setenv("ANYPOINT_CLIENT_ID", "cid")
	t.Setenv("ANYPOINT_CLIENT_SECRET", "sec")
	if _, err := runCLI(t, "login"); err != nil {
		t.Fatal(err)
	}
	f.reject = "cc-token" // server now rejects the cached token…
	_, err := runCLI(t, "whoami")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("a token the server keeps rejecting must surface a 401, got %v", err)
	}
	if f.tokens != 2 {
		t.Fatalf("expected exactly one re-login, got %d token requests", f.tokens)
	}
}

func TestEnvNameResolutionAndList(t *testing.T) {
	setup(t)
	t.Setenv("ANYPOINT_BEARER", "static")
	out, err := runCLI(t, "account:environment:list")
	if err != nil || !strings.Contains(out, "Sandbox") {
		t.Fatalf("env ls: %v\n%s", err, out)
	}
	if _, err := runCLI(t, "runtime-mgr:application:list", "--env", "nope"); err == nil || !strings.Contains(err.Error(), "available: Sandbox") {
		t.Fatalf("expected helpful env error, got %v", err)
	}
	out, err = runCLI(t, "account", "business-group", "list")
	if err != nil || !strings.Contains(out, "Team BG") {
		t.Fatalf("org ls: %v\n%s", err, out)
	}
}

func TestRawAPIExpandsPlaceholders(t *testing.T) {
	f, _ := setup(t)
	t.Setenv("ANYPOINT_BEARER", "static")
	if _, err := runCLI(t, "api", "/accounts/api/organizations/{org}/environments", "--env", "Sandbox", "-H", "X-ANYPNT-ENV-ID: {env}"); err != nil {
		t.Fatal(err)
	}
	if got := f.headers["GET /accounts/api/organizations/"+orgID+"/environments"].Get("X-Anypnt-Env-Id"); got != envID {
		t.Fatalf("header not expanded: %q", got)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	setup(t)
	if _, err := runCLI(t, "config", "set", "env", "Sandbox", "--profile", "work"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, "config", "use", "work"); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, "config", "get", "env")
	if err != nil || strings.TrimSpace(out) != "Sandbox" {
		t.Fatalf("config get: %v %q", err, out)
	}
}
