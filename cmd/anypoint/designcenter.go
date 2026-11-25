package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/muratcanozdemir/anypoint-go-client/anypoint"
)

// mulesoft-anypoint-cli-designcenter-plugin (designcenter:project:*) and
// anypoint-cli-datagraph-plugin (datagraph:load-balancer-config:*).

func designCenterCmd() *Cmd {
	return group("designcenter", "Design Center",
		group("project", "API Designer projects",
			leaf("create", "NAME", "Create a project", cmdDesignCreate),
			leaf("delete", "NAME", "Delete a project", cmdDesignDelete).alias("rm"),
			leaf("download", "NAME TARGETDIR", "Download a project's files", cmdDesignDownload),
			leaf("list", "[SEARCHTEXT]", "List projects", cmdDesignList).alias("ls"),
			leaf("publish", "NAME", "Publish a project to Exchange", cmdDesignPublish),
			leaf("upload", "NAME PROJDIR", "Upload a directory's files to a project", cmdDesignUpload),
		))
}

func dataGraphCmd() *Cmd {
	return group("datagraph", "DataGraph",
		group("load-balancer-config", "The dedicated load balancer DataGraph uses",
			leaf("add", "DLBURL", "Use a dedicated load balancer", cmdDataGraphLB("add")),
			leaf("describe", "", "Show the load balancer setting", cmdDataGraphLB("describe")),
			leaf("remove", "", "Stop using a dedicated load balancer", cmdDataGraphLB("remove")),
		))
}

func cmdDataGraphLB(op string) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		pos, c, org, env, err := apiSetup(x, x.Flags(), args)
		if err != nil {
			return err
		}
		switch op {
		case "add":
			if err := c.SetDataGraphLBConfig(x.ctx, org, env, pos[0]); err != nil {
				return fmt.Errorf("unexpected error when adding config: %w", err)
			}
			return x.Done("Load Balancer configuration set.")
		case "remove":
			if err := c.DeleteDataGraphLBConfig(x.ctx, org, env); err != nil {
				return fmt.Errorf("unexpected error when removing config: %w", err)
			}
			return x.Done("Load Balancer configuration unset.")
		}
		cfg, err := c.GetDataGraphLBConfig(x.ctx, org, env)
		if err != nil {
			return fmt.Errorf("unexpected error when loading config: %w", err)
		}
		return x.Print(cfg, func() { printKV(x.Out, "Domain", cfg.Domain, "dlbUrl", cfg.DLBURL) })
	}
}

func findProject(x *Ctx, c *anypoint.Client, org, name string) (*anypoint.DesignProject, error) {
	p, err := c.FindDesignProject(x.ctx, org, name)
	if anypoint.IsNotFound(err) {
		return nil, fmt.Errorf("project %s not found", name)
	}
	return p, err
}

// branchOf returns the branch flag, or the project's default branch.
func branchOf(x *Ctx, c *anypoint.Client, org string, p *anypoint.DesignProject, branch string) (string, error) {
	if branch != "" {
		return branch, nil
	}
	full, err := c.GetDesignProject(x.ctx, org, p.ID)
	if err != nil {
		return "", err
	}
	return full.DefaultBranch, nil
}

var ramlFragmentTypes = []string{"trait", "resource_type", "library", "type", "user_documentation"}

