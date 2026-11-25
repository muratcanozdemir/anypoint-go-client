package main

import (
	"archive/zip"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	vpcs = "/cloudhub/api/organizations/" + rootOrg + "/vpcs"
	vpc1 = vpcs + "/vpc-1"
	lbs1 = vpc1 + "/loadbalancers"
)

func cloudhubVPCs(r *recorder) {
	r.on("GET "+vpcs, map[string]any{"data": []any{map[string]any{
		"id": "vpc-1", "name": "main", "region": "us-east-1", "cidrBlock": "10.0.0.0/22", "ownerId": rootOrg, "isDefault": true,
		"associatedEnvironments": []any{sbxEnv}, "sharedWith": []any{subOrg}, "hash": "h1", "vpcRoutes": []any{"keep"},
		"internalDns":   map[string]any{"dnsServers": []any{"10.0.0.2"}, "specialDomains": []any{"corp"}},
		"firewallRules": []any{map[string]any{"cidrBlock": "0.0.0.0/0", "protocol": "tcp", "fromPort": 8081, "toPort": 8082}},
	}}})
}

func TestVPCs(t *testing.T) {
	r := newRecorder(t)
	cloudhubVPCs(r)
	r.on("GET "+lbs1, map[string]any{"data": []any{map[string]any{"id": "lb-1", "name": "edge"}}})

	out := r.mustRun("cloudhub:vpc:list", "--limit", "5")
	if !strings.Contains(out, "Sandbox") || !strings.Contains(out, "Team BG") || !strings.Contains(out, "No") {
		t.Errorf("vpc list:\n%s", out)
	}
	if q := r.last("GET " + vpcs).Query; !strings.Contains(q, "limit=5") || !strings.Contains(q, "sort=id") {
		t.Errorf("vpc list query = %s", q)
	}
	out = r.mustRun("cloudhub:vpc:describe", "main")
	for _, want := range []string{"edge", "corp", "10.0.0.2", "Sandbox"} {
		if !strings.Contains(out, want) {
			t.Errorf("vpc describe lacks %q:\n%s", want, out)
		}
	}
	if _, err := r.run("cloudhub:vpc:describe", "nope"); err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Errorf("unknown vpc: %v", err)
	}

	r.on("POST "+vpcs, map[string]any{"id": "vpc-2", "name": "new"})
	r.on("GET "+vpcs+"/vpc-2/loadbalancers", map[string]any{"data": []any{}})
	r.mustRun("cloudhub:vpc:create", "new", "us-west-2", "10.1.0.0/24", "Sandbox", "Production", "--default")
	b := r.last("POST " + vpcs).JSON(t)
	jsonEq(t, b, "associatedEnvironments", []any{sbxEnv, prodEnv})
	jsonEq(t, b, "isDefault", true)
	jsonEq(t, b, "cidrBlock", "10.1.0.0/24")
	if _, err := r.run("cloudhub:vpc:create", "x", "r", "c", "Staging"); err == nil {
		t.Error("an unknown environment must fail")
	}

	r.on("PUT "+vpc1, map[string]any{})
	r.mustRun("cloudhub:vpc:dns-servers:set", "main", "--server", "1.1.1.1, 8.8.8.8", "--domain", "a.com,b.com")
	jsonEq(t, r.last("PUT "+vpc1).JSON(t), "internalDns", map[string]any{"dnsServers": []any{"1.1.1.1", "8.8.8.8"}, "specialDomains": []any{"a.com", "b.com"}})
	r.mustRun("cloudhub:vpc:dns-servers:unset", "main")
	jsonEq(t, r.last("PUT "+vpc1).JSON(t), "internalDns", map[string]any{"dnsServers": []any{}, "specialDomains": []any{}})

	r.mustRun("cloudhub:vpc:environments:add", "main", "Production", "Sandbox", "--default")
	b = r.last("PUT " + vpc1).JSON(t)
	jsonEq(t, b, "associatedEnvironments", []any{sbxEnv, prodEnv})
	jsonEq(t, b, "isDefault", true)
	r.mustRun("cloudhub:vpc:environments:remove", "main", "Sandbox")
	if b := r.last("PUT " + vpc1).JSON(t).(map[string]any); len(b) != 1 || len(b["associatedEnvironments"].([]any)) != 0 {
		t.Errorf("environments remove body = %v", b)
	}
	r.mustRun("cloudhub:vpc:business-groups:remove", "main", "Team BG")
	jsonEq(t, r.last("PUT "+vpc1).JSON(t), "sharedWith", []any{})
	r.mustRun("cloudhub:vpc:business-groups:add", "main", rootOrg)
	jsonEq(t, r.last("PUT "+vpc1).JSON(t), "sharedWith", []any{subOrg, rootOrg})

	r.mustRun("cloudhub:vpc:firewall-rules:add", "main", "10.2.0.0/16", "udp", "53", "53")
	b = r.last("PUT " + vpc1).JSON(t)
	jsonEq(t, b, "firewallRules.1", map[string]any{"cidrBlock": "10.2.0.0/16", "protocol": "udp", "fromPort": 53, "toPort": 53})
	jsonEq(t, b, "vpcRoutes", []any{"keep"}) // the rest of the document goes back unchanged
	if b.(map[string]any)["hash"] != nil {
		t.Error("the hash must not be sent back")
	}
	r.mustRun("cloudhub:vpc:firewall-rules:remove", "main", "0")
	jsonEq(t, r.last("PUT "+vpc1).JSON(t), "firewallRules", []any{})
	if _, err := r.run("cloudhub:vpc:firewall-rules:remove", "main", "3"); err == nil {
		t.Error("an out-of-range index must fail")
	}
	if out := r.mustRun("cloudhub:vpc:firewall-rules:describe", "main"); !strings.Contains(out, "TCP") || !strings.Contains(out, "8082") {
		t.Errorf("firewall describe:\n%s", out)
	}

	r.on("DELETE "+vpc1, reply{204, nil})
	if out := r.mustRun("cloudhub:vpc:delete", "main", "-f"); !strings.Contains(out, "VPC main deleted successfully") {
		t.Errorf("delete: %s", out)
	}
}

