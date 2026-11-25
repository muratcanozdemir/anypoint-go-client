package anypoint

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/muratcanozdemir/anypoint-go-client/internal/jsonx"
)

// Design Center (API Designer) projects, as anypoint-cli-v4's designcenter
// plugin uses them, and the DataGraph load balancer setting.

const designerBase = "/designcenter/api-designer/projects"

// designerHeaders scopes a request to an organization and the caller, as
// API Designer requires (x-owner-id is the user's ID).
func (c *Client) designerHeaders(ctx context.Context, orgID string) (http.Header, error) {
	me, err := c.Me(ctx)
	if err != nil {
		return nil, err
	}
	h := scopeHeaders(orgID, "")
	if id := jsonx.Str(jsonx.Dig(me, "user.id")); id != "" {
		h.Set("x-owner-id", id)
	}
	return h, nil
}

func (c *Client) designer(ctx context.Context, orgID, method, path string, in, out any) error {
	h, err := c.designerHeaders(ctx, orgID)
	if err != nil {
		return err
	}
	return c.jsonH(ctx, method, path, h, in, out)
}

// DesignProject is an API Designer project.
type DesignProject struct {
	rawJSON
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	Version        any    `json:"version,omitempty"`
	CreatedDate    string `json:"createdDate,omitempty"`
	OrganizationID string `json:"organizationId,omitempty"`
	DefaultBranch  string `json:"defaultBranch,omitempty"`
}

// ListDesignProjects lists one page of an organization's projects.
func (c *Client) ListDesignProjects(ctx context.Context, orgID string, pageIndex, pageSize int) ([]DesignProject, error) {
	q := url.Values{"pageIndex": {strconv.Itoa(pageIndex)}, "pageSize": {strconv.Itoa(pageSize)}}
	var raw json.RawMessage
	if err := c.designer(ctx, orgID, http.MethodGet, withQuery("/designcenter/api/v2/organizations/"+esc(orgID)+"/projects", q), nil, &raw); err != nil {
		return nil, err
	}
	return decodeList[DesignProject](raw)
}

// FindDesignProject returns the project with the given name.
func (c *Client) FindDesignProject(ctx context.Context, orgID, name string) (*DesignProject, error) {
	var raw json.RawMessage
	if err := c.designer(ctx, orgID, http.MethodGet, designerBase, nil, &raw); err != nil {
		return nil, err
	}
	all, err := decodeList[DesignProject](raw)
	if err != nil {
		return nil, err
	}
	var found *DesignProject
	for i := range all {
		if all[i].Name == name {
			found = &all[i] // the last match, as anypoint-cli-v4 picks it
		}
	}
	if found == nil {
		return nil, notFound("project", name)
	}
	return found, nil
}

