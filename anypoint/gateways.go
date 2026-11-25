package anypoint

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Flex Gateways in Gateway Manager. Managed gateways follow
// anypoint-cli-v4's runtime-mgr plugin; self-managed (connected mode)
// gateways and registration tokens follow the Terraform provider.

func gatewaysPath(api, orgID, envID string) string {
	return "/gatewaymanager/" + api + "/v1/organizations/" + esc(orgID) + "/environments/" + esc(envID) + "/gateways"
}

// Gateway is a Flex Gateway as listed and described by Gateway Manager.
type Gateway struct {
	rawJSON
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	TargetID       string               `json:"targetId,omitempty"`
	TargetName     string               `json:"targetName,omitempty"`
	TargetType     string               `json:"targetType,omitempty"`
	Status         string               `json:"status,omitempty"`
	DesiredStatus  string               `json:"desiredStatus,omitempty"`
	DateCreated    string               `json:"dateCreated,omitempty"`
	LastUpdated    string               `json:"lastUpdated,omitempty"`
	RuntimeVersion string               `json:"runtimeVersion,omitempty"`
	ReleaseChannel string               `json:"releaseChannel,omitempty"`
	Size           string               `json:"size,omitempty"`
	Configuration  GatewayConfiguration `json:"configuration"`
	Running        bool                 `json:"running,omitempty"`
	Ready          bool                 `json:"ready,omitempty"`
}

// GatewayConfiguration is a managed gateway's configuration.
type GatewayConfiguration struct {
	Ingress struct {
		PublicURL         string `json:"publicUrl"`
		InternalURL       string `json:"internalUrl,omitempty"`
		LastMileSecurity  bool   `json:"lastMileSecurity"`
		ForwardSSLSession bool   `json:"forwardSslSession"`
	} `json:"ingress"`
	Properties struct {
		UpstreamResponseTimeout int `json:"upstreamResponseTimeout"`
		ConnectionIdleTimeout   int `json:"connectionIdleTimeout"`
	} `json:"properties"`
	Logging struct {
		Level       string `json:"level"`
		ForwardLogs bool   `json:"forwardLogs"`
	} `json:"logging"`
	Tracing *struct {
		Enabled  bool  `json:"enabled"`
		Sampling *int  `json:"sampling,omitempty"`
		Labels   []any `json:"labels,omitempty"`
	} `json:"tracing,omitempty"`
}

// ListGateways lists the gateways of a kind ("managed" or "selfManaged").
func (c *Client) ListGateways(ctx context.Context, orgID, envID, kind string) ([]Gateway, error) {
	return list[Gateway](ctx, c, withQuery(gatewaysPath("xapi", orgID, envID), url.Values{"kind": {kind}}), "content")
}

// GetManagedGateway returns a managed gateway (with its status).
func (c *Client) GetManagedGateway(ctx context.Context, orgID, envID, gatewayID string) (*Gateway, error) {
	return get[Gateway](ctx, c, gatewaysPath("xapi", orgID, envID)+"/"+esc(gatewayID))
}

// CreateManagedGateway creates a managed gateway from its document (name,
// targetId, runtimeVersion, releaseChannel, size, configuration).
func (c *Client) CreateManagedGateway(ctx context.Context, orgID, envID string, body any) (*Gateway, error) {
	return do[Gateway](ctx, c, http.MethodPost, gatewaysPath("api", orgID, envID), body)
}

// UpdateManagedGateway replaces a managed gateway's document.
func (c *Client) UpdateManagedGateway(ctx context.Context, orgID, envID, gatewayID string, body any) (*Gateway, error) {
	return do[Gateway](ctx, c, http.MethodPut, gatewaysPath("api", orgID, envID)+"/"+esc(gatewayID), body)
}

// DeleteManagedGateway deletes a managed gateway (it must have no APIs).
func (c *Client) DeleteManagedGateway(ctx context.Context, orgID, envID, gatewayID string) error {
	return c.JSON(ctx, http.MethodDelete, gatewaysPath("api", orgID, envID)+"/"+esc(gatewayID), nil, nil)
}

// SetManagedGatewayStatus starts (STARTED) or stops (STOPPED) a managed gateway.
func (c *Client) SetManagedGatewayStatus(ctx context.Context, orgID, envID, gatewayID, status string) error {
	return c.JSON(ctx, http.MethodPatch, gatewaysPath("api", orgID, envID)+"/"+esc(gatewayID)+"/desiredstatus", Doc{"desiredStatus": status}, nil)
}

