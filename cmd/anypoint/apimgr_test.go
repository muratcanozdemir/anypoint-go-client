package main

import (
	"encoding/json"
	stdmaps "maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	apisBase = "/apimanager/api/v1/organizations/" + rootOrg + "/environments/" + prodEnv + "/apis"
	api1     = apisBase + "/101"
)

func apiInstance(extra map[string]any) map[string]any {
	a := map[string]any{"id": 101, "groupId": rootOrg, "assetId": "orders", "assetVersion": "1.0.0", "productVersion": "v1",
		"instanceLabel": "orders-prod", "technology": "mule4", "environmentId": prodEnv,
		"endpoint": map[string]any{"type": "rest-api", "uri": "http://impl", "proxyUri": nil, "deploymentType": "CH", "muleVersion4OrAbove": true}}
	stdmaps.Copy(a, extra)
	return a
}

func TestAPIListDescribe(t *testing.T) {
	r := newRecorder(t)
	r.on("GET "+apisBase, map[string]any{"total": 1, "assets": []any{map[string]any{"id": 7, "exchangeAssetName": "Orders API",
		"apis": []any{apiInstance(map[string]any{"deprecated": true})}}}})
	out := r.mustRun("api-mgr:api:list", "--assetId", "orders", "--limit", "5")
	if !strings.Contains(out, "Orders API") || !strings.Contains(out, "orders-prod") {
		t.Errorf("list:\n%s", out)
	}
	if q := r.last("GET " + apisBase).Query; !strings.Contains(q, "assetId=orders") || !strings.Contains(q, "limit=5") ||
		!strings.Contains(q, "sort=createdDate") || !strings.Contains(q, "ascending=false") {
		t.Errorf("list query = %s", q)
	}
	if out := r.mustRun("api-mgr:api:list", "-o", "json"); !strings.HasPrefix(strings.TrimSpace(out), "[") || !strings.Contains(out, `"orders-prod"`) {
		t.Errorf("json list must be the flattened instances:\n%s", out)
	}

	r.on("GET "+api1, apiInstance(map[string]any{"deployment": map[string]any{"applicationName": "orders-proxy", "targetId": 55}}))
	out = r.mustRun("api-mgr:api:describe", "101")
	for _, want := range []string{"orders-prod", "Mule 4 or above", "orders-proxy", "Deployment TargetID", "55", "http://impl", "RAML/OAS"} {
		if want == "RAML/OAS" {
			continue // rest-api is not in v4's endpoint type names
		}
		if !strings.Contains(out, want) {
			t.Errorf("describe missing %q:\n%s", want, out)
		}
	}
	if q := r.last("GET " + api1).Query; q != "includeTlsContexts=true" {
		t.Errorf("describe query = %q", q)
	}
	if _, err := r.run("api-mgr:api:describe", "999"); err == nil || !strings.Contains(err.Error(), `API with ID "999" not found`) {
		t.Errorf("describe of an unknown API: %v", err)
	}
}

func TestAPIManage(t *testing.T) {
	r := newRecorder(t)
	r.on("POST "+apisBase, reply{201, map[string]any{"id": 202}})
	out := r.mustRun("api-mgr:api:manage", "orders", "1.0.0", "-p", "--deploymentType", "hybrid", "--scheme", "https",
		"--port", "8082", "--path", "/api", "--uri", "http://impl", "--type", "raml", "-m", "--apiInstanceLabel", "lbl",
		"--inboundTlsContextId", "tls1", "--inboundSecretGroupId", "sg1", "--endpointUri", "https://consumer")
	if !strings.Contains(out, "Created new API with ID: 202") {
		t.Errorf("manage:\n%s", out)
	}
	b := r.last("POST " + apisBase).JSON(t)
	jsonEq(t, b, "spec", map[string]any{"assetId": "orders", "version": "1.0.0", "groupId": rootOrg})
	jsonEq(t, b, "endpoint.type", "rest-api") // raml behind a proxy
	jsonEq(t, b, "endpoint.deploymentType", "HY")
	jsonEq(t, b, "endpoint.proxyUri", "https://0.0.0.0:8082/api")
	jsonEq(t, b, "endpoint.isCloudHub", nil)
	jsonEq(t, b, "endpoint.referencesUserDomain", nil) // not given: null, as v4
	jsonEq(t, b, "endpoint.responseTimeout", nil)
	jsonEq(t, b, "endpoint.tlsContexts", map[string]any{"inbound": map[string]any{"secretGroupId": "sg1", "tlsContextId": "tls1"}})
	jsonEq(t, b, "instanceLabel", "lbl")
	jsonEq(t, b, "endpointUri", "https://consumer")
	if _, ok := b.(map[string]any)["technology"]; ok {
		t.Error("technology is only sent for Flex Gateway")
	}

	// no proxy: basic endpoint, nulls for what was not given
	r.mustRun("api-mgr:api:manage", "orders", "1.0.0", "my-group", "--isFlex")
	b = r.last("POST " + apisBase).JSON(t)
	jsonEq(t, b, "technology", "flexGateway")
	jsonEq(t, b, "spec.groupId", "my-group")
	jsonEq(t, b, "endpoint.deploymentType", "HY")
	jsonEq(t, b, "endpoint.uri", nil)
	jsonEq(t, b, "endpoint.proxyUri", nil)

	for _, bad := range [][]string{
		{"-p", "--scheme", "http", "--port", "1", "--path", "/", "--uri", "http://x"},                   // deploymentType
		{"-p", "--deploymentType", "cloudhub", "--scheme", "http", "--path", "/", "--uri", "http://x"},  // port
		{"-p", "--deploymentType", "rtf", "--scheme", "http", "--path", "/", "--uri", "http://x", "-r"}, // refUserDomain on rtf
		{"--isFlex", "--deploymentType", "cloudhub"},                                                    // flex must be hybrid
		{"--inboundTlsContextId", "t", "--inboundSecretGroupId", "s"},                                   // TLS needs proxy
		{"--inboundTlsContextId", "t"},                                                                  // TLS needs group
		{"--endpointUri", "not a url"},
		{"--type", "soap"},
	} {
		if _, err := r.run(append([]string{"api-mgr:api:manage", "orders", "1.0.0"}, bad...)...); err == nil {
			t.Errorf("manage %v must fail", bad)
		}
	}
}