func cmdDesignCreate(x *Ctx, args []string) error {
	fs := x.Flags()
	typ := fs.String("type", "raml", "project `type`: raml, raml-fragment, oas")
	aliasFlag(fs, "t", "type")
	fragment := fs.String("fragmentType", "", "fragment `type` for raml-fragment: "+strings.Join(ramlFragmentTypes, ", ")+", oas-components, json-schema")
	aliasFlag(fs, "f", "fragmentType")
	aliasFlag(fs, "fragment-type", "fragmentType")
	format := fs.String("format", "", "OAS `format`: yaml or json")
	version := fs.String("version", "", "OAS `version`: 2.0 or 3.0")
	aliasFlag(fs, "v", "version")
	pos, c, org, err := orgSetup(x, fs, args)
	if err != nil {
		return err
	}
	req := anypoint.DesignProjectRequest{Name: pos[0], Type: *typ}
	switch *typ {
	case "raml":
	case "oas":
		if *format == "" {
			return errors.New("--format flag is required for OAS project types")
		}
		if *version == "" {
			return errors.New("--version flag is required for OAS project types")
		}
		if err := oneOf("format", *format, "yaml", "json"); err != nil {
			return err
		}
		if err := oneOf("version", *version, "2.0", "3.0"); err != nil {
			return err
		}
		req.Format, req.Version = *format, *version
	case "raml-fragment":
		switch {
		case *fragment == "":
			return errors.New("--fragmentType is required for raml-fragment project types")
		case slices.Contains(ramlFragmentTypes, *fragment):
			req.SubType = *fragment
		case *fragment == "oas-components":
			req.Classifier, req.Format = *fragment, "yaml"
		case *fragment == "json-schema":
			req.Classifier, req.Format = *fragment, "json"
		default:
			return fmt.Errorf("--fragmentType flag value %s unsupported", *fragment)
		}
	default:
		return fmt.Errorf("--type flag value %s unsupported", *typ)
	}
	p, err := c.CreateDesignProject(x.ctx, org, req)
	if err != nil {
		return err
	}
	fmt.Fprintf(x.Err, "Project %s created\n", p.Name)
	return x.Print(p, func() { printKV(x.Out, "Id", p.ID, "Name", p.Name, "Type", p.Type) })
}

func cmdDesignDelete(x *Ctx, args []string) error {
	fs := x.Flags()
	fs.Bool("force", false, "accepted for anypoint-cli-v4 compatibility; deletion never asks")
	aliasFlag(fs, "f", "force")
	pos, c, org, err := orgSetup(x, fs, args)
	if err != nil {
		return err
	}
	p, err := findProject(x, c, org, pos[0])
	if err != nil {
		return err
	}
	if err := c.DeleteDesignProject(x.ctx, org, p.ID); err != nil {
		return err
	}
	return x.Done("Project %s deleted successfully", pos[0])
}

func cmdDesignList(x *Ctx, args []string) error {
	fs := x.Flags()
	pageIndex := fs.Int("pageIndex", 0, "page `number`, from 0")
	pageSize := fs.Int("pageSize", 10, "projects per `page`")
	pos, c, org, err := orgSetup(x, fs, args)
	if err != nil {
		return err
	}
	if *pageSize <= 0 {
		*pageSize = 10
	}
	projects, err := c.ListDesignProjects(x.ctx, org, *pageIndex, *pageSize)
	if err != nil {
		return err
	}
	if q := arg(pos, 0); q != "" {
		projects = slices.DeleteFunc(projects, func(p anypoint.DesignProject) bool { return !strings.Contains(p.Name, q) })
	}
	if projects == nil {
		projects = []anypoint.DesignProject{}
	}
	return x.Print(projects, func() {
		t := newTable(x.Out, "ID", "VERSION", "NAME", "TYPE", "CREATED")
		for _, p := range projects {
			created, _, _ := strings.Cut(p.CreatedDate, "T")
			t.row(p.ID, str(p.Version), strings.TrimSpace(p.Name), p.Type, created)
		}
		t.flush()
	})
}

// dependencyPoll is how often download checks dependency resolution.
var dependencyPoll = 3 * time.Second

// resolveDependencies resolves a branch's Exchange dependencies, waiting up
// to timeout; it reports whether they resolved.
func resolveDependencies(x *Ctx, c *anypoint.Client, org, projectID, branch string, timeout time.Duration) bool {
	invalid, _ := c.ResolveDesignDependencies(x.ctx, org, projectID, branch) // errors: poll the job anyway, as v4 does
	if len(invalid) > 0 {
		fmt.Fprintln(x.Err, "Dependency Resolution failed.")
		fmt.Fprintln(x.Err, "The following dependencies did not resolve properly:")
		for _, d := range invalid {
			msg := "without error message."
			if d.Message != "" {
				msg = "with following message: " + d.Message
			}
			fmt.Fprintf(x.Err, "* %s %s\n", d.AssetID, msg)
		}
		return false
	}
	deadline := time.Now().Add(timeout)
	for {
		if status, err := c.DesignDependencyStatus(x.ctx, org, projectID, branch); err == nil && status == "done" {
			fmt.Fprintln(x.Err, "Dependencies Resolved")
			return true
		}
		if time.Now().After(deadline) {
			fmt.Fprintln(x.Err, "Dependencies Resolution timed out.")
			fmt.Fprintln(x.Err, "Your download will not contain the project dependencies.")
			return false
		}
		select {
		case <-x.ctx.Done():
			return false
		case <-time.After(dependencyPoll):
		}
	}
}

