package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/muratcanozdemir/anypoint-go-client/anypoint"
)

// anypoint-cli-exchange-plugin: exchange:asset:*.

func exchangeCmd() *Cmd {
	return group("exchange", "Exchange assets",
		group("asset", "Exchange assets",
			leaf("copy", "SOURCE TARGET", "Copy an API asset version (rest-api, soap-api or custom) to another version or organization", cmdExCopy),
			leaf("delete", "ASSETIDENTIFIER", "Delete an asset version (hard delete)", cmdExDelete).alias("rm"),
			leaf("deprecate", "ASSETIDENTIFIER", "Deprecate an asset version", cmdExStatus("deprecated", "Deprecated")),
			leaf("describe", "ASSETIDENTIFIER", "Show an asset version", cmdExDescribe),
			leaf("download", "[ASSETIDENTIFIER] [DIRECTORY]", "Download the main file of an API asset version", cmdExDownload),
			leaf("list", "[SEARCHTEXT]", "Search assets", cmdExList).alias("ls"),
			leaf("modify", "ASSETIDENTIFIER", "Rename an asset or replace a version's tags", cmdExModify),
			leaf("mutableDataUpload", "ASSETIDENTIFIER [PROPERTIES]", "Upload tags, categories, fields and documentation of a published version", cmdExMutableData),
			group("page", "Asset portal pages",
				leaf("delete", "ASSETIDENTIFIER [PAGENAME]", "Delete a page", cmdExPageDelete).alias("rm"),
				leaf("download", "ASSETIDENTIFIER DIRECTORY [PAGENAME]", "Download pages as Markdown (all when PAGENAME is omitted)", cmdExPageDownload),
				leaf("list", "ASSETIDENTIFIER", "List pages", cmdExPageList).alias("ls"),
				leaf("modify", "ASSETIDENTIFIER PAGENAME", "Rename page PAGENAME to --name", cmdExPageModify),
				leaf("update", "ASSETIDENTIFIER PAGENAME MDPATH", "Replace a page's Markdown", cmdExPageUpload(false)),
				leaf("upload", "ASSETIDENTIFIER PAGENAME MDPATH", "Create or replace a page from Markdown (\"home\" is the main page)", cmdExPageUpload(true)),
			),
			group("resource", "Asset portal resources (images)",
				leaf("delete", "ASSETIDENTIFIER RESOURCEPATH", "Delete a resource", cmdExResDelete).alias("rm"),
				leaf("download", "ASSETIDENTIFIER RESOURCEPATH FILEPATH", "Download a published resource", cmdExResDownload),
				leaf("list", "ASSETIDENTIFIER", "List resources", cmdExResList).alias("ls"),
				leaf("upload", "ASSETIDENTIFIER FILEPATH", "Upload a resource and print the Markdown to use it", cmdExResUpload),
			),
			leaf("undeprecate", "ASSETIDENTIFIER", "Undeprecate an asset version", cmdExStatus("published", "Undeprecated")),
			leaf("updateStatus", "ASSETIDENTIFIER", "Change an asset version's status", cmdExUpdateStatus),
			leaf("upload", "ASSETIDENTIFIER [PROPERTIES]", "Publish an asset version", cmdExUpload).alias("publish"),
		),
	)
}

// exchangeSetup parses flags, builds the client and resolves the asset
// identifier in the first positional (if present) against the organization.
func exchangeSetup(x *Ctx, fs *flag.FlagSet, args []string) (pos []string, c *anypoint.Client, org string, ref anypoint.AssetRef, err error) {
	if pos, c, err = x.Setup(fs, args); err != nil {
		return
	}
	if org, err = c.OrgID(x.ctx); err != nil {
		return
	}
	if len(pos) > 0 {
		ref, err = anypoint.ParseAssetRef(pos[0], org)
	}
	return
}

// ignoredFlags registers anypoint-cli-v4 flags that have no effect there either.
func ignoredFlags(fs *flag.FlagSet, names ...string) {
	for _, n := range names {
		switch n {
		case "force":
			fs.Bool("force", false, "ignored (anypoint-cli-v4 compatibility)")
			aliasFlag(fs, "f", "force")
		case "name":
			fs.String("name", "", "ignored (anypoint-cli-v4 compatibility)")
			aliasFlag(fs, "n", "name")
		}
	}
}