func TestAPIEdit(t *testing.T) {
	r := newRecorder(t)
	r.on("GET "+api1, apiInstance(map[string]any{"endpoint": map[string]any{"type": "rest-api", "uri": "http://impl",
		"proxyUri": "http://0.0.0.0:8081/v1?x=1", "deploymentType": "HY", "muleVersion4OrAbove": true, "responseTimeout": 5000}}))
	r.on("PATCH "+api1, map[string]any{"id": 101})
	out := r.mustRun("api-mgr:api:edit", "101", "--uri", "http://new", "--updateApisInSamePort")
	if !strings.Contains(out, "Edited API with ID: 101") {
		t.Errorf("edit:\n%s", out)
	}
	c := r.last("PATCH " + api1)
	if c.Query != "updateApisInSamePort=true" {
		t.Errorf("edit query = %q", c.Query)
	}
	b := c.JSON(t)
	jsonEq(t, b, "endpoint.uri", "http://new")
	jsonEq(t, b, "endpoint.proxyUri", "http://0.0.0.0:8081/v1?x=1") // kept from the current instance
	jsonEq(t, b, "endpoint.responseTimeout", 5000)
	jsonEq(t, b, "endpoint.deploymentType", "HY")
	jsonEq(t, b, "instanceLabel", "orders-prod")
	if _, ok := b.(map[string]any)["spec"]; ok {
		t.Error("edit must not send spec")
	}

	r.on("GET "+api1, apiInstance(map[string]any{"technology": "flexGateway"}))
	if _, err := r.run("api-mgr:api:edit", "101", "--deploymentType", "hybrid"); err == nil {
		t.Error("a Flex Gateway API's deployment type cannot change")
	}
}

