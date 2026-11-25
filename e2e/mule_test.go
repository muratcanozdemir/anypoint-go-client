//go:build e2e

// Package e2e drives the anypoint CLI against a real Mule runtime.
//
// Without a MuleSoft license there is no Runtime Manager agent and no
// control plane, so the test runs a small stand-in for both: it serves the
// Runtime Manager hybrid API the CLI talks to and acts on a real Mule CE
// runtime (MULE_HOME) the way the agent would, by dropping archives into
// apps/ and removing anchor files. The status the CLI prints comes from what
// the runtime actually did: an anchor file means deployed, and the apps log
// what they run.
//
//	MULE_HOME=/path/to/mule-standalone-4.x go test -tags e2e ./e2e -v
package e2e

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	orgID = "aaaaaaaa-0000-0000-0000-000000000001"
	envID = "bbbbbbbb-0000-0000-0000-000000000001"
)

var cli string // the built CLI

func TestMain(m *testing.M) {
	if os.Getenv("MULE_HOME") == "" {
		fmt.Println("MULE_HOME is not set; skipping the Mule runtime end-to-end tests")
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "anypoint-e2e-")
	if err != nil {
		panic(err)
	}
	cli = filepath.Join(dir, "anypoint")
	if out, err := exec.CommandContext(context.Background(), "go", "build", "-o", cli, "../cmd/anypoint").CombinedOutput(); err != nil {
		panic(fmt.Sprintf("build: %v\n%s", err, out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// muleApp builds a deployable Mule 4 application whose flow logs marker
// every second. It uses only the core module, so it needs no Maven build.
func muleApp(t *testing.T, name, marker string) []byte {
	t.Helper()
	cfg := `<?xml version="1.0" encoding="UTF-8"?>
<mule xmlns="http://www.mulesoft.org/schema/mule/core" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
      xsi:schemaLocation="http://www.mulesoft.org/schema/mule/core http://www.mulesoft.org/schema/mule/core/current/mule.xsd">
  <flow name="tick">
    <scheduler><scheduling-strategy><fixed-frequency frequency="1000"/></scheduling-strategy></scheduler>
    <logger level="INFO" message="` + marker + `"/>
  </flow>
</mule>`
	artifact, _ := json.Marshal(map[string]any{"minMuleVersion": "4.6.0", "name": name, "requiredProduct": "MULE",
		"configs":                          []string{name + ".xml"},
		"classLoaderModelLoaderDescriptor": map[string]any{"id": "mule", "attributes": map[string]any{"exportedResources": []string{}, "exportedPackages": []string{}}},
		"bundleDescriptorLoader":           map[string]any{"id": "mule", "attributes": map[string]any{}}})
	files := map[string]string{
		name + ".xml": cfg,
		"META-INF/mule-artifact/mule-artifact.json": string(artifact),
		"META-INF/maven/com.example/" + name + "/pom.xml": `<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion>` +
			`<groupId>com.example</groupId><artifactId>` + name + `</artifactId><version>1.0.0</version><packaging>mule-application</packaging></project>`,
		"META-INF/maven/com.example/" + name + "/pom.properties": "groupId=com.example\nartifactId=" + name + "\nversion=1.0.0\n",
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, c := range files {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// ---------------------------------------------------------------- the Mule runtime

type runtime struct{ home string }

func (m runtime) path(p ...string) string { return filepath.Join(append([]string{m.home}, p...)...) }

func (m runtime) cmd(t *testing.T, args ...string) {
	t.Helper()
	c := exec.CommandContext(t.Context(), m.path("bin", "mule"), args...)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("mule %v: %v\n%s", args, err, out)
	}
}

func (m runtime) deployed(app string) bool {
	_, err := os.Stat(m.path("apps", app+"-anchor.txt"))
	return err == nil
}

func (m runtime) exploded(app string) bool {
	_, err := os.Stat(m.path("apps", app))
	return err == nil
}

func (m runtime) logged(app, marker string) bool {
	b, _ := os.ReadFile(m.path("logs", "mule-app-"+app+".log"))
	return bytes.Contains(b, []byte(marker))
}

// start runs the runtime for the test and stops it afterwards.
func (m runtime) start(t *testing.T) {
	t.Helper()
	for _, d := range []string{"apps", "logs"} {
		entries, _ := os.ReadDir(m.path(d))
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "e2e-") || strings.HasPrefix(e.Name(), "mule-app-e2e-") || e.Name() == "mule.log" {
				os.RemoveAll(m.path(d, e.Name()))
			}
		}
	}
	m.cmd(t, "start")
	t.Cleanup(func() {
		if out, err := exec.CommandContext(context.Background(), m.path("bin", "mule"), "stop").CombinedOutput(); err != nil {
			t.Errorf("mule stop: %v\n%s", err, out)
		}
	})
	waitFor(t, 3*time.Minute, "the runtime to start", func() bool {
		b, _ := os.ReadFile(m.path("logs", "mule.log"))
		return bytes.Contains(b, []byte("Mule is up and kicking"))
	})
}

func waitFor(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", d, what)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// ---------------------------------------------------------------- the stand-in control plane

type app struct {
	ID      int
	Name    string
	Desired string
	Jar     []byte
	File    string
}

// platform serves the Runtime Manager hybrid API for one registered server
// (the local runtime) and applies what the CLI asks to that runtime.
type platform struct {
	mu    sync.Mutex
	mule  runtime
	apps  map[int]*app
	next  int
	calls []string
}

func (p *platform) status(a *app) string {
	switch {
	case p.mule.deployed(a.Name):
		return "STARTED"
	case a.Desired == "STOPPED" && !p.mule.exploded(a.Name):
		return "STOPPED"
	}
	return "DEPLOYING"
}

func (p *platform) doc(a *app) map[string]any {
	return map[string]any{"id": a.ID, "name": a.Name, "desiredStatus": a.Desired, "lastReportedStatus": p.status(a),
		"artifact": map[string]any{"name": a.Name, "fileName": a.File},
		"target":   map[string]any{"id": 1, "name": "local-ce", "type": "SERVER", "status": "RUNNING"}}
}

// install writes the archive into apps/, which makes the runtime (re)deploy it.
func (p *platform) install(a *app) error {
	tmp := p.mule.path("apps", "."+a.Name+".jar.tmp")
	if err := os.WriteFile(tmp, a.Jar, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.mule.path("apps", a.Name+".jar"))
}

// undeploy removes the anchor file, which makes the runtime undeploy the app.
func (p *platform) undeploy(a *app) error {
	err := os.Remove(p.mule.path("apps", a.Name+"-anchor.txt"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func data(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": v})
}

func form(r *http.Request) (map[string]string, []byte, string, error) {
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return nil, nil, "", err
	}
	mr := multipart.NewReader(r.Body, params["boundary"])
	fields := map[string]string{}
	var file []byte
	var name string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return fields, file, name, nil
		}
		if err != nil {
			return nil, nil, "", err
		}
		b, _ := io.ReadAll(part)
		if part.FormName() == "file" {
			file, name = b, part.FileName()
		} else {
			fields[part.FormName()] = string(b)
		}
	}
}

func (p *platform) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, r.Method+" "+r.URL.Path)
	fail := func(code int, err error) { http.Error(w, fmt.Sprintf(`{"message":%q}`, err), code) }
	const hy = "/hybrid/api/v1"
	path := r.URL.Path
	switch {
	case path == "/accounts/api/me":
		_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"id": "u1", "username": "e2e",
			"organization": map[string]any{"id": orgID}, "memberOfOrganizations": []any{map[string]any{"id": orgID, "name": "E2E Org"}}}})
	case path == "/accounts/api/organizations/"+orgID+"/environments":
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": envID, "name": "Sandbox", "type": "sandbox"}}})
	case path == hy+"/servers":
		data(w, []any{map[string]any{"id": 1, "name": "local-ce", "serverType": "MULE", "status": "RUNNING", "muleVersion": "CE"}})
	case path == hy+"/serverGroups" || path == hy+"/clusters":
		data(w, []any{})
	case path == hy+"/targets/1/components":
		data(w, []any{
			map[string]any{"component": map[string]any{"id": 11, "name": "mule.agent.tracking.handler.analytics"}, "enabled": false},
			map[string]any{"component": map[string]any{"id": 12, "name": "mule.agent.tracking.service"}, "configuration": map[string]any{"trackedApplications": []any{}}},
			map[string]any{"component": map[string]any{"id": 13, "name": "mule.agent.logging.service"}, "configuration": map[string]any{"applicationLogLevels": []any{}}}})
	case strings.HasPrefix(path, hy+"/targets/1/components/") && r.Method == http.MethodPatch:
		data(w, map[string]any{})
	case path == hy+"/applications" && r.Method == http.MethodGet:
		list := []any{}
		for id := 1; id <= p.next; id++ {
			if a := p.apps[id]; a != nil {
				list = append(list, p.doc(a))
			}
		}
		data(w, list)
	case path == hy+"/applications" && r.Method == http.MethodPost:
		fields, jar, file, err := form(r)
		if err != nil || jar == nil || fields["targetId"] != "1" {
			fail(http.StatusBadRequest, fmt.Errorf("bad deploy request %v: %w", fields, err))
			return
		}
		p.next++
		a := &app{ID: p.next, Name: fields["artifactName"], Desired: "STARTED", Jar: jar, File: file}
		if err := p.install(a); err != nil {
			fail(http.StatusInternalServerError, err)
			return
		}
		p.apps[a.ID] = a
		w.WriteHeader(http.StatusCreated)
		data(w, p.doc(a))
	case strings.HasPrefix(path, hy+"/applications/"):
		rest := strings.TrimPrefix(path, hy+"/applications/")
		id, _ := strconv.Atoi(strings.TrimSuffix(rest, "/artifact"))
		a := p.apps[id]
		if a == nil {
			fail(http.StatusNotFound, fmt.Errorf("application %s not found", rest))
			return
		}
		switch {
		case strings.HasSuffix(rest, "/artifact"):
			_, _ = w.Write(a.Jar)
		case r.Method == http.MethodGet:
			data(w, p.doc(a))
		case r.Method == http.MethodDelete:
			if err := p.undeploy(a); err != nil {
				fail(http.StatusInternalServerError, err)
				return
			}
			delete(p.apps, id)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPatch && strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/"):
			_, jar, file, err := form(r)
			if err != nil {
				fail(http.StatusBadRequest, err)
				return
			}
			if jar != nil { // redeploy with a new archive; otherwise a restart
				a.Jar, a.File = jar, file
			}
			a.Desired = "STARTED"
			if err := p.install(a); err != nil {
				fail(http.StatusInternalServerError, err)
				return
			}
			data(w, p.doc(a))
		case r.Method == http.MethodPatch:
			var body struct {
				DesiredStatus string `json:"desiredStatus"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				fail(http.StatusBadRequest, err)
				return
			}
			a.Desired = body.DesiredStatus
			var err error
			if body.DesiredStatus == "STOPPED" {
				err = p.undeploy(a)
			} else {
				err = p.install(a)
			}
			if err != nil {
				fail(http.StatusInternalServerError, err)
				return
			}
			data(w, p.doc(a))
		default:
			fail(http.StatusMethodNotAllowed, fmt.Errorf("%s %s", r.Method, path))
		}
	default:
		fail(http.StatusNotFound, fmt.Errorf("no route %s %s", r.Method, path))
	}
}

// ---------------------------------------------------------------- the test

func TestHybridApplicationsOnMuleRuntime(t *testing.T) {
	mule := runtime{home: os.Getenv("MULE_HOME")}
	mule.start(t)
	p := &platform{mule: mule, apps: map[int]*app{}}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	home := t.TempDir()
	env := append(os.Environ(), "ANYPOINT_HOST="+srv.URL, "ANYPOINT_BEARER=e2e-token", "ANYPOINT_ENV=Sandbox",
		"ANYPOINT_CONFIG_DIR="+t.TempDir(), "XDG_CACHE_HOME="+t.TempDir(), "HOME="+home)
	run := func(args ...string) string {
		t.Helper()
		c := exec.CommandContext(t.Context(), cli, args...)
		c.Env = env
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("anypoint %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		t.Logf("anypoint %s\n%s", strings.Join(args, " "), out)
		return string(out)
	}
	jar := func(name, marker string) string {
		f := filepath.Join(t.TempDir(), name+".jar")
		if err := os.WriteFile(f, muleApp(t, name, marker), 0o600); err != nil {
			t.Fatal(err)
		}
		return f
	}
	const name = "e2e-hello"
	step := 2 * time.Minute

	if out := run("runtime-mgr:server:list"); !strings.Contains(out, "local-ce") {
		t.Fatalf("server list:\n%s", out)
	}
	run("dx:mule:runtime:path", "--set", mule.home)
	if out := run("dx:mule:runtime:path"); !strings.Contains(out, mule.home) || !strings.Contains(out, "Path exists") {
		t.Fatalf("runtime path:\n%s", out)
	}

	// deploy: the runtime deploys the archive and runs its flow
	run("runtime-mgr:standalone-application:deploy", "local-ce", name, jar(name, "E2E-V1"))
	waitFor(t, step, "the first deployment", func() bool { return mule.deployed(name) && mule.logged(name, "E2E-V1") })
	if out := run("runtime-mgr:standalone-application:describe", name); !strings.Contains(out, "STARTED") {
		t.Fatalf("describe after deploy:\n%s", out)
	}
	if out := run("runtime-mgr:standalone-application:list"); !strings.Contains(out, name) || !strings.Contains(out, "STARTED") {
		t.Fatalf("list:\n%s", out)
	}

	// artifact: the archive comes back as deployed
	dir := t.TempDir()
	run("runtime-mgr:standalone-application:artifact", name, dir)
	got, _ := os.ReadFile(filepath.Join(dir, "app_1_artifact.zip"))
	if want, _ := os.ReadFile(jar(name, "E2E-V1")); len(got) == 0 || !bytes.Equal(zipEntry(t, got, name+".xml"), zipEntry(t, want, name+".xml")) {
		t.Fatal("the downloaded artifact differs from the deployed one")
	}

	// deploy again under the same name: a redeploy that runs the new flow
	run("runtime-mgr:standalone-application:deploy", "local-ce", name, jar(name, "E2E-V2"))
	waitFor(t, step, "the redeployment", func() bool { return mule.deployed(name) && mule.logged(name, "E2E-V2") })

	// stop and start
	run("runtime-mgr:standalone-application:stop", name)
	waitFor(t, step, "the undeployment", func() bool { return !mule.deployed(name) && !mule.exploded(name) })
	if out := run("runtime-mgr:standalone-application:describe", name); !strings.Contains(out, "STOPPED") {
		t.Fatalf("describe after stop:\n%s", out)
	}
	run("runtime-mgr:standalone-application:start", name)
	waitFor(t, step, "the start", func() bool { return mule.deployed(name) })

	// restart: the runtime redeploys the app
	before := redeployments(mule, name)
	run("runtime-mgr:standalone-application:restart", name)
	waitFor(t, step, "the restart", func() bool { return redeployments(mule, name) > before && mule.deployed(name) })

	// modify --insight goes to the agent components (the stand-in accepts them)
	run("runtime-mgr:standalone-application:modify", name, "--insight", "--log", "DEBUG:org.mule")

	// copy: the archive is downloaded and deployed again under a new name,
	// and the copy only completes once the runtime reports it started
	const copyName = "e2e-hello-copy"
	run("runtime-mgr:standalone-application:copy", name, copyName, "local-ce")
	if !mule.deployed(copyName) {
		t.Fatal("copy returned before the copy was deployed")
	}
	waitFor(t, step, "the copy to run", func() bool { return mule.logged(copyName, "E2E-V2") })

	// delete both: the runtime undeploys them
	run("runtime-mgr:standalone-application:delete", name)
	run("runtime-mgr:standalone-application:delete", copyName)
	waitFor(t, step, "the deletions", func() bool {
		return !mule.deployed(name) && !mule.exploded(name) && !mule.deployed(copyName) && !mule.exploded(copyName)
	})
	if out := run("runtime-mgr:standalone-application:list", "-o", "json"); strings.TrimSpace(out) != "[]" {
		t.Fatalf("list after delete:\n%s", out)
	}
}

// redeployments counts how often the runtime reported deploying an app.
func redeployments(m runtime, app string) int {
	b, _ := os.ReadFile(m.path("logs", "mule.log"))
	return bytes.Count(b, []byte("Started app '"+app+"'"))
}

func zipEntry(t *testing.T, archive []byte, name string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			return b
		}
	}
	return nil
}
