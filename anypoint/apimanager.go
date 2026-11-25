package anypoint

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
)

// API Manager: API instances, deployments, policies, SLA tiers, contracts,
// alerts, upstreams and client applications. Endpoints follow
// anypoint-cli-v4's api-mgr plugin; outbound policies follow the Terraform
// provider. API instance IDs are passed as strings, as the CLI takes them.

// FlexID is an ID the API returns as a number or a string.
type FlexID string

// UnmarshalJSON accepts a JSON string, number or null.
func (f *FlexID) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = FlexID(s)
		return nil
	}
	if string(b) == "null" {
		*f = ""
		return nil
	}
	*f = FlexID(b)
	return nil
}

// String returns the ID.
func (f FlexID) String() string { return string(f) }

// Doc is a loosely typed JSON object, for bodies the API defines openly
// (policy configuration, deployment records) and read-modify-write updates.
type Doc = map[string]any

// Audit carries creation and update times.
type Audit struct {
	Created struct {
		Date string `json:"date"`
	} `json:"created"`
	Updated struct {
		Date string `json:"date"`
	} `json:"updated"`
}

// Changed returns the update time, or the creation time.
func (a *Audit) Changed() string {
	if a == nil {
		return ""
	}
	if a.Updated.Date != "" {
		return a.Updated.Date
	}
	return a.Created.Date
}

// ---------------------------------------------------------------- API instances

// APIInstance is a managed API.
type APIInstance struct {
	rawJSON
	ID                        int            `json:"id"`
	GroupID                   string         `json:"groupId,omitempty"`
	AssetID                   string         `json:"assetId,omitempty"`
	AssetVersion              string         `json:"assetVersion,omitempty"`
	ProductVersion            string         `json:"productVersion,omitempty"`
	Technology                string         `json:"technology,omitempty"`
	EndpointURI               string         `json:"endpointUri,omitempty"`
	ProviderID                *string        `json:"providerId,omitempty"`
	InstanceLabel             string         `json:"instanceLabel,omitempty"`
	Status                    string         `json:"status,omitempty"`
	EnvironmentID             string         `json:"environmentId,omitempty"`
	Deprecated                bool           `json:"deprecated"`
	IsPublic                  bool           `json:"isPublic"`
	Tags                      []string       `json:"tags,omitempty"`
	LastActiveDate            string         `json:"lastActiveDate,omitempty"`
	AutodiscoveryInstanceName string         `json:"autodiscoveryInstanceName,omitempty"`
	Endpoint                  *APIEndpoint   `json:"endpoint,omitempty"`
	Deployment                *APIDeployment `json:"deployment,omitempty"`
}

// APIEndpoint is an API instance's implementation and proxy configuration.
type APIEndpoint struct {
	Type                 string          `json:"type,omitempty"`
	URI                  *string         `json:"uri,omitempty"`
	ProxyURI             *string         `json:"proxyUri,omitempty"`
	DeploymentType       string          `json:"deploymentType,omitempty"`
	IsCloudHub           *bool           `json:"isCloudHub,omitempty"`
	ReferencesUserDomain *bool           `json:"referencesUserDomain,omitempty"`
	ResponseTimeout      *int            `json:"responseTimeout,omitempty"`
	MuleVersion4OrAbove  *bool           `json:"muleVersion4OrAbove,omitempty"`
	WSDLConfig           *WSDLConfig     `json:"wsdlConfig,omitempty"`
	TLSContexts          *APITLSContexts `json:"tlsContexts,omitempty"`
}

// WSDLConfig names the service of a SOAP proxy.
type WSDLConfig struct {
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Port      string `json:"port,omitempty"`
}

// APITLSContexts are a proxy's inbound and outbound TLS contexts.
type APITLSContexts struct {
	Inbound  *APITLSContextRef `json:"inbound"`
	Outbound *APITLSContextRef `json:"outbound"`
}

// APITLSContextRef points at a Secrets Manager TLS context.
type APITLSContextRef struct {
	SecretGroupID string `json:"secretGroupId"`
	TLSContextID  string `json:"tlsContextId"`
	Name          string `json:"name,omitempty"`
}

// APIDeployment summarizes where an API's proxy runs.
type APIDeployment struct {
	ID              FlexID `json:"id,omitempty"`
	Type            string `json:"type,omitempty"`
	ApplicationName string `json:"applicationName,omitempty"`
	EnvironmentName string `json:"environmentName,omitempty"`
	GatewayVersion  string `json:"gatewayVersion,omitempty"`
	TargetID        FlexID `json:"targetId,omitempty"`
}

