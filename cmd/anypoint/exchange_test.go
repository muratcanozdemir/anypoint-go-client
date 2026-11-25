package main

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	v1Asset  = "/exchange/api/v1/assets/" + rootOrg + "/my-api/1.0.0"
	v2Asset  = "/exchange/api/v2/assets/" + rootOrg + "/my-api/1.0.0"
	orgAsset = "/exchange/api/v2/organizations/" + rootOrg + "/assets/" + rootOrg + "/my-api/1.0.0"
)

// multipartFields decodes a multipart request into field name -> value (file
// parts: their content).
func multipartFields(t *testing.T, c call) map[string]string {
	t.Helper()
	_, params, err := mime.ParseMediaType(c.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("%s: content type %q: %v", c.key(), c.Header.Get("Content-Type"), err)
	}
	r := multipart.NewReader(strings.NewReader(string(c.Body)), params["boundary"])
	out := map[string]string{}
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p)
		out[p.FormName()] = string(b)
	}
}

func TestExchangeList(t *testing.T) {
	r := newRecorder(t)
	r.on("POST /graph/api/v2/graphql", map[string]any{"data": map[string]any{"assets": []any{
		map[string]any{"groupId": rootOrg, "assetId": "my-api", "version": "1.0.0", "name": "My API", "type": "rest-api", "updatedDate": "2020-01-01T00:00:00Z"},
	}}})
	out := r.mustRun("exchange:asset:list", "orders", "--limit", "5", "--offset", "10", "--organizationId", rootOrg)
	if !strings.Contains(out, "My API") || !strings.Contains(out, "years ago") {
		t.Errorf("list:\n%s", out)
	}
	body := r.last("POST /graph/api/v2/graphql").JSON(t)
	jsonEq(t, body, "variables.searchText", "orders")
	jsonEq(t, body, "variables.limit", 5)
	jsonEq(t, body, "variables.offset", 10)
	jsonEq(t, body, "variables.organizationIds", []string{rootOrg})
	jsonEq(t, body, "variables.accessToken", "test-token")

	r.on("POST /graph/api/v2/graphql", map[string]any{"errors": []any{map[string]any{"message": "bad query"}}})
	if _, err := r.run("exchange:asset:list"); err == nil || !strings.Contains(err.Error(), "bad query") {
		t.Errorf("graphql errors must surface: %v", err)
	}
}

func TestExchangeDescribeAndStatus(t *testing.T) {
	r := newRecorder(t)
	r.on("GET "+v1Asset, map[string]any{"groupId": rootOrg, "assetId": "my-api", "version": "1.0.0", "name": "My API", "type": "rest-api",
		"status": "published", "productAPIVersion": "v1", "productAPIVersions": []any{
			map[string]any{"productAPIVersion": "v1", "versions": []any{map[string]any{"version": "1.0.0"}, map[string]any{"version": "1.0.1"}}},
			map[string]any{"productAPIVersion": "v2", "versions": []any{map[string]any{"version": "2.0.0"}}},
		}})
	out := r.mustRun("exchange", "asset", "describe", "my-api/1.0.0")
	for _, want := range []string{"My API", "published", "1.0.0, 1.0.1", "v1, v2"} {
		if !strings.Contains(out, want) {
			t.Errorf("describe missing %q:\n%s", want, out)
		}
	}

	r.on("PUT "+v2Asset+"/status", reply{204, nil})
	if out := r.mustRun("exchange:asset:deprecate", rootOrg+"/my-api/1.0.0"); !strings.Contains(out, "Asset my-api Deprecated") {
		t.Errorf("deprecate: %s", out)
	}
	jsonEq(t, r.last("PUT "+v2Asset+"/status").JSON(t), "status", "deprecated")
	r.mustRun("exchange:asset:undeprecate", "my-api/1.0.0")
	jsonEq(t, r.last("PUT "+v2Asset+"/status").JSON(t), "status", "published")
	r.mustRun("exchange:asset:updateStatus", "my-api/1.0.0", "--status", "development")
	jsonEq(t, r.last("PUT "+v2Asset+"/status").JSON(t), "status", "development")
	if _, err := r.run("exchange:asset:updateStatus", "my-api/1.0.0"); err == nil {
		t.Error("--status is required")
	}

	del := "/exchange/api/v1/organizations/" + rootOrg + "/assets/" + rootOrg + "/my-api/1.0.0"
	r.on("DELETE "+del, reply{204, nil})
	r.mustRun("exchange:asset:delete", "my-api/1.0.0", "-f")
	if got := r.last("DELETE " + del).Header.Get("X-Delete-Type"); got != "hard-delete" {
		t.Errorf("X-Delete-Type = %q", got)
	}

	if _, err := r.run("exchange:asset:describe", "just-one-part"); err == nil {
		t.Error("an identifier without a version must be refused")
	}
}

