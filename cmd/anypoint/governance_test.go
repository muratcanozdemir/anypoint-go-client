package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const gov = "/governance/xapi/api/v1"

func TestGovernanceProfiles(t *testing.T) {
	r := newRecorder(t)
	r.on("GET /exchange/api/v2/assets/"+rootOrg+"/owasp/1.0.0", map[string]any{"type": "ruleset"})
	r.on("GET /exchange/api/v2/assets/"+rootOrg+"/best", map[string]any{"type": "ruleset"})
	r.on("GET /exchange/api/v2/assets/"+rootOrg+"/orders-api/1.0.0", map[string]any{"type": "rest-api"})
	created := map[string]any{"id": "prof-1", "name": "web", "description": "d", "filter": "scope:rest-api",
		"rulesets": []any{map[string]any{"uri": "gav://" + rootOrg + "/owasp/1.0.0"}}, "allowing": []any{"x"}, "denying": []any{},
		"notificationConfig": map[string]any{"enabled": true, "notifications": []any{map[string]any{"condition": "OnFailure", "enabled": true,
			"recipients": []any{map[string]any{"contactType": "Publisher"}, map[string]any{"contactType": "Others", "value": "a@b.c"}}}}}}
	r.on("POST "+gov+"/profiles", created)

	out := r.mustRun("governance:profile:create", "web", rootOrg+"/owasp/1.0.0,"+rootOrg+"/best/latest", "--criteria", "scope:rest-api",
		"--notify-publisher", "--notify-others", "a@b.c")
	if !strings.Contains(out, "Publisher,a@b.c") {
		t.Errorf("create output:\n%s", out)
	}
	c := r.last("POST " + gov + "/profiles")
	if !strings.HasPrefix(c.Header.Get("X-Request-Id"), "anypoint-cli-governance-") {
		t.Errorf("request id = %q", c.Header.Get("X-Request-Id"))
	}
	b := c.JSON(t)
	jsonEq(t, b, "rulesets", []any{"gav://" + rootOrg + "/owasp/1.0.0", "gav://" + rootOrg + "/best/latest"})
	jsonEq(t, b, "org", rootOrg)
	jsonEq(t, b, "notificationConfig.notifications.0.recipients", []any{
		map[string]any{"contactType": "Publisher", "notificationType": "Email", "label": "", "value": ""},
		map[string]any{"contactType": "Others", "notificationType": "Email", "label": "", "value": "a@b.c"}})
	r.mustRun("governance:profile:create", "quiet", rootOrg+"/owasp/1.0.0")
	jsonEq(t, r.last("POST "+gov+"/profiles").JSON(t), "notificationConfig.enabled", false)
	for _, bad := range []string{rootOrg + "/owasp", rootOrg + "/orders-api/1.0.0", rootOrg + "/missing/1.0.0"} {
		if _, err := r.run("governance:profile:create", "x", bad); err == nil {
			t.Errorf("ruleset %s must be refused", bad)
		}
	}

	r.on("GET "+gov+"/profiles/prof-1", created)
	r.on("PUT "+gov+"/profiles/prof-1", reply{204, nil})
	r.mustRun("governance:profile:update", "prof-1", "--profile-name", "web2", "--notify-off")
	c = r.last("PUT " + gov + "/profiles/prof-1")
	if c.Header.Get("If-Match") != "*" {
		t.Error("update must send If-Match: *")
	}
	b = c.JSON(t)
	jsonEq(t, b, "name", "web2")
	jsonEq(t, b, "description", "d")
	jsonEq(t, b, "allowing", []any{"x"})
	jsonEq(t, b, "rulesets", []any{"gav://" + rootOrg + "/owasp/1.0.0"})
	jsonEq(t, b, "notificationConfig.enabled", false)
	jsonEq(t, b, "notificationConfig.notifications.0.recipients.1.value", "a@b.c")

	if out := r.mustRun("governance:profile:info", "prof-1"); !strings.Contains(out, rootOrg+"/owasp/1.0.0") {
		t.Errorf("info:\n%s", out)
	}
	r.on("GET "+gov+"/profiles", []any{map[string]any{"id": "prof-1", "name": "web"}})
	if out := r.mustRun("governance:profile:list"); !strings.Contains(out, "prof-1") {
		t.Errorf("list:\n%s", out)
	}
	if q := r.last("GET " + gov + "/profiles").Query; q != "organization="+rootOrg {
		t.Errorf("list query = %q", q)
	}
	r.on("DELETE "+gov+"/profiles/prof-1", reply{204, nil})
	if out := r.mustRun("governance:profile:delete", "prof-1"); !strings.Contains(out, "Profile with id prof-1 removed") {
		t.Errorf("delete: %s", out)
	}
	r.on("DELETE "+gov+"/profiles/prof-1", reply{404, map[string]any{"message": "gone"}})
	if _, err := r.run("governance:profile:delete", "prof-1"); err == nil || !strings.Contains(err.Error(), "requestId anypoint-cli-governance-") {
		t.Errorf("a failed delete reports its request id: %v", err)
	}
}

func TestGovernanceEvaluate(t *testing.T) {
	r := newRecorder(t)
	r.on("POST "+gov+"/plan/explain", map[string]any{"profiles": []any{
		map[string]any{"rulesets": []any{map[string]any{"uri": "gav://g/a/1"}, map[string]any{"uri": "gav://g/b/2"}}},
		map[string]any{"rulesets": []any{map[string]any{"uri": "gav://g/a/1"}}}}})
	out := r.mustRun("governance:api:evaluate", "--criteria", "tag:pci")
	if strings.Count(out, "g/a/1") != 1 || !strings.Contains(out, "g/b/2") {
		t.Errorf("evaluate:\n%s", out)
	}
	q := r.last("POST " + gov + "/plan/explain").Query
	if !strings.Contains(q, "filter=tag%3Apci%2Cscope%3Arest-api%2Cscope%3Aevented-api%2Cscope%3Aasync-api") || !strings.Contains(q, "organization="+rootOrg) {
		t.Errorf("evaluate query = %s", q)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "exchange.json"), []byte(`{"groupId":"`+subOrg+`","assetId":"orders-api","version":"1.0.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r.mustRun("governance:api:evaluate", "--api", dir, "--criteria", "scope:rest-api")
	q = r.last("POST " + gov + "/plan/explain").Query
	if !strings.Contains(q, "assetId=orders-api") || !strings.Contains(q, "organization="+subOrg) || strings.Contains(q, "async") {
		t.Errorf("evaluate --api query = %s", q)
	}
	if _, err := r.run("governance:api:evaluate"); err == nil {
		t.Error("evaluate needs --api or --criteria")
	}
	if _, err := r.run("governance:api:evaluate", "--api", filepath.Join(dir, "nope")); err == nil {
		t.Error("a missing --api path must fail")
	}
}
