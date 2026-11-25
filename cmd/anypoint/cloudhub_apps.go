package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/muratcanozdemir/anypoint-go-client/anypoint"
)

// anypoint-cli-cloudhub-plugin, runtime-mgr:*: CloudHub 1.0 applications
// (cloudhub-application), CloudHub 2.0 / Runtime Fabric applications
// (application) and CloudHub 1.0 alerts.

func cloudhubRuntimeMgrCmds() []*Cmd {
	return []*Cmd{
		group("application", "CloudHub 2.0 and Runtime Fabric applications",
			leaf("delete", "NAME", "Delete an application (by ID)", cmdCH2Delete).alias("rm"),
			leaf("deploy", "NAME DEPLOYMENTTARGETID RUNTIMEVERSION ARTIFACTID", "Deploy an application from Exchange", cmdCH2Deploy),
			leaf("describe", "APPID", "Show an application", cmdCH2Describe),
			leaf("download-logs", "APPID SPECID DIRECTORY", "Download the logs of a deployment spec as a zip", cmdCH2DownloadLogs),
			leaf("list", "", "List applications", cmdCH2List).alias("ls"),
			leaf("logs", "APPID SPECID", "Tail the logs of a deployment spec (ctrl-c stops)", cmdCH2Logs),
			leaf("modify", "APPID", "Change an application", cmdCH2Modify),
			leaf("start", "APPID", "Start an application", cmdCH2State("STARTED")),
			leaf("stop", "APPID", "Stop an application", cmdCH2State("STOPPED")),
		),
		group("cloudhub-alert", "CloudHub 1.0 alerts",
			leaf("list", "", "List alerts", cmdCloudHubAlertList).alias("ls")),
		group("cloudhub-alert-history", "CloudHub 1.0 alert history",
			leaf("describe", "[NAME]", "Show when an alert fired", cmdCloudHubAlertHistory)),
		group("cloudhub-application", "CloudHub 1.0 applications",
			leaf("copy", "SOURCE TARGET", "Copy an application ([org[:env]/]app) to a new one", cmdCH1Copy),
			leaf("copy-replace", "SOURCE TARGET", "Replace an application's archive with another's ([org[:env]/]app)", cmdCH1CopyReplace),
			leaf("delete", "NAME", "Delete an application", cmdCH1Delete).alias("rm"),
			leaf("deploy", "NAME ZIPFILE", "Deploy an application", cmdCH1Deploy),
			leaf("describe", "NAME", "Show an application", cmdCH1Describe),
			leaf("download-logs", "NAME DIRECTORY", "Download the latest deployment's logs as a zip", cmdCH1DownloadLogs),
			leaf("list", "", "List applications", cmdCH1List).alias("ls"),
			leaf("modify", "NAME [ZIPFILE]", "Change an application's settings or archive", cmdCH1Modify),
			leaf("restart", "APPLICATIONNAME", "Restart an application", cmdCH1Status("restart")),
			leaf("revert-runtime", "NAME", "Go back to the previous runtime", cmdCH1RevertRuntime),
			leaf("start", "APPLICATIONNAME", "Start an application", cmdCH1Status("start")),
			leaf("stop", "APPLICATIONNAME", "Stop an application", cmdCH1Status("stop")),
			leaf("tail-logs", "NAME", "Tail the latest deployment's logs (ctrl-c stops)", cmdCH1TailLogs),
		),
	}
}

// ---------------------------------------------------------------- properties

// splitProperty splits "name:value" or "name=value" at the first unescaped
// ":" or "=" (\: and \= stand for literal ones), keeping a "secure::"
// prefix on the name. anypoint-cli-v4 unescapes before splitting, so its
// escapes never protect a name; this follows their intent.
func splitProperty(p string) (string, string, error) {
	prefix := ""
	if rest, ok := strings.CutPrefix(p, "secure::"); ok {
		prefix, p = "secure::", rest
	}
	unescape := strings.NewReplacer(`\=`, "=", `\:`, ":").Replace
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '\\':
			i++
		case ':', '=':
			return prefix + unescape(p[:i]), unescape(p[i+1:]), nil
		}
	}
	return "", "", fmt.Errorf("invalid property format, expected name:value but was %s", unescape(p))
}

// parseProperties adds name:value pairs to props (a new map when nil).
func parseProperties(pairs []string, props map[string]any) (map[string]any, error) {
	if props == nil {
		props = map[string]any{}
	}
	for _, p := range pairs {
		k, v, err := splitProperty(p)
		if err != nil {
			return nil, err
		}
		props[k] = v
	}
	return props, nil
}

// fileLines returns a file's non-empty lines.
func fileLines(path string) ([]string, error) {
	b, err := os.ReadFile(expandHome(path))
	if err != nil {
		return nil, err
	}
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		if l := strings.TrimRight(sc.Text(), "\r"); l != "" {
			out = append(out, l)
		}
	}
	return out, sc.Err()
}

var validLogLevels = []string{"DEBUG", "INFO", "WARN", "ERROR"}

func logLevel(s string) (string, error) {
	u := strings.ToUpper(strings.TrimSpace(s))
	if !slices.Contains(validLogLevels, u) {
		return "", fmt.Errorf("invalid logLevel %q. Valid levels are: %s", s, strings.Join(validLogLevels, ", "))
	}
	return u, nil
}

// scopeLogging parses --scopeLoggingConfig "scope:LEVEL,scope2:LEVEL".
func scopeLogging(s string) ([]any, error) {
	out := []any{}
	for part := range strings.SplitSeq(s, ",") {
		scope, lvl, _ := strings.Cut(part, ":")
		l, err := logLevel(lvl)
		if err != nil {
			return nil, err
		}
		out = append(out, anypoint.Doc{"scope": scope, "logLevel": l})
	}
	return out, nil
}

var (
	enclosed   = regexp.MustCompile(`^[(\[{](.*)[)\]}]$`)
	scopeRe    = regexp.MustCompile(`scope\s*[:=]\s*["']?([^"',]+)["']?`)
	logLevelRe = regexp.MustCompile(`logLevel\s*[:=]\s*["']?([^"',]+)["']?`)
)