func TestExchangeModify(t *testing.T) {
	r := newRecorder(t)
	meta := "/exchange/api/v1/organizations/" + rootOrg + "/assets/" + rootOrg + "/my-api/1.0.0/metadata"
	r.on("PATCH "+meta, reply{204, nil}).on("PUT "+v1Asset+"/tags", reply{204, nil})
	r.mustRun("exchange:asset:modify", "my-api/1.0.0", "-n", "Renamed")
	jsonEq(t, r.last("PATCH "+meta).JSON(t), "name", "Renamed")
	r.mustRun("exchange:asset:modify", "my-api/1.0.0", "--tags", "a,b")
	tags := r.last("PUT " + v1Asset + "/tags").JSON(t)
	jsonEq(t, tags, "0", map[string]any{"key": "a", "value": "a", "mutable": true})
	jsonEq(t, tags, "1.value", "b")
}

func TestExchangeUpload(t *testing.T) {
	r := newRecorder(t)
	dir := t.TempDir()
	spec := filepath.Join(dir, "api.zip")
	pom := filepath.Join(dir, "pom.xml")
	if err := os.WriteFile(spec, []byte("ZIP"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pom, []byte("<project/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := json.Marshal(map[string]string{"raml.zip": spec, "pom.xml": pom}) // escapes Windows backslashes
	if err != nil {
		t.Fatal(err)
	}
	r.on("POST "+orgAsset, map[string]any{"publicationStatusLink": r.srv.URL + "/exchange/api/v2/organizations/" + rootOrg + "/assets/status/1"})
	polls := 0
	r.on("GET /exchange/api/v2/organizations/"+rootOrg+"/assets/status/1", func(*http.Request) reply {
		polls++
		if polls < 2 {
			return reply{200, map[string]any{"status": "running"}}
		}
		return reply{200, map[string]any{"status": "completed"}}
	})
	out := r.mustRun("exchange:asset:upload", "my-api/1.0.0", "--name", "My API", "--type", "rest-api", "--status", "development",
		"--tags", "t1,t2", "--properties", `{"mainFile":"api.raml","apiVersion":"v1"}`, "--categories", `{"Department":["IT","Ops"]}`,
		"--fields", `{"owner":"me"}`, "--files", string(files), "--headers", `{"x-user-id":"u1"}`)
	if !strings.Contains(out, "Publishing completed") || polls != 2 {
		t.Errorf("upload must wait for the publication (polls %d):\n%s", polls, out)
	}
	c := r.last("POST " + orgAsset)
	f := multipartFields(t, c)
	for k, want := range map[string]string{"name": "My API", "type": "rest-api", "status": "development", "tags": "t1,t2",
		"properties.mainFile": "api.raml", "properties.apiVersion": "v1", "categories.Department": "IT,Ops", "fields.owner": "me",
		"files.raml.zip": "ZIP", "files.pom.xml": "<project/>", "organizationId": rootOrg} {
		if f[k] != want {
			t.Errorf("field %s = %q, want %q", k, f[k], want)
		}
	}
	if c.Header.Get("X-User-Id") != "u1" || c.Header.Get("X-From-Service") != "anypoint-cli" {
		t.Errorf("headers: %v", c.Header)
	}

	r.on("GET /exchange/api/v2/organizations/"+rootOrg+"/assets/status/1", map[string]any{"status": "error",
		"steps": []any{map[string]any{"statusCode": 400, "errors": []any{"main file not found"}}}})
	if _, err := r.run("exchange:asset:upload", "my-api/1.0.0", "--name", "x"); err == nil || !strings.Contains(err.Error(), "main file not found") {
		t.Errorf("a failed publication must surface its errors: %v", err)
	}
	if _, err := r.run("exchange:asset:upload", "my-api/1.0.0", "--status", "bogus", "--name", "x"); err == nil {
		t.Error("an unknown --status must be refused")
	}
}

func TestExchangeMutableData(t *testing.T) {
	r := newRecorder(t)
	docs := filepath.Join(t.TempDir(), "docs.zip")
	if err := os.WriteFile(docs, []byte("DOCS"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.on("PATCH "+orgAsset+"/mutabledata", map[string]any{"mutableDataStatusLink": "/exchange/status/2"})
	r.on("GET /exchange/status/2", map[string]any{"status": "completed"})
	r.mustRun("exchange:asset:mutableDataUpload", "my-api/1.0.0", "--tags", "x", "--fields", `{"k":"v"}`, "--docs", docs)
	f := multipartFields(t, r.last("PATCH "+orgAsset+"/mutabledata"))
	if f["tags"] != "x" || f["fields.k"] != "v" || f["files.docs.zip"] != "DOCS" {
		t.Errorf("mutable data fields: %v", f)
	}
	if _, err := r.run("exchange:asset:mutableDataUpload", "my-api/1.0.0"); err == nil {
		t.Error("nothing to upload must be refused")
	}
}

func TestExchangePages(t *testing.T) {
	r := newRecorder(t)
	r.on("GET "+v2Asset+"/portal/pages", []any{map[string]any{"name": "home", "path": "home"}, map[string]any{"name": "Usage", "path": "usage"}})
	r.on("GET "+v2Asset+"/portal/draft", map[string]any{})
	r.on("POST "+v2Asset+"/portal/draft/pages", reply{409, map[string]any{"message": "exists"}})
	r.on("PUT "+v2Asset+"/portal/draft/pages/usage", reply{204, nil})
	r.on("PATCH "+v1Asset, map[string]any{})
	r.on("DELETE "+v2Asset+"/portal/draft/pages/usage", reply{204, nil})
	r.on("GET "+v2Asset+"/portal/pages/usage", "# Usage\n")
	r.on("GET "+v2Asset+"/portal/pages/home", "# Home\n")

	if out := r.mustRun("exchange:asset:page:list", "my-api/1.0.0"); !strings.Contains(out, "Usage") {
		t.Errorf("page list:\n%s", out)
	}

	md := filepath.Join(t.TempDir(), "usage.md")
	if err := os.WriteFile(md, []byte("# New usage"), 0o600); err != nil {
		t.Fatal(err)
	}
	// upload: draft, create (409 tolerated), content, publish
	r.mustRun("exchange:asset:page:upload", "my-api/1.0.0", "usage", md)
	if got := string(r.last("PUT " + v2Asset + "/portal/draft/pages/usage").Body); got != "# New usage" {
		t.Errorf("page content = %q", got)
	}
	if !r.sent("GET "+v2Asset+"/portal/draft") || !r.sent("PATCH "+v1Asset) {
		t.Error("upload must open and publish the portal draft")
	}

	r.on("POST "+v2Asset+"/portal/draft/pages", reply{201, nil})
	r.mustRun("exchange:asset:page:modify", "my-api/1.0.0", "usage", "--name", "how-to")
	c := r.last("POST " + v2Asset + "/portal/draft/pages")
	jsonEq(t, c.JSON(t), "pagePath", "how-to")
	if c.Header.Get("X-Move-Source") != "usage" {
		t.Errorf("X-Move-Source = %q", c.Header.Get("X-Move-Source"))
	}

	r.mustRun("exchange:asset:page:delete", "my-api/1.0.0", "Usage")
	r.last("DELETE " + v2Asset + "/portal/draft/pages/usage")
	if _, err := r.run("exchange:asset:page:delete", "my-api/1.0.0", "nope"); err == nil {
		t.Error("deleting an unknown page must fail")
	}

	dir := t.TempDir()
	r.mustRun("exchange:asset:page:download", "my-api/1.0.0", dir)
	for name, want := range map[string]string{"home.md": "# Home\n", "usage.md": "# Usage\n"} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(b) != want {
			t.Errorf("%s: %q, %v", name, b, err)
		}
	}
}

func TestExchangeResources(t *testing.T) {
	r := newRecorder(t)
	r.on("GET "+v2Asset+"/portal/draft/resources", []any{map[string]any{"path": "resources/logo-1.png"}})
	if out := r.mustRun("exchange:asset:resource:list", "my-api/1.0.0", "-d"); !strings.Contains(out, "resources/logo-1.png") {
		t.Errorf("resource list:\n%s", out)
	}

	img := filepath.Join(t.TempDir(), "logo.png")
	if err := os.WriteFile(img, []byte("PNG"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.on("POST "+v2Asset+"/portal/draft/resources", map[string]any{"path": "resources/logo-2.png"})
	out := r.mustRun("exchange:asset:resource:upload", "my-api/1.0.0", img)
	if !strings.Contains(out, "![logo](resources/logo-2.png)") {
		t.Errorf("upload output:\n%s", out)
	}
	if f := multipartFields(t, r.last("POST "+v2Asset+"/portal/draft/resources")); f["files.png"] != "PNG" {
		t.Errorf("resource part: %v", f)
	}

	r.on("GET "+v2Asset+"/portal/resources/logo-2.png", "PNG")
	dst := filepath.Join(t.TempDir(), "out.png")
	r.mustRun("exchange:asset:resource:download", "my-api/1.0.0", "resources/logo-2.png", dst)
	if b, _ := os.ReadFile(dst); string(b) != "PNG" {
		t.Errorf("downloaded %q", b)
	}

	r.on("DELETE "+v2Asset+"/portal/draft/resources/logo-2.png", reply{204, nil})
	r.mustRun("exchange:asset:resource:delete", "my-api/1.0.0", "resources/logo-2.png")
	r.last("DELETE " + v2Asset + "/portal/draft/resources/logo-2.png")
}

func TestExchangeDownloadAndCopy(t *testing.T) {
	r := newRecorder(t)
	// The file lives on another host (e.g. S3): it must be fetched without the token.
	var auth []string
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		auth = append(auth, req.Header.Get("Authorization"))
		_, _ = io.WriteString(w, "openapi: 3.0.0")
	}))
	defer files.Close()
	asset := map[string]any{"groupId": rootOrg, "assetId": "my-api", "version": "1.0.0", "name": "My API", "type": "rest-api",
		"productAPIVersion": "v1", "dependencies": []any{map[string]any{"groupId": "g", "assetId": "frag", "version": "1.0.0"}},
		"files": []any{
			map[string]any{"classifier": "oas", "packaging": "yaml", "isGenerated": true, "externalLink": files.URL + "/gen.yaml"},
			map[string]any{"classifier": "oas", "packaging": "yaml", "mainFile": "api.yaml", "externalLink": files.URL + "/x/api.yaml?sig=1"},
		}}
	r.on("GET "+v1Asset, asset)

	dir := t.TempDir()
	r.mustRun("exchange:asset:download", "my-api/1.0.0", dir)
	if b, err := os.ReadFile(filepath.Join(dir, "api.yaml")); err != nil || string(b) != "openapi: 3.0.0" {
		t.Errorf("downloaded: %q, %v", b, err)
	}

	target := "/exchange/api/v2/organizations/" + subOrg + "/assets/" + subOrg + "/my-api-copy/2.0.0"
	r.on("POST "+target, map[string]any{"publicationStatusLink": "/exchange/status/3"})
	r.on("GET /exchange/status/3", map[string]any{"status": "completed"})
	r.mustRun("exchange:asset:copy", "my-api/1.0.0", "my-api-copy/2.0.0", "-t", subOrg)
	f := multipartFields(t, r.last("POST "+target))
	for k, want := range map[string]string{"name": "My API", "type": "rest-api", "properties.apiVersion": "v1",
		"properties.mainFile": "api.yaml", "dependencies": "g:frag:1.0.0", "files.oas.yaml": "openapi: 3.0.0"} {
		if f[k] != want {
			t.Errorf("copy field %s = %q, want %q", k, f[k], want)
		}
	}
	for _, a := range auth {
		if a != "" {
			t.Fatalf("token sent to the file host: %q", a)
		}
	}

	asset["type"] = "connector"
	r.on("GET "+v1Asset, asset)
	if _, err := r.run("exchange:asset:copy", "my-api/1.0.0", "x/1.0.0"); err == nil || !strings.Contains(err.Error(), "Supported asset types") {
		t.Errorf("copying a connector must be refused: %v", err)
	}
}
