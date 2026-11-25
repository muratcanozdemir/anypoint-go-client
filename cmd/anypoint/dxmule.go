package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/muratcanozdemir/anypoint-go-client/anypoint"
)

// @salesforce/anypoint-cli-dx-mule-plugin (dx:mule:*). Runtimes, JDKs and
// the runtime path are implemented here. Creating projects and describing
// connectors run MuleSoft's mule-dx jars, which this CLI takes from the
// plugin's npm package on first use and runs with Java (see tooling.go).

func dxCmd() *Cmd {
	return group("dx", "Developer experience",
		group("mule", "Mule development tools",
			leaf("describe-connector", "", "Describe a connector's operations, sources and configs (--connector GAV)", cmdDXDescribeConnector),
			group("jdk", "JDKs", leaf("download", "", "Install a JDK for Mule development (default: the latest runtime's)", cmdDXJDKDownload)),
			group("project", "Mule projects", leaf("create", "PROJECTNAME", "Create a Mule project from scratch or a template", cmdDXProjectCreate)),
			group("runtime", "Mule runtimes",
				leaf("download", "", "Install a Mule runtime (default: the latest)", cmdDXRuntimeDownload),
				leaf("list", "", "List the supported Mule runtimes", cmdDXRuntimeList).alias("ls"),
				leaf("path", "", "Show or (--set) change the Mule runtime the tools use", cmdDXRuntimePath)),
		))
}

func codeBuilderDir(sub string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "AnypointCodeBuilder", sub), nil
}

func cmdDXRuntimeList(x *Ctx, args []string) error {
	_, c, _, err := orgSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	fmt.Fprintln(x.Err, "Fetching available runtimes...")
	rts, err := c.ListMuleDXRuntimes(x.ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch runtimes: %w", err)
	}
	if rts == nil {
		rts = []anypoint.MuleDXRuntime{}
	}
	return x.Print(rts, func() {
		if len(rts) == 0 {
			fmt.Fprintln(x.Out, "No runtime versions found")
			return
		}
		fmt.Fprintln(x.Out, "Supported Mule Runtime Versions:")
		for _, r := range rts {
			latest := ""
			if r.Latest {
				latest = " (LATEST)"
			}
			fmt.Fprintf(x.Out, "  %s%s\n", r.Version, latest)
		}
	})
}

func latestRuntime(x *Ctx, c *anypoint.Client) (*anypoint.MuleDXRuntime, error) {
	rts, err := c.ListMuleDXRuntimes(x.ctx)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(rts, func(r anypoint.MuleDXRuntime) bool { return r.Latest })
	if i < 0 {
		return nil, errors.New("no latest runtime found")
	}
	return &rts[i], nil
}

// dxDownloadTimeout bounds a runtime or JDK download.
var dxDownloadTimeout = 15 * time.Minute

// download saves a stream to path, printing a dot per MB to the progress writer.
func download(x *Ctx, body io.ReadCloser, path string) error {
	defer body.Close()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	var n int64
	buf := make([]byte, 256<<10)
	next := int64(1 << 20)
	for {
		k, rerr := body.Read(buf)
		if k > 0 {
			if _, err := f.Write(buf[:k]); err != nil {
				f.Close()
				return err
			}
			for n += int64(k); n >= next; next += 1 << 20 {
				fmt.Fprint(x.Err, ".")
			}
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			f.Close()
			return rerr
		}
	}
	fmt.Fprintf(x.Err, " (%.1f MB total)\n", float64(n)/(1<<20))
	return f.Close()
}

