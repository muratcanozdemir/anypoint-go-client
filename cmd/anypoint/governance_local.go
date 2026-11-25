package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	acv "github.com/aml-org/amf-custom-validator/pkg"
	"gopkg.in/yaml.v3"

	"github.com/muratcanozdemir/anypoint-go-client/anypoint"
)

// The governance commands that run locally. As in anypoint-cli-v4, an API
// project is parsed and checked by AMF and its rulesets are applied by the
// AMF custom validator. Here AMF runs on the JVM (tooling_java.go) and the
// validator, which is written in Go, is linked in. The ruleset dialect and
// the authoring metadata come from the plugin's npm packages, pinned like
// the dx-mule jars.

var (
	governancePackage = npmPackage{Name: "mulesoft-anypoint-cli-governance-plugin", Version: "1.1.5",
		Integrity: "sha512-M8fc4Oq18jKu6HVXzVgrqPMcWC+jrTzaWJ7MIs/zE7FOGX34TD1wvi4TTC1omiiuFHb6KmHj4kGunR/8a5VKrw=="}
	profileDialectPackage = npmPackage{Name: "@aml-org/amf-validation-profile-dialect", Version: "1.6.0",
		Integrity: "sha512-hPz3mnexj8B07qqCsWk87ZZYTZSpnTgBdqylqs0NXDYc3opHL2hYf/36kCxz7cMT/SZZkSJ3oDOb2DH1K1v+TA=="}
)

// govResources is the plugin's resources directory (models, schema, aliases).
func govResources(x *Ctx) (string, error) {
	dir, err := governancePackage.unpack(x)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "src", "resources"), nil
}

func profileDialect(x *Ctx) (string, error) {
	dir, err := profileDialectPackage.unpack(x)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "validation-profile.yaml"), nil
}

// ---------------------------------------------------------------- Exchange assets

// extractGav reads group/asset/version (optionally gav://...).
func extractGav(s string) (anypoint.AssetRef, error) {
	p := strings.Split(strings.TrimPrefix(s, "gav://"), "/")
	if len(p) != 3 || slices.Contains(p, "") {
		return anypoint.AssetRef{}, fmt.Errorf("can not resolve remote asset %s: it should have the format group/asset/version", s)
	}
	return anypoint.AssetRef{GroupID: p[0], AssetID: p[1], Version: p[2]}, nil
}

var governedClassifiers = []string{"oas", "raml", "raml-fragment", "evented-api", "semantic-extension", "ruleset", "graphql", "oas-components"}

// assetDepsDir is where downloaded assets are kept, shared with
// anypoint-cli-v4: $ANYPOINT_HOME/.anypoint_deps or ~/.anypoint_deps.
func assetDepsDir() (string, error) {
	base := os.Getenv("ANYPOINT_HOME")
	if base == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = h
	}
	return filepath.Join(base, ".anypoint_deps"), nil
}

// obtainAsset downloads an API, fragment or ruleset asset (once, unless
// forced or in development) and returns its directory and main file.
func obtainAsset(x *Ctx, c *anypoint.Client, ref anypoint.AssetRef, force bool) (dir, main string, err error) {
	a, err := c.GetAssetV2(x.ctx, ref)
	if err != nil {
		return "", "", fmt.Errorf("asset %s/%s/%s: %w", ref.GroupID, ref.AssetID, ref.Version, err)
	}
	var file *anypoint.AssetFile
	for i, f := range a.Files {
		if !f.IsGenerated && slices.Contains(governedClassifiers, f.Classifier) &&
			(f.Packaging == "zip" || f.Packaging == "yaml" && f.Classifier == "ruleset") {
			file = &a.Files[i]
			break
		}
	}
	if file == nil {
		return "", "", fmt.Errorf("asset %s/%s/%s has no valid file to download", ref.GroupID, ref.AssetID, ref.Version)
	}
	status := cmp(a.Status, "Stable")
	root, err := assetDepsDir()
	if err != nil {
		return "", "", err
	}
	dir, err = safeJoin(root, filepath.Join(ref.GroupID, ref.AssetID, ref.Version, status))
	if err != nil {
		return "", "", err
	}
	main = file.MainFile
	if a.Status == "development" {
		force = true
	}
	if !force && hasVisibleEntries(dir) {
		return dir, main, nil
	}
	link := file.ExternalLink
	if link == "" {
		link = file.DownloadURL
	}
	data, err := c.FetchExternal(x.ctx, link)
	if err != nil {
		return "", "", err
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", "", err
	}
	if file.Packaging == "yaml" {
		dst, err := safeJoin(dir, cmp(main, ref.AssetID+".yaml"))
		if err != nil {
			return "", "", err
		}
		return dir, main, writeEntry(dst, bytes.NewReader(data), 0o600)
	}
	return dir, main, unzipBytes(data, dir)
}

func hasVisibleEntries(dir string) bool {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			return true
		}
	}
	return false
}

