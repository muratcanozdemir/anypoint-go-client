package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const hy = "/hybrid/api/v1"

func data(v any) map[string]any { return map[string]any{"data": v} }

func hybridTargets(r *recorder) {
	r.on("GET "+hy+"/servers", data([]any{map[string]any{"id": 1, "name": "srv1", "status": "RUNNING", "timeUpdated": 1700000000000}}))
	r.on("GET "+hy+"/serverGroups", data([]any{map[string]any{"id": 2, "name": "grp", "servers": []any{map[string]any{"id": 1}}}}))
	r.on("GET "+hy+"/clusters", data([]any{map[string]any{"id": 3, "name": "clu", "multicastEnabled": true, "servers": []any{}}}))
}

func TestHybridServersGroupsClusters(t *testing.T) {
	r := newRecorder(t)
	hybridTargets(r)
	out := r.mustRun("runtime-mgr:server:list")
	if !strings.Contains(out, "srv1") || !strings.Contains(out, "ago") {
		t.Errorf("server list:\n%s", out)
	}
	c := r.last("GET " + hy + "/servers")
	if c.Header.Get("X-ANYPNT-ORG-ID") != rootOrg || c.Header.Get("X-ANYPNT-ENV-ID") != prodEnv || c.Header.Get("x-organization-id") != rootOrg {
		t.Errorf("hybrid scope headers: %v", c.Header)
	}

	r.on("GET "+hy+"/servers/1", data(map[string]any{"id": 1, "name": "srv1", "muleVersion": "4.6.0",
		"addresses": []any{map[string]any{"ip": "10.0.0.1", "networkInterface": "eth0"}}}))
	if out := r.mustRun("runtime-mgr:server:describe", "1"); !strings.Contains(out, "10.0.0.1") || !strings.Contains(out, "4.6.0") {
		t.Errorf("server describe:\n%s", out)
	}
	r.on("PATCH "+hy+"/servers/1", data(map[string]any{})).on("DELETE "+hy+"/servers/1", reply{204, nil})
	r.mustRun("runtime-mgr:server:modify", "1", "--name", "renamed")
	jsonEq(t, r.last("PATCH "+hy+"/servers/1").JSON(t), "name", "renamed")
	if _, err := r.run("runtime-mgr:server:modify", "1"); err == nil {
		t.Error("--name is required")
	}
	if out := r.mustRun("runtime-mgr:server:delete", "1"); !strings.Contains(out, "Server with ID 1 deleted") {
		t.Errorf("server delete: %s", out)
	}
	r.on("GET "+hy+"/servers/registrationToken", data("tok-123"))
	if out := r.mustRun("runtime-mgr:server:token"); strings.TrimSpace(out) != "tok-123" {
		t.Errorf("token: %q", out)
	}

	r.on("POST "+hy+"/serverGroups", reply{201, data(map[string]any{"id": 9})})
	r.mustRun("runtime-mgr:serverGroup:create", "grp2", "1", "4")
	jsonEq(t, r.last("POST "+hy+"/serverGroups").JSON(t), "serverIds", []any{"1", "4"})
	r.mustRun("runtime-mgr:serverGroup:create", "empty")
	jsonEq(t, r.last("POST "+hy+"/serverGroups").JSON(t), "serverIds", []any{})
	r.on("POST "+hy+"/serverGroups/2/servers/5", reply{200, nil}).on("DELETE "+hy+"/serverGroups/2/servers/5", reply{204, nil})
	r.mustRun("runtime-mgr:serverGroup:add:server", "2", "5")
	r.mustRun("runtime-mgr:serverGroup:remove:server", "2", "5")
	r.on("GET "+hy+"/serverGroups/2", data(map[string]any{"id": 2, "name": "grp", "servers": []any{map[string]any{"id": 1, "name": "srv1", "status": "RUNNING"}}}))
	if out := r.mustRun("runtime-mgr:serverGroup:describe", "2"); !strings.Contains(out, "Server 1 Name") {
		t.Errorf("group describe:\n%s", out)
	}
	if out := r.mustRun("runtime-mgr:serverGroup:list"); !strings.Contains(out, "grp") {
		t.Errorf("group list:\n%s", out)
	}

	r.on("POST "+hy+"/clusters", reply{201, data(map[string]any{"id": 7})})
	out = r.mustRun("runtime-mgr:cluster:create", "c1", "--server", "1:10.0.0.1", "--server", "4", "--multicast")
	if !strings.Contains(out, "Created cluster with ID 7") {
		t.Errorf("cluster create: %s", out)
	}
	b := r.last("POST " + hy + "/clusters").JSON(t)
	jsonEq(t, b, "servers", []any{map[string]any{"serverId": "1", "serverIp": "10.0.0.1"}, map[string]any{"serverId": "4"}})
	jsonEq(t, b, "multicastEnabled", true)
	if _, err := r.run("runtime-mgr:cluster:create", "c1"); err == nil {
		t.Error("--server is required")
	}
	r.on("POST "+hy+"/clusters/3/servers", reply{201, nil}).on("DELETE "+hy+"/clusters/3/servers/1", reply{204, nil})
	r.mustRun("runtime-mgr:cluster:add:server", "3", "1", "10.0.0.9")
	jsonEq(t, r.last("POST "+hy+"/clusters/3/servers").JSON(t), "serverIp", "10.0.0.9")
	r.mustRun("runtime-mgr:cluster:remove:server", "3", "1")
	if out := r.mustRun("runtime-mgr:cluster:list"); !strings.Contains(out, "Enabled") {
		t.Errorf("cluster list:\n%s", out)
	}

	r.on("GET /amc/atlas/api/v1/organizations/"+rootOrg+"/providers/MC/targets", []any{
		map[string]any{"id": "rtf1", "name": "fabric", "version": "2.1", "isAvailableForDeployments": true, "provider": "MC"}})
	if out := r.mustRun("runtime-mgr:rtf:list"); !strings.Contains(out, "fabric") {
		t.Errorf("rtf list:\n%s", out)
	}
}