// scopeLoggingFile parses a --scopeLoggingConfigFile: one
// "{scope: name, logLevel: LEVEL}" per line ((), [] or {} around it).
// Strict files fail on a bad line; others skip it with a warning, as
// anypoint-cli-v4's deploy and modify do.
func scopeLoggingFile(x *Ctx, path string, strict bool) ([]any, error) {
	lines, err := fileLines(path)
	if err != nil {
		return nil, err
	}
	out := []any{}
	for _, line := range lines {
		cfg, err := func() (anypoint.Doc, error) {
			m := enclosed.FindStringSubmatch(strings.TrimSpace(line))
			if m == nil {
				return nil, errors.New("missing enclosing brackets")
			}
			s, l := scopeRe.FindStringSubmatch(m[1]), logLevelRe.FindStringSubmatch(m[1])
			if s == nil || l == nil {
				return nil, errors.New("missing scope or logLevel")
			}
			lvl, err := logLevel(l[1])
			if err != nil {
				return nil, err
			}
			return anypoint.Doc{"scope": strings.TrimSpace(s[1]), "logLevel": lvl}, nil
		}()
		if err != nil {
			if strict {
				return nil, fmt.Errorf("invalid config format, expected {scope: scopeName, logLevel: logLevelType} but was %s: %w", line, err)
			}
			fmt.Fprintf(x.Err, "Skipping invalid logging config line: %s - %v\n", line, err)
			continue
		}
		out = append(out, cfg)
	}
	return out, nil
}

// optBool is a boolean flag with a --no- form whose absence is visible.
type optBool struct {
	on, off *bool
	name    string
}

func newOptBool(fs *flag.FlagSet, name, usage string) *optBool {
	return &optBool{fs.Bool(name, false, usage), fs.Bool("no-"+name, false, "turn off --"+name), name}
}

// get returns the flag's value and whether it was given.
func (o *optBool) get(set map[string]bool) (bool, bool) {
	return *o.on && !*o.off, set[o.name] || set["no-"+o.name]
}

// ---------------------------------------------------------------- CloudHub 1.0 applications

var workerSizes = map[string]string{"0.1": "Micro", "0.2": "Small", "1": "Medium", "2": "Large", "4": "xLarge", "8": "xxLarge", "16": "4xLarge"}

// ch1Flags are the settings deploy and modify share.
type ch1Flags struct {
	fs                                                  *flag.FlagSet
	runtime, channel, java, workers, workerSize, region *string
	propsFile                                           *string
	props                                               *multiFlag
	pq, pqEnc, staticIPs, osV1, osV2, autoRestart       *optBool
}

func newCH1Flags(fs *flag.FlagSet) *ch1Flags {
	return &ch1Flags{fs: fs,
		runtime:     fs.String("runtime", "", "Mule runtime `version`, e.g. 4.6.0"),
		channel:     fs.String("releaseChannel", "", "runtime release `channel`"),
		java:        fs.String("javaVersion", "", "Java `version`"),
		workers:     fs.String("workers", "", "number of `workers` (1-8)"),
		workerSize:  fs.String("workerSize", "", "worker size in `vCores`: 0.1, 0.2, 1, 2, 4, 8, 16"),
		region:      fs.String("region", "", "`region` to deploy to"),
		props:       multi(fs, "property", "property `name:value` (repeatable)"),
		propsFile:   fs.String("propertiesFile", "", "`file` of name:value properties, one per line (replaces all)"),
		pq:          newOptBool(fs, "persistentQueues", "use persistent queues"),
		pqEnc:       newOptBool(fs, "persistentQueuesEncrypted", "encrypt persistent queues"),
		staticIPs:   newOptBool(fs, "staticIPsEnabled", "use static IPs"),
		osV1:        newOptBool(fs, "objectStoreV1", "use Object Store v1"),
		osV2:        newOptBool(fs, "objectStoreV2", "use Object Store v2"),
		autoRestart: newOptBool(fs, "autoRestart", "restart the application when it stops responding"),
	}
}

func setPath(m map[string]any, path string, v any) {
	keys := strings.Split(path, ".")
	for _, k := range keys[:len(keys)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[k] = next
		}
		m = next
	}
	m[keys[len(keys)-1]] = v
}

// body builds the appInfoJson of the given flags; current properties are
// kept and overridden by --property.
func (f *ch1Flags) body(current map[string]string) (map[string]any, error) {
	set := setFlags(f.fs)
	if set["objectStoreV1"] && set["objectStoreV2"] {
		return nil, errors.New("--objectStoreV1 and --objectStoreV2 cannot be used together")
	}
	d := map[string]any{}
	if set["runtime"] {
		setPath(d, "muleVersion.version", *f.runtime)
	}
	if set["region"] {
		d["region"] = *f.region
	}
	if set["releaseChannel"] {
		setPath(d, "muleVersion.releaseChannel", *f.channel)
		d["releaseChannel"] = *f.channel
	}
	if set["javaVersion"] {
		setPath(d, "muleVersion.javaVersion", *f.java)
		d["javaVersion"] = *f.java
	}
	if set["workers"] {
		n, err := strconv.Atoi(*f.workers)
		if err != nil || n < 1 || n > 8 {
			return nil, errors.New("invalid workers. Valid values are 1 to 8")
		}
		setPath(d, "workers.amount", n)
	}
	if set["workerSize"] {
		name, ok := workerSizes[*f.workerSize]
		if !ok {
			return nil, errors.New("invalid workerSize. Valid values are 0.1, 0.2, 1, 2, 4, 8, 16")
		}
		setPath(d, "workers.type.name", name)
	}
	switch {
	case set["propertiesFile"]:
		lines, err := fileLines(*f.propsFile)
		if err != nil {
			return nil, err
		}
		if d["properties"], err = parseProperties(lines, nil); err != nil {
			return nil, err
		}
	case set["property"]:
		props := map[string]any{}
		for k, v := range current {
			props[k] = v
		}
		var err error
		if d["properties"], err = parseProperties(*f.props, props); err != nil {
			return nil, err
		}
	}
	for key, o := range map[string]*optBool{"persistentQueues": f.pq, "persistentQueuesEncrypted": f.pqEnc, "staticIPsEnabled": f.staticIPs, "objectStoreV1": f.osV1} {
		if v, ok := o.get(set); ok {
			d[key] = v
		}
	}
	if v, ok := f.osV2.get(set); ok {
		d["objectStoreV1"] = !v
	}
	if v, ok := f.autoRestart.get(set); ok {
		d["monitoringEnabled"], d["monitoringAutoRestart"] = v, v
	}
	return d, nil
}