func unzipBytes(data []byte, dir string) error {
	tmp, err := os.CreateTemp("", "anypoint-asset-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_, err = extractZip(tmp.Name(), dir)
	return err
}

// projectFolder returns a project directory for a directory or a zip; the
// cleanup removes what it extracted.
func projectFolder(p string) (string, func(), error) {
	st, err := os.Stat(p)
	if err != nil {
		return "", nil, fmt.Errorf("no such path %s", p)
	}
	if st.IsDir() {
		abs, err := filepath.Abs(p)
		return abs, func() {}, err
	}
	dir, err := os.MkdirTemp("", "anypoint-project-")
	if err != nil {
		return "", nil, err
	}
	if _, err := extractZip(p, dir); err != nil {
		os.RemoveAll(dir)
		return "", nil, fmt.Errorf("%s is not a project directory or zip: %w", p, err)
	}
	return dir, func() { os.RemoveAll(dir) }, nil
}

type govDescriptor struct {
	Main         string `json:"main"`
	Classifier   string `json:"classifier"`
	Dependencies []struct {
		GroupID string `json:"groupId"`
		AssetID string `json:"assetId"`
		Version string `json:"version"`
	} `json:"dependencies"`
}

func readDescriptor(dir string) (*govDescriptor, error) {
	b, err := os.ReadFile(filepath.Join(dir, "exchange.json"))
	if err != nil {
		return nil, errors.New("exchange.json file must be present at top level of the project zip or folder")
	}
	var d govDescriptor
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("exchange.json: %w", err)
	}
	return &d, nil
}

// copyTree copies a project so dependencies can be added beside it.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		f, err := os.OpenInRoot(src, rel) // stays inside src even if the tree changes underneath
		if err != nil {
			return err
		}
		defer f.Close()
		return writeEntry(target, f, 0o600)
	})
}

// rulesetFile resolves a ruleset argument (a .yaml file, or a ruleset
// project directory or zip) to its YAML file.
func rulesetFile(p string) (string, func(), error) {
	ext := filepath.Ext(p)
	st, err := os.Stat(p)
	switch {
	case err != nil && ext != ".yaml" && ext != ".zip" && ext != "":
		return "", nil, fmt.Errorf("argument %s %s is not a supported type (project directory, project zip or yaml file)", p, ext)
	case err != nil:
		return "", nil, fmt.Errorf("no such file or directory %s", p)
	case !st.IsDir() && ext != ".yaml" && ext != ".zip":
		return "", nil, fmt.Errorf("argument %s %s is not a supported type (project directory, project zip or yaml file)", p, ext)
	case ext == ".yaml" && !st.IsDir():
		return p, func() {}, nil
	}
	dir, cleanup, err := projectFolder(p)
	if err != nil {
		return "", nil, err
	}
	d, err := readDescriptor(dir)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return filepath.Join(dir, d.Main), cleanup, nil
}

// resolveRuleset fetches a remote ruleset (group/asset/version) when asked.
func resolveRuleset(x *Ctx, remote bool, ruleset string) (string, func(), error) {
	if !remote {
		return rulesetFile(ruleset)
	}
	ref, err := extractGav(ruleset)
	if err != nil {
		return "", nil, err
	}
	c, err := x.Client()
	if err != nil {
		return "", nil, err
	}
	dir, main, err := obtainAsset(x, c, ref, false)
	if err != nil {
		return "", nil, err
	}
	if _, err := os.Stat(filepath.Join(dir, "exchange.json")); err == nil {
		return rulesetFile(dir)
	}
	if main == "" {
		return "", nil, fmt.Errorf("ruleset asset %s has no main file", ruleset)
	}
	return filepath.Join(dir, main), func() {}, nil
}

// ---------------------------------------------------------------- reports

// govResult is one finding, as anypoint-cli-v4 reports it.
type govResult struct {
	ValidationID   string `json:"validationId"`
	SeverityLevel  string `json:"severityLevel"`
	Message        string `json:"message"`
	TargetNode     string `json:"targetNode"`
	TargetProperty string `json:"targetProperty,omitempty"`
	Position       string `json:"position,omitempty"`
	Location       string `json:"location,omitempty"`
}

const amlPrefix = "http://a.ml/"

// opaResults reads the custom validator's JSON-LD report.
func opaResults(report string) (bool, []govResult, error) {
	var doc []struct {
		Encodes []struct {
			Conforms bool `json:"conforms"`
			Result   []struct {
				FocusNode       string      `json:"focusNode"`
				ResultMessage   string      `json:"resultMessage"`
				ResultSeverity  string      `json:"resultSeverity"`
				SourceShapeName string      `json:"sourceShapeName"`
				Location        *opaLoc     `json:"location"`
				Trace           []opaTraceT `json:"trace"`
			} `json:"result"`
		} `json:"doc:encodes"`
	}
	if err := json.Unmarshal([]byte(report), &doc); err != nil || len(doc) == 0 || len(doc[0].Encodes) == 0 {
		return false, nil, fmt.Errorf("unexpected validation report: %.200s", report)
	}
	enc := doc[0].Encodes[0]
	var out []govResult
	for _, r := range enc.Result {
		g := govResult{ValidationID: r.SourceShapeName, Message: r.ResultMessage, TargetNode: r.FocusNode,
			SeverityLevel: strings.TrimPrefix(r.ResultSeverity, "http://www.w3.org/ns/shacl#")}
		loc := r.Location
		if len(r.Trace) > 0 {
			g.TargetProperty = r.Trace[0].ResultPath
			if loc == nil {
				loc = r.Trace[0].Location
			}
		}
		if loc != nil {
			g.Position = loc.String()
			g.Location = loc.URI
		}
		out = append(out, g)
	}
	return enc.Conforms, out, nil
}