// ago renders an RFC 3339 time relative to now, like "3 days ago".
func ago(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "a few seconds ago"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	case d < 2*365*24*time.Hour:
		return fmt.Sprintf("%d months ago", int(d.Hours()/24/30))
	}
	return fmt.Sprintf("%d years ago", int(d.Hours()/24/365))
}

func cmdExList(x *Ctx, args []string) error {
	fs := x.Flags()
	limit := fs.Int("limit", 10, "number of results")
	aliasFlag(fs, "l", "limit")
	offset := fs.Int("offset", 0, "results to skip")
	orgID := fs.String("organizationId", "", "only assets of this organization `ID`")
	pos, c, err := x.Setup(fs, args)
	if err != nil {
		return err
	}
	assets, err := c.SearchAssets(x.ctx, anypoint.AssetSearch{OrganizationID: *orgID, Search: arg(pos, 0), Limit: *limit, Offset: *offset})
	if err != nil {
		return err
	}
	return x.Print(assets, func() {
		t := newTable(x.Out, "GROUP ID", "ASSET ID", "VERSION", "NAME", "TYPE", "CHANGED")
		for _, a := range assets {
			changed := a.UpdatedDate
			if changed == "" {
				changed = a.CreatedDate
			}
			t.row(a.GroupID, a.AssetID, a.Version, a.Name, a.Type, ago(changed))
		}
		t.flush()
	})
}

func cmdExDescribe(x *Ctx, args []string) error {
	_, c, _, ref, err := exchangeSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	a, err := c.GetAsset(x.ctx, ref)
	if err != nil {
		return err
	}
	return x.Print(a, func() {
		kv := []string{"Name", a.Name, "Changed", ago(str(dig(object(a), "modifiedAt"))), "Status", a.Status, "Type", a.Type,
			"API Version", a.ProductAPIVersion}
		versions := func(vs []anypoint.AssetRefDep) string {
			var out []string
			for _, v := range vs {
				out = append(out, v.Version)
			}
			return strings.Join(out, ", ")
		}
		if len(a.ProductAPIVersions) > 0 {
			assetVersions := a.Version
			var apis []string
			for _, p := range a.ProductAPIVersions {
				apis = append(apis, p.ProductAPIVersion)
				if p.ProductAPIVersion == a.ProductAPIVersion {
					assetVersions = versions(p.Versions)
				}
			}
			kv = append(kv, "Asset Versions", assetVersions, "API Versions", strings.Join(apis, ", "))
		} else if len(a.Versions) > 0 {
			kv = append(kv, "Asset Versions", versions(a.Versions))
		}
		printKV(x.Out, kv...)
	})
}

// apiMainFile returns the specification file of an API asset, with
// anypoint-cli-v4's errors for other asset types.
func apiMainFile(a *anypoint.Asset, verb string) (*anypoint.AssetFile, error) {
	switch a.Type {
	case "soap-api", "custom", "rest-api":
	default:
		return nil, errors.New("Invalid asset type. Supported asset types are: soap-api, custom, rest-api") //nolint:staticcheck // v4's message
	}
	f := a.MainFile()
	if f == nil {
		return nil, fmt.Errorf("asset has no files to be %s", verb)
	}
	return f, nil
}

// linkFileName is the base name of a file link's URL path.
func linkFileName(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		return safeFileName(path.Base(link))
	}
	return safeFileName(path.Base(u.Path))
}