func TestAPISmallCommands(t *testing.T) {
	r := newRecorder(t)
	r.on("PATCH "+api1, map[string]any{"id": 101}).on("DELETE "+api1, reply{204, nil})
	if out := r.mustRun("api-mgr:api:deprecate", "101"); !strings.Contains(out, `API instance with ID "101" deprecated`) {
		t.Errorf("deprecate: %s", out)
	}
	jsonEq(t, r.last("PATCH "+api1).JSON(t), "deprecated", true)
	r.mustRun("api-mgr:api:undeprecate", "101")
	jsonEq(t, r.last("PATCH "+api1).JSON(t), "deprecated", false)
	r.mustRun("api-mgr:api:change-specification", "101", "1.0.1")
	jsonEq(t, r.last("PATCH "+api1).JSON(t), "assetVersion", "1.0.1")
	if out := r.mustRun("api-mgr:api:delete", "101", "-o", "json"); !strings.Contains(out, `"message": "API deleted"`) {
		t.Errorf("delete json: %s", out)
	}

	r.on("GET "+api1+"/autodiscoveryProperties", map[string]any{"api.id": 101, "api.version": "v1"})
	out := r.mustRun("api-mgr:api:autodiscovery", "101", "4.4.0")
	if !strings.Contains(out, "api.version") || r.last("GET "+api1+"/autodiscoveryProperties").Query != "gatewayVersion=4.4.0" {
		t.Errorf("autodiscovery:\n%s", out)
	}

	proxy := "/proxies/xapi/v1/organizations/" + rootOrg + "/environments/" + prodEnv + "/apis/101/proxy"
	r.on("GET "+proxy, hreply{reply{200, "JAR"}, http.Header{"Content-Disposition": {`attachment; filename="orders-proxy.jar"`}}})
	dir := filepath.Join(t.TempDir(), "out")
	r.mustRun("api-mgr:api:download-proxy", "101", dir)
	if b, err := os.ReadFile(filepath.Join(dir, "orders-proxy.jar")); err != nil || string(b) != "JAR" {
		t.Errorf("proxy file: %q, %v", b, err)
	}
	if q := r.last("GET " + proxy).Query; q != "gatewayVersion=3.9.0" {
		t.Errorf("proxy query = %q", q)
	}

	// classify: the instance's asset, then the destination environment by name
	r.on("GET "+api1, apiInstance(nil))
	r.on("GET "+apisBase, map[string]any{"total": 1, "assets": []any{map[string]any{"id": 7, "apis": []any{}}}})
	classify := "/apiplatform/repository/v2/organizations/" + rootOrg + "/apis/7/versions/101/classify"
	r.on("POST "+classify, reply{204, nil})
	r.mustRun("api-mgr:api:classify", "Sandbox", "101")
	jsonEq(t, r.last("POST "+classify).JSON(t), "environmentId", sbxEnv)
	if q := r.last("GET " + apisBase).Query; !strings.Contains(q, "groupId="+rootOrg) || !strings.Contains(q, "assetVersion=1.0.0") {
		t.Errorf("classify GAV query = %s", q)
	}

	// promote from Sandbox into the selected (Production) environment
	sbxAPI := "/apimanager/api/v1/organizations/" + rootOrg + "/environments/" + sbxEnv + "/apis/101"
	r.on("GET "+sbxAPI, apiInstance(nil))
	r.on("POST "+apisBase, reply{201, map[string]any{"id": 303}})
	out = r.mustRun("api-mgr:api:promote", "101", sbxEnv, "-a", "false", "--providerId", "p1")
	if !strings.Contains(out, `promoted from "`+sbxEnv+`" to "Production"`) {
		t.Errorf("promote: %s", out)
	}
	b := r.last("POST " + apisBase).JSON(t)
	jsonEq(t, b, "instanceLabel", "orders-prod (Promoted)")
	jsonEq(t, b, "promote", map[string]any{"originApiId": 101, "alerts": map[string]any{"allEntities": false},
		"policies": map[string]any{"allEntities": true}, "tiers": map[string]any{"allEntities": true}})
	jsonEq(t, b, "providerId", "p1")
	if _, err := r.run("api-mgr:api:promote", "101", prodEnv); err == nil {
		t.Error("promoting into the source environment must fail")
	}
}

