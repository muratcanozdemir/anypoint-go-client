package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// The governance plugin's ruleset-authoring aids work on metadata about the
// AMF model (resources/models/*.json): which classes a rule can target,
// their properties and the properties' ranges. This is that model, ported
// from the plugin with its lookup rules, including where they depend on
// the JSON files' key order.

// ordered is a JSON object with its keys in document order.
type ordered struct {
	keys []string
	vals map[string]json.RawMessage
}

func (o *ordered) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	t, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		return fmt.Errorf("want an object, got %v", t)
	}
	o.vals = map[string]json.RawMessage{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		k, _ := t.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return err
		}
		if _, seen := o.vals[k]; !seen {
			o.keys = append(o.keys, k)
		}
		o.vals[k] = v
	}
	return nil
}

// prefixMap maps prefixes to namespaces, keeping first-insertion order as
// a JavaScript object spread does.
type prefixMap struct {
	keys []string
	ns   map[string]string
}

func newPrefixMap(pairs ...[2]string) *prefixMap {
	p := &prefixMap{ns: map[string]string{}}
	for _, kv := range pairs {
		p.set(kv[0], kv[1])
	}
	return p
}

func (p *prefixMap) set(k, v string) {
	if _, ok := p.ns[k]; !ok {
		p.keys = append(p.keys, k)
	}
	p.ns[k] = v
}

func (p *prefixMap) merge(others ...*prefixMap) *prefixMap {
	out := newPrefixMap()
	for _, m := range append([]*prefixMap{p}, others...) {
		if m == nil {
			continue
		}
		for _, k := range m.keys {
			out.set(k, m.ns[k])
		}
	}
	return out
}

var defaultPrefixes = newPrefixMap(
	[2]string{"data", "http://a.ml/vocabularies/data#"},
	[2]string{"shacl", "http://www.w3.org/ns/shacl#"},
	[2]string{"shapes", "http://a.ml/vocabularies/shapes#"},
	[2]string{"raml-shapes", "http://a.ml/vocabularies/shapes#"},
	[2]string{"doc", "http://a.ml/vocabularies/document#"},
	[2]string{"meta", "http://a.ml/vocabularies/meta#"},
	[2]string{"apiContract", "http://a.ml/vocabularies/apiContract#"},
	[2]string{"core", "http://a.ml/vocabularies/core#"},
	[2]string{"xsd", "http://www.w3.org/2001/XMLSchema#"},
	[2]string{"rdfs", "http://www.w3.org/2000/01/rdf-schema"},
	[2]string{"rdf", "http://www.w3.org/1999/02/22-rdf-syntax-ns#"},
	[2]string{"security", "http://a.ml/vocabularies/security#"},
	[2]string{"sourcemaps", "http://a.ml/vocabularies/document-source-maps#"},
	[2]string{"apiExt", "http://a.ml/vocabularies/api-extension#"},
	[2]string{"gcl", "http://anypoint.com/vocabs/gcl#"},
	[2]string{"management", "http://anypoint.com/vocabs/management#"},
	[2]string{"api", "http://anypoint.com/vocabs/api#"},
	[2]string{"catalog", "http://anypoint.com/vocabs/digital-repository#"},
)

type propInfo struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Range     string `json:"range"`
	IsArray   bool   `json:"isArray"`
	Dynamic   bool   `json:"dynamic"`
}

type classModel struct {
	name       string
	properties []string
	propKeys   []string
	propInfo   map[string]propInfo
	uris       []string
	targetable bool
	unionOf    []string
}

type modelDomain struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Source      string   `json:"source"`
	ClassCount  int      `json:"classCount"`
	Prefixes    []string `json:"prefixes"`
	SpecKind    string   `json:"specKind,omitempty"`
}

type amfModel struct {
	models     map[string]*classModel
	uriToClass map[string]string
	discovered *prefixMap
	domains    []modelDomain
	classSrc   map[string]string
	srcKind    map[string]string
}

type modelFile struct {
	Domain         string  `json:"domain"`
	Description    string  `json:"description"`
	SpecKind       string  `json:"specKind"`
	Models         ordered `json:"models"`
	URIToClassName ordered `json:"uriToClassName"`
}