type opaTraceT struct {
	ResultPath string  `json:"resultPath"`
	Location   *opaLoc `json:"location"`
}

type opaLoc struct {
	URI   string `json:"uri"`
	Range *struct {
		RawStart struct {
			Line   int `json:"line"`
			Column int `json:"column"`
		} `json:"start"`
		RawEnd struct {
			Line   int `json:"line"`
			Column int `json:"column"`
		} `json:"end"`
	} `json:"range"`
}

// String renders a range as AMF does: [(line,column)-(line,column)].
func (l *opaLoc) String() string {
	if l == nil || l.Range == nil {
		return ""
	}
	return fmt.Sprintf("[(%d,%d)-(%d,%d)]", l.Range.RawStart.Line, l.Range.RawStart.Column, l.Range.RawEnd.Line, l.Range.RawEnd.Column)
}

var severityRank = map[string]int{"Violation": 0, "Warning": 1, "Info": 2}

func sortResults(rs []govResult) {
	sort.SliceStable(rs, func(i, j int) bool {
		ai, aj := strings.HasPrefix(rs[i].ValidationID, amlPrefix), strings.HasPrefix(rs[j].ValidationID, amlPrefix)
		if ai != aj {
			return ai
		}
		ri, ok := severityRank[rs[i].SeverityLevel]
		if !ok {
			ri = 3
		}
		rj, ok := severityRank[rs[j].SeverityLevel]
		if !ok {
			rj = 3
		}
		return ri < rj
	})
}

// printResults writes findings as anypoint-cli-v4 does: AMF's own
// (functional) findings first, then the rulesets' (conformance) findings.
func printResults(w io.Writer, rs []govResult) {
	section := ""
	for _, r := range rs {
		s := "conformance"
		if strings.HasPrefix(r.ValidationID, amlPrefix) {
			s = "functional"
		}
		if s != section {
			if s == "functional" {
				fmt.Fprint(w, "\nFunctional Validations\n______________________\n\n")
			} else {
				fmt.Fprint(w, "\n\nConformance Validations\n_______________________\n\n")
			}
			section = s
		}
		fmt.Fprintf(w, "\nConstraint: %s\nSeverity: %s\nMessage: %s\nTarget: %s\n", r.ValidationID, r.SeverityLevel, r.Message, r.TargetNode)
		if r.TargetProperty != "" {
			fmt.Fprintf(w, "Property: %s\n", r.TargetProperty)
		}
		if r.Position != "" {
			fmt.Fprintf(w, "Range: %s\n", r.Position)
		}
		if r.Location != "" {
			fmt.Fprintf(w, "Location: %s\n", r.Location)
		}
	}
}

// ---------------------------------------------------------------- api:validate

type amfParseResult struct {
	Spec     string      `json:"spec"`
	Conforms bool        `json:"conforms"`
	Rendered bool        `json:"rendered"`
	Results  []govResult `json:"results"`
}