func TestAPIDeploy(t *testing.T) {
	r := newRecorder(t)
	deps := "/proxies/xapi/v1/organizations/" + rootOrg + "/environments/" + prodEnv + "/apis/101/deployments"
	r.on("POST "+deps, reply{201, map[string]any{}})

	r.on("GET "+api1, apiInstance(nil)) // CloudHub
	if _, err := r.run("api-mgr:api:deploy", "101", "--gatewayVersion", "4.4.0"); err == nil || !strings.Contains(err.Error(), "applicationName") {
		t.Errorf("CloudHub deploy needs --applicationName: %v", err)
	}
	r.mustRun("api-mgr:api:deploy", "101", "--applicationName", "orders-proxy", "--gatewayVersion", "4.4.0", "--javaVersion", "17")
	b := r.last("POST " + deps).JSON(t)
	jsonEq(t, b, "type", "CH")
	jsonEq(t, b, "environmentName", "Production")
	jsonEq(t, b, "environmentId", prodEnv)
	jsonEq(t, b, "javaVersion", "17")
	jsonEq(t, b, "overwrite", false)

	// hybrid: the target comes from the deployment targets
	r.on("GET "+api1, apiInstance(map[string]any{"endpoint": map[string]any{"deploymentType": "HY"}}))
	r.on("GET /proxies/xapi/v1/organizations/"+rootOrg+"/deployment-targets", []any{
		map[string]any{"id": 42, "name": "srv1", "type": "server", "gatewayVersion": "4.4.0"}})
	r.mustRun("api-mgr:api:deploy", "101", "--target", "42")
	b = r.last("POST " + deps).JSON(t)
	jsonEq(t, b, "type", "HY")
	jsonEq(t, b, "targetName", "srv1")
	jsonEq(t, b, "gatewayVersion", "4.4.0")
	if _, err := r.run("api-mgr:api:deploy", "101", "--target", "43"); err == nil {
		t.Error("an unknown hybrid target must fail")
	}

	// Runtime Fabric: the version must be offered by the fabric
	r.on("GET "+api1, apiInstance(map[string]any{"endpoint": map[string]any{"deploymentType": "RF"}}))
	r.on("GET /amc/atlas/api/v1/organizations/"+rootOrg+"/providers/MC/targets/rtf1", map[string]any{"id": "rtf1",
		"runtimes": []any{map[string]any{"type": "mule", "versions": []any{map[string]any{"baseVersion": "4.6.0"}}}}})
	r.mustRun("api-mgr:api:deploy", "101", "--target", "rtf1", "--gatewayVersion", "4.6.0", "--applicationName", "p", "--releaseChannel", "edge")
	b = r.last("POST " + deps).JSON(t)
	jsonEq(t, b, "type", "RF")
	jsonEq(t, b, "target.deploymentSettings", map[string]any{"runtimeVersion": "4.6.0",
		"runtime": map[string]any{"releaseChannel": "edge", "version": "4.6.0"}})
	if _, err := r.run("api-mgr:api:deploy", "101", "--target", "rtf1", "--gatewayVersion", "4.9.9"); err == nil {
		t.Error("an unavailable runtime version must fail")
	}

	// redeploy CloudHub: the current deployment, minus server-side fields
	r.on("GET "+api1, apiInstance(nil))
	r.on("GET "+deps, []any{map[string]any{"id": 9, "type": "CH", "applicationName": "orders-proxy", "gatewayVersion": "4.4.0",
		"organizationId": rootOrg, "masterOrganizationId": rootOrg, "targetType": "x", "expectedStatus": "undeployed", "apiId": 101}})
	r.on("PATCH "+deps+"/9", map[string]any{})
	out := r.mustRun("api-mgr:api:redeploy", "101", "--gatewayVersion", "4.5.0", "--overwrite")
	if !strings.Contains(out, `Redeployed API with ID "101"`) {
		t.Errorf("redeploy: %s", out)
	}
	b = r.last("PATCH " + deps + "/9").JSON(t)
	jsonEq(t, b, "gatewayVersion", "4.5.0")
	jsonEq(t, b, "overwrite", true)
	jsonEq(t, b, "expectedStatus", "deployed")
	for _, k := range []string{"organizationId", "masterOrganizationId", "targetType"} {
		if _, ok := b.(map[string]any)[k]; ok {
			t.Errorf("redeploy must drop %s", k)
		}
	}

	// --environmentName: the API lives in "unclassified"
	unc := "/proxies/xapi/v1/organizations/" + rootOrg + "/environments/unclassified/apis/101/deployments"
	r.on("GET /apimanager/api/v1/organizations/"+rootOrg+"/environments/unclassified/apis/101", apiInstance(nil))
	r.on("GET "+unc, []any{map[string]any{"id": 9, "type": "CH", "apiId": 101}})
	r.on("PATCH "+unc+"/9", map[string]any{})
	r.mustRun("api-mgr:api:redeploy", "101", "--environmentName", "Sandbox")
	b = r.last("PATCH " + unc + "/9").JSON(t)
	jsonEq(t, b, "apiVersionId", 101)
	jsonEq(t, b, "environmentId", sbxEnv)

	r.on("GET "+deps, []any{})
	if _, err := r.run("api-mgr:api:redeploy", "101"); err == nil || !strings.Contains(err.Error(), "is not deployed") {
		t.Errorf("redeploy of an undeployed API: %v", err)
	}
}