func ch1Status(a *anypoint.CloudHubApp) string {
	if a.DeploymentUpdateStatus == "DEPLOY_FAILED" && a.Status == "STARTED" {
		return "STARTED (Update failed)"
	}
	return cmp(a.DeploymentUpdateStatus, a.Status)
}

func showCH1App(x *Ctx, a *anypoint.CloudHubApp) error {
	return x.Print(a, func() {
		kv := []string{"Domain", a.FullDomain, "Status", ch1Status(a)}
		if a.DeploymentErrorMessage != "" {
			kv = append(kv, "Deployment error", a.DeploymentErrorMessage)
		}
		kv = append(kv, "Updated", agoAny(a.LastUpdateTime), "Runtime", a.MuleVersion.Version,
			"Release Channel", cmp(a.MuleVersion.ReleaseChannel, "NONE"), "Java version", cmp(a.MuleVersion.JavaVersion, "8"))
		if a.PreviousMuleVersion != nil {
			kv = append(kv, "Previous runtime", a.PreviousMuleVersion.Version)
		}
		kv = append(kv, "File name", a.FileName)
		if a.Region != "" {
			kv = append(kv, "Region", a.Region)
		}
		monitoring := "Disabled"
		if a.MonitoringAutoRestart {
			monitoring = "Enabled. Will auto restart when not responding"
		}
		var workers strings.Builder
		fmt.Fprintf(&workers, "%s * %d", a.Workers.Type.CPU, a.Workers.Amount)
		for _, w := range a.WorkerStatuses {
			fmt.Fprintf(&workers, "\nIP: %s, %s", w.Host, w.Status)
		}
		kv = append(kv, "Persistent queues", strconv.FormatBool(a.PersistentQueues),
			"Persistent queues encrypted", strconv.FormatBool(a.PersistentQueuesEnc),
			"Static IPs enabled", strconv.FormatBool(a.StaticIPsEnabled), "Monitoring", monitoring, "Workers", workers.String())
		var props []string
		for _, k := range sortedKeys(a.Properties) {
			props = append(props, k+" = "+a.Properties[k])
		}
		if len(props) > 0 {
			kv = append(kv, "Properties", strings.Join(props, "\n"))
		}
		printKV(x.Out, kv...)
	})
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func cmdCH1List(x *Ctx, args []string) error {
	_, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	apps, err := c.ListCloudHubApps(x.ctx, org, env)
	if err != nil {
		return err
	}
	return x.Print(apps, func() {
		t := newTable(x.Out, "APPLICATION", "FILE", "STATUS", "VCORES", "UPDATED")
		for i := range apps {
			a := &apps[i]
			t.row(a.Domain, a.FileName, ch1Status(a), fmt.Sprintf("%s * %d", a.Workers.Type.CPU, a.Workers.Amount), agoAny(a.LastUpdateTime))
		}
		t.flush()
	})
}

func cmdCH1Describe(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	a, err := c.GetCloudHubApp(x.ctx, org, env, pos[0])
	if err != nil {
		return err
	}
	return showCH1App(x, a)
}

func cmdCH1Deploy(x *Ctx, args []string) error {
	fs := x.Flags()
	timeout := fs.Int("timeout", 90000, "upload timeout in `milliseconds` (60000-300000)")
	f := newCH1Flags(fs)
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	if *timeout < 60000 || *timeout > 300000 {
		return errors.New("set the timeout value in milliseconds. Accepted values between 60000 and 300000")
	}
	file, err := openArchive(pos[1])
	if err != nil {
		return err
	}
	d, err := f.body(nil)
	if err != nil {
		return err
	}
	d["domain"] = pos[0]
	if workers, _ := d["workers"].(map[string]any); workers["amount"] == nil {
		setPath(d, "workers.amount", 1)
	}
	if _, ok := d["monitoringAutoRestart"]; !ok {
		d["monitoringEnabled"], d["monitoringAutoRestart"] = true, true
	}
	fmt.Fprintf(x.Err, "Deploying %s ...\n", pos[0])
	ctx, cancel := context.WithTimeout(x.ctx, time.Duration(*timeout)*time.Millisecond)
	defer cancel()
	a, err := c.DeployCloudHubApp(ctx, org, env, d, true, file)
	if err != nil {
		return err
	}
	return showCH1App(x, a)
}

func cmdCH1Modify(x *Ctx, args []string) error {
	fs := x.Flags()
	f := newCH1Flags(fs)
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	cur, err := c.GetCloudHubApp(x.ctx, org, env, pos[0])
	if err != nil {
		return err
	}
	u := anypoint.CloudHubUpdate{AutoStart: true}
	if zip := arg(pos, 1); zip != "" {
		file, err := openArchive(zip)
		if err != nil {
			return err
		}
		u.File = &file
	}
	d, err := f.body(cur.Properties)
	if err != nil {
		return err
	}
	fmt.Fprintf(x.Err, "Updating %s ...\n", pos[0])
	a, err := c.UpdateCloudHubApp(x.ctx, org, env, pos[0], d, u)
	if err != nil {
		return err
	}
	return showCH1App(x, a)
}

func cmdCH1RevertRuntime(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	cur, err := c.GetCloudHubApp(x.ctx, org, env, pos[0])
	if err != nil {
		return err
	}
	if cur.PreviousMuleVersion == nil {
		return errors.New("this application has no previous runtime to revert to")
	}
	a, err := c.UpdateCloudHubApp(x.ctx, org, env, pos[0], anypoint.Doc{"muleVersion": anypoint.Doc{"version": cur.PreviousMuleVersion.Version}}, anypoint.CloudHubUpdate{})
	if err != nil {
		return err
	}
	return showCH1App(x, a)
}

func cmdCH1Delete(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := c.DeleteCloudHubApp(x.ctx, org, env, pos[0]); err != nil {
		return err
	}
	return x.Done("%s deleted", pos[0])
}

// setCH1Status changes an application's status; "already" reports the 304
// the API answers when nothing changes.
func setCH1Status(x *Ctx, c *anypoint.Client, org, env, name, status string) (already bool, err error) {
	err = c.SetCloudHubAppStatus(x.ctx, org, env, name, status)
	if anypoint.IsStatus(err, 304) {
		return true, nil
	}
	return false, err
}

func cmdCH1Status(status string) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		pos, c, org, env, err := apiSetup(x, x.Flags(), args)
		if err != nil {
			return err
		}
		already, err := setCH1Status(x, c, org, env, pos[0], status)
		if err != nil {
			return err
		}
		if already {
			return x.Done("Application already in this state")
		}
		return x.Done("OK")
	}
}

