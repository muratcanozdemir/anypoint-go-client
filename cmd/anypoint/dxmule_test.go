package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tarGz(t *testing.T, files map[string]string, links map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range sortedKeys(files) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(files[name])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range sortedKeys(links) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Linkname: links[name], Typeflag: tar.TypeSymlink}); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestDXMuleRuntimes(t *testing.T) {
	r := newRecorder(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	r.on("GET /muledx/api/runtime/all", map[string]any{"supportedRuntimeVersions": []any{
		map[string]any{"version": "4.9.0"},
		map[string]any{"version": "4.10.0", "latest": true, "compatibleJDKs": []any{map[string]any{"version": "17.0.12_7", "latest": true}}}}})
	if out := r.mustRun("dx:mule:runtime:list"); !strings.Contains(out, "4.10.0 (LATEST)") {
		t.Errorf("list:\n%s", out)
	}

	r.on("GET /muledx/api/runtime/download/4.10.0", tarGz(t,
		map[string]string{"mule-enterprise-standalone-4.10.0/bin/mule": "#!/bin/sh"},
		map[string]string{"mule-enterprise-standalone-4.10.0/bin/mule-link": "mule"}))
	out := r.mustRun("dx:mule:runtime:download")
	install := filepath.Join(home, "AnypointCodeBuilder", "runtime", "mule-enterprise-standalone-4.10.0")
	if !strings.Contains(out, install) {
		t.Errorf("download: %s", out)
	}
	if b, err := os.ReadFile(filepath.Join(install, "bin", "mule-link")); err != nil || string(b) != "#!/bin/sh" {
		t.Errorf("runtime files: %q %v", b, err)
	}
	if out := r.mustRun("dx:mule:runtime:download", "--version", "4.10.0"); !strings.Contains(out, "already installed") {
		t.Errorf("second download: %s", out)
	}
	if entries, _ := os.ReadDir(filepath.Dir(install)); len(entries) != 1 {
		t.Errorf("the archive must be removed, found %v", entries)
	}

	r.on("GET /muledx/api/runtime/download/6.6.6", tarGz(t, map[string]string{"../../evil": "x"}, nil))
	if _, err := r.run("dx:mule:runtime:download", "--version", "6.6.6"); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Errorf("traversal: %v", err)
	}
	r.on("GET /muledx/api/runtime/download/6.6.7", tarGz(t, nil, map[string]string{"rt/l": "/etc/passwd"}))
	if _, err := r.run("dx:mule:runtime:download", "--version", "6.6.7"); err == nil {
		t.Error("an absolute link must be refused")
	}

	// JDK: a zip with one top folder, installed under its canonical name
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	f, _ := zw.Create("jdk-17.0.12+7/bin/java")
	if _, err := f.Write([]byte("JAVA")); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	r.on("GET /muledx/api/jdk/download", zb.Bytes())
	r.mustRun("dx:mule:jdk:download")
	c := r.last("GET /muledx/api/jdk/download")
	if !strings.Contains(c.Query, "version=17.0.12_7") || !strings.Contains(c.Query, "os=") {
		t.Errorf("jdk query = %s", c.Query)
	}
	if b, err := os.ReadFile(filepath.Join(home, "AnypointCodeBuilder", "java", "jdk-17.0.12+7", "bin", "java")); err != nil || string(b) != "JAVA" {
		t.Errorf("jdk files: %q %v", b, err)
	}

	if _, err := r.run("dx:mule:runtime:path"); err == nil {
		t.Error("no path configured yet")
	}
	r.mustRun("dx:mule:runtime:path", "--set", install)
	if out := r.mustRun("dx:mule:runtime:path"); !strings.Contains(out, install) || !strings.Contains(out, "Path exists") {
		t.Errorf("path: %s", out)
	}
	if _, err := r.run("dx:mule:runtime:path", "--set", filepath.Join(home, "missing")); err == nil {
		t.Error("a missing path must be refused")
	}
}
