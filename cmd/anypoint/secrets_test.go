package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	sgBase = "/secrets-manager/api/v1/organizations/" + rootOrg + "/environments/" + prodEnv + "/secretGroups"
	sg     = sgBase + "/sg1"
)

func TestSecretGroups(t *testing.T) {
	r := newRecorder(t)
	r.on("GET "+sgBase, []any{map[string]any{"meta": map[string]any{"id": "sg1"}, "name": "certs", "downloadable": true}})
	if out := r.mustRun("secrets-mgr:secret-group:list"); !strings.Contains(out, "sg1") || !strings.Contains(out, "certs") {
		t.Errorf("list:\n%s", out)
	}
	r.on("GET "+sg, map[string]any{"meta": map[string]any{"id": "sg1", "modifiedBy": "jdoe"}, "name": "certs"})
	if out := r.mustRun("secrets-mgr:secret-group:describe", "-i", "sg1"); !strings.Contains(out, "jdoe") {
		t.Errorf("describe:\n%s", out)
	}

	r.on("POST "+sgBase, map[string]any{"id": "sg2", "message": "Secret group created"})
	out := r.mustRun("secrets-mgr:secret-group:create", "--name", "new", "--downloadable")
	if !strings.Contains(out, "sg2") || !strings.Contains(out, "Secret group created") {
		t.Errorf("create:\n%s", out)
	}
	body := r.last("POST " + sgBase).JSON(t)
	jsonEq(t, body, "name", "new")
	jsonEq(t, body, "downloadable", true)

	r.on("PATCH "+sg, map[string]any{"message": "updated"})
	r.mustRun("secrets-mgr:secret-group:modify", "--id", "sg1", "--no-downloadable")
	if got := string(r.last("PATCH " + sg).Body); got != `{"downloadable":false}` {
		t.Errorf("modify body = %s", got)
	}
	if _, err := r.run("secrets-mgr:secret-group:modify", "--id", "sg1"); err == nil {
		t.Error("modify without changes must fail")
	}

	r.on("DELETE "+sg, reply{204, nil})
	if out := r.mustRun("secrets-mgr:secret-group:delete", "--id", "sg1"); !strings.Contains(out, "deleted successfully") {
		t.Errorf("delete:\n%s", out)
	}
	if _, err := r.run("secrets-mgr:secret-group:delete"); err == nil || !strings.Contains(err.Error(), "--id") {
		t.Errorf("--id is required: %v", err)
	}

	// --environment selects another environment
	sbx := "/secrets-manager/api/v1/organizations/" + rootOrg + "/environments/" + sbxEnv + "/secretGroups"
	r.on("GET "+sbx, []any{})
	r.mustRun("secrets-mgr:secret-group:list", "--environment", "Sandbox")
	r.last("GET " + sbx)
}

func TestSharedSecrets(t *testing.T) {
	r := newRecorder(t)
	base := sg + "/sharedSecrets"
	r.on("POST "+base, map[string]any{"id": "s1", "message": "created"})
	r.mustRun("secrets-mgr:shared-secret:create", "-g", "sg1", "-n", "db", "-t", "UsernamePassword",
		"--secret-username", "u", "--secret-password", "p", "--expiration-date", "2030-01-01")
	body := r.last("POST " + base).JSON(t)
	jsonEq(t, body, "type", "UsernamePassword")
	jsonEq(t, body, "username", "u")
	jsonEq(t, body, "password", "p")
	jsonEq(t, body, "expirationDate", "2030-01-01")

	for _, bad := range [][]string{
		{"-t", "UsernamePassword", "--secret-username", "u"}, // password missing
		{"-t", "Blob", "--content", "x", "--key", "k"},       // key belongs to SymmetricKey
		{"-t", "Nope"},     // unknown type
		{"--content", "x"}, // type missing
	} {
		if _, err := r.run(append([]string{"secrets-mgr:shared-secret:create", "-g", "sg1", "-n", "db"}, bad...)...); err == nil {
			t.Errorf("create %v must fail", bad)
		}
	}

	r.on("PUT "+base+"/s1", map[string]any{"message": "replaced"})
	r.mustRun("secrets-mgr:shared-secret:replace", "-i", "s1", "-g", "sg1", "-n", "db", "-t", "S3Credential",
		"--access-key-id", "AK", "--secret-access-key", "SK")
	jsonEq(t, r.last("PUT "+base+"/s1").JSON(t), "accessKeyId", "AK")

	r.on("PATCH "+base+"/s1", map[string]any{"message": "modified"})
	r.mustRun("secrets-mgr:shared-secret:modify", "-i", "s1", "-g", "sg1", "--expiration-date", "2031-01-01")
	if got := string(r.last("PATCH " + base + "/s1").Body); got != `{"expirationDate":"2031-01-01"}` {
		t.Errorf("modify body = %s", got)
	}

	r.on("GET "+base, []any{map[string]any{"meta": map[string]any{"id": "s1"}, "name": "db", "type": "Blob"}})
	if out := r.mustRun("secrets-mgr:shared-secret:list", "-g", "sg1"); !strings.Contains(out, "N/A") {
		t.Errorf("list without expiration should show N/A:\n%s", out)
	}
	r.on("GET "+base+"/s1", map[string]any{"meta": map[string]any{"id": "s1"}, "name": "db", "type": "UsernamePassword", "username": "u"})
	if out := r.mustRun("secrets-mgr:shared-secret:describe", "-i", "s1", "-g", "sg1"); !strings.Contains(out, "Username") {
		t.Errorf("describe:\n%s", out)
	}
}