// latestDeployment returns an application's newest deployment.
func latestDeployment(x *Ctx, c *anypoint.Client, org, env, name string) (*anypoint.CloudHubDeployment, error) {
	deps, err := c.ListCloudHubDeployments(x.ctx, org, env, name, 1)
	if err != nil {
		return nil, err
	}
	if len(deps) == 0 {
		return nil, errors.New("this app has no logs")
	}
	return &deps[0], nil
}

// writeZip writes files (name → content) into a new zip archive at path.
func writeZip(path string, files map[string][]byte) error {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, name := range sortedKeys(files) {
		f, err := w.Create(name)
		if err != nil {
			return err
		}
		if _, err := f.Write(files[name]); err != nil {
			return err
		}
	}
	if err := w.Close(); err != nil {
		return err
	}
	_, err := writeFile(path, &buf)
	return err
}

// timeOf reads an epoch-milliseconds or RFC 3339 time (zero when neither).
func timeOf(v any) time.Time {
	switch t := v.(type) {
	case float64:
		return time.UnixMilli(int64(t)).UTC()
	case string:
		if n, err := strconv.ParseInt(t, 10, 64); err == nil {
			return time.UnixMilli(n).UTC()
		}
		ts, _ := time.Parse(time.RFC3339, t)
		return ts.UTC()
	}
	return time.Time{}
}

func cmdCH1DownloadLogs(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	dep, err := latestDeployment(x, c, org, env, pos[0])
	if err != nil {
		return err
	}
	files := map[string][]byte{}
	for _, in := range dep.Instances {
		fmt.Fprintf(x.Err, "Downloading log for instance %s\n", in.InstanceID)
		b, err := c.GetCloudHubInstanceLog(x.ctx, org, env, pos[0], in.InstanceID)
		if err != nil {
			return err
		}
		if len(b) > 0 {
			files[safeFileName(in.InstanceID)+".txt"] = b
		}
	}
	created := timeOf(dep.CreateTime)
	if created.IsZero() {
		created = time.Now().UTC()
	}
	dst := filepath.Join(pos[1], safeFileName(pos[0]+"-"+created.Format("2006_01_02")+".zip"))
	if err := writeZip(dst, files); err != nil {
		return err
	}
	return x.Done("Saved to %s", dst)
}

// tailPolls limits how often tail loops poll (0: until interrupted); tests set it.
var (
	tailPolls    = 0
	tailInterval = 500 * time.Millisecond
)

// tail calls poll until the context ends (or tailPolls is reached).
func tail(x *Ctx, poll func() error) error {
	fmt.Fprintln(x.Err, "Tailing log... hit ctrl-c to stop tailing")
	for n := 1; ; n++ {
		if err := poll(); err != nil {
			return err
		}
		if tailPolls > 0 && n >= tailPolls {
			return nil
		}
		select {
		case <-x.ctx.Done():
			return nil
		case <-time.After(tailInterval):
		}
	}
}

func cmdCH1TailLogs(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	dep, err := latestDeployment(x, c, org, env, pos[0])
	if err != nil {
		return err
	}
	var latest int64
	return tail(x, func() error {
		lines, err := c.ListCloudHubDeploymentLogs(x.ctx, org, env, pos[0], dep.DeploymentID, 50)
		if err != nil {
			return err
		}
		for _, l := range lines {
			if l.Timestamp > latest {
				fmt.Fprintf(x.Out, "[%s] %s\n", time.UnixMilli(l.Timestamp).Format("15:04:05 2 Jan"), l.Message)
				latest = l.Timestamp
			}
		}
		return nil
	})
}

// ch1Ref resolves a "[org[:env]/]app" reference.
func ch1Ref(x *Ctx, c *anypoint.Client, org, env, raw string) (appRef, string, string, error) {
	r := parseAppRef(raw)
	o, e, err := r.scope(x, c, org, env)
	return r, o, e, err
}

// cleanAppData turns an application's document into the appInfoJson of a
// copy named domain.
func cleanAppData(a *anypoint.CloudHubApp, domain string) (map[string]any, error) {
	var d map[string]any
	if err := json.Unmarshal(a.RawJSON(), &d); err != nil {
		return nil, err
	}
	for _, k := range []string{"versionId", "fullDomain", "workerStatuses", "lastUpdateTime", "previousMuleVersion", "userName",
		"persistentQueuesEncryptionEnabled", "hasFile", "ipAddresses", "status"} {
		delete(d, k)
	}
	region, _ := d["cloudObjectStoreRegion"].(string)
	d["objectStoreV1"] = region == ""
	d["domain"] = domain
	return d, nil
}

func downloadCH1Archive(x *Ctx, c *anypoint.Client, org, env string, a *anypoint.CloudHubApp) (anypoint.HybridFile, error) {
	body, err := c.DownloadCloudHubAppFile(x.ctx, org, env, a.Domain, a.FileName)
	if err != nil {
		return anypoint.HybridFile{}, err
	}
	b, err := readAllClose(body)
	if err != nil {
		return anypoint.HybridFile{}, err
	}
	return anypoint.HybridFile{Name: a.FileName, Content: bytes.NewReader(b)}, nil
}