// fetch opens a URL; the bearer token is only sent to the Anypoint host.
func fetch(x *Ctx, c *anypoint.Client, link string) (io.ReadCloser, error) {
	resp, err := c.Do(x.ctx, http.MethodGet, link, nil, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// writeFile copies r to path, reporting close errors.
func writeFile(path string, r io.Reader) (int64, error) {
	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return n, err
}

func cmdExDownload(x *Ctx, args []string) error {
	fs := x.Flags()
	ignoredFlags(fs, "force")
	pos, c, _, ref, err := exchangeSetup(x, fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 2 {
		fs.Usage()
		return errors.New("ASSETIDENTIFIER and DIRECTORY are required")
	}
	if err := os.MkdirAll(pos[1], 0o750); err != nil {
		return err
	}
	a, err := c.GetAsset(x.ctx, ref)
	if err != nil {
		return err
	}
	f, err := apiMainFile(a, "downloaded")
	if err != nil {
		return err
	}
	dst := filepath.Join(pos[1], linkFileName(f.Link()))
	fmt.Fprintf(x.Err, "Downloading asset to %s\n", dst)
	body, err := fetch(x, c, f.Link())
	if err != nil {
		return err
	}
	defer body.Close()
	if _, err := writeFile(dst, body); err != nil {
		return err
	}
	return x.Done("Asset downloaded")
}

func cmdExDelete(x *Ctx, args []string) error {
	fs := x.Flags()
	ignoredFlags(fs, "force")
	_, c, org, ref, err := exchangeSetup(x, fs, args)
	if err != nil {
		return err
	}
	if err := c.DeleteAsset(x.ctx, org, ref, true); err != nil {
		return err
	}
	return x.Done("Asset deleted")
}

func cmdExStatus(status, verb string) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		_, c, _, ref, err := exchangeSetup(x, x.Flags(), args)
		if err != nil {
			return err
		}
		if err := c.SetAssetStatus(x.ctx, ref, status); err != nil {
			return err
		}
		return x.Done("Asset %s %s", ref.AssetID, verb)
	}
}

func cmdExUpdateStatus(x *Ctx, args []string) error {
	fs := x.Flags()
	status := fs.String("status", "", "(required) new `status`: development, published or deprecated")
	_, c, _, ref, err := exchangeSetup(x, fs, args)
	if err != nil {
		return err
	}
	if *status == "" {
		return errors.New("missing required flag --status")
	}
	if err := c.SetAssetStatus(x.ctx, ref, *status); err != nil {
		return err
	}
	return x.Done("Asset status modified")
}

func cmdExModify(x *Ctx, args []string) error {
	fs := x.Flags()
	name := fs.String("name", "", "new asset `name`")
	aliasFlag(fs, "n", "name")
	tags := fs.String("tags", "", "comma-separated `tags`, replacing the version's tags")
	aliasFlag(fs, "t", "tags")
	_, c, org, ref, err := exchangeSetup(x, fs, args)
	if err != nil {
		return err
	}
	switch {
	case *name != "":
		if err := c.RenameAsset(x.ctx, org, ref, *name); err != nil {
			return err
		}
		return x.Done("Asset name modified")
	case *tags != "":
		if err := c.SetAssetTags(x.ctx, ref, strings.Split(*tags, ",")); err != nil {
			return err
		}
		return x.Done("Asset tags modified")
	}
	return errors.New("pass --name or --tags")
}

func cmdExCopy(x *Ctx, args []string) error {
	fs := x.Flags()
	targetOrg := fs.String("targetOrganizationId", "", "target organization `ID` (default: the selected one)")
	aliasFlag(fs, "t", "targetOrganizationId")
	pos, c, org, src, err := exchangeSetup(x, fs, args)
	if err != nil {
		return err
	}
	if *targetOrg != "" {
		org = *targetOrg
	}
	tgt, err := anypoint.ParseAssetRef(pos[1], org)
	if err != nil {
		return err
	}
	fmt.Fprintf(x.Err, "Copying asset from %q to %q\n", pos[0], pos[1])
	a, err := c.GetAsset(x.ctx, src)
	if err != nil {
		return err
	}
	f, err := apiMainFile(a, "copied")
	if err != nil {
		return err
	}
	body, err := fetch(x, c, f.Link())
	if err != nil {
		return err
	}
	defer body.Close()
	content, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	apiVersion := a.ProductAPIVersion
	if apiVersion == "" {
		apiVersion = a.VersionGroup
	}
	props := map[string]string{"apiVersion": apiVersion, "mainFile": f.MainFile}
	if a.ContactName != nil {
		props["contactName"] = *a.ContactName
	}
	if a.ContactEmail != nil {
		props["contactEmail"] = *a.ContactEmail
	}
	var deps []string
	for _, d := range a.Dependencies {
		deps = append(deps, d.GroupID+":"+d.AssetID+":"+d.Version)
	}
	req := &anypoint.PublishAssetRequest{
		Name: a.Name, Description: a.Description, Type: a.Type, Properties: props, Dependencies: strings.Join(deps, ","),
		Files: []anypoint.AssetUpload{{Classifier: f.Classifier, Packaging: f.Packaging, FileName: linkFileName(f.Link()), Content: bytes.NewReader(content)}},
	}
	if err := publish(x, c, org, tgt, req); err != nil {
		return err
	}
	return x.Done("Asset copied")
}

// publish starts a publication and waits for it.
func publish(x *Ctx, c *anypoint.Client, org string, ref anypoint.AssetRef, req *anypoint.PublishAssetRequest) error {
	link, err := c.PublishAsset(x.ctx, org, ref, req)
	if err != nil {
		return err
	}
	fmt.Fprintln(x.Err, "Waiting for the publication to finish")
	_, err = c.WaitForPublication(x.ctx, link)
	return err
}

// jsonObjectFlag parses a JSON object flag value (empty: nil).
func jsonObjectFlag(name, value string) (map[string]any, error) {
	if value == "" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(value), &m); err != nil {
		return nil, fmt.Errorf("--%s must be a JSON object: %w", name, err)
	}
	return m, nil
}