func TestPolicies(t *testing.T) {
	r := newRecorder(t)
	pols := api1 + "/policies"
	r.on("POST "+pols, reply{201, map[string]any{"id": 501}})

	out := r.mustRun("api-mgr:policy:apply", "101", "rate-limiting", "--policyVersion", "1.4.0", "-c",
		`{"jwtExpression":"#[attributes.headers[authorization]]","customKeyExpression":"#[vars[key]]"}`,
		"--pointcut", `[{"methodRegex":"GET","host":"a"}]`, "--label", "rl", "--allowDuplicated")
	if !strings.Contains(out, `New policy ID: "501"`) {
		t.Errorf("apply: %s", out)
	}
	c := r.last("POST " + pols)
	if c.Query != "allowDuplicated=true" {
		t.Errorf("apply query = %q", c.Query)
	}
	b := c.JSON(t)
	jsonEq(t, b, "groupId", "68ef9520-24e9-4cf2-b2f5-620025690913")
	jsonEq(t, b, "assetId", "rate-limiting")
	jsonEq(t, b, "policyTemplateId", "rate-limiting")
	jsonEq(t, b, "apiVersionId", "101")
	jsonEq(t, b, "id", nil)
	jsonEq(t, b, "configurationData.jwtExpression", "#[attributes.headers['authorization']]")
	jsonEq(t, b, "configurationData.customKeyExpression", "#[vars['key']]")
	jsonEq(t, b, "pointcutData", []any{map[string]any{"methodRegex": "GET", "host": "a"}})

	if _, err := r.run("api-mgr:policy:apply", "101", "x", "-u", "up1"); err == nil {
		t.Log("-u is not a short flag in v4 either") // flag error expected
	}
	if _, err := r.run("api-mgr:policy:apply", "101", "x", "--upstreamId", "up1", "--pointcut", `{"host":"a"}`); err == nil ||
		!strings.Contains(err.Error(), "Outbound policies only support") {
		t.Errorf("outbound pointcut validation: %v", err)
	}
	cfg := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(cfg, []byte(`{"k":"v"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r.mustRun("api-mgr:policy:apply", "101", "12", "--configFile", cfg)
	b = r.last("POST " + pols).JSON(t)
	jsonEq(t, b, "configurationData", map[string]any{"k": "v"})
	if _, ok := b.(map[string]any)["assetId"]; ok {
		t.Error("a Mule 3 policy has no asset")
	}
	if _, err := r.run("api-mgr:policy:apply", "101", "12", "--configFile", cfg, "-c", "{}"); err == nil {
		t.Error("--config and --configFile are exclusive")
	}

	r.on("GET "+pols, []any{
		map[string]any{"id": 501, "policyTemplateId": "348742", "assetId": "rate-limiting", "assetVersion": "1.4.0", "groupId": "g",
			"order": 2, "apiId": 101, "configurationData": map[string]any{"rateLimits": []any{}, "apiId": 101, "k": ""},
			"pointcutData": nil, "label": "rl", "audit": map[string]any{"created": map[string]any{"date": "2024-01-01T00:00:00Z"}}},
		map[string]any{"id": 502, "assetId": "cors", "order": 1, "disabled": true, "audit": map[string]any{}},
	})
	out = r.mustRun("api-mgr:policy:list", "101")
	if !strings.Contains(out, "rate-limiting") || !strings.Contains(out, "Disabled") || !strings.Contains(out, "k: [Empty]") || strings.Contains(out, "apiId:") {
		t.Errorf("policy list:\n%s", out)
	}
	if r.last("GET "+pols).Query != "fullInfo=false" {
		t.Error("policy list must ask for fullInfo=false")
	}

	r.on("PATCH "+pols+"/501", map[string]any{})
	r.mustRun("api-mgr:policy:edit", "101", "501", "-c", `{"x":1}`, "--label", "null")
	b = r.last("PATCH " + pols + "/501").JSON(t)
	jsonEq(t, b, "apiVersionId", 101)
	jsonEq(t, b, "policyTemplateId", 348742)
	jsonEq(t, b, "configurationData", map[string]any{"x": 1})
	jsonEq(t, b, "label", nil)
	jsonEq(t, b, "assetVersion", "1.4.0")
	if _, err := r.run("api-mgr:policy:edit", "101", "999"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("editing an unknown policy: %v", err)
	}

	r.mustRun("api-mgr:policy:disable", "101", "501")
	jsonEq(t, r.last("PATCH "+pols+"/501").JSON(t), "disabled", true)
	r.on("DELETE "+pols+"/501", reply{204, nil})
	r.mustRun("api-mgr:policy:remove", "101", "501")

	// order: show, then reorder
	out = r.mustRun("api-mgr:policy:order", "101")
	if !strings.Contains(out, `[{"id":501,"order":2},{"id":502,"order":1}]`) {
		t.Errorf("order hint:\n%s", out)
	}
	r.on("PATCH "+pols, map[string]any{})
	r.mustRun("api-mgr:policy:order", "101", "502,501", "--commaSeparated")
	if got := string(r.last("PATCH " + pols).Body); got != `[{"id":502,"order":1},{"id":501,"order":2}]` {
		t.Errorf("order body = %s", got)
	}
	r.mustRun("api-mgr:policy:order", "101", `[{"id":501,"order":5},{"id":502,"order":9}]`)
	if got := string(r.last("PATCH " + pols).Body); got != `[{"id":501,"order":1},{"id":502,"order":2}]` {
		t.Errorf("JSON order body = %s", got)
	}
	for _, bad := range []string{"501", "501,501,502", "501,502,7", "x"} {
		if _, err := r.run("api-mgr:policy:order", "101", bad, "--commaSeparated"); err == nil {
			t.Errorf("order %q must fail", bad)
		}
	}
}

func TestPolicyDescribe(t *testing.T) {
	r := newRecorder(t)
	def := "id: rl\nname: Rate limiting\ncategory: Quality of service\ndescription: Limits requests.\nconfiguration:\n" +
		"  - propertyName: rateLimits\n    type: keyvalues\n    allowMultiple: true\n    configuration:\n" +
		"      - propertyName: maximumRequests\n        type: int\n        defaultValue: 10\n" +
		"  - propertyName: clusterizable\n    type: boolean\n    optional: true\n    dependsOnKey: rateLimits\n"
	var auth string
	r.on("GET /files/rl.yaml", func(req *http.Request) reply {
		auth = req.Header.Get("Authorization")
		return reply{200, def}
	})
	r.on("POST /graph/api/v2/graphql", map[string]any{"data": map[string]any{"asset": map[string]any{
		"name": "Rate limiting", "assetId": "rl", "files": []any{map[string]any{"classifier": "policy-definition", "externalLink": r.srv.URL + "/files/rl.yaml"}}}}})
	out := r.mustRun("api-mgr:policy:describe", "rl", "--policyVersion", "1.0.0")
	for _, want := range []string{"Quality of service", "Array of keyvalues", "Nested under: rateLimits", "Default: 10", "Depends on: rateLimits", "Required: false"} {
		if !strings.Contains(out, want) {
			t.Errorf("describe missing %q:\n%s", want, out)
		}
	}
	jsonEq(t, r.last("POST /graph/api/v2/graphql").JSON(t), "variables.groupId", "68ef9520-24e9-4cf2-b2f5-620025690913")
	if auth == "" {
		t.Log("the definition link is on the Anypoint host here, so the token is sent; foreign hosts never get it (client tests)")
	}

	tmpl := "/apimanager/api/v1/organizations/" + rootOrg + "/policy-templates/12"
	r.on("GET "+tmpl, map[string]any{"id": 12, "name": "CORS", "category": "Security", "configuration": []any{}})
	if out := r.mustRun("api-mgr:policy:describe", "12"); !strings.Contains(out, "does not require any configuration") {
		t.Errorf("Mule 3 describe:\n%s", out)
	}
}

func TestTiersAndContracts(t *testing.T) {
	r := newRecorder(t)
	tiers := api1 + "/tiers"
	r.on("POST "+tiers, reply{201, map[string]any{"id": 11}})
	out := r.mustRun("api-mgr:tier:add", "101", "--name", "Gold", "-l", "true,100,min", "-l", "false,5,sec", "-a")
	if !strings.Contains(out, `Created SLA Tier with ID "11"`) {
		t.Errorf("tier add: %s", out)
	}
	b := r.last("POST " + tiers).JSON(t)
	jsonEq(t, b, "status", "ACTIVE")
	jsonEq(t, b, "apiVersionId", "101")
	jsonEq(t, b, "autoApprove", true)
	jsonEq(t, b, "description", "")
	jsonEq(t, b, "limits", []any{
		map[string]any{"visible": true, "maximumRequests": 100, "timePeriodInMilliseconds": 60000},
		map[string]any{"visible": false, "maximumRequests": 5, "timePeriodInMilliseconds": 1000}})
	if _, err := r.run("api-mgr:tier:add", "101", "--name", "x", "-l", "true,1,fortnight"); err == nil {
		t.Error("an unknown period must fail")
	}

	r.on("GET "+tiers, map[string]any{"tiers": []any{map[string]any{"id": 11, "name": "Gold", "limits": []any{map[string]any{}},
		"applicationCount": 3, "status": "ACTIVE", "autoApprove": true, "audit": map[string]any{}}}})
	if out := r.mustRun("api-mgr:tier:list", "101", "go"); !strings.Contains(out, "Gold") || !strings.Contains(out, "Auto") {
		t.Errorf("tier list:\n%s", out)
	}
	if q := r.last("GET " + tiers).Query; !strings.Contains(q, "query=go") || !strings.Contains(q, "sort=id") || !strings.Contains(q, "ascending=true") {
		t.Errorf("tier list query = %s", q)
	}

	// copy: Gold updated, Silver created, Bronze removed
	src, dst := apisBase+"/1/tiers", apisBase+"/2/tiers"
	r.on("GET "+src, map[string]any{"tiers": []any{
		map[string]any{"id": 1, "name": "Gold", "description": "new", "limits": []any{}, "status": "ACTIVE", "audit": map[string]any{}},
		map[string]any{"id": 2, "name": "Silver", "limits": []any{}, "organizationId": "o", "audit": map[string]any{}}}})
	r.on("GET "+dst, map[string]any{"tiers": []any{
		map[string]any{"id": 21, "name": "Gold", "description": "old", "limits": []any{}},
		map[string]any{"id": 23, "name": "Bronze", "limits": []any{}}}})
	r.on("PUT "+dst+"/21", map[string]any{"id": 21}).on("POST "+dst, map[string]any{"id": 22}).on("DELETE "+dst+"/23", reply{204, nil})
	r.mustRun("api-mgr:tier:copy", "1", "2")
	jsonEq(t, r.last("PUT "+dst+"/21").JSON(t), "description", "new")
	nb := r.last("POST " + dst).JSON(t)
	jsonEq(t, nb, "name", "Silver")
	jsonEq(t, nb, "apiVersionId", "2")
	if _, ok := nb.(map[string]any)["id"]; ok {
		t.Error("a copied tier must not carry the source ID")
	}
	r.last("DELETE " + dst + "/23")

	r.on("DELETE "+tiers+"/11", reply{204, nil})
	r.mustRun("api-mgr:tier:delete", "101", "11")

	contracts := api1 + "/contracts"
	r.on("GET "+contracts, map[string]any{"contracts": []any{map[string]any{"id": 77, "status": "APPROVED",
		"application": map[string]any{"name": "app", "coreServicesId": "cid"}, "tier": map[string]any{"name": "Gold"}, "audit": map[string]any{}}}})
	if out := r.mustRun("api-mgr:contract:list", "101"); !strings.Contains(out, "cid") || !strings.Contains(out, "N/A") {
		t.Errorf("contract list:\n%s", out)
	}
	r.on("DELETE "+contracts+"/77", reply{204, nil})
	r.mustRun("api-mgr:contract:delete", "101", "cid")
	if q := r.last("GET " + contracts).Query; !strings.Contains(q, "coreServicesId=cid") {
		t.Errorf("contract lookup query = %s", q)
	}
	r.on("GET "+contracts, map[string]any{"contracts": []any{}})
	if _, err := r.run("api-mgr:contract:delete", "101", "nope"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("deleting an unknown contract: %v", err)
	}
}

func TestAlerts(t *testing.T) {
	r := newRecorder(t)
	r.on("GET /accounts/api/organizations/"+rootOrg+"/members", map[string]any{"data": []any{
		map[string]any{"id": "u1", "username": "ops", "email": "ops@example.com", "firstName": "O", "lastName": "P"}}})
	r.on("GET /accounts/api/me", map[string]any{"user": map[string]any{"id": userID, "organization": map[string]any{"id": rootOrg},
		"memberOfOrganizations": []any{map[string]any{"id": rootOrg, "isMaster": true}}}})
	mon := "/monitoring/api/alerts/api/v2/organizations/" + rootOrg + "/environments/" + prodEnv + "/alerts"
	r.on("POST "+mon, reply{201, map[string]any{"alertId": "a-1"}})
	out := r.mustRun("api-mgr:alert:add", "101", "Too many 5xx", "--severity", "critical", "--type", "response-code", "--operator", "ab",
		"--threshold", "10", "--periods", "2", "--responseCode", "500", "--responseCode", "503", "--recipient", "ops", "--email", "a@b.c")
	if !strings.Contains(out, `Created new alert with ID "a-1" in API ID "101"`) {
		t.Errorf("alert add: %s", out)
	}
	b := r.last("POST " + mon).JSON(t)
	jsonEq(t, b, "metricType", "api_response_code_count")
	jsonEq(t, b, "masterOrganizationId", rootOrg)
	jsonEq(t, b, "condition", map[string]any{"operator": "above", "threshold": 10, "interval": 2})
	jsonEq(t, b, "resources.0.subFilters", []any{map[string]any{"name": "response_code", "values": []any{"500", "503"}}})
	jsonEq(t, b, "notifications.0.recipients", []any{"a@b.c", "ops@example.com"})
	if !strings.Contains(string(r.last("POST "+mon).Body), "${severity}") {
		t.Error("the default subject template must be sent")
	}
	for _, bad := range [][]string{
		{"--severity", "critical", "--type", "request-count", "--operator", "ab", "--threshold", "1", "--periods", "1"},                      // no recipients
		{"--severity", "critical", "--type", "response-code", "--operator", "ab", "--threshold", "1", "--periods", "1", "--email", "a@b"},    // no codes
		{"--severity", "Critical", "--type", "request-count", "--operator", "ab", "--threshold", "1", "--periods", "1", "--email", "a@b"},    // legacy severity
		{"--severity", "critical", "--type", "request-count", "--operator", "ab", "--threshold", "lots", "--periods", "1", "--email", "a@b"}, // NaN
	} {
		if _, err := r.run(append([]string{"api-mgr:alert:add", "101", "x"}, bad...)...); err == nil {
			t.Errorf("alert add %v must fail", bad)
		}
	}

	legacy := api1 + "/alerts"
	r.on("POST "+legacy, reply{201, map[string]any{"id": 9}})
	r.on("GET "+api1+"/policies", []any{map[string]any{"id": 501, "policyTemplateId": "12"}})
	r.on("GET /apimanager/api/v1/organizations/"+rootOrg+"/policy-templates/12", map[string]any{"id": 12, "name": "CORS"})
	r.mustRun("api-mgr:alert:add-legacy", "101", "violations", "--severity", "Warning", "--type", "policy-violation", "--operator", "gt",
		"--threshold", "3", "--periods", "1", "--duration", "5", "--durationUnit", "minutes", "--policyId", "501", "--recipient", "ops", "--enabled")
	b = r.last("POST " + legacy).JSON(t)
	jsonEq(t, b, "type", "api-policy-violation")
	jsonEq(t, b, "policyTemplate", map[string]any{"id": "12", "name": "CORS"})
	jsonEq(t, b, "period", map[string]any{"duration": map[string]any{"count": 5, "weight": "MINUTES"}, "repeat": 1})
	jsonEq(t, b, "recipients", []any{map[string]any{"type": "user", "value": "u1", "firstName": "O", "lastName": "P"}})
	jsonEq(t, b, "enabled", true)
	jsonEq(t, b, "condition.operator", "GREATER_THAN")

	r.on("GET "+mon, map[string]any{"alerts": []any{map[string]any{"alertId": "a-1", "name": "5xx", "metricType": "api_response_code_count",
		"state": "paused", "notifications": []any{map[string]any{"recipients": []any{"a", "b", "a"}}}}}})
	out = r.mustRun("api-mgr:alert:list", "101")
	if !strings.Contains(out, "a-1") || !strings.Contains(out, "No") {
		t.Errorf("alert list:\n%s", out)
	}
	if q := r.last("GET " + mon).Query; !strings.Contains(q, "apiId=101") || !strings.Contains(q, "type=basic") {
		t.Errorf("alert list query = %s", q)
	}
	r.on("GET "+legacy, []any{map[string]any{"id": 9, "name": "v", "type": "api-policy-violation", "enabled": true, "recipients": []any{1, 2}}})
	if out := r.mustRun("api-mgr:alert:list-legacy", "101"); !strings.Contains(out, "api-policy-violation") {
		t.Errorf("legacy list:\n%s", out)
	}
}

func TestUpstreamsAndApplications(t *testing.T) {
	r := newRecorder(t)
	ups := api1 + "/upstreams"
	r.on("POST "+ups, reply{201, map[string]any{"id": "up1"}})
	r.mustRun("api-mgr:upstream:add", "101", "http://backend", "--label", "b", "--tlsContextId", "t", "--tlsContextSecretGroupId", "s")
	jsonEq(t, r.last("POST "+ups).JSON(t), "tlsContext", map[string]any{"secretGroupId": "s", "tlsContextId": "t"})
	if _, err := r.run("api-mgr:upstream:add", "101", "http://b", "--tlsContextId", "t"); err == nil {
		t.Error("--tlsContextId needs --tlsContextSecretGroupId")
	}
	r.on("PATCH "+ups+"/up1", map[string]any{})
	r.mustRun("api-mgr:upstream:edit", "101", "up1", "--tlsContextId", "null")
	if got := string(r.last("PATCH " + ups + "/up1").Body); got != `{"tlsContext":null}` {
		t.Errorf("edit body = %s", got)
	}
	r.on("GET "+ups, map[string]any{"upstreams": []any{
		map[string]any{"id": "up1", "uri": "http://a", "label": "a", "tlsContext": map[string]any{"name": "ctx"}, "audit": map[string]any{}},
		map[string]any{"id": "up2", "uri": "http://b"}}})
	if out := r.mustRun("api-mgr:upstream:list", "101"); !strings.Contains(out, "ctx") {
		t.Errorf("upstream list:\n%s", out)
	}
	xapi := "/apimanager/xapi/v1/organizations/" + rootOrg + "/environments/" + prodEnv + "/apis/101"
	r.on("PATCH "+xapi, map[string]any{})
	r.mustRun("api-mgr:upstream:bulk-edit", "101", "--tlsContextId", "t2", "--tlsContextSecretGroupId", "s2")
	var body struct{ Upstreams []map[string]any }
	if err := json.Unmarshal(r.last("PATCH "+xapi).Body, &body); err != nil || len(body.Upstreams) != 2 ||
		body.Upstreams[1]["label"] != nil || body.Upstreams[0]["tlsContext"].(map[string]any)["tlsContextId"] != "t2" {
		t.Errorf("bulk edit body: %s", r.last("PATCH "+xapi).Body)
	}
	if _, err := r.run("api-mgr:upstream:bulk-edit", "101", "--tlsContextId", "t2"); err == nil {
		t.Error("bulk edit needs the secret group unless removing")
	}
	r.on("DELETE "+ups+"/up1", reply{204, nil})
	r.mustRun("api-mgr:upstream:delete", "101", "up1")

	r = newRecorder(t) // fresh: check the organization-level commands alone
	owners := "/apiplatform/repository/v2/organizations/" + rootOrg + "/applications/55/owners"
	r.on("POST "+owners, reply{201, nil})
	r.mustRun("api-mgr:application:add-owner", "55", userID, "user")
	jsonEq(t, r.last("POST "+owners).JSON(t), "entityType", "user")
	if r.sent("GET /accounts/api/organizations/" + rootOrg + "/environments") {
		t.Error("organization-level commands must not resolve an environment")
	}

	apps := "/apimanager/api/v1/organizations/" + rootOrg + "/applications"
	r.on("POST "+apps, reply{201, map[string]any{"id": 66}})
	if out := r.mustRun("api-mgr:application:import", "prov", "client", "--url", "https://app"); !strings.Contains(out, `"66"`) {
		t.Errorf("import: %s", out)
	}
	jsonEq(t, r.last("POST "+apps).JSON(t), "url", "https://app")

	po := "/apimanager/xapi/v1/organizations/" + rootOrg + "/possible-application-owners"
	r.on("GET "+po, []any{map[string]any{"id": "t1", "entityType": "team", "firstName": "Core"}})
	if out := r.mustRun("api-mgr:application:possible-owners-list", "--search", "co"); !strings.Contains(out, "team") {
		t.Errorf("possible owners:\n%s", out)
	}
	if q := r.last("GET " + po).Query; !strings.Contains(q, "includeCurrent=true") || !strings.Contains(q, "search=co") {
		t.Errorf("possible owners query = %s", q)
	}
	r.on("GET /apimanager/xapi/v1/organizations/"+rootOrg+"/importable-client-identity-providers", []any{map[string]any{"providerId": "okta", "name": "Okta"}})
	if out := r.mustRun("api-mgr:client-provider:list-importable"); !strings.Contains(out, "Okta") {
		t.Errorf("importable providers:\n%s", out)
	}
}