func cmdCH1Copy(x *Ctx, args []string) (err error) {
	fs := x.Flags()
	props := multi(fs, "property", "property `name:value` to set on the copy (repeatable)")
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	src, sOrg, sEnv, err := ch1Ref(x, c, org, env, pos[0])
	if err != nil {
		return err
	}
	dst, tOrg, tEnv, err := ch1Ref(x, c, org, env, pos[1])
	if err != nil {
		return err
	}
	if _, err := c.GetCloudHubApp(x.ctx, tOrg, tEnv, dst.app); err == nil {
		return fmt.Errorf("application named %s already exists", dst.app)
	} else if !anypoint.IsNotFound(err) {
		return err
	}
	fmt.Fprintln(x.Err, "Loading source application")
	sApp, err := c.GetCloudHubApp(x.ctx, sOrg, sEnv, src.app)
	if err != nil {
		return fmt.Errorf("source app: %w", err)
	}
	d, err := cleanAppData(sApp, dst.app)
	if err != nil {
		return err
	}
	if len(*props) > 0 {
		cur, _ := d["properties"].(map[string]any)
		if d["properties"], err = parseProperties(*props, cur); err != nil {
			return err
		}
	}
	fmt.Fprintln(x.Err, "Loading source application zip")
	file, err := downloadCH1Archive(x, c, sOrg, sEnv, sApp)
	if err != nil {
		return err
	}
	fmt.Fprintln(x.Err, "Copying source application to target")
	created, err := c.DeployCloudHubApp(x.ctx, tOrg, tEnv, d, false, file)
	if err != nil {
		return err
	}
	defer func() { // undo a half-done copy
		if err != nil {
			_ = c.DeleteCloudHubApp(x.ctx, tOrg, tEnv, dst.app)
		}
	}()
	if sApp.StaticIPsEnabled && len(sApp.IPAddresses) > 0 {
		fmt.Fprintln(x.Err, "Allocating static IPs for target application")
		for _, ip := range sApp.IPAddresses {
			if err := c.AllocateCloudHubStaticIP(x.ctx, tOrg, tEnv, dst.app, ip.RegionID); err != nil {
				return err
			}
		}
	}
	status := "stop"
	if sApp.Status == "STARTED" || sApp.Status == "DEPLOYING" {
		status = "start"
	}
	fmt.Fprintf(x.Err, "Updating target application state: %s\n", status)
	already, err := setCH1Status(x, c, tOrg, tEnv, dst.app, status)
	if err != nil {
		return err
	}
	if already {
		fmt.Fprintln(x.Err, "Application already in this state")
	}
	return x.Print(created, func() { fmt.Fprintf(x.Out, "Application %s copied to %s\n", src.app, dst.app) })
}

func cmdCH1CopyReplace(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	src, sOrg, sEnv, err := ch1Ref(x, c, org, env, pos[0])
	if err != nil {
		return err
	}
	dst, tOrg, tEnv, err := ch1Ref(x, c, org, env, pos[1])
	if err != nil {
		return err
	}
	fmt.Fprintln(x.Err, "Loading source application")
	sApp, err := c.GetCloudHubApp(x.ctx, sOrg, sEnv, src.app)
	if err != nil {
		return fmt.Errorf("source app: %w", err)
	}
	tApp, err := c.GetCloudHubApp(x.ctx, tOrg, tEnv, dst.app)
	if err != nil {
		return fmt.Errorf("target app: %w", err)
	}
	fmt.Fprintln(x.Err, "Loading source application zip")
	file, err := downloadCH1Archive(x, c, sOrg, sEnv, sApp)
	if err != nil {
		return err
	}
	d, err := cleanAppData(tApp, dst.app)
	if err != nil {
		return err
	}
	d["fileName"] = sApp.FileName
	fmt.Fprintln(x.Err, "Copying source application to target")
	a, err := c.UpdateCloudHubApp(x.ctx, tOrg, tEnv, dst.app, d, anypoint.CloudHubUpdate{File: &file, TargetInfo: true})
	if err != nil {
		return err
	}
	return x.Print(a, func() { fmt.Fprintf(x.Out, "Updating application %s\n", cmp(a.Domain, dst.app)) })
}

// ---------------------------------------------------------------- CloudHub 1.0 alerts

func cmdCloudHubAlertList(x *Ctx, args []string) error {
	fs := x.Flags()
	limit := fs.Int("limit", 10, "number of results")
	offset := fs.Int("offset", 0, "results to skip")
	_, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	alerts, err := c.ListCloudHubAlerts(x.ctx, org, env, *limit, *offset)
	if err != nil {
		return err
	}
	return x.Print(alerts, func() {
		t := newTable(x.Out, "NAME", "ID", "TYPE", "SEVERITY", "SOURCE", "SOURCE TYPE", "ENABLED", "UPDATED")
		for _, a := range alerts {
			t.row(a.Name, a.ID, a.Condition.Type, a.Severity, strings.Join(a.Condition.Resources, ", "), a.Condition.ResourceType,
				yesNo(a.Enabled), agoAny(a.LastModified))
		}
		t.flush()
	})
}

func cmdCloudHubAlertHistory(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := required(pos, "NAME"); err != nil {
		return err
	}
	alerts, err := c.ListCloudHubAlerts(x.ctx, org, env, 1000, 0)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(alerts, func(a anypoint.CloudHubAlert) bool { return a.Name == pos[0] })
	if i < 0 {
		return fmt.Errorf("alert not found with name %q", pos[0])
	}
	history, err := c.GetCloudHubAlertHistory(x.ctx, org, env, alerts[i].ID)
	if err != nil {
		return err
	}
	if history == nil {
		history = []anypoint.AlertTrigger{}
	}
	return x.Print(history, func() {
		if len(history) == 0 {
			fmt.Fprintln(x.Out, "This alert has no history")
			return
		}
		t := newTable(x.Out, "TRIGGERED AT", "EVENT", "CONTEXT")
		for _, h := range history {
			b, _ := json.Marshal(h.Context)
			t.row(timeOf(h.TriggeredAt).Format("Mon, Jan 2, 2006 3:04 PM"), "trigger", string(b))
		}
		t.flush()
	})
}

// ---------------------------------------------------------------- CloudHub 2.0 / RTF applications

// ch2Flags are the settings deploy and modify share.
type ch2Flags struct {
	fs                                                                    *flag.FlagSet
	assetVersion, groupID, replicas, replicaSize, instanceType, strategy  *string
	channel, java, propsFile, scopeCfg, scopeFile, pathRewrite, endpoints *string
	props, secure                                                         *multiFlag
	osV2, clustered, lastMile, noAMLogs, fwdSSL                           *optBool
}

