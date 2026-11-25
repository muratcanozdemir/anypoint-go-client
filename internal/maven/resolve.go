// Package maven resolves a Maven artifact's runtime classpath, enough for
// pinning the Java tools this CLI runs. It follows Maven's rules where they
// matter for that: parent POMs, properties, dependencyManagement (with
// imported BOMs), compile and runtime scopes, optional dependencies,
// exclusions, and nearest-wins version mediation.
package maven

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"regexp"
	"strings"
	"sync"
)

// Coord is an artifact's coordinates.
type Coord struct {
	Group, Artifact, Version string
	Classifier, Type         string
}

func (c Coord) key() string { return c.Group + ":" + c.Artifact + ":" + c.Classifier }

func (c Coord) String() string {
	s := c.Group + ":" + c.Artifact + ":" + c.Version
	if c.Classifier != "" {
		s += ":" + c.Classifier
	}
	return s
}

// Path is the artifact's path in a repository, for the given extension.
func (c Coord) Path(ext string) string {
	name := c.Artifact + "-" + c.Version
	if c.Classifier != "" && ext != "pom" {
		name += "-" + c.Classifier
	}
	return strings.ReplaceAll(c.Group, ".", "/") + "/" + c.Artifact + "/" + c.Version + "/" + name + "." + ext
}

// ParseCoord reads group:artifact:version[:classifier].
func ParseCoord(s string) (Coord, error) {
	p := strings.Split(s, ":")
	if len(p) < 3 || len(p) > 4 {
		return Coord{}, fmt.Errorf("want group:artifact:version[:classifier], got %q", s)
	}
	c := Coord{Group: p[0], Artifact: p[1], Version: p[2]}
	if len(p) == 4 {
		c.Classifier = p[3]
	}
	return c, nil
}

type dependency struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
	Type       string `xml:"type"`
	Classifier string `xml:"classifier"`
	Scope      string `xml:"scope"`
	Optional   string `xml:"optional"`
	Exclusions []struct {
		GroupID    string `xml:"groupId"`
		ArtifactID string `xml:"artifactId"`
	} `xml:"exclusions>exclusion"`
}

type pom struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
	Packaging  string `xml:"packaging"`
	Parent     *struct {
		GroupID    string `xml:"groupId"`
		ArtifactID string `xml:"artifactId"`
		Version    string `xml:"version"`
	} `xml:"parent"`
	Properties struct {
		Entries []struct {
			XMLName xml.Name
			Value   string `xml:",chardata"`
		} `xml:",any"`
	} `xml:"properties"`
	Managed      []dependency `xml:"dependencyManagement>dependencies>dependency"`
	Dependencies []dependency `xml:"dependencies>dependency"`
}

// effective is a POM with its parents and imports applied.
type effective struct {
	coord   Coord
	props   map[string]string
	managed map[string]dependency // by group:artifact:classifier
	deps    []dependency
}

// Resolver fetches POMs from repositories, first match wins.
type Resolver struct {
	Repos  []string
	Client *http.Client
	mu     sync.Mutex
	poms   map[string]*effective
	// Found records which repository served each artifact path.
	Found map[string]string
}

func (r *Resolver) get(ctx context.Context, path string) ([]byte, string, error) {
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	var errs []error
	for _, repo := range r.Repos {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(repo, "/")+"/"+path, nil)
		if err != nil {
			return nil, "", err
		}
		resp, err := client.Do(req)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		b, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK && err == nil {
			return b, repo, nil
		}
		errs = append(errs, fmt.Errorf("%s: %s", repo, resp.Status))
	}
	return nil, "", fmt.Errorf("%s not found: %w", path, errors.Join(errs...))
}

var propRe = regexp.MustCompile(`\$\{([^}]+)\}`)

func (e *effective) interpolate(s string) string {
	for range 10 {
		next := propRe.ReplaceAllStringFunc(s, func(m string) string {
			if v, ok := e.props[m[2:len(m)-1]]; ok {
				return v
			}
			return m
		})
		if next == s {
			break
		}
		s = next
	}
	return strings.TrimSpace(s)
}

