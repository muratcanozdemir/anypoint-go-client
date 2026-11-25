package main

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A small model in the plugin's resources format: WebAPI → EndPoint →
// Operation, with a non-targetable Payload, and a second spec kind.
var testGovResources = map[string]string{
	"src/resources/models/_prefixes.json": `{"mcp":"http://a.ml/vocabularies/mcp#"}`,
	"src/resources/models/api-spec.json": `{"domain":"api-spec","description":"API specs","specKind":"api-spec","models":{
		"WebAPI":{"name":"WebAPI","properties":["name","endpoint"],"propertyInfo":{"name":{"name":"name","namespace":"http://a.ml/vocabularies/core#"},"endpoint":{"name":"endpoint","range":"EndPoint","isArray":true}},"uris":["http://a.ml/vocabularies/apiContract#WebAPI"]},
		"EndPoint":{"name":"EndPoint","properties":["path","supportedOperation"],"propertyInfo":{"path":{"name":"path"},"supportedOperation":{"name":"supportedOperation","range":"Operation","isArray":true}},"uris":["http://a.ml/vocabularies/apiContract#EndPoint"]},
		"Operation":{"name":"Operation","properties":["method","name","tag"],"propertyInfo":{"method":{"name":"method"},"name":{"name":"name","namespace":"http://a.ml/vocabularies/core#"},"tag":{"name":"tag","isArray":true}},"uris":["http://a.ml/vocabularies/apiContract#Operation"]},
		"Payload":{"name":"Payload","properties":["mediaType"],"propertyInfo":{"mediaType":{"name":"mediaType"}},"uris":["http://a.ml/vocabularies/apiContract#Payload"],"targetable":false}
	},"uriToClassName":{}}`,
	"src/resources/models/mcp.json": `{"domain":"mcp","description":"MCP servers","specKind":"mcp","models":{
		"Server":{"name":"Server","properties":["name"],"propertyInfo":{"name":{"name":"name","namespace":"http://a.ml/vocabularies/core#"}},"uris":["http://a.ml/vocabularies/mcp#Server"]}
	},"uriToClassName":{}}`,
	"src/resources/aliases.json": `{"aliases":[{"alias":"http-method","targetClass":"apiContract.Operation","path":"apiContract.method","description":"The operation's method"}]}`,
	"src/resources/ruleset.schema.json": `{"type":"object","required":["profile"],"additionalProperties":false,
		"properties":{"profile":{"type":"string"},"description":{"type":"string"},"prefixes":{"type":"object"},
		"violation":{"type":"array","items":{"type":"string"}},"warning":{"type":"array","items":{"type":"string"}},"info":{"type":"array","items":{"type":"string"}},
		"validations":{"type":"object","additionalProperties":{"$ref":"#/definitions/validation"}}},
		"definitions":{"validation":{"type":"object","properties":{"message":{"type":"string","description":"The message"},"targetClass":{"type":"string"},"propertyConstraints":{"type":"object"}}},
		"baseValidation":{"properties":{"message":{"type":"string","description":"The message"},"targetClass":{"type":"string"}}},
		"shapeValidation":{"properties":{"propertyConstraints":{"type":"object"}}}}}`,
}

func govAuthoringSetup(t *testing.T) (*recorder, string) {
	t.Helper()
	r := newRecorder(t)
	swapPackage(t, &governancePackage, fakeNPMPackage(t, r, "mulesoft-anypoint-cli-governance-plugin", "9.0.0", testGovResources))
	return r, t.TempDir()
}

