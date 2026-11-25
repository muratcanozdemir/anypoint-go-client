//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGovernanceAMF runs the governance commands on the real AMF jars (the
// pinned classpath, fetched into the tooling cache on first use) and the
// linked-in custom validator: scaffold a ruleset from an API's types, check
// it against the dialect and the authoring rules, then validate an API that
// breaks it and one that does not.
func TestGovernanceAMF(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	run := func(args ...string) string {
		t.Helper()
		var stderr strings.Builder
		c := exec.CommandContext(t.Context(), cli, args...)
		c.Stderr = &stderr
		out, err := c.Output()
		if err != nil {
			t.Fatalf("anypoint %s: %v\n%s%s", strings.Join(args, " "), err, out, stderr.String())
		}
		return string(out)
	}
	spec := write("types.raml", "#%RAML 1.0\ntitle: People\ntypes:\n  Person:\n    properties:\n      name: string\n      age?: integer\n")
	if out := run("governance:api:inspect", spec); out != "Person\n" {
		t.Fatalf("inspect:\n%s", out)
	}
	rules := write("rules.yaml", run("governance:ruleset:init", spec, "--name", "people"))
	if out := run("governance:ruleset:validate", rules); !strings.Contains(out, "Ruleset conforms with Dialect") {
		t.Fatalf("the scaffold must be a valid ruleset:\n%s", out)
	}
	if out := run("governance:ruleset:validate-authoring", rules); !strings.Contains(out, "Ruleset is valid") {
		t.Fatalf("authoring:\n%s", out)
	}

	write("good/exchange.json", `{"main":"api.raml"}`)
	write("good/api.raml", "#%RAML 1.0\ntitle: Good\ntypes:\n  Person:\n    properties:\n      name: string\n/people:\n  get:\n")
	if out := run("governance:api:validate", filepath.Join(dir, "good"), "--rulesets", rules); !strings.HasPrefix(out, "Conforms: true\n") {
		t.Errorf("a conforming API:\n%s", out)
	}
	write("bad/exchange.json", `{"main":"api.raml"}`)
	write("bad/api.raml", "#%RAML 1.0\ntitle: Bad\ntypes:\n  Person:\n    properties:\n      nick: string\n")
	out := run("governance:api:validate", filepath.Join(dir, "bad"), "--rulesets", rules)
	if !strings.HasPrefix(out, "Conforms: false\n") || !strings.Contains(out, "Constraint: person-shape\nSeverity: Violation\n") ||
		!strings.Contains(out, "Location: file://"+filepath.ToSlash(filepath.Join(dir, "bad", "api.raml"))) {
		t.Errorf("a breaking API:\n%s", out)
	}
	write("broken/exchange.json", `{"main":"api.raml"}`)
	write("broken/api.raml", "#%RAML 1.0\ntitle: Broken\n/people:\n  get:\n    responses:\n      200:\n        body:\n          application/json:\n            type: Missing\n")
	if out := run("governance:api:validate", filepath.Join(dir, "broken"), "--rulesets", rules); !strings.HasPrefix(out, "Conforms: false\n") ||
		!strings.Contains(out, "Functional Validations") {
		t.Errorf("an invalid spec must fail AMF's own validation:\n%s", out)
	}
}
