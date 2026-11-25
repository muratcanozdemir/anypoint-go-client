package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	pss = "/runtimefabric/api/organizations/" + rootOrg + "/privatespaces"
	ps1 = pss + "/ps-1"
)

func privateSpaces(r *recorder) {
	space := map[string]any{"id": "ps-1", "name": "prod-space", "region": "us-east-1", "status": "Active", "version": "1.2",
		"network":              map[string]any{"cidrBlock": "10.0.0.0/22", "inboundStaticIps": []any{"1.2.3.4"}, "dnsTarget": "x.cloudhub.io"},
		"managedFirewallRules": []any{map[string]any{"cidrBlock": "0.0.0.0/0", "protocol": "tcp", "fromPort": 443, "toPort": 443, "type": "inbound"}},
		"ingressConfiguration": map[string]any{"readResponseTimeout": "300", "protocol": "auto",
			"deployment": map[string]any{"status": "APPLIED"}, "logs": map[string]any{"portLogLevel": "ERROR"}},
		"enableIAMRole": true}
	r.on("GET "+pss, map[string]any{"content": []any{space}, "totalElements": 1})
	r.on("GET "+ps1, space)
	r.on("PATCH "+ps1, space)
	r.on("PUT "+ps1, space)
}

func TestPrivateSpaces(t *testing.T) {
	r := newRecorder(t)
	privateSpaces(r)

	if out := r.mustRun("cloudhub:private-space:list"); !strings.Contains(out, "ps-1") || !strings.Contains(out, "prod-space") || !strings.Contains(out, "10.0.0.0/22") {
		t.Errorf("list:\n%s", out)
	}
	if out := r.mustRun("cloudhub", "private-space", "describe", "prod-space"); !strings.Contains(out, "x.cloudhub.io") || !strings.Contains(out, "1.2.3.4") {
		t.Errorf("describe by name:\n%s", out)
	}
	if _, err := r.run("cloudhub:private-space:describe", "nope"); err == nil {
		t.Error("an unknown space must fail")
	}

	r.on("POST "+pss, map[string]any{"id": "ps-2", "name": "new"})
	r.mustRun("cloudhub:private-space:create", "new", "--region", "eu-west-1", "--enable-egress")
	b := r.last("POST " + pss).JSON(t)
	jsonEq(t, b, "name", "new")
	jsonEq(t, b, "region", "eu-west-1")
	jsonEq(t, b, "enableEgress", true)
	if _, ok := b.(map[string]any)["enableIAMRole"]; ok {
		t.Error("an unset flag must not be sent")
	}
	if _, err := r.run("cloudhub:private-space:create", "x"); err == nil {
		t.Error("create needs --region")
	}
	r.on("PATCH "+pss+"/ps-2", map[string]any{"id": "ps-2", "name": "new"})
	r.mustRun("cloudhub:private-space:create", "new", "--region", "eu-west-1", "--cidr-block", "10.2.0.0/22", "--reserved-cidr", "10.2.8.0/24")
	jsonEq(t, r.last("PATCH "+pss+"/ps-2").JSON(t), "network", map[string]any{"region": "eu-west-1", "cidrBlock": "10.2.0.0/22", "reservedCidrs": []any{"10.2.8.0/24"}})
	if _, err := r.run("cloudhub:private-space:create", "x", "--region", "r", "--reserved-cidr", "10.0.0.0/24"); err == nil {
		t.Error("--reserved-cidr needs --cidr-block")
	}

	r.mustRun("cloudhub:private-space:update", "ps-1", "--name", "renamed", "--no-enable-egress")
	b = r.last("PUT " + ps1).JSON(t)
	jsonEq(t, b, "name", "renamed")
	jsonEq(t, b, "enableEgress", false)
	if _, ok := b.(map[string]any)["enableIAMRole"]; ok {
		t.Error("update must send only what was given")
	}
	if _, err := r.run("cloudhub:private-space:update", "ps-1"); err == nil {
		t.Error("an empty update must fail")
	}

	r.mustRun("cloudhub:private-space:network", "ps-1", "--cidr-block", "10.1.0.0/22", "--reserved-cidr", "10.1.4.0/24")
	b = r.last("PATCH " + ps1).JSON(t)
	jsonEq(t, b, "network", map[string]any{"region": "us-east-1", "cidrBlock": "10.1.0.0/22", "reservedCidrs": []any{"10.1.4.0/24"}})

	r.mustRun("cloudhub:private-space:firewall-rules:add", "ps-1", "outbound", "TCP", "10.0.0.0/8", "8000", "8100")
	jsonEq(t, r.last("PATCH "+ps1).JSON(t), "managedFirewallRules", []any{
		map[string]any{"cidrBlock": "0.0.0.0/0", "protocol": "tcp", "fromPort": 443, "toPort": 443, "type": "inbound"},
		map[string]any{"cidrBlock": "10.0.0.0/8", "protocol": "tcp", "fromPort": 8000, "toPort": 8100, "type": "outbound"}})
	r.mustRun("cloudhub:private-space:firewall-rules:remove", "ps-1", "0")
	jsonEq(t, r.last("PATCH "+ps1).JSON(t), "managedFirewallRules", []any{})
	if _, err := r.run("cloudhub:private-space:firewall-rules:remove", "ps-1", "3"); err == nil {
		t.Error("an index out of range must fail")
	}
	if _, err := r.run("cloudhub:private-space:firewall-rules:add", "ps-1", "sideways", "tcp", "0.0.0.0/0", "1"); err == nil {
		t.Error("a bad rule type must fail")
	}
	if out := r.mustRun("cloudhub:private-space:firewall-rules:list", "ps-1"); !strings.Contains(out, "inbound") {
		t.Errorf("firewall list:\n%s", out)
	}

	r.mustRun("cloudhub:private-space:ingress", "ps-1", "--read-response-timeout", "600", "--log-filter", "10.0.0.1=DEBUG", "--no-enable-iam-role")
	b = r.last("PATCH " + ps1).JSON(t)
	jsonEq(t, b, "ingressConfiguration", map[string]any{"readResponseTimeout": "600", "protocol": "auto",
		"logs": map[string]any{"portLogLevel": "ERROR", "filters": []any{map[string]any{"ip": "10.0.0.1", "level": "DEBUG"}}}})
	jsonEq(t, b, "enableIAMRole", false)
	r.mustRun("cloudhub:private-space:ingress", "ps-1", "--reset")
	b = r.last("PATCH " + ps1).JSON(t)
	jsonEq(t, b, "ingressConfiguration", map[string]any{"readResponseTimeout": "300", "protocol": "https-redirect",
		"logs": map[string]any{"portLogLevel": "ERROR", "filters": []any{}}})
	jsonEq(t, b, "enableIAMRole", false)
	r.mustRun("cloudhub:private-space:ingress", "ps-1", "--port-log-level", "INFO")
	jsonEq(t, r.last("PATCH "+ps1).JSON(t), "ingressConfiguration.logs", map[string]any{"portLogLevel": "INFO", "filters": []any{}})

	r.on("DELETE "+ps1, reply{Status: http.StatusNoContent})
	if out := r.mustRun("cloudhub:private-space:delete", "prod-space"); !strings.Contains(out, "deleted") {
		t.Errorf("delete: %s", out)
	}
}

