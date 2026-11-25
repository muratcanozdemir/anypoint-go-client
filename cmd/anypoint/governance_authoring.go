package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Ruleset-authoring aids (governance:ruleset:classes, properties, domains,
// constraints, resolve, generate, simplify, completions and
// validate-authoring), ported from anypoint-cli-v4's governance plugin.
// They read the plugin's model metadata and ruleset schema.

const validationProfileHeader = "#%Validation Profile 1.0"

func loadGovModel(x *Ctx) (*amfModel, string, error) {
	res, err := govResources(x)
	if err != nil {
		return nil, "", err
	}
	m, err := loadAMFModel(res)
	return m, res, err
}

func cmdGovRulesetDomains(x *Ctx, args []string) error {
	if _, err := x.Parse(x.Flags(), args); err != nil {
		return err
	}
	m, _, err := loadGovModel(x)
	if err != nil {
		return err
	}
	ds := m.sortedDomains()
	return x.Print(ds, func() {
		for _, d := range ds {
			fmt.Fprintf(x.Out, "%s\n  Description: %s\n  Classes: %d\n  Prefixes: %s\n", d.Name, d.Description, d.ClassCount, strings.Join(d.Prefixes, ", "))
			if d.SpecKind != "" {
				fmt.Fprintf(x.Out, "  Spec Kind: %s\n", d.SpecKind)
			}
			fmt.Fprintln(x.Out)
		}
	})
}

func cmdGovRulesetClasses(x *Ctx, args []string) error {
	fs := x.Flags()
	domain := fs.String("domain", "", "only the classes of this `domain` (api-spec, mcp, api-project, ...)")
	if _, err := x.Parse(fs, args); err != nil {
		return err
	}
	m, _, err := loadGovModel(x)
	if err != nil {
		return err
	}
	uris := m.targetableClasses()
	prefixes := m.prefixes(nil)
	grouped := map[string][]string{}
	var order []string
	for _, uri := range uris {
		for _, p := range prefixes.keys {
			if rest, ok := strings.CutPrefix(uri, prefixes.ns[p]); ok {
				if _, seen := grouped[p]; !seen {
					order = append(order, p)
				}
				grouped[p] = append(grouped[p], p+"."+rest)
			}
		}
	}
	var show []string
	if *domain != "" {
		var names []string
		var found *modelDomain
		for _, d := range m.sortedDomains() {
			names = append(names, d.Name)
			if d.Name == *domain {
				found = &d
			}
		}
		if found == nil {
			return fmt.Errorf("unknown domain: %q. Available: %s", *domain, strings.Join(names, ", "))
		}
		for _, p := range order {
			if slices.Contains(found.Prefixes, p) {
				show = append(show, p)
			}
		}
	} else {
		show = slices.Clone(order)
		sort.Strings(show)
	}
	out := map[string][]string{}
	for _, p := range show {
		cls := slices.Clone(grouped[p])
		sort.Strings(cls)
		out[p] = cls
	}
	return x.Print(out, func() {
		for _, p := range show {
			fmt.Fprintf(x.Out, "%s:\n", p)
			for _, c := range out[p] {
				fmt.Fprintf(x.Out, "  %s\n", c)
			}
		}
		fmt.Fprintf(x.Out, "\nTotal: %d targetable classes\n", len(uris))
	})
}

func cmdGovRulesetProperties(x *Ctx, args []string) error {
	pos, err := x.Parse(x.Flags(), args)
	if err != nil {
		return err
	}
	m, _, err := loadGovModel(x)
	if err != nil {
		return err
	}
	class := pos[0]
	if !m.isValidClass(class, nil) {
		return fmt.Errorf("unknown class: %q. Use \"governance:ruleset:classes\" to see available classes", class)
	}
	props := m.prefixedProperties(class, nil)
	return x.Print(props, func() {
		fmt.Fprintf(x.Out, "Properties for %s (%d total):\n\n", class, len(props))
		for _, p := range props {
			r := ""
			if p.Range != "" {
				r = " → " + p.Range
			}
			fmt.Fprintf(x.Out, "  %s (%s)%s\n", p.Prefixed, p.kind(), r)
		}
	})
}

func cmdGovRulesetConstraints(x *Ctx, args []string) error {
	fs := x.Flags()
	kind := fs.String("type", "", "only this property `type`: scalar, node, scalarArray or nodeArray")
	if _, err := x.Parse(fs, args); err != nil {
		return err
	}
	if *kind != "" {
		list, _, ok := constraintsFor(*kind)
		if !ok {
			return fmt.Errorf("unknown type: %q. Valid types: scalar, node, scalarArray, nodeArray", *kind)
		}
		return x.Print(list, func() {
			fmt.Fprintf(x.Out, "Constraints for %s:\n\n", *kind)
			for _, c := range list {
				fmt.Fprintf(x.Out, "  %s\n", c)
			}
		})
	}
	all := map[string][]string{}
	for _, c := range constraintsByType {
		all[c.kind] = c.list
	}
	return x.Print(all, func() {
		for _, c := range constraintsByType {
			fmt.Fprintf(x.Out, "%s:\n", c.kind)
			for _, v := range c.list {
				fmt.Fprintf(x.Out, "  %s\n", v)
			}
			fmt.Fprintln(x.Out)
		}
	})
}

func cmdGovRulesetResolve(x *Ctx, args []string) error {
	pos, err := x.Parse(x.Flags(), args)
	if err != nil {
		return err
	}
	res, err := govResources(x)
	if err != nil {
		return err
	}
	var data struct {
		Aliases []struct {
			Alias       string `json:"alias"`
			TargetClass string `json:"targetClass"`
			Path        string `json:"path"`
			Description string `json:"description,omitempty"`
		} `json:"aliases"`
	}
	b, err := os.ReadFile(res + "/aliases.json")
	if err == nil {
		err = json.Unmarshal(b, &data)
	}
	if err != nil {
		return errors.New("could not load alias data")
	}
	norm := func(s string) string {
		return strings.NewReplacer(" ", "", "_", "", "-", "", "\t", "").Replace(strings.ToLower(s))
	}
	needle := norm(pos[0])
	matches := data.Aliases[:0:0]
	for _, a := range data.Aliases {
		if n := norm(a.Alias); strings.Contains(n, needle) || strings.Contains(needle, n) {
			matches = append(matches, a)
		}
	}
	return x.Print(matches, func() {
		if len(matches) == 0 {
			fmt.Fprintf(x.Out, "No alias matches for %q.\nUse \"governance:ruleset:classes\" + \"governance:ruleset:properties\" to discover the canonical property name.\n", pos[0])
			return
		}
		fmt.Fprintf(x.Out, "Matches for %q:\n\n", pos[0])
		for _, m := range matches {
			fmt.Fprintf(x.Out, "  Alias: %s\n  Target Class: %s\n  Path: %s\n", m.Alias, m.TargetClass, m.Path)
			if m.Description != "" {
				fmt.Fprintf(x.Out, "  Description: %s\n", m.Description)
			}
			fmt.Fprintln(x.Out)
		}
	})
}

func cmdGovRulesetGenerate(x *Ctx, args []string) error {
	fs := x.Flags()
	spec := fs.String("api-spec", "", "API spec `file` for context")
	attempt := fs.Int("attempt", 1, "retry attempt `number`")
	pos, err := x.Parse(fs, args)
	if err != nil {
		return err
	}
	specType := "unknown"
	if *spec != "" {
		if b, err := os.ReadFile(*spec); err == nil {
			// v4 tests the first 500 characters as a set of characters, so
			// it never detects a type; this checks the text as meant
			head := string(b[:min(500, len(b))])
			switch {
			case strings.Contains(head, "#%RAML"):
				specType = "raml"
			case strings.Contains(head, "openapi:") || strings.Contains(head, `"openapi"`) || strings.Contains(head, "swagger:"):
				specType = "openapi"
			}
		}
	}
	w := x.Out
	fmt.Fprintf(w, "Generation Instructions:\n\nGenerate a governance ruleset in Validation Profile 1.0 format (YAML) that implements:\n  Intent: %s\n", pos[0])
	if specType != "unknown" {
		fmt.Fprintf(w, "  Spec Type: %s\n", specType)
	}
	fmt.Fprint(w, "\nCRITICAL REQUIREMENTS:\n"+
		"1. Start with: #%Validation Profile 1.0\n"+
		"2. Use valid target classes (run governance:ruleset:classes to list)\n"+
		"3. Use valid property paths with correct prefixes (run governance:ruleset:properties <class>)\n"+
		"4. Property constraints MUST include at least one validation field\n"+
		"5. Rules MUST be assigned to a severity level: violation, warning, or info\n"+
		"6. Validate with governance:ruleset:validate-authoring after writing\n")
	if *attempt > 1 {
		fmt.Fprintf(w, "\nThis is retry attempt #%d. Fix errors from previous attempt.\n", *attempt)
	}
	return nil
}

// ---------------------------------------------------------------- YAML positions

type textIndex struct {
	text  string
	lines []int // byte offset of each line start
}

func newTextIndex(text string) *textIndex {
	t := &textIndex{text: text, lines: []int{0}}
	for i := range len(text) {
		if text[i] == '\n' {
			t.lines = append(t.lines, i+1)
		}
	}
	return t
}

// offset of a yaml.v3 position (1-based line and column, in characters).
func (t *textIndex) offset(line, col int) int {
	if line < 1 || line > len(t.lines) {
		return len(t.text)
	}
	off := t.lines[line-1]
	for c := 1; c < col && off < len(t.text) && t.text[off] != '\n'; c++ {
		_, n := utf8.DecodeRuneInString(t.text[off:])
		off += n
	}
	return off
}

func (t *textIndex) lineText(line int) string {
	ls := strings.Split(t.text, "\n")
	if line < len(ls) {
		return ls[line]
	}
	return ""
}

// end is the offset after a node's value.
func (t *textIndex) end(n *yaml.Node) int {
	switch n.Kind {
	case yaml.DocumentNode, yaml.MappingNode, yaml.SequenceNode:
		if len(n.Content) == 0 {
			return t.offset(n.Line, n.Column) + 2
		}
		return t.end(n.Content[len(n.Content)-1])
	case yaml.AliasNode:
		return t.offset(n.Line, n.Column) + 1 + len(n.Value)
	}
	start := t.offset(n.Line, n.Column)
	if n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		end := t.lines[min(n.Line-1+strings.Count(n.Value, "\n")+1, len(t.lines)-1)]
		return max(end-1, start)
	}
	rest := t.text[start:]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[:i]
	}
	if n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) == 0 {
		if i := strings.Index(rest, " #"); i >= 0 {
			rest = rest[:i]
		}
	}
	return start + len(strings.TrimRight(rest, " \t\r"))
}

