package main

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The governance commands run AMF through java; these tests put a shell
// script in its place that answers as the bridge would, with an empty AMF
// lock so nothing is downloaded. The rulesets themselves run on the real
// (linked-in) custom validator, over a model AMF rendered for
// testdata/governance (the AMF custom validator's profile9 fixture, which
// has 4 endpoints against a maximum of 3).

func fakeAMF(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fakes")
	}
	old := amfLock
	amfLock = ""
	t.Cleanup(func() { amfLock = old })
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0o750); err != nil {
		t.Fatal(err)
	}
	// $1 -cp, $2 classpath, $3 the bridge source, then the bridge command
	body := "#!/bin/sh\nshift 3\ncmd=$1\n" + script
	if err := os.WriteFile(filepath.Join(home, "bin", "java"), []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JAVA_HOME", home)
}

func testdataPath(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", "governance", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for n, c := range files {
		p := filepath.Join(dir, n)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGovernanceAPIValidate(t *testing.T) {
	r := newRecorder(t)
	t.Setenv("ANYPOINT_HOME", t.TempDir())
	model := testdataPath(t, "api.jsonld")
	fakeAMF(t, `case $cmd in
parse) echo "$2" > "$(dirname "$3")/.parsed-url"; cp `+model+` "$3"
  echo '{"spec":"RAML 1.0","conforms":true,"rendered":true,"results":[{"validationId":"http://a.ml/vocabularies/amf/parser#unused","severityLevel":"Warning","message":"unused type","targetNode":"file://x","targetProperty":"","position":"[(1,0)-(1,4)]","location":""}]}';;
dialect) echo '{"conforms":true,"parseErrors":0,"results":[]}';;
esac`)
	dialect := fakeNPMPackage(t, r, "@aml-org/amf-validation-profile-dialect", "9.0.0", map[string]string{"validation-profile.yaml": "#%Dialect 1.0\n"})
	swapPackage(t, &profileDialectPackage, dialect)

	proj := t.TempDir()
	writeFiles(t, proj, map[string]string{
		"api.raml":      "#%RAML 1.0\ntitle: Test API\n",
		"exchange.json": `{"main":"api.raml","dependencies":[{"groupId":"g","assetId":"frag","version":"1.0.0"}]}`,
	})
	// the fragment dependency and a remote ruleset come from Exchange
	r.on("GET /exchange/api/v2/assets/g/frag/1.0.0", map[string]any{"status": "published", "files": []any{
		map[string]any{"classifier": "raml-fragment", "packaging": "zip", "externalLink": r.srv.URL + "/dl/frag.zip", "mainFile": "lib.raml"}}})
	r.on("GET /dl/frag.zip", zipBytes(t, map[string]string{"lib.raml": "#%RAML 1.0 Library\n"}))
	rules, _ := os.ReadFile(testdataPath(t, "max-endpoints.yaml"))
	r.on("GET /exchange/api/v2/assets/g/rules/1.0.0", map[string]any{"status": "published", "files": []any{
		map[string]any{"classifier": "ruleset", "packaging": "yaml", "isGenerated": true, "externalLink": r.srv.URL + "/nope"},
		map[string]any{"classifier": "ruleset", "packaging": "yaml", "externalLink": r.srv.URL + "/dl/rules.yaml", "mainFile": "ruleset.yaml"}}})
	r.on("GET /dl/rules.yaml", rules)

	out := r.mustRun("governance:api:validate", proj, "--remote-rulesets", "g/rules/1.0.0")
	for _, want := range []string{
		"Conforms: false\nNumber of results: 2\n",
		"\nFunctional Validations\n______________________\n\n\nConstraint: http://a.ml/vocabularies/amf/parser#unused\nSeverity: Warning\n",
		"\n\nConformance Validations\n_______________________\n\n\nConstraint: max-endpoints\nSeverity: Violation\nMessage: maximum number of endpoints test\n",
		"Property: http://a.ml/vocabularies/apiContract#endpoint\nRange: [(3,0)-(28,20)]\nLocation: file://" + filepath.ToSlash(proj) + "/api.raml\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("validate output lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(proj, "exchange_modules")); err == nil {
		t.Error("validate must not change the project")
	}
	deps := filepath.Join(os.Getenv("ANYPOINT_HOME"), ".anypoint_deps", "g", "frag", "1.0.0", "published", "lib.raml")
	if _, err := os.Stat(deps); err != nil {
		t.Errorf("dependency cache: %v", err)
	}
	calls := countCalls(r, "GET /dl/frag.zip")
	r.mustRun("governance:api:validate", proj, "--rulesets", testdataPath(t, "max-endpoints.yaml"), "-o", "json")
	if countCalls(r, "GET /dl/frag.zip") != calls {
		t.Error("a cached dependency must not be fetched again")
	}
	r.mustRun("governance:api:validate", proj, "--force")
	if countCalls(r, "GET /dl/frag.zip") != calls+1 {
		t.Error("--force must fetch again")
	}

	var got struct {
		Conforms bool        `json:"conforms"`
		Results  []govResult `json:"results"`
	}
	out = r.mustRun("governance:api:validate", proj, "--rulesets", testdataPath(t, "max-endpoints.yaml"), "-o", "json")
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Conforms || len(got.Results) != 2 || got.Results[1].ValidationID != "max-endpoints" {
		t.Errorf("json: %v %+v", err, got)
	}

	if _, err := r.run("governance:api:validate", t.TempDir()); err == nil || !strings.Contains(err.Error(), "exchange.json") {
		t.Errorf("a project needs exchange.json: %v", err)
	}
	if _, err := r.run("governance:api:validate", "g/a", "--remote"); err == nil || !strings.Contains(err.Error(), "group/asset/version") {
		t.Errorf("bad GAV: %v", err)
	}
}

func zipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b strings.Builder
	zw := zip.NewWriter(&b)
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
	return []byte(b.String())
}

