package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const dc = "/designcenter/api-designer/projects"

func designProjects(r *recorder) {
	r.on("GET "+dc, []any{map[string]any{"id": "p1", "name": "orders-api", "type": "raml", "organizationId": rootOrg}})
	r.on("GET "+dc+"/p1", map[string]any{"id": "p1", "name": "orders-api", "defaultBranch": "master"})
}

func TestDesignCenterProjects(t *testing.T) {
	r := newRecorder(t)
	designProjects(r)
	r.on("GET /designcenter/api/v2/organizations/"+rootOrg+"/projects", []any{
		map[string]any{"id": "p1", "name": "orders-api ", "type": "raml", "version": 3, "createdDate": "2024-05-01T10:00:00Z"},
		map[string]any{"id": "p2", "name": "billing", "type": "oas", "createdDate": "2024-05-02T10:00:00Z"}})
	out := r.mustRun("designcenter:project:list", "orders")
	if !strings.Contains(out, "2024-05-01") || strings.Contains(out, "billing") {
		t.Errorf("list:\n%s", out)
	}
	c := r.last("GET /designcenter/api/v2/organizations/" + rootOrg + "/projects")
	if c.Query != "pageIndex=0&pageSize=10" || c.Header.Get("x-owner-id") != userID || c.Header.Get("x-organization-id") != rootOrg {
		t.Errorf("list request: %s %v", c.Query, c.Header)
	}

	r.on("POST "+dc, reply{201, map[string]any{"id": "p3", "name": "new", "type": "oas"}})
	r.mustRun("designcenter:project:create", "new", "-t", "oas", "--format", "json", "-v", "3.0")
	jsonEq(t, r.last("POST "+dc).JSON(t), "", map[string]any{"name": "new", "type": "oas", "format": "json", "version": "3.0"})
	r.mustRun("designcenter:project:create", "frag", "--type", "raml-fragment", "--fragment-type", "json-schema")
	jsonEq(t, r.last("POST "+dc).JSON(t), "", map[string]any{"name": "frag", "type": "raml-fragment", "classifier": "json-schema", "format": "json"})
	r.mustRun("designcenter:project:create", "lib", "--type", "raml-fragment", "-f", "library")
	jsonEq(t, r.last("POST "+dc).JSON(t), "subType", "library")
	r.mustRun("designcenter:project:create", "plain")
	jsonEq(t, r.last("POST "+dc).JSON(t), "", map[string]any{"name": "plain", "type": "raml"})
	for _, bad := range [][]string{{"--type", "wsdl"}, {"--type", "oas", "--format", "json"}, {"--type", "oas", "--format", "xml", "--version", "3.0"},
		{"--type", "raml-fragment"}, {"--type", "raml-fragment", "--fragmentType", "nope"}} {
		if _, err := r.run(append([]string{"designcenter:project:create", "x"}, bad...)...); err == nil {
			t.Errorf("create %v must fail", bad)
		}
	}

	r.on("DELETE "+dc+"/p1", reply{204, nil})
	if out := r.mustRun("designcenter:project:delete", "orders-api", "--force"); !strings.Contains(out, "Project orders-api deleted successfully") {
		t.Errorf("delete: %s", out)
	}
	if _, err := r.run("designcenter:project:delete", "ghost"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("delete unknown: %v", err)
	}
}