func TestPrivateSpaceAssociationsAndUpgrades(t *testing.T) {
	r := newRecorder(t)
	privateSpaces(r)
	r.on("GET "+ps1+"/associations", []any{map[string]any{"id": "as-1", "organizationId": rootOrg, "environmentId": sbxEnv}})
	if out := r.mustRun("cloudhub:private-space:association:list", "ps-1"); !strings.Contains(out, "as-1") {
		t.Errorf("associations:\n%s", out)
	}
	r.on("POST "+ps1+"/associations", []any{map[string]any{"id": "as-2"}, map[string]any{"id": "as-3"}})
	out := r.mustRun("cloudhub:private-space:association:create", "ps-1", "--associate", "all", "--associate", subOrg+":production")
	jsonEq(t, r.last("POST "+ps1+"/associations").JSON(t), "associations", []any{
		map[string]any{"organizationId": rootOrg, "environment": "all"}, map[string]any{"organizationId": subOrg, "environment": "production"}})
	if !strings.Contains(out, "as-3") {
		t.Errorf("create:\n%s", out)
	}
	r.on("DELETE "+ps1+"/associations/as-9", reply{Status: http.StatusNotFound})
	r.mustRun("cloudhub:private-space:association:delete", "ps-1", "as-9") // already gone is fine

	r.on("GET "+ps1+"/upgradestatus", reply{Status: http.StatusOK, Body: ""})
	if out := r.mustRun("cloudhub:private-space:upgrade:status", "ps-1"); !strings.Contains(out, "NO_UPGRADE_SCHEDULED") {
		t.Errorf("an empty upgrade status:\n%s", out)
	}
	r.on("PATCH "+ps1+"/upgrade", map[string]any{"status": "SCHEDULED", "scheduledUpdateTime": "2026-12-01T00:00:00Z"})
	out = r.mustRun("cloudhub:private-space:upgrade:schedule", "ps-1", "2026-12-01", "--opt-in")
	if q := r.last("PATCH " + ps1 + "/upgrade").Query; q != "date=2026-12-01&optIn=true" {
		t.Errorf("schedule query = %s", q)
	}
	if !strings.Contains(out, "SCHEDULED") {
		t.Errorf("schedule:\n%s", out)
	}
	if _, err := r.run("cloudhub:private-space:upgrade:schedule", "ps-1", "1 Dec"); err == nil {
		t.Error("a bad date must fail")
	}
	r.on("DELETE "+ps1+"/upgrade", reply{Status: http.StatusNotFound})
	r.mustRun("cloudhub:private-space:upgrade:cancel", "ps-1")
}