func cmdGovAPIValidate(x *Ctx, args []string) error {
	fs := x.Flags()
	rulesets := multi(fs, "rulesets", "ruleset `file` (yaml, project directory or zip) to apply (repeatable)")
	remoteRulesets := multi(fs, "remote-rulesets", "Exchange ruleset `group/asset/version` to apply (repeatable)")
	remote := fs.Bool("remote", false, "SPEC is an Exchange asset group/asset/version")
	force := fs.Bool("force", false, "download Exchange assets again")
	pos, err := x.Parse(fs, args)
	if err != nil {
		return err
	}
	var c *anypoint.Client
	client := func() (*anypoint.Client, error) {
		if c == nil {
			var err error
			if c, err = x.Client(); err != nil {
				return nil, err
			}
		}
		return c, nil
	}
	location := pos[0]
	if *remote {
		ref, err := extractGav(location)
		if err != nil {
			return err
		}
		cl, err := client()
		if err != nil {
			return err
		}
		if location, _, err = obtainAsset(x, cl, ref, *force); err != nil {
			return err
		}
	}
	folder, cleanup, err := projectFolder(location)
	if err != nil {
		return err
	}
	defer cleanup()
	desc, err := readDescriptor(folder)
	if err != nil {
		return err
	}
	if desc.Main == "" {
		return errors.New("exchange.json does not name the main file (main)")
	}
	work, err := os.MkdirTemp("", "anypoint-validate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err := copyTree(folder, work); err != nil {
		return err
	}
	// dependencies (fragments, libraries) go to exchange_modules, as Exchange and API Designer lay them out
	var depErrors []string
	for _, d := range desc.Dependencies {
		dst := filepath.Join(work, "exchange_modules", d.GroupID, d.AssetID, d.Version)
		if hasVisibleEntries(dst) {
			continue
		}
		cl, err := client()
		if err == nil {
			var dir string
			if dir, _, err = obtainAsset(x, cl, anypoint.AssetRef{GroupID: d.GroupID, AssetID: d.AssetID, Version: d.Version}, *force); err == nil {
				err = copyTree(dir, dst)
			}
		}
		if err != nil {
			depErrors = append(depErrors, fmt.Sprintf("dependency %s/%s/%s: %v", d.GroupID, d.AssetID, d.Version, err))
		}
	}
	if len(depErrors) > 0 {
		fmt.Fprintf(x.Err, "Errors Found Before Validation:\nNumber of Errors:%d\nErrors:\n%s\n\nValidation will continue ignoring pre-validation errors\n\n",
			len(depErrors), strings.Join(depErrors, "\n"))
	}
	var files []string
	for _, r := range *rulesets {
		f, cleanup, err := rulesetFile(r)
		if err != nil {
			return err
		}
		defer cleanup()
		files = append(files, f)
	}
	for _, r := range *remoteRulesets {
		f, cleanup, err := resolveRuleset(x, true, r)
		if err != nil {
			return err
		}
		defer cleanup()
		files = append(files, f)
	}
	mainURL, err := fileURL(filepath.Join(work, desc.Main))
	if err != nil {
		return err
	}
	jsonld := filepath.Join(work, ".anypoint-model.jsonld")
	var parsed amfParseResult
	if err := runAMF(x, &parsed, "parse", mainURL, jsonld); err != nil {
		return err
	}
	results := parsed.Results
	conforms := parsed.Conforms
	if parsed.Rendered && len(files) > 0 {
		model, err := os.ReadFile(jsonld)
		if err != nil {
			return err
		}
		for _, f := range files {
			profile, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			report, err := acv.Validate(string(profile), string(model), false, nil)
			if err != nil {
				return fmt.Errorf("ruleset %s: %w", f, err)
			}
			ok, rs, err := opaResults(report)
			if err != nil {
				return fmt.Errorf("ruleset %s: %w", f, err)
			}
			for i := range rs { // the validator knows positions, not files: they are in the main file
				if rs[i].Location == "" || strings.HasPrefix(rs[i].Location, "amf://") {
					rs[i].Location = mainURL
				}
			}
			conforms = conforms && ok
			results = append(results, rs...)
		}
	} else if !parsed.Rendered && len(files) > 0 {
		fmt.Fprintln(x.Err, "The API does not conform to its specification, so the rulesets were not applied.")
	}
	work = strings.TrimSuffix(filepath.ToSlash(work), "/")
	for i := range results { // report paths in the project, not in the working copy
		for _, s := range []*string{&results[i].TargetNode, &results[i].Location} {
			*s = strings.ReplaceAll(*s, "file://"+work, "file://"+filepath.ToSlash(folder))
		}
	}
	sortResults(results)
	return x.Print(map[string]any{"conforms": conforms, "spec": parsed.Spec, "results": results}, func() {
		fmt.Fprintf(x.Out, "Conforms: %t\n", conforms)
		if len(results) > 0 {
			fmt.Fprintf(x.Out, "Number of results: %d\n", len(results))
		}
		printResults(x.Out, results)
	})
}

// ---------------------------------------------------------------- api:inspect

type amfDeclarations struct {
	Conforms     bool        `json:"conforms"`
	Results      []govResult `json:"results"`
	Declarations []struct {
		ID         string         `json:"id"`
		Name       string         `json:"name"`
		NodeShape  bool           `json:"nodeShape"`
		Properties []declProperty `json:"properties"`
	} `json:"declarations"`
}

func declarationsOf(x *Ctx, file string) (*amfDeclarations, error) {
	u, err := fileURL(file)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(file); err != nil {
		return nil, fmt.Errorf("no such file %s", file)
	}
	var d amfDeclarations
	return &d, runAMF(x, &d, "declarations", u)
}

// typeName is how inspect shows a declaration and --types picks it: v4
// shows the id's last segment, which under AMF 5 ids (amf://id#N) is no
// longer the name, so this prefers the declared name.
func declName(name, id string) string {
	if name != "" {
		return name
	}
	return id[strings.LastIndex(id, "/")+1:]
}

func cmdGovAPIInspect(x *Ctx, args []string) error {
	pos, err := x.Parse(x.Flags(), args)
	if err != nil {
		return err
	}
	d, err := declarationsOf(x, pos[0])
	if err != nil {
		return err
	}
	var types []string
	for _, dec := range d.Declarations {
		types = append(types, declName(dec.Name, dec.ID))
	}
	return x.Print(types, func() {
		for _, t := range types {
			fmt.Fprintln(x.Out, t)
		}
	})
}