func isNullScalar(n *yaml.Node) bool {
	return n == nil || n.Kind == yaml.ScalarNode && n.Tag == "!!null" && n.Value == ""
}

// locate finds a path's node (or its key) and returns its position, falling
// back to its parent as the plugin does.
func locate(doc *yaml.Node, t *textIndex, path []string, preferKey bool) (int, int) {
	if doc == nil || len(doc.Content) == 0 {
		return 0, 0
	}
	cur := doc.Content[0]
	var key *yaml.Node
	for _, part := range path {
		if cur == nil {
			break
		}
		switch cur.Kind {
		case yaml.MappingNode:
			var next *yaml.Node
			key = nil
			for i := 0; i+1 < len(cur.Content); i += 2 {
				if cur.Content[i].Value == part {
					key, next = cur.Content[i], cur.Content[i+1]
					break
				}
			}
			cur = next
		case yaml.SequenceNode:
			i, err := strconv.Atoi(part)
			key = nil
			if err == nil && i >= 0 && i < len(cur.Content) {
				cur = cur.Content[i]
			} else {
				cur = nil
			}
		default:
			cur = nil
		}
	}
	if preferKey && key != nil {
		return key.Line - 1, key.Column - 1
	}
	if cur != nil && !isNullScalar(cur) {
		return cur.Line - 1, cur.Column - 1
	}
	if len(path) > 0 {
		return locate(doc, t, path[:len(path)-1], preferKey)
	}
	return 0, 0
}

// yamlToAny converts a node to JSON-like values (maps unordered).
func yamlToAny(n *yaml.Node) any {
	var v any
	_ = n.Decode(&v)
	return normalizeYAML(v)
}

func normalizeYAML(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			t[k] = normalizeYAML(e)
		}
		return t
	case map[any]any:
		m := map[string]any{}
		for k, e := range t {
			m[fmt.Sprint(k)] = normalizeYAML(e)
		}
		return m
	case []any:
		for i, e := range t {
			t[i] = normalizeYAML(e)
		}
	case int:
		return float64(t)
	}
	return v
}

// ---------------------------------------------------------------- JSON Schema (the subset ruleset.schema.json uses)

type schemaError struct {
	path    string // JSON pointer of the instance
	message string
}

type jsonSchema struct {
	root map[string]any
}