func TestTransitGateways(t *testing.T) {
	r := newRecorder(t)
	privateSpaces(r)
	tgws := ps1 + "/transitgateways"
	tgw := map[string]any{"id": "tgw-a", "name": "corp", "spec": map[string]any{"region": "us-east-1",
		"resourceShare": map[string]any{"id": "rs-1", "account": "123456789012"}},
		"status": map[string]any{"gateway": "available", "attachment": "attached", "routes": []any{"10.8.0.0/16"},
			"tgwResource": "https://console.aws.amazon.com/vpc/home?region=us-east-1#TransitGatewayDetails:transitGatewayId=tgw-0abc123"}}
	r.on("GET "+tgws, []any{tgw})
	out := r.mustRun("cloudhub:private-space:transit-gateway:describe", "ps-1", "corp")
	if !strings.Contains(out, "tgw-0abc123") || strings.Contains(out, "console.aws") {
		t.Errorf("describe shows the AWS transit gateway ID:\n%s", out)
	}

	r.on("POST "+tgws, reply{Status: http.StatusCreated})
	r.mustRun("cloudhub:private-space:transit-gateway:create", "ps-1", "corp", "--resource-share-id", "rs-1",
		"--resource-share-account", "123456789012", "--route", "10.8.0.0/16")
	b := r.last("POST " + tgws).JSON(t)
	jsonEq(t, b, "resourceShareId", "rs-1")
	jsonEq(t, b, "routes", []any{"10.8.0.0/16"})
	if _, err := r.run("cloudhub:private-space:transit-gateway:create", "ps-1", "x", "--resource-share-id", "a", "--resource-share-account", "b"); err == nil {
		t.Error("create needs a route")
	}

	org := "/runtimefabric/api/organizations/" + rootOrg + "/transitgateways/tgw-a"
	r.on("PATCH "+org, []any{tgw}) // the platform answers with the organization's list
	r.mustRun("cloudhub:private-space:transit-gateway:rename", "ps-1", "corp", "corp-2")
	jsonEq(t, r.last("PATCH "+org).JSON(t), "name", "corp-2")

	r.on("PATCH "+tgws+"/tgw-a", tgw)
	r.mustRun("cloudhub:private-space:transit-gateway:routes", "ps-1", "tgw-a")
	b = r.last("PATCH " + tgws + "/tgw-a").JSON(t)
	jsonEq(t, b, "routes", []any{})
	jsonEq(t, b, "name", "corp")

	// detached already: the private-space delete answers 400, the organization delete still runs
	r.on("DELETE "+tgws+"/tgw-a", reply{Status: http.StatusBadRequest, Body: map[string]any{"message": "Transit gateway: tgw-a is not attached to private space prod-space"}})
	r.on("DELETE "+org, reply{Status: http.StatusNoContent})
	r.mustRun("cloudhub:private-space:transit-gateway:delete", "ps-1", "corp")
	if countCalls(r, "DELETE "+org) != 1 {
		t.Error("delete must also de-register the transit gateway")
	}
	r.on("DELETE "+tgws+"/tgw-a", reply{Status: http.StatusBadRequest, Body: map[string]any{"message": "invalid id"}})
	if _, err := r.run("cloudhub:private-space:transit-gateway:delete", "ps-1", "corp"); err == nil {
		t.Error("another 400 must fail")
	}
}

