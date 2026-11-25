package anypoint

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Exchange assets. Endpoints and request shapes follow anypoint-cli-v4's
// exchange plugin; type names, and the operations the CLI lacks (categories,
// custom fields, external instances), follow the Terraform provider.

// AssetRef identifies an asset version. Version may be empty where a whole
// asset is meant.
type AssetRef struct {
	GroupID, AssetID, Version string
}

func (r AssetRef) String() string {
	return strings.TrimSuffix(r.GroupID+"/"+r.AssetID+"/"+r.Version, "/")
}

// ParseAssetRef parses "[groupId/]assetId/version" (the anypoint-cli-v4
// notation); the group defaults to defaultGroup, usually the organization ID.
func ParseAssetRef(s, defaultGroup string) (AssetRef, error) {
	p := strings.Split(s, "/")
	switch len(p) {
	case 2:
		return AssetRef{defaultGroup, p[0], p[1]}, nil
	case 3:
		return AssetRef{p[0], p[1], p[2]}, nil
	}
	return AssetRef{}, fmt.Errorf("invalid asset identifier %q: want [groupId/]assetId/version", s)
}

// v1 is the path anypoint-cli-v4 uses to read an asset and publish its portal.
func (r AssetRef) v1() string {
	return "/exchange/api/v1/assets/" + esc(r.GroupID) + "/" + esc(r.AssetID) + "/" + esc(r.Version)
}

func (r AssetRef) orgV1(orgID string) string {
	return "/exchange/api/v1/organizations/" + esc(orgID) + "/assets/" + esc(r.GroupID) + "/" + esc(r.AssetID) + "/" + esc(r.Version)
}

func (r AssetRef) v2() string {
	p := "/exchange/api/v2/assets/" + esc(r.GroupID) + "/" + esc(r.AssetID)
	if r.Version != "" {
		p += "/" + esc(r.Version)
	}
	return p
}

// Asset is an Exchange asset version.
type Asset struct {
	rawJSON
	ID                 string        `json:"id,omitempty"`
	GroupID            string        `json:"groupId"`
	AssetID            string        `json:"assetId"`
	Version            string        `json:"version"`
	MinorVersion       string        `json:"minorVersion,omitempty"`
	VersionGroup       string        `json:"versionGroup,omitempty"`
	ProductAPIVersion  string        `json:"productAPIVersion,omitempty"`
	Name               string        `json:"name"`
	Description        string        `json:"description,omitempty"`
	ContactName        *string       `json:"contactName,omitempty"`
	ContactEmail       *string       `json:"contactEmail,omitempty"`
	Type               string        `json:"type"`
	Status             string        `json:"status,omitempty"`
	IsPublic           bool          `json:"isPublic,omitempty"`
	IsSnapshot         bool          `json:"isSnapshot,omitempty"`
	CreatedDate        string        `json:"createdDate,omitempty"`
	UpdatedDate        string        `json:"updatedDate,omitempty"`
	Labels             []string      `json:"labels,omitempty"`
	Categories         []Category    `json:"categories,omitempty"`
	CustomFields       []CustomField `json:"customFields,omitempty"`
	Files              []AssetFile   `json:"files,omitempty"`
	Dependencies       []AssetRefDep `json:"dependencies,omitempty"`
	Versions           []AssetRefDep `json:"versions,omitempty"`
	ProductAPIVersions []struct {
		ProductAPIVersion string        `json:"productAPIVersion"`
		Versions          []AssetRefDep `json:"versions"`
	} `json:"productAPIVersions,omitempty"`
}

// AssetRefDep is an asset reference as it appears in dependencies and versions.
type AssetRefDep struct {
	GroupID string `json:"groupId,omitempty"`
	AssetID string `json:"assetId,omitempty"`
	Version string `json:"version"`
}

// Category is an asset category with its values.
type Category struct {
	Key   string   `json:"key"`
	Value []string `json:"value"`
}

// CustomField is an asset custom field.
type CustomField struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// AssetFile is a file of an asset version.
type AssetFile struct {
	Classifier   string `json:"classifier"`
	Packaging    string `json:"packaging"`
	DownloadURL  string `json:"downloadURL,omitempty"`
	ExternalLink string `json:"externalLink,omitempty"`
	MD5          string `json:"md5,omitempty"`
	SHA1         string `json:"sha1,omitempty"`
	MainFile     string `json:"mainFile,omitempty"`
	IsGenerated  bool   `json:"isGenerated,omitempty"`
}