// within joins a server-supplied relative path under dir, refusing paths
// that would leave it.
func within(dir, rel string) (string, error) {
	clean := path.Clean("/" + strings.ReplaceAll(rel, `\`, "/"))
	p := filepath.Join(dir, filepath.FromSlash(clean))
	if p != dir && !strings.HasPrefix(p, dir+string(filepath.Separator)) {
		return "", fmt.Errorf("refusing to write %q outside %s", rel, dir)
	}
	return p, nil
}

func cmdDesignDownload(x *Ctx, args []string) error {
	fs := x.Flags()
	branch := fs.String("branch", "", "`branch` (default: the project's default branch)")
	aliasFlag(fs, "b", "branch")
	timeout := fs.Int("resolveDependenciesTimeout", 2, "`minutes` to wait for Exchange dependencies")
	pos, c, org, err := orgSetup(x, fs, args)
	if err != nil {
		return err
	}
	p, err := findProject(x, c, org, pos[0])
	if err != nil {
		return err
	}
	dir := filepath.Clean(expandHome(pos[1]))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("%s is a file, a directory is expected", pos[1])
	}
	b, err := branchOf(x, c, org, p, *branch)
	if err != nil {
		return err
	}
	fmt.Fprintf(x.Err, "Resolving dependencies... timeout is set to %d minutes\n", *timeout)
	resolved := resolveDependencies(x, c, org, p.ID, b, time.Duration(*timeout)*time.Minute)
	fmt.Fprintln(x.Err, "Getting files list")
	files, err := c.ListDesignFiles(x.ctx, org, p.ID, b)
	if err != nil {
		return err
	}
	for _, f := range files {
		dst, err := within(dir, f.Path)
		if err != nil {
			return err
		}
		fmt.Fprintf(x.Err, "Downloading %s\n", f.Path)
		switch f.Type {
		case "FOLDER":
			if err := os.MkdirAll(dst, 0o750); err != nil {
				return err
			}
		case "FILE":
			content, err := c.GetDesignFile(x.ctx, org, p.ID, b, f.Path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
				return err
			}
			if err := os.WriteFile(dst, content, 0o600); err != nil {
				return err
			}
		}
	}
	how := "successfully"
	if !resolved {
		how = "with warnings"
	}
	return x.Done("Project %s downloaded %s", pos[0], how)
}

// projectFiles reads a directory's files as API Designer paths ("/a/b.raml"),
// sorted; dot files and directories are skipped unless includeDot.
func projectFiles(x *Ctx, dir string, includeDot bool) ([]anypoint.DesignFileContent, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != dir && !includeDot && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(paths)
	out := []anypoint.DesignFileContent{}
	for _, p := range paths {
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil, err
		}
		name := "/" + filepath.ToSlash(rel)
		fmt.Fprintf(x.Err, "Preparing file: %s\n", name)
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, anypoint.DesignFileContent{Path: name, Content: string(b)})
	}
	return out, nil
}

// withLock runs fn holding a branch's lock, releasing it afterwards.
func withLock(x *Ctx, c *anypoint.Client, org, projectID, branch string, fn func() error) (err error) {
	fmt.Fprintln(x.Err, "Acquiring lock")
	if err := c.AcquireDesignLock(x.ctx, org, projectID, branch); err != nil {
		return err
	}
	defer func() {
		fmt.Fprintln(x.Err, "Releasing lock")
		if rerr := c.ReleaseDesignLock(x.ctx, org, projectID, branch); err == nil {
			err = rerr
		}
	}()
	return fn()
}

func cmdDesignUpload(x *Ctx, args []string) error {
	fs := x.Flags()
	includeDot := fs.Bool("include-dot-files", false, "upload files and directories whose names start with a dot")
	aliasFlag(fs, "i", "include-dot-files")
	branch := fs.String("branch", "", "`branch` (default: the project's default branch)")
	aliasFlag(fs, "b", "branch")
	pos, c, org, err := orgSetup(x, fs, args)
	if err != nil {
		return err
	}
	p, err := findProject(x, c, org, pos[0])
	if err != nil {
		return err
	}
	files, err := projectFiles(x, filepath.Clean(expandHome(pos[1])), *includeDot)
	if err != nil {
		return err
	}
	b, err := branchOf(x, c, org, p, *branch)
	if err != nil {
		return err
	}
	err = withLock(x, c, org, p.ID, b, func() error {
		fmt.Fprintf(x.Err, "Uploading %d files\n", len(files))
		return c.SaveDesignFiles(x.ctx, org, p.ID, b, files)
	})
	if err != nil {
		return err
	}
	return x.Done("Project %s uploaded successfully", pos[0])
}

func cmdDesignPublish(x *Ctx, args []string) error {
	fs := x.Flags()
	name := fs.String("name", "", "asset `name` (default: the project's)")
	main := fs.String("main", "", "main `file` (default: exchange.json's)")
	apiVersion := fs.String("apiVersion", "", "API `version` for RAML projects")
	groupID := fs.String("groupId", "", "Exchange group `ID` (default: exchange.json's, or the organization)")
	assetID := fs.String("assetId", "", "Exchange asset `ID` (default: exchange.json's, or the name in lower case)")
	version := fs.String("version", "", "asset `version` (default: exchange.json's)")
	branch := fs.String("branch", "", "`branch` (default: the project's default branch)")
	aliasFlag(fs, "b", "branch")
	status := fs.String("status", "", "asset `status`: development or published (default published)")
	pos, c, org, err := orgSetup(x, fs, args)
	if err != nil {
		return err
	}
	if *status != "" && *status != "development" && *status != "published" {
		return fmt.Errorf("%s is not a valid status. Expected values are 'development' and 'published'", *status)
	}
	p, err := findProject(x, c, org, pos[0])
	if err != nil {
		return err
	}
	fmt.Fprintln(x.Err, "Loading exchange.json")
	b, err := branchOf(x, c, org, p, *branch)
	if err != nil {
		return err
	}
	fmt.Fprintln(x.Err, "Syncing project files")
	if _, err := c.ListDesignFiles(x.ctx, org, p.ID, b); err != nil {
		return err
	}
	ex, err := c.GetDesignExchangeJSON(x.ctx, org, p.ID, b)
	if err != nil {
		return err
	}
	exStr := func(k string) string { s, _ := ex[k].(string); return s }
	err = withLock(x, c, org, p.ID, b, func() error {
		if *groupID != "" && exStr("groupId") != "" && *groupID != exStr("groupId") {
			fmt.Fprintln(x.Err, "Updating exchange.json")
			updated := maps.Clone(ex)
			updated["groupId"] = *groupID
			content, err := json.Marshal(updated)
			if err != nil {
				return err
			}
			if err := c.SaveDesignFiles(x.ctx, org, p.ID, b, []anypoint.DesignFileContent{{Path: "/exchange.json", Content: string(content)}}); err != nil {
				return err
			}
		}
		fmt.Fprintln(x.Err, "Publishing project to Exchange")
		assetName := cmp(*name, cmp(p.Name, exStr("name")))
		tags := []any{}
		if t, ok := ex["tags"].([]any); ok {
			for _, v := range t {
				if !slices.Contains(tags, v) {
					tags = append(tags, v)
				}
			}
		}
		body := anypoint.Doc{"name": assetName, "version": cmp(*version, exStr("version")), "main": cmp(*main, exStr("main")),
			"assetId":    cmp(*assetID, cmp(exStr("assetId"), strings.ToLower(assetName))),
			"groupId":    cmp(*groupID, cmp(exStr("groupId"), cmp(p.OrganizationID, org))),
			"classifier": cmp(exStr("classifier"), p.Type), "tags": tags, "apiVersion": "", "status": cmp(*status, "published"),
			"metadata": anypoint.Doc{"projectId": p.ID, "branchId": b}}
		if p.Type == "raml" {
			body["apiVersion"] = cmp(*apiVersion, exStr("apiVersion"))
		}
		return c.PublishDesignProject(x.ctx, org, p.ID, b, body)
	})
	if err != nil {
		return err
	}
	return x.Done("Project published to Exchange")
}