// safeJoin joins an archive entry name under dir, refusing names that
// would leave it.
func safeJoin(dir, name string) (string, error) {
	name = strings.TrimLeft(strings.ReplaceAll(name, `\`, "/"), "/")
	if name == "" || slices.Contains(strings.Split(name, "/"), "..") {
		return "", fmt.Errorf("unsafe archive entry %q", name)
	}
	return filepath.Join(dir, filepath.FromSlash(name)), nil
}

func topDir(name string) string {
	name = strings.TrimPrefix(strings.TrimLeft(strings.ReplaceAll(name, `\`, "/"), "/"), "./")
	top, _, _ := strings.Cut(name, "/")
	return top
}

// extractTarGz unpacks a .tar.gz into dir and returns its top-level names.
func extractTarGz(path, dir string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(bufio.NewReader(f))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	var tops []string
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return tops, nil
		}
		if err != nil {
			return nil, err
		}
		if t := topDir(h.Name); t != "" && t != "." && !slices.Contains(tops, t) {
			tops = append(tops, t)
		}
		if strings.Trim(h.Name, "./") == "" {
			continue
		}
		dst, err := safeJoin(dir, h.Name)
		if err != nil {
			return nil, err
		}
		mode := os.FileMode(h.Mode&0o777) &^ 0o022 // never group/world-writable
		switch h.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(dst, 0o750)
		case tar.TypeReg:
			err = writeEntry(dst, tr, mode)
		case tar.TypeSymlink:
			// keep links that stay inside the archive (JDKs and runtimes use them)
			target := filepath.Join(filepath.Dir(dst), h.Linkname) //nolint:gosec // checked to stay under dir just below
			// tar link names use '/', so "/etc/passwd" is absolute even where filepath.IsAbs says not (Windows)
			if filepath.IsAbs(h.Linkname) || strings.HasPrefix(strings.ReplaceAll(h.Linkname, `\`, "/"), "/") ||
				filepath.VolumeName(h.Linkname) != "" || !strings.HasPrefix(target, filepath.Clean(dir)+string(filepath.Separator)) {
				return nil, fmt.Errorf("unsafe link %q -> %q", h.Name, h.Linkname)
			}
			if err = os.MkdirAll(filepath.Dir(dst), 0o750); err == nil {
				_ = os.Remove(dst)
				err = os.Symlink(filepath.FromSlash(h.Linkname), dst)
			}
		}
		if err != nil {
			return nil, err
		}
	}
}

func writeEntry(dst string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	if mode == 0 {
		mode = 0o644
	}
	w, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, r); err != nil { //nolint:gosec // archives from the Mule DX service; sizes are bounded by the download
		w.Close()
		return err
	}
	return w.Close()
}

// extractZip unpacks a zip into dir and returns its top-level names.
func extractZip(path, dir string) ([]string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var tops []string
	for _, f := range zr.File {
		if t := topDir(f.Name); t != "" && !slices.Contains(tops, t) {
			tops = append(tops, t)
		}
		dst, err := safeJoin(dir, f.Name)
		if err != nil {
			return nil, err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dst, 0o750); err != nil {
				return nil, err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		err = writeEntry(dst, rc, f.Mode().Perm()&^0o022)
		rc.Close()
		if err != nil {
			return nil, err
		}
	}
	return tops, nil
}

func cmdDXRuntimeDownload(x *Ctx, args []string) error {
	fs := x.Flags()
	version := fs.String("version", "", "runtime `version` (default: the latest)")
	_, c, _, err := orgSetup(x, fs, args)
	if err != nil {
		return err
	}
	v := *version
	if v == "" {
		fmt.Fprintln(x.Err, "Fetching available runtimes...")
		r, err := latestRuntime(x, c)
		if err != nil {
			return err
		}
		v = r.Version
		fmt.Fprintf(x.Err, "Latest runtime: %s\n", v)
	}
	dir, err := codeBuilderDir("runtime")
	if err != nil {
		return err
	}
	install := filepath.Join(dir, "mule-enterprise-standalone-"+v)
	if _, err := os.Stat(install); err == nil {
		return x.Done("Runtime %s already installed at %s", v, install)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	fmt.Fprintf(x.Err, "Downloading Mule Runtime %s...\n", v)
	ctx, cancel := context.WithTimeout(x.ctx, dxDownloadTimeout)
	defer cancel()
	body, err := c.DownloadMuleDXRuntime(ctx, v)
	if err != nil {
		return err
	}
	archive := filepath.Join(dir, "runtime-"+safeFileName(v)+".tar.gz")
	defer os.Remove(archive)
	if err := download(x, body, archive); err != nil {
		return err
	}
	fmt.Fprintln(x.Err, "Extracting runtime...")
	if _, err := extractTarGz(archive, dir); err != nil {
		return err
	}
	return x.Done("Runtime installed at %s", install)
}

func jdkTarget() (string, string) {
	osName := map[string]string{"darwin": "mac", "windows": "windows", "linux": "linux"}[runtime.GOOS]
	arch := map[string]string{"amd64": "x64", "arm64": "arm64", "386": "ia32", "arm": "arm"}[runtime.GOARCH]
	return cmp(osName, runtime.GOOS), cmp(arch, runtime.GOARCH)
}

func cmdDXJDKDownload(x *Ctx, args []string) error {
	fs := x.Flags()
	version := fs.String("version", "", "JDK `version` (default: the latest runtime's)")
	_, c, _, err := orgSetup(x, fs, args)
	if err != nil {
		return err
	}
	v := *version
	if v == "" {
		fmt.Fprintln(x.Err, "Resolving latest JDK...")
		r, err := latestRuntime(x, c)
		if err != nil {
			return err
		}
		if len(r.CompatibleJDKs) == 0 {
			return fmt.Errorf("no compatible JDK found for runtime %s", r.Version)
		}
		v = r.CompatibleJDKs[0].Version
		for _, j := range r.CompatibleJDKs {
			if j.Latest {
				v = j.Version
			}
		}
		fmt.Fprintf(x.Err, "Latest JDK: %s (for runtime %s)\n", v, r.Version)
	}
	osName, arch := jdkTarget()
	dir, err := codeBuilderDir("java")
	if err != nil {
		return err
	}
	install := filepath.Join(dir, "jdk-"+strings.ReplaceAll(v, "_", "+"))
	if _, err := os.Stat(install); err == nil {
		return x.Done("JDK %s already installed", v)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	fmt.Fprintf(x.Err, "Downloading JDK %s (%s/%s)...\n", v, osName, arch)
	ctx, cancel := context.WithTimeout(x.ctx, dxDownloadTimeout)
	defer cancel()
	body, err := c.DownloadMuleDXJDK(ctx, v, osName, arch)
	if err != nil {
		return err
	}
	archive := filepath.Join(dir, "jdk-"+safeFileName(v)+".download")
	defer os.Remove(archive)
	if err := download(x, body, archive); err != nil {
		return err
	}
	head := make([]byte, 2)
	if f, err := os.Open(archive); err == nil {
		_, _ = io.ReadFull(f, head)
		f.Close()
	}
	tmp, err := os.MkdirTemp(dir, "_jdk_extract_tmp_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	fmt.Fprintln(x.Err, "Extracting JDK...")
	var tops []string
	switch {
	case head[0] == 'P' && head[1] == 'K':
		tops, err = extractZip(archive, tmp)
	case head[0] == 0x1f && head[1] == 0x8b:
		tops, err = extractTarGz(archive, tmp)
	default:
		return fmt.Errorf("unexpected archive format (expected .zip or .tar.gz, got bytes %x)", head)
	}
	if err != nil {
		return err
	}
	if len(tops) != 1 {
		return fmt.Errorf("unexpected JDK archive layout: expected a single top-level folder, found [%s]", strings.Join(tops, ", "))
	}
	if err := os.Rename(filepath.Join(tmp, tops[0]), install); err != nil {
		return err
	}
	return x.Done("JDK installed at %s", install)
}

func dxConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".mule-dx", "config.json"), nil
}

func cmdDXRuntimePath(x *Ctx, args []string) error {
	fs := x.Flags()
	set := fs.String("set", "", "runtime `directory` to use")
	if _, err := x.Parse(fs, args); err != nil {
		return err
	}
	cfgPath, err := dxConfigPath()
	if err != nil {
		return err
	}
	cfg := map[string]any{}
	if b, err := os.ReadFile(cfgPath); err == nil {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("failed to read config: %w", err)
		}
	}
	if *set != "" {
		p := expandHome(strings.TrimSpace(*set))
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("path does not exist: %s", p)
		}
		cfg["runtimePath"] = p
		b, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.MkdirAll(filepath.Dir(cfgPath), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
			return err
		}
		return x.Done("Runtime path set to: %s (saved in %s)", p, cfgPath)
	}
	p, _ := cfg["runtimePath"].(string)
	if p == "" {
		return errors.New("no mule runtime path configured; set one with `anypoint dx:mule:runtime:path --set <path>` or install one with `anypoint dx:mule:runtime:download`")
	}
	exists := "Path exists"
	if _, err := os.Stat(p); err != nil {
		exists = "Path does not exist"
	}
	return x.Print(map[string]any{"runtimePath": p, "exists": exists == "Path exists"}, func() {
		fmt.Fprintf(x.Out, "Current runtime path: %s\n  %s\n", p, exists)
	})
}

// dxJars returns the mule-dx jars directory, fetching the plugin package on
// first use.
func dxJars(x *Ctx) (string, error) {
	dir, err := dxMulePackage.unpack(x)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "jars"), nil
}

// v4 names its text output "text"; this CLI's is "table".
func textOutput(args []string) []string {
	out := slices.Clone(args)
	for i, a := range out {
		switch {
		case a == "--output=text" || a == "-o=text":
			out[i] = "-o=table"
		case (a == "--output" || a == "-o") && i+1 < len(out) && out[i+1] == "text":
			out[i+1] = "table"
		}
	}
	return out
}

func cmdDXProjectCreate(x *Ctx, args []string) error {
	fs := x.Flags()
	groupID := fs.String("group-id", "", "Maven group `ID` for the project")
	muleVersion := fs.String("mule-version", "4.4.0", "Mule runtime `version`")
	deps := fs.String("dependencies", "", "comma-separated `GAVs` (groupId:artifactId:version) to add")
	templateFile := fs.String("template-file", "", "local template `file` (.jar or .zip)")
	templateAsset := fs.String("template-asset", "", "Exchange template `groupId:assetId:version`")
	assetType := fs.String("asset-type", "template", "Exchange asset `type` for --template-asset: template or example")
	fs.Bool("skip-environment", false, "accepted for anypoint-cli-v4 compatibility (no environment is needed)")
	pos, err := x.Parse(fs, textOutput(args))
	if err != nil {
		return err
	}
	if *templateFile != "" && *templateAsset != "" {
		return errors.New("--template-file and --template-asset cannot be used together")
	}
	if err := oneOf("asset-type", *assetType, "template", "example"); err != nil {
		return err
	}
	jars, err := dxJars(x)
	if err != nil {
		return err
	}
	output := map[string]string{"table": "text", "json": "json"}[x.Output]
	javaArgs := []string{"create-project", pos[0]}
	switch {
	case *templateAsset != "":
		tmp, err := downloadTemplate(x, *templateAsset, *assetType)
		if err != nil {
			return err
		}
		defer os.Remove(tmp)
		javaArgs = append(javaArgs, "--template-file", tmp)
	case *templateFile != "":
		javaArgs = append(javaArgs, "--template-file", expandHome(*templateFile))
	}
	if *groupID != "" {
		javaArgs = append(javaArgs, "--group-id", *groupID)
	}
	javaArgs = append(javaArgs, "--mule-version", *muleVersion)
	if *deps != "" {
		javaArgs = append(javaArgs, "--dependencies", *deps)
	}
	javaArgs = append(javaArgs, "--output", output)
	if output == "text" {
		fmt.Fprintf(x.Err, "Creating project: %s...\n", pos[0])
	}
	out, err := javaCommand(x, nil, filepath.Join(jars, "mule-dx-tooling-cli.jar"), javaArgs...)
	if err != nil {
		return fmt.Errorf("failed to create project: %w", err)
	}
	_, err = x.Out.Write(out)
	return err
}

// downloadTemplate fetches an Exchange template (or example) jar.
func downloadTemplate(x *Ctx, gav, assetType string) (string, error) {
	parts := strings.Split(gav, ":")
	if len(parts) != 3 || slices.Contains(parts, "") {
		return "", fmt.Errorf("invalid template GAV %q: expected groupId:assetId:version", gav)
	}
	c, err := x.Client()
	if err != nil {
		return "", err
	}
	classifier := "mule-application-template"
	if assetType == "example" {
		classifier = "mule-application-example"
	}
	fmt.Fprintf(x.Err, "Downloading template: %s...\n", gav)
	ctx, cancel := context.WithTimeout(x.ctx, time.Minute)
	defer cancel()
	body, err := c.DownloadExchangeMavenArtifact(ctx, parts[0], parts[1], parts[2], classifier)
	if err != nil {
		var ferr error
		if body, ferr = c.DownloadExchangeMavenArtifact(ctx, parts[0], parts[1], parts[2], ""); ferr != nil {
			return "", fmt.Errorf("template download failed: %w; without a classifier: %w", err, ferr)
		}
	}
	f, err := os.CreateTemp("", safeFileName(parts[1]+"-"+parts[2])+"-*.jar")
	if err != nil {
		body.Close()
		return "", err
	}
	f.Close()
	if err := download(x, body, f.Name()); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// The flow design service needs these opened on Java 17+, as v4 runs it.
var designServiceJVM = []string{
	"--add-opens", "java.base/java.lang=ALL-UNNAMED",
	"--add-opens", "java.base/java.lang.ref=ALL-UNNAMED",
	"--add-opens", "java.base/java.lang.reflect=ALL-UNNAMED",
	"--add-opens", "java.base/java.lang.invoke=ALL-UNNAMED",
	"--add-opens", "java.xml/javax.xml.namespace=ALL-UNNAMED",
}

func cmdDXDescribeConnector(x *Ctx, args []string) error {
	fs := x.Flags()
	connector := fs.String("connector", "", "connector `GAV` (groupId:artifactId:version)")
	kind := fs.String("type", "", "detail `type`: connection-provider, operation or source")
	name := fs.String("name", "", "component `name` (required for operation and source)")
	configName := fs.String("config-name", "", "configuration `name` (required with --type connection-provider)")
	if _, err := x.Parse(fs, args); err != nil {
		return err
	}
	if err := need("connector", *connector); err != nil {
		return err
	}
	switch {
	case *kind != "" && oneOf("type", *kind, "connection-provider", "operation", "source") != nil:
		return oneOf("type", *kind, "connection-provider", "operation", "source")
	case (*kind == "operation" || *kind == "source") && *name == "":
		return errors.New("--name is required when --type is operation or source")
	case *name != "" && *kind == "":
		return errors.New("--type is required when --name is provided")
	case *kind == "connection-provider" && *configName == "":
		return errors.New("--config-name is required when --type is connection-provider")
	}
	gav := strings.Split(*connector, ":")
	if len(gav) != 3 || slices.Contains(gav, "") {
		return errors.New("connector must be in format: groupId:artifactId:version")
	}
	runtimePath, err := dxRuntimePath()
	if err != nil {
		return err
	}
	muleVersion := "4.11.2"
	if v, ok := strings.CutPrefix(filepath.Base(runtimePath), "mule-enterprise-standalone-"); ok && v != "" {
		muleVersion = v
	}
	jars, err := dxJars(x)
	if err != nil {
		return err
	}
	entries, _ := os.ReadDir(filepath.Join(jars, "flow-design-service"))
	jar := ""
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "mule-dx-flow-design-service-impl-") && strings.HasSuffix(e.Name(), ".jar") {
			jar = filepath.Join(jars, "flow-design-service", e.Name())
		}
	}
	if jar == "" {
		return errors.New("the flow design service jar is missing from " + dxMulePackage.String())
	}
	gavArgs := []string{"--muleVersion", muleVersion, "--runtimePath", runtimePath,
		"--groupId", gav[0], "--artifactId", gav[1], "--version", gav[2]}
	settings, err := exchangeMavenSettings(x)
	if err != nil {
		return err
	}
	if settings != "" {
		defer os.RemoveAll(filepath.Dir(settings))
		gavArgs = append(gavArgs, "--mavenSettingsFile", settings)
	}
	var javaArgs []string
	switch *kind {
	case "":
		javaArgs = append([]string{"describe-connector"}, gavArgs...)
	case "operation":
		javaArgs = append(append([]string{"get-operation-detail"}, gavArgs...), "--operationName", *name)
	case "source":
		javaArgs = append(append([]string{"get-source-detail"}, gavArgs...), "--sourceName", *name)
	default:
		javaArgs = append(append([]string{"get-config-detail"}, gavArgs...), "--configName", *configName)
		if *name != "" {
			javaArgs = append(javaArgs, "--connectionProviderName", *name)
		}
	}
	out, err := javaCommand(x, designServiceJVM, jar, javaArgs...)
	if err != nil {
		return fmt.Errorf("failed to describe connector: %w", err)
	}
	var doc connectorDoc
	if err := json.Unmarshal(out, &doc); err != nil {
		return fmt.Errorf("unexpected describe-connector output: %w\n%s", err, out)
	}
	return x.Print(json.RawMessage(out), func() { printConnector(x, *kind, *connector, &doc) })
}

// dxRuntimePath is the runtime the tools use (dx:mule:runtime:path).
func dxRuntimePath() (string, error) {
	cfgPath, err := dxConfigPath()
	if err != nil {
		return "", err
	}
	var cfg struct {
		RuntimePath string `json:"runtimePath"`
	}
	if b, err := os.ReadFile(cfgPath); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	if cfg.RuntimePath == "" {
		return "", errors.New("no Mule runtime configured: install one with `anypoint dx:mule:runtime:download` or point to one with `anypoint dx:mule:runtime:path --set DIR`")
	}
	if _, err := os.Stat(cfg.RuntimePath); err != nil {
		return "", fmt.Errorf("the configured Mule runtime %s does not exist: change it with `anypoint dx:mule:runtime:path --set DIR`", cfg.RuntimePath)
	}
	return cfg.RuntimePath, nil
}

type connectorDoc struct {
	Name        string `json:"name"`
	Prefix      string `json:"prefix"`
	ElementName string `json:"elementName"`
	Namespace   struct {
		Prefix    string `json:"prefix"`
		Namespace string `json:"namespace"`
	} `json:"namespace"`
	Operations []string `json:"operations"`
	Sources    []string `json:"sources"`
	Configs    []struct {
		Name                string   `json:"name"`
		ConnectionProviders []string `json:"connectionProviders"`
	} `json:"configs"`
	Attributes []struct {
		AttributeName string `json:"attributeName"`
		Required      bool   `json:"required"`
	} `json:"attributes"`
	ChildElements []struct {
		Prefix      string `json:"prefix"`
		ElementName string `json:"elementName"`
		Required    bool   `json:"required"`
	} `json:"childElements"`
	ConnectionProviders []struct {
		Name        string `json:"name"`
		Prefix      string `json:"prefix"`
		ElementName string `json:"elementName"`
	} `json:"connectionProviders"`
}

func printConnector(x *Ctx, kind, gav string, d *connectorDoc) {
	w := x.Out
	req := func(b bool) string {
		if b {
			return " (required)"
		}
		return ""
	}
	switch kind {
	case "":
		fmt.Fprintf(w, "\nConnector: %s\nNamespace: %s (%s)\n", gav, d.Namespace.Prefix, d.Namespace.Namespace)
		fmt.Fprintf(w, "\nOperations (%d):\n", len(d.Operations))
		for _, o := range d.Operations {
			fmt.Fprintf(w, "  - %s\n", o)
		}
		fmt.Fprintf(w, "\nSources (%d):\n", len(d.Sources))
		for _, s := range d.Sources {
			fmt.Fprintf(w, "  - %s\n", s)
		}
		fmt.Fprintln(w, "\nConfigs:")
		for _, c := range d.Configs {
			fmt.Fprintf(w, "  - %s\n    Connection Providers: %s\n", c.Name, strings.Join(c.ConnectionProviders, ", "))
		}
	case "connection-provider":
		fmt.Fprintf(w, "\nConfig: %s\nElement: <%s:%s>\n", d.Name, d.Prefix, d.ElementName)
		for _, cp := range d.ConnectionProviders {
			fmt.Fprintf(w, "\nConnection Provider: %s\n", cp.Name)
			if cp.Prefix != "" && cp.ElementName != "" {
				fmt.Fprintf(w, "Element: <%s:%s>\n", cp.Prefix, cp.ElementName)
			}
		}
	default:
		label := map[string]string{"operation": "Operation", "source": "Source"}[kind]
		fmt.Fprintf(w, "\n%s: %s\nElement: <%s:%s>\n", label, d.Name, d.Prefix, d.ElementName)
		if len(d.Attributes) > 0 {
			fmt.Fprintln(w, "Attributes:")
			for _, a := range d.Attributes {
				fmt.Fprintf(w, "  - %s%s\n", a.AttributeName, req(a.Required))
			}
		}
		if len(d.ChildElements) > 0 {
			fmt.Fprintln(w, "Child Elements:")
			for _, c := range d.ChildElements {
				fmt.Fprintf(w, "  - <%s:%s>%s\n", c.Prefix, c.ElementName, req(c.Required))
			}
		}
	}
}

const (
	exchangeServerID  = "anypoint-exchange-v3"
	exchangeProfileID = "anypoint-exchange"
)

// exchangeMavenSettings writes a private Maven settings.xml that adds
// Exchange (signed in with this CLI's credentials) to the user's
// ~/.m2/settings.xml, so the jar can resolve private connectors. It returns
// "" when there are no credentials. The caller removes its directory.
func exchangeMavenSettings(x *Ctx) (string, error) {
	c, err := x.Client()
	if err != nil {
		return "", err
	}
	user, pass := "~~~Client~~~", x.prof.ClientID+"~?~"+x.prof.ClientSecret
	if x.prof.ClientID == "" || x.prof.ClientSecret == "" || x.prof.Username != "" {
		tok, err := c.Token(x.ctx)
		if errors.Is(err, anypoint.ErrNoCredentials) {
			return "", nil
		}
		if err != nil {
			return "", hint(err)
		}
		user, pass = "~~~Token~~~", tok
	}
	base := ""
	if home, err := os.UserHomeDir(); err == nil {
		if b, err := os.ReadFile(filepath.Join(home, ".m2", "settings.xml")); err == nil {
			base = string(b)
		}
	}
	doc := mergeMavenSettings(base, user, pass, c.ExchangeMavenURL()+"/api/v3/maven")
	dir, err := os.MkdirTemp("", "anypoint-dx-mule-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "settings.xml")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return path, nil
}

func xmlText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// mergeMavenSettings adds the Exchange server, profile and active profile
// to a settings.xml, ahead of any existing entries so they win, and keeps
// catch-all mirrors from swallowing Exchange requests.
func mergeMavenSettings(base, user, pass, repoURL string) string {
	server := "<server><id>" + exchangeServerID + "</id><username>" + xmlText(user) + "</username><password>" + xmlText(pass) + "</password></server>"
	profile := "<profile><id>" + exchangeProfileID + "</id><repositories><repository><id>" + exchangeServerID +
		"</id><url>" + xmlText(repoURL) + "</url></repository></repositories></profile>"
	active := "<activeProfile>" + exchangeProfileID + "</activeProfile>"
	if !strings.Contains(base, "</settings>") {
		return `<?xml version="1.0" encoding="UTF-8"?>` + "\n<settings>\n  <servers>" + server + "</servers>\n  <profiles>" + profile +
			"</profiles>\n  <activeProfiles>" + active + "</activeProfiles>\n</settings>\n"
	}
	insert := func(doc, container, item string) string {
		if i := strings.Index(doc, "<"+container+">"); i >= 0 {
			i += len(container) + 2
			return doc[:i] + item + doc[i:]
		}
		doc = strings.Replace(doc, "<"+container+"/>", "", 1)
		i := strings.LastIndex(doc, "</settings>")
		return doc[:i] + "<" + container + ">" + item + "</" + container + ">\n" + doc[i:]
	}
	doc := insert(insert(insert(base, "servers", server), "profiles", profile), "activeProfiles", active)
	var b strings.Builder
	for {
		i := strings.Index(doc, "<mirrorOf>")
		j := strings.Index(doc[max(i, 0):], "</mirrorOf>")
		if i < 0 || j < 0 {
			b.WriteString(doc)
			return b.String()
		}
		j += i
		value := doc[i+len("<mirrorOf>") : j]
		parts := strings.Split(value, ",")
		for k := range parts {
			parts[k] = strings.TrimSpace(parts[k])
		}
		broad := slices.ContainsFunc(parts, func(p string) bool {
			return p == "*" || p == "external:*" || p == "external:http:*" || p == "external:https:*"
		})
		if broad && !slices.Contains(parts, "!"+exchangeServerID) {
			value = strings.Join(append(parts, "!"+exchangeServerID), ",")
		}
		b.WriteString(doc[:i] + "<mirrorOf>" + value + "</mirrorOf>")
		doc = doc[j+len("</mirrorOf>"):]
	}
}