// ---------------------------------------------------------------- ruleset:init

func cmdGovRulesetInit(x *Ctx, args []string) error {
	fs := x.Flags()
	types := multi(fs, "types", "API `type` to write rules for (repeatable; default: every declared type)")
	name := fs.String("name", "my-ruleset", "ruleset `name`")
	pos, err := x.Parse(fs, args)
	if err != nil {
		return err
	}
	d, err := declarationsOf(x, pos[0])
	if err != nil {
		return err
	}
	var rules []scaffoldRule
	for _, dec := range d.Declarations {
		if len(*types) > 0 && !slices.Contains(*types, declName(dec.Name, dec.ID)) {
			continue
		}
		if !dec.NodeShape { // v4 writes an empty, invalid "then" for these
			continue
		}
		rules = append(rules, scaffoldFor(dec.Name, dec.NodeShape, dec.Properties))
	}
	if len(rules) == 0 {
		return fmt.Errorf("could not find a target for: %s", strings.Join(*types, ","))
	}
	fmt.Fprint(x.Out, composeRuleset(*name, rules))
	return nil
}

type scaffoldRule struct{ name, text string }

type declProperty = struct {
	Name     string `json:"name"`
	MinCount int    `json:"minCount"`
}

// scaffoldFor writes the rule anypoint-cli-v4 derives from a declared type:
// shapes named like it may only have its properties, and must have the
// required ones. (v4's own output is mis-indented YAML when a type has
// required properties; this writes the same rule, well-formed.)
func scaffoldFor(name string, nodeShape bool, props []declProperty) scaffoldRule {
	var lines []string
	line := func(indent int, s string) { lines = append(lines, strings.Repeat(" ", indent)+s) }
	rule := scaffoldRuleName(name)
	line(0, rule+":")
	line(2, `message: Schema for resource of type " + name + " is not valid`)
	line(2, "targetClass: shacl.NodeShape")
	line(2, "if:")
	line(4, "propertyConstraints:")
	line(6, "shacl.name:")
	line(8, "pattern: "+name)
	line(2, "then:")
	if !nodeShape {
		return scaffoldRule{rule, strings.Join(lines, "\n") + "\n"}
	}
	var names, required []string
	for _, p := range props {
		names = append(names, p.Name)
		if p.MinCount > 0 {
			required = append(required, p.Name)
		}
	}
	allowed := func(indent int, first string) {
		line(indent, first+"propertyConstraints:")
		line(indent+len(first)+2, "shacl.property/shacl.name:")
		line(indent+len(first)+4, "in:")
		for _, n := range names {
			line(indent+len(first)+6, "- "+n)
		}
	}
	if len(required) == 0 {
		allowed(4, "")
		return scaffoldRule{rule, strings.Join(lines, "\n") + "\n"}
	}
	line(4, "and:")
	for _, r := range required {
		line(6, "- propertyConstraints:")
		line(10, "shacl.property:")
		line(12, "atLeast:")
		line(14, "count: 1")
		line(14, "validation:")
		line(16, "propertyConstraints:")
		line(18, "shacl.name:")
		line(20, "pattern: "+r)
	}
	allowed(6, "- ")
	return scaffoldRule{rule, strings.Join(lines, "\n") + "\n"}
}

func scaffoldRuleName(name string) string {
	s := strings.Join(strings.Fields(name), "-")
	s = strings.ReplaceAll(s, ".", "-")
	var out []rune
	r := []rune(s)
	for i, c := range r {
		out = append(out, c)
		if i+1 < len(r) && isLetter(c) && r[i+1] >= 'A' && r[i+1] <= 'Z' {
			out = append(out, '-')
		}
	}
	return strings.ToLower(string(out)) + "-shape"
}

func isLetter(c rune) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// composeRuleset writes the scaffold. v4 also writes "version: 1.0.0",
// which neither the dialect nor the ruleset schema allows, so its output
// fails governance:ruleset:validate; this leaves it out.
func composeRuleset(profile string, rules []scaffoldRule) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#%%Validation Profile 1.0\n\nprofile: %s\n\nviolation:\n", profile)
	for _, r := range rules {
		fmt.Fprintf(&b, "  - %s\n", r.name)
	}
	b.WriteString("\nvalidations:\n")
	for _, r := range rules {
		for l := range strings.SplitSeq(strings.TrimSuffix(r.text, "\n"), "\n") {
			b.WriteString("  " + l + "\n")
		}
	}
	return b.String()
}

// ---------------------------------------------------------------- ruleset:validate, info

type amfDialectResult struct {
	Conforms    bool        `json:"conforms"`
	ParseErrors int         `json:"parseErrors"`
	Results     []govResult `json:"results"`
}

