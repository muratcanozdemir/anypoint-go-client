package main

import (
	"crypto/sha512"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeNPMPackage serves a package tarball from the recorder as the npm
// registry and returns its pinned description.
func fakeNPMPackage(t *testing.T, r *recorder, name, version string, files map[string]string) npmPackage {
	t.Helper()
	pkg := map[string]string{}
	for n, c := range files {
		pkg["package/"+n] = c
	}
	tgz := tarGz(t, pkg, nil)
	sum := sha512.Sum512(tgz)
	p := npmPackage{Name: name, Version: version, Integrity: "sha512-" + base64.StdEncoding.EncodeToString(sum[:])}
	t.Setenv("ANYPOINT_NPM_REGISTRY", r.srv.URL)
	r.on("GET "+strings.TrimPrefix(p.tarballURL(), r.srv.URL), tgz)
	return p
}

func swapPackage(t *testing.T, dst *npmPackage, p npmPackage) {
	old := *dst
	*dst = p
	t.Cleanup(func() { *dst = old })
}

func TestToolingIntegrity(t *testing.T) {
	r := newRecorder(t)
	p := fakeNPMPackage(t, r, "@acme/tool", "1.0.0", map[string]string{"jars/a.jar": "A"})
	if got := p.tarballURL(); got != r.srv.URL+"/@acme/tool/-/tool-1.0.0.tgz" {
		t.Errorf("tarball URL %s", got)
	}
	x := &Ctx{Err: &strings.Builder{}, ctx: t.Context()}
	bad := p
	bad.Integrity = "sha512-" + base64.StdEncoding.EncodeToString(make([]byte, 64))
	if _, err := bad.unpack(x); err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("a tampered tarball must be refused: %v", err)
	}
	if dir, _ := bad.dir(); func() bool { _, err := os.Stat(dir); return err == nil }() {
		t.Error("nothing may be installed from a tampered tarball")
	}
	dir, err := p.unpack(x)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "jars", "a.jar")); string(b) != "A" {
		t.Errorf("jar = %q", b)
	}
	before := countCalls(r, "GET /@acme/tool/-/tool-1.0.0.tgz")
	if _, err := p.unpack(x); err != nil || countCalls(r, "GET /@acme/tool/-/tool-1.0.0.tgz") != before {
		t.Errorf("a second use must not fetch again (%v)", err)
	}
}