func TestGovernanceRulesetDialect(t *testing.T) {
	r := newRecorder(t)
	fakeAMF(t, `case "$3" in
*bad*) echo '{"conforms":false,"parseErrors":1,"results":[{"validationId":"http://a.ml/vocabularies/amf/aml#closed-shape","severityLevel":"Violation","message":"Property: 'version' not supported","targetNode":"file:///r#/encodes","position":"[(5,9)-(5,14)]","location":"file:///r"}]}';;
*) echo '{"conforms":true,"parseErrors":0,"results":[]}';;
esac`)
	swapPackage(t, &profileDialectPackage, fakeNPMPackage(t, r, "@aml-org/amf-validation-profile-dialect", "9.0.0", map[string]string{"validation-profile.yaml": "x"}))
	dir := t.TempDir()
	good := testdataPath(t, "max-endpoints.yaml")
	bad := filepath.Join(dir, "bad.yaml")
	writeFiles(t, dir, map[string]string{"bad.yaml": "#%Validation Profile 1.0\nprofile: x\n"})

	if out := r.mustRun("governance:ruleset:validate", good); out != "Ruleset conforms with Dialect\n" {
		t.Errorf("validate:\n%s", out)
	}
	out := r.mustRun("governance:ruleset:validate", bad)
	if !strings.Contains(out, "Parsing errors found: 1\nRuleset does not conform with Dialect\n") || !strings.Contains(out, "Constraint: http://a.ml/vocabularies/amf/aml#closed-shape") {
		t.Errorf("validate bad:\n%s", out)
	}
	// a ruleset project: exchange.json names the main file
	writeFiles(t, dir, map[string]string{"proj/exchange.json": `{"main":"rules.yaml"}`, "proj/rules.yaml": "x"})
	r.mustRun("governance:ruleset:validate", filepath.Join(dir, "proj"))
	if _, err := r.run("governance:ruleset:validate", filepath.Join(dir, "r.txt")); err == nil || !strings.Contains(err.Error(), "not a supported type") {
		t.Errorf("unsupported ruleset type: %v", err)
	}

	out = r.mustRun("governance:ruleset:info", good)
	if !strings.Contains(out, "Ruleset conforms with Dialect\n") || !strings.Contains(out, "Violation  max-endpoints") {
		t.Errorf("info:\n%s", out)
	}

	out = r.mustRun("governance:ruleset:clone", good, "Copy", "A copy", "--warning", "max-endpoints")
	if !strings.HasPrefix(out, "#%Validation Profile 1.0\nprofile: Copy\n") || !strings.Contains(out, "warning:\n  - max-endpoints\n") ||
		strings.Contains(out, "violation:") || !strings.Contains(out, "description: A copy\n") {
		t.Errorf("clone:\n%s", out)
	}
	out = r.mustRun("governance:ruleset:clone", good, "Copy", "d", "--remove", "max-endpoints")
	if strings.Contains(out, "max-endpoints") {
		t.Errorf("a removed rule must leave the ruleset:\n%s", out)
	}
	if _, err := r.run("governance:ruleset:clone", good, "C", "d", "--info", "nope"); err == nil || !strings.Contains(err.Error(), "rule nope does not exist") {
		t.Errorf("clone unknown rule: %v", err)
	}
	if _, err := r.run("governance:ruleset:clone", good, "C", "d", "--info", "a", "--remove", "a"); err == nil || !strings.Contains(err.Error(), "move and remove") {
		t.Errorf("clone move+remove: %v", err)
	}

	zipPath := filepath.Join(dir, "docs.zip")
	r.mustRun("governance:document", good, zipPath)
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	pages := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		pages[f.Name] = string(b)
	}
	if !strings.Contains(pages["home.md"], "# Test9") || !strings.Contains(pages["home.md"], "* [max-endpoints](pages/max-endpoints)") ||
		pages["config.json"] != "{ \n\"pageOrder\": [\"home\", \"max-endpoints\"] \n}" ||
		!strings.Contains(pages["max-endpoints.md"], "##### Type: Declarative Validation") {
		t.Errorf("document pages: %q", pages)
	}

	if out := r.mustRun("governance:ruleset:version"); !strings.HasPrefix(out, "governance-ruleset-tools v1.1.5") {
		t.Errorf("version: %s", out)
	}
	if out := r.mustRun("governance:update"); !strings.Contains(out, "pinned") {
		t.Errorf("update: %s", out)
	}
}