// GetDesignProject returns a project by ID.
func (c *Client) GetDesignProject(ctx context.Context, orgID, projectID string) (*DesignProject, error) {
	var p DesignProject
	if err := c.designer(ctx, orgID, http.MethodGet, designerBase+"/"+esc(projectID), nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// DesignProjectRequest creates a project.
type DesignProjectRequest struct {
	Name       string `json:"name"`
	Type       string `json:"type"`                 // raml, raml-fragment, oas
	Classifier string `json:"classifier,omitempty"` // oas-components, json-schema
	SubType    string `json:"subType,omitempty"`    // RAML fragment kind
	Format     string `json:"format,omitempty"`     // yaml, json
	Version    string `json:"version,omitempty"`    // OAS 2.0 or 3.0
}

// CreateDesignProject creates a project.
func (c *Client) CreateDesignProject(ctx context.Context, orgID string, req DesignProjectRequest) (*DesignProject, error) {
	var p DesignProject
	if err := c.designer(ctx, orgID, http.MethodPost, designerBase, req, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// DeleteDesignProject deletes a project.
func (c *Client) DeleteDesignProject(ctx context.Context, orgID, projectID string) error {
	return c.designer(ctx, orgID, http.MethodDelete, designerBase+"/"+esc(projectID), nil, nil)
}

func branchPath(projectID, branch string) string {
	return designerBase + "/" + esc(projectID) + "/branches/" + esc(branch)
}

// DesignFile is a file or folder of a project branch.
type DesignFile struct {
	Path string `json:"path"`
	Type string `json:"type,omitempty"` // FILE or FOLDER
}

// ListDesignFiles lists a branch's files (this also syncs the caller's
// working copy).
func (c *Client) ListDesignFiles(ctx context.Context, orgID, projectID, branch string) ([]DesignFile, error) {
	var raw json.RawMessage
	if err := c.designer(ctx, orgID, http.MethodGet, branchPath(projectID, branch)+"/files", nil, &raw); err != nil {
		return nil, err
	}
	return decodeList[DesignFile](raw)
}

// GetDesignFile returns a file's content.
func (c *Client) GetDesignFile(ctx context.Context, orgID, projectID, branch, path string) ([]byte, error) {
	h, err := c.designerHeaders(ctx, orgID)
	if err != nil {
		return nil, err
	}
	h.Set("Accept", "*/*")
	// The API takes the path with \ separators, as one segment.
	resp, err := c.Do(ctx, http.MethodGet, branchPath(projectID, branch)+"/files/"+esc(strings.ReplaceAll(path, "/", `\`)), nil, h)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// DesignFileContent is a file to save to a branch.
type DesignFileContent struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// SaveDesignFiles saves files to a branch (the lock must be held).
func (c *Client) SaveDesignFiles(ctx context.Context, orgID, projectID, branch string, files []DesignFileContent) error {
	return c.designer(ctx, orgID, http.MethodPost, branchPath(projectID, branch)+"/save", files, nil)
}

// AcquireDesignLock locks a branch for writing.
func (c *Client) AcquireDesignLock(ctx context.Context, orgID, projectID, branch string) error {
	return c.designer(ctx, orgID, http.MethodPost, branchPath(projectID, branch)+"/acquireLock", nil, nil)
}

// ReleaseDesignLock releases a branch lock.
func (c *Client) ReleaseDesignLock(ctx context.Context, orgID, projectID, branch string) error {
	return c.designer(ctx, orgID, http.MethodPost, branchPath(projectID, branch)+"/releaseLock", nil, nil)
}

// GetDesignExchangeJSON returns a branch's exchange.json (empty when absent).
func (c *Client) GetDesignExchangeJSON(ctx context.Context, orgID, projectID, branch string) (map[string]any, error) {
	var raw json.RawMessage
	err := c.designer(ctx, orgID, http.MethodGet, branchPath(projectID, branch)+"/files/v2/exchange.json", nil, &raw)
	if IsNotFound(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if len(raw) > 0 && raw[0] == '{' {
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// PublishDesignProject publishes a branch to Exchange.
func (c *Client) PublishDesignProject(ctx context.Context, orgID, projectID, branch string, body any) error {
	return c.designer(ctx, orgID, http.MethodPost, branchPath(projectID, branch)+"/publish/exchange", body, nil)
}

// InvalidDependency is an Exchange dependency that did not resolve.
type InvalidDependency struct {
	AssetID string `json:"assetId"`
	Message string `json:"message,omitempty"`
}

// ResolveDesignDependencies starts resolving a branch's Exchange
// dependencies and returns those already known to be invalid.
func (c *Client) ResolveDesignDependencies(ctx context.Context, orgID, projectID, branch string) ([]InvalidDependency, error) {
	var out struct {
		InvalidDependencies []InvalidDependency `json:"invalidDependencies"`
	}
	err := c.designer(ctx, orgID, http.MethodPatch, branchPath(projectID, branch)+"/exchange/dependencies", nil, &out)
	return out.InvalidDependencies, err
}

// DesignDependencyStatus returns the dependency resolution job's status
// ("done" when finished).
func (c *Client) DesignDependencyStatus(ctx context.Context, orgID, projectID, branch string) (string, error) {
	var out struct {
		Status string `json:"status"`
	}
	err := c.designer(ctx, orgID, http.MethodGet, branchPath(projectID, branch)+"/exchange/dependencies/job", nil, &out)
	return out.Status, err
}

// ---------------------------------------------------------------- DataGraph

// DataGraphLBConfig is the dedicated load balancer DataGraph uses.
type DataGraphLBConfig struct {
	rawJSON
	Domain string `json:"domain,omitempty"`
	DLBURL string `json:"dlbUrl"`
}

func dataGraphPath(orgID, envID string) string {
	return "/datagraph/xapi/organizations/" + esc(orgID) + "/environments/" + esc(envID) + "/dlbConfig"
}

// GetDataGraphLBConfig returns an environment's DataGraph load balancer setting.
func (c *Client) GetDataGraphLBConfig(ctx context.Context, orgID, envID string) (*DataGraphLBConfig, error) {
	return get[DataGraphLBConfig](ctx, c, dataGraphPath(orgID, envID))
}

// SetDataGraphLBConfig makes DataGraph use a dedicated load balancer URL.
func (c *Client) SetDataGraphLBConfig(ctx context.Context, orgID, envID, dlbURL string) error {
	return c.JSON(ctx, http.MethodPut, dataGraphPath(orgID, envID), Doc{"dlbUrl": dlbURL}, nil)
}

// DeleteDataGraphLBConfig removes the DataGraph load balancer setting.
func (c *Client) DeleteDataGraphLBConfig(ctx context.Context, orgID, envID string) error {
	return c.JSON(ctx, http.MethodDelete, dataGraphPath(orgID, envID), nil, nil)
}
