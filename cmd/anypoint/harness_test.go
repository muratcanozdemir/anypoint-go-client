package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	stdmaps "maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// IDs used by the recorder's default routes.
const (
	rootOrg = "aaaaaaaa-0000-0000-0000-000000000001"
	subOrg  = "aaaaaaaa-0000-0000-0000-000000000002"
	sbxEnv  = "bbbbbbbb-0000-0000-0000-000000000001"
	prodEnv = "bbbbbbbb-0000-0000-0000-000000000002"
	userID  = "cccccccc-0000-0000-0000-000000000001"
)

// call is one request the CLI sent.
type call struct {
	Method, Path, Query string
	Header              http.Header
	Body                []byte
}

func (c call) key() string { return c.Method + " " + c.Path }

// JSON decodes the request body.
func (c call) JSON(t *testing.T) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(c.Body, &v); err != nil {
		t.Fatalf("%s: body is not JSON: %v\n%s", c.key(), err, c.Body)
	}
	return v
}

// recorder is a fake control plane that records every request. Routes map
// "METHOD /path" to a response: a value encoded as JSON (status 200), a
// reply, or a func(*http.Request) reply.
type recorder struct {
	t      *testing.T
	mu     sync.Mutex
	calls  []call
	routes map[string]any
	srv    *httptest.Server
}

type reply struct {
	Status int
	Body   any // JSON-encoded unless []byte or string
}

// hreply is a reply with response headers.
type hreply struct {
	reply
	Header http.Header
}

func newRecorder(t *testing.T) *recorder {
	t.Helper()
	t.Setenv("ANYPOINT_CONFIG_DIR", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("ANYPOINT_TOOLING_DIR", t.TempDir())
	for _, k := range []string{"ANYPOINT_CLIENT_ID", "ANYPOINT_CLIENT_SECRET", "ANYPOINT_USERNAME", "ANYPOINT_PASSWORD",
		"ANYPOINT_ORG", "ANYPOINT_ENV", "ANYPOINT_TOKEN", "ANYPOINT_PROFILE"} {
		t.Setenv(k, "")
	}
	t.Setenv("ANYPOINT_BEARER", "test-token")
	r := &recorder{t: t, routes: map[string]any{
		"GET /accounts/api/me": map[string]any{"user": map[string]any{
			"id": userID, "username": "jdoe", "organization": map[string]any{"id": rootOrg},
			"memberOfOrganizations": []any{
				map[string]any{"id": rootOrg, "name": "Root Org", "isRoot": true},
				map[string]any{"id": subOrg, "name": "Team BG", "isRoot": false, "parentId": rootOrg},
			},
		}},
		"GET /accounts/api/organizations/" + rootOrg + "/environments": map[string]any{"data": []any{
			map[string]any{"id": sbxEnv, "name": "Sandbox", "type": "sandbox", "isProduction": false, "organizationId": rootOrg},
			map[string]any{"id": prodEnv, "name": "Production", "type": "production", "isProduction": true, "organizationId": rootOrg},
		}},
	}}
	r.srv = httptest.NewServer(r)
	t.Cleanup(r.srv.Close)
	t.Setenv("ANYPOINT_HOST", r.srv.URL)
	return r
}

func (r *recorder) on(key string, resp any) *recorder {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes[key] = resp
	return r
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	c := call{Method: req.Method, Path: req.URL.Path, Query: req.URL.RawQuery, Header: req.Header.Clone(), Body: body}
	r.mu.Lock()
	r.calls = append(r.calls, c)
	resp, ok := r.routes[c.key()]
	r.mu.Unlock()
	if !ok {
		resp = reply{Status: http.StatusNotFound, Body: map[string]any{"message": "no route " + c.key()}}
	}
	if f, ok := resp.(func(*http.Request) reply); ok {
		req.Body = io.NopCloser(bytes.NewReader(body))
		resp = f(req)
	}
	var hdr http.Header
	if h, ok := resp.(hreply); ok {
		resp, hdr = h.reply, h.Header
	}
	rep, ok := resp.(reply)
	if !ok {
		rep = reply{Status: http.StatusOK, Body: resp}
	}
	stdmaps.Copy(w.Header(), hdr)
	switch b := rep.Body.(type) {
	case nil:
		w.WriteHeader(rep.Status)
	case []byte:
		w.WriteHeader(rep.Status)
		_, _ = w.Write(b)
	case string:
		w.WriteHeader(rep.Status)
		_, _ = io.WriteString(w, b)
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rep.Status)
		_ = json.NewEncoder(w).Encode(b)
	}
}

// run runs the CLI and returns stdout.
func (r *recorder) run(args ...string) (string, error) {
	r.t.Helper()
	var out, errOut bytes.Buffer
	err := runContext(context.Background(), args, &out, &errOut)
	return out.String(), err
}

// mustRun runs the CLI and fails the test on error.
func (r *recorder) mustRun(args ...string) string {
	r.t.Helper()
	out, err := r.run(args...)
	if err != nil {
		r.t.Fatalf("anypoint %s: %v", strings.Join(args, " "), err)
	}
	return out
}

// last returns the last call with the given "METHOD /path", failing if none.
func (r *recorder) last(key string) call {
	r.t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range slices.Backward(r.calls) {
		if c.key() == key {
			return c
		}
	}
	var seen []string
	for _, c := range r.calls {
		seen = append(seen, c.key())
	}
	r.t.Fatalf("no request %s; requests:\n  %s", key, strings.Join(seen, "\n  "))
	return call{}
}

// sent reports whether a request with the given "METHOD /path" was made.
func (r *recorder) sent(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if c.key() == key {
			return true
		}
	}
	return false
}

// jsonEq fails unless the JSON at path in v (dig syntax) equals want.
func jsonEq(t *testing.T, v any, path string, want any) {
	t.Helper()
	got := dig(v, path)
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	if !bytes.Equal(gb, wb) {
		t.Errorf("%s = %s, want %s", path, gb, wb)
	}
}
