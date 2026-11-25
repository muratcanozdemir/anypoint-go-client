package anypoint

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
)

// CloudHub 1.0: VPCs, dedicated load balancers, regions, runtimes,
// applications and alerts, as anypoint-cli-v4's cloudhub plugin uses them.

const cloudhubBase = "/cloudhub/api"

func vpcsPath(orgID string) string { return cloudhubBase + "/organizations/" + esc(orgID) + "/vpcs" }

// ---------------------------------------------------------------- VPCs

// VPC is a CloudHub 1.0 virtual private cloud.
type VPC struct {
	rawJSON
	ID                     string         `json:"id"`
	Name                   string         `json:"name"`
	Region                 string         `json:"region"`
	CIDRBlock              string         `json:"cidrBlock"`
	IsDefault              bool           `json:"isDefault"`
	OwnerID                string         `json:"ownerId"`
	AssociatedEnvironments []string       `json:"associatedEnvironments"`
	SharedWith             []string       `json:"sharedWith"`
	InternalDNS            VPCInternalDNS `json:"internalDns"`
	FirewallRules          []FirewallRule `json:"firewallRules"`
}

// VPCInternalDNS lists a VPC's DNS servers and the domains they resolve.
type VPCInternalDNS struct {
	DNSServers     []string `json:"dnsServers"`
	SpecialDomains []string `json:"specialDomains"`
}

// FirewallRule is an inbound VPC firewall rule.
type FirewallRule struct {
	CIDRBlock string `json:"cidrBlock"`
	Protocol  string `json:"protocol"`
	FromPort  FlexID `json:"fromPort"`
	ToPort    FlexID `json:"toPort"`
}

// VPCListOptions pages a VPC list; zero values use the API defaults.
type VPCListOptions struct {
	Limit, Offset int
	Sort          string
}

// ListVPCs lists one page of an organization's VPCs.
func (c *Client) ListVPCs(ctx context.Context, orgID string, o VPCListOptions) ([]VPC, error) {
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
	return list[VPC](ctx, c, withQuery(vpcsPath(orgID), q), "data")
}

// FindVPC returns the VPC with the given name.
func (c *Client) FindVPC(ctx context.Context, orgID, name string) (*VPC, error) {
	vpcs, err := c.ListVPCs(ctx, orgID, VPCListOptions{})
	if err != nil {
		return nil, err
	}
	for i := range vpcs {
		if vpcs[i].Name == name {
			return &vpcs[i], nil
		}
	}
	return nil, notFound("VPC", name)
}

// CreateVPC creates a VPC from its document.
func (c *Client) CreateVPC(ctx context.Context, orgID string, body any) (*VPC, error) {
	return do[VPC](ctx, c, http.MethodPost, vpcsPath(orgID), body)
}

// UpdateVPC replaces (PUT) a VPC's document, or the parts of it given.
func (c *Client) UpdateVPC(ctx context.Context, orgID, vpcID string, body any) error {
	return c.JSON(ctx, http.MethodPut, vpcsPath(orgID)+"/"+esc(vpcID), body, nil)
}

// DeleteVPC deletes a VPC.
func (c *Client) DeleteVPC(ctx context.Context, orgID, vpcID string) error {
	return c.JSON(ctx, http.MethodDelete, vpcsPath(orgID)+"/"+esc(vpcID), nil, nil)
}

// ---------------------------------------------------------------- load balancers

// LoadBalancer is a CloudHub 1.0 dedicated load balancer.
type LoadBalancer struct {
	rawJSON
	ID                 string        `json:"id"`
	Name               string        `json:"name"`
	Domain             string        `json:"domain"`
	State              string        `json:"state"`
	VPCID              string        `json:"vpcId,omitempty"`
	HTTPMode           string        `json:"httpMode"`
	TLSv1              bool          `json:"tlsv1"`
	IPAddresses        []string      `json:"ipAddresses"`
	IPAllowlist        []string      `json:"ipAllowlist"`
	DefaultSSLEndpoint int           `json:"defaultSslEndpoint"`
	SSLEndpoints       []SSLEndpoint `json:"sslEndpoints"`
	StaticIPsDisabled  bool          `json:"staticIPsDisabled"`
}

// SSLEndpoint is a certificate a load balancer serves, with its mappings.
type SSLEndpoint struct {
	PublicKeyCN              string      `json:"publicKeyCN"`
	PublicKeySANs            []string    `json:"publicKeySANs,omitempty"`
	ClientCertCN             string      `json:"clientCertCN,omitempty"`
	VerifyClientMode         string      `json:"verifyClientMode,omitempty"`
	RevocationListDigest     string      `json:"revocationListDigest,omitempty"`
	RevocationListSerial     string      `json:"revocationListSerial,omitempty"`
	RevocationListLastUpdate string      `json:"revocationListLastUpdate,omitempty"`
	RevocationListNextUpdate string      `json:"revocationListNextUpdate,omitempty"`
	Mappings                 []LBMapping `json:"mappings"`
}