// ListAllGateways lists every gateway of an environment (all kinds), page
// by page.
func (c *Client) ListAllGateways(ctx context.Context, orgID, envID string) ([]Gateway, error) {
	var all []Gateway
	for page := range 100 {
		var resp struct {
			Content       []json.RawMessage `json:"content"`
			TotalElements *int              `json:"totalElements"`
		}
		q := url.Values{"pageNumber": {strconv.Itoa(page)}, "pageSize": {"20"}}
		if err := c.JSON(ctx, http.MethodGet, withQuery(gatewaysPath("api", orgID, envID), q), nil, &resp); err != nil {
			return nil, err
		}
		for _, raw := range resp.Content {
			var g Gateway
			if err := decode(raw, &g); err != nil {
				return nil, err
			}
			all = append(all, g)
		}
		if resp.TotalElements == nil || len(resp.Content) == 0 || len(all) >= *resp.TotalElements {
			break
		}
	}
	return all, nil
}

// GetGatewayDocument returns a managed gateway's stored document (name,
// targetId, releaseChannel, runtimeVersion, size, configuration).
func (c *Client) GetGatewayDocument(ctx context.Context, orgID, envID, gatewayID string) (*Gateway, error) {
	return get[Gateway](ctx, c, gatewaysPath("api", orgID, envID)+"/"+esc(gatewayID))
}

// TargetDomains are the domains a CloudHub 2.0 space or Runtime Fabric
// serves ("*.example.com"), with the suffix that makes app hosts unique.
type TargetDomains struct {
	Domains     []string `json:"domains"`
	AppUniqueID string   `json:"appUniqueId"`
}

// GetTargetDomains returns the domains of a deployment target in an environment.
func (c *Client) GetTargetDomains(ctx context.Context, orgID, envID, targetID string) (*TargetDomains, error) {
	return get[TargetDomains](ctx, c, "/runtimefabric/api/organizations/"+esc(orgID)+"/targets/"+esc(targetID)+
		"/environments/"+esc(envID)+"/domains?sendAppUniqueId=true")
}

// GatewayAPI is an API instance running on a gateway.
type GatewayAPI struct {
	rawJSON
	APIName    string `json:"apiName"`
	InstanceID FlexID `json:"instanceId"`
	DateAdded  string `json:"dateAdded"`
}

// ListManagedGatewayAPIs lists the API instances on a managed gateway.
func (c *Client) ListManagedGatewayAPIs(ctx context.Context, orgID, envID, gatewayID string) ([]GatewayAPI, error) {
	return list[GatewayAPI](ctx, c, gatewaysPath("xapi", orgID, envID)+"/"+esc(gatewayID)+"/apis", "content")
}