// APIAsset groups the API instances of one Exchange asset.
type APIAsset struct {
	ID                FlexID        `json:"id"`
	GroupID           string        `json:"groupId"`
	AssetID           string        `json:"assetId"`
	ExchangeAssetName string        `json:"exchangeAssetName"`
	APIs              []APIInstance `json:"apis"`
}

// APIAssetPage is a page of API Manager's asset listing.
type APIAssetPage struct {
	rawJSON
	Total  int        `json:"total"`
	Assets []APIAsset `json:"assets"`
}

// APIListOptions filters and pages ListAPIInstances.
type APIListOptions struct {
	GroupID, AssetID, AssetVersion string
	ProductVersion, InstanceLabel  string
	Limit, Offset                  int
	Sort                           string
	Ascending                      bool
}

// ListQuery pages and filters the list endpoints of API instances (tiers,
// contracts, alerts).
type ListQuery struct {
	Limit, Offset int
	Sort          string
	Query         string // text search; tiers and contracts only
	Ascending     bool
}

func (q ListQuery) values() url.Values {
	v := url.Values{}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Offset > 0 {
		v.Set("offset", strconv.Itoa(q.Offset))
	}
	if q.Sort != "" {
		v.Set("sort", q.Sort)
	}
	if q.Query != "" {
		v.Set("query", q.Query)
	}
	v.Set("ascending", strconv.FormatBool(q.Ascending))
	return v
}

func apisPath(orgID, envID string) string {
	return "/apimanager/api/v1/organizations/" + esc(orgID) + "/environments/" + esc(envID) + "/apis"
}

func apiPath(orgID, envID, apiID string) string { return apisPath(orgID, envID) + "/" + esc(apiID) }

// ListAPIInstances lists API instances, grouped by Exchange asset.
func (c *Client) ListAPIInstances(ctx context.Context, orgID, envID string, o APIListOptions) (*APIAssetPage, error) {
	q := url.Values{}
	for k, v := range map[string]string{"groupId": o.GroupID, "assetId": o.AssetID, "assetVersion": o.AssetVersion,
		"productVersion": o.ProductVersion, "instanceLabel": o.InstanceLabel, "sort": o.Sort} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	if o.Offset > 0 {
		q.Set("offset", strconv.Itoa(o.Offset))
	}
	q.Set("ascending", strconv.FormatBool(o.Ascending))
	return get[APIAssetPage](ctx, c, withQuery(apisPath(orgID, envID), q))
}

// GetAPIInstance returns an API instance; includeTLS adds TLS context names.
func (c *Client) GetAPIInstance(ctx context.Context, orgID, envID, apiID string, includeTLS bool) (*APIInstance, error) {
	p := apiPath(orgID, envID, apiID)
	if includeTLS {
		p = withQuery(p, url.Values{"includeTlsContexts": {"true"}})
	}
	return get[APIInstance](ctx, c, p)
}

// CreateAPIInstance creates (manages) or promotes an API instance; body is
// the API's create document (see the CLI's api manage and api promote).
func (c *Client) CreateAPIInstance(ctx context.Context, orgID, envID string, body any) (*APIInstance, error) {
	return do[APIInstance](ctx, c, http.MethodPost, apisPath(orgID, envID), body)
}

// UpdateAPIInstance patches an API instance, e.g. {"deprecated": true} or
// {"assetVersion": "1.0.1"}. query may be nil.
func (c *Client) UpdateAPIInstance(ctx context.Context, orgID, envID, apiID string, body any, query url.Values) (*APIInstance, error) {
	return do[APIInstance](ctx, c, http.MethodPatch, withQuery(apiPath(orgID, envID, apiID), query), body)
}

// UpdateAPIInstanceXAPI patches an API instance through the experience API,
// which also accepts {"upstreams": [...]}.
func (c *Client) UpdateAPIInstanceXAPI(ctx context.Context, orgID, envID, apiID string, body any) error {
	p := "/apimanager/xapi/v1/organizations/" + esc(orgID) + "/environments/" + esc(envID) + "/apis/" + esc(apiID)
	return c.JSON(ctx, http.MethodPatch, p, body, nil)
}

// DeleteAPIInstance deletes an API instance.
func (c *Client) DeleteAPIInstance(ctx context.Context, orgID, envID, apiID string) error {
	return c.JSON(ctx, http.MethodDelete, apiPath(orgID, envID, apiID), nil, nil)
}

// GetAutodiscoveryProperties returns the properties a Mule app needs to
// pair with the API instance on a gateway version.
func (c *Client) GetAutodiscoveryProperties(ctx context.Context, orgID, envID, apiID, gatewayVersion string) (Doc, error) {
	p := withQuery(apiPath(orgID, envID, apiID)+"/autodiscoveryProperties", url.Values{"gatewayVersion": {gatewayVersion}})
	out, err := get[Doc](ctx, c, p)
	if err != nil {
		return nil, err
	}
	return *out, nil
}