// Link is the URL to fetch the file from: externalLink, else downloadURL.
func (f AssetFile) Link() string {
	if f.ExternalLink != "" {
		return f.ExternalLink
	}
	return f.DownloadURL
}

// MainFile returns an API asset's specification file (wsdl, oas/raml or
// custom, not generated), as anypoint-cli-v4 picks it, or nil.
func (a *Asset) MainFile() *AssetFile {
	classifiers := map[string][]string{"soap-api": {"wsdl"}, "custom": {"custom"}, "rest-api": {"oas", "raml"}}[a.Type]
	for i, f := range a.Files {
		for _, c := range classifiers {
			if f.Classifier == c && !f.IsGenerated {
				return &a.Files[i]
			}
		}
	}
	return nil
}

// AssetSearch filters SearchAssets.
type AssetSearch struct {
	OrganizationID string // assets of this organization; all visible assets if empty
	Search         string
	Limit          int // default 10
	Offset         int
}

// searchAssetsQuery is anypoint-cli-v4's Exchange search.
const searchAssetsQuery = `query($organizationIds: [String], $limit: Int, $offset: Int, $searchText: String!) {
  assets(limit: $limit, offset: $offset, searchTerm: $searchText, organizationIds: $organizationIds) {
    groupId
    assetId
    version
    name
    type
    updatedDate
    createdDate
  }
}`

// SearchAssets searches Exchange through the Anypoint GraphQL API.
func (c *Client) SearchAssets(ctx context.Context, s AssetSearch) ([]Asset, error) {
	if s.Limit == 0 {
		s.Limit = 10
	}
	vars := map[string]any{"limit": s.Limit, "offset": s.Offset, "searchText": s.Search}
	if s.OrganizationID != "" {
		vars["organizationIds"] = []string{s.OrganizationID}
	}
	var out struct {
		Data struct {
			Assets json.RawMessage `json:"assets"`
		} `json:"data"`
	}
	if err := c.GraphQL(ctx, searchAssetsQuery, vars, &out); err != nil {
		return nil, err
	}
	return decodeList[Asset](out.Data.Assets)
}

// GraphQL runs a query against the Anypoint GraphQL API (/graph/api/v2/graphql)
// and decodes the whole response ({"data": ...}) into out. GraphQL errors are
// returned as an error.
func (c *Client) GraphQL(ctx context.Context, query string, variables map[string]any, out any) error {
	if variables == nil {
		variables = map[string]any{}
	}
	if err := c.ensureToken(ctx); err != nil {
		return err
	}
	variables["accessToken"] = c.currentToken() // the graph API reads the token from here
	var raw json.RawMessage
	if err := c.JSON(ctx, http.MethodPost, "/graph/api/v2/graphql", map[string]any{"query": query, "variables": variables}, &raw); err != nil {
		return err
	}
	var errs struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(raw, &errs) == nil && len(errs.Errors) > 0 {
		var msgs []string
		for _, e := range errs.Errors {
			msgs = append(msgs, e.Message)
		}
		return fmt.Errorf("graphql: %s", strings.Join(msgs, ", "))
	}
	return decode(raw, out)
}

// GetAsset returns an asset version, with its other versions.
func (c *Client) GetAsset(ctx context.Context, ref AssetRef) (*Asset, error) {
	return get[Asset](ctx, c, ref.v1())
}

// UpdateAssetRequest changes the fields that are set, for all versions.
type UpdateAssetRequest struct {
	Name         *string `json:"name,omitempty"`
	Description  *string `json:"description,omitempty"`
	ContactName  *string `json:"contactName,omitempty"`
	ContactEmail *string `json:"contactEmail,omitempty"`
}

// UpdateAsset changes an asset's name, description or contact (the
// provider's call; anypoint-cli-v4 renames with RenameAsset).
func (c *Client) UpdateAsset(ctx context.Context, groupID, assetID string, req *UpdateAssetRequest) error {
	return c.JSON(ctx, http.MethodPatch, AssetRef{GroupID: groupID, AssetID: assetID}.v2(), req, nil)
}