func newCH2Flags(fs *flag.FlagSet) *ch2Flags {
	return &ch2Flags{fs: fs,
		assetVersion: fs.String("assetVersion", "", "Exchange asset `version` (default: the latest)"),
		groupID:      fs.String("groupId", "", "Exchange group `ID` (default: the organization)"),
		replicas:     fs.String("replicas", "", "number of `replicas` (default 1)"),
		replicaSize:  fs.String("replicaSize", "", "vCores per replica, e.g. 0.1 (default 0.1; not with usage-based pricing)"),
		instanceType: fs.String("instanceType", "", "instance `type` with usage-based pricing, e.g. mule.micro"),
		strategy:     fs.String("updateStrategy", "", "`strategy`: rolling or recreate (default rolling)"),
		channel:      fs.String("releaseChannel", "", "runtime release `channel`"),
		java:         fs.String("javaVersion", "", "Java `version`"),
		props:        multi(fs, "property", "property `name:value` (repeatable)"),
		secure:       multi(fs, "secureProperty", "secure property `name:value` (repeatable)"),
		propsFile:    fs.String("propertiesFile", "", "`file` of name:value properties, one per line"),
		scopeCfg:     fs.String("scopeLoggingConfig", "", "log levels as `scope:LEVEL,...` (DEBUG, INFO, WARN, ERROR)"),
		scopeFile:    fs.String("scopeLoggingConfigFile", "", "`file` with one {scope: name, logLevel: LEVEL} per line"),
		pathRewrite:  fs.String("pathRewrite", "", "base `path` the application receives requests under"),
		endpoints:    fs.String("publicEndpoints", "", "public endpoint `URL`"),
		osV2:         newOptBool(fs, "objectStoreV2", "use Object Store v2"),
		clustered:    newOptBool(fs, "clustered", "run the replicas as a cluster (needs 2 or more)"),
		lastMile:     newOptBool(fs, "lastMileSecurity", "use TLS between the ingress and the application"),
		noAMLogs:     newOptBool(fs, "disableAmLogForwarding", "do not forward logs to Anypoint Monitoring"),
		fwdSSL:       newOptBool(fs, "forwardSslSession", "forward the client TLS session"),
	}
}

func (f *ch2Flags) replicaCount() (int, error) {
	if *f.replicas == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(*f.replicas)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid replicas %q", *f.replicas)
	}
	return n, nil
}

// properties builds the mule.agent.application.properties.service values.
func (f *ch2Flags) properties(set map[string]bool, svc map[string]any) error {
	var err error
	switch {
	case set["propertiesFile"]:
		lines, lerr := fileLines(*f.propsFile)
		if lerr != nil {
			return lerr
		}
		svc["properties"], err = parseProperties(lines, nil)
	case set["property"]:
		svc["properties"], err = parseProperties(*f.props, nil)
	}
	if err != nil {
		return err
	}
	if set["secureProperty"] {
		svc["secureProperties"], err = parseProperties(*f.secure, nil)
	}
	return err
}

func dropEmpty(m map[string]any) map[string]any {
	for k, v := range m {
		if v == "" || v == nil {
			delete(m, k)
		}
	}
	return m
}

func ch2Status(d *anypoint.Deployment) string {
	if d.DeploymentUpdateStatus == "DEPLOY_FAILED" && d.Status == "STARTED" {
		return "STARTED (Update failed)"
	}
	return d.Status
}

// specs lists a deployment's spec versions (desired first) and the reasons
// replicas give for not running the desired one.
func specs(d *anypoint.Deployment) ([]string, []string) {
	var out, seen, errs []string
	for _, r := range d.Replicas {
		v := r.CurrentDeploymentVersion
		if slices.Contains(seen, v) {
			continue
		}
		switch v {
		case d.LastSuccessfulVersion:
			out = append(out, v+" (LAST SUCCESSFUL)")
		case d.DesiredVersion:
			out = append([]string{v + " (DESIRED)"}, out...)
		}
		seen = append(seen, v)
		if r.Reason != "" && !slices.Contains(errs, r.Reason) {
			errs = append(errs, r.Reason)
		}
	}
	if len(out) == 0 {
		out = seen
		if d.DesiredVersion != "" {
			out = []string{d.DesiredVersion + " (DESIRED)"}
		}
	}
	return out, errs
}

func propLines(v any) string {
	m, _ := v.(map[string]any)
	var out []string
	for _, k := range sortedKeys(m) {
		out = append(out, fmt.Sprintf("%s : %v", k, m[k]))
	}
	return strings.Join(out, ",\n")
}

func showCH2App(x *Ctx, d *anypoint.Deployment) error {
	return x.Print(d, func() {
		ds := d.Target.DeploymentSettings
		runtime, _, _ := strings.Cut(ds.RuntimeVersion, ":")
		kv := []string{"Name", d.Name, "App Id", d.ID, "Status", ch2Status(d), "Updated", agoAny(d.LastModifiedDate), "Runtime", runtime}
		if ds.Runtime != nil {
			kv = append(kv, "Release Channel", ds.Runtime.ReleaseChannel, "Java Version", ds.Runtime.Java)
		}
		kv = append(kv, "Target", d.Target.TargetID, "Artifact Id", d.Application.Ref.ArtifactID, "Replicas", strconv.Itoa(d.Target.Replicas))
		if ds.InstanceType != "" {
			kv = append(kv, "Instance type", ds.InstanceType)
		} else if d.Application.VCores != "" {
			kv = append(kv, "vCores per replica", d.Application.VCores.String())
		}
		sp, errs := specs(d)
		kv = append(kv, "Update Strategy", ds.UpdateStrategy, "Specs", strings.Join(sp, ",\n"))
		if u, ok := ds.HTTP.Inbound.PublicURL.(string); ok && u != "" {
			kv = append(kv, "Public Endpoint", u)
		}
		svc, _ := d.Application.Configuration["mule.agent.application.properties.service"].(map[string]any)
		kv = append(kv, "Properties", propLines(svc["properties"]), "Secure Properties", propLines(svc["secureProperties"]))
		if len(errs) > 0 {
			kv = append(kv, "Desired config not applied, 1 or more replicas", strings.Join(errs, ",\n"))
		}
		printKV(x.Out, kv...)
	})
}

