package main

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Some anypoint-cli-v4 commands run Java tooling that MuleSoft ships only
// inside its npm plugins (the mule-dx jars). The npm package is only a
// container here: this CLI fetches it on first use, pinned to a version and
// its registry integrity hash, unpacks the jars and runs them with Java. No
// Node.js is involved; the glue the plugin wrote in JavaScript is this CLI's.

// npmPackage is a pinned release of an npm package.
type npmPackage struct {
	Name, Version string
	Integrity     string // the registry's dist.integrity (sha512)
}

var (
	dxMulePackage = npmPackage{Name: "@salesforce/anypoint-cli-dx-mule-plugin", Version: "1.3.2",
		Integrity: "sha512-+s2WhFzD8cJNXWRV5KoESWgUqrPXPw88l9sMCXZRUlyOx4DFHwxF5wXihYy5cSbab5BQl7ueECrA3yH1ob7QLw=="}
)

// npmRegistry is where packages are fetched from: $ANYPOINT_NPM_REGISTRY
// (a mirror), else the public registry.
func npmRegistry() string {
	if r := os.Getenv("ANYPOINT_NPM_REGISTRY"); r != "" {
		return strings.TrimRight(r, "/")
	}
	return "https://registry.npmjs.org"
}

func (p npmPackage) String() string { return p.Name + "@" + p.Version }

func (p npmPackage) tarballURL() string {
	base := p.Name[strings.LastIndex(p.Name, "/")+1:]
	return npmRegistry() + "/" + p.Name + "/-/" + base + "-" + p.Version + ".tgz"
}

// dir is where the package lives once fetched.
func (p npmPackage) dir() (string, error) {
	root, err := toolingRoot()
	if err != nil {
		return "", err
	}
	name := strings.ReplaceAll(strings.TrimPrefix(p.Name, "@"), "/", "+")
	// npm/ keeps these apart from the Node plugin installs earlier builds
	// made at the top level
	return filepath.Join(root, "npm", name+"@"+p.Version), nil
}

const toolingComplete = ".anypoint-complete"

// prepare returns the package directory, running fill in a fresh directory
// and moving the result into place the first time. A crash or a concurrent
// run leaves at most a stray temporary directory.
func (p npmPackage) prepare(x *Ctx, fill func(tmp string) (string, error)) (string, error) {
	final, err := p.dir()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(final, toolingComplete)); err == nil {
		return final, nil
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o750); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(final), ".tmp-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	fmt.Fprintf(x.Err, "Fetching %s (first use only) into %s", p, final) // download() ends the line with the size
	built, err := fill(tmp)
	if err != nil {
		return "", fmt.Errorf("installing %s: %w", p, err)
	}
	if err := os.WriteFile(filepath.Join(built, toolingComplete), []byte(p.String()+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.RemoveAll(final); err != nil {
		return "", err
	}
	if err := os.Rename(built, final); err != nil {
		if _, serr := os.Stat(filepath.Join(final, toolingComplete)); serr == nil {
			return final, nil // another run finished first
		}
		return "", err
	}
	return final, nil
}

// fetch downloads the tarball into dir and checks it against the pinned
// integrity before anything reads it.
func (p npmPackage) fetch(x *Ctx, dir string) (string, error) {
	ctx, cancel := context.WithTimeout(x.ctx, dxDownloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.tarballURL(), nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return "", fmt.Errorf("GET %s: %s", p.tarballURL(), resp.Status)
	}
	path := filepath.Join(dir, "package.tgz")
	if err := download(x, resp.Body, path); err != nil {
		return "", err
	}
	want, ok := strings.CutPrefix(p.Integrity, "sha512-")
	sum, err := fileSHA512(path)
	if err != nil {
		return "", err
	}
	if !ok || base64.StdEncoding.EncodeToString(sum) != want {
		return "", fmt.Errorf("%s does not match its pinned integrity %s; refusing to use it", p.tarballURL(), p.Integrity)
	}
	return path, nil
}

func fileSHA512(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := sha512.Sum512(b)
	return s[:], nil
}

// unpack makes the package's own files available (for its jars).
func (p npmPackage) unpack(x *Ctx) (string, error) {
	return p.prepare(x, func(tmp string) (string, error) {
		tgz, err := p.fetch(x, tmp)
		if err != nil {
			return "", err
		}
		out := filepath.Join(tmp, "x")
		if _, err := extractTarGz(tgz, out); err != nil {
			return "", err
		}
		return filepath.Join(out, "package"), nil
	})
}

// javaCommand runs a jar with $JAVA_HOME's java, else java on PATH, and
// returns its standard output; standard error is shown with --verbose and
// in the error when it fails.
func javaCommand(x *Ctx, jvmArgs []string, jar string, args ...string) ([]byte, error) {
	java := "java"
	if h := os.Getenv("JAVA_HOME"); h != "" {
		java = filepath.Join(h, "bin", "java")
		if _, err := os.Stat(java); err != nil {
			return nil, fmt.Errorf("JAVA_HOME is set to %q but %s was not found; fix JAVA_HOME or unset it to use java on PATH", h, java)
		}
	} else if _, err := exec.LookPath(java); err != nil {
		return nil, fmt.Errorf("anypoint %s needs Java 17 or later: set JAVA_HOME or put java on PATH (anypoint dx:mule:jdk:download installs one)", x.Path)
	}
	cmd := exec.CommandContext(x.ctx, java, append(append(append([]string{}, jvmArgs...), "-jar", jar), args...)...) //nolint:gosec // java from JAVA_HOME/PATH runs the pinned mule-dx jar
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if x.Verbose {
		fmt.Fprintf(x.Err, "> %s\n", strings.Join(cmd.Args, " "))
		cmd.Stderr = x.Err
	}
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(strings.Join([]string{strings.TrimSpace(stderr.String()), strings.TrimSpace(stdout.String())}, "\n"))
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	return stdout.Bytes(), nil
}