// formValues renders JSON values as multipart form values: strings as is,
// arrays comma-joined, anything else as JSON.
func formValues(m map[string]any) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = str(v)
	}
	return out
}

// uploadFiles turns --files ({"classifier.packaging": path}, {"packaging": path}
// or {"classifier": {"packaging": path}}) into uploads.
func uploadFiles(m map[string]any) ([]anypoint.AssetUpload, func(), error) {
	var files []*os.File
	closeAll := func() {
		for _, f := range files {
			_ = f.Close()
		}
	}
	var out []anypoint.AssetUpload
	add := func(classifier, packaging, p string) error {
		f, err := os.Open(expandHome(p))
		if err != nil {
			return err
		}
		files = append(files, f)
		out = append(out, anypoint.AssetUpload{Classifier: classifier, Packaging: packaging, FileName: filepath.Base(p), Content: f})
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch v := m[k].(type) {
		case string:
			classifier, packaging, ok := strings.Cut(k, ".")
			if !ok {
				classifier, packaging = "", k
			}
			if err := add(classifier, packaging, v); err != nil {
				closeAll()
				return nil, nil, err
			}
		case map[string]any:
			for packaging, p := range v {
				if err := add(k, packaging, str(p)); err != nil {
					closeAll()
					return nil, nil, err
				}
			}
		default:
			closeAll()
			return nil, nil, fmt.Errorf("--files: %q must map to a path or an object", k)
		}
	}
	return out, closeAll, nil
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[1:])
		}
	}
	return p
}

func cmdExUpload(x *Ctx, args []string) error {
	fs := x.Flags()
	name := fs.String("name", "", "asset `name` (required without a pom file)")
	desc := fs.String("description", "", "asset `description`")
	typ := fs.String("type", "", "asset `type`: rest-api, soap-api, http-api, raml-fragment, custom, connector, template, example, policy, app, extension, external-library (inferred from the files when omitted)")
	keywords := fs.String("keywords", "", "comma-separated `keywords`")
	tags := fs.String("tags", "", "comma-separated `tags`")
	categories := fs.String("categories", "", "categories as a JSON `object`")
	fields := fs.String("fields", "", "custom fields as a JSON `object`")
	deps := fs.String("dependencies", "", "comma-separated `groupId:assetId:version` list")
	props := fs.String("properties", "", `properties as a JSON object, e.g. {"mainFile":"api.raml","apiVersion":"v1"}`)
	files := fs.String("files", "", `files as a JSON object of classifier.packaging (or packaging) to path, e.g. {"raml.zip":"api.zip","pom.xml":"pom.xml"}`)
	status := fs.String("status", "", "`status`: development or published")
	headers := fs.String("headers", "", "request headers as a JSON `object` (x-user-id, assign-roles)")
	pos, c, org, ref, err := exchangeSetup(x, fs, args)
	if err != nil {
		return err
	}
	if *status != "" && *status != "development" && *status != "published" {
		return fmt.Errorf("--status must be development or published")
	}
	if *props == "" {
		*props = arg(pos, 1) // PROPERTIES may also be given as an argument
	}
	req := &anypoint.PublishAssetRequest{Name: *name, Description: *desc, Type: *typ, Keywords: *keywords, Tags: *tags,
		Dependencies: *deps, Status: *status, Headers: http.Header{}}
	for flagName, dst := range map[string]*map[string]string{"categories": &req.Categories, "fields": &req.Fields, "properties": &req.Properties} {
		val := map[string]string{"categories": *categories, "fields": *fields, "properties": *props}[flagName]
		m, err := jsonObjectFlag(flagName, val)
		if err != nil {
			return err
		}
		*dst = formValues(m)
	}
	hm, err := jsonObjectFlag("headers", *headers)
	if err != nil {
		return err
	}
	for k, v := range hm {
		req.Headers.Set(k, str(v))
	}
	fm, err := jsonObjectFlag("files", *files)
	if err != nil {
		return err
	}
	uploads, closeFiles, err := uploadFiles(fm)
	if err != nil {
		return err
	}
	defer closeFiles()
	req.Files = uploads
	if req.Name == "" && req.Type == "" && len(req.Files) == 0 && len(req.Properties) == 0 {
		return errors.New("no arguments have been specified")
	}
	fmt.Fprintf(x.Err, "Uploading asset %s\n", ref)
	if err := publish(x, c, org, ref, req); err != nil {
		return err
	}
	return x.Done("Publishing completed")
}