func TestVPNConnections(t *testing.T) {
	r := newRecorder(t)
	privateSpaces(r)
	conns := ps1 + "/connections"
	conn := map[string]any{"id": "c-1", "name": "office", "vpns": []any{map[string]any{"vpnId": "v-1", "name": "a",
		"vpnConnectionStatus": "AVAILABLE", "remoteIpAddress": "203.0.113.1", "localAsn": 64512, "remoteAsn": 65000,
		"vpnTunnels": []any{map[string]any{"ptpCidr": "169.254.10.0/30"}, map[string]any{"ptpCidr": "169.254.11.0/30"}}}}}
	r.on("POST "+conns, conn)
	out := r.mustRun("cloudhub:private-space:vpn:create", "ps-1", "office", "--remote-ip", "203.0.113.1", "--local-asn", "64512",
		"--remote-asn", "65000", "--tunnel", "secret1,169.254.10.0/30", "--tunnel", "secret2,169.254.11.0/30,add")
	jsonEq(t, r.last("POST "+conns).JSON(t), "vpns", []any{map[string]any{"localAsn": "64512", "remoteAsn": "65000", "remoteIpAddress": "203.0.113.1",
		"vpnTunnels": []any{map[string]any{"psk": "secret1", "ptpCidr": "169.254.10.0/30", "startupAction": "start"},
			map[string]any{"psk": "secret2", "ptpCidr": "169.254.11.0/30", "startupAction": "add"}}}})
	if !strings.Contains(out, "v-1") || !strings.Contains(out, "AVAILABLE") || !strings.Contains(out, "64512") {
		t.Errorf("create:\n%s", out)
	}
	file := filepath.Join(t.TempDir(), "vpns.json")
	if err := os.WriteFile(file, []byte(`[{"remoteIpAddress":"198.51.100.1","localAsn":"64512","staticRoutes":["10.9.0.0/16"],"vpnTunnels":[]}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	r.mustRun("cloudhub:private-space:vpn:create", "ps-1", "office", "--file", file)
	jsonEq(t, r.last("POST "+conns).JSON(t), "vpns", []any{map[string]any{"localAsn": "64512", "remoteIpAddress": "198.51.100.1",
		"staticRoutes": []any{"10.9.0.0/16"}, "vpnTunnels": []any{}}})
	if _, err := r.run("cloudhub:private-space:vpn:create", "ps-1", "office", "--remote-ip", "1.1.1.1", "--local-asn", "1"); err == nil {
		t.Error("a VPN needs tunnels")
	}

	r.on("GET "+conns+"/c-1", conn)
	if out := r.mustRun("cloudhub:private-space:vpn:describe", "ps-1", "c-1"); !strings.Contains(out, "office") {
		t.Errorf("describe:\n%s", out)
	}
	r.on("DELETE "+conns+"/c-1/vpns/v-1", reply{Status: http.StatusNoContent})
	r.mustRun("cloudhub:private-space:vpn:delete", "ps-1", "c-1", "--vpn", "v-1")
	r.on("DELETE "+conns+"/c-1", reply{Status: http.StatusNoContent})
	old := vpnPollInterval
	vpnPollInterval = time.Millisecond
	t.Cleanup(func() { vpnPollInterval = old })
	polls := 0
	r.on("GET "+conns+"/c-1", func(*http.Request) reply { // deleting takes a while
		if polls++; polls < 3 {
			return reply{Status: http.StatusOK, Body: conn}
		}
		return reply{Status: http.StatusNotFound, Body: map[string]any{}}
	})
	r.mustRun("cloudhub:private-space:vpn:delete", "ps-1", "c-1")
	if polls != 3 {
		t.Errorf("delete must wait until the connection is gone (%d polls)", polls)
	}
	r.on("GET "+conns+"/c-1", conn)
	if _, err := r.run("cloudhub:private-space:vpn:delete", "ps-1", "c-1", "--wait", "20ms"); err == nil || !strings.Contains(err.Error(), "still being deleted") {
		t.Errorf("a connection that stays must time out: %v", err)
	}
	r.mustRun("cloudhub:private-space:vpn:delete", "ps-1", "c-1", "--wait", "0")
}

func TestPrivateSpaceTLSContexts(t *testing.T) {
	r := newRecorder(t)
	privateSpaces(r)
	tls := ps1 + "/tlsContexts"
	ctx := map[string]any{"id": "tls-1", "name": "api", "type": "PEM",
		"keyStore": map[string]any{"type": "PEM", "cn": "api.example.com", "expirationDate": "2027-01-01"},
		"ciphers":  map[string]any{"aes128GcmSha256": true, "aes256Sha256": false}}
	r.on("GET "+tls, []any{ctx})
	if out := r.mustRun("cloudhub:private-space:tls-context:list", "ps-1"); !strings.Contains(out, "api.example.com") {
		t.Errorf("list:\n%s", out)
	}

	dir := t.TempDir()
	cert, key := filepath.Join(dir, "api.crt"), filepath.Join(dir, "api.key")
	for p, text := range map[string]string{cert: "-----BEGIN CERTIFICATE-----\nX\n", key: "-----BEGIN PRIVATE KEY-----\nY\n"} {
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r.on("POST "+tls, reply{Status: http.StatusCreated})
	out := r.mustRun("cloudhub:private-space:tls-context:create", "ps-1", "api", "--certificate", cert, "--key", key, "--cipher", "tlsAes256GcmSha384")
	b := r.last("POST " + tls).JSON(t)
	jsonEq(t, b, "tlsConfig.keyStore", map[string]any{"source": "PEM", "certificate": "-----BEGIN CERTIFICATE-----\nX\n",
		"key": "-----BEGIN PRIVATE KEY-----\nY\n", "certificateFileName": "api.crt", "keyFileName": "api.key"})
	jsonEq(t, b, "ciphers", map[string]any{"tlsAes256GcmSha384": true})
	if !strings.Contains(out, "tls-1") || !strings.Contains(out, "aes128GcmSha256") {
		t.Errorf("create shows the created context:\n%s", out)
	}
	if _, err := r.run("cloudhub:private-space:tls-context:create", "ps-1", "x", "--certificate", cert); err == nil {
		t.Error("a PEM key store needs --key")
	}
	jks := filepath.Join(dir, "api.jks")
	if err := os.WriteFile(jks, []byte{0xfe, 0xed}, 0o600); err != nil {
		t.Fatal(err)
	}
	r.mustRun("cloudhub:private-space:tls-context:create", "ps-1", "api", "--keystore", jks, "--store-passphrase", "pw", "--alias", "k")
	jsonEq(t, r.last("POST "+tls).JSON(t), "tlsConfig.keyStore", map[string]any{"source": "JKS", "keystoreBase64": "/u0=",
		"storePassphrase": "pw", "alias": "k", "keystoreFileName": "api.jks"})

	r.on("PUT "+tls+"/tls-1", ctx)
	r.mustRun("cloudhub:private-space:tls-context:update", "ps-1", "api", "--disable-cipher", "aes128GcmSha256", "--cipher", "aes256Sha256")
	b = r.last("PUT " + tls + "/tls-1").JSON(t)
	jsonEq(t, b, "ciphers", map[string]any{"aes128GcmSha256": false, "aes256Sha256": true})
	if _, ok := b.(map[string]any)["tlsConfig"]; ok {
		t.Error("an update without a key store must keep the current one")
	}
	if _, err := r.run("cloudhub:private-space:tls-context:update", "ps-1", "api"); err == nil {
		t.Error("an empty update must fail")
	}
	r.on("DELETE "+tls+"/tls-1", reply{Status: http.StatusNoContent})
	r.mustRun("cloudhub:private-space:tls-context:delete", "ps-1", "api")
	if countCalls(r, "DELETE "+tls+"/tls-1") != 1 {
		t.Error("delete by name must use the context's ID")
	}
}