func TestHybridApplications(t *testing.T) {
	r := newRecorder(t)
	hybridTargets(r)
	app := map[string]any{"id": 50, "name": "orders", "desiredStatus": "STARTED", "lastReportedStatus": "STARTED",
		"artifact": map[string]any{"name": "orders", "fileName": "orders.jar"}, "target": map[string]any{"id": 1, "name": "srv1"}}
	r.on("GET "+hy+"/applications", data([]any{app}))
	r.on("GET "+hy+"/applications/orders", reply{404, map[string]any{"message": "not found"}})
	r.on("GET "+hy+"/applications/50", data(app))
	r.on("PATCH "+hy+"/applications/50", data(app))

	if out := r.mustRun("runtime-mgr:standalone-application:describe", "orders"); !strings.Contains(out, "orders.jar") {
		t.Errorf("describe by name:\n%s", out)
	}
	r.mustRun("runtime-mgr:standalone-application:stop", "50")
	jsonEq(t, r.last("PATCH "+hy+"/applications/50").JSON(t), "desiredStatus", "STOPPED")
	if _, err := r.run("runtime-mgr:standalone-application:start", "nope"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown app: %v", err)
	}
	r.mustRun("runtime-mgr:standalone-application:restart", "orders")
	if f := multipartFields(t, r.last("PATCH "+hy+"/applications/50")); f["id"] != "50" {
		t.Errorf("restart form: %v", f)
	}

	zip := filepath.Join(t.TempDir(), "orders.jar")
	if err := os.WriteFile(zip, []byte("JAR"), 0o600); err != nil {
		t.Fatal(err)
	}
	// same name on the target: redeploy
	r.mustRun("runtime-mgr:standalone-application:deploy", "srv1", "orders", zip)
	if f := multipartFields(t, r.last("PATCH "+hy+"/applications/50")); f["file"] != "JAR" {
		t.Errorf("redeploy form: %v", f)
	}
	if q := r.last("GET " + hy + "/applications").Query; q != "targetId=1" {
		t.Errorf("deploy app lookup query = %q", q)
	}
	// new name on a cluster (by ID): deploy
	r.on("POST "+hy+"/applications", reply{201, data(map[string]any{"id": 51})})
	out := r.mustRun("runtime-mgr:standalone-application:deploy", "3", "billing", zip)
	if !strings.Contains(out, "Application with ID 51 created") {
		t.Errorf("deploy: %s", out)
	}
	c := r.last("POST " + hy + "/applications")
	if f := multipartFields(t, c); f["artifactName"] != "billing" || f["targetId"] != "3" || f["file"] != "JAR" {
		t.Errorf("deploy form: %v", f)
	}
	if c.Header.Get("x-from-service") != "anypoint-cli" {
		t.Error("deploy must send x-from-service")
	}
	if _, err := r.run("runtime-mgr:standalone-application:deploy", "nowhere", "x", zip); err == nil {
		t.Error("an unknown target must fail")
	}

	r.on("GET "+hy+"/applications/50/artifact", "ZIPDATA")
	dir := t.TempDir()
	r.mustRun("runtime-mgr:standalone-application:artifact", "50", dir)
	if b, err := os.ReadFile(filepath.Join(dir, "app_50_artifact.zip")); err != nil || string(b) != "ZIPDATA" {
		t.Errorf("artifact: %q, %v", b, err)
	}

	// modify: insight on and a log level, through the target's agent components
	comps := hy + "/targets/1/components"
	r.on("GET "+comps, data([]any{
		map[string]any{"component": map[string]any{"id": 11, "name": "mule.agent.tracking.handler.analytics"}, "enabled": false},
		map[string]any{"component": map[string]any{"id": 12, "name": "mule.agent.tracking.service"},
			"configuration": map[string]any{"trackedApplications": []any{map[string]any{"appName": "other"}}}},
		map[string]any{"component": map[string]any{"id": 13, "name": "mule.agent.logging.service"},
			"configuration": map[string]any{"applicationLogLevels": []any{map[string]any{"artifactName": "orders", "old": true}}}},
	}))
	for _, id := range []string{"11", "12", "13"} {
		r.on("PATCH "+comps+"/"+id, data(map[string]any{}))
	}
	r.mustRun("runtime-mgr:standalone-application:modify", "orders", "--insight", "--log", "DEBUG:org.mule")
	jsonEq(t, r.last("PATCH "+comps+"/11").JSON(t), "enabled", true)
	jsonEq(t, r.last("PATCH "+comps+"/12").JSON(t), "configuration.trackedApplications",
		[]any{map[string]any{"appName": "other"}, map[string]any{"appName": "orders", "trackingLevel": "DEBUG"}})
	jsonEq(t, r.last("PATCH "+comps+"/13").JSON(t), "configuration.applicationLogLevels",
		[]any{map[string]any{"artifactName": "orders", "scopeLoggingConfigurations": []any{map[string]any{"logLevel": "DEBUG", "scope": "org.mule"}}}})
	if _, err := r.run("runtime-mgr:standalone-application:modify", "orders", "--log", "LOUD:x"); err == nil {
		t.Error("an unknown log level must fail")
	}

	r.on("DELETE "+hy+"/applications/50", reply{204, nil})
	if out := r.mustRun("runtime-mgr:standalone-application:delete", "orders"); !strings.Contains(out, "Application with ID 50 deleted") {
		t.Errorf("delete: %s", out)
	}
	r.on("GET "+hy+"/applications", data([]any{app}))
	if out := r.mustRun("runtime-mgr:standalone-application:list", "--limit", "3"); !strings.Contains(out, "srv1") {
		t.Errorf("list:\n%s", out)
	}
	if q := r.last("GET " + hy + "/applications").Query; !strings.Contains(q, "limit=3") || !strings.Contains(q, "sort=timeCreated") {
		t.Errorf("list query = %s", q)
	}
}