func TestGovernanceModelCommands(t *testing.T) {
	r, _ := govAuthoringSetup(t)
	out := r.mustRun("governance:ruleset:domains")
	if !strings.Contains(out, "api-spec\n  Description: API specs\n  Classes: 4\n  Prefixes: apiContract\n  Spec Kind: api-spec\n") ||
		!strings.Contains(out, "mcp\n  Description: MCP servers\n  Classes: 1\n  Prefixes: mcp\n") {
		t.Errorf("domains:\n%s", out)
	}
	out = r.mustRun("governance:ruleset:classes")
	if out != "apiContract:\n  apiContract.EndPoint\n  apiContract.Operation\n  apiContract.WebAPI\nmcp:\n  mcp.Server\n\nTotal: 4 targetable classes\n" {
		t.Errorf("classes:\n%s", out)
	}
	if out := r.mustRun("governance:ruleset:classes", "--domain", "mcp"); !strings.HasPrefix(out, "mcp:\n  mcp.Server\n") || strings.Contains(out, "apiContract") {
		t.Errorf("classes --domain:\n%s", out)
	}
	if _, err := r.run("governance:ruleset:classes", "--domain", "nope"); err == nil || !strings.Contains(err.Error(), "Available: api-spec, mcp") {
		t.Errorf("unknown domain: %v", err)
	}
	out = r.mustRun("governance:ruleset:properties", "apiContract.EndPoint")
	if out != "Properties for apiContract.EndPoint (2 total):\n\n  apiContract.path (scalar)\n  apiContract.supportedOperation (nodeArray) → Operation\n" {
		t.Errorf("properties:\n%s", out)
	}
	if out := r.mustRun("governance:ruleset:properties", "apiContract.WebAPI"); !strings.Contains(out, "core.name (scalar)") {
		t.Errorf("a property's namespace picks its prefix:\n%s", out)
	}
	if _, err := r.run("governance:ruleset:properties", "apiContract.Nope"); err == nil {
		t.Error("unknown class must fail")
	}
	if out := r.mustRun("governance:ruleset:constraints", "--type", "nodeArray"); out != "Constraints for nodeArray:\n\n  atLeast\n  atMost\n  nested\n  minCount\n  maxCount\n  exactCount\n" {
		t.Errorf("constraints:\n%s", out)
	}
	if _, err := r.run("governance:ruleset:constraints", "--type", "x"); err == nil {
		t.Error("unknown constraint type must fail")
	}
	if out := r.mustRun("governance:ruleset:resolve", "HTTP method"); !strings.Contains(out, "Alias: http-method\n  Target Class: apiContract.Operation\n  Path: apiContract.method\n") {
		t.Errorf("resolve:\n%s", out)
	}
	if out := r.mustRun("governance:ruleset:resolve", "zzz"); !strings.HasPrefix(out, `No alias matches for "zzz".`) {
		t.Errorf("resolve none:\n%s", out)
	}
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"api.raml": "#%RAML 1.0\ntitle: x\n"})
	if out := r.mustRun("governance:ruleset:generate", "require tags", "--api-spec", filepath.Join(dir, "api.raml"), "--attempt", "2"); !strings.Contains(out, "  Spec Type: raml\n") ||
		!strings.Contains(out, "retry attempt #2") {
		t.Errorf("generate:\n%s", out)
	}
}

func TestGovernanceValidateAuthoring(t *testing.T) {
	r, dir := govAuthoringSetup(t)
	good := `#%Validation Profile 1.0
profile: p
violation:
  - ops
validations:
  ops:
    message: m
    targetClass: apiContract.EndPoint
    propertyConstraints:
      apiContract.supportedOperation / core.name:
        minCount: 1
`
	writeFiles(t, dir, map[string]string{"good.yaml": good, "bad.yaml": `#%Validation Profile 1.0
profile: p
violation:
  - a
warning:
  - a
validations:
  a:
    targetClass: apiContract.Payload
  b:
    targetClass: apiContract.EndPoint
    propertyConstraints:
      apiContract.path:
        atLeast: {count: 1}
      apiContract.nope:
        minCount: 1
  c:
    targetClass: mcp.Server
extra: 1
`, "broken.yaml": "#%Validation Profile 1.0\nprofile: [\n"})
	if out := r.mustRun("governance:ruleset:validate-authoring", filepath.Join(dir, "good.yaml")); out != "Ruleset is valid\n" {
		t.Errorf("good:\n%s", out)
	}
	out, err := r.run("governance:ruleset:validate-authoring", filepath.Join(dir, "bad.yaml"))
	if err == nil {
		t.Error("authoring errors must fail the command")
	}
	for _, want := range []string{
		"[ERROR] Line 2: Additional property not allowed: extra\n",
		`[ERROR] Line 9: Class "apiContract.Payload" cannot be used as a targetClass directly.`,
		`[WARN] Line 14: Constraint "atLeast" is not applicable to property "apiContract.path" (scalar type).`,
		`[ERROR] Line 15: Invalid property path "apiContract.nope" for class "apiContract.EndPoint": Property 'apiContract.nope' not found on class 'apiContract.EndPoint'. Available properties include: path, supportedOperation...`,
		`[WARN] Line 11: Cross-spec-kind ruleset: validations target multiple incompatible spec kinds (api-spec (b) vs mcp (c)).`,
		`[ERROR] Line 4: Validation "a" is assigned to multiple severity levels: violation, warning.`,
		`[ERROR] Line 6: Validation "a" is assigned to multiple severity levels: violation, warning.`,
		"\n5 error(s), 3 warning(s)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("bad lacks %q:\n%s", want, out)
		}
	}
	if out, _ := r.run("governance:ruleset:validate-authoring", filepath.Join(dir, "broken.yaml")); !strings.Contains(out, "YAML parsing error") {
		t.Errorf("broken:\n%s", out)
	}
}