type modelData struct {
	Name         string   `json:"name"`
	Properties   []string `json:"properties"`
	PropertyInfo ordered  `json:"propertyInfo"`
	URIs         []string `json:"uris"`
	Targetable   *bool    `json:"targetable"`
	UnionOf      []string `json:"unionOf"`
}

func loadAMFModel(resources string) (*amfModel, error) {
	m := &amfModel{models: map[string]*classModel{}, uriToClass: map[string]string{}, discovered: newPrefixMap(),
		classSrc: map[string]string{}, srcKind: map[string]string{}}
	dir := filepath.Join(resources, "models")
	if b, err := os.ReadFile(filepath.Join(dir, "_prefixes.json")); err == nil {
		var o ordered
		if json.Unmarshal(b, &o) == nil {
			for _, k := range o.keys {
				var v string
				if json.Unmarshal(o.vals[k], &v) == nil {
					m.discovered.set(k, v)
				}
			}
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("governance model metadata: %w", err)
	}
	var files []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") && !strings.HasPrefix(e.Name(), "_") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	known := defaultPrefixes.merge(m.discovered)
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			continue
		}
		var data modelFile
		if json.Unmarshal(b, &data) != nil {
			continue
		}
		m.domains = append(m.domains, m.merge(&data, f, known))
		m.srcKind[f] = data.SpecKind
	}
	for _, model := range m.models { // unions take their members' properties
		for _, member := range model.unionOf {
			mm := m.models[member]
			if mm == nil {
				continue
			}
			for _, p := range mm.properties {
				if !slices.Contains(model.properties, p) {
					model.properties = append(model.properties, p)
				}
			}
			for _, k := range mm.propKeys {
				if _, ok := model.propInfo[k]; !ok {
					model.propKeys = append(model.propKeys, k)
					model.propInfo[k] = mm.propInfo[k]
				}
			}
		}
	}
	return m, nil
}

func (m *amfModel) merge(data *modelFile, source string, known *prefixMap) modelDomain {
	var tracked []string
	for _, name := range data.Models.keys {
		var md modelData
		if json.Unmarshal(data.Models.vals[name], &md) != nil {
			continue
		}
		m.register(name, &md)
		if _, ok := m.classSrc[name]; !ok {
			m.classSrc[name] = source
		}
		tracked = append(tracked, name)
	}
	for _, uri := range data.URIToClassName.keys {
		var cn string
		if json.Unmarshal(data.URIToClassName.vals[uri], &cn) == nil {
			if _, ok := m.uriToClass[uri]; !ok {
				m.uriToClass[uri] = cn
			}
		}
	}
	for _, name := range tracked {
		if model := m.models[name]; model != nil && len(model.uris) == 0 {
			if _, ok := m.uriToClass["__internal#"+name]; !ok {
				m.uriToClass["__internal#"+name] = name
			}
		}
	}
	prefixes := map[string]bool{}
	for _, name := range tracked {
		for _, uri := range m.models[name].uris {
			for _, k := range known.keys {
				if strings.HasPrefix(uri, known.ns[k]) {
					prefixes[k] = true
					break
				}
			}
		}
	}
	d := modelDomain{Name: cmp(data.Domain, strings.TrimSuffix(source, ".json")), Description: data.Description, Source: source,
		ClassCount: len(tracked), Prefixes: sortedKeys(prefixes), SpecKind: data.SpecKind}
	if d.Prefixes == nil {
		d.Prefixes = []string{}
	}
	return d
}

