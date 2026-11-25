package main

import (
	"strings"
	"testing"
)

func TestAccountBusinessGroups(t *testing.T) {
	r := newRecorder(t)
	out := r.mustRun("account:business-group:list")
	for _, want := range []string{"Root Org", "Root", "Team BG", "Business unit", subOrg} {
		if !strings.Contains(out, want) {
			t.Errorf("list output missing %q:\n%s", want, out)
		}
	}

	r.on("GET /accounts/api/organizations/"+subOrg, map[string]any{
		"id": subOrg, "name": "Team BG", "isRoot": false, "owner": map[string]any{"id": userID, "username": "jdoe"},
		"environments": []any{map[string]any{"id": sbxEnv, "name": "Sandbox"}},
		"entitlements": map[string]any{"vCoresSandbox": map[string]any{"assigned": 1}, "hybrid": map[string]any{"enabled": true}},
	})
	// by name; then by ID with -o json, which must print the API's JSON as is
	out = r.mustRun("account", "business-group", "describe", "Team BG")
	for _, want := range []string{"Team BG", "jdoe", "Business unit", "Sandbox", "hybrid, vCoresSandbox"} {
		if !strings.Contains(out, want) {
			t.Errorf("describe output missing %q:\n%s", want, out)
		}
	}
	out = r.mustRun("account:business-group:describe", subOrg, "-o", "json")
	if !strings.Contains(out, `"assigned": 1`) {
		t.Errorf("-o json must keep fields the model does not declare:\n%s", out)
	}
	if _, err := r.run("account:business-group:describe", "nope"); err == nil || !strings.Contains(err.Error(), "nope not found") {
		t.Errorf("unknown business group: got %v", err)
	}
}

func TestAccountEnvironments(t *testing.T) {
	r := newRecorder(t)
	envs := "/accounts/api/organizations/" + rootOrg + "/environments"

	out := r.mustRun("account:environment:list")
	if !strings.Contains(out, "Sandbox") || !strings.Contains(out, "Production") {
		t.Errorf("list:\n%s", out)
	}

	// describe with no name: the first production environment, as in anypoint-cli-v4
	out = r.mustRun("account:environment:describe")
	if !strings.Contains(out, prodEnv) {
		t.Errorf("default environment should be Production:\n%s", out)
	}
	out = r.mustRun("account:environment:describe", "sandbox")
	if !strings.Contains(out, sbxEnv) {
		t.Errorf("describe sandbox:\n%s", out)
	}

	r.on("POST "+envs, reply{201, map[string]any{"id": "new", "name": "QA", "type": "design", "isProduction": false}})
	out = r.mustRun("account", "env", "create", "QA", "-t", "design")
	body := r.last("POST " + envs).JSON(t)
	jsonEq(t, body, "name", "QA")
	jsonEq(t, body, "type", "design")
	jsonEq(t, body, "organizationId", rootOrg)
	if !strings.Contains(out, "Environment QA created") {
		t.Errorf("create output:\n%s", out)
	}
	if _, err := r.run("account:environment:create", "QA", "--type", "bogus"); err == nil {
		t.Error("an unknown --type must be refused")
	}

	r.on("DELETE "+envs+"/"+sbxEnv, reply{204, nil})
	out = r.mustRun("account:environment:delete", "Sandbox")
	if !strings.Contains(out, "Environment Sandbox deleted successfully") {
		t.Errorf("delete output:\n%s", out)
	}
	if _, err := r.run("account:environment:delete"); err == nil {
		t.Error("delete without NAME must fail")
	}
}

func TestAccountUserAndPreferredEnvironment(t *testing.T) {
	r := newRecorder(t)
	r.on("GET /accounts/api/users/me", map[string]any{"id": userID, "username": "jdoe", "firstName": "Jane", "lastName": "Doe",
		"email": "j@example.com", "createdAt": "2024-01-02T03:04:05Z"})
	out := r.mustRun("account:user:describe")
	for _, want := range []string{"jdoe", "Jane Doe", "2024-01-02"} {
		if !strings.Contains(out, want) {
			t.Errorf("user describe missing %q:\n%s", want, out)
		}
	}

	// The user's preferred environment for the org wins over the production default.
	r.on("GET /accounts/api/me", map[string]any{"user": map[string]any{
		"id": userID, "organization": map[string]any{"id": rootOrg},
		"organizationPreferences": map[string]any{rootOrg: map[string]any{"defaultEnvironment": sbxEnv}},
	}})
	if out := r.mustRun("account:environment:describe"); !strings.Contains(out, sbxEnv) {
		t.Errorf("preferred environment should be Sandbox:\n%s", out)
	}
}

func TestV4GlobalFlags(t *testing.T) {
	r := newRecorder(t)
	t.Setenv("ANYPOINT_BEARER", "")
	r.mustRun("account:environment:list", "--bearer", "flag-token", "--organization", rootOrg, "--collectMetrics")
	if got := r.last("GET /accounts/api/organizations/" + rootOrg + "/environments").Header.Get("Authorization"); got != "Bearer flag-token" {
		t.Errorf("--bearer: Authorization = %q", got)
	}
	if _, err := r.run("account:environment:nope"); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("unknown v4 command: %v", err)
	}
}