func cmdCH2List(x *Ctx, args []string) error {
	_, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	deps, err := c.ListDeployments(x.ctx, org, env)
	if err != nil {
		return err
	}
	if deps == nil {
		deps = []anypoint.Deployment{}
	}
	slices.Reverse(deps)
	return x.Print(deps, func() {
		t := newTable(x.Out, "APP NAME", "STATUS", "TARGET", "UPDATED", "APP ID")
		for i := range deps {
			d := &deps[i]
			t.row(d.Name, ch2Status(d), d.Target.TargetID, agoAny(d.LastModifiedDate), d.ID)
		}
		t.flush()
	})
}

func cmdCH2Describe(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	d, err := c.GetDeployment(x.ctx, org, env, pos[0])
	if err != nil {
		return err
	}
	return showCH2App(x, d)
}

func cmdCH2Delete(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := c.DeleteDeployment(x.ctx, org, env, pos[0]); err != nil {
		return err
	}
	return x.Done("%s deleted", pos[0])
}

func cmdCH2State(state string) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		pos, c, org, env, err := apiSetup(x, x.Flags(), args)
		if err != nil {
			return err
		}
		err = c.SetDeploymentState(x.ctx, org, env, pos[0], state)
		switch {
		case anypoint.IsStatus(err, 304):
			return errors.New("application already in this state")
		case err != nil:
			return fmt.Errorf("changing app state with app id %s errored: %w", pos[0], err)
		}
		return x.Done("OK, %s", state)
	}
}

func cmdCH2Deploy(x *Ctx, args []string) error {
	fs := x.Flags()
	f := newCH2Flags(fs)
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	set := setFlags(fs)
	name, targetID, runtimeVersion, artifactID := pos[0], pos[1], pos[2], pos[3]
	replicas, err := f.replicaCount()
	if err != nil {
		return err
	}
	clustered, _ := f.clustered.get(set)
	if clustered && replicas < 2 {
		return errors.New("requires at least 2 replicas to run in cluster mode")
	}
	if replicas == 0 {
		replicas = 1
	}
	if err := oneOf("updateStrategy", *f.strategy, "rolling", "recreate"); err != nil {
		return err
	}
	svc := map[string]any{"applicationName": name, "properties": map[string]any{}, "secureProperties": map[string]any{}}
	if err := f.properties(set, svc); err != nil {
		return err
	}
	scopes := []any{}
	if set["scopeLoggingConfigFile"] {
		if scopes, err = scopeLoggingFile(x, *f.scopeFile, true); err != nil {
			return err
		}
	}
	if set["scopeLoggingConfig"] {
		more, err := scopeLogging(*f.scopeCfg)
		if err != nil {
			return err
		}
		scopes = append(scopes, more...)
	}
	ubp, err := c.UsageBasedPricing(x.ctx, org)
	if err != nil {
		return err
	}
	target, err := c.GetFabricTarget(x.ctx, org, targetID)
	if err != nil {
		return err
	}
	rtf := target.Type == "runtime-fabric"
	ref := anypoint.Doc{"artifactId": artifactID, "groupId": cmp(*f.groupID, org), "packaging": "jar", "version": *f.assetVersion}
	if *f.assetVersion == "" {
		v, err := c.LatestAssetVersion(x.ctx, cmp(*f.groupID, org), artifactID)
		if err != nil {
			return err
		}
		ref["version"] = v
		fmt.Fprintf(x.Err, "Using latest exchange app (%s) version as default version: v%s\n", artifactID, v)
	}
	var osV2 any
	if v, _ := f.osV2.get(set); v {
		osV2 = true
	}
	app := anypoint.Doc{"desiredState": "STARTED", "assets": []any{}, "ref": ref,
		"configuration": anypoint.Doc{"mule.agent.application.properties.service": svc,
			"mule.agent.logging.service": anypoint.Doc{"scopeLoggingConfigurations": scopes}},
		"integrations": anypoint.Doc{"services": anypoint.Doc{"objectStoreV2": anypoint.Doc{"enabled": osV2}}}}
	lastMile, _ := f.lastMile.get(set)
	fwdSSL, _ := f.fwdSSL.get(set)
	noAMLogs, _ := f.noAMLogs.get(set)
	var publicURL, pathRewrite any
	if *f.endpoints != "" {
		publicURL = *f.endpoints
	}
	if *f.pathRewrite != "" {
		pathRewrite = *f.pathRewrite
	}
	settings := anypoint.Doc{"clustered": clustered, "disableAmLogForwarding": noAMLogs, "enforceDeployingReplicasAcrossNodes": false,
		"jvm": anypoint.Doc{}, "runtime": dropEmpty(map[string]any{"version": runtimeVersion, "releaseChannel": *f.channel, "java": *f.java}),
		"runtimeVersion": runtimeVersion, "updateStrategy": cmp(*f.strategy, "rolling"),
		"http": anypoint.Doc{"inbound": anypoint.Doc{"publicUrl": publicURL, "pathRewrite": pathRewrite,
			"lastMileSecurity": lastMile, "forwardSslSession": fwdSSL}},
		"generateDefaultPublicUrl": strings.Contains(*f.endpoints, "cloudhub.io")}
	switch {
	case rtf:
	case ubp:
		settings["instanceType"] = cmp(*f.instanceType, "mule.micro")
	default:
		app["vCores"] = cmp(*f.replicaSize, "0.1")
	}
	body := anypoint.Doc{"name": name, "labels": []any{}, "application": app,
		"target": anypoint.Doc{"provider": "MC", "targetId": targetID, "replicas": replicas, "deploymentSettings": settings}}
	fmt.Fprintf(x.Err, "Deploying %s ...\n", name)
	d, err := c.CreateDeployment(x.ctx, org, env, body)
	if err != nil {
		return err
	}
	return showCH2App(x, d)
}