func (m *amfModel) register(name string, md *modelData) {
	info := map[string]propInfo{}
	for _, k := range md.PropertyInfo.keys {
		var pi propInfo
		_ = json.Unmarshal(md.PropertyInfo.vals[k], &pi)
		info[k] = pi
	}
	if ex := m.models[name]; ex != nil {
		for _, p := range md.Properties {
			if !slices.Contains(ex.properties, p) {
				ex.properties = append(ex.properties, p)
			}
		}
		for _, k := range md.PropertyInfo.keys {
			if _, ok := ex.propInfo[k]; !ok {
				ex.propKeys = append(ex.propKeys, k)
				ex.propInfo[k] = info[k]
			}
		}
		for _, u := range md.URIs {
			if !slices.Contains(ex.uris, u) {
				ex.uris = append(ex.uris, u)
				m.uriToClass[u] = name
			}
		}
		if md.Targetable != nil && !*md.Targetable {
			ex.targetable = false
		}
		for _, u := range md.UnionOf {
			if !slices.Contains(ex.unionOf, u) {
				ex.unionOf = append(ex.unionOf, u)
			}
		}
		return
	}
	m.models[name] = &classModel{name: md.Name, properties: slices.Clone(md.Properties), propKeys: slices.Clone(md.PropertyInfo.keys),
		propInfo: info, uris: slices.Clone(md.URIs), targetable: md.Targetable == nil || *md.Targetable, unionOf: md.UnionOf}
	for _, u := range md.URIs {
		m.uriToClass[u] = name
	}
}

func (m *amfModel) prefixes(custom *prefixMap) *prefixMap {
	return defaultPrefixes.merge(m.discovered, custom)
}

// expand turns prefix.Name into a URI; "" when the prefix is unknown.
func (m *amfModel) expand(prefixed string, custom *prefixMap) (string, bool) {
	if !strings.Contains(prefixed, ".") {
		return prefixed, true
	}
	if cn, ok := strings.CutPrefix(prefixed, "__internal."); ok {
		if m.models[cn] != nil {
			return "__internal#" + cn, true
		}
		return "", false
	}
	parts := strings.Split(prefixed, ".") // like JS split('.', 2): the third part on is dropped
	ns, ok := m.prefixes(custom).ns[parts[0]]
	if !ok {
		return "", false
	}
	return ns + parts[1], true
}

func fragmentOf(uri string) string { return uri[strings.LastIndex(uri, "#")+1:] }

func (m *amfModel) isValidClass(prefixed string, custom *prefixMap) bool {
	if uri, ok := m.expand(prefixed, custom); ok {
		if _, ok := m.uriToClass[uri]; ok {
			return true
		}
	}
	return m.models[prefixed] != nil
}

func (m *amfModel) isTargetable(prefixed string, custom *prefixMap) bool {
	uri, ok := m.expand(prefixed, custom)
	if !ok {
		return false
	}
	model := m.models[m.uriToClass[uri]]
	return model != nil && model.targetable
}

func (m *amfModel) targetableClasses() []string {
	set := map[string]bool{}
	for _, model := range m.models {
		if model.targetable {
			for _, u := range model.uris {
				set[u] = true
			}
		}
	}
	return sortedKeys(set)
}

func (m *amfModel) allClasses() []string {
	set := map[string]bool{}
	for _, model := range m.models {
		for _, u := range model.uris {
			set[u] = true
		}
	}
	return sortedKeys(set)
}

func (m *amfModel) classOf(prefixed string, custom *prefixMap) string {
	if uri, ok := m.expand(prefixed, custom); ok {
		if cn, ok := m.uriToClass[uri]; ok {
			return cn
		}
	}
	if m.models[prefixed] != nil {
		return prefixed
	}
	return ""
}

func (m *amfModel) modelOfClass(prefixed string, custom *prefixMap) *classModel {
	uri, ok := m.expand(prefixed, custom)
	if !ok {
		return nil
	}
	return m.models[m.uriToClass[uri]]
}

func (m *amfModel) isValidProperty(class, property string, custom *prefixMap) bool {
	model := m.modelOfClass(class, custom)
	if model == nil {
		return false
	}
	uri, ok := m.expand(property, custom)
	return ok && slices.Contains(model.properties, fragmentOf(uri))
}

type prefixedProperty struct {
	Prefixed  string `json:"prefixed"`
	Fragment  string `json:"fragment"`
	Namespace string `json:"namespace,omitempty"`
	Range     string `json:"range,omitempty"`
	IsArray   bool   `json:"isArray,omitempty"`
	Dynamic   bool   `json:"dynamic,omitempty"`
}