// RenameAsset changes an asset's name, as anypoint-cli-v4 does.
func (c *Client) RenameAsset(ctx context.Context, orgID string, ref AssetRef, name string) error {
	return c.JSON(ctx, http.MethodPatch, ref.orgV1(orgID)+"/metadata", map[string]string{"name": name}, nil)
}

// DeleteAsset deletes an asset version of an organization. A hard delete
// (what anypoint-cli-v4 does) frees the coordinates for publishing again.
func (c *Client) DeleteAsset(ctx context.Context, orgID string, ref AssetRef, hard bool) error {
	typ := "soft-delete"
	if hard {
		typ = "hard-delete"
	}
	resp, err := c.Do(ctx, http.MethodDelete, ref.orgV1(orgID), nil, http.Header{"X-Delete-Type": {typ}})
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// SetAssetStatus sets a version's lifecycle status: development, published or deprecated.
func (c *Client) SetAssetStatus(ctx context.Context, ref AssetRef, status string) error {
	return c.JSON(ctx, http.MethodPut, ref.v2()+"/status", map[string]string{"status": status}, nil)
}

// Tag is an asset tag (label).
type Tag struct {
	Value   string `json:"value"`
	Key     string `json:"key,omitempty"`
	Mutable bool   `json:"mutable"`
}

// SetAssetTags replaces a version's tags. Tags with an empty key use the
// value as key, and all are mutable, as anypoint-cli-v4 sends them.
func (c *Client) SetAssetTags(ctx context.Context, ref AssetRef, tags []string) error {
	body := make([]Tag, 0, len(tags))
	for _, t := range tags {
		body = append(body, Tag{Value: t, Key: t, Mutable: true})
	}
	return c.JSON(ctx, http.MethodPut, ref.v1()+"/tags", body, nil)
}

// SetAssetCategory sets the values of one category on a version.
func (c *Client) SetAssetCategory(ctx context.Context, ref AssetRef, key string, values []string) error {
	return c.JSON(ctx, http.MethodPut, ref.v2()+"/tags/categories/"+esc(key), map[string][]string{"tagValue": values}, nil)
}

// DeleteAssetCategory removes a category from a version.
func (c *Client) DeleteAssetCategory(ctx context.Context, ref AssetRef, key string) error {
	return c.JSON(ctx, http.MethodDelete, ref.v2()+"/tags/categories/"+esc(key), nil, nil)
}

// SetAssetCustomField sets a custom field on a version.
func (c *Client) SetAssetCustomField(ctx context.Context, ref AssetRef, key string, value any) error {
	return c.JSON(ctx, http.MethodPut, ref.v2()+"/tags/fields/"+esc(key), map[string]any{"tagValue": value}, nil)
}

// DeleteAssetCustomField removes a custom field from a version.
func (c *Client) DeleteAssetCustomField(ctx context.Context, ref AssetRef, key string) error {
	return c.JSON(ctx, http.MethodDelete, ref.v2()+"/tags/fields/"+esc(key), nil, nil)
}

// ---------------------------------------------------------------- publication

// AssetUpload is one file of a publication. Packaging is the file's
// extension (jar, zip, json, ...); the form field is files.classifier.packaging.
type AssetUpload struct {
	Classifier string
	Packaging  string
	FileName   string
	Content    io.Reader
}

// PublishAssetRequest is the multipart publication of an asset version, as
// documented for Exchange's asset creation endpoint.
type PublishAssetRequest struct {
	Name         string
	Description  string
	Type         string            // rest-api, soap-api, http-api, raml-fragment, custom, connector, template, example, policy, app, extension, external-library, ...
	Status       string            // development or published
	Keywords     string            // comma-separated
	Tags         string            // comma-separated
	Dependencies string            // comma-separated groupId:assetId:version
	Properties   map[string]string // e.g. apiVersion, mainFile, assetLink
	Fields       map[string]string // custom fields
	Categories   map[string]string
	Files        []AssetUpload
	Headers      http.Header // e.g. x-user-id, assign-roles
}

// PublicationStatus is the state of an asynchronous publication.
type PublicationStatus struct {
	rawJSON
	ID     string `json:"id,omitempty"`
	Status string `json:"status"` // running, completed or error
	Steps  []struct {
		Name       string   `json:"name"`
		Status     string   `json:"status"`
		StatusCode int      `json:"statusCode"`
		Errors     []string `json:"errors"`
	} `json:"steps,omitempty"`
}

// Err describes a failed publication, or returns nil.
func (s *PublicationStatus) Err() error {
	if s.Status != "error" {
		return nil
	}
	for _, st := range s.Steps {
		if len(st.Errors) > 0 {
			return fmt.Errorf("publication failed (status code %d): %s", st.StatusCode, strings.Join(st.Errors, " "))
		}
	}
	return fmt.Errorf("publication failed")
}

func writeFields(w *multipart.Writer, prefix string, m map[string]string) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := w.WriteField(prefix+k, m[k]); err != nil {
			return err
		}
	}
	return nil
}