// checkRuleset validates a ruleset against the validation profile dialect.
func checkRuleset(x *Ctx, file string) (*amfDialectResult, error) {
	dialect, err := profileDialect(x)
	if err != nil {
		return nil, err
	}
	du, err := fileURL(dialect)
	if err != nil {
		return nil, err
	}
	fu, err := fileURL(file)
	if err != nil {
		return nil, err
	}
	var r amfDialectResult
	if err := runAMF(x, &r, "dialect", du, fu); err != nil {
		return nil, fmt.Errorf("file (%s) can not be parsed as a ruleset: %w", file, err)
	}
	return &r, nil
}

func conformsLine(ok bool) string {
	if ok {
		return "Ruleset conforms with Dialect"
	}
	return "Ruleset does not conform with Dialect"
}

func cmdGovRulesetValidate(x *Ctx, args []string) error {
	pos, err := x.Parse(x.Flags(), args)
	if err != nil {
		return err
	}
	fmt.Fprintln(x.Err, "Ruleset", pos[0])
	file, cleanup, err := rulesetFile(pos[0])
	if err != nil {
		return err
	}
	defer cleanup()
	r, err := checkRuleset(x, file)
	if err != nil {
		return err
	}
	return x.Print(r, func() {
		if r.ParseErrors > 0 {
			fmt.Fprintf(x.Out, "Parsing errors found: %d\n", r.ParseErrors)
		}
		fmt.Fprintln(x.Out, conformsLine(r.Conforms))
		if !r.Conforms {
			printResults(x.Out, r.Results)
		}
	})
}

// rulesetDoc is the parts of a ruleset the CLI reads itself.
type rulesetDoc struct {
	Profile     string   `yaml:"profile"`
	Description string   `yaml:"description"`
	Violation   []string `yaml:"violation"`
	Warning     []string `yaml:"warning"`
	Info        []string `yaml:"info"`
}

func cmdGovRulesetInfo(x *Ctx, args []string) error {
	fs := x.Flags()
	remote := fs.Bool("remote", false, "RULESET is an Exchange asset group/asset/version")
	pos, err := x.Parse(fs, args)
	if err != nil {
		return err
	}
	fmt.Fprintln(x.Err, "Ruleset", pos[0])
	file, cleanup, err := resolveRuleset(x, *remote, pos[0])
	if err != nil {
		return err
	}
	defer cleanup()
	r, err := checkRuleset(x, file)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var doc rulesetDoc
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("file (%s) can not be parsed as a ruleset: %w", file, err)
	}
	type rule struct {
		Severity string `json:"severity"`
		Rule     string `json:"rule"`
	}
	var rules []rule
	for _, s := range []struct {
		name  string
		names []string
	}{{"Violation", doc.Violation}, {"Info", doc.Info}, {"Warning", doc.Warning}} {
		for _, n := range s.names {
			rules = append(rules, rule{s.name, n})
		}
	}
	return x.Print(map[string]any{"conforms": r.Conforms, "rules": rules}, func() {
		if r.ParseErrors > 0 {
			fmt.Fprintf(x.Out, "Parsing errors found: %d\n", r.ParseErrors)
		}
		fmt.Fprintln(x.Out, conformsLine(r.Conforms))
		t := newTable(x.Out, "SEVERITY", "RULE")
		for _, r := range rules {
			t.row(r.Severity, r.Rule)
		}
		t.flush()
	})
}

func cmdGovRulesetVersion(x *Ctx, args []string) error {
	if _, err := x.Parse(x.Flags(), args); err != nil {
		return err
	}
	v := map[string]string{"governance-ruleset-tools": governancePackage.Version, "amf-custom-validator": "1.7.7",
		"amf": amfVersion(), "validation-profile-dialect": profileDialectPackage.Version}
	return x.Print(v, func() {
		fmt.Fprintf(x.Out, "governance-ruleset-tools v%s (AMF %s, AMF custom validator 1.7.7)\n", governancePackage.Version, amfVersion())
	})
}

func amfVersion() string {
	for l := range strings.SplitSeq(amfLock, "\n") {
		if _, rest, ok := strings.Cut(l, "/amf-cli_2.12/"); ok {
			v, _, _ := strings.Cut(rest, "/")
			return v
		}
	}
	return "unknown"
}

func cmdGovUpdate(x *Ctx, args []string) error {
	if _, err := x.Parse(x.Flags(), args); err != nil {
		return err
	}
	return x.Done("The governance tooling is pinned to v%s (AMF %s) in this build; update anypoint to update it.", governancePackage.Version, amfVersion())
}

// ---------------------------------------------------------------- ruleset:clone