func (s *jsonSchema) validate(v any, sch map[string]any, path string) []schemaError {
	if ref, ok := sch["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/definitions/")
		defs, _ := s.root["definitions"].(map[string]any)
		target, _ := defs[name].(map[string]any)
		if target == nil {
			return nil
		}
		return s.validate(v, target, path)
	}
	where := path
	if where == "" {
		where = "root"
	}
	var errs []schemaError
	if t, ok := sch["type"]; ok && !typeMatches(v, t) {
		return []schemaError{{path, fmt.Sprintf("Invalid type at %s: expected %s", where, schemaTypeName(t))}}
	}
	if e, ok := sch["enum"].([]any); ok && !slices.ContainsFunc(e, func(x any) bool { return fmt.Sprint(x) == fmt.Sprint(v) }) {
		var vals []string
		for _, x := range e {
			vals = append(vals, fmt.Sprint(x))
		}
		errs = append(errs, schemaError{path, fmt.Sprintf("Value at %s must be one of: %s", where, strings.Join(vals, ", "))})
	}
	for _, sub := range anyList(sch["allOf"]) {
		errs = append(errs, s.validate(v, sub, path)...)
	}
	if subs := anyList(sch["oneOf"]); len(subs) > 0 {
		var branch []schemaError
		matched := 0
		for _, sub := range subs {
			e := s.validate(v, sub, path)
			if len(e) == 0 {
				matched++
			}
			branch = append(branch, e...)
		}
		switch {
		case matched == 0:
			errs = append(errs, branch...)
			errs = append(errs, schemaError{path, fmt.Sprintf("Validation error at %s: must match exactly one schema in oneOf", where)})
		case matched > 1:
			errs = append(errs, schemaError{path, fmt.Sprintf("Validation error at %s: must match exactly one schema in oneOf", where)})
		}
	}
	switch val := v.(type) {
	case map[string]any:
		for _, r := range anyStrings(sch["required"]) {
			if _, ok := val[r]; !ok {
				errs = append(errs, schemaError{path, "Missing required property: " + r})
			}
		}
		props, _ := sch["properties"].(map[string]any)
		patterns, _ := sch["patternProperties"].(map[string]any)
		for _, k := range sortedKeys(val) {
			p := path + "/" + strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
			matched := false
			if ps, ok := props[k].(map[string]any); ok {
				matched = true
				errs = append(errs, s.validate(val[k], ps, p)...)
			}
			for pat, ps := range patterns {
				if re, err := regexp.Compile(pat); err == nil && re.MatchString(k) {
					matched = true
					if m, ok := ps.(map[string]any); ok {
						errs = append(errs, s.validate(val[k], m, p)...)
					}
				}
			}
			if matched {
				continue
			}
			switch ap := sch["additionalProperties"].(type) {
			case bool:
				if !ap {
					errs = append(errs, schemaError{path, "Additional property not allowed: " + k})
				}
			case map[string]any:
				errs = append(errs, s.validate(val[k], ap, p)...)
			}
		}
	case []any:
		if mi, ok := sch["minItems"].(float64); ok && float64(len(val)) < mi {
			errs = append(errs, schemaError{path, fmt.Sprintf("Validation error at %s: must NOT have fewer than %d items", where, int(mi))})
		}
		if it, ok := sch["items"].(map[string]any); ok {
			for i, e := range val {
				errs = append(errs, s.validate(e, it, path+"/"+strconv.Itoa(i))...)
			}
		}
	case string:
		if ml, ok := sch["minLength"].(float64); ok && float64(utf8.RuneCountInString(val)) < ml {
			errs = append(errs, schemaError{path, fmt.Sprintf("Validation error at %s: must NOT have fewer than %d characters", where, int(ml))})
		}
	case float64:
		if mn, ok := sch["minimum"].(float64); ok && val < mn {
			errs = append(errs, schemaError{path, fmt.Sprintf("Validation error at %s: must be >= %v", where, mn)})
		}
	}
	return errs
}