func TestSecretStores(t *testing.T) {
	r := newRecorder(t)
	dir := t.TempDir()
	file := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cert, key, jks := file("cert.pem", "CERT"), file("key.pem", "KEY"), file("ks.jks", "JKS")

	r.on("POST "+sg+"/keystores", map[string]any{"id": "k1", "message": "created"})
	r.mustRun("secrets-mgr:keystore:create", "-g", "sg1", "-n", "pem", "-t", "PEM", "--cert-file", cert, "--key-file", key, "--key-passphrase", "kp")
	f := multipartFields(t, r.last("POST "+sg+"/keystores"))
	for k, want := range map[string]string{"type": "PEM", "name": "pem", "certificate": "CERT", "key": "KEY", "keyPassphrase": "kp"} {
		if f[k] != want {
			t.Errorf("PEM keystore %s = %q, want %q", k, f[k], want)
		}
	}
	if _, ok := f["storePassphrase"]; ok {
		t.Error("empty fields must not be sent")
	}

	r.mustRun("secrets-mgr:keystore:create", "-g", "sg1", "-n", "jks", "-t", "JKS", "--keystore-file", jks,
		"--store-passphrase", "sp", "--key-passphrase", "kp", "--alias", "a", "--algorithm", "PKIX")
	f = multipartFields(t, r.last("POST "+sg+"/keystores"))
	for k, want := range map[string]string{"keyStore": "JKS", "storePassphrase": "sp", "alias": "a", "algorithm": "PKIX"} {
		if f[k] != want {
			t.Errorf("JKS keystore %s = %q, want %q", k, f[k], want)
		}
	}
	for _, bad := range [][]string{
		{"-t", "JKS", "--keystore-file", jks, "--store-passphrase", "sp", "--key-passphrase", "kp"},                                     // alias missing
		{"-t", "PEM", "--cert-file", cert, "--key-file", key, "--alias", "a"},                                                           // alias not for PEM
		{"-t", "JKS", "--keystore-file", jks, "--store-passphrase", "s", "--key-passphrase", "k", "--alias", "a", "--algorithm", "MD5"}, // bad algorithm
	} {
		if _, err := r.run(append([]string{"secrets-mgr:keystore:create", "-g", "sg1", "-n", "x"}, bad...)...); err == nil {
			t.Errorf("keystore create %v must fail", bad)
		}
	}

	r.on("PUT "+sg+"/truststores/t1", map[string]any{"message": "replaced"})
	r.mustRun("secrets-mgr:truststore:replace", "-i", "t1", "-g", "sg1", "-n", "ts", "-t", "PEM", "--truststore-file", cert)
	if f := multipartFields(t, r.last("PUT "+sg+"/truststores/t1")); f["trustStore"] != "CERT" || f["type"] != "PEM" {
		t.Errorf("truststore: %v", f)
	}
	if _, err := r.run("secrets-mgr:truststore:create", "-g", "sg1", "-n", "ts", "-t", "JKS", "--truststore-file", jks); err == nil {
		t.Error("a JKS truststore needs --store-passphrase")
	}

	r.on("POST "+sg+"/certificates", map[string]any{"id": "c1", "message": "created"})
	r.mustRun("secrets-mgr:certificate:create", "-g", "sg1", "-n", "c", "-t", "PEM", "--cert-file", cert)
	if f := multipartFields(t, r.last("POST "+sg+"/certificates")); f["certStore"] != "CERT" {
		t.Errorf("certificate: %v", f)
	}

	r.on("GET "+sg+"/keystores/k1", map[string]any{"meta": map[string]any{"id": "k1"}, "name": "jks", "type": "JKS",
		"keystoreFileName": "ks.jks", "alias": "a"})
	if out := r.mustRun("secrets-mgr:keystore:describe", "-i", "k1", "-g", "sg1"); !strings.Contains(out, "ks.jks") || !strings.Contains(out, "Alias") {
		t.Errorf("keystore describe:\n%s", out)
	}
}