func TestHybridApplicationCopy(t *testing.T) {
	old := pollInterval
	pollInterval = time.Millisecond
	t.Cleanup(func() { pollInterval = old })

	r := newRecorder(t)
	hybridTargets(r)
	src := map[string]any{"id": 50, "name": "orders", "desiredStatus": "STOPPED", "artifact": map[string]any{"fileName": "orders.jar"},
		"target": map[string]any{"id": 1}}
	// source in Production, target in Sandbox: the env header tells them apart
	r.on("GET "+hy+"/applications", func(req *http.Request) reply {
		if req.Header.Get("X-ANYPNT-ENV-ID") == prodEnv {
			return reply{200, data([]any{src})}
		}
		return reply{200, data([]any{})}
	})
	r.on("GET "+hy+"/applications/50/artifact", "JAR")
	r.on("POST "+hy+"/applications", reply{201, data(map[string]any{"id": 60})})
	polls := 0
	r.on("GET "+hy+"/applications/60", func(*http.Request) reply {
		polls++
		status := "DEPLOYING"
		if polls > 1 {
			status = "STARTED"
		}
		return reply{200, data(map[string]any{"id": 60, "name": "orders-copy", "desiredStatus": "STARTED", "lastReportedStatus": status,
			"target": map[string]any{"id": 2}})}
	})
	r.on("PATCH "+hy+"/applications/60", data(map[string]any{}))
	comps := func(target string, analytics bool, tracked, logged []any) {
		r.on("GET "+hy+"/targets/"+target+"/components", data([]any{
			map[string]any{"component": map[string]any{"id": target + "1", "name": "mule.agent.tracking.handler.analytics"}, "enabled": analytics},
			map[string]any{"component": map[string]any{"id": target + "2", "name": "mule.agent.tracking.service"}, "configuration": map[string]any{"trackedApplications": tracked}},
			map[string]any{"component": map[string]any{"id": target + "3", "name": "mule.agent.logging.service"}, "configuration": map[string]any{"applicationLogLevels": logged}},
		}))
		for _, n := range []string{"1", "2", "3"} {
			r.on("PATCH "+hy+"/targets/"+target+"/components/"+target+n, data(map[string]any{}))
		}
	}
	comps("1", true, []any{map[string]any{"appName": "orders"}}, []any{map[string]any{"artifactName": "orders",
		"scopeLoggingConfigurations": []any{map[string]any{"logLevel": "WARN", "scope": "x"}}}})
	comps("2", false, []any{}, []any{})

	out := r.mustRun("runtime-mgr:standalone-application:copy", rootOrg+":Production/orders", rootOrg+":Sandbox/orders-copy", "grp")
	if !strings.Contains(out, "Application copied with ID: 60") || polls < 2 {
		t.Errorf("copy (polls %d):\n%s", polls, out)
	}
	dep := r.last("POST " + hy + "/applications")
	if dep.Header.Get("X-ANYPNT-ENV-ID") != sbxEnv {
		t.Errorf("deploy must go to the target environment, got %s", dep.Header.Get("X-ANYPNT-ENV-ID"))
	}
	if f := multipartFields(t, dep); f["artifactName"] != "orders-copy" || f["targetId"] != "2" || f["file"] != "JAR" {
		t.Errorf("copy deploy form: %v", f)
	}
	jsonEq(t, r.last("PATCH "+hy+"/applications/60").JSON(t), "desiredStatus", "STOPPED")
	jsonEq(t, r.last("PATCH "+hy+"/targets/2/components/21").JSON(t), "enabled", true)
	jsonEq(t, r.last("PATCH "+hy+"/targets/2/components/22").JSON(t), "enabled", true)
	jsonEq(t, r.last("PATCH "+hy+"/targets/2/components/23").JSON(t), "configuration.applicationLogLevels.0.scopeLoggingConfigurations",
		[]any{map[string]any{"logLevel": "WARN", "scope": "x"}})

	// a failure after the deploy removes the half-copied app
	r.on("GET "+hy+"/targets/2/components", reply{500, map[string]any{"message": "boom"}})
	r.on("DELETE "+hy+"/applications/60", reply{204, nil})
	polls = 0
	if _, err := r.run("runtime-mgr:standalone-application:copy", rootOrg+":Production/orders", rootOrg+":Sandbox/orders-copy", "grp"); err == nil {
		t.Fatal("copy must fail")
	}
	if !r.sent("DELETE " + hy + "/applications/60") {
		t.Error("a failed copy must delete the target app")
	}

	if _, err := r.run("runtime-mgr:standalone-application:copy", "missing", rootOrg+":Sandbox/x", "grp"); err == nil {
		t.Error("an unknown source must fail")
	}
}