// LBMapping maps a load balancer input URI to an application.
type LBMapping struct {
	InputURI         string `json:"inputUri"`
	AppName          string `json:"appName"`
	AppURI           string `json:"appUri"`
	UpstreamProtocol string `json:"upstreamProtocol,omitempty"`
}

// PatchOp is one JSON Patch operation.
type PatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

func lbPath(orgID, vpcID string) string { return vpcsPath(orgID) + "/" + esc(vpcID) + "/loadbalancers" }

// ListLoadBalancers lists the load balancers of a VPC.
func (c *Client) ListLoadBalancers(ctx context.Context, orgID, vpcID string) ([]LoadBalancer, error) {
	return list[LoadBalancer](ctx, c, lbPath(orgID, vpcID), "data")
}

// CreateLoadBalancer creates a load balancer in a VPC from its document.
func (c *Client) CreateLoadBalancer(ctx context.Context, orgID, vpcID string, body any) (*LoadBalancer, error) {
	return do[LoadBalancer](ctx, c, http.MethodPost, lbPath(orgID, vpcID), body)
}

// PatchLoadBalancer applies JSON Patch operations to a load balancer.
func (c *Client) PatchLoadBalancer(ctx context.Context, orgID, vpcID, lbID string, ops []PatchOp) (*LoadBalancer, error) {
	return do[LoadBalancer](ctx, c, http.MethodPatch, lbPath(orgID, vpcID)+"/"+esc(lbID), ops)
}

// DeleteLoadBalancer deletes a load balancer.
func (c *Client) DeleteLoadBalancer(ctx context.Context, orgID, vpcID, lbID string) error {
	return c.JSON(ctx, http.MethodDelete, lbPath(orgID, vpcID)+"/"+esc(lbID), nil, nil)
}

// ---------------------------------------------------------------- regions, runtimes