func TestTLSContexts(t *testing.T) {
	r := newRecorder(t)
	base := sg + "/tlsContexts"
	r.on("POST "+base, map[string]any{"id": "tls1", "message": "created"})

	r.mustRun("secrets-mgr:tls-context:mule:create", "-g", "sg1", "-n", "m", "--keystore-id", "k1", "-v", "TLSv1.1", "-v", "TLSv1.2", "-c", "C1")
	body := r.last("POST " + base).JSON(t)
	jsonEq(t, body, "target", "Mule")
	jsonEq(t, body, "keystore.path", "keystores/k1")
	jsonEq(t, body, "acceptableTlsVersions", map[string]any{"tlsV1Dot1": true, "tlsV1Dot2": true})
	jsonEq(t, body, "cipherSuites", []string{"C1"})
	jsonEq(t, body, "insecure", false)

	r.mustRun("secrets-mgr:tls-context:mule:create", "-g", "sg1", "-n", "m", "--truststore-id", "t1", "--insecure")
	body = r.last("POST " + base).JSON(t)
	jsonEq(t, body, "acceptableTlsVersions.tlsV1Dot2", true)
	jsonEq(t, body, "acceptableTlsVersions.tlsV1Dot1", false)
	jsonEq(t, body, "insecure", true)
	jsonEq(t, body, "cipherSuites", []string{})
	if _, err := r.run("secrets-mgr:tls-context:mule:create", "-g", "sg1", "-n", "m"); err == nil {
		t.Error("a Mule TLS context needs a keystore or truststore")
	}
	if _, err := r.run("secrets-mgr:tls-context:mule:create", "-g", "sg1", "-n", "m", "--keystore-id", "k", "--insecure"); err == nil {
		t.Error("--insecure needs --truststore-id")
	}

	// Flex Gateway: TLS 1.3 only by default, no ciphers.
	r.mustRun("secrets-mgr:tls-context:flex-gateway:create", "-g", "sg1", "-n", "f", "--keystore-id", "k1")
	body = r.last("POST " + base).JSON(t)
	jsonEq(t, body, "target", "FlexGateway")
	jsonEq(t, body, "minTlsVersion", "TLSv1.3")
	jsonEq(t, body, "alpnProtocols", []string{"h2", "http/1.1"})
	jsonEq(t, body, "cipherSuites", []string{})
	jsonEq(t, body, "inboundSettings.enableClientCertValidation", false)
	jsonEq(t, body, "outboundSettings.skipServerCertValidation", false)

	// TLS 1.2 in range and only a 1.1-only cipher given: 1.2 defaults first.
	r.mustRun("secrets-mgr:tls-context:flex-gateway:create", "-g", "sg1", "-n", "f", "--min-tls-version", "TLSv1.1",
		"--max-tls-version", "TLSv1.2", "-c", "TLS_RSA_WITH_3DES_EDE_CBC_SHA", "--skip-server-cert-validation")
	body = r.last("POST " + base).JSON(t)
	suites := body.(map[string]any)["cipherSuites"].([]any)
	if len(suites) != len(flexDefaultCiphers)+1 || suites[len(suites)-1] != "TLS_RSA_WITH_3DES_EDE_CBC_SHA" {
		t.Errorf("cipher suites = %v", suites)
	}
	jsonEq(t, body, "outboundSettings.skipServerCertValidation", true)
	for _, bad := range [][]string{
		{"-c", "NOT_A_CIPHER"},
		{"--min-tls-version", "TLSv1.1", "--max-tls-version", "TLSv1.2"}, // TLS 1.1 needs a 1.1 cipher
		{"--alpn-protocol", "spdy"},
	} {
		if _, err := r.run(append([]string{"secrets-mgr:tls-context:flex-gateway:create", "-g", "sg1", "-n", "f"}, bad...)...); err == nil {
			t.Errorf("flex create %v must fail", bad)
		}
	}

	r.on("GET "+base, []any{map[string]any{"meta": map[string]any{"id": "tls1"}, "name": "m"}})
	r.mustRun("secrets-mgr:tls-context:mule:list", "-g", "sg1")
	if got := r.last("GET " + base).Query; got != "type=Mule" {
		t.Errorf("mule list query = %q", got)
	}
	r.mustRun("secrets-mgr:tls-context:tls-context-list", "-g", "sg1")
	if got := r.last("GET " + base).Query; got != "" {
		t.Errorf("tls-context-list must not filter: %q", got)
	}

	r.on("GET "+base+"/tls1", map[string]any{"meta": map[string]any{"id": "tls1"}, "name": "m", "target": "Mule",
		"acceptableTlsVersions": map[string]any{"tlsV1Dot2": true}, "cipherSuites": []any{}, "keystore": map[string]any{"path": "keystores/k1"}})
	out := r.mustRun("secrets-mgr:tls-context:mule:describe", "-i", "tls1", "-g", "sg1")
	if !strings.Contains(out, "TLSv1.2") || !strings.Contains(out, "k1") || strings.Contains(out, "keystores/") {
		t.Errorf("mule describe:\n%s", out)
	}
}