func cmdExMutableData(x *Ctx, args []string) error {
	fs := x.Flags()
	tags := fs.String("tags", "", "comma-separated `tags`")
	categories := fs.String("categories", "", "categories as a JSON `object`")
	fields := fs.String("fields", "", "custom fields as a JSON `object`")
	docs := fs.String("docs", "", "documentation zip `file`")
	_, c, org, ref, err := exchangeSetup(x, fs, args)
	if err != nil {
		return err
	}
	req := &anypoint.MutableDataRequest{Tags: *tags}
	cm, err := jsonObjectFlag("categories", *categories)
	if err != nil {
		return err
	}
	fm, err := jsonObjectFlag("fields", *fields)
	if err != nil {
		return err
	}
	req.Categories, req.Fields = formValues(cm), formValues(fm)
	if *docs != "" {
		f, err := os.Open(expandHome(*docs))
		if err != nil {
			return err
		}
		defer f.Close()
		req.Docs = f
	}
	if req.Tags == "" && req.Categories == nil && req.Fields == nil && req.Docs == nil {
		return errors.New("no arguments have been specified")
	}
	link, err := c.UpdateAssetMutableData(x.ctx, org, ref, req)
	if err != nil {
		return err
	}
	if _, err := c.WaitForPublication(x.ctx, link); err != nil {
		return err
	}
	return x.Done("Publishing completed")
}

// ---------------------------------------------------------------- pages

// pagePaths returns the paths of the published pages, or of the one whose
// name or path is pageName.
func pagePaths(x *Ctx, c *anypoint.Client, ref anypoint.AssetRef, pageName string) ([]string, error) {
	pages, err := c.ListPortalPages(x.ctx, ref, false)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range pages {
		if pageName == "" || p.Name == pageName || p.Path == pageName {
			out = append(out, p.Path)
		}
	}
	if pageName != "" && len(out) == 0 {
		return nil, errors.New("unable to find page")
	}
	return out, nil
}

func cmdExPageList(x *Ctx, args []string) error {
	_, c, _, ref, err := exchangeSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	pages, err := c.ListPortalPages(x.ctx, ref, false)
	if err != nil {
		return err
	}
	return x.Print(pages, func() { printTable(x.Out, pages, col("NAME", "name"), col("PATH", "path")) })
}

func cmdExPageDownload(x *Ctx, args []string) error {
	pos, c, _, ref, err := exchangeSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(pos[1], 0o750); err != nil {
		return err
	}
	paths, err := pagePaths(x, c, ref, arg(pos, 2))
	if err != nil {
		return err
	}
	for _, p := range paths {
		name := safeFileName(path.Base(p))
		fmt.Fprintf(x.Err, "Downloading asset page %q\n", name)
		md, err := c.GetPortalPage(x.ctx, ref, p, false)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(pos[1], name+".md"), []byte(md), 0o644); err != nil { //nolint:gosec // documentation, not secrets
			return err
		}
	}
	return nil
}

// draftEdit makes a change in the portal draft and publishes it.
func draftEdit(x *Ctx, c *anypoint.Client, ref anypoint.AssetRef, change func() error) error {
	if err := c.StartPortalDraft(x.ctx, ref); err != nil {
		return err
	}
	if err := change(); err != nil {
		return err
	}
	return c.PublishPortalDraft(x.ctx, ref)
}