func TestDesignCenterDownloadUpload(t *testing.T) {
	old := dependencyPoll
	dependencyPoll = time.Millisecond
	t.Cleanup(func() { dependencyPoll = old })

	r := newRecorder(t)
	designProjects(r)
	br := dc + "/p1/branches/master"
	r.on("PATCH "+br+"/exchange/dependencies", map[string]any{"invalidDependencies": []any{}})
	r.on("GET "+br+"/exchange/dependencies/job", map[string]any{"status": "done"})
	r.on("GET "+br+"/files", []any{
		map[string]any{"path": "api.raml", "type": "FILE"}, map[string]any{"path": "types", "type": "FOLDER"},
		map[string]any{"path": "types/order.raml", "type": "FILE"}})
	r.on("GET "+br+"/files/api.raml", "#%RAML 1.0")
	r.on("GET "+br+`/files/types\order.raml`, "#%RAML 1.0 DataType")
	dir := filepath.Join(t.TempDir(), "out")
	out := r.mustRun("designcenter:project:download", "orders-api", dir)
	if !strings.Contains(out, "downloaded successfully") {
		t.Errorf("download: %s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "types", "order.raml")); string(b) != "#%RAML 1.0 DataType" {
		t.Errorf("types/order.raml = %q", b)
	}

	// failed dependencies: the download still happens, with warnings
	r.on("PATCH "+br+"/exchange/dependencies", map[string]any{"invalidDependencies": []any{map[string]any{"assetId": "common-types"}}})
	if out := r.mustRun("designcenter:project:download", "orders-api", dir); !strings.Contains(out, "with warnings") {
		t.Errorf("download with bad dependencies: %s", out)
	}
	// a path that escapes the target directory is refused
	r.on("PATCH "+br+"/exchange/dependencies", map[string]any{"invalidDependencies": []any{}})
	r.on("GET "+br+"/files", []any{map[string]any{"path": "../../evil", "type": "FILE"}})
	r.on("GET "+br+`/files/..\..\evil`, "x")
	if _, err := r.run("designcenter:project:download", "orders-api", dir, "-b", "master"); err != nil {
		t.Errorf("a cleaned path stays inside: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "evil")); err != nil {
		t.Errorf("../../evil must land inside the target: %v", err)
	}

	src := t.TempDir()
	for name, content := range map[string]string{"api.raml": "A", "types/t.raml": "T", ".git/config": "G", ".hidden": "H"} {
		p := filepath.Join(src, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r.on("POST "+br+"/acquireLock", reply{200, nil})
	r.on("POST "+br+"/releaseLock", reply{200, nil})
	r.on("POST "+br+"/save", []any{})
	r.mustRun("designcenter:project:upload", "orders-api", src)
	jsonEq(t, r.last("POST "+br+"/save").JSON(t), "", []any{
		map[string]any{"path": "/api.raml", "content": "A"}, map[string]any{"path": "/types/t.raml", "content": "T"}})
	r.mustRun("designcenter:project:upload", "orders-api", src, "-i")
	if n := len(r.last("POST " + br + "/save").JSON(t).([]any)); n != 4 {
		t.Errorf("--include-dot-files uploads 4 files, got %d", n)
	}

	// the lock is released even when the save fails
	r.on("POST "+br+"/save", reply{500, map[string]any{"message": "boom"}})
	before := countCalls(r, "POST "+br+"/releaseLock")
	if _, err := r.run("designcenter:project:upload", "orders-api", src); err == nil {
		t.Error("upload must fail")
	}
	if countCalls(r, "POST "+br+"/releaseLock") != before+1 {
		t.Error("the lock must be released after a failed save")
	}
}

func countCalls(r *recorder, key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if c.key() == key {
			n++
		}
	}
	return n
}

func TestDesignCenterPublish(t *testing.T) {
	r := newRecorder(t)
	designProjects(r)
	br := dc + "/p1/branches/dev"
	r.on("GET "+br+"/files", []any{})
	r.on("GET "+br+"/files/v2/exchange.json", map[string]any{"groupId": "old-group", "version": "1.0.0", "main": "api.raml",
		"apiVersion": "v1", "tags": []any{"a", "a", "b"}})
	r.on("POST "+br+"/acquireLock", reply{200, nil})
	r.on("POST "+br+"/releaseLock", reply{200, nil})
	r.on("POST "+br+"/save", []any{})
	r.on("POST "+br+"/publish/exchange", map[string]any{})

	r.mustRun("designcenter:project:publish", "orders-api", "-b", "dev", "--groupId", rootOrg, "--version", "1.1.0")
	var ex map[string]any
	saved := r.last("POST " + br + "/save").JSON(t).([]any)[0].(map[string]any)
	if err := json.Unmarshal([]byte(saved["content"].(string)), &ex); err != nil || saved["path"] != "/exchange.json" || ex["groupId"] != rootOrg {
		t.Errorf("exchange.json update: %v", saved)
	}
	b := r.last("POST " + br + "/publish/exchange").JSON(t)
	jsonEq(t, b, "", map[string]any{"name": "orders-api", "version": "1.1.0", "main": "api.raml", "assetId": "orders-api",
		"groupId": rootOrg, "classifier": "raml", "tags": []any{"a", "b"}, "apiVersion": "v1", "status": "published",
		"metadata": map[string]any{"projectId": "p1", "branchId": "dev"}})

	if _, err := r.run("designcenter:project:publish", "orders-api", "--status", "draft"); err == nil {
		t.Error("an invalid status must fail")
	}
	// no exchange.json yet
	r.on("GET "+br+"/files/v2/exchange.json", reply{404, map[string]any{"message": "missing"}})
	r.mustRun("designcenter:project:publish", "orders-api", "-b", "dev", "--version", "1.0.0", "--status", "development")
	b = r.last("POST " + br + "/publish/exchange").JSON(t)
	jsonEq(t, b, "groupId", rootOrg)
	jsonEq(t, b, "status", "development")
}

func TestDataGraphLoadBalancer(t *testing.T) {
	r := newRecorder(t)
	p := "/datagraph/xapi/organizations/" + rootOrg + "/environments/" + prodEnv + "/dlbConfig"
	r.on("PUT "+p, reply{200, nil}).on("DELETE "+p, reply{200, nil})
	r.on("GET "+p, map[string]any{"domain": "dg.example.com", "dlbUrl": "https://dlb.example.com"})
	if out := r.mustRun("datagraph:load-balancer-config:add", "https://dlb.example.com"); !strings.Contains(out, "configuration set") {
		t.Errorf("add: %s", out)
	}
	jsonEq(t, r.last("PUT "+p).JSON(t), "dlbUrl", "https://dlb.example.com")
	if out := r.mustRun("datagraph:load-balancer-config:describe"); !strings.Contains(out, "dg.example.com") {
		t.Errorf("describe: %s", out)
	}
	r.mustRun("datagraph:load-balancer-config:remove")
	r.on("GET "+p, func(*http.Request) reply { return reply{500, map[string]any{"message": "down"}} })
	if _, err := r.run("datagraph:load-balancer-config:describe"); err == nil || !strings.Contains(err.Error(), "loading config") {
		t.Errorf("describe error: %v", err)
	}
}