func anyList(v any) []map[string]any {
	var out []map[string]any
	for _, e := range asSlice(v) {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func anyStrings(v any) []string {
	var out []string
	for _, e := range asSlice(v) {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func schemaTypeName(t any) string {
	if l, ok := t.([]any); ok {
		var s []string
		for _, e := range l {
			s = append(s, fmt.Sprint(e))
		}
		return strings.Join(s, ",")
	}
	return fmt.Sprint(t)
}

func typeMatches(v any, t any) bool {
	if l, ok := t.([]any); ok {
		return slices.ContainsFunc(l, func(e any) bool { return typeMatches(v, e) })
	}
	switch t {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == math.Trunc(f)
	case "null":
		return v == nil
	}
	return true
}

// ---------------------------------------------------------------- validate-authoring

type diagnostic struct {
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	EndLine  int    `json:"endLine"`
	EndCol   int    `json:"endColumn"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
	Source   string `json:"source"`
}

const (
	govValidatorSource = "Governance Validator"
	govModelSource     = "Governance Model Validator"
)

func diagAt(t *textIndex, line, col int, msg, sev, src string) diagnostic {
	return diagnostic{Line: line, Column: col, EndLine: line, EndCol: utf8.RuneCountInString(t.lineText(line)), Message: msg, Severity: sev, Source: src}
}

var yamlErrLine = regexp.MustCompile(`line (\d+)`)

// mapPairs iterates a mapping node's keys and values in order.
func mapPairs(n *yaml.Node) [][2]*yaml.Node {
	var out [][2]*yaml.Node
	if n == nil || n.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		out = append(out, [2]*yaml.Node{n.Content[i], n.Content[i+1]})
	}
	return out
}

func customPrefixes(root *yaml.Node) *prefixMap {
	p := newPrefixMap()
	for _, kv := range mapPairs(mapValue(root, "prefixes")) {
		p.set(kv[0].Value, kv[1].Value)
	}
	return p
}

func validateRulesetText(text string, m *amfModel, schema map[string]any) []diagnostic {
	if !strings.HasPrefix(strings.TrimLeft(text, " \t\r\n"), validationProfileHeader) {
		return nil
	}
	t := newTextIndex(text)
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		line := 0
		if mm := yamlErrLine.FindStringSubmatch(err.Error()); mm != nil {
			line, _ = strconv.Atoi(mm[1])
			line--
		}
		return []diagnostic{diagAt(t, max(line, 0), 0, "YAML parsing error: "+strings.TrimPrefix(err.Error(), "yaml: "), "error", govValidatorSource)}
	}
	if len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	data := yamlToAny(root)
	var diags []diagnostic
	// JSON Schema
	if schema == nil {
		schema = map[string]any{"type": "object", "required": []any{"name", "version"}}
	}
	s := &jsonSchema{root: schema}
	for _, e := range s.validate(data, schema, "") {
		var parts []string
		for p := range strings.SplitSeq(e.path, "/") {
			if p != "" {
				parts = append(parts, strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~"))
			}
		}
		l, c := locate(&doc, t, parts, false)
		diags = append(diags, diagAt(t, l, c, e.message, "error", govValidatorSource))
	}
	// the model
	validations := mapValue(root, "validations")
	custom := customPrefixes(root)
	keyAt := func(path ...string) (int, int) { return locate(&doc, t, path, true) }
	type kindNames struct {
		kind  string
		names []string
	}
	var kinds []kindNames
	for _, kv := range mapPairs(validations) {
		name, v := kv[0].Value, kv[1]
		if v.Kind != yaml.MappingNode {
			continue
		}
		tc := mapValue(v, "targetClass")
		if tc != nil && tc.Kind == yaml.ScalarNode && tc.Tag == "!!str" {
			class := tc.Value
			l, c := keyAt("validations", name, "targetClass")
			switch {
			case !m.isValidClass(class, custom):
				diags = append(diags, diagAt(t, l, c, fmt.Sprintf(`Invalid targetClass: "%s". Expected format: "prefix.ClassName" (e.g., "apiContract.EndPoint"). Check that the prefix is defined and the class exists.`, class), "error", govModelSource))
			case !m.isTargetable(class, custom):
				diags = append(diags, diagAt(t, l, c, fmt.Sprintf(`Class "%s" cannot be used as a targetClass directly. It is only reachable via property paths from another class.`, class), "error", govModelSource))
			default:
				if k := m.specKind(class, custom); k != "" {
					i := slices.IndexFunc(kinds, func(e kindNames) bool { return e.kind == k })
					if i < 0 {
						kinds = append(kinds, kindNames{kind: k})
						i = len(kinds) - 1
					}
					kinds[i].names = append(kinds[i].names, name)
				}
			}
			pc := mapValue(v, "propertyConstraints")
			if pc == nil || pc.Kind != yaml.MappingNode || !m.isValidClass(class, custom) {
				continue
			}
			for _, pkv := range mapPairs(pc) {
				prop, constraints := pkv[0].Value, pkv[1]
				if r := m.validatePath(prop, class, custom); !r.ok {
					l, c := keyAt("validations", name, "propertyConstraints", prop)
					diags = append(diags, diagAt(t, l, c, fmt.Sprintf(`Invalid property path "%s" for class "%s": %s`, prop, class, r.err), "error", govModelSource))
					continue
				}
				if constraints.Kind != yaml.MappingNode {
					continue
				}
				kind := m.pathEndType(prop, class, custom)
				allowed, label, ok := constraintsFor(kind)
				if !ok {
					continue
				}
				for _, ckv := range mapPairs(constraints) {
					key := ckv[0].Value
					if key == "message" || slices.Contains(allowed, key) {
						continue
					}
					l, c := keyAt("validations", name, "propertyConstraints", prop, key)
					diags = append(diags, diagAt(t, l, c, fmt.Sprintf(`Constraint "%s" is not applicable to property "%s" (%s type). Allowed constraints for %s: %s`,
						key, prop, label, label, strings.Join(allowed, ", ")), "warning", govModelSource))
				}
			}
		}
	}
	if len(kinds) > 1 {
		var parts []string
		for _, k := range kinds {
			parts = append(parts, fmt.Sprintf("%s (%s)", k.kind, strings.Join(k.names, ", ")))
		}
		for _, k := range kinds {
			for _, name := range k.names {
				l, c := keyAt("validations", name, "targetClass")
				diags = append(diags, diagAt(t, l, c, fmt.Sprintf("Cross-spec-kind ruleset: validations target multiple incompatible spec kinds (%s). A ruleset should target only one spec kind at a time.", strings.Join(parts, " vs ")), "warning", govModelSource))
			}
		}
	}
	// severities
	if validations != nil && len(validations.Content) > 0 {
		lists := map[string][]string{}
		for _, sev := range []string{"violation", "warning", "info"} {
			if sq := mapValue(root, sev); sq != nil && sq.Kind == yaml.SequenceNode {
				for _, n := range sq.Content {
					lists[sev] = append(lists[sev], n.Value)
				}
			}
		}
		if len(lists["violation"])+len(lists["warning"])+len(lists["info"]) == 0 {
			l, c := keyAt("validations")
			diags = append(diags, diagAt(t, l, c, "No validations are assigned to severity levels (violation, warning, or info). Add at least one validation to a severity level.", "warning", govValidatorSource))
		}
		assigned := map[string][]string{}
		var order []string
		for _, sev := range []string{"violation", "warning", "info"} {
			for _, n := range lists[sev] {
				if _, ok := assigned[n]; !ok {
					order = append(order, n)
				}
				assigned[n] = append(assigned[n], sev)
			}
		}
		for _, n := range order {
			sevs := assigned[n]
			if len(sevs) <= 1 {
				continue
			}
			for _, sev := range sevs {
				kl, _ := keyAt(sev)
				line := kl + 1 + max(0, slices.Index(lists[sev], n))
				diags = append(diags, diagAt(t, line, 0, fmt.Sprintf(`Validation "%s" is assigned to multiple severity levels: %s. A validation should only be in one severity level.`, n, strings.Join(sevs, ", ")), "error", govValidatorSource))
			}
		}
	}
	return diags
}

func loadRulesetSchema(res string) map[string]any {
	b, err := os.ReadFile(res + "/ruleset.schema.json")
	if err != nil {
		return nil
	}
	var s map[string]any
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	return s
}

func cmdGovRulesetValidateAuthoring(x *Ctx, args []string) error {
	pos, err := x.Parse(x.Flags(), args)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(pos[0])
	if err != nil {
		return fmt.Errorf("file not found: %s", pos[0])
	}
	m, res, err := loadGovModel(x)
	if err != nil {
		return err
	}
	diags := validateRulesetText(string(b), m, loadRulesetSchema(res))
	errorsN, warnings := 0, 0
	for _, d := range diags {
		switch d.Severity {
		case "error":
			errorsN++
		case "warning":
			warnings++
		}
	}
	if err := x.Print(map[string]any{"diagnostics": diags, "errors": errorsN, "warnings": warnings}, func() {
		if len(diags) == 0 {
			fmt.Fprintln(x.Out, "Ruleset is valid")
			return
		}
		for _, d := range diags {
			p := map[string]string{"error": "ERROR", "warning": "WARN"}[d.Severity]
			fmt.Fprintf(x.Out, "[%s] Line %d: %s\n", cmp(p, "INFO"), d.Line+1, d.Message)
		}
		fmt.Fprintf(x.Out, "\n%d error(s), %d warning(s)\n", errorsN, warnings)
	}); err != nil {
		return err
	}
	if errorsN > 0 {
		return fmt.Errorf("%s has %d authoring error(s)", pos[0], errorsN)
	}
	return nil
}

// ---------------------------------------------------------------- completions

type completion struct {
	Label  string `json:"label"`
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
	Doc    string `json:"documentation,omitempty"`
	Insert string `json:"insertText,omitempty"`
}

var structureCompletions = []completion{
	{"profile", "Property", "Profile name", "Name of the validation profile", "profile: "},
	{"description", "Property", "Profile description", "Description of what this validation profile does", "description: "},
	{"prefixes", "Property", "Custom URI prefixes", "Define custom prefixes for URI expansion", "prefixes:\n  "},
	{"tags", "Property", "Profile tags", "Tags for categorizing the validation profile", "tags:\n  - "},
	{"validations", "Property", "Validation rules", "Define validation rules for the metadata model", "validations:\n  "},
	{"violation", "Property", "Violation severity", "List of validation rules that produce violations", "violation:\n  - "},
	{"warning", "Property", "Warning severity", "List of validation rules that produce warnings", "warning:\n  - "},
	{"info", "Property", "Info severity", "List of validation rules that produce informational messages", "info:\n  - "},
}

var constraintFieldCompletions = []completion{
	{"minCount", "Property", "Minimum occurrences", "", "minCount: "},
	{"maxCount", "Property", "Maximum occurrences", "", "maxCount: "},
	{"pattern", "Property", "Regex pattern", "", "pattern: "},
	{"minLength", "Property", "Minimum length", "", "minLength: "},
	{"maxLength", "Property", "Maximum length", "", "maxLength: "},
	{"minExclusive", "Property", "Minimum value (exclusive)", "", "minExclusive: "},
	{"minInclusive", "Property", "Minimum value (inclusive)", "", "minInclusive: "},
	{"maxExclusive", "Property", "Maximum value (exclusive)", "", "maxExclusive: "},
	{"maxInclusive", "Property", "Maximum value (inclusive)", "", "maxInclusive: "},
	{"in", "Property", "Allowed values", "", "in:\n  - "},
	{"message", "Property", "Constraint message", "", "message: "},
	{"atLeast", "Property", "At least constraint", "", "atLeast: "},
	{"atMost", "Property", "At most constraint", "", "atMost: "},
}

type completer struct {
	m      *amfModel
	schema map[string]any
}

func indentOf(s string) int { return len(s) - len(strings.TrimLeft(s, " \t")) }

func (cc *completer) complete(text string, offset int) []completion {
	if !strings.HasPrefix(strings.TrimLeft(text, " \t\r\n"), validationProfileHeader) {
		return nil
	}
	offset = max(0, min(offset, len(text)))
	before := text[:offset]
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return structureCompletions
	}
	var root *yaml.Node
	if len(doc.Content) > 0 {
		root = doc.Content[0]
	}
	custom := customPrefixes(root)
	t := newTextIndex(text)
	path := cc.pathAt(root, t, offset)
	if len(path) == 0 {
		path = inferPath(before)
	}
	switch {
	case cc.inConstraintDefinition(path, before):
		return constraintFieldCompletions
	case cc.inPropertyConstraints(path, before):
		return cc.propertyConstraintCompletions(path, root, custom, text, offset)
	case strings.HasSuffix(strings.TrimSpace(before), "targetClass:"):
		return cc.targetClassCompletions(custom)
	case inValidations(path):
		return cc.validationFieldCompletions(path, root, before)
	case inSeverityArray(path, before):
		var items []completion
		for _, kv := range mapPairs(mapValue(root, "validations")) {
			n := kv[0].Value
			items = append(items, completion{n, "Value", "Validation rule", fmt.Sprintf(`Add "%s" validation to this severity level`, n), "- " + n})
		}
		return items
	case inRoot(path):
		return structureCompletions
	}
	return nil
}

func (cc *completer) pathAt(root *yaml.Node, t *textIndex, offset int) []string {
	var result []string
	var find func(n *yaml.Node, cur []string) bool
	find = func(n *yaml.Node, cur []string) bool {
		if n == nil || isNullScalar(n) {
			return false
		}
		start, end := t.offset(n.Line, n.Column), t.end(n)
		if offset < start || offset > end {
			return false
		}
		switch n.Kind {
		case yaml.MappingNode:
			for _, kv := range mapPairs(n) {
				key, val := kv[0], kv[1]
				kp := append(slices.Clone(cur), key.Value)
				ks, ke := t.offset(key.Line, key.Column), t.end(key)
				if offset >= ks && offset <= ke {
					result = kp
					return true
				}
				if !isNullScalar(val) && find(val, kp) {
					return true
				}
				if !isNullScalar(val) {
					if offset >= ke && offset <= t.offset(val.Line, val.Column) {
						result = kp
						return true
					}
				} else if offset >= ke && offset <= end {
					result = kp
					return true
				}
			}
		case yaml.SequenceNode:
			for i, item := range n.Content {
				if find(item, append(slices.Clone(cur), strconv.Itoa(i))) {
					return true
				}
			}
		}
		return false
	}
	find(root, nil)
	return result
}

var keyLine = regexp.MustCompile(`^([\w.-]+):`)

func inferPath(before string) []string {
	type entry struct {
		key    string
		indent int
	}
	var stack []entry
	for line := range strings.SplitSeq(before, "\n") {
		tr := strings.TrimSpace(line)
		if tr == "" || strings.HasPrefix(tr, "#") {
			continue
		}
		if m := keyLine.FindStringSubmatch(tr); m != nil {
			ind := indentOf(line)
			for len(stack) > 0 && stack[len(stack)-1].indent >= ind {
				stack = stack[:len(stack)-1]
			}
			stack = append(stack, entry{m[1], ind})
		}
	}
	out := make([]string, len(stack))
	for i, e := range stack {
		out[i] = e.key
	}
	return out
}

func inRoot(path []string) bool {
	if len(path) == 0 || len(path) == 1 && path[0] == "" {
		return true
	}
	return len(path) == 1 && slices.Contains([]string{"profile", "description", "extends", "prefixes", "tags", "violation", "warning", "info", "validations"}, path[0])
}

// keyAbove reports whether one of the last five lines starts with key and
// the cursor's line is indented deeper.
func keyAbove(before, key string) bool {
	lines := strings.Split(before, "\n")
	for i := len(lines) - 1; i >= max(0, len(lines)-5); i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), key+":") && indentOf(lines[len(lines)-1]) > indentOf(lines[i]) {
			return true
		}
	}
	return false
}

func inSeverityArray(path []string, before string) bool {
	for _, k := range []string{"violation", "warning", "info"} {
		if len(path) == 1 && path[0] == k || strings.HasSuffix(strings.TrimSpace(before), k+":") || keyAbove(before, k) {
			return true
		}
	}
	return false
}

func inValidations(path []string) bool {
	if len(path) == 2 && path[0] == "validations" {
		return true
	}
	return len(path) == 3 && path[0] == "validations" && path[2] != "propertyConstraints" && path[2] != "classConstraints"
}

func (cc *completer) inPropertyConstraints(path []string, before string) bool {
	return strings.HasSuffix(strings.TrimSpace(before), "propertyConstraints:") ||
		len(path) >= 3 && path[0] == "validations" && path[2] == "propertyConstraints" || keyAbove(before, "propertyConstraints")
}

func (cc *completer) inConstraintDefinition(path []string, before string) bool {
	lines := strings.Split(before, "\n")
	cur := lines[len(lines)-1]
	if len(path) >= 4 && path[0] == "validations" && path[2] == "propertyConstraints" {
		return strings.Contains(cur, ":") || strings.TrimSpace(cur) == ""
	}
	ci := indentOf(cur)
	for i := len(lines) - 2; i >= max(0, len(lines)-10); i-- {
		line := lines[i]
		tr := strings.TrimSpace(line)
		if tr == "" {
			continue
		}
		if c := strings.Index(tr, ":"); c > 0 {
			// v4 compares characters of the key (a Set of its characters),
			// so this is "the key has a dot"
			if strings.Contains(tr[:c], ".") && ci > indentOf(line) {
				return true
			}
		}
		if strings.HasPrefix(tr, "propertyConstraints:") {
			break
		}
		if indentOf(line) < ci-2 {
			break
		}
	}
	return false
}

func (cc *completer) targetClassCompletions(custom *prefixMap) []completion {
	all := cc.m.prefixes(custom)
	type group struct {
		prefix string
		names  []string
	}
	var groups []group
	for _, uri := range cc.m.allClasses() {
		for _, p := range all.keys {
			if rest, ok := strings.CutPrefix(uri, all.ns[p]); ok {
				i := slices.IndexFunc(groups, func(g group) bool { return g.prefix == p })
				if i < 0 {
					groups = append(groups, group{prefix: p})
					i = len(groups) - 1
				}
				if !slices.Contains(groups[i].names, rest) {
					groups[i].names = append(groups[i].names, rest)
				}
			}
		}
	}
	var items []completion
	for _, g := range groups {
		for _, n := range g.names {
			items = append(items, completion{Label: g.prefix + "." + n, Kind: "Class", Detail: "Class (" + g.prefix + ")"})
		}
	}
	return items
}

func (cc *completer) validationFieldCompletions(path []string, root *yaml.Node, before string) []completion {
	existing := map[string]bool{}
	if len(path) >= 2 {
		for _, kv := range mapPairs(mapValue(mapValue(root, "validations"), path[1])) {
			existing[kv[0].Value] = true
		}
	}
	fields := schemaFields(cc.schema, existing)
	keyInPath := ""
	if len(path) > 2 {
		keyInPath = path[len(path)-1]
	}
	lines := strings.Split(before, "\n")
	cur := lines[len(lines)-1]
	inKey := !strings.Contains(cur, ":") && strings.TrimSpace(cur) != ""
	var items []completion
	for _, f := range fields {
		keep := !existing[f.Label]
		if !keep && keyInPath != "" {
			if strings.TrimSpace(keyInPath) != "" && f.Label == keyInPath {
				keep = inKey
			} else {
				keep = strings.HasPrefix(f.Label, keyInPath)
			}
		}
		if keep {
			items = append(items, f)
		}
	}
	return items
}

// schemaFields lists the fields a validation can have, from the schema's
// baseValidation and the definition its existing fields point to.
func schemaFields(schema map[string]any, existing map[string]bool) []completion {
	kind := "shapeValidation"
	switch {
	case existing["rego"]:
		kind = "regoValidation"
	case existing["regoModule"]:
		kind = "regoModuleValidation"
	case existing["condition"]:
		kind = "conditionalValidation"
	case existing["validations"]:
		kind = "andValidation"
	case existing["validation"]:
		kind = "notValidation"
	}
	defs, _ := schema["definitions"].(map[string]any)
	var fields []completion
	seen := map[string]bool{}
	var walk func(def map[string]any, raw json.RawMessage)
	walk = func(def map[string]any, raw json.RawMessage) {
		if ref, ok := def["$ref"].(string); ok {
			name := strings.TrimPrefix(ref, "#/definitions/")
			if d, ok := defs[name].(map[string]any); ok {
				walk(d, schemaRaw(schema, name))
				return
			}
		}
		for _, sub := range anyList(def["allOf"]) {
			walk(sub, nil)
		}
		props, _ := def["properties"].(map[string]any)
		for _, name := range orderedPropertyNames(raw, props) {
			pd, _ := props[name].(map[string]any)
			typ := schemaType(pd)
			insert := name + ": "
			switch typ {
			case "object":
				insert = name + ":\n  "
			case "array":
				insert = name + ":\n  - "
			}
			detail := strings.ToUpper(typ[:1]) + typ[1:]
			if alts := anyList(pd["oneOf"]); len(alts) > 0 {
				var ts []string
				for _, a := range alts {
					if s, ok := a["type"].(string); ok {
						ts = append(ts, s)
					}
				}
				if len(ts) > 1 {
					detail = strings.Join(ts, " or ")
				}
			}
			desc, _ := pd["description"].(string)
			if !seen[name] {
				seen[name] = true
				fields = append(fields, completion{Label: name, Kind: "Property", Detail: detail, Doc: desc, Insert: insert})
			}
		}
	}
	for _, name := range []string{"baseValidation", kind} {
		if d, ok := defs[name].(map[string]any); ok {
			walk(d, schemaRaw(schema, name))
		}
	}
	return fields
}

func schemaType(pd map[string]any) string {
	switch t := pd["type"].(type) {
	case string:
		return t
	case []any:
		if len(t) > 0 {
			return fmt.Sprint(t[0])
		}
	}
	if alts := anyList(pd["oneOf"]); len(alts) > 0 {
		if s, ok := alts[0]["type"].(string); ok {
			return s
		}
	}
	return "string"
}

// schemaRaw keeps the schema's own key order for a definition's properties
// (decoded maps lose it).
var schemaRawCache = map[string]json.RawMessage{}

func schemaRaw(schema map[string]any, name string) json.RawMessage {
	if raw, ok := schemaRawCache[name]; ok {
		return raw
	}
	b, _ := json.Marshal(schema["definitions"].(map[string]any)[name])
	return b
}

func orderedPropertyNames(raw json.RawMessage, props map[string]any) []string {
	if raw != nil {
		var def struct {
			Properties ordered `json:"properties"`
		}
		if json.Unmarshal(raw, &def) == nil && len(def.Properties.keys) == len(props) {
			return def.Properties.keys
		}
	}
	return sortedKeys(props)
}

func (cc *completer) propertyConstraintCompletions(path []string, root *yaml.Node, custom *prefixMap, text string, offset int) []completion {
	if len(path) < 2 {
		return nil
	}
	v := mapValue(mapValue(root, "validations"), path[1])
	tc := mapValue(v, "targetClass")
	if tc == nil || tc.Value == "" {
		return nil
	}
	class := tc.Value
	current := currentLine(text, offset)
	lines := strings.Split(text[:offset], "\n")
	prev := ""
	if len(lines) > 1 {
		prev = lines[len(lines)-2]
	}
	check := current
	if strings.TrimSpace(current) == "" {
		check = prev
	}
	deeper := indentOf(current) > indentOf(check)
	trimmed := strings.TrimSpace(check)
	if strings.Contains(trimmed, ":") && !strings.Contains(trimmed, "/") && !strings.HasSuffix(trimmed, "propertyConstraints:") && !deeper {
		return nil
	}
	if strings.Contains(check, "/") {
		key := trimmed
		if c := strings.Index(trimmed, ":"); c > 0 {
			key = trimmed[:c]
		}
		if strings.Contains(key, "/") {
			return cc.pathCompletions(key, class, custom)
		}
	}
	prefix := "core"
	if strings.Contains(class, ".") {
		prefix, _, _ = strings.Cut(class, ".")
	}
	var items []completion
	for _, p := range cc.m.propertiesOf(class, custom) {
		n := prefix + "." + p
		items = append(items, completion{Label: n, Kind: "Field", Detail: "Property of " + class, Insert: n + ":\n  "})
	}
	return items
}

func (cc *completer) pathCompletions(path, class string, custom *prefixMap) []completion {
	segs := strings.Split(path, "/")
	for i := range segs {
		segs[i] = strings.TrimSpace(segs[i])
	}
	partial := segs[len(segs)-1]
	cur := class
	for _, s := range segs[:len(segs)-1] {
		if s == "" {
			continue
		}
		if cur = cc.m.propertyRange(s, cur, custom); cur == "" {
			return nil
		}
	}
	prefix := "core"
	if strings.Contains(cur, ".") {
		prefix, _, _ = strings.Cut(cur, ".")
	}
	var items []completion
	for _, p := range cc.m.propertiesOf(cur, custom) {
		n := prefix + "." + p
		if partial != "" && !strings.Contains(strings.ToLower(n), strings.ToLower(partial)) {
			continue
		}
		items = append(items, completion{Label: n, Kind: "Field", Detail: "Property of " + cur, Insert: n})
	}
	return items
}

func currentLine(text string, offset int) string {
	pos := 0
	for line := range strings.SplitSeq(text, "\n") {
		end := pos + len(line) + 1
		if offset <= end {
			return line
		}
		pos = end
	}
	return ""
}

func cmdGovRulesetCompletions(x *Ctx, args []string) error {
	fs := x.Flags()
	offset := fs.Int("offset", -1, "cursor `offset` in the file")
	fs.String("line-text", "", "the current line up to the cursor (accepted; the file is read at --offset)")
	pos, err := x.Parse(fs, args)
	if err != nil {
		return err
	}
	if *offset < 0 {
		return errors.New("--offset is required")
	}
	b, err := os.ReadFile(pos[0])
	if err != nil {
		return fmt.Errorf("file not found: %s", pos[0])
	}
	m, res, err := loadGovModel(x)
	if err != nil {
		return err
	}
	cc := &completer{m: m, schema: loadRulesetSchema(res)}
	// v4 counts the offset in UTF-16 code units; this counts characters
	items := cc.complete(string(b), runeOffset(string(b), *offset))
	return x.Print(items, func() {
		if len(items) == 0 {
			fmt.Fprintln(x.Out, "No completions available at this position.")
			return
		}
		for _, it := range items {
			d := ""
			if it.Detail != "" {
				d = " (" + it.Detail + ")"
			}
			fmt.Fprintf(x.Out, "%s%s\n", it.Label, d)
		}
	})
}

// runeOffset converts a character offset to a byte offset.
func runeOffset(s string, chars int) int {
	i := 0
	for n := 0; n < chars && i < len(s); n++ {
		_, w := utf8.DecodeRuneInString(s[i:])
		i += w
	}
	return i
}

// ---------------------------------------------------------------- simplify

func cmdGovRulesetSimplify(x *Ctx, args []string) error {
	pos, err := x.Parse(x.Flags(), args)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(pos[0])
	if err != nil {
		return fmt.Errorf("file not found: %s", pos[0])
	}
	out, changes, err := simplifyRulesetText(string(b))
	if err != nil {
		return fmt.Errorf("failed to simplify: %w", err)
	}
	return x.Print(map[string]any{"changes": changes, "ruleset": out}, func() {
		if len(changes) == 0 {
			fmt.Fprintln(x.Out, "No simplifications needed.")
		} else {
			fmt.Fprintf(x.Out, "Applied %d simplification(s):\n", len(changes))
			for _, c := range changes {
				fmt.Fprintf(x.Out, "  - %s\n", c)
			}
			fmt.Fprintln(x.Out)
		}
		fmt.Fprintln(x.Out, out)
	})
}

// simplifyRulesetText flattens nested property paths and removes redundant
// and/or/not/xone wrappers, as the plugin's simplifier does, keeping the
// rest of the ruleset as written.
func simplifyRulesetText(text string) (string, []string, error) {
	body := regexp.MustCompile(`^#%Validation Profile 1\.0\s*\n*`).ReplaceAllString(text, "")
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		return "", nil, err
	}
	var changes []string
	root := &yaml.Node{Kind: yaml.MappingNode}
	if len(doc.Content) > 0 {
		root = doc.Content[0]
	}
	if v := mapValue(root, "validations"); v != nil {
		for i := 0; i+1 < len(v.Content); i += 2 {
			v.Content[i+1] = simplifyBody(v.Content[i+1], &changes, v.Content[i].Value)
		}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return "", nil, err
	}
	return validationProfileHeader + "\n\n" + buf.String(), changes, nil
}

func bodyKind(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.MappingNode {
		return "shape"
	}
	has := func(k string) *yaml.Node { return mapValue(n, k) }
	switch {
	case has("and") != nil && has("and").Kind == yaml.SequenceNode:
		return "and"
	case has("or") != nil && has("or").Kind == yaml.SequenceNode:
		return "or"
	case has("not") != nil && !isNullScalar(has("not")):
		return "not"
	case has("xone") != nil && has("xone").Kind == yaml.SequenceNode:
		return "xone"
	case has("if") != nil && !isNullScalar(has("if")) && has("then") != nil && !isNullScalar(has("then")):
		return "ifThenElse"
	case has("rego") != nil && has("rego").Kind == yaml.ScalarNode && has("propertyConstraints") == nil:
		return "rego"
	case has("regoModule") != nil && has("regoModule").Kind == yaml.MappingNode:
		return "regoModule"
	}
	return "shape"
}

var shapeOnlyKeys = []string{"message", "documentation", "aspect", "examples", "targetClass", "classConstraints", "propertyConstraints"}

func simplifyBody(n *yaml.Node, changes *[]string, ctx string) *yaml.Node {
	switch bodyKind(n) {
	case "shape":
		if pc := mapValue(n, "propertyConstraints"); pc != nil && pc.Kind == yaml.MappingNode {
			var out []*yaml.Node
			for _, kv := range mapPairs(pc) {
				out = append(out, simplifyConstraint(kv[0], kv[1], changes, ctx)...)
			}
			pc.Content = out
		}
		return n
	case "and", "or":
		op := bodyKind(n)
		list := mapValue(n, op)
		var flat []*yaml.Node
		for _, c := range list.Content {
			c = simplifyBody(c, changes, ctx)
			if bodyKind(c) == op && onlyKey(c, op) {
				flat = append(flat, mapValue(c, op).Content...)
				*changes = append(*changes, fmt.Sprintf(`%s: flattened nested "%s" into parent "%s"`, ctx, op, op))
				continue
			}
			flat = append(flat, c)
		}
		if len(flat) == 1 && wrapperOnly(n, op) {
			if lifted, ok := liftMeta(n, flat[0]); ok {
				*changes = append(*changes, fmt.Sprintf(`%s: unwrapped single-child "%s"`, ctx, op))
				return lifted
			}
		}
		if op == "and" {
			flat = mergeAdjacentShapes(flat, changes, ctx)
		}
		list.Content = flat
		return n
	case "not":
		child := simplifyBody(mapValue(n, "not"), changes, ctx)
		if bodyKind(child) == "not" && onlyKey(child, "not") && wrapperOnly(n, "not") {
			if lifted, ok := liftMeta(n, mapValue(child, "not")); ok {
				*changes = append(*changes, ctx+": eliminated double negation")
				return lifted
			}
		}
		setValue(n, "not", child)
		return n
	case "xone":
		list := mapValue(n, "xone")
		for i, c := range list.Content {
			list.Content[i] = simplifyBody(c, changes, ctx)
		}
		if len(list.Content) == 1 && wrapperOnly(n, "xone") {
			if lifted, ok := liftMeta(n, list.Content[0]); ok {
				*changes = append(*changes, ctx+`: unwrapped single-child "xone"`)
				return lifted
			}
		}
		return n
	case "ifThenElse":
		for _, k := range []string{"if", "then", "else"} {
			if c := mapValue(n, k); c != nil && !isNullScalar(c) {
				setValue(n, k, simplifyBody(c, changes, ctx))
			}
		}
		then := mapValue(n, "then")
		if e := mapValue(n, "else"); (e == nil || isNullScalar(e)) && bodyKind(then) == "shape" && isEmptyShape(then) {
			*changes = append(*changes, ctx+`: removed if/then with empty "then" body`)
			kept := &yaml.Node{Kind: yaml.MappingNode}
			for _, k := range []string{"message", "documentation", "aspect", "examples", "targetClass"} {
				if v := mapValue(n, k); v != nil {
					setValue(kept, k, v)
				}
			}
			return kept
		}
		return n
	}
	return n
}

// metaKeys describe a rule rather than constrain it. v4's simplifier drops
// them from the wrappers it removes (so not: {not: X} under a targetClass
// loses the targetClass); here they move onto what is left.
var metaKeys = []string{"message", "documentation", "aspect", "examples", "targetClass"}

// onlyKey reports whether a body is just its operator.
func onlyKey(n *yaml.Node, op string) bool {
	for _, kv := range mapPairs(n) {
		if kv[0].Value != op {
			return false
		}
	}
	return true
}

// wrapperOnly reports whether a body is its operator and metadata.
func wrapperOnly(n *yaml.Node, op string) bool {
	for _, kv := range mapPairs(n) {
		if kv[0].Value != op && !slices.Contains(metaKeys, kv[0].Value) {
			return false
		}
	}
	return true
}

// liftMeta puts a wrapper's metadata on the body it wraps; false when the
// body has different metadata of its own.
func liftMeta(outer, inner *yaml.Node) (*yaml.Node, bool) {
	var meta []*yaml.Node
	for _, kv := range mapPairs(outer) {
		if !slices.Contains(metaKeys, kv[0].Value) {
			continue
		}
		if own := mapValue(inner, kv[0].Value); own != nil {
			if own.Kind != yaml.ScalarNode || kv[1].Kind != yaml.ScalarNode || own.Value != kv[1].Value {
				return nil, false
			}
			continue
		}
		meta = append(meta, kv[0], kv[1])
	}
	if len(meta) == 0 {
		return inner, true
	}
	if inner.Kind != yaml.MappingNode {
		return nil, false
	}
	return &yaml.Node{Kind: yaml.MappingNode, Content: append(meta, inner.Content...)}, true
}

func isEmptyShape(n *yaml.Node) bool {
	pc := mapValue(n, "propertyConstraints")
	return pc == nil || len(pc.Content) == 0
}

func setValue(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
}

// simplifyConstraint flattens path: {nested: {propertyConstraints: …}} into
// "path / inner" entries when nothing else sits at that level.
func simplifyConstraint(key, val *yaml.Node, changes *[]string, ctx string) []*yaml.Node {
	if val.Kind == yaml.MappingNode {
		if n := mapValue(val, "nested"); n != nil {
			setValue(val, "nested", simplifyBody(n, changes, ctx))
		}
		for _, q := range []string{"atLeast", "atMost"} {
			if qc := mapValue(val, q); qc != nil {
				if v := mapValue(qc, "validation"); v != nil {
					setValue(qc, "validation", simplifyBody(v, changes, ctx))
				}
			}
		}
		if nested := mapValue(val, "nested"); nested != nil && canFlatten(val, nested) {
			outerMsg := mapValue(val, "message")
			var out, paths []*yaml.Node
			for _, kv := range mapPairs(mapValue(nested, "propertyConstraints")) {
				inner := kv[1]
				if outerMsg != nil && inner.Kind == yaml.MappingNode && mapValue(inner, "message") == nil {
					setValue(inner, "message", outerMsg)
				}
				k := &yaml.Node{Kind: yaml.ScalarNode, Value: key.Value + " / " + kv[0].Value}
				paths = append(paths, k)
				out = append(out, k, inner)
			}
			var names []string
			for _, p := range paths {
				names = append(names, p.Value)
			}
			*changes = append(*changes, fmt.Sprintf(`%s: flattened nested under "%s" into path "%s"`, ctx, key.Value, strings.Join(names, `", "`)))
			var result []*yaml.Node
			for i := 0; i+1 < len(out); i += 2 {
				result = append(result, simplifyConstraint(out[i], out[i+1], changes, ctx)...)
			}
			return result
		}
	}
	return []*yaml.Node{key, val}
}

var scalarConstraintKeys = []string{"pattern", "uniqueValues", "minCount", "maxCount", "exactCount", "minLength", "maxLength", "exactLength",
	"minInclusive", "maxInclusive", "minExclusive", "maxExclusive", "datatype", "in", "containsAll", "containsSome",
	"equalsToProperty", "disjointWithProperty", "lessThanProperty", "lessThanOrEqualsToProperty"}

func canFlatten(val, nested *yaml.Node) bool {
	if bodyKind(nested) != "shape" || mapValue(nested, "targetClass") != nil || mapValue(nested, "classConstraints") != nil {
		return false
	}
	for _, kv := range mapPairs(val) {
		k := kv[0].Value
		if slices.Contains(scalarConstraintKeys, k) || k == "atLeast" || k == "atMost" || k == "rego" || k == "regoModule" {
			return false
		}
	}
	for _, kv := range mapPairs(nested) {
		if !slices.Contains(shapeOnlyKeys, kv[0].Value) {
			return false
		}
	}
	return mapValue(nested, "propertyConstraints") != nil
}

// mergeAdjacentShapes joins consecutive plain shapes of an "and". A path
// in both is merged when their constraints do not overlap; otherwise the
// shapes stay apart (v4 lists the path twice, and YAML keeps only one).
func mergeAdjacentShapes(children []*yaml.Node, changes *[]string, ctx string) []*yaml.Node {
	var out []*yaml.Node
	var pending *yaml.Node
	for _, c := range children {
		mergeable := bodyKind(c) == "shape" && c.Kind == yaml.MappingNode && mapValue(c, "targetClass") == nil &&
			mapValue(c, "classConstraints") == nil && mapValue(c, "message") == nil
		if !mergeable {
			if pending != nil {
				out = append(out, pending)
				pending = nil
			}
			out = append(out, c)
			continue
		}
		if pending != nil && mergeShapeInto(pending, c) {
			*changes = append(*changes, ctx+`: merged adjacent shape constraints in "and"`)
			continue
		}
		if pending != nil {
			out = append(out, pending)
		}
		pending = c
	}
	if pending != nil {
		out = append(out, pending)
	}
	return out
}

func mergeShapeInto(dst, src *yaml.Node) bool {
	more := mapValue(src, "propertyConstraints")
	pc := mapValue(dst, "propertyConstraints")
	for _, kv := range mapPairs(more) {
		have := mapValue(pc, kv[0].Value)
		if have == nil {
			continue
		}
		if have.Kind != yaml.MappingNode || kv[1].Kind != yaml.MappingNode {
			return false
		}
		for _, c := range mapPairs(kv[1]) {
			if mapValue(have, c[0].Value) != nil {
				return false
			}
		}
	}
	if more == nil {
		return true
	}
	if pc == nil {
		pc = &yaml.Node{Kind: yaml.MappingNode}
		setValue(dst, "propertyConstraints", pc)
	}
	for _, kv := range mapPairs(more) {
		if have := mapValue(pc, kv[0].Value); have != nil {
			have.Content = append(have.Content, kv[1].Content...)
			continue
		}
		pc.Content = append(pc.Content, kv[0], kv[1])
	}
	return true
}