func TestHybridAlerts(t *testing.T) {
	r := newRecorder(t)
	r.on("GET /accounts/api/organizations/"+rootOrg+"/members", map[string]any{"data": []any{map[string]any{"id": "u1", "username": "ops"}}})
	r.on("POST "+hy+"/alerts", reply{201, data(map[string]any{"id": 5})})
	r.mustRun("runtime-mgr:standalone-alert:create", "down", "--severity", "CRITICAL", "--resourceType", "server",
		"--condition", "server-down", "--resource", "srv1", "--email", "a@b.c", "--recipient", "ops", "--subject", "S")
	b := r.last("POST " + hy + "/alerts").JSON(t)
	jsonEq(t, b, "condition", map[string]any{"resourceType": "server", "type": "server-down", "resources": []any{"srv1"}})
	jsonEq(t, b, "actions", []any{map[string]any{"type": "email", "subject": "S", "emails": []any{"a@b.c"}, "userIds": []any{"u1"}}})
	if _, err := r.run("runtime-mgr:standalone-alert:create", "x", "--condition", "server-explodes"); err == nil {
		t.Error("an unknown condition must fail")
	}

	r.on("GET "+hy+"/alerts/5", data(map[string]any{"id": 5, "name": "down", "enabled": true, "severity": "INFO", "organizationId": "o",
		"condition": map[string]any{"type": "server-down", "resourceType": "server", "resources": []any{"a", "b"}},
		"actions":   []any{map[string]any{"type": "email", "emails": []any{"x@y"}, "subject": "old"}}}))
	r.on("PATCH "+hy+"/alerts/5", data(map[string]any{}))
	r.mustRun("runtime-mgr:standalone-alert:modify", "5", "--severity", "WARNING", "--content", "C")
	b = r.last("PATCH " + hy + "/alerts/5").JSON(t)
	jsonEq(t, b, "severity", "WARNING")
	jsonEq(t, b, "name", "down")
	jsonEq(t, b, "condition.resources", []any{"*", "b"}) // lodash merge: arrays merge by index, as in v4
	jsonEq(t, b, "actions.0", map[string]any{"type": "email", "emails": []any{"x@y"}, "subject": "old", "content": "C"})
	if m := b.(map[string]any); m["id"] != nil || m["organizationId"] != nil || m["enabled"] != nil {
		t.Errorf("system keys must be dropped: %v", m)
	}

	if out := r.mustRun("runtime-mgr:standalone-alert:describe", "5"); !strings.Contains(out, "x@y") {
		t.Errorf("describe:\n%s", out)
	}
	r.on("GET "+hy+"/alerts", data([]any{map[string]any{"id": 5, "name": "down", "condition": map[string]any{"resources": []any{"*"}}}}))
	r.mustRun("runtime-mgr:standalone-alert:list")
	if q := r.last("GET " + hy + "/alerts").Query; !strings.Contains(q, "sort=createdAt") {
		t.Errorf("alert list query = %s", q)
	}
}