func cmdExPageDelete(x *Ctx, args []string) error {
	fs := x.Flags()
	ignoredFlags(fs, "name", "force")
	pos, c, _, ref, err := exchangeSetup(x, fs, args)
	if err != nil {
		return err
	}
	paths, err := pagePaths(x, c, ref, arg(pos, 1))
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return errors.New("unable to find page")
	}
	if err := draftEdit(x, c, ref, func() error { return c.DeletePortalPage(x.ctx, ref, paths[0]) }); err != nil {
		return err
	}
	return x.Done("Asset page deleted")
}

func cmdExPageModify(x *Ctx, args []string) error {
	fs := x.Flags()
	name := fs.String("name", "", "(required) new page `name`")
	aliasFlag(fs, "n", "name")
	ignoredFlags(fs, "force")
	pos, c, _, ref, err := exchangeSetup(x, fs, args)
	if err != nil {
		return err
	}
	if *name == "" {
		return errors.New("missing required flag --name")
	}
	fmt.Fprintf(x.Err, "Renaming asset page %q to %q\n", pos[1], *name)
	if err := draftEdit(x, c, ref, func() error { return c.CreatePortalPage(x.ctx, ref, *name, pos[1]) }); err != nil {
		return err
	}
	return x.Done("Asset page modified")
}

func cmdExPageUpload(create bool) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		fs := x.Flags()
		ignoredFlags(fs, "name", "force")
		pos, c, _, ref, err := exchangeSetup(x, fs, args)
		if err != nil {
			return err
		}
		md, err := os.ReadFile(expandHome(pos[2]))
		if err != nil {
			return err
		}
		err = draftEdit(x, c, ref, func() error {
			if create {
				if err := c.CreatePortalPage(x.ctx, ref, pos[1], ""); err != nil && !anypoint.IsConflict(err) {
					return err
				}
			}
			return c.UpdatePortalPage(x.ctx, ref, pos[1], string(md))
		})
		if err != nil {
			return err
		}
		return x.Done("Asset page uploaded and published")
	}
}

// ---------------------------------------------------------------- resources

func cmdExResList(x *Ctx, args []string) error {
	fs := x.Flags()
	draft := fs.Bool("draft", false, "list the draft's resources")
	aliasFlag(fs, "d", "draft")
	_, c, _, ref, err := exchangeSetup(x, fs, args)
	if err != nil {
		return err
	}
	res, err := c.ListPortalResources(x.ctx, ref, *draft)
	if err != nil {
		return err
	}
	return x.Print(res, func() { printTable(x.Out, res, col("RESOURCE PATH", "path")) })
}

func cmdExResDownload(x *Ctx, args []string) error {
	fs := x.Flags()
	ignoredFlags(fs, "name", "force")
	pos, c, _, ref, err := exchangeSetup(x, fs, args)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(pos[2]); dir != "" {
		if _, err := os.Stat(dir); err != nil {
			return fmt.Errorf("no such directory: %s", dir)
		}
	}
	body, err := c.DownloadPortalResource(x.ctx, ref, pos[1])
	if err != nil {
		return err
	}
	defer body.Close()
	if _, err := writeFile(pos[2], body); err != nil {
		return err
	}
	return x.Done("Resource downloaded")
}

func cmdExResUpload(x *Ctx, args []string) error {
	fs := x.Flags()
	ignoredFlags(fs, "name", "force")
	pos, c, _, ref, err := exchangeSetup(x, fs, args)
	if err != nil {
		return err
	}
	f, err := os.Open(expandHome(pos[1]))
	if err != nil {
		return err
	}
	defer f.Close()
	res, err := c.UploadPortalResource(x.ctx, ref, filepath.Base(pos[1]), f)
	if err != nil {
		return err
	}
	return x.Print(res, func() {
		name := strings.TrimSuffix(filepath.Base(pos[1]), filepath.Ext(pos[1]))
		fmt.Fprintf(x.Out, "Resource uploaded. To use it, add this Markdown to a page:\n\n![%s](%s)\n", name, res.Path)
	})
}

func cmdExResDelete(x *Ctx, args []string) error {
	pos, c, _, ref, err := exchangeSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := c.DeletePortalResource(x.ctx, ref, pos[1]); err != nil {
		return err
	}
	return x.Done("Resource deleted: %s", pos[1])
}

// safeFileName replaces path separators so a server-supplied name never
// leaves the directory it is joined to.
func safeFileName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 {
			return '_'
		}
		return r
	}, name)
	if strings.Trim(name, ".") == "" {
		return "file"
	}
	return name
}