func (p prefixedProperty) kind() string {
	switch {
	case p.Dynamic:
		return "dynamic"
	case p.Range != "" && p.IsArray:
		return "nodeArray"
	case p.Range != "":
		return "node"
	case p.IsArray:
		return "scalarArray"
	}
	return "scalar"
}

func (m *amfModel) prefixedProperties(class string, custom *prefixMap) []prefixedProperty {
	model := m.models[m.classOf(class, custom)]
	if model == nil {
		return nil
	}
	all := m.prefixes(custom)
	nsToPrefix := map[string]string{}
	for _, k := range all.keys {
		if _, ok := nsToPrefix[all.ns[k]]; !ok {
			nsToPrefix[all.ns[k]] = k
		}
	}
	var out []prefixedProperty
	for _, frag := range model.propKeys {
		info := model.propInfo[frag]
		prefix := ""
		if info.Namespace != "" {
			prefix = nsToPrefix[info.Namespace]
		}
		if prefix == "" && strings.Contains(class, ".") {
			prefix, _, _ = strings.Cut(class, ".")
		}
		name := frag
		if prefix != "" {
			name = prefix + "." + frag
		}
		out = append(out, prefixedProperty{Prefixed: name, Fragment: frag, Namespace: info.Namespace, Range: info.Range, IsArray: info.IsArray, Dynamic: info.Dynamic})
	}
	return out
}

func (m *amfModel) propertiesOf(class string, custom *prefixMap) []string {
	if model := m.models[m.classOf(class, custom)]; model != nil {
		return model.properties
	}
	return nil
}

func (m *amfModel) specKind(class string, custom *prefixMap) string {
	uri, ok := m.expand(class, custom)
	if !ok {
		return ""
	}
	src := m.classSrc[m.uriToClass[uri]]
	return m.srcKind[src]
}

func (m *amfModel) sortedDomains() []modelDomain {
	d := slices.Clone(m.domains)
	sort.SliceStable(d, func(i, j int) bool { return d[i].Name < d[j].Name })
	return d
}

// ---- property paths: a / b (sequence), a | b (alternatives), a^ (inverse), (…)

type pathResult struct {
	ok  bool
	err string
}

func topLevel(path string, op rune) int {
	depth := 0
	for i, c := range []rune(path) {
		switch {
		case c == '(':
			depth++
		case c == ')':
			depth--
		case depth == 0 && c == op:
			return i
		}
	}
	return -1
}

func splitSequence(path string) []string {
	var parts []string
	var cur strings.Builder
	depth := 0
	for _, c := range path {
		switch {
		case c == '(':
			depth++
			cur.WriteRune(c)
		case c == ')':
			depth--
			cur.WriteRune(c)
		case c == '/' && depth == 0:
			if s := strings.TrimSpace(cur.String()); s != "" {
				parts = append(parts, s)
			}
			cur.Reset()
		default:
			cur.WriteRune(c)
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		parts = append(parts, s)
	}
	return parts
}

func runeSplit(s string, i int) (string, string) {
	r := []rune(s)
	return strings.TrimSpace(string(r[:i])), strings.TrimSpace(string(r[i+1:]))
}

func (m *amfModel) validatePath(path, class string, custom *prefixMap) pathResult {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "apiExt.") {
		return pathResult{ok: true}
	}
	return m.parsePath(path, class, custom)
}

func (m *amfModel) parsePath(path, class string, custom *prefixMap) pathResult {
	if i := topLevel(path, '|'); i != -1 {
		l, r := runeSplit(path, i)
		lr, rr := m.parsePath(l, class, custom), m.parsePath(r, class, custom)
		if lr.ok || rr.ok {
			return pathResult{ok: true}
		}
		return pathResult{err: fmt.Sprintf("Neither alternate path is valid. Left: %s, Right: %s", lr.err, rr.err)}
	}
	if topLevel(path, '/') != -1 {
		return m.validateSequence(path, class, custom)
	}
	if strings.HasPrefix(path, "(") && strings.HasSuffix(path, ")") {
		return m.parsePath(strings.TrimSpace(path[1:len(path)-1]), class, custom)
	}
	return m.validatePredicate(path, class, custom)
}

