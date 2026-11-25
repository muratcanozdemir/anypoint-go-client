package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// AMF runs on the JVM. Its classpath is pinned in tooling/amf.lock (one
// "sha256 url" line per jar, generated with internal/maven/lockgen); the
// jars are fetched on first use and checked against those hashes. The
// CLI's bridge to AMF is one Java source file, compiled once with javac
// when the JDK has it, else run with Java's source launcher.

//go:embed tooling/amf.lock
var amfLock string

//go:embed tooling/AmfTool.java
var amfToolSource []byte

type lockedJar struct{ sum, url string }

func parseLock(lock string) ([]lockedJar, error) {
	var out []lockedJar
	sc := bufio.NewScanner(strings.NewReader(lock))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sum, url, ok := strings.Cut(line, " ")
		if !ok || len(sum) != 64 {
			return nil, fmt.Errorf("bad lock line %q", line)
		}
		out = append(out, lockedJar{sum, url})
	}
	return out, sc.Err()
}

func toolingRoot() (string, error) {
	if root := os.Getenv("ANYPOINT_TOOLING_DIR"); root != "" {
		return root, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "anypoint", "tooling"), nil
}

// mavenMirror rewrites locked URLs to $ANYPOINT_MAVEN_MIRROR when set (a
// repository manager that proxies Maven Central and MuleSoft's
// repositories); the hashes still apply.
func mavenMirror(url string) string {
	m := strings.TrimRight(os.Getenv("ANYPOINT_MAVEN_MIRROR"), "/")
	if m == "" {
		return url
	}
	for _, base := range []string{"https://repo.maven.apache.org/maven2",
		"https://repository-master.mulesoft.org/nexus/content/repositories/releases",
		"https://repository-master.mulesoft.org/nexus/content/repositories/public"} {
		if rest, ok := strings.CutPrefix(url, base); ok {
			return m + rest
		}
	}
	return url
}

// classpath returns the locked jars, downloading the missing ones.
func classpath(x *Ctx, lock string) ([]string, error) {
	jars, err := parseLock(lock)
	if err != nil {
		return nil, err
	}
	root, err := toolingRoot()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, "maven")
	var out []string
	announced := false
	for _, j := range jars {
		dst := filepath.Join(dir, j.sum[:2], j.sum+"-"+path.Base(j.url))
		out = append(out, dst)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if !announced {
			fmt.Fprintf(x.Err, "Fetching the AMF tooling (first use only) into %s\n", dir)
			announced = true
		}
		if err := fetchChecked(x.ctx, mavenMirror(j.url), j.sum, dst); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// fetchChecked downloads url to dst if its sha256 is sum.
func fetchChecked(ctx context.Context, url, sum, dst string) error {
	ctx, cancel := context.WithTimeout(ctx, dxDownloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".part-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), resp.Body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		return fmt.Errorf("%s has sha256 %s, not the pinned %s; refusing to use it", url, got, sum)
	}
	return os.Rename(tmp.Name(), dst)
}

// javaBinary is $JAVA_HOME/bin/name, else name on PATH.
func javaBinary(x *Ctx, name string) (string, error) {
	if h := os.Getenv("JAVA_HOME"); h != "" {
		p := filepath.Join(h, "bin", name)
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("JAVA_HOME is set to %q but %s was not found; fix JAVA_HOME or unset it to use %s on PATH", h, p, name)
		}
		return p, nil
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("anypoint %s needs Java 17 or later: set JAVA_HOME or put java on PATH (anypoint dx:mule:jdk:download installs one)", x.Path)
	}
	return p, nil
}

// amfTool prepares the AMF classpath and the bridge, and returns the java
// arguments that run the bridge.
func amfTool(x *Ctx) ([]string, error) {
	cp, err := classpath(x, amfLock)
	if err != nil {
		return nil, err
	}
	root, err := toolingRoot()
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(append([]byte(amfLock), amfToolSource...))
	dir := filepath.Join(root, "amftool", hex.EncodeToString(sum[:8]))
	src := filepath.Join(dir, "AmfTool.java")
	classes := filepath.Join(dir, "classes")
	sep := string(os.PathListSeparator)
	if _, err := os.Stat(filepath.Join(classes, "AmfTool.class")); err == nil {
		return []string{"-cp", strings.Join(append(cp, classes), sep), "AmfTool"}, nil
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	if err := os.WriteFile(src, amfToolSource, 0o600); err != nil {
		return nil, err
	}
	if javac, err := javaBinary(x, "javac"); err == nil {
		tmp, err := os.MkdirTemp(dir, ".classes-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmp)
		out, err := exec.CommandContext(x.ctx, javac, "-nowarn", "-cp", strings.Join(cp, sep), "-d", tmp, src).CombinedOutput() //nolint:gosec // javac compiles the embedded bridge
		if err == nil && os.Rename(tmp, classes) == nil {
			return []string{"-cp", strings.Join(append(cp, classes), sep), "AmfTool"}, nil
		}
		if x.Verbose {
			fmt.Fprintf(x.Err, "javac failed, using the source launcher: %v\n%s", err, out)
		}
	}
	return []string{"-cp", strings.Join(cp, sep), src}, nil
}

// runAMF runs a bridge command and decodes its JSON answer into out.
func runAMF(x *Ctx, out any, args ...string) error {
	base, err := amfTool(x)
	if err != nil {
		return err
	}
	java, err := javaBinary(x, "java")
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(x.ctx, java, append(base, args...)...) //nolint:gosec // java runs the pinned AMF classpath
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if x.Verbose {
		fmt.Fprintf(x.Err, "> %s\n", strings.Join(cmd.Args, " "))
		cmd.Stderr = io.MultiWriter(&stderr, x.Err)
	}
	runErr := cmd.Run()
	var answer struct {
		Error string `json:"error"`
	}
	line := lastLine(stdout.Bytes())
	if json.Unmarshal(line, &answer) != nil {
		msg := strings.TrimSpace(stderr.String() + "\n" + stdout.String())
		if runErr != nil {
			return fmt.Errorf("AMF: %w: %s", runErr, msg)
		}
		return fmt.Errorf("AMF: unexpected output: %s", msg)
	}
	if answer.Error != "" {
		return errors.New("AMF: " + answer.Error)
	}
	return json.Unmarshal(line, out)
}

func lastLine(b []byte) []byte {
	b = bytes.TrimSpace(b)
	if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
		return b[i+1:]
	}
	return b
}

// fileURL turns a local path into the file: URL AMF expects.
func fileURL(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	abs = filepath.ToSlash(abs)
	if !strings.HasPrefix(abs, "/") {
		abs = "/" + abs // C:/x → file:///C:/x
	}
	return "file://" + abs, nil
}