func (r *Resolver) effective(ctx context.Context, c Coord) (*effective, error) {
	r.mu.Lock()
	if r.poms == nil {
		r.poms = map[string]*effective{}
		r.Found = map[string]string{}
	}
	if e, ok := r.poms[c.Group+":"+c.Artifact+":"+c.Version]; ok {
		r.mu.Unlock()
		return e, nil
	}
	r.mu.Unlock()
	b, repo, err := r.get(ctx, c.Path("pom"))
	if err != nil {
		return nil, err
	}
	var p pom
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.CharsetReader = charsetReader
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("%s: %w", c, err)
	}
	e := &effective{coord: c, props: map[string]string{}, managed: map[string]dependency{}}
	if p.Parent != nil {
		parent, err := r.effective(ctx, Coord{Group: p.Parent.GroupID, Artifact: p.Parent.ArtifactID, Version: p.Parent.Version})
		if err != nil {
			return nil, fmt.Errorf("parent of %s: %w", c, err)
		}
		maps.Copy(e.props, parent.props)
		maps.Copy(e.managed, parent.managed)
		e.deps = append(e.deps, parent.deps...)
		if p.GroupID == "" {
			p.GroupID = p.Parent.GroupID
		}
		if p.Version == "" {
			p.Version = p.Parent.Version
		}
		e.props["project.parent.version"] = p.Parent.Version
		e.props["project.parent.groupId"] = p.Parent.GroupID
	}
	for _, kv := range p.Properties.Entries {
		e.props[kv.XMLName.Local] = strings.TrimSpace(kv.Value)
	}
	for _, k := range []string{"project.", "pom.", ""} {
		e.props[k+"groupId"] = p.GroupID
		e.props[k+"version"] = p.Version
		e.props[k+"artifactId"] = p.ArtifactID
	}
	for _, d := range p.Managed {
		d = e.resolve(d)
		if d.Scope == "import" && d.Type == "pom" {
			bom, err := r.effective(ctx, Coord{Group: d.GroupID, Artifact: d.ArtifactID, Version: d.Version})
			if err != nil {
				return nil, fmt.Errorf("BOM %s:%s of %s: %w", d.GroupID, d.ArtifactID, c, err)
			}
			for k, v := range bom.managed {
				if _, ok := e.managed[k]; !ok {
					e.managed[k] = v
				}
			}
			continue
		}
		e.managed[d.GroupID+":"+d.ArtifactID+":"+d.Classifier] = d
	}
	e.deps = append(e.deps, p.Dependencies...)
	for i := range e.deps {
		e.deps[i] = e.resolve(e.deps[i])
	}
	r.mu.Lock()
	r.poms[c.Group+":"+c.Artifact+":"+c.Version] = e
	r.Found[c.Path("pom")] = repo
	r.mu.Unlock()
	return e, nil
}

func (e *effective) resolve(d dependency) dependency {
	d.GroupID, d.ArtifactID = e.interpolate(d.GroupID), e.interpolate(d.ArtifactID)
	d.Version, d.Classifier = e.interpolate(d.Version), e.interpolate(d.Classifier)
	d.Scope, d.Type, d.Optional = e.interpolate(d.Scope), e.interpolate(d.Type), e.interpolate(d.Optional)
	return d
}

// Resolve returns root and its runtime dependencies, nearest first.
func (r *Resolver) Resolve(ctx context.Context, root Coord) ([]Coord, error) {
	type item struct {
		c        Coord
		excluded map[string]bool
		managed  map[string]dependency // the root's management applies throughout
	}
	rootPom, err := r.effective(ctx, root)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Coord
	queue := []item{{c: root, excluded: map[string]bool{}, managed: rootPom.managed}}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if seen[it.c.key()] {
			continue
		}
		seen[it.c.key()] = true
		e, err := r.effective(ctx, it.c)
		if err != nil {
			return nil, err
		}
		if t := it.c.Type; t == "" || t == "jar" || t == "bundle" {
			out = append(out, it.c)
		}
		for _, d := range e.deps {
			scope := d.Scope
			if scope == "" {
				if m, ok := e.managed[d.GroupID+":"+d.ArtifactID+":"+d.Classifier]; ok && m.Scope != "" {
					scope = m.Scope
				}
			}
			if scope != "" && scope != "compile" && scope != "runtime" || d.Optional == "true" {
				continue
			}
			if it.excluded[d.GroupID+":"+d.ArtifactID] || it.excluded[d.GroupID+":*"] || it.excluded["*:*"] {
				continue
			}
			k := d.GroupID + ":" + d.ArtifactID + ":" + d.Classifier
			version := d.Version
			if m, ok := it.managed[k]; ok && m.Version != "" && it.c != root {
				version = m.Version
			} else if version == "" {
				if m, ok := e.managed[k]; ok {
					version = m.Version
				}
			}
			if version == "" {
				return nil, fmt.Errorf("%s: no version for %s:%s", it.c, d.GroupID, d.ArtifactID)
			}
			if strings.HasPrefix(version, "[") || strings.HasPrefix(version, "(") {
				version = strings.Trim(strings.Split(version, ",")[0], "[(] ")
			}
			ex := map[string]bool{}
			for k := range it.excluded {
				ex[k] = true
			}
			for _, x := range d.Exclusions {
				ex[x.GroupID+":"+x.ArtifactID] = true
			}
			queue = append(queue, item{c: Coord{Group: d.GroupID, Artifact: d.ArtifactID, Version: version, Classifier: d.Classifier, Type: d.Type},
				excluded: ex, managed: it.managed})
		}
	}
	return out, nil
}

// Locate returns the repository that serves path.
func (r *Resolver) Locate(ctx context.Context, path string) (string, []byte, error) {
	b, repo, err := r.get(ctx, path)
	return repo, b, err
}

// charsetReader reads the single-byte encodings old POMs declare.
func charsetReader(charset string, in io.Reader) (io.Reader, error) {
	switch strings.ToLower(charset) {
	case "iso-8859-1", "latin1", "latin-1", "windows-1252", "cp1252", "us-ascii", "ascii":
		b, err := io.ReadAll(in)
		if err != nil {
			return nil, err
		}
		var out strings.Builder
		for _, c := range b {
			out.WriteRune(rune(c))
		}
		return strings.NewReader(out.String()), nil
	}
	return nil, fmt.Errorf("unsupported POM encoding %q", charset)
}