func TestManagedGateways(t *testing.T) {
	r := newRecorder(t)
	gwAPI := "/gatewaymanager/api/v1/organizations/" + rootOrg + "/environments/" + prodEnv + "/gateways"
	gwX := "/gatewaymanager/xapi/v1/organizations/" + rootOrg + "/environments/" + prodEnv + "/gateways"
	r.on("GET /gatewaymanager/xapi/v1/gateway/versions", map[string]any{"channels": map[string]any{
		"lts":  map[string]any{"versions": []any{map[string]any{"displayName": "1.9.2"}, map[string]any{"displayName": "1.10.0"}, map[string]any{"displayName": "1.9.10"}}},
		"edge": map[string]any{"versions": []any{map[string]any{"displayName": "1.11.0"}}}}})
	r.on("GET "+gwX+"/targets", map[string]any{"content": []any{map[string]any{"id": "space-1", "name": "Space"}}})
	gw := map[string]any{"id": "g1", "name": "gw", "targetId": "space-1", "status": "RUNNING", "releaseChannel": "lts",
		"runtimeVersion": "1.9.2", "size": "small", "configuration": map[string]any{
			"ingress":    map[string]any{"publicUrl": "https://a, https://b", "lastMileSecurity": true},
			"properties": map[string]any{"upstreamResponseTimeout": 15, "connectionIdleTimeout": 60},
			"logging":    map[string]any{"level": "info", "forwardLogs": true},
			"tracing":    map[string]any{"enabled": true, "sampling": 5, "labels": []any{"x"}}}}
	r.on("POST "+gwAPI, reply{201, gw})

	out := r.mustRun("runtime-mgr:gateways:managed:create", "gw", "space-1", "small", "--releaseChannel", "lts", "--tracingEnabled", "--tracingLabels", `[{"k":"v"}]`)
	if !strings.Contains(out, "https://a, https://b") {
		t.Errorf("create output:\n%s", out)
	}
	b := r.last("POST " + gwAPI).JSON(t)
	jsonEq(t, b, "runtimeVersion", "1.10.0") // latest of the channel, by semantic version
	jsonEq(t, b, "configuration.properties", map[string]any{"upstreamResponseTimeout": 15, "connectionIdleTimeout": 60})
	jsonEq(t, b, "configuration.logging", map[string]any{"level": "info", "forwardLogs": true})
	jsonEq(t, b, "configuration.tracing", map[string]any{"enabled": true, "sampling": 1, "labels": []any{map[string]any{"k": "v"}}})
	jsonEq(t, b, "configuration.ingress", map[string]any{"publicUrl": "", "lastMileSecurity": false, "forwardSslSession": false})

	t.Setenv("LOGGING_LEVEL", "debug")
	r.mustRun("runtime-mgr:gateways:managed:create", "gw", "space-1", "small", "--releaseChannel", "edge", "--version", "1.11.0", "--no-forwardLogs")
	b = r.last("POST " + gwAPI).JSON(t)
	jsonEq(t, b, "configuration.logging", map[string]any{"level": "debug", "forwardLogs": false})
	jsonEq(t, b, "configuration.tracing", map[string]any{"enabled": false})
	os.Unsetenv("LOGGING_LEVEL")
	for _, bad := range [][]string{
		{"gw", "space-1", "small"}, // releaseChannel
		{"gw", "space-9", "small", "--releaseChannel", "lts"},
		{"gw", "space-1", "small", "--releaseChannel", "lts", "--version", "9.9.9"},
		{"gw", "space-1", "small", "--releaseChannel", "lts", "--loggingLevel", "chatty"},
	} {
		if _, err := r.run(append([]string{"runtime-mgr:gateways:managed:create"}, bad...)...); err == nil {
			t.Errorf("create %v must fail", bad)
		}
	}

	r.on("GET "+gwX+"/g1", gw)
	r.on("PUT "+gwAPI+"/g1", gw)
	r.mustRun("runtime-mgr:gateways:managed:edit", "g1", "--upstreamResponseTimeout", "30", "--releaseChannel", "edge")
	b = r.last("PUT " + gwAPI + "/g1").JSON(t)
	jsonEq(t, b, "configuration.properties.upstreamResponseTimeout", 30)
	jsonEq(t, b, "configuration.ingress.publicUrl", "https://a, https://b") // kept
	jsonEq(t, b, "configuration.ingress.lastMileSecurity", true)            // kept
	jsonEq(t, b, "runtimeVersion", "1.11.0")                                // new channel's latest
	jsonEq(t, b, "configuration.tracing", map[string]any{"enabled": true, "sampling": 5, "labels": []any{"x"}})
	if _, err := r.run("runtime-mgr:gateways:managed:edit", "g1"); err == nil {
		t.Error("edit without flags must fail")
	}
	if _, err := r.run("runtime-mgr:gateways:managed:edit", "g1", "--no-tracingEnabled", "--tracingSampling", "3"); err == nil {
		t.Error("sampling with tracing off must fail")
	}

	r.on("PATCH "+gwAPI+"/g1/desiredstatus", reply{200, nil})
	if out := r.mustRun("runtime-mgr:gateways:managed:start", "g1"); !strings.Contains(out, "currently running") || r.sent("PATCH "+gwAPI+"/g1/desiredstatus") {
		t.Errorf("a running gateway cannot be started: %s", out)
	}
	r.mustRun("runtime-mgr:gateways:managed:stop", "g1")
	jsonEq(t, r.last("PATCH "+gwAPI+"/g1/desiredstatus").JSON(t), "desiredStatus", "STOPPED")

	r.on("DELETE "+gwAPI+"/g1", reply{409, map[string]any{"message": "Gateway has associated APIs"}})
	r.on("GET "+gwX+"/g1/apis", map[string]any{"content": []any{map[string]any{"apiName": "orders", "instanceId": 101}}})
	if out := r.mustRun("runtime-mgr:gateways:managed:delete", "g1"); !strings.Contains(out, "orders") {
		t.Errorf("delete with APIs:\n%s", out)
	}
	r.on("DELETE "+gwAPI+"/g1", reply{204, nil})
	if out := r.mustRun("runtime-mgr:gateways:managed:delete", "g1"); !strings.Contains(out, "being deleted") {
		t.Errorf("delete: %s", out)
	}
	r.on("GET "+gwX, map[string]any{"content": []any{gw}})
	r.mustRun("runtime-mgr:gateways:managed:list")
	if q := r.last("GET " + gwX).Query; q != "kind=managed" {
		t.Errorf("list query = %q", q)
	}
}