// DownloadProxy fetches the generated proxy application (gatewayVersion
// defaults to 3.9.0, as in anypoint-cli-v4). The caller closes the body.
func (c *Client) DownloadProxy(ctx context.Context, orgID, envID, apiID, gatewayVersion string) (body io.ReadCloser, fileName string, err error) {
	if gatewayVersion == "" {
		gatewayVersion = "3.9.0"
	}
	p := "/proxies/xapi/v1/organizations/" + esc(orgID) + "/environments/" + esc(envID) + "/apis/" + esc(apiID) + "/proxy"
	resp, err := c.Do(ctx, http.MethodGet, withQuery(p, url.Values{"gatewayVersion": {gatewayVersion}}), nil, http.Header{"Accept": {"*/*"}})
	if err != nil {
		return nil, "", err
	}
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		fileName = params["filename"]
	}
	return resp.Body, fileName, nil
}

// ClassifyAPI moves an API instance of the unclassified environment into envID.
// assetID is API Manager's ID of the instance's asset (APIAsset.ID).
func (c *Client) ClassifyAPI(ctx context.Context, orgID, assetID, apiID, envID string) error {
	p := "/apiplatform/repository/v2/organizations/" + esc(orgID) + "/apis/" + esc(assetID) + "/versions/" + esc(apiID) + "/classify"
	return c.JSON(ctx, http.MethodPost, p, Doc{"environmentId": envID}, nil)
}

// ---------------------------------------------------------------- deployments

func apiDeploymentsPath(orgID, envID, apiID string) string {
	return "/proxies/xapi/v1/organizations/" + esc(orgID) + "/environments/" + esc(envID) + "/apis/" + esc(apiID) + "/deployments"
}

// ListAPIDeployments lists the proxy deployments of an API instance, as
// documents so they can be sent back changed.
func (c *Client) ListAPIDeployments(ctx context.Context, orgID, envID, apiID string) ([]Doc, error) {
	return list[Doc](ctx, c, apiDeploymentsPath(orgID, envID, apiID))
}

// CreateAPIDeployment deploys an API instance's proxy.
func (c *Client) CreateAPIDeployment(ctx context.Context, orgID, envID, apiID string, body any) error {
	return c.JSON(ctx, http.MethodPost, apiDeploymentsPath(orgID, envID, apiID), body, nil)
}

// UpdateAPIDeployment redeploys an API instance's proxy.
func (c *Client) UpdateAPIDeployment(ctx context.Context, orgID, envID, apiID, deploymentID string, body any) error {
	return c.JSON(ctx, http.MethodPatch, apiDeploymentsPath(orgID, envID, apiID)+"/"+esc(deploymentID), body, nil)
}