// Region is a CloudHub region.
type Region struct {
	rawJSON
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListRegions lists the CloudHub regions.
func (c *Client) ListRegions(ctx context.Context) ([]Region, error) {
	return list[Region](ctx, c, cloudhubBase+"/regions")
}

// MuleVersion is a Mule runtime CloudHub 1.0 offers.
type MuleVersion struct {
	rawJSON
	Version        string `json:"version"`
	State          string `json:"state,omitempty"`
	ReleaseChannel string `json:"releaseChannel,omitempty"`
	JavaVersion    string `json:"javaVersion,omitempty"`
}

// ListMuleVersions lists the CloudHub 1.0 runtimes of an environment.
func (c *Client) ListMuleVersions(ctx context.Context, orgID, envID string) ([]MuleVersion, error) {
	var raw json.RawMessage
	if err := c.jsonH(ctx, http.MethodGet, cloudhubBase+"/mule-versions", scopeHeaders(orgID, envID), nil, &raw); err != nil {
		return nil, err
	}
	return decodeList[MuleVersion](raw, "data")
}

// ---------------------------------------------------------------- applications

// CloudHubApp is a CloudHub 1.0 application.
type CloudHubApp struct {
	rawJSON
	Domain                 string             `json:"domain"`
	FullDomain             string             `json:"fullDomain,omitempty"`
	Status                 string             `json:"status,omitempty"`
	DeploymentUpdateStatus string             `json:"deploymentUpdateStatus,omitempty"`
	DeploymentErrorMessage string             `json:"deploymentErrorMessage,omitempty"`
	LastUpdateTime         any                `json:"lastUpdateTime,omitempty"`
	MuleVersion            CloudHubRuntime    `json:"muleVersion"`
	PreviousMuleVersion    *CloudHubRuntime   `json:"previousMuleVersion,omitempty"`
	FileName               string             `json:"fileName,omitempty"`
	Region                 string             `json:"region,omitempty"`
	PersistentQueues       bool               `json:"persistentQueues"`
	PersistentQueuesEnc    bool               `json:"persistentQueuesEncrypted"`
	StaticIPsEnabled       bool               `json:"staticIPsEnabled"`
	MonitoringAutoRestart  bool               `json:"monitoringAutoRestart"`
	Workers                CloudHubWorkers    `json:"workers"`
	WorkerStatuses         []CloudHubWorker   `json:"workerStatuses,omitempty"`
	Properties             map[string]string  `json:"properties,omitempty"`
	IPAddresses            []CloudHubStaticIP `json:"ipAddresses,omitempty"`
}

// CloudHubRuntime is an application's Mule runtime.
type CloudHubRuntime struct {
	Version        string `json:"version"`
	ReleaseChannel string `json:"releaseChannel,omitempty"`
	JavaVersion    string `json:"javaVersion,omitempty"`
}

// CloudHubWorkers is an application's worker size and count.
type CloudHubWorkers struct {
	Amount int `json:"amount"`
	Type   struct {
		Name string `json:"name"`
		CPU  string `json:"cpu"`
	} `json:"type"`
}

// CloudHubWorker is the status of one worker.
type CloudHubWorker struct {
	Host   string `json:"host"`
	Status string `json:"status"`
}

// CloudHubStaticIP is a static IP allocated to an application.
type CloudHubStaticIP struct {
	Address  string `json:"address,omitempty"`
	RegionID string `json:"regionId"`
}

const cloudhubAppsV2 = cloudhubBase + "/v2/applications"

// ListCloudHubApps lists the CloudHub 1.0 applications of an environment.
func (c *Client) ListCloudHubApps(ctx context.Context, orgID, envID string) ([]CloudHubApp, error) {
	var raw json.RawMessage
	if err := c.jsonH(ctx, http.MethodGet, cloudhubAppsV2, scopeHeaders(orgID, envID), nil, &raw); err != nil {
		return nil, err
	}
	return decodeList[CloudHubApp](raw)
}

// GetCloudHubApp returns a CloudHub 1.0 application by domain.
func (c *Client) GetCloudHubApp(ctx context.Context, orgID, envID, domain string) (*CloudHubApp, error) {
	var app CloudHubApp
	if err := c.jsonH(ctx, http.MethodGet, cloudhubAppsV2+"/"+esc(domain), scopeHeaders(orgID, envID), nil, &app); err != nil {
		return nil, err
	}
	return &app, nil
}

func (c *Client) cloudhubAppForm(ctx context.Context, orgID, envID, method, path string, appInfo any, autoStart bool, file *HybridFile, extra http.Header) (*CloudHubApp, error) {
	info, err := json.Marshal(appInfo)
	if err != nil {
		return nil, err
	}
	fields := map[string]string{"appInfoJson": string(info)}
	if autoStart {
		fields["autoStart"] = "true"
	}
	h := scopeHeaders(orgID, envID)
	maps.Copy(h, extra)
	data, err := c.multipartForm(ctx, method, path, h, fields, file)
	if err != nil {
		return nil, err
	}
	var app CloudHubApp
	if len(data) > 0 {
		if err := decode(data, &app); err != nil {
			return nil, err
		}
	}
	return &app, nil
}

// DeployCloudHubApp creates an application from its settings (appInfoJson)
// and archive, starting it when autoStart is set.
func (c *Client) DeployCloudHubApp(ctx context.Context, orgID, envID string, appInfo any, autoStart bool, file HybridFile) (*CloudHubApp, error) {
	return c.cloudhubAppForm(ctx, orgID, envID, http.MethodPost, cloudhubAppsV2, appInfo, autoStart, &file, nil)
}

// CloudHubUpdate says what an application update sends besides its settings.
type CloudHubUpdate struct {
	AutoStart bool        // start the application after the update
	File      *HybridFile // a new archive, if any
	// TargetInfo marks an update that replaces one application with a copy
	// of another (anypoint-cli-v4's copy-replace sends it).
	TargetInfo bool
}

// UpdateCloudHubApp changes an application's settings (appInfoJson) and,
// with u.File, its archive.
func (c *Client) UpdateCloudHubApp(ctx context.Context, orgID, envID, domain string, appInfo any, u CloudHubUpdate) (*CloudHubApp, error) {
	var extra http.Header
	if u.TargetInfo {
		extra = http.Header{"Target-Info": {"target-info"}}
	}
	return c.cloudhubAppForm(ctx, orgID, envID, http.MethodPut, cloudhubAppsV2+"/"+esc(domain), appInfo, u.AutoStart, u.File, extra)
}

// DeleteCloudHubApp deletes an application.
func (c *Client) DeleteCloudHubApp(ctx context.Context, orgID, envID, domain string) error {
	return c.jsonH(ctx, http.MethodDelete, cloudhubBase+"/applications/"+esc(domain), scopeHeaders(orgID, envID), nil, nil)
}

// SetCloudHubAppStatus starts ("start"), stops ("stop") or restarts
// ("restart") an application. The API answers 304 when nothing changes;
// see [IsStatus].
func (c *Client) SetCloudHubAppStatus(ctx context.Context, orgID, envID, domain, status string) error {
	return c.jsonH(ctx, http.MethodPost, cloudhubBase+"/applications/"+esc(domain)+"/status", scopeHeaders(orgID, envID), Doc{"status": status}, nil)
}

// DownloadCloudHubAppFile streams an application's deployed archive.
func (c *Client) DownloadCloudHubAppFile(ctx context.Context, orgID, envID, domain, fileName string) (io.ReadCloser, error) {
	h := scopeHeaders(orgID, envID)
	h.Set("Accept", "*/*")
	resp, err := c.Do(ctx, http.MethodGet, cloudhubBase+"/applications/"+esc(domain)+"/download/"+esc(fileName), nil, h)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// AllocateCloudHubStaticIP allocates a static IP for an application in a region.
func (c *Client) AllocateCloudHubStaticIP(ctx context.Context, orgID, envID, domain, region string) error {
	return c.jsonH(ctx, http.MethodPost, cloudhubAppsV2+"/"+esc(domain)+"/staticips", scopeHeaders(orgID, envID), Doc{"region": region}, nil)
}

// CloudHubDeployment is one deployment of an application.
type CloudHubDeployment struct {
	rawJSON
	DeploymentID string `json:"deploymentId"`
	CreateTime   any    `json:"createTime"`
	Instances    []struct {
		InstanceID string `json:"instanceId"`
	} `json:"instances"`
}

// ListCloudHubDeployments lists an application's deployments, newest first.
func (c *Client) ListCloudHubDeployments(ctx context.Context, orgID, envID, domain string, limit int) ([]CloudHubDeployment, error) {
	q := url.Values{"orderByDate": {"DESC"}}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var raw json.RawMessage
	if err := c.jsonH(ctx, http.MethodGet, withQuery(cloudhubAppsV2+"/"+esc(domain)+"/deployments", q), scopeHeaders(orgID, envID), nil, &raw); err != nil {
		return nil, err
	}
	return decodeList[CloudHubDeployment](raw, "data")
}

// GetCloudHubInstanceLog returns the log file of one worker instance.
func (c *Client) GetCloudHubInstanceLog(ctx context.Context, orgID, envID, domain, instanceID string) ([]byte, error) {
	h := scopeHeaders(orgID, envID)
	h.Set("Accept", "*/*")
	resp, err := c.Do(ctx, http.MethodGet, cloudhubAppsV2+"/"+esc(domain)+"/instances/"+esc(instanceID)+"/log-file", nil, h)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// CloudHubLogLine is one line of a deployment's log.
type CloudHubLogLine struct {
	Timestamp int64  `json:"timestamp"`
	Message   string `json:"message"`
}

// ListCloudHubDeploymentLogs returns the latest log lines of a deployment.
func (c *Client) ListCloudHubDeploymentLogs(ctx context.Context, orgID, envID, domain, deploymentID string, limit int) ([]CloudHubLogLine, error) {
	var raw json.RawMessage
	p := withQuery(cloudhubAppsV2+"/"+esc(domain)+"/deployments/"+esc(deploymentID)+"/logs", url.Values{"limit": {strconv.Itoa(limit)}})
	if err := c.jsonH(ctx, http.MethodGet, p, scopeHeaders(orgID, envID), nil, &raw); err != nil {
		return nil, err
	}
	return decodeList[CloudHubLogLine](raw, "data")
}

// ---------------------------------------------------------------- alerts

// CloudHubAlert is a CloudHub 1.0 alert.
type CloudHubAlert struct {
	rawJSON
	ID           string `json:"id"`
	Name         string `json:"name"`
	Severity     string `json:"severity"`
	Enabled      bool   `json:"enabled"`
	LastModified any    `json:"lastModified,omitempty"`
	Condition    struct {
		Type         string   `json:"type"`
		ResourceType string   `json:"resourceType"`
		Resources    []string `json:"resources"`
	} `json:"condition"`
}

// ListCloudHubAlerts lists one page of an environment's CloudHub alerts.
func (c *Client) ListCloudHubAlerts(ctx context.Context, orgID, envID string, limit, offset int) ([]CloudHubAlert, error) {
	var raw json.RawMessage
	q := url.Values{"limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
	if err := c.jsonH(ctx, http.MethodGet, withQuery(cloudhubBase+"/v2/alerts", q), scopeHeaders(orgID, envID), nil, &raw); err != nil {
		return nil, err
	}
	return decodeList[CloudHubAlert](raw, "data")
}

// AlertTrigger is one firing of an alert.
type AlertTrigger struct {
	TriggeredAt any            `json:"triggeredAt"`
	Context     map[string]any `json:"context"`
}

// GetCloudHubAlertHistory returns the latest (up to 1000) firings of an alert.
func (c *Client) GetCloudHubAlertHistory(ctx context.Context, orgID, envID, alertID string) ([]AlertTrigger, error) {
	var raw json.RawMessage
	p := withQuery(cloudhubBase+"/v2/alerts/"+esc(alertID)+"/history", url.Values{"limit": {"1000"}})
	if err := c.jsonH(ctx, http.MethodGet, p, scopeHeaders(orgID, envID), nil, &raw); err != nil {
		return nil, err
	}
	return decodeList[AlertTrigger](raw, "data")
}