func TestDXMuleJars(t *testing.T) {
	r := newRecorder(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	dp := fakeNPMPackage(t, r, "@salesforce/anypoint-cli-dx-mule-plugin", "9.9.9", map[string]string{
		"jars/mule-dx-tooling-cli.jar":                                      "tooling",
		"jars/flow-design-service/mule-dx-flow-design-service-impl-1.0.jar": "design",
	})
	swapPackage(t, &dxMulePackage, dp)
	javaHome := t.TempDir()
	capture := filepath.Join(t.TempDir(), "settings.xml")
	if err := os.MkdirAll(filepath.Join(javaHome, "bin"), 0o750); err != nil {
		t.Fatal(err)
	}
	java := `case "$*" in
*describe-connector*) while [ $# -gt 0 ]; do [ "$1" = --mavenSettingsFile ] && cp "$2" "` + capture + `"; shift; done
  echo '{"namespace":{"prefix":"http","namespace":"http://www.mulesoft.org/schema/mule/http"},"operations":["request"],"sources":["listener"],"configs":[{"name":"listenerConfig","connectionProviders":["listener"]}]}';;
*get-operation-detail*) echo '{"name":"request","prefix":"http","elementName":"request","attributes":[{"attributeName":"path","required":true}],"childElements":[]}';;
*broken*) echo 'boom' >&2; exit 1;;
*) echo "java $*";;
esac`
	if err := os.WriteFile(filepath.Join(javaHome, "bin", "java"), []byte("#!/bin/sh\n"+java), 0o700); err != nil {
		t.Skip(err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fakes")
	}
	t.Setenv("JAVA_HOME", javaHome)

	out := r.mustRun("dx:mule:project:create", "demo", "--group-id", "com.acme", "--output", "text", "--skip-environment")
	if !strings.Contains(out, "mule-dx-tooling-cli.jar create-project demo --group-id com.acme --mule-version 4.4.0 --output text") {
		t.Errorf("create:\n%s", out)
	}
	r.on("GET /api/v2/maven/org/mule/templates/tpl/1.0.0/tpl-1.0.0-mule-application-template.jar", reply{404, map[string]any{}})
	r.on("GET /api/v2/maven/org/mule/templates/tpl/1.0.0/tpl-1.0.0.jar", []byte("TEMPLATE"))
	out = r.mustRun("dx:mule:project:create", "demo", "--template-asset", "org.mule.templates:tpl:1.0.0", "-o", "json")
	if !strings.Contains(out, "--template-file ") || !strings.Contains(out, "--output json") {
		t.Errorf("template asset:\n%s", out)
	}
	if c := r.last("GET /api/v2/maven/org/mule/templates/tpl/1.0.0/tpl-1.0.0.jar"); c.Header.Get("Authorization") != "Bearer test-token" {
		t.Error("the Exchange Maven download must be signed in")
	}
	if _, err := r.run("dx:mule:project:create", "broken"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("a java failure must report its stderr: %v", err)
	}

	if _, err := r.run("dx:mule:describe-connector", "--connector", "org.mule.connectors:mule-http-connector:1.10.0"); err == nil || !strings.Contains(err.Error(), "no Mule runtime") {
		t.Errorf("describe needs a runtime: %v", err)
	}
	rt := filepath.Join(home, "mule-enterprise-standalone-4.9.0")
	if err := os.MkdirAll(rt, 0o750); err != nil {
		t.Fatal(err)
	}
	r.mustRun("dx:mule:runtime:path", "--set", rt)
	out = r.mustRun("dx:mule:describe-connector", "--connector", "org.mule.connectors:mule-http-connector:1.10.0")
	if !strings.Contains(out, "Namespace: http (http://www.mulesoft.org/schema/mule/http)") || !strings.Contains(out, "Operations (1):\n  - request") ||
		!strings.Contains(out, "Connection Providers: listener") {
		t.Errorf("describe:\n%s", out)
	}
	if b, _ := os.ReadFile(capture); !strings.Contains(string(b), "<username>~~~Token~~~</username><password>test-token</password>") {
		t.Errorf("maven settings:\n%s", b)
	}
	out = r.mustRun("dx:mule:describe-connector", "--connector", "org.mule.connectors:mule-http-connector:1.10.0", "--type", "operation", "--name", "request")
	if !strings.Contains(out, "Operation: request\nElement: <http:request>\nAttributes:\n  - path (required)") {
		t.Errorf("operation:\n%s", out)
	}
	for _, bad := range [][]string{{"--type", "operation"}, {"--name", "x"}, {"--type", "connection-provider"}, {"--type", "nope", "--name", "x"}} {
		if _, err := r.run(append([]string{"dx:mule:describe-connector", "--connector", "a:b:c"}, bad...)...); err == nil {
			t.Errorf("describe %v must fail", bad)
		}
	}
}

func TestMergeMavenSettings(t *testing.T) {
	got := mergeMavenSettings("", "u", "p&q", "https://maven.example/api/v3/maven")
	if !strings.Contains(got, "<password>p&amp;q</password>") || !strings.Contains(got, "<activeProfile>anypoint-exchange</activeProfile>") {
		t.Errorf("fresh settings:\n%s", got)
	}
	base := `<settings><servers><server><id>corp</id></server></servers><mirrors><mirror><id>m</id><mirrorOf>*</mirrorOf></mirror>` +
		`<mirror><id>n</id><mirrorOf>central</mirrorOf></mirror></mirrors></settings>`
	got = mergeMavenSettings(base, "u", "p", "https://maven.example/api/v3/maven")
	for _, want := range []string{
		"<servers><server><id>anypoint-exchange-v3</id>", "<server><id>corp</id></server>",
		"<mirrorOf>*,!anypoint-exchange-v3</mirrorOf>", "<mirrorOf>central</mirrorOf>",
		"<profiles><profile><id>anypoint-exchange</id>", "<activeProfiles><activeProfile>anypoint-exchange</activeProfile></activeProfiles>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("merged settings lack %s:\n%s", want, got)
		}
	}
}