func cmdGovRulesetClone(x *Ctx, args []string) error {
	fs := x.Flags()
	remote := fs.Bool("remote", false, "RULESET is an Exchange asset group/asset/version")
	toError := fs.String("error", "", "comma-separated `rules` to make violations")
	toWarning := fs.String("warning", "", "comma-separated `rules` to make warnings")
	toInfo := fs.String("info", "", "comma-separated `rules` to make info")
	remove := fs.String("remove", "", "comma-separated `rules` to remove")
	pos, err := x.Parse(fs, args)
	if err != nil {
		return err
	}
	list := func(s string) []string {
		var out []string
		for v := range strings.SplitSeq(s, ",") {
			if v = strings.Trim(strings.TrimSpace(v), `"'`); v != "" {
				out = append(out, v)
			}
		}
		return out
	}
	moves := []struct {
		key   string
		rules []string
	}{{"violation", list(*toError)}, {"warning", list(*toWarning)}, {"info", list(*toInfo)}}
	removed := list(*remove)
	for _, m := range moves {
		for _, r := range m.rules {
			if slices.Contains(removed, r) {
				return errors.New("can not move and remove the same rule")
			}
		}
	}
	file, cleanup, err := resolveRuleset(x, *remote, pos[0])
	if err != nil {
		return err
	}
	defer cleanup()
	r, err := checkRuleset(x, file)
	if err != nil {
		return err
	}
	if r.ParseErrors > 0 {
		fmt.Fprintf(x.Err, "Parsing errors found: %d\n", r.ParseErrors)
	}
	if !r.Conforms {
		fmt.Fprintln(x.Err, conformsLine(false))
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	out, err := cloneRuleset(b, pos[1], pos[2], moves, removed)
	if err != nil {
		return err
	}
	fmt.Fprint(x.Out, out)
	return nil
}

// cloneRuleset renames a ruleset and moves or removes rules. Removed rules
// leave both the severity lists and validations (v4 meant to drop the
// validation too, but wrote it under a misspelled property).
func cloneRuleset(src []byte, title, description string, moves []struct {
	key   string
	rules []string
}, removed []string) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return "", err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return "", errors.New("the ruleset is not a YAML map")
	}
	root := doc.Content[0]
	seq := func(key string) *yaml.Node {
		if v := mapValue(root, key); v != nil && v.Kind == yaml.SequenceNode {
			return v
		}
		return nil
	}
	dropFrom := func(key, rule string) bool {
		s := seq(key)
		if s == nil {
			return false
		}
		for i, n := range s.Content {
			if n.Value == rule {
				s.Content = append(s.Content[:i], s.Content[i+1:]...)
				return true
			}
		}
		return false
	}
	var missing []string
	for _, rule := range removed {
		found := false
		for _, k := range []string{"violation", "warning", "info"} {
			found = dropFrom(k, rule) || found
		}
		if v := mapValue(root, "validations"); v != nil {
			found = deleteKey(v, rule) || found
		}
		if !found {
			missing = append(missing, rule)
		}
	}
	for _, m := range moves {
		for _, rule := range m.rules {
			found := false
			for _, k := range []string{"violation", "warning", "info"} {
				if dropFrom(k, rule) {
					found = true
					break
				}
			}
			if !found {
				missing = append(missing, rule)
				continue
			}
			target := seq(m.key)
			if target == nil {
				target = &yaml.Node{Kind: yaml.SequenceNode}
				root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: m.key}, target)
			}
			target.Content = append(target.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: rule})
		}
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("rule %s does not exist", strings.Join(missing, ", "))
	}
	for _, k := range []string{"violation", "warning", "info"} {
		if s := seq(k); s != nil && len(s.Content) == 0 {
			deleteKey(root, k)
		}
	}
	if v := mapValue(root, "validations"); v != nil && len(v.Content) == 0 {
		deleteKey(root, "validations")
	}
	setScalar(root, "profile", title)
	setScalar(root, "description", description)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return "", err
	}
	return "#%Validation Profile 1.0\n" + buf.String(), nil
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func deleteKey(m *yaml.Node, key string) bool {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return true
		}
	}
	return false
}

func setScalar(m *yaml.Node, key, value string) {
	if v := mapValue(m, key); v != nil {
		*v = yaml.Node{Kind: yaml.ScalarNode, Value: value}
		return
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, &yaml.Node{Kind: yaml.ScalarNode, Value: value})
}

// ---------------------------------------------------------------- document

func cmdGovDocument(x *Ctx, args []string) error {
	fs := x.Flags()
	single := fs.Bool("single-page", false, "write the documentation as a single page")
	pos, err := x.Parse(fs, args)
	if err != nil {
		return err
	}
	file, out := pos[0], pos[1]
	r, err := checkRuleset(x, file)
	if err != nil {
		return err
	}
	if !r.Conforms {
		fmt.Fprintln(x.Err, conformsLine(false))
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	pages, err := documentRuleset(b, *single, func(name string) { fmt.Fprintln(x.Err, "validation name", name) })
	if err != nil {
		return err
	}
	fmt.Fprintln(x.Err, "Saving to", out)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range pages {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: p.name, Method: zip.Deflate, Modified: time.Now()})
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(p.text)); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o600); err != nil {
		return err
	}
	return x.Done("Documentation written to %s", out)
}

type docPage struct{ name, text string }