// DeploymentTarget is a hybrid server, cluster, group or Flex Gateway.
type DeploymentTarget struct {
	rawJSON
	ID             FlexID `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	GatewayVersion string `json:"gatewayVersion,omitempty"`
}

// ListDeploymentTargets lists the hybrid (Mule) targets of an environment.
func (c *Client) ListDeploymentTargets(ctx context.Context, orgID, envID string) ([]DeploymentTarget, error) {
	p := withQuery("/proxies/xapi/v1/organizations/"+esc(orgID)+"/deployment-targets", url.Values{"environmentId": {envID}})
	return list[DeploymentTarget](ctx, c, p)
}

// GetGatewayTarget returns a Flex Gateway deployment target.
func (c *Client) GetGatewayTarget(ctx context.Context, orgID, envID, targetID string) (*DeploymentTarget, error) {
	p := "/apimanager/xapi/v1/organizations/" + esc(orgID) + "/environments/" + esc(envID) + "/gateway-targets/" + esc(targetID)
	return get[DeploymentTarget](ctx, c, withQuery(p, url.Values{"enviromentId": {envID}, "targetId": {targetID}}))
}

// RuntimeTarget is a Runtime Fabric or CloudHub 2.0 target with its runtimes.
type RuntimeTarget struct {
	rawJSON
	ID                        string `json:"id"`
	Name                      string `json:"name"`
	Version                   string `json:"version,omitempty"`
	Provider                  string `json:"provider,omitempty"`
	IsAvailableForDeployments bool   `json:"isAvailableForDeployments,omitempty"`
	Runtimes                  []struct {
		Type     string `json:"type"`
		Versions []struct {
			BaseVersion string `json:"baseVersion"`
			Tag         string `json:"tag"`
		} `json:"versions"`
	} `json:"runtimes"`
}

// HasRuntimeVersion reports whether the target offers a runtime base version.
func (t *RuntimeTarget) HasRuntimeVersion(v string) bool {
	for _, r := range t.Runtimes {
		for _, rv := range r.Versions {
			if rv.BaseVersion == v {
				return true
			}
		}
	}
	return false
}

// GetRuntimeTarget returns a Runtime Manager target (Runtime Fabric or
// CloudHub 2.0 space) of provider MC.
func (c *Client) GetRuntimeTarget(ctx context.Context, orgID, envID, targetID string) (*RuntimeTarget, error) {
	p := "/amc/atlas/api/v1/organizations/" + esc(orgID) + "/providers/MC/targets/" + esc(targetID)
	return get[RuntimeTarget](ctx, c, withQuery(p, url.Values{"environmentId": {envID}}))
}

// ---------------------------------------------------------------- policies

// APIPolicy is a policy applied to an API instance.
type APIPolicy struct {
	rawJSON
	ID                FlexID   `json:"id"`
	PolicyTemplateID  FlexID   `json:"policyTemplateId,omitempty"`
	GroupID           string   `json:"groupId,omitempty"`
	AssetID           string   `json:"assetId,omitempty"`
	AssetVersion      string   `json:"assetVersion,omitempty"`
	Label             string   `json:"label,omitempty"`
	Order             *int     `json:"order"`
	Disabled          bool     `json:"disabled"`
	PointcutData      any      `json:"pointcutData"`
	ConfigurationData Doc      `json:"configurationData,omitempty"`
	APIID             FlexID   `json:"apiId,omitempty"`
	UpstreamID        string   `json:"upstreamId,omitempty"`
	UpstreamIDs       []string `json:"upstreamIds,omitempty"`
	Audit             *Audit   `json:"audit,omitempty"`
}

// PolicyOrder is one entry of a policy reordering.
type PolicyOrder struct {
	ID    int `json:"id"`
	Order int `json:"order"`
}

func policiesPath(orgID, envID, apiID string) string {
	return apiPath(orgID, envID, apiID) + "/policies"
}

func allowDup(p string, allowDuplicated bool) string {
	if allowDuplicated {
		return withQuery(p, url.Values{"allowDuplicated": {"true"}})
	}
	return p
}

// ListAPIPolicies lists the policies applied to an API instance.
func (c *Client) ListAPIPolicies(ctx context.Context, orgID, envID, apiID string) ([]APIPolicy, error) {
	return list[APIPolicy](ctx, c, withQuery(policiesPath(orgID, envID, apiID), url.Values{"fullInfo": {"false"}}), "policies")
}

// GetAPIPolicy returns an applied policy (from the list, as anypoint-cli-v4 does).
func (c *Client) GetAPIPolicy(ctx context.Context, orgID, envID, apiID, policyID string) (*APIPolicy, error) {
	policies, err := c.ListAPIPolicies(ctx, orgID, envID, apiID)
	if err != nil {
		return nil, err
	}
	for i := range policies {
		if a, b := atoi(policies[i].ID.String()), atoi(policyID); a == b && a >= 0 {
			return &policies[i], nil
		}
	}
	return nil, notFound("policy", policyID)
}

func atoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}

// ApplyAPIPolicy applies a policy; body is the policy document (see the
// CLI's policy apply).
func (c *Client) ApplyAPIPolicy(ctx context.Context, orgID, envID, apiID string, body any, allowDuplicated bool) (*APIPolicy, error) {
	return do[APIPolicy](ctx, c, http.MethodPost, allowDup(policiesPath(orgID, envID, apiID), allowDuplicated), body)
}

// UpdateAPIPolicy patches an applied policy.
func (c *Client) UpdateAPIPolicy(ctx context.Context, orgID, envID, apiID, policyID string, body any, allowDuplicated bool) error {
	return c.JSON(ctx, http.MethodPatch, allowDup(policiesPath(orgID, envID, apiID)+"/"+esc(policyID), allowDuplicated), body, nil)
}

// SetAPIPolicyDisabled disables or enables an applied policy.
func (c *Client) SetAPIPolicyDisabled(ctx context.Context, orgID, envID, apiID, policyID string, disabled bool) error {
	return c.UpdateAPIPolicy(ctx, orgID, envID, apiID, policyID, Doc{"disabled": disabled}, false)
}

// RemoveAPIPolicy removes an applied policy.
func (c *Client) RemoveAPIPolicy(ctx context.Context, orgID, envID, apiID, policyID string) error {
	return c.JSON(ctx, http.MethodDelete, policiesPath(orgID, envID, apiID)+"/"+esc(policyID), nil, nil)
}

// ReorderAPIPolicies sets the order of all ordered policies of an API instance.
func (c *Client) ReorderAPIPolicies(ctx context.Context, orgID, envID, apiID string, order []PolicyOrder) error {
	return c.JSON(ctx, http.MethodPatch, policiesPath(orgID, envID, apiID), order, nil)
}

func outboundPoliciesPath(orgID, envID, apiID string) string {
	return "/apimanager/xapi/v1/organizations/" + esc(orgID) + "/environments/" + esc(envID) + "/apis/" + esc(apiID) + "/policies/outbound-policies"
}

// ListOutboundAPIPolicies lists the outbound (upstream) policies of an API instance.
func (c *Client) ListOutboundAPIPolicies(ctx context.Context, orgID, envID, apiID string) ([]APIPolicy, error) {
	return list[APIPolicy](ctx, c, outboundPoliciesPath(orgID, envID, apiID), "policies")
}

// ApplyOutboundAPIPolicy applies an outbound policy to upstreams; body
// includes "upstreamIds".
func (c *Client) ApplyOutboundAPIPolicy(ctx context.Context, orgID, envID, apiID string, body any) (*APIPolicy, error) {
	return do[APIPolicy](ctx, c, http.MethodPost, outboundPoliciesPath(orgID, envID, apiID), body)
}

// UpdateOutboundAPIPolicy patches an outbound policy.
func (c *Client) UpdateOutboundAPIPolicy(ctx context.Context, orgID, envID, apiID, policyID string, body any) (*APIPolicy, error) {
	return do[APIPolicy](ctx, c, http.MethodPatch, outboundPoliciesPath(orgID, envID, apiID)+"/"+esc(policyID), body)
}

// RemoveOutboundAPIPolicy removes an outbound policy.
func (c *Client) RemoveOutboundAPIPolicy(ctx context.Context, orgID, envID, apiID, policyID string) error {
	return c.JSON(ctx, http.MethodDelete, outboundPoliciesPath(orgID, envID, apiID)+"/"+esc(policyID), nil, nil)
}

// PolicyTemplate is a Mule 3 policy template.
type PolicyTemplate struct {
	rawJSON
	ID            FlexID                `json:"id"`
	Name          string                `json:"name"`
	Description   string                `json:"description"`
	Category      string                `json:"category"`
	Definition    string                `json:"definition,omitempty"` // YAML, when the template carries one
	Configuration []PolicyConfiguration `json:"configuration,omitempty"`
}

// PolicyConfiguration describes one configuration property of a policy.
type PolicyConfiguration struct {
	PropertyName   string                `json:"propertyName"`
	Name           string                `json:"name,omitempty"`
	Description    string                `json:"description,omitempty"`
	Type           string                `json:"type,omitempty"`
	Optional       bool                  `json:"optional"`
	AllowMultiple  bool                  `json:"allowMultiple,omitempty"`
	DefaultValue   any                   `json:"defaultValue,omitempty"`
	DependsOnKey   string                `json:"dependsOnKey,omitempty"`
	DependsOnValue any                   `json:"dependsOnValue,omitempty"`
	Options        []struct{ Value any } `json:"options,omitempty"`
	Configuration  []PolicyConfiguration `json:"configuration,omitempty"`
}

func policyTemplatesPath(orgID string) string {
	return "/apimanager/api/v1/organizations/" + esc(orgID) + "/policy-templates"
}

// ListPolicyTemplates lists the Mule 3 policy templates.
func (c *Client) ListPolicyTemplates(ctx context.Context, orgID string) ([]PolicyTemplate, error) {
	return list[PolicyTemplate](ctx, c, policyTemplatesPath(orgID))
}

// GetPolicyTemplate returns a Mule 3 policy template.
func (c *Client) GetPolicyTemplate(ctx context.Context, orgID, templateID string) (*PolicyTemplate, error) {
	return get[PolicyTemplate](ctx, c, policyTemplatesPath(orgID)+"/"+esc(templateID))
}

// MuleSoftGroupID is the Exchange group of MuleSoft's own policies.
const MuleSoftGroupID = "68ef9520-24e9-4cf2-b2f5-620025690913"

// ListPolicyAssets lists the Mule 4 policies in Exchange (GraphQL).
func (c *Client) ListPolicyAssets(ctx context.Context) ([]Asset, error) {
	const q = `{ assets(types: "policy", limit: 100, offset: 0, latestVersionsOnly: false) { groupId assetId version description name } }`
	var out struct {
		Data struct {
			Assets []Asset `json:"assets"`
		} `json:"data"`
	}
	if err := c.GraphQL(ctx, q, nil, &out); err != nil {
		return nil, err
	}
	return out.Data.Assets, nil
}

// GetPolicyAsset returns a Mule 4 policy's Exchange asset with its file
// links (GraphQL); the "policy-definition" file holds its YAML definition.
func (c *Client) GetPolicyAsset(ctx context.Context, groupID, assetID, version string) (*Asset, error) {
	if groupID == "" {
		groupID = MuleSoftGroupID
	}
	const q = `query($groupId: String!, $assetId: String!, $version: String!) {
  asset(groupId: $groupId, assetId: $assetId, version: $version) { name description assetId files { classifier externalLink } }
}`
	var out struct {
		Data struct {
			Asset *Asset `json:"asset"`
		} `json:"data"`
	}
	if err := c.GraphQL(ctx, q, map[string]any{"groupId": groupID, "assetId": assetID, "version": version}, &out); err != nil {
		return nil, err
	}
	if out.Data.Asset == nil {
		return nil, notFound("policy", groupID+"/"+assetID+"/"+version)
	}
	return out.Data.Asset, nil
}

// ---------------------------------------------------------------- SLA tiers

// SLATier is an SLA tier of an API instance.
type SLATier struct {
	rawJSON
	ID               int        `json:"id,omitempty"`
	Name             string     `json:"name"`
	Description      string     `json:"description"`
	AutoApprove      bool       `json:"autoApprove"`
	Status           string     `json:"status,omitempty"`
	Limits           []SLALimit `json:"limits"`
	APIVersionID     FlexID     `json:"apiVersionId,omitempty"`
	ApplicationCount int        `json:"applicationCount,omitempty"`
	Audit            *Audit     `json:"audit,omitempty"`
}

// SLALimit is a rate limit of an SLA tier.
type SLALimit struct {
	Visible                  bool  `json:"visible"`
	MaximumRequests          int   `json:"maximumRequests"`
	TimePeriodInMilliseconds int64 `json:"timePeriodInMilliseconds"`
}

func tiersPath(orgID, envID, apiID string) string { return apiPath(orgID, envID, apiID) + "/tiers" }

// ListSLATiers lists the SLA tiers of an API instance.
func (c *Client) ListSLATiers(ctx context.Context, orgID, envID, apiID string, q ListQuery) ([]SLATier, error) {
	return list[SLATier](ctx, c, withQuery(tiersPath(orgID, envID, apiID), q.values()), "tiers")
}

// CreateSLATier creates an SLA tier. The API instance ID is set as apiVersionId.
func (c *Client) CreateSLATier(ctx context.Context, orgID, envID, apiID string, tier *SLATier) (*SLATier, error) {
	body := *tier
	body.APIVersionID = FlexID(apiID)
	body.Audit, body.ID = nil, 0
	return do[SLATier](ctx, c, http.MethodPost, tiersPath(orgID, envID, apiID), tierBody(&body))
}

// UpdateSLATier replaces an SLA tier.
func (c *Client) UpdateSLATier(ctx context.Context, orgID, envID, apiID string, tier *SLATier) (*SLATier, error) {
	return do[SLATier](ctx, c, http.MethodPut, tiersPath(orgID, envID, apiID)+"/"+strconv.Itoa(tier.ID), tierBody(tier))
}

// tierBody marshals a tier without the read-only audit and counters.
func tierBody(t *SLATier) Doc {
	d := Doc{"name": t.Name, "description": t.Description, "autoApprove": t.AutoApprove, "limits": t.Limits,
		"apiVersionId": t.APIVersionID}
	if t.Limits == nil {
		d["limits"] = []SLALimit{}
	}
	if t.Status != "" {
		d["status"] = t.Status
	}
	if t.ID != 0 {
		d["id"] = t.ID
	}
	return d
}

// DeleteSLATier deletes an SLA tier.
func (c *Client) DeleteSLATier(ctx context.Context, orgID, envID, apiID, tierID string) error {
	return c.JSON(ctx, http.MethodDelete, tiersPath(orgID, envID, apiID)+"/"+esc(tierID), nil, nil)
}

// ---------------------------------------------------------------- contracts

// Contract grants a client application access to an API instance.
type Contract struct {
	rawJSON
	ID          int    `json:"id"`
	Status      string `json:"status"`
	Application struct {
		ID             FlexID `json:"id"`
		Name           string `json:"name"`
		CoreServicesID string `json:"coreServicesId"`
	} `json:"application"`
	Tier          *SLATier `json:"tier,omitempty"`
	RequestedTier *SLATier `json:"requestedTier,omitempty"`
	Audit         *Audit   `json:"audit,omitempty"`
}

func contractsPath(orgID, envID, apiID string) string {
	return apiPath(orgID, envID, apiID) + "/contracts"
}

// ListContracts lists the contracts of an API instance.
func (c *Client) ListContracts(ctx context.Context, orgID, envID, apiID string, q ListQuery) ([]Contract, error) {
	return list[Contract](ctx, c, withQuery(contractsPath(orgID, envID, apiID), q.values()), "contracts")
}

// GetContractByClientID returns the contract of the application with a client ID.
func (c *Client) GetContractByClientID(ctx context.Context, orgID, envID, apiID, clientID string) (*Contract, error) {
	q := url.Values{"limit": {"1"}, "coreServicesId": {clientID}}
	cs, err := list[Contract](ctx, c, withQuery(contractsPath(orgID, envID, apiID), q), "contracts")
	if err != nil {
		return nil, err
	}
	if len(cs) == 0 {
		return nil, notFound("contract for client ID", clientID)
	}
	return &cs[0], nil
}

// DeleteContract deletes a contract.
func (c *Client) DeleteContract(ctx context.Context, orgID, envID, apiID string, contractID int) error {
	return c.JSON(ctx, http.MethodDelete, contractsPath(orgID, envID, apiID)+"/"+strconv.Itoa(contractID), nil, nil)
}

// ---------------------------------------------------------------- alerts

// APIAlert is an alert of an API instance: either a monitoring alert
// (AlertID, MetricType, State, Notifications) or a legacy one (ID, Type,
// Enabled, Recipients).
type APIAlert struct {
	rawJSON
	AlertID       string `json:"alertId,omitempty"`
	ID            FlexID `json:"id,omitempty"`
	Name          string `json:"name"`
	Severity      string `json:"severity"`
	MetricType    string `json:"metricType,omitempty"`
	Type          string `json:"type,omitempty"`
	State         string `json:"state,omitempty"`
	Enabled       bool   `json:"enabled,omitempty"`
	LastModified  string `json:"lastModified,omitempty"`
	CreatedAt     string `json:"createdAt,omitempty"`
	Notifications []struct {
		Recipients []string `json:"recipients"`
	} `json:"notifications,omitempty"`
	Recipients []any `json:"recipients,omitempty"`
}

func monitoringAlertsPath(orgID, envID string) string {
	return "/monitoring/api/alerts/api/v2/organizations/" + esc(orgID) + "/environments/" + esc(envID) + "/alerts"
}

// ListAPIAlerts lists an API instance's monitoring alerts.
func (c *Client) ListAPIAlerts(ctx context.Context, orgID, envID, apiID string, q ListQuery) ([]APIAlert, error) {
	v := q.values()
	v.Set("type", "basic")
	v.Set("apiId", apiID)
	v.Set("resourceType", "api")
	return list[APIAlert](ctx, c, withQuery(monitoringAlertsPath(orgID, envID), v), "alerts", "data")
}

// CreateAPIAlert creates a monitoring alert; body is the alert document
// (see the CLI's alert add). It returns the new alert's ID.
func (c *Client) CreateAPIAlert(ctx context.Context, orgID, envID string, body any) (string, error) {
	a, err := do[APIAlert](ctx, c, http.MethodPost, monitoringAlertsPath(orgID, envID), body)
	if err != nil {
		return "", err
	}
	return a.AlertID, nil
}

// ListAPIAlertsLegacy lists an API instance's legacy API Manager alerts.
func (c *Client) ListAPIAlertsLegacy(ctx context.Context, orgID, envID, apiID string, q ListQuery) ([]APIAlert, error) {
	q.Query = ""
	return list[APIAlert](ctx, c, withQuery(apiPath(orgID, envID, apiID)+"/alerts", q.values()))
}

// CreateAPIAlertLegacy creates a legacy API Manager alert and returns its ID.
func (c *Client) CreateAPIAlertLegacy(ctx context.Context, orgID, envID, apiID string, body any) (string, error) {
	a, err := do[APIAlert](ctx, c, http.MethodPost, apiPath(orgID, envID, apiID)+"/alerts", body)
	if err != nil {
		return "", err
	}
	return a.ID.String(), nil
}

// ListOrganizationMembers lists an organization's members (users with their
// e-mail addresses).
func (c *Client) ListOrganizationMembers(ctx context.Context, orgID string) ([]User, error) {
	return list[User](ctx, c, orgPath(orgID)+"/members", "data")
}

// ---------------------------------------------------------------- upstreams

// APIUpstream is a backend of a Flex Gateway API instance.
type APIUpstream struct {
	rawJSON
	ID         string            `json:"id"`
	Label      string            `json:"label,omitempty"`
	URI        string            `json:"uri"`
	TLSContext *APITLSContextRef `json:"tlsContext,omitempty"`
	Audit      *Audit            `json:"audit,omitempty"`
}

func upstreamsPath(orgID, envID, apiID string) string {
	return apiPath(orgID, envID, apiID) + "/upstreams"
}

// ListUpstreams lists the upstreams of an API instance.
func (c *Client) ListUpstreams(ctx context.Context, orgID, envID, apiID string) ([]APIUpstream, error) {
	return list[APIUpstream](ctx, c, upstreamsPath(orgID, envID, apiID), "upstreams")
}

// CreateUpstream adds an upstream; body has uri and optionally label and tlsContext.
func (c *Client) CreateUpstream(ctx context.Context, orgID, envID, apiID string, body any) (*APIUpstream, error) {
	return do[APIUpstream](ctx, c, http.MethodPost, upstreamsPath(orgID, envID, apiID), body)
}

// UpdateUpstream patches an upstream ("tlsContext": null removes the TLS context).
func (c *Client) UpdateUpstream(ctx context.Context, orgID, envID, apiID, upstreamID string, body any) error {
	return c.JSON(ctx, http.MethodPatch, upstreamsPath(orgID, envID, apiID)+"/"+esc(upstreamID), body, nil)
}

// DeleteUpstream deletes an upstream.
func (c *Client) DeleteUpstream(ctx context.Context, orgID, envID, apiID, upstreamID string) error {
	return c.JSON(ctx, http.MethodDelete, upstreamsPath(orgID, envID, apiID)+"/"+esc(upstreamID), nil, nil)
}

// ---------------------------------------------------------------- client applications

// ClientProvider is an identity provider whose clients can be imported.
type ClientProvider struct {
	rawJSON
	ProviderID string `json:"providerId"`
	Name       string `json:"name"`
}

// ListImportableClientProviders lists the client providers applications can be imported from.
func (c *Client) ListImportableClientProviders(ctx context.Context, orgID string) ([]ClientProvider, error) {
	return list[ClientProvider](ctx, c, "/apimanager/xapi/v1/organizations/"+esc(orgID)+"/importable-client-identity-providers")
}

// ImportClientApplication imports a client of an identity provider as a
// client application and returns its ID. description and appURL may be empty.
func (c *Client) ImportClientApplication(ctx context.Context, orgID, providerID, clientID, description, appURL string) (string, error) {
	body := Doc{"providerId": providerID, "clientId": clientID}
	if description != "" {
		body["description"] = description
	}
	if appURL != "" {
		body["url"] = appURL
	}
	var out struct {
		ID FlexID `json:"id"`
	}
	if err := c.JSON(ctx, http.MethodPost, "/apimanager/api/v1/organizations/"+esc(orgID)+"/applications", body, &out); err != nil {
		return "", err
	}
	return out.ID.String(), nil
}

// ApplicationOwner is a user or team that can own a client application.
type ApplicationOwner struct {
	rawJSON
	ID         string `json:"id"`
	FirstName  string `json:"firstName,omitempty"`
	LastName   string `json:"lastName,omitempty"`
	Email      string `json:"email,omitempty"`
	Username   string `json:"username,omitempty"`
	EntityType string `json:"entityType"`
}

// ListPossibleApplicationOwners lists who can own client applications.
func (c *Client) ListPossibleApplicationOwners(ctx context.Context, orgID, search string, limit, offset int) ([]ApplicationOwner, error) {
	q := url.Values{"includeCurrent": {"true"}}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	if search != "" {
		q.Set("search", search)
	}
	return list[ApplicationOwner](ctx, c, withQuery("/apimanager/xapi/v1/organizations/"+esc(orgID)+"/possible-application-owners", q))
}

// AddApplicationOwner adds an owner (entityType user or team) to a client application.
func (c *Client) AddApplicationOwner(ctx context.Context, orgID, applicationID, ownerID, entityType string) error {
	p := "/apiplatform/repository/v2/organizations/" + esc(orgID) + "/applications/" + esc(applicationID) + "/owners"
	return c.JSON(ctx, http.MethodPost, p, Doc{"id": ownerID, "entityType": entityType}, nil)
}

// FetchExternal GETs an absolute URL (e.g. an Exchange file link) without
// credentials and returns its body.
func (c *Client) FetchExternal(ctx context.Context, link string) ([]byte, error) {
	resp, err := c.Do(ctx, http.MethodGet, link, nil, http.Header{"Accept": {"*/*"}})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	_, err = io.Copy(&b, resp.Body)
	return b.Bytes(), err
}