func (m *amfModel) validateSequence(path, class string, custom *prefixMap) pathResult {
	if strings.HasSuffix(strings.TrimSpace(path), "/") {
		return pathResult{err: "Incomplete property path - path ends with '/' but has no property after it"}
	}
	parts := splitSequence(path)
	cur := class
	for i, part := range parts {
		if i == 0 || !strings.Contains(part, "|") && !strings.Contains(part, "(") {
			if r := m.validatePredicate(part, cur, custom); !r.ok {
				return pathResult{err: fmt.Sprintf("Invalid path segment '%s' (step %d of %d): %s", part, i+1, len(parts), r.err)}
			}
			if i < len(parts)-1 {
				next := m.propertyRange(part, cur, custom)
				if next == "" {
					return m.remainingExist(parts[i+1:], custom)
				}
				cur = next
			}
		} else if r := m.existsAnywhere(part, custom); !r.ok {
			return pathResult{err: fmt.Sprintf("Invalid path segment '%s' (step %d of %d): %s", part, i+1, len(parts), r.err)}
		}
	}
	return pathResult{ok: true}
}

// propertyRange is the prefixed class a property leads to, "" if none.
func (m *amfModel) propertyRange(predicate, class string, custom *prefixMap) string {
	if strings.HasSuffix(predicate, "^") {
		return ""
	}
	model := m.modelOfClass(class, custom)
	if model == nil {
		return ""
	}
	uri, ok := m.expand(predicate, custom)
	if !ok {
		return ""
	}
	info, ok := model.propInfo[fragmentOf(uri)]
	if !ok || info.Range == "" {
		return ""
	}
	all := m.prefixes(custom)
	rangeModel := m.models[info.Range]
	if rangeModel != nil && len(rangeModel.uris) > 0 {
		for _, k := range all.keys {
			if rest, ok := strings.CutPrefix(rangeModel.uris[0], all.ns[k]); ok {
				return k + "." + rest
			}
		}
	}
	for _, k := range all.keys {
		if pn := k + "." + info.Range; m.isValidClass(pn, custom) {
			return pn
		}
	}
	if rangeModel != nil {
		return "__internal." + info.Range
	}
	return info.Range
}

func (m *amfModel) remainingExist(parts []string, custom *prefixMap) pathResult {
	for _, p := range parts {
		if r := m.singleExistsAnywhere(strings.TrimSpace(p), custom); !r.ok {
			return pathResult{err: fmt.Sprintf("Invalid path segment '%s': %s", p, r.err)}
		}
	}
	return pathResult{ok: true}
}

func (m *amfModel) existsAnywhere(path string, custom *prefixMap) pathResult {
	if strings.HasPrefix(path, "(") && strings.HasSuffix(path, ")") {
		return m.existsAnywhere(strings.TrimSpace(path[1:len(path)-1]), custom)
	}
	if i := topLevel(path, '|'); i != -1 {
		l, r := runeSplit(path, i)
		lr, rr := m.existsAnywhere(l, custom), m.existsAnywhere(r, custom)
		if lr.ok || rr.ok {
			return pathResult{ok: true}
		}
		return pathResult{err: fmt.Sprintf("Neither alternate path is valid. Left: %s, Right: %s", lr.err, rr.err)}
	}
	if topLevel(path, '/') != -1 {
		for _, p := range splitSequence(path) {
			if r := m.existsAnywhere(strings.TrimSpace(p), custom); !r.ok {
				return r
			}
		}
		return pathResult{ok: true}
	}
	return m.singleExistsAnywhere(path, custom)
}

func (m *amfModel) anyClassHas(fragment string) bool {
	for _, model := range m.models {
		if slices.Contains(model.properties, fragment) {
			return true
		}
	}
	return false
}

func (m *amfModel) singleExistsAnywhere(predicate string, custom *prefixMap) pathResult {
	p := strings.TrimSpace(strings.TrimSuffix(predicate, "^"))
	if strings.HasPrefix(p, "apiExt.") {
		return pathResult{ok: true}
	}
	uri, ok := m.expand(p, custom)
	if !ok {
		return pathResult{err: fmt.Sprintf("Unknown prefix in '%s'", p)}
	}
	if m.anyClassHas(fragmentOf(uri)) {
		return pathResult{ok: true}
	}
	return pathResult{err: fmt.Sprintf("Property '%s' not found in any class in the model", p)}
}