// vcoresInstanceType maps vCores to the usage-based pricing instance type.
var vcoresInstanceType = map[string]string{"0.1": "mule.micro", "0.2": "mule.small", "0.5": "mule.medium", "1": "mule.large",
	"1.5": "mule.xlarge", "2": "mule.xlarge.mem", "3": "mule.2xlarge", "3.5": "mule.2xlarge", "4": "mule.2xlarge"}

func cmdCH2Modify(x *Ctx, args []string) error {
	fs := x.Flags()
	f := newCH2Flags(fs)
	runtimeVersion := fs.String("runtimeVersion", "", "Mule runtime `version`")
	artifactID := fs.String("artifactId", "", "Exchange asset `ID` to deploy instead")
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	set := setFlags(fs)
	replicas, err := f.replicaCount()
	if err != nil {
		return err
	}
	if err := oneOf("updateStrategy", *f.strategy, "rolling", "recreate"); err != nil {
		return err
	}
	cur, err := c.GetDeployment(x.ctx, org, env, pos[0])
	if err != nil {
		return err
	}
	ubp, err := c.UsageBasedPricing(x.ctx, org)
	if err != nil {
		return err
	}
	instanceType := *f.instanceType
	if ubp {
		if *f.replicaSize != "" {
			return errors.New("cannot modify vCores while in usage based pricing (UBP)")
		}
		if cur.Application.VCores != "" && cur.Target.DeploymentSettings.InstanceType == "" && instanceType == "" {
			if it, ok := vcoresInstanceType[cur.Application.VCores.String()]; ok {
				instanceType = it
				fmt.Fprintf(x.Err, "Converting vCores to instance type for UBP compatibility: %s\n", it)
			}
		}
	}
	app := anypoint.Doc{}
	if *f.assetVersion != "" || *artifactID != "" {
		ref := anypoint.Doc{"artifactId": cmp(*artifactID, cur.Application.Ref.ArtifactID), "groupId": cmp(*f.groupID, org),
			"packaging": "jar", "version": *f.assetVersion}
		if *f.assetVersion == "" {
			v, err := c.LatestAssetVersion(x.ctx, ref["groupId"].(string), *artifactID)
			if err != nil {
				return err
			}
			ref["version"] = v
			fmt.Fprintf(x.Err, "Using latest exchange app version as default version: v%s\n", v)
		}
		app["ref"] = ref
	}
	if v, ok := f.osV2.get(set); ok {
		app["integrations"] = anypoint.Doc{"services": anypoint.Doc{"objectStoreV2": anypoint.Doc{"enabled": v}}}
	}
	svc := map[string]any{}
	if err := f.properties(set, svc); err != nil {
		return err
	}
	conf := anypoint.Doc{"mule.agent.application.properties.service": svc}
	switch {
	case set["scopeLoggingConfig"]:
		scopes, err := scopeLogging(*f.scopeCfg)
		if err != nil {
			return err
		}
		conf["mule.agent.logging.service"] = anypoint.Doc{"scopeLoggingConfigurations": scopes}
	case set["scopeLoggingConfigFile"]:
		scopes, err := scopeLoggingFile(x, *f.scopeFile, false)
		if err != nil {
			return err
		}
		conf["mule.agent.logging.service"] = anypoint.Doc{"scopeLoggingConfigurations": scopes}
	}
	app["configuration"] = conf
	settings := anypoint.Doc{"runtime": dropEmpty(map[string]any{"releaseChannel": *f.channel, "java": *f.java})}
	for key, o := range map[string]*optBool{"clustered": f.clustered, "disableAmLogForwarding": f.noAMLogs} {
		if v, ok := o.get(set); ok {
			settings[key] = v
		}
	}
	if *runtimeVersion != "" {
		settings["runtimeVersion"] = *runtimeVersion
	}
	if *f.strategy != "" {
		settings["updateStrategy"] = *f.strategy
	}
	if instanceType != "" {
		settings["instanceType"] = instanceType
	} else if *f.replicaSize != "" {
		app["vCores"] = *f.replicaSize
	}
	inbound := anypoint.Doc{}
	if set["publicEndpoints"] {
		inbound["publicUrl"] = *f.endpoints
	}
	if set["pathRewrite"] {
		inbound["pathRewrite"] = *f.pathRewrite
	}
	for key, o := range map[string]*optBool{"lastMileSecurity": f.lastMile, "forwardSslSession": f.fwdSSL} {
		if v, ok := o.get(set); ok {
			inbound[key] = v
		}
	}
	settings["http"] = anypoint.Doc{"inbound": inbound}
	target := anypoint.Doc{"deploymentSettings": settings}
	if replicas > 0 {
		target["replicas"] = replicas
	}
	fmt.Fprintf(x.Err, "Modifying %s ...\n", pos[0])
	d, err := c.UpdateDeployment(x.ctx, org, env, pos[0], anypoint.Doc{"application": app, "target": target})
	if err != nil {
		return err
	}
	return showCH2App(x, d)
}

func cmdCH2Logs(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	var latest int64
	var docID string
	return tail(x, func() error {
		logs, err := c.ListDeploymentLogs(x.ctx, org, env, pos[0], pos[1], docID)
		if err != nil {
			return err
		}
		for _, l := range logs {
			if l.Timestamp > latest {
				fmt.Fprintf(x.Out, "%s - [%s] %s - %s %s\n", l.LogLevel, time.UnixMilli(l.Timestamp).Format("15:04:05 2 Jan"),
					l.Context.Logger, l.Message, l.Context.Event)
				latest = l.Timestamp
			}
		}
		if len(logs) > 0 {
			docID = logs[len(logs)-1].DocID
		}
		return nil
	})
}

func cmdCH2DownloadLogs(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	fmt.Fprintln(x.Err, "Downloading logs...")
	b, err := c.DownloadDeploymentLogs(x.ctx, org, env, pos[0], pos[1])
	if err != nil {
		return err
	}
	files := map[string][]byte{}
	if len(b) > 0 {
		files[safeFileName(pos[0])+".txt"] = b
	}
	dst := filepath.Join(pos[2], safeFileName(pos[0]+"-"+time.Now().UTC().Format("2006_01_02")+".zip"))
	if err := writeZip(dst, files); err != nil {
		return err
	}
	return x.Done("Saved to %s", dst)
}