func TestLoadBalancers(t *testing.T) {
	r := newRecorder(t)
	cloudhubVPCs(r)
	lb := map[string]any{"id": "lb-1", "name": "edge", "domain": "edge.lb.anypointdns.net", "state": "STARTED",
		"ipAddresses": []any{"1.2.3.4"}, "ipAllowlist": []any{"0.0.0.0/0", "10.0.0.0/8"}, "defaultSslEndpoint": 1,
		"sslEndpoints": []any{
			map[string]any{"publicKeyCN": "a.example.com", "mappings": []any{}},
			map[string]any{"publicKeyCN": "b.example.com", "publicKeySANs": []any{"c.example.com"},
				"mappings": []any{map[string]any{"inputUri": "/{app}/", "appName": "{app}", "appUri": "/", "upstreamProtocol": "http"}}},
			map[string]any{"publicKeyCN": "d.example.com", "mappings": []any{}},
		}}
	r.on("GET "+lbs1, map[string]any{"data": []any{lb}})
	r.on("PATCH "+lbs1+"/lb-1", lb)
	patch := func() []any { return r.last("PATCH " + lbs1 + "/lb-1").JSON(t).([]any) }

	out := r.mustRun("cloudhub:load-balancer:list")
	if !strings.Contains(out, "b.example.com") || !strings.Contains(out, "main") {
		t.Errorf("lb list:\n%s", out)
	}
	out = r.mustRun("cloudhub:load-balancer:describe", "edge")
	if !strings.Contains(out, "b.example.com (c.example.com)") || !strings.Contains(out, "10.0.0.0/8") {
		t.Errorf("lb describe:\n%s", out)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(r.mustRun("cloudhub:load-balancer:describe", "edge", "-o", "json")), &raw); err != nil || raw["domain"] == nil {
		t.Errorf("lb describe -o json: %v %v", raw, err)
	}

	r.mustRun("cloudhub:load-balancer:allowlist:add", "edge", "192.168.0.0/16")
	jsonEq(t, patch(), "0", map[string]any{"op": "add", "path": "/ipAllowlist/-", "value": "192.168.0.0/16"})
	r.mustRun("cloudhub:load-balancer:whitelist:remove", "edge", "10.0.0.0/8")
	jsonEq(t, patch(), "0", map[string]any{"op": "remove", "path": "/ipAllowlist/1"})
	if _, err := r.run("cloudhub:load-balancer:allowlist:remove", "edge", "1.1.1.1/32"); err == nil {
		t.Error("removing an absent rule must fail")
	}
	r.mustRun("cloudhub:load-balancer:dynamic-ips:enable", "edge")
	jsonEq(t, patch(), "0", map[string]any{"op": "replace", "path": "/staticIPsDisabled", "value": true})
	r.mustRun("cloudhub:load-balancer:stop", "edge")
	jsonEq(t, patch(), "0.value", "STOPPED")

	r.mustRun("cloudhub:load-balancer:mappings:add", "edge", "/api/", "orders", "/", "d.example.com", "--upstreamProtocol", "https")
	jsonEq(t, patch(), "0", map[string]any{"op": "add", "path": "/sslEndpoints/2/mappings/0",
		"value": map[string]any{"inputUri": "/api/", "appName": "orders", "appUri": "/", "upstreamProtocol": "https"}})
	r.mustRun("cloudhub:load-balancer:mappings:remove", "edge", "0")
	jsonEq(t, patch(), "0.path", "/sslEndpoints/1/mappings/0") // default certificate
	if out := r.mustRun("cloudhub:load-balancer:mappings:describe", "edge"); !strings.Contains(out, "{app}") {
		t.Errorf("mappings describe:\n%s", out)
	}
	if _, err := r.run("cloudhub:load-balancer:mappings:describe", "edge", "zzz"); err == nil {
		t.Error("an unknown certificate must fail")
	}

	r.mustRun("cloudhub:load-balancer:ssl-endpoint:setDefault", "edge", "a.example.com", "--http", "off")
	if p := patch(); len(p) != 2 {
		t.Errorf("setDefault ops = %v", p)
	} else {
		jsonEq(t, p, "0.value", 0)
		jsonEq(t, p, "1", map[string]any{"op": "replace", "path": "/httpMode", "value": "off"})
	}
	r.mustRun("cloudhub:load-balancer:ssl-endpoint:remove", "edge", "a.example.com")
	jsonEq(t, patch(), "1", map[string]any{"op": "replace", "path": "/defaultSslEndpoint", "value": 0})
	if _, err := r.run("cloudhub:load-balancer:ssl-endpoint:remove", "edge", "b.example.com"); err == nil {
		t.Error("removing the default certificate must fail")
	}
	if out := r.mustRun("cloudhub:load-balancer:ssl-endpoint:describe", "edge", "b.example.com"); !strings.Contains(out, "c.example.com") {
		t.Errorf("ssl describe:\n%s", out)
	}

	dir := t.TempDir()
	for _, f := range []string{"cert.pem", "key.pem", "ca.pem"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(f+" data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r.mustRun("cloudhub:load-balancer:ssl-endpoint:add", "edge", filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"),
		"--clientCertificate", filepath.Join(dir, "ca.pem"))
	v := patch()
	jsonEq(t, v, "0.path", "/sslEndpoints/-")
	jsonEq(t, v, "0.value.publicKey", "cert.pem data")
	jsonEq(t, v, "0.value.clientCert", "ca.pem data")
	jsonEq(t, v, "0.value.verifyClientMode", "optional")

	r.on("POST "+lbs1, map[string]any{"name": "edge2", "defaultSslEndpoint": 0, "sslEndpoints": []any{map[string]any{"publicKeyCN": "x.example.com"}}})
	out = r.mustRun("cloudhub:load-balancer:create", "edge2", "main", filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"), "--tlsv1", "--dynamic-ips")
	if !strings.Contains(out, "Created Load balancer edge2 with Common Name x.example.com") {
		t.Errorf("create: %s", out)
	}
	b := r.last("POST " + lbs1).JSON(t)
	jsonEq(t, b, "httpMode", "redirect")
	jsonEq(t, b, "tlsv1", true)
	jsonEq(t, b, "staticIPsDisabled", true)
	jsonEq(t, b, "sslEndpoints.0.mappings.0.appName", "{app}")
	if _, err := r.run("cloudhub:load-balancer:create", "edge2", "main"); err == nil {
		t.Error("create without certificates must fail")
	}

	r.on("DELETE "+lbs1+"/lb-1", reply{204, nil})
	r.mustRun("cloudhub:load-balancer:delete", "edge")
	if _, err := r.run("cloudhub:load-balancer:delete", "nope"); err == nil {
		t.Error("an unknown load balancer must fail")
	}
}

func TestRegionsRuntimesAlerts(t *testing.T) {
	r := newRecorder(t)
	r.on("GET /cloudhub/api/regions", []any{map[string]any{"id": "us-east-1", "name": "US East (N. Virginia)"}})
	if out := r.mustRun("cloudhub:region:list"); !strings.Contains(out, "N. Virginia") {
		t.Errorf("regions:\n%s", out)
	}
	r.on("GET /cloudhub/api/mule-versions", map[string]any{"data": []any{
		map[string]any{"version": "4.6.0", "state": "ACTIVE", "releaseChannel": "LTS", "javaVersion": "17"},
		map[string]any{"version": "3.9.0", "state": "DEFAULT"},
		map[string]any{"version": "3.8.0", "state": "DEPRECATED"},
	}})
	out := r.mustRun("cloudhub:runtime:list")
	if !strings.Contains(out, "LTS") || !strings.Contains(out, "NONE") || strings.Contains(out, "3.8.0") {
		t.Errorf("runtimes:\n%s", out)
	}
	if h := r.last("GET /cloudhub/api/mule-versions").Header.Get("X-ANYPNT-ENV-ID"); h != prodEnv {
		t.Errorf("env header = %q", h)
	}

	r.on("GET /cloudhub/api/v2/alerts", map[string]any{"data": []any{map[string]any{"id": "al-1", "name": "cpu", "enabled": true,
		"condition": map[string]any{"type": "cpu", "resources": []any{"orders"}}}}})
	if out := r.mustRun("runtime-mgr:cloudhub-alert:list"); !strings.Contains(out, "orders") {
		t.Errorf("alerts:\n%s", out)
	}
	r.on("GET /cloudhub/api/v2/alerts/al-1/history", map[string]any{"total": 1, "data": []any{
		map[string]any{"triggeredAt": 1700000000000, "context": map[string]any{"cpu": 99}}}})
	if out := r.mustRun("runtime-mgr:cloudhub-alert-history:describe", "cpu"); !strings.Contains(out, `{"cpu":99}`) || !strings.Contains(out, "2023") {
		t.Errorf("history:\n%s", out)
	}
	if q := r.last("GET /cloudhub/api/v2/alerts").Query; !strings.Contains(q, "limit=1000") {
		t.Errorf("history looks the alert up in %s", q)
	}
	if _, err := r.run("runtime-mgr:cloudhub-alert-history:describe", "mem"); err == nil {
		t.Error("an unknown alert must fail")
	}
}

const ch1 = "/cloudhub/api/v2/applications"

func ch1App(name, status string) map[string]any {
	return map[string]any{"domain": name, "fullDomain": name + ".cloudhub.io", "status": status, "fileName": name + ".jar",
		"lastUpdateTime": 1700000000000, "muleVersion": map[string]any{"version": "4.6.0"},
		"workers":    map[string]any{"amount": 1, "type": map[string]any{"name": "Micro", "cpu": "0.1 vCores"}},
		"properties": map[string]any{"a": "1"}, "versionId": "v", "ipAddresses": []any{map[string]any{"regionId": "eu-west-1"}},
		"staticIPsEnabled": true, "cloudObjectStoreRegion": "eu-west-1"}
}

func TestCloudHubApplications(t *testing.T) {
	r := newRecorder(t)
	r.on("GET "+ch1, []any{ch1App("orders", "STARTED")})
	if out := r.mustRun("runtime-mgr:cloudhub-application:list"); !strings.Contains(out, "0.1 vCores * 1") {
		t.Errorf("list:\n%s", out)
	}
	r.on("GET "+ch1+"/orders", ch1App("orders", "STARTED"))
	if out := r.mustRun("runtime-mgr:cloudhub-application:describe", "orders"); !strings.Contains(out, "a = 1") || !strings.Contains(out, "NONE") {
		t.Errorf("describe:\n%s", out)
	}

	zipPath := filepath.Join(t.TempDir(), "orders.jar")
	if err := os.WriteFile(zipPath, []byte("JAR"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.on("POST "+ch1, ch1App("orders", "DEPLOYING"))
	r.mustRun("runtime-mgr:cloudhub-application:deploy", "orders", zipPath, "--workers", "2", "--workerSize", "0.2",
		"--runtime", "4.6.0", "--property", `k\:x:v`, "--no-persistentQueues", "--objectStoreV2")
	c := r.last("POST " + ch1)
	f := multipartFields(t, c)
	if f["file"] != "JAR" || f["autoStart"] != "true" || c.Header.Get("x-from-service") != "anypoint-cli" {
		t.Errorf("deploy form: %v", f)
	}
	var info map[string]any
	if err := json.Unmarshal([]byte(f["appInfoJson"]), &info); err != nil {
		t.Fatal(err)
	}
	jsonEq(t, info, "domain", "orders")
	jsonEq(t, info, "workers", map[string]any{"amount": 2, "type": map[string]any{"name": "Small"}})
	jsonEq(t, info, "properties", map[string]any{"k:x": "v"})
	jsonEq(t, info, "persistentQueues", false)
	jsonEq(t, info, "objectStoreV1", false)
	jsonEq(t, info, "monitoringAutoRestart", true)
	for _, bad := range [][]string{{"--workers", "9"}, {"--workerSize", "3"}, {"--timeout", "1000"}, {"--objectStoreV1", "--objectStoreV2"}} {
		if _, err := r.run(append([]string{"runtime-mgr:cloudhub-application:deploy", "orders", zipPath}, bad...)...); err == nil {
			t.Errorf("deploy %v must fail", bad)
		}
	}

	r.on("PUT "+ch1+"/orders", ch1App("orders", "STARTED"))
	r.mustRun("runtime-mgr:cloudhub-application:modify", "orders", "--property", "b=2", "--no-autoRestart")
	f = multipartFields(t, r.last("PUT "+ch1+"/orders"))
	if _, ok := f["file"]; ok {
		t.Error("modify without ZIPFILE must not send a file")
	}
	if err := json.Unmarshal([]byte(f["appInfoJson"]), &info); err != nil {
		t.Fatal(err)
	}
	jsonEq(t, info, "properties", map[string]any{"a": "1", "b": "2"})
	jsonEq(t, info, "monitoringEnabled", false)

	if _, err := r.run("runtime-mgr:cloudhub-application:revert-runtime", "orders"); err == nil {
		t.Error("revert without a previous runtime must fail")
	}
	prev := ch1App("orders", "STARTED")
	prev["previousMuleVersion"] = map[string]any{"version": "4.5.0"}
	r.on("GET "+ch1+"/orders", prev)
	r.mustRun("runtime-mgr:cloudhub-application:revert-runtime", "orders")
	f = multipartFields(t, r.last("PUT "+ch1+"/orders"))
	if f["appInfoJson"] != `{"muleVersion":{"version":"4.5.0"}}` || f["autoStart"] != "" {
		t.Errorf("revert form: %v", f)
	}

	status := "/cloudhub/api/applications/orders/status"
	r.on("POST "+status, reply{200, nil})
	r.mustRun("runtime-mgr:cloudhub-application:restart", "orders")
	jsonEq(t, r.last("POST "+status).JSON(t), "status", "restart")
	r.on("POST "+status, reply{304, nil})
	if out := r.mustRun("runtime-mgr:cloudhub-application:stop", "orders"); !strings.Contains(out, "already in this state") {
		t.Errorf("stop when stopped: %s", out)
	}
	r.on("DELETE /cloudhub/api/applications/orders", reply{204, nil})
	r.mustRun("runtime-mgr:cloudhub-application:delete", "orders")

	// logs
	r.on("GET "+ch1+"/orders/deployments", map[string]any{"data": []any{map[string]any{"deploymentId": "d1", "createTime": 1700000000000,
		"instances": []any{map[string]any{"instanceId": "i-1"}, map[string]any{"instanceId": "i-2"}}}}})
	r.on("GET "+ch1+"/orders/instances/i-1/log-file", "line one\n")
	r.on("GET "+ch1+"/orders/instances/i-2/log-file", "line two\n")
	dir := t.TempDir()
	r.mustRun("runtime-mgr:cloudhub-application:download-logs", "orders", dir)
	got := readZip(t, filepath.Join(dir, "orders-2023_11_14.zip"))
	if got["i-1.txt"] != "line one\n" || got["i-2.txt"] != "line two\n" {
		t.Errorf("log zip = %v", got)
	}
	if q := r.last("GET " + ch1 + "/orders/deployments").Query; !strings.Contains(q, "orderByDate=DESC") {
		t.Errorf("deployments query = %s", q)
	}

	old := tailPolls
	tailPolls = 2
	tailInterval = time.Millisecond
	t.Cleanup(func() { tailPolls, tailInterval = old, 500*time.Millisecond })
	r.on("GET "+ch1+"/orders/deployments/d1/logs", map[string]any{"data": []any{
		map[string]any{"timestamp": 1, "message": "first"}, map[string]any{"timestamp": 2, "message": "second"}}})
	out := r.mustRun("runtime-mgr:cloudhub-application:tail-logs", "orders")
	if strings.Count(out, "first") != 1 || strings.Count(out, "second") != 1 {
		t.Errorf("tail must print each line once:\n%s", out)
	}
}

func readZip(t *testing.T, path string) map[string]string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

func TestCloudHubApplicationCopy(t *testing.T) {
	r := newRecorder(t)
	byEnv := func(env string, resp any) func(*http.Request) reply {
		return func(req *http.Request) reply {
			if req.Header.Get("X-ANYPNT-ENV-ID") == env {
				return reply{200, resp}
			}
			return reply{404, map[string]any{"message": "not found"}}
		}
	}
	r.on("GET "+ch1+"/orders", byEnv(prodEnv, ch1App("orders", "STARTED")))
	r.on("GET "+ch1+"/orders-copy", byEnv("none", nil))
	r.on("GET /cloudhub/api/applications/orders/download/orders.jar", "JAR")
	r.on("POST "+ch1, ch1App("orders-copy", "UNDEPLOYED"))
	r.on("POST "+ch1+"/orders-copy/staticips", reply{200, nil})
	r.on("POST /cloudhub/api/applications/orders-copy/status", reply{200, nil})

	r.mustRun("runtime-mgr:cloudhub-application:copy", "orders", rootOrg+":Sandbox/orders-copy", "--property", "a=9")
	dep := r.last("POST " + ch1)
	if dep.Header.Get("X-ANYPNT-ENV-ID") != sbxEnv {
		t.Errorf("copy must deploy to the target env, got %q", dep.Header.Get("X-ANYPNT-ENV-ID"))
	}
	f := multipartFields(t, dep)
	var info map[string]any
	if err := json.Unmarshal([]byte(f["appInfoJson"]), &info); err != nil {
		t.Fatal(err)
	}
	jsonEq(t, info, "domain", "orders-copy")
	jsonEq(t, info, "properties", map[string]any{"a": "9"})
	jsonEq(t, info, "objectStoreV1", false)
	for _, k := range []string{"versionId", "status", "ipAddresses", "fullDomain"} {
		if _, ok := info[k]; ok {
			t.Errorf("system field %s must be dropped", k)
		}
	}
	if f["file"] != "JAR" || f["autoStart"] != "" {
		t.Errorf("copy form: %v", f)
	}
	jsonEq(t, r.last("POST "+ch1+"/orders-copy/staticips").JSON(t), "region", "eu-west-1")
	jsonEq(t, r.last("POST /cloudhub/api/applications/orders-copy/status").JSON(t), "status", "start")

	// the target exists already
	r.on("GET "+ch1+"/orders-copy", ch1App("orders-copy", "STARTED"))
	if _, err := r.run("runtime-mgr:cloudhub-application:copy", "orders", "orders-copy"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("copy onto an existing app: %v", err)
	}

	// a failure after the deploy removes the copy
	r.on("GET "+ch1+"/orders-copy", byEnv("none", nil))
	r.on("POST "+ch1+"/orders-copy/staticips", reply{500, map[string]any{"message": "no IPs left"}})
	r.on("DELETE /cloudhub/api/applications/orders-copy", reply{204, nil})
	if _, err := r.run("runtime-mgr:cloudhub-application:copy", "orders", "orders-copy"); err == nil {
		t.Fatal("copy must fail")
	}
	if !r.sent("DELETE /cloudhub/api/applications/orders-copy") {
		t.Error("a failed copy must delete the target")
	}

	// copy-replace keeps the target's settings and takes the source's archive
	target := ch1App("orders-copy", "STARTED")
	target["properties"] = map[string]any{"t": "1"}
	r.on("GET "+ch1+"/orders-copy", target)
	r.on("PUT "+ch1+"/orders-copy", target)
	r.mustRun("runtime-mgr:cloudhub-application:copy-replace", "orders", "orders-copy")
	put := r.last("PUT " + ch1 + "/orders-copy")
	if put.Header.Get("Target-Info") != "target-info" {
		t.Error("copy-replace must send target-info")
	}
	f = multipartFields(t, put)
	if err := json.Unmarshal([]byte(f["appInfoJson"]), &info); err != nil {
		t.Fatal(err)
	}
	jsonEq(t, info, "fileName", "orders.jar")
	jsonEq(t, info, "properties", map[string]any{"t": "1"})
	if f["file"] != "JAR" {
		t.Errorf("copy-replace form: %v", f)
	}
}

const ch2 = "/amc/application-manager/api/v2/organizations/" + rootOrg + "/environments/" + prodEnv + "/deployments"

func ch2Deployment() map[string]any {
	return map[string]any{"id": "app-1", "name": "orders", "status": "APPLIED", "lastModifiedDate": 1700000000000,
		"desiredVersion": "v2", "lastSuccessfulVersion": "v1",
		"replicas": []any{map[string]any{"currentDeploymentVersion": "v1"}, map[string]any{"currentDeploymentVersion": "v2", "reason": "OOM"}},
		"application": map[string]any{"vCores": 0.1, "ref": map[string]any{"artifactId": "orders"},
			"configuration": map[string]any{"mule.agent.application.properties.service": map[string]any{
				"properties": map[string]any{"p": "1"}, "secureProperties": map[string]any{"s": "****"}}}},
		"target": map[string]any{"targetId": "space-1", "replicas": 2, "deploymentSettings": map[string]any{
			"runtimeVersion": "4.6.0:2e-java17", "updateStrategy": "rolling",
			"http": map[string]any{"inbound": map[string]any{"publicUrl": "https://orders.example.com"}}}}}
}

func TestCloudHub2Applications(t *testing.T) {
	r := newRecorder(t)
	r.on("GET "+ch2, map[string]any{"items": []any{map[string]any{"id": "a", "name": "first"}, map[string]any{"id": "b", "name": "second"}}})
	out := r.mustRun("runtime-mgr:application:list")
	if strings.Index(out, "second") > strings.Index(out, "first") {
		t.Errorf("list is newest first:\n%s", out)
	}
	r.on("GET "+ch2+"/app-1", ch2Deployment())
	out = r.mustRun("runtime-mgr:application:describe", "app-1")
	for _, want := range []string{"v2 (DESIRED),\nv1 (LAST SUCCESSFUL)", "OOM", "p : 1", "https://orders.example.com", "0.1"} {
		if !strings.Contains(out, want) {
			t.Errorf("describe lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "2e-java17") {
		t.Errorf("describe shows the runtime without its tag:\n%s", out)
	}

	org := "/accounts/api/organizations/" + rootOrg
	r.on("GET "+org, map[string]any{"id": rootOrg, "entitlements": map[string]any{}})
	r.on("GET /runtimefabric/api/organizations/"+rootOrg+"/targets/space-1", map[string]any{"id": "space-1", "type": "shared-space"})
	r.on("GET /exchange/api/v1/assets/"+rootOrg+"/orders", map[string]any{"version": "1.2.3"})
	r.on("POST "+ch2, ch2Deployment())
	logCfg := filepath.Join(t.TempDir(), "log.txt")
	if err := os.WriteFile(logCfg, []byte("{scope: org.mule, logLevel: debug}\n\n[scope=com.acme, logLevel=WARN]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.mustRun("runtime-mgr:application:deploy", "orders", "space-1", "4.6.0:2e-java17", "orders", "--replicas", "2", "--clustered",
		"--property", "p:1", "--secureProperty", "s:2", "--scopeLoggingConfigFile", logCfg, "--scopeLoggingConfig", "x:error",
		"--publicEndpoints", "https://orders.cloudhub.io", "--lastMileSecurity")
	b := r.last("POST " + ch2).JSON(t)
	jsonEq(t, b, "application.ref", map[string]any{"artifactId": "orders", "groupId": rootOrg, "packaging": "jar", "version": "1.2.3"})
	jsonEq(t, b, "application.vCores", "0.1")
	conf := b.(map[string]any)["application"].(map[string]any)["configuration"]
	jsonEq(t, conf.(map[string]any)["mule.agent.application.properties.service"], "",
		map[string]any{"applicationName": "orders", "properties": map[string]any{"p": "1"}, "secureProperties": map[string]any{"s": "2"}})
	jsonEq(t, conf.(map[string]any)["mule.agent.logging.service"], "scopeLoggingConfigurations", []any{
		map[string]any{"scope": "org.mule", "logLevel": "DEBUG"}, map[string]any{"scope": "com.acme", "logLevel": "WARN"},
		map[string]any{"scope": "x", "logLevel": "ERROR"}})
	jsonEq(t, b, "application.integrations.services.objectStoreV2.enabled", nil)
	jsonEq(t, b, "target.replicas", 2)
	jsonEq(t, b, "target.deploymentSettings.clustered", true)
	jsonEq(t, b, "target.deploymentSettings.generateDefaultPublicUrl", true)
	jsonEq(t, b, "target.deploymentSettings.runtime", map[string]any{"version": "4.6.0:2e-java17"})
	jsonEq(t, b, "target.deploymentSettings.http.inbound", map[string]any{"publicUrl": "https://orders.cloudhub.io", "pathRewrite": nil,
		"lastMileSecurity": true, "forwardSslSession": false})

	// usage-based pricing: an instance type instead of vCores
	r.on("GET "+org, map[string]any{"entitlements": map[string]any{"usageBasedPricing": map[string]any{"muleRuntimeIntegration": map[string]any{"enabled": true}}}})
	r.mustRun("runtime-mgr:application:deploy", "orders", "space-1", "4.6.0", "orders", "--assetVersion", "1.0.0")
	b = r.last("POST " + ch2).JSON(t)
	jsonEq(t, b, "target.deploymentSettings.instanceType", "mule.micro")
	if app := b.(map[string]any)["application"].(map[string]any); app["vCores"] != nil {
		t.Error("UBP deploys send no vCores")
	}
	for _, bad := range [][]string{{"--clustered"}, {"--replicas", "x"}, {"--scopeLoggingConfig", "x:LOUD"}} {
		if _, err := r.run(append([]string{"runtime-mgr:application:deploy", "orders", "space-1", "4.6.0", "orders"}, bad...)...); err == nil {
			t.Errorf("deploy %v must fail", bad)
		}
	}

	r.on("PATCH "+ch2+"/app-1", ch2Deployment())
	r.mustRun("runtime-mgr:application:modify", "app-1", "--replicas", "3", "--runtimeVersion", "4.7.0", "--no-forwardSslSession", "--assetVersion", "2.0.0")
	b = r.last("PATCH " + ch2 + "/app-1").JSON(t)
	jsonEq(t, b, "target.replicas", 3)
	jsonEq(t, b, "target.deploymentSettings.runtimeVersion", "4.7.0")
	jsonEq(t, b, "target.deploymentSettings.instanceType", "mule.micro") // converted from 0.1 vCores under UBP
	jsonEq(t, b, "target.deploymentSettings.http.inbound", map[string]any{"forwardSslSession": false})
	jsonEq(t, b, "application.ref", map[string]any{"artifactId": "orders", "groupId": rootOrg, "packaging": "jar", "version": "2.0.0"})
	if _, err := r.run("runtime-mgr:application:modify", "app-1", "--replicaSize", "1"); err == nil {
		t.Error("vCores cannot change under UBP")
	}

	r.mustRun("runtime-mgr:application:stop", "app-1")
	jsonEq(t, r.last("PATCH "+ch2+"/app-1").JSON(t), "application.desiredState", "STOPPED")
	r.on("PATCH "+ch2+"/app-1", reply{304, nil})
	if _, err := r.run("runtime-mgr:application:start", "app-1"); err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("start when started: %v", err)
	}
	r.on("DELETE "+ch2+"/app-1", reply{204, nil})
	r.mustRun("runtime-mgr:application:delete", "app-1")

	r.on("GET "+ch2+"/app-1/specs/v2/logs/file", "LOGS")
	dir := t.TempDir()
	r.mustRun("runtime-mgr:application:download-logs", "app-1", "v2", dir)
	got := readZip(t, filepath.Join(dir, "app-1-"+time.Now().UTC().Format("2006_01_02")+".zip"))
	if got["app-1.txt"] != "LOGS" {
		t.Errorf("logs zip = %v", got)
	}

	old := tailPolls
	tailPolls, tailInterval = 2, time.Millisecond
	t.Cleanup(func() { tailPolls, tailInterval = old, 500*time.Millisecond })
	r.on("GET "+ch2+"/app-1/specs/v2/logs", []any{map[string]any{"docId": "d9", "timestamp": 5, "logLevel": "INFO", "message": "hello",
		"context": map[string]any{"logger": "app"}}})
	out = r.mustRun("runtime-mgr:application:logs", "app-1", "v2")
	if strings.Count(out, "INFO - [") != 1 || !strings.Contains(out, "app - hello") {
		t.Errorf("logs:\n%s", out)
	}
	if q := r.last("GET " + ch2 + "/app-1/specs/v2/logs").Query; q != "afterDocId=d9&descending=false" {
		t.Errorf("second poll query = %q", q)
	}
}