// GatewayTarget is a CloudHub 2.0 space managed gateways can run in.
type GatewayTarget struct {
	rawJSON
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

// ListGatewayTargets lists the targets for managed gateways.
func (c *Client) ListGatewayTargets(ctx context.Context, orgID, envID string) ([]GatewayTarget, error) {
	return list[GatewayTarget](ctx, c, gatewaysPath("xapi", orgID, envID)+"/targets", "content")
}

// GatewayVersions lists the Flex Gateway runtime versions per release channel.
type GatewayVersions struct {
	rawJSON
	Channels map[string]struct {
		Versions []struct {
			DisplayName string `json:"displayName"`
		} `json:"versions"`
	} `json:"channels"`
}

// GetGatewayVersions returns the available runtime versions.
func (c *Client) GetGatewayVersions(ctx context.Context) (*GatewayVersions, error) {
	return get[GatewayVersions](ctx, c, "/gatewaymanager/xapi/v1/gateway/versions")
}

// Versions returns a channel's version names.
func (v *GatewayVersions) Versions(channel string) []string {
	var out []string
	for _, x := range v.Channels[channel].Versions {
		out = append(out, x.DisplayName)
	}
	return out
}

// LatestVersion returns the highest version of a channel ("" if none).
func (v *GatewayVersions) LatestVersion(channel string) string {
	best := ""
	for _, name := range v.Versions(channel) {
		if best == "" || CompareVersions(name, best) > 0 {
			best = name
		}
	}
	return best
}

// CompareVersions compares two semantic versions ("1.2.3", "1.2.3-rc.1"):
// -1, 0 or 1. A release sorts after its pre-releases.
func CompareVersions(a, b string) int {
	a, b = strings.TrimPrefix(a, "v"), strings.TrimPrefix(b, "v")
	ac, apre, _ := strings.Cut(strings.SplitN(a, "+", 2)[0], "-")
	bc, bpre, _ := strings.Cut(strings.SplitN(b, "+", 2)[0], "-")
	if c := compareDotted(ac, bc, true); c != 0 {
		return c
	}
	switch {
	case apre == bpre:
		return 0
	case apre == "":
		return 1
	case bpre == "":
		return -1
	}
	return compareDotted(apre, bpre, false)
}

func compareDotted(a, b string, numericOnly bool) int {
	ap, bp := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(ap) || i < len(bp); i++ {
		if i >= len(ap) {
			return -1
		}
		if i >= len(bp) {
			return 1
		}
		an, aerr := strconv.Atoi(ap[i])
		bn, berr := strconv.Atoi(bp[i])
		switch {
		case aerr == nil && berr == nil:
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
		case numericOnly || (aerr == nil) != (berr == nil):
			if aerr == nil { // numbers sort before identifiers
				return -1
			}
			if berr == nil {
				return 1
			}
			fallthrough
		default:
			if c := strings.Compare(ap[i], bp[i]); c != 0 {
				return c
			}
		}
	}
	return 0
}

// ---------------------------------------------------------------- self-managed

const standaloneBase = "/standalone/api/v1"

func selfManagedPath(orgID, envID string) string {
	return standaloneBase + "/organizations/" + esc(orgID) + "/environments/" + esc(envID)
}

// SelfManagedGateway is a Flex Gateway running in connected mode.
type SelfManagedGateway struct {
	rawJSON
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status,omitempty"`
}

// ListSelfManagedGateways lists the connected-mode gateways of an environment.
func (c *Client) ListSelfManagedGateways(ctx context.Context, orgID, envID string) ([]SelfManagedGateway, error) {
	var all []SelfManagedGateway
	for page := 0; ; page++ {
		q := url.Values{"pageSize": {"100"}, "pageNumber": {strconv.Itoa(page)}}
		gs, err := list[SelfManagedGateway](ctx, c, withQuery(selfManagedPath(orgID, envID)+"/gateways", q), "content")
		if err != nil {
			return nil, err
		}
		all = append(all, gs...)
		if len(gs) < 100 {
			return all, nil
		}
	}
}

// GetSelfManagedGateway returns a connected-mode gateway.
func (c *Client) GetSelfManagedGateway(ctx context.Context, orgID, envID, gatewayID string) (*SelfManagedGateway, error) {
	return get[SelfManagedGateway](ctx, c, selfManagedPath(orgID, envID)+"/gateways/"+esc(gatewayID))
}

// DeleteSelfManagedGateway deletes a connected-mode gateway.
func (c *Client) DeleteSelfManagedGateway(ctx context.Context, orgID, envID, gatewayID string) error {
	return c.JSON(ctx, http.MethodDelete, selfManagedPath(orgID, envID)+"/gateways/"+esc(gatewayID), nil, nil)
}

// ListSelfManagedGatewayReplicas lists the replicas of a connected-mode gateway.
func (c *Client) ListSelfManagedGatewayReplicas(ctx context.Context, orgID, envID, gatewayID string) ([]Doc, error) {
	return list[Doc](ctx, c, withQuery(selfManagedPath(orgID, envID)+"/gateways/"+esc(gatewayID)+"/replicas",
		url.Values{"pageSize": {"100"}, "pageNumber": {"0"}}), "content")
}

// MintGatewayRegistrationToken creates a token that registers a Flex
// Gateway in connected mode. Treat it as a secret.
func (c *Client) MintGatewayRegistrationToken(ctx context.Context, orgID, envID string) (string, error) {
	var out struct {
		RegistrationToken string `json:"registrationToken"`
	}
	if err := c.JSON(ctx, http.MethodPost, selfManagedPath(orgID, envID)+"/gatewaytokens", []byte{}, &out); err != nil {
		return "", err
	}
	return out.RegistrationToken, nil
}