func (r *PublishAssetRequest) multipart(orgID string) (*bytes.Buffer, string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, f := range [][2]string{{"name", r.Name}, {"description", r.Description}, {"type", r.Type},
		{"status", r.Status}, {"keywords", r.Keywords}, {"tags", r.Tags}, {"dependencies", r.Dependencies},
		{"organizationId", orgID}} {
		if f[1] != "" {
			if err := w.WriteField(f[0], f[1]); err != nil {
				return nil, "", err
			}
		}
	}
	for prefix, m := range map[string]map[string]string{"properties.": r.Properties, "fields.": r.Fields, "categories.": r.Categories} {
		if err := writeFields(w, prefix, m); err != nil {
			return nil, "", err
		}
	}
	for _, f := range r.Files {
		if err := writeFilePart(w, f); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &body, w.FormDataContentType(), nil
}

func writeFilePart(w *multipart.Writer, f AssetUpload) error {
	field := "files." + f.Packaging
	if f.Classifier != "" {
		field = "files." + f.Classifier + "." + f.Packaging
	}
	name := f.FileName
	if name == "" {
		name = strings.Trim(f.Classifier+"."+f.Packaging, ".")
	}
	part, err := w.CreateFormFile(field, filepath.Base(name))
	if err != nil {
		return err
	}
	_, err = io.Copy(part, f.Content)
	return err
}

func (c *Client) multipartRequest(ctx context.Context, method, path string, body *bytes.Buffer, contentType string, hdr http.Header, out any) error {
	h := http.Header{}
	for k, v := range hdr {
		h[http.CanonicalHeaderKey(k)] = v
	}
	h.Set("Content-Type", contentType)
	h.Set("X-From-Service", "anypoint-cli")
	h.Set("X-From-Service-Version", "v2")
	resp, err := c.Do(ctx, method, path, body.Bytes(), h)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil || out == nil || len(bytes.TrimSpace(data)) == 0 {
		return err
	}
	return decode(data, out)
}

func orgAssetPath(orgID string, ref AssetRef) string {
	return "/exchange/api/v2/organizations/" + esc(orgID) + "/assets/" + esc(ref.GroupID) + "/" + esc(ref.AssetID) + "/" + esc(ref.Version)
}

// PublishAsset starts publishing an asset version and returns the link to
// poll with WaitForPublication.
func (c *Client) PublishAsset(ctx context.Context, orgID string, ref AssetRef, req *PublishAssetRequest) (statusLink string, err error) {
	body, ct, err := req.multipart(orgID)
	if err != nil {
		return "", err
	}
	var out struct {
		PublicationStatusLink string `json:"publicationStatusLink"`
	}
	if err := c.multipartRequest(ctx, http.MethodPost, orgAssetPath(orgID, ref), body, ct, req.Headers, &out); err != nil {
		return "", err
	}
	return out.PublicationStatusLink, nil
}

// MutableDataRequest updates what can change after publication.
type MutableDataRequest struct {
	Tags       string // comma-separated
	Fields     map[string]string
	Categories map[string]string
	Docs       io.Reader // documentation zip, published as files.docs.zip
}

// UpdateAssetMutableData updates tags, categories, fields and documentation
// of a published version and returns the link to poll with WaitForPublication.
func (c *Client) UpdateAssetMutableData(ctx context.Context, orgID string, ref AssetRef, req *MutableDataRequest) (statusLink string, err error) {
	pr := &PublishAssetRequest{Tags: req.Tags, Fields: req.Fields, Categories: req.Categories}
	if req.Docs != nil {
		pr.Files = []AssetUpload{{Classifier: "docs", Packaging: "zip", FileName: "docs.zip", Content: req.Docs}}
	}
	body, ct, err := pr.multipart("")
	if err != nil {
		return "", err
	}
	var out struct {
		MutableDataStatusLink string `json:"mutableDataStatusLink"`
	}
	if err := c.multipartRequest(ctx, http.MethodPatch, orgAssetPath(orgID, ref)+"/mutabledata", body, ct, nil, &out); err != nil {
		return "", err
	}
	return out.MutableDataStatusLink, nil
}

// WaitForPublication polls a publication or mutable-data status link until it
// completes or fails.
func (c *Client) WaitForPublication(ctx context.Context, statusLink string) (*PublicationStatus, error) {
	if statusLink == "" {
		return nil, fmt.Errorf("no publication status link in the response")
	}
	delay := 500 * time.Millisecond
	for {
		st, err := get[PublicationStatus](ctx, c, statusLink)
		if err != nil {
			return nil, err
		}
		if st.Status == "completed" || st.Status == "error" {
			return st, st.Err()
		}
		select {
		case <-ctx.Done():
			return st, ctx.Err()
		case <-time.After(delay):
		}
		delay = min(delay*2, 5*time.Second)
	}
}

// ---------------------------------------------------------------- portal

// PortalPage is a documentation page of an asset version.
type PortalPage struct {
	rawJSON
	Name      string `json:"name"`
	Path      string `json:"path"`
	Synthetic bool   `json:"synthetic,omitempty"`
}

func portal(ref AssetRef, draft bool) string {
	if draft {
		return ref.v2() + "/portal/draft"
	}
	return ref.v2() + "/portal"
}

// ListPortalPages lists the published (or draft) pages of a version.
func (c *Client) ListPortalPages(ctx context.Context, ref AssetRef, draft bool) ([]PortalPage, error) {
	return list[PortalPage](ctx, c, portal(ref, draft)+"/pages")
}

// GetPortalPage returns a page's Markdown.
func (c *Client) GetPortalPage(ctx context.Context, ref AssetRef, pagePath string, draft bool) (string, error) {
	resp, err := c.Do(ctx, http.MethodGet, portal(ref, draft)+"/pages/"+esc(pagePath), nil, http.Header{"Accept": {"text/markdown"}})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

// StartPortalDraft makes sure the version's portal has a draft to edit.
func (c *Client) StartPortalDraft(ctx context.Context, ref AssetRef) error {
	return c.JSON(ctx, http.MethodGet, portal(ref, true), nil, nil)
}

// CreatePortalPage creates an empty draft page. With moveFrom, the page at
// that path is renamed to pagePath instead.
func (c *Client) CreatePortalPage(ctx context.Context, ref AssetRef, pagePath, moveFrom string) error {
	var hdr http.Header
	if moveFrom != "" {
		hdr = http.Header{"X-Move-Source": {moveFrom}}
	}
	b, _ := json.Marshal(map[string]string{"pagePath": pagePath})
	resp, err := c.Do(ctx, http.MethodPost, portal(ref, true)+"/pages", b, hdr)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// UpdatePortalPage replaces a draft page's Markdown.
func (c *Client) UpdatePortalPage(ctx context.Context, ref AssetRef, pagePath, markdown string) error {
	resp, err := c.Do(ctx, http.MethodPut, portal(ref, true)+"/pages/"+esc(pagePath), []byte(markdown),
		http.Header{"Content-Type": {"text/markdown"}})
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// DeletePortalPage deletes a draft page.
func (c *Client) DeletePortalPage(ctx context.Context, ref AssetRef, pagePath string) error {
	return c.JSON(ctx, http.MethodDelete, portal(ref, true)+"/pages/"+esc(pagePath), nil, nil)
}

// PublishPortalDraft publishes the portal draft of a version.
func (c *Client) PublishPortalDraft(ctx context.Context, ref AssetRef) error {
	return c.JSON(ctx, http.MethodPatch, ref.v1(), nil, nil)
}

// PortalResource is an image or other file used by portal pages.
type PortalResource struct {
	rawJSON
	Path string `json:"path"`
}

// ListPortalResources lists the published (or draft) resources of a version.
func (c *Client) ListPortalResources(ctx context.Context, ref AssetRef, draft bool) ([]PortalResource, error) {
	return list[PortalResource](ctx, c, portal(ref, draft)+"/resources")
}

// UploadPortalResource uploads a file to the portal draft; the result's Path
// is what pages reference, as ![name](path).
func (c *Client) UploadPortalResource(ctx context.Context, ref AssetRef, fileName string, content io.Reader) (*PortalResource, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	ext := strings.TrimPrefix(filepath.Ext(fileName), ".")
	if err := writeFilePart(w, AssetUpload{Packaging: ext, FileName: fileName, Content: content}); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	var out PortalResource
	return &out, c.multipartRequest(ctx, http.MethodPost, portal(ref, true)+"/resources", &body, w.FormDataContentType(), nil, &out)
}

// DownloadPortalResource opens a published resource; the caller closes it.
// resourcePath is as listed, e.g. "resources/image-1234.png".
func (c *Client) DownloadPortalResource(ctx context.Context, ref AssetRef, resourcePath string) (io.ReadCloser, error) {
	resp, err := c.Do(ctx, http.MethodGet, portal(ref, false)+"/"+strings.TrimPrefix(resourcePath, "/"), nil, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// DeletePortalResource deletes a resource from the portal draft.
func (c *Client) DeletePortalResource(ctx context.Context, ref AssetRef, resourcePath string) error {
	return c.JSON(ctx, http.MethodDelete, portal(ref, true)+"/"+strings.TrimPrefix(resourcePath, "/"), nil, nil)
}

// ---------------------------------------------------------------- external instances

// ExternalInstance is an API instance registered in Exchange outside API Manager.
type ExternalInstance struct {
	rawJSON
	ID                string `json:"id,omitempty"`
	Name              string `json:"name"`
	EndpointURI       string `json:"endpointUri"`
	IsPublic          bool   `json:"isPublic"`
	Type              string `json:"type,omitempty"`
	OrganizationID    string `json:"organizationId,omitempty"`
	GroupID           string `json:"groupId,omitempty"`
	AssetID           string `json:"assetId,omitempty"`
	ProductAPIVersion string `json:"productApiVersion,omitempty"`
}

func instancesPath(groupID, assetID, versionGroup string) string {
	return AssetRef{GroupID: groupID, AssetID: assetID}.v2() + "/versionGroups/" + esc(versionGroup) + "/instances"
}

// ListAssetInstances lists the instances of an API's version group (e.g. v1).
func (c *Client) ListAssetInstances(ctx context.Context, groupID, assetID, versionGroup string) ([]ExternalInstance, error) {
	return list[ExternalInstance](ctx, c, instancesPath(groupID, assetID, versionGroup))
}

// CreateExternalInstance registers an external instance.
func (c *Client) CreateExternalInstance(ctx context.Context, groupID, assetID, versionGroup string, in *ExternalInstance) (*ExternalInstance, error) {
	return do[ExternalInstance](ctx, c, http.MethodPost, instancesPath(groupID, assetID, versionGroup)+"/external", in)
}

// UpdateExternalInstance changes an external instance.
func (c *Client) UpdateExternalInstance(ctx context.Context, groupID, assetID, versionGroup, id string, in *ExternalInstance) error {
	return c.JSON(ctx, http.MethodPatch, instancesPath(groupID, assetID, versionGroup)+"/external/"+esc(id), in, nil)
}

// DeleteExternalInstance deletes an external instance.
func (c *Client) DeleteExternalInstance(ctx context.Context, groupID, assetID, versionGroup, id string) error {
	return c.JSON(ctx, http.MethodDelete, instancesPath(groupID, assetID, versionGroup)+"/external/"+esc(id), nil, nil)
}

// GetAssetV2 reads an asset version from Exchange's v2 API, whose files
// carry an externalLink for each classifier.
func (c *Client) GetAssetV2(ctx context.Context, ref AssetRef) (*Asset, error) {
	return get[Asset](ctx, c, ref.v2())
}
