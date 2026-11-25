package anypoint

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
)

// Runtime Manager for hybrid (customer-hosted) Mule runtimes: servers,
// server groups, clusters, applications, alerts and agent components.
// The hybrid API takes the organization and environment from headers;
// endpoints follow anypoint-cli-v4's runtime-mgr plugin.

const hybridBase = "/hybrid/api/v1"

// scopeHeaders carries the organization and environment the way
// anypoint-cli-v4 sends them on every request.
func scopeHeaders(orgID, envID string) http.Header {
	h := http.Header{}
	if orgID != "" {
		h.Set("X-ANYPNT-ORG-ID", orgID)
		h.Set("x-organization-id", orgID)
	}
	if envID != "" {
		h.Set("X-ANYPNT-ENV-ID", envID)
	}
	return h
}

// hybridDo sends a hybrid request and decodes the "data" member of the
// response into out (if not nil).
func (c *Client) hybridDo(ctx context.Context, orgID, envID, method, path string, in, out any) error {
	if out == nil {
		return c.jsonH(ctx, method, path, scopeHeaders(orgID, envID), in, nil)
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := c.jsonH(ctx, method, path, scopeHeaders(orgID, envID), in, &env); err != nil {
		return err
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return nil
	}
	return decode(env.Data, out)
}

func hybridGet[T any](ctx context.Context, c *Client, orgID, envID, path string) (*T, error) {
	var out T
	if err := c.hybridDo(ctx, orgID, envID, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func hybridList[T any](ctx context.Context, c *Client, orgID, envID, path string) ([]T, error) {
	var raw json.RawMessage
	if err := c.hybridDo(ctx, orgID, envID, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	return decodeList[T](raw)
}

// hybridMultipart sends a multipart form; files maps field -> (name, content).
func (c *Client) hybridMultipart(ctx context.Context, orgID, envID, method, path string, fields map[string]string, file *HybridFile, out any) error {
	data, err := c.multipartForm(ctx, method, path, scopeHeaders(orgID, envID), fields, file)
	if err != nil || out == nil || len(bytes.TrimSpace(data)) == 0 {
		return err
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &env); err != nil || len(env.Data) == 0 {
		return err
	}
	return decode(env.Data, out)
}

// multipartForm sends fields and an optional "file" part the way the
// Runtime Manager UI does (x-from-service headers) and returns the body.
func (c *Client) multipartForm(ctx context.Context, method, path string, h http.Header, fields map[string]string, file *HybridFile) ([]byte, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := writeFields(w, "", fields); err != nil {
		return nil, err
	}
	if file != nil {
		part, err := w.CreateFormFile("file", file.Name)
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(part, file.Content); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	h = h.Clone()
	if h == nil {
		h = http.Header{}
	}
	h.Set("Content-Type", w.FormDataContentType())
	h.Set("x-from-service", "anypoint-cli")
	h.Set("x-from-service-version", "v2")
	resp, err := c.Do(ctx, method, path, body.Bytes(), h)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// HybridFile is an application archive to upload.
type HybridFile struct {
	Name    string
	Content io.Reader
}

// ---------------------------------------------------------------- servers

// HybridServer is a registered Mule runtime (or a server of a group/cluster).
type HybridServer struct {
	rawJSON
	ID              FlexID `json:"id"`
	Name            string `json:"name"`
	ServerType      string `json:"serverType,omitempty"`
	Type            string `json:"type,omitempty"`
	MuleVersion     string `json:"muleVersion,omitempty"`
	AgentVersion    string `json:"agentVersion,omitempty"`
	GatewayVersion  string `json:"gatewayVersion,omitempty"`
	ClusterID       FlexID `json:"clusterId,omitempty"`
	ClusterName     string `json:"clusterName,omitempty"`
	ServerGroupID   FlexID `json:"serverGroupId,omitempty"`
	ServerGroupName string `json:"serverGroupName,omitempty"`
	Status          string `json:"status,omitempty"`
	TimeCreated     any    `json:"timeCreated,omitempty"`
	TimeUpdated     any    `json:"timeUpdated,omitempty"`
	Addresses       []struct {
		IP               string `json:"ip"`
		NetworkInterface string `json:"networkInterface"`
	} `json:"addresses,omitempty"`
}

// ListServers lists the servers of an environment.
func (c *Client) ListServers(ctx context.Context, orgID, envID string) ([]HybridServer, error) {
	return hybridList[HybridServer](ctx, c, orgID, envID, hybridBase+"/servers")
}

// GetServer returns a server.
func (c *Client) GetServer(ctx context.Context, orgID, envID, serverID string) (*HybridServer, error) {
	return hybridGet[HybridServer](ctx, c, orgID, envID, hybridBase+"/servers/"+esc(serverID))
}

// RenameServer renames a server.
func (c *Client) RenameServer(ctx context.Context, orgID, envID, serverID, name string) error {
	return c.hybridDo(ctx, orgID, envID, http.MethodPatch, hybridBase+"/servers/"+esc(serverID), Doc{"name": name}, nil)
}

// DeleteServer deletes a server.
func (c *Client) DeleteServer(ctx context.Context, orgID, envID, serverID string) error {
	return c.hybridDo(ctx, orgID, envID, http.MethodDelete, hybridBase+"/servers/"+esc(serverID), nil, nil)
}

// GetServerRegistrationToken returns the token to register a new server
// (amc_setup -H <token>).
func (c *Client) GetServerRegistrationToken(ctx context.Context, orgID, envID string) (string, error) {
	var raw json.RawMessage
	if err := c.jsonH(ctx, http.MethodGet, hybridBase+"/servers/registrationToken", scopeHeaders(orgID, envID), nil, &raw); err != nil {
		return "", err
	}
	var env struct {
		Data string `json:"data"`
	}
	if json.Unmarshal(raw, &env) == nil && env.Data != "" {
		return env.Data, nil
	}
	return string(bytes.TrimSpace(raw)), nil
}

// ---------------------------------------------------------------- server groups and clusters

// ServerGroup is a server group or a cluster.
type ServerGroup struct {
	rawJSON
	ID               FlexID         `json:"id"`
	Name             string         `json:"name"`
	Status           string         `json:"status,omitempty"`
	Type             string         `json:"type,omitempty"`
	MulticastEnabled bool           `json:"multicastEnabled,omitempty"`
	PrimaryNodeID    FlexID         `json:"primaryNodeId,omitempty"`
	Servers          []HybridServer `json:"servers"`
	TimeCreated      any            `json:"timeCreated,omitempty"`
	TimeUpdated      any            `json:"timeUpdated,omitempty"`
}

// ListServerGroups lists the server groups of an environment.
func (c *Client) ListServerGroups(ctx context.Context, orgID, envID string) ([]ServerGroup, error) {
	return hybridList[ServerGroup](ctx, c, orgID, envID, hybridBase+"/serverGroups")
}

// GetServerGroup returns a server group.
func (c *Client) GetServerGroup(ctx context.Context, orgID, envID, groupID string) (*ServerGroup, error) {
	return hybridGet[ServerGroup](ctx, c, orgID, envID, hybridBase+"/serverGroups/"+esc(groupID))
}

// CreateServerGroup creates a server group of servers.
func (c *Client) CreateServerGroup(ctx context.Context, orgID, envID, name string, serverIDs []string) error {
	if serverIDs == nil {
		serverIDs = []string{}
	}
	return c.hybridDo(ctx, orgID, envID, http.MethodPost, hybridBase+"/serverGroups", Doc{"name": name, "serverIds": serverIDs}, nil)
}

// RenameServerGroup renames a server group.
func (c *Client) RenameServerGroup(ctx context.Context, orgID, envID, groupID, name string) error {
	return c.hybridDo(ctx, orgID, envID, http.MethodPatch, hybridBase+"/serverGroups/"+esc(groupID), Doc{"name": name}, nil)
}

// DeleteServerGroup deletes a server group.
func (c *Client) DeleteServerGroup(ctx context.Context, orgID, envID, groupID string) error {
	return c.hybridDo(ctx, orgID, envID, http.MethodDelete, hybridBase+"/serverGroups/"+esc(groupID), nil, nil)
}

// AddServerToGroup adds a server to a server group.
func (c *Client) AddServerToGroup(ctx context.Context, orgID, envID, groupID, serverID string) error {
	return c.hybridDo(ctx, orgID, envID, http.MethodPost, hybridBase+"/serverGroups/"+esc(groupID)+"/servers/"+esc(serverID), nil, nil)
}

// RemoveServerFromGroup removes a server from a server group.
func (c *Client) RemoveServerFromGroup(ctx context.Context, orgID, envID, groupID, serverID string) error {
	return c.hybridDo(ctx, orgID, envID, http.MethodDelete, hybridBase+"/serverGroups/"+esc(groupID)+"/servers/"+esc(serverID), nil, nil)
}

// ClusterMember is a server joining a cluster; ServerIP is needed for
// unicast clusters.
type ClusterMember struct {
	ServerID string `json:"serverId"`
	ServerIP string `json:"serverIp,omitempty"`
}

// ListClusters lists the clusters of an environment.
func (c *Client) ListClusters(ctx context.Context, orgID, envID string) ([]ServerGroup, error) {
	return hybridList[ServerGroup](ctx, c, orgID, envID, hybridBase+"/clusters")
}

// GetCluster returns a cluster.
func (c *Client) GetCluster(ctx context.Context, orgID, envID, clusterID string) (*ServerGroup, error) {
	return hybridGet[ServerGroup](ctx, c, orgID, envID, hybridBase+"/clusters/"+esc(clusterID))
}

// CreateCluster creates a cluster and returns it.
func (c *Client) CreateCluster(ctx context.Context, orgID, envID, name string, multicast bool, servers []ClusterMember) (*ServerGroup, error) {
	if servers == nil {
		servers = []ClusterMember{}
	}
	var out ServerGroup
	err := c.hybridDo(ctx, orgID, envID, http.MethodPost, hybridBase+"/clusters", Doc{"name": name, "multicastEnabled": multicast, "servers": servers}, &out)
	return &out, err
}

// RenameCluster renames a cluster.
func (c *Client) RenameCluster(ctx context.Context, orgID, envID, clusterID, name string) error {
	return c.hybridDo(ctx, orgID, envID, http.MethodPatch, hybridBase+"/clusters/"+esc(clusterID), Doc{"name": name}, nil)
}

// DeleteCluster deletes a cluster.
func (c *Client) DeleteCluster(ctx context.Context, orgID, envID, clusterID string) error {
	return c.hybridDo(ctx, orgID, envID, http.MethodDelete, hybridBase+"/clusters/"+esc(clusterID), nil, nil)
}

// AddServerToCluster adds a server to a cluster.
func (c *Client) AddServerToCluster(ctx context.Context, orgID, envID, clusterID string, m ClusterMember) error {
	return c.hybridDo(ctx, orgID, envID, http.MethodPost, hybridBase+"/clusters/"+esc(clusterID)+"/servers", m, nil)
}

// RemoveServerFromCluster removes a server from a cluster.
func (c *Client) RemoveServerFromCluster(ctx context.Context, orgID, envID, clusterID, serverID string) error {
	return c.hybridDo(ctx, orgID, envID, http.MethodDelete, hybridBase+"/clusters/"+esc(clusterID)+"/servers/"+esc(serverID), nil, nil)
}

// ---------------------------------------------------------------- applications

// HybridApp is an application deployed to a server, group or cluster.
type HybridApp struct {
	rawJSON
	ID                 FlexID `json:"id"`
	Name               string `json:"name"`
	DesiredStatus      string `json:"desiredStatus,omitempty"`
	LastReportedStatus string `json:"lastReportedStatus,omitempty"`
	TimeCreated        any    `json:"timeCreated,omitempty"`
	TimeUpdated        any    `json:"timeUpdated,omitempty"`
	Artifact           struct {
		Name         string `json:"name"`
		FileName     string `json:"fileName"`
		FileChecksum string `json:"fileChecksum"`
		TimeUpdated  any    `json:"timeUpdated,omitempty"`
	} `json:"artifact"`
	Target struct {
		ID     FlexID `json:"id"`
		Name   string `json:"name"`
		Type   string `json:"type"`
		Status string `json:"status"`
	} `json:"target"`
}

// HybridListOptions pages the hybrid application and alert lists.
type HybridListOptions struct {
	Limit, Offset int
	Sort          string
	TargetID      string
}

func (o HybridListOptions) values() url.Values {
	q := url.Values{}
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	if o.Offset > 0 {
		q.Set("offset", strconv.Itoa(o.Offset))
	}
	if o.Sort != "" {
		q.Set("sort", o.Sort)
	}
	if o.TargetID != "" {
		q.Set("targetId", o.TargetID)
	}
	return q
}

// ListHybridApps lists hybrid applications.
func (c *Client) ListHybridApps(ctx context.Context, orgID, envID string, o HybridListOptions) ([]HybridApp, error) {
	return hybridList[HybridApp](ctx, c, orgID, envID, withQuery(hybridBase+"/applications", o.values()))
}

// GetHybridApp returns a hybrid application by ID.
func (c *Client) GetHybridApp(ctx context.Context, orgID, envID, appID string) (*HybridApp, error) {
	return hybridGet[HybridApp](ctx, c, orgID, envID, hybridBase+"/applications/"+esc(appID))
}

// FindHybridApp finds an application by ID, else by name.
func (c *Client) FindHybridApp(ctx context.Context, orgID, envID, ident string) (*HybridApp, error) {
	if a, err := c.GetHybridApp(ctx, orgID, envID, ident); err == nil && a.ID != "" {
		return a, nil
	}
	apps, err := c.ListHybridApps(ctx, orgID, envID, HybridListOptions{})
	if err != nil {
		return nil, err
	}
	for i := range apps {
		if apps[i].Name == ident {
			return &apps[i], nil
		}
	}
	return nil, notFound("application", ident)
}

// DeployHybridApp deploys an archive as a new application on a target
// (server, group or cluster ID).
func (c *Client) DeployHybridApp(ctx context.Context, orgID, envID, name, targetID string, file HybridFile) (*HybridApp, error) {
	var out HybridApp
	err := c.hybridMultipart(ctx, orgID, envID, http.MethodPost, hybridBase+"/applications",
		map[string]string{"artifactName": name, "targetId": targetID}, &file, &out)
	return &out, err
}

// RedeployHybridApp replaces an application's archive.
func (c *Client) RedeployHybridApp(ctx context.Context, orgID, envID, appID string, file HybridFile) (*HybridApp, error) {
	var out HybridApp
	err := c.hybridMultipart(ctx, orgID, envID, http.MethodPatch, hybridBase+"/applications/"+esc(appID), nil, &file, &out)
	return &out, err
}

// RestartHybridApp restarts an application.
func (c *Client) RestartHybridApp(ctx context.Context, orgID, envID, appID string) error {
	return c.hybridMultipart(ctx, orgID, envID, http.MethodPatch, hybridBase+"/applications/"+esc(appID), map[string]string{"id": appID}, nil, nil)
}

// SetHybridAppStatus starts (STARTED) or stops (STOPPED) an application.
func (c *Client) SetHybridAppStatus(ctx context.Context, orgID, envID, appID, status string) error {
	return c.hybridDo(ctx, orgID, envID, http.MethodPatch, hybridBase+"/applications/"+esc(appID), Doc{"desiredStatus": status}, nil)
}

// DeleteHybridApp undeploys an application.
func (c *Client) DeleteHybridApp(ctx context.Context, orgID, envID, appID string) error {
	return c.hybridDo(ctx, orgID, envID, http.MethodDelete, hybridBase+"/applications/"+esc(appID), nil, nil)
}

// DownloadHybridAppArtifact opens an application's archive; the caller closes it.
func (c *Client) DownloadHybridAppArtifact(ctx context.Context, orgID, envID, appID string) (io.ReadCloser, error) {
	h := scopeHeaders(orgID, envID)
	h.Set("Accept", "*/*")
	resp, err := c.Do(ctx, http.MethodGet, hybridBase+"/applications/"+esc(appID)+"/artifact", nil, h)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// TargetComponent is a Runtime Manager agent component of a target, such as
// mule.agent.logging.service.
type TargetComponent struct {
	rawJSON
	Component struct {
		ID   FlexID `json:"id"`
		Name string `json:"name"`
	} `json:"component"`
	Enabled       bool `json:"enabled"`
	Configuration Doc  `json:"configuration"`
}

// ListTargetComponents lists the agent components of a target.
func (c *Client) ListTargetComponents(ctx context.Context, orgID, envID, targetID string) ([]TargetComponent, error) {
	return hybridList[TargetComponent](ctx, c, orgID, envID, hybridBase+"/targets/"+esc(targetID)+"/components")
}

// UpdateTargetComponent patches an agent component, e.g. {"enabled": true}
// or {"configuration": {...}}.
func (c *Client) UpdateTargetComponent(ctx context.Context, orgID, envID, targetID, componentID string, body any) error {
	return c.hybridDo(ctx, orgID, envID, http.MethodPatch, hybridBase+"/targets/"+esc(targetID)+"/components/"+esc(componentID), body, nil)
}

// ---------------------------------------------------------------- alerts

// HybridAlert is a Runtime Manager alert on hybrid resources.
type HybridAlert struct {
	rawJSON
	ID           FlexID `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Enabled      bool   `json:"enabled"`
	Severity     string `json:"severity"`
	ProductName  string `json:"productName,omitempty"`
	TimeCreated  any    `json:"timeCreated,omitempty"`
	TimeUpdated  any    `json:"timeUpdated,omitempty"`
	LastModified any    `json:"lastModified,omitempty"`
	Condition    struct {
		Type         string   `json:"type"`
		Event        string   `json:"event,omitempty"`
		ResourceType string   `json:"resourceType"`
		Resources    []string `json:"resources"`
	} `json:"condition"`
	Actions []struct {
		Type    string   `json:"type"`
		Emails  []string `json:"emails,omitempty"`
		UserIDs []string `json:"userIds,omitempty"`
		Subject string   `json:"subject,omitempty"`
		Content string   `json:"content,omitempty"`
	} `json:"actions"`
}

// ListHybridAlerts lists hybrid alerts.
func (c *Client) ListHybridAlerts(ctx context.Context, orgID, envID string, o HybridListOptions) ([]HybridAlert, error) {
	return hybridList[HybridAlert](ctx, c, orgID, envID, withQuery(hybridBase+"/alerts", o.values()))
}

// GetHybridAlert returns a hybrid alert.
func (c *Client) GetHybridAlert(ctx context.Context, orgID, envID, alertID string) (*HybridAlert, error) {
	return hybridGet[HybridAlert](ctx, c, orgID, envID, hybridBase+"/alerts/"+esc(alertID))
}

// CreateHybridAlert creates a hybrid alert from its document.
func (c *Client) CreateHybridAlert(ctx context.Context, orgID, envID string, body any) error {
	return c.hybridDo(ctx, orgID, envID, http.MethodPost, hybridBase+"/alerts", body, nil)
}

// UpdateHybridAlert patches a hybrid alert.
func (c *Client) UpdateHybridAlert(ctx context.Context, orgID, envID, alertID string, body any) error {
	return c.hybridDo(ctx, orgID, envID, http.MethodPatch, hybridBase+"/alerts/"+esc(alertID), body, nil)
}

// ---------------------------------------------------------------- runtime targets

// ListRuntimeTargets lists the Runtime Fabric targets of an environment
// (provider MC).
func (c *Client) ListRuntimeTargets(ctx context.Context, orgID, envID string) ([]RuntimeTarget, error) {
	p := "/amc/atlas/api/v1/organizations/" + esc(orgID) + "/providers/MC/targets"
	return list[RuntimeTarget](ctx, c, withQuery(p, url.Values{"environmentId": {envID}}))
}