// documentRuleset renders the Exchange documentation pages of a ruleset
// with anypoint-cli-v4's templates.
func documentRuleset(src []byte, single bool, onRule func(string)) ([]docPage, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, errors.New("empty ruleset")
	}
	root := doc.Content[0]
	var head rulesetDoc
	if err := root.Decode(&head); err != nil {
		return nil, err
	}
	link := func(name string) string {
		if single {
			return "(#" + name + ")"
		}
		return "(pages/" + name + ")"
	}
	var home strings.Builder
	fmt.Fprintf(&home, "\n# %s\n%s\n\n## Rules in this Ruleset\n\n", head.Profile, head.Description)
	for _, s := range []struct {
		title string
		names []string
	}{{"Violations", head.Violation}, {"Warnings", head.Warning}, {"Info", head.Info}} {
		if len(s.names) == 0 {
			continue
		}
		fmt.Fprintf(&home, "\n### %s\n\n------\n\n", s.title)
		for _, n := range s.names {
			fmt.Fprintf(&home, "* [%s]%s\n", n, link(n))
		}
	}
	var pages []docPage
	var all strings.Builder
	names := []string{"home"}
	if v := mapValue(root, "validations"); v != nil {
		for i := 0; i+1 < len(v.Content); i += 2 {
			name := v.Content[i].Value
			onRule(name)
			page := documentRule(name, v.Content[i+1], single)
			names = append(names, name)
			if single {
				all.WriteString(page)
			} else {
				pages = append(pages, docPage{name + ".md", page})
			}
		}
	}
	text := home.String()
	if single {
		text += all.String()
	} else {
		var cfg strings.Builder
		cfg.WriteString("{ \n\"pageOrder\": [")
		for i, n := range names {
			if i > 0 {
				cfg.WriteString(", ")
			}
			cfg.WriteString(strconv.Quote(n))
		}
		cfg.WriteString("] \n}")
		pages = append(pages, docPage{"config.json", cfg.String()})
	}
	return append(pages, docPage{"home.md", text}), nil
}

func documentRule(name string, v *yaml.Node, single bool) string {
	str := func(key string) (string, bool) {
		n := mapValue(v, key)
		if n == nil {
			return "", false
		}
		return n.Value, true
	}
	message, hasMessage := str("message")
	documentation, hasDoc := str("documentation")
	targetClass, _ := str("targetClass")
	modelElement := targetClass[strings.Index(targetClass, ".")+1:]
	var b strings.Builder
	if single {
		fmt.Fprintf(&b, "\n[general](#%s) > %s\n\n------\n# %s\n\n", name, name, name)
	} else {
		fmt.Fprintf(&b, "\n[general](pages/home) > %s\n\n------\n\n", name)
	}
	if !hasDoc || !hasMessage {
		fmt.Fprintf(&b, "## Guidance / Message\n%s\n%s\n\n", documentation, renderMessagePlaceholders(message))
	} else {
		fmt.Fprintf(&b, "## Guidance\n%s\n\n## Message\n%s\n\n", documentation, renderMessagePlaceholders(message))
	}
	if ex := mapValue(v, "examples"); ex != nil && ex.Kind == yaml.MappingNode && len(ex.Content) > 0 {
		b.WriteString("## Examples\n")
		for i := 0; i+1 < len(ex.Content); i += 2 {
			fmt.Fprintf(&b, "### %s\n```\n%s\n```\n ", ex.Content[i].Value, ex.Content[i+1].Value)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\n> Applies to <a href=\"https://github.com/aml-org/amf/blob/develop/documentation/model.md#%s\" target=\"_blank\">%s</a>\n\n### Constraint\n\n\n", modelElement, modelElement)
	kind := "Declarative Validation"
	if mapValue(v, "rego") != nil || mapValue(v, "regoModule") != nil {
		kind = "Rego Validation"
	}
	fmt.Fprintf(&b, "##### Type: %s \n", kind)
	return b.String()
}

// renderMessagePlaceholders shows {{core.name}} as *name*: generated docs
// have no element to substitute.
func renderMessagePlaceholders(text string) string {
	var out strings.Builder
	pos := 0
	for pos < len(text) {
		start := strings.Index(text[pos:], "{{")
		if start < 0 {
			break
		}
		start += pos
		end := strings.Index(text[start+2:], "}}")
		path := ""
		if end >= 0 {
			end += start + 2
			path = strings.TrimSpace(text[start+2 : end])
		}
		if end >= 0 && isPlaceholderPath(path) {
			out.WriteString(text[pos:start] + "*" + path[strings.LastIndex(path, ".")+1:] + "*")
			pos = end + 2
			continue
		}
		out.WriteString(text[pos : start+1])
		pos = start + 1
	}
	return out.String() + text[pos:]
}

func isPlaceholderPath(p string) bool {
	for seg := range strings.SplitSeq(p, ".") {
		if seg == "" {
			return false
		}
		for _, c := range seg {
			if !isLetter(c) && (c < '0' || c > '9') && c != '_' && c != '$' && c != '-' {
				return false
			}
		}
	}
	return true
}