func TestGovernanceScaffold(t *testing.T) {
	r := newRecorder(t)
	fakeAMF(t, `echo '{"conforms":true,"results":[],"declarations":[{"id":"amf://id#3","name":"Person","nodeShape":true,"properties":[{"name":"name","minCount":1},{"name":"age","minCount":0}]},{"id":"amf://id#5","name":"Code"},{"id":"amf://id#6","name":"Tag","nodeShape":true,"properties":[{"name":"label","minCount":0}]}]}'`)
	spec := filepath.Join(t.TempDir(), "api.raml")
	writeFiles(t, filepath.Dir(spec), map[string]string{"api.raml": "#%RAML 1.0\n"})
	if out := r.mustRun("governance:api:inspect", spec); out != "Person\nCode\nTag\n" {
		t.Errorf("inspect:\n%s", out)
	}
	out := r.mustRun("governance:ruleset:init", spec, "--types", "Person", "--types", "Tag", "--name", "mine")
	want := `#%Validation Profile 1.0

profile: mine

violation:
  - person-shape
  - tag-shape

validations:
  person-shape:
    message: Schema for resource of type " + name + " is not valid
    targetClass: shacl.NodeShape
    if:
      propertyConstraints:
        shacl.name:
          pattern: Person
    then:
      and:
        - propertyConstraints:
            shacl.property:
              atLeast:
                count: 1
                validation:
                  propertyConstraints:
                    shacl.name:
                      pattern: name
        - propertyConstraints:
            shacl.property/shacl.name:
              in:
                - name
                - age
  tag-shape:
    message: Schema for resource of type " + name + " is not valid
    targetClass: shacl.NodeShape
    if:
      propertyConstraints:
        shacl.name:
          pattern: Tag
    then:
      propertyConstraints:
        shacl.property/shacl.name:
          in:
            - label
`
	if out != want {
		t.Errorf("init:\n%s", out)
	}
	if _, err := r.run("governance:ruleset:init", spec, "--types", "Code"); err == nil {
		t.Error("a scalar type has no properties to scaffold")
	}
	if got := scaffoldRuleName("myHTTPType.v2"); got != "my-h-t-t-p-type-v2-shape" {
		t.Errorf("rule name %s", got)
	}
}