func (m *amfModel) validatePredicate(predicate, class string, custom *prefixMap) pathResult {
	inverse := strings.HasSuffix(predicate, "^")
	p := predicate
	if inverse {
		p = strings.TrimSpace(strings.TrimSuffix(predicate, "^"))
	}
	if strings.HasPrefix(p, "apiExt.") {
		return pathResult{ok: true}
	}
	if inverse {
		uri, ok := m.expand(p, custom)
		if !ok {
			return pathResult{err: fmt.Sprintf("Unknown prefix in inverse path '%s^'", p)}
		}
		if m.anyClassHas(fragmentOf(uri)) {
			return pathResult{ok: true}
		}
		return pathResult{err: fmt.Sprintf("Property '%s' not found in any class for inverse path navigation", p)}
	}
	if m.isValidProperty(class, p, custom) {
		return pathResult{ok: true}
	}
	if _, ok := m.expand(p, custom); !ok {
		return pathResult{err: fmt.Sprintf("Unknown prefix in '%s'", p)}
	}
	model := m.modelOfClass(class, custom)
	if model == nil {
		return pathResult{err: fmt.Sprintf("Unknown class '%s'", class)}
	}
	msg := fmt.Sprintf("Property '%s' not found on class '%s'", p, class)
	if avail := model.properties[:min(5, len(model.properties))]; len(avail) > 0 {
		msg += ". Available properties include: " + strings.Join(avail, ", ") + "..."
	}
	return pathResult{err: msg}
}

// pathEndType is the kind of value a property path ends at.
func (m *amfModel) pathEndType(path, class string, custom *prefixMap) string {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "apiExt.") {
		return "unknown"
	}
	parts := splitSequence(path)
	if len(parts) == 0 {
		return "unknown"
	}
	last := strings.TrimSpace(parts[len(parts)-1])
	if strings.HasSuffix(last, "^") {
		return "unknown"
	}
	cur := class
	for _, part := range parts[:len(parts)-1] {
		part = strings.TrimSpace(part)
		if strings.HasSuffix(part, "^") || strings.Contains(part, "|") || strings.HasPrefix(part, "(") {
			return "unknown"
		}
		if cur = m.propertyRange(part, cur, custom); cur == "" {
			return "unknown"
		}
	}
	if strings.Contains(last, "|") || strings.HasPrefix(last, "(") {
		return "unknown"
	}
	model := m.modelOfClass(cur, custom)
	if model == nil {
		return "unknown"
	}
	uri, ok := m.expand(last, custom)
	if !ok {
		return "unknown"
	}
	info, ok := model.propInfo[fragmentOf(uri)]
	if !ok {
		return "unknown"
	}
	return prefixedProperty{Range: info.Range, IsArray: info.IsArray, Dynamic: info.Dynamic}.kind()
}

// ---- constraints per property kind

var constraintsByType = []struct {
	kind  string
	label string
	list  []string
}{
	{"scalar", "scalar", []string{"pattern", "minLength", "maxLength", "exactLength", "minExclusive", "maxExclusive", "minInclusive", "maxInclusive",
		"datatype", "minCount", "maxCount", "exactCount", "lessThanProperty", "lessThanOrEqualsToProperty", "equalsToProperty", "disjointWithProperty"}},
	{"node", "node (object)", []string{"nested", "minCount", "maxCount", "exactCount"}},
	{"scalarArray", "scalar array", []string{"pattern", "minLength", "maxLength", "exactLength", "minExclusive", "maxExclusive", "minInclusive", "maxInclusive",
		"minCount", "maxCount", "exactCount", "in", "containsAll", "containsSome"}},
	{"nodeArray", "node (object) array", []string{"atLeast", "atMost", "nested", "minCount", "maxCount", "exactCount"}},
}

func constraintsFor(kind string) ([]string, string, bool) {
	for _, c := range constraintsByType {
		if c.kind == kind {
			return c.list, c.label, true
		}
	}
	return nil, kind, false
}