func TestGovernanceSimplify(t *testing.T) {
	r, dir := govAuthoringSetup(t)
	writeFiles(t, dir, map[string]string{"r.yaml": `#%Validation Profile 1.0
profile: p
validations:
  a:
    message: m
    targetClass: apiContract.EndPoint
    propertyConstraints:
      apiContract.supportedOperation:
        nested:
          propertyConstraints:
            core.name:
              minCount: 1
  b:
    targetClass: apiContract.EndPoint
    not:
      not:
        propertyConstraints:
          apiContract.path:
            pattern: ^/
  c:
    targetClass: apiContract.EndPoint
    and:
      - and:
          - propertyConstraints:
              apiContract.path:
                minLength: 1
      - propertyConstraints:
          apiContract.path:
            maxLength: 9
`})
	out := r.mustRun("governance:ruleset:simplify", filepath.Join(dir, "r.yaml"))
	for _, want := range []string{
		`a: flattened nested under "apiContract.supportedOperation" into path "apiContract.supportedOperation / core.name"`,
		"b: eliminated double negation",
		`c: unwrapped single-child "and"`,
		`c: merged adjacent shape constraints in "and"`,
		"      - propertyConstraints:\n          apiContract.path:\n            minLength: 1\n            maxLength: 9\n",
		"#%Validation Profile 1.0\n\nprofile: p\n",
		"      apiContract.supportedOperation / core.name:\n        minCount: 1\n",
		"  b:\n    targetClass: apiContract.EndPoint\n    propertyConstraints:\n      apiContract.path:\n        pattern: ^/\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("simplify lacks %q:\n%s", want, out)
		}
	}
	writeFiles(t, dir, map[string]string{"s.yaml": "#%Validation Profile 1.0\nprofile: p\n"})
	if out := r.mustRun("governance:ruleset:simplify", filepath.Join(dir, "s.yaml")); !strings.HasPrefix(out, "No simplifications needed.\n") {
		t.Errorf("nothing to do:\n%s", out)
	}
}

func TestGovernanceCompletions(t *testing.T) {
	r, dir := govAuthoringSetup(t)
	text := "#%Validation Profile 1.0\nprofile: p\nviolation:\n  - \nvalidations:\n  ops:\n    targetClass: apiContract.EndPoint\n    propertyConstraints:\n      \n"
	p := filepath.Join(dir, "r.yaml")
	writeFiles(t, dir, map[string]string{"r.yaml": text})
	at := func(marker string, delta int) string {
		i := strings.Index(text, marker)
		if i < 0 {
			t.Fatalf("no %q", marker)
		}
		return r.mustRun("governance:ruleset:completions", p, "--offset", strconv.Itoa(i+len(marker)+delta), "--line-text", "")
	}
	if out := at("violation:\n  - ", 0); !strings.Contains(out, "ops (Validation rule)") {
		t.Errorf("severity list:\n%s", out)
	}
	if out := at("propertyConstraints:\n      ", 0); !strings.Contains(out, "apiContract.path (Property of apiContract.EndPoint)") {
		t.Errorf("property constraints:\n%s", out)
	}
	if out := r.mustRun("governance:ruleset:completions", p, "--offset", "0", "--line-text", ""); !strings.Contains(out, "profile (Profile name)") {
		t.Errorf("root:\n%s", out)
	}
	writeFiles(t, dir, map[string]string{"plain.yaml": "a: 1\n"})
	if out := r.mustRun("governance:ruleset:completions", filepath.Join(dir, "plain.yaml"), "--offset", "1", "--line-text", ""); out != "No completions available at this position.\n" {
		t.Errorf("not a ruleset:\n%s", out)
	}
	if _, err := r.run("governance:ruleset:completions", p); err == nil {
		t.Error("--offset is required")
	}
}
