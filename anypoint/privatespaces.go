package anypoint

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// CloudHub 2.0 private spaces: the space itself (network, firewall rules,
// ingress settings), its environment associations, scheduled upgrades,
// transit gateway attachments, VPN connections and TLS contexts. These
// follow the Terraform provider (anypoint-cli-v4 has no private-space
// commands), including the API behaviour it verified against the platform
// where the API's own description differs; those places say so.
//
// The calls need the connected app or user to have the CloudHub
// Organization Admin permission.

func privateSpacesPath(orgID string) string {
	return "/runtimefabric/api/organizations/" + esc(orgID) + "/privatespaces"
}

func privateSpacePath(orgID, spaceID string) string {
	return privateSpacesPath(orgID) + "/" + esc(spaceID)
}

// ---------------------------------------------------------------- private spaces

// PrivateSpace is a CloudHub 2.0 private space.
type PrivateSpace struct {
	rawJSON
	ID                     string                     `json:"id"`
	Name                   string                     `json:"name"`
	Version                string                     `json:"version"`
	Status                 string                     `json:"status"`
	StatusMessage          string                     `json:"statusMessage"`
	Region                 string                     `json:"region"`
	OrganizationID         string                     `json:"organizationId"`
	RootOrganizationID     string                     `json:"rootOrganizationId"`
	Provisioning           PrivateSpaceProvisioning   `json:"provisioning"`
	Environments           PrivateSpaceEnvironments   `json:"environments"`
	Network                PrivateSpaceNetwork        `json:"network"`
	FirewallRules          []PrivateSpaceFirewallRule `json:"firewallRules"`
	ManagedFirewallRules   []PrivateSpaceFirewallRule `json:"managedFirewallRules"`
	IngressConfiguration   PrivateSpaceIngressConfig  `json:"ingressConfiguration"`
	LogForwarding          PrivateSpaceLogForwarding  `json:"logForwarding"`
	EnableIAMRole          bool                       `json:"enableIAMRole"`
	EnableEgress           bool                       `json:"enableEgress"`
	EnableNetworkIsolation bool                       `json:"enableNetworkIsolation"`
	MuleAppDeploymentCount int                        `json:"muleAppDeploymentCount"`
	VPCMigrationInProgress bool                       `json:"vpcMigrationInProgress"`
	GlobalSpaceStatus      map[string]json.RawMessage `json:"globalSpaceStatus"`
}

// PrivateSpaceProvisioning is the provisioning state of a private space.
type PrivateSpaceProvisioning struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// PrivateSpaceEnvironments says which environments a private space serves.
type PrivateSpaceEnvironments struct {
	Type           string   `json:"type"`
	BusinessGroups []string `json:"businessGroups"`
}

// PrivateSpaceNetwork is a private space's network.
type PrivateSpaceNetwork struct {
	Region                   string   `json:"region,omitempty"`
	CIDRBlock                string   `json:"cidrBlock,omitempty"`
	ReservedCIDRs            []string `json:"reservedCidrs,omitempty"`
	InboundStaticIPs         []string `json:"inboundStaticIps,omitempty"`
	InboundInternalStaticIPs []string `json:"inboundInternalStaticIps,omitempty"`
	OutboundStaticIPs        []string `json:"outboundStaticIps,omitempty"`
	DNSTarget                string   `json:"dnsTarget,omitempty"`
}

// PrivateSpaceFirewallRule allows traffic into ("inbound") or out of
// ("outbound") a private space.
type PrivateSpaceFirewallRule struct {
	CIDRBlock string `json:"cidrBlock"`
	Protocol  string `json:"protocol"`
	FromPort  int    `json:"fromPort"`
	ToPort    int    `json:"toPort"`
	Type      string `json:"type"`
}

// PrivateSpaceIngressConfig is a private space's ingress settings.
type PrivateSpaceIngressConfig struct {
	ReadResponseTimeout FlexID                  `json:"readResponseTimeout,omitempty"`
	Protocol            string                  `json:"protocol,omitempty"`
	Logs                *PrivateSpaceIngressLog `json:"logs,omitempty"`
	Deployment          *struct {
		Status            string `json:"status"`
		LastSeenTimestamp int64  `json:"lastSeenTimestamp"`
	} `json:"deployment,omitempty"`
}

// PrivateSpaceIngressLog sets the ingress log levels.
type PrivateSpaceIngressLog struct {
	PortLogLevel string                  `json:"portLogLevel,omitempty"`
	Filters      []PrivateSpaceLogFilter `json:"filters"`
}

// PrivateSpaceLogFilter sets the ingress log level for one client IP.
type PrivateSpaceLogFilter struct {
	IP    string `json:"ip"`
	Level string `json:"level"`
}

// PrivateSpaceLogForwarding says where a private space's logs go.
type PrivateSpaceLogForwarding struct {
	AnypointMonitoring bool `json:"anypointMonitoring"`
}

// CreatePrivateSpaceRequest creates a private space; Region is required by
// the platform unless a network is set afterwards.
type CreatePrivateSpaceRequest struct {
	Name          string `json:"name"`
	Region        string `json:"region,omitempty"`
	EnableIAMRole *bool  `json:"enableIAMRole,omitempty"`
	EnableEgress  *bool  `json:"enableEgress,omitempty"`
}

// UpdatePrivateSpaceRequest changes a private space's name or flags.
type UpdatePrivateSpaceRequest struct {
	Name          *string `json:"name,omitempty"`
	EnableIAMRole *bool   `json:"enableIAMRole,omitempty"`
	EnableEgress  *bool   `json:"enableEgress,omitempty"`
}

// ListPrivateSpaces lists an organization's private spaces. The list comes
// wrapped in an object (unlike the space's sub-collections).
func (c *Client) ListPrivateSpaces(ctx context.Context, orgID string) ([]PrivateSpace, error) {
	return list[PrivateSpace](ctx, c, privateSpacesPath(orgID), "content", "data", "items", "privateSpaces")
}

// GetPrivateSpace reads a private space.
func (c *Client) GetPrivateSpace(ctx context.Context, orgID, spaceID string) (*PrivateSpace, error) {
	return get[PrivateSpace](ctx, c, privateSpacePath(orgID, spaceID))
}

// FindPrivateSpace finds a private space by ID or name.
func (c *Client) FindPrivateSpace(ctx context.Context, orgID, idOrName string) (*PrivateSpace, error) {
	spaces, err := c.ListPrivateSpaces(ctx, orgID)
	if err != nil {
		return nil, err
	}
	for i := range spaces {
		if spaces[i].ID == idOrName || spaces[i].Name == idOrName {
			return &spaces[i], nil
		}
	}
	return nil, notFound("private space", idOrName)
}

// CreatePrivateSpace creates a private space.
func (c *Client) CreatePrivateSpace(ctx context.Context, orgID string, req *CreatePrivateSpaceRequest) (*PrivateSpace, error) {
	return do[PrivateSpace](ctx, c, http.MethodPost, privateSpacesPath(orgID), req)
}

// UpdatePrivateSpace changes a private space's name or flags (PUT).
func (c *Client) UpdatePrivateSpace(ctx context.Context, orgID, spaceID string, req *UpdatePrivateSpaceRequest) (*PrivateSpace, error) {
	return do[PrivateSpace](ctx, c, http.MethodPut, privateSpacePath(orgID, spaceID), req)
}

// PatchPrivateSpace changes parts of a private space; the network, firewall
// rules and ingress settings are all set this way.
func (c *Client) PatchPrivateSpace(ctx context.Context, orgID, spaceID string, body any) (*PrivateSpace, error) {
	return do[PrivateSpace](ctx, c, http.MethodPatch, privateSpacePath(orgID, spaceID), body)
}

// SetPrivateSpaceNetwork sets a private space's network (region, CIDR block
// and reserved CIDRs).
func (c *Client) SetPrivateSpaceNetwork(ctx context.Context, orgID, spaceID string, n PrivateSpaceNetwork) (*PrivateSpace, error) {
	return c.PatchPrivateSpace(ctx, orgID, spaceID, map[string]any{"network": PrivateSpaceNetwork{
		Region: n.Region, CIDRBlock: n.CIDRBlock, ReservedCIDRs: n.ReservedCIDRs}})
}

// SetPrivateSpaceFirewallRules replaces a private space's firewall rules.
func (c *Client) SetPrivateSpaceFirewallRules(ctx context.Context, orgID, spaceID string, rules []PrivateSpaceFirewallRule) (*PrivateSpace, error) {
	if rules == nil {
		rules = []PrivateSpaceFirewallRule{}
	}
	return c.PatchPrivateSpace(ctx, orgID, spaceID, map[string]any{"managedFirewallRules": rules})
}

// PrivateSpaceAdvancedConfig is the private space's ingress settings and
// IAM role flag, set together.
type PrivateSpaceAdvancedConfig struct {
	IngressConfiguration PrivateSpaceIngressConfig `json:"ingressConfiguration"`
	EnableIAMRole        bool                      `json:"enableIAMRole"`
}

// DefaultPrivateSpaceAdvancedConfig is the platform's default advanced
// configuration, which the Terraform provider restores when the setting is
// removed.
func DefaultPrivateSpaceAdvancedConfig() PrivateSpaceAdvancedConfig {
	return PrivateSpaceAdvancedConfig{IngressConfiguration: PrivateSpaceIngressConfig{
		ReadResponseTimeout: "300", Protocol: "https-redirect",
		Logs: &PrivateSpaceIngressLog{PortLogLevel: "ERROR", Filters: []PrivateSpaceLogFilter{}},
	}}
}

// SetPrivateSpaceAdvancedConfig sets a private space's ingress settings and
// IAM role flag.
func (c *Client) SetPrivateSpaceAdvancedConfig(ctx context.Context, orgID, spaceID string, cfg PrivateSpaceAdvancedConfig) (*PrivateSpace, error) {
	cfg.IngressConfiguration.Deployment = nil // read-only
	if l := cfg.IngressConfiguration.Logs; l != nil && l.Filters == nil {
		l.Filters = []PrivateSpaceLogFilter{} // [] clears them; null is not a list
	}
	return c.PatchPrivateSpace(ctx, orgID, spaceID, cfg)
}

// DeletePrivateSpace deletes a private space.
func (c *Client) DeletePrivateSpace(ctx context.Context, orgID, spaceID string) error {
	return c.JSON(ctx, http.MethodDelete, privateSpacePath(orgID, spaceID), nil, nil)
}

// ---------------------------------------------------------------- associations

// PrivateSpaceAssociation links environments to a private space.
type PrivateSpaceAssociation struct {
	rawJSON
	ID             string `json:"id"`
	OrganizationID string `json:"organizationId"`
	EnvironmentID  string `json:"environmentId"`
}

// AssociationRequest names an organization and an environment ID, or
// "all", "production" or "sandbox" for a kind of environment.
type AssociationRequest struct {
	OrganizationID string `json:"organizationId"`
	Environment    string `json:"environment"`
}

// ListPrivateSpaceAssociations lists a private space's associations.
func (c *Client) ListPrivateSpaceAssociations(ctx context.Context, orgID, spaceID string) ([]PrivateSpaceAssociation, error) {
	as, err := list[PrivateSpaceAssociation](ctx, c, privateSpacePath(orgID, spaceID)+"/associations")
	if IsNotFound(err) { // a space without associations
		return nil, nil
	}
	return as, err
}

// CreatePrivateSpaceAssociations associates environments with a private space.
func (c *Client) CreatePrivateSpaceAssociations(ctx context.Context, orgID, spaceID string, reqs []AssociationRequest) ([]PrivateSpaceAssociation, error) {
	var raw json.RawMessage
	if err := c.JSON(ctx, http.MethodPost, privateSpacePath(orgID, spaceID)+"/associations",
		map[string]any{"associations": reqs}, &raw); err != nil {
		return nil, err
	}
	return decodeList[PrivateSpaceAssociation](raw, "associations")
}

// DeletePrivateSpaceAssociation removes an association; one already gone is
// not an error.
func (c *Client) DeletePrivateSpaceAssociation(ctx context.Context, orgID, spaceID, associationID string) error {
	return ignoreNotFound(c.JSON(ctx, http.MethodDelete, privateSpacePath(orgID, spaceID)+"/associations/"+esc(associationID), nil, nil))
}

func ignoreNotFound(err error) error {
	if IsNotFound(err) {
		return nil
	}
	return err
}

// ---------------------------------------------------------------- upgrades

// PrivateSpaceUpgrade is a private space's scheduled upgrade.
type PrivateSpaceUpgrade struct {
	rawJSON
	ScheduledUpdateTime string `json:"scheduledUpdateTime"`
	Status              string `json:"status"`
}

// NoUpgradeScheduled is the status GetPrivateSpaceUpgrade reports when the
// platform answers with an empty body.
const NoUpgradeScheduled = "NO_UPGRADE_SCHEDULED"

// SchedulePrivateSpaceUpgrade schedules the private space's upgrade on date
// (YYYY-MM-DD), opting in to it when optIn is set.
func (c *Client) SchedulePrivateSpaceUpgrade(ctx context.Context, orgID, spaceID, date string, optIn bool) (*PrivateSpaceUpgrade, error) {
	q := url.Values{"date": {date}, "optIn": {strconv.FormatBool(optIn)}}
	return do[PrivateSpaceUpgrade](ctx, c, http.MethodPatch, withQuery(privateSpacePath(orgID, spaceID)+"/upgrade", q), nil)
}

// GetPrivateSpaceUpgrade reads the private space's upgrade schedule.
func (c *Client) GetPrivateSpaceUpgrade(ctx context.Context, orgID, spaceID string) (*PrivateSpaceUpgrade, error) {
	u, err := get[PrivateSpaceUpgrade](ctx, c, privateSpacePath(orgID, spaceID)+"/upgradestatus")
	if err != nil {
		return nil, err
	}
	if u.Status == "" && u.ScheduledUpdateTime == "" {
		u.Status = NoUpgradeScheduled
	}
	return u, nil
}

// CancelPrivateSpaceUpgrade cancels a scheduled upgrade; none scheduled is
// not an error.
func (c *Client) CancelPrivateSpaceUpgrade(ctx context.Context, orgID, spaceID string) error {
	return ignoreNotFound(c.JSON(ctx, http.MethodDelete, privateSpacePath(orgID, spaceID)+"/upgrade", nil, nil))
}

// ---------------------------------------------------------------- transit gateways

// TransitGateway is an AWS transit gateway attached to a private space.
type TransitGateway struct {
	rawJSON
	ID   string `json:"id"`
	Name string `json:"name"`
	Spec struct {
		ResourceShare struct {
			ID      string `json:"id"`
			Account string `json:"account"`
		} `json:"resourceShare"`
		Region    string `json:"region"`
		SpaceName string `json:"spaceName,omitempty"`
	} `json:"spec"`
	Status TransitGatewayStatus `json:"status"`
}

// TransitGatewayStatus is the attachment's state and routes.
type TransitGatewayStatus struct {
	Gateway     string   `json:"gateway"`
	Attachment  string   `json:"attachment"`
	TGWResource string   `json:"tgwResource"`
	Routes      []string `json:"routes"`
}

// AWSTransitGatewayID returns the tgw-... ID. The platform reports an AWS
// console link in tgwResource, with the ID in its transitGatewayId
// parameter (in the fragment, so it is looked up as text); anything else is
// returned as it is.
func (s TransitGatewayStatus) AWSTransitGatewayID() string {
	raw := strings.TrimSpace(s.TGWResource)
	_, id, ok := strings.Cut(raw, "transitGatewayId=")
	if !ok || strings.HasPrefix(raw, "tgw-") {
		return raw
	}
	if i := strings.IndexAny(id, "&;#?/ "); i >= 0 {
		id = id[:i]
	}
	if id == "" {
		return raw
	}
	return id
}

// ErrTransitGatewayDetached is returned (wrapped) when a transit gateway is
// detached from the private space but still registered in the
// organization: the platform answers 400 "not attached to the private
// space" rather than 404.
var ErrTransitGatewayDetached = errors.New("transit gateway is detached from the private space")

func isDetached(err error) bool {
	var h *HTTPError
	if !errors.As(err, &h) || h.Status != http.StatusBadRequest {
		return false
	}
	s := strings.ToLower(string(h.Body))
	return strings.Contains(s, "not attached to") && strings.Contains(s, "private space")
}

func transitGatewaysPath(orgID, spaceID string) string {
	return privateSpacePath(orgID, spaceID) + "/transitgateways"
}

// ListTransitGateways lists a private space's transit gateways.
func (c *Client) ListTransitGateways(ctx context.Context, orgID, spaceID string) ([]TransitGateway, error) {
	return list[TransitGateway](ctx, c, transitGatewaysPath(orgID, spaceID))
}

// GetTransitGateway reads a transit gateway; one detached from the space
// fails with [ErrTransitGatewayDetached].
func (c *Client) GetTransitGateway(ctx context.Context, orgID, spaceID, tgwID string) (*TransitGateway, error) {
	t, err := get[TransitGateway](ctx, c, transitGatewaysPath(orgID, spaceID)+"/"+esc(tgwID))
	if isDetached(err) {
		return nil, errors.Join(ErrTransitGatewayDetached, err)
	}
	return t, err
}

// CreateTransitGatewayRequest attaches the transit gateway an AWS RAM
// resource share holds; at least one route is required.
type CreateTransitGatewayRequest struct {
	Name                 string   `json:"name"`
	ResourceShareID      string   `json:"resourceShareId"`
	ResourceShareAccount string   `json:"resourceShareAccount"`
	Routes               []string `json:"routes"`
}

// CreateTransitGateway attaches a transit gateway. The platform answers
// with no body, so the new attachment is found by name.
func (c *Client) CreateTransitGateway(ctx context.Context, orgID, spaceID string, req *CreateTransitGatewayRequest) (*TransitGateway, error) {
	if err := c.JSON(ctx, http.MethodPost, transitGatewaysPath(orgID, spaceID), req, nil); err != nil {
		return nil, err
	}
	tgws, err := c.ListTransitGateways(ctx, orgID, spaceID)
	if err != nil {
		return nil, err
	}
	for i := range tgws {
		if tgws[i].Name == req.Name {
			return &tgws[i], nil
		}
	}
	return nil, notFound("created transit gateway", req.Name)
}

// RenameTransitGateway renames a transit gateway. This is the
// organization-scoped PATCH the Anypoint UI uses: the private-space-scoped
// one ignores the name.
func (c *Client) RenameTransitGateway(ctx context.Context, orgID, tgwID, name string) error {
	return c.JSON(ctx, http.MethodPatch, "/runtimefabric/api/organizations/"+esc(orgID)+"/transitgateways/"+esc(tgwID),
		map[string]string{"name": name}, nil)
}

// SetTransitGatewayRoutes replaces a transit gateway's routes. The platform
// requires the name alongside them (and ignores it).
func (c *Client) SetTransitGatewayRoutes(ctx context.Context, orgID, spaceID, tgwID, name string, routes []string) error {
	if routes == nil {
		routes = []string{} // "routes": null is refused
	}
	return c.JSON(ctx, http.MethodPatch, transitGatewaysPath(orgID, spaceID)+"/"+esc(tgwID),
		map[string]any{"name": name, "routes": routes}, nil)
}

// DeleteTransitGateway detaches a transit gateway from the private space and
// then de-registers it from the organization. Detaching alone leaves it
// registered, holding the AWS resource share, and attaching the same share
// again then fails. Either step finding it already done is not an error.
func (c *Client) DeleteTransitGateway(ctx context.Context, orgID, spaceID, tgwID string) error {
	err := c.JSON(ctx, http.MethodDelete, transitGatewaysPath(orgID, spaceID)+"/"+esc(tgwID), nil, nil)
	if err != nil && !IsNotFound(err) && !isDetached(err) {
		return err
	}
	err = c.JSON(ctx, http.MethodDelete, "/runtimefabric/api/organizations/"+esc(orgID)+"/transitgateways/"+esc(tgwID), nil, nil)
	if err != nil && !IsNotFound(err) && !isDetached(err) {
		return err
	}
	return nil
}

// ---------------------------------------------------------------- VPN connections

// VPNConnection is a private space's VPN connection and its VPNs.
type VPNConnection struct {
	rawJSON
	ID   string `json:"id"`
	Name string `json:"name"`
	VPNs []struct {
		Name                string   `json:"name"`
		VPNID               string   `json:"vpnId"`
		ConnectionID        string   `json:"connectionId"`
		ConnectionName      string   `json:"connectionName"`
		VPNConnectionStatus string   `json:"vpnConnectionStatus"`
		RemoteASN           FlexID   `json:"remoteAsn"`
		LocalASN            FlexID   `json:"localAsn"`
		RemoteIPAddress     string   `json:"remoteIpAddress"`
		StaticRoutes        []string `json:"staticRoutes,omitempty"`
		VPNTunnels          []struct {
			PTPCIDR       string `json:"ptpCidr"`
			StartupAction string `json:"startupAction"`
			IsLogsEnabled bool   `json:"isLogsEnabled"`
		} `json:"vpnTunnels"`
	} `json:"vpns"`
}

// CreateVPNConnectionRequest creates a VPN connection with its VPNs.
type CreateVPNConnectionRequest struct {
	Name string       `json:"name"`
	VPNs []VPNRequest `json:"vpns"`
}

// VPNRequest is one VPN of a connection; without static routes it uses BGP.
type VPNRequest struct {
	Name            string             `json:"name,omitempty"`
	LocalASN        string             `json:"localAsn"`
	RemoteASN       string             `json:"remoteAsn,omitempty"`
	RemoteIPAddress string             `json:"remoteIpAddress"`
	StaticRoutes    []string           `json:"staticRoutes,omitempty"`
	VPNTunnels      []VPNTunnelRequest `json:"vpnTunnels"`
}

// VPNTunnelRequest is one tunnel of a VPN.
type VPNTunnelRequest struct {
	PSK           string `json:"psk"`
	PTPCIDR       string `json:"ptpCidr"`
	StartupAction string `json:"startupAction"`
}

func vpnPath(orgID, spaceID string) string { return privateSpacePath(orgID, spaceID) + "/connections" }

// CreateVPNConnection creates a VPN connection.
func (c *Client) CreateVPNConnection(ctx context.Context, orgID, spaceID string, req *CreateVPNConnectionRequest) (*VPNConnection, error) {
	return do[VPNConnection](ctx, c, http.MethodPost, vpnPath(orgID, spaceID), req)
}

// GetVPNConnection reads a VPN connection.
func (c *Client) GetVPNConnection(ctx context.Context, orgID, spaceID, connectionID string) (*VPNConnection, error) {
	return get[VPNConnection](ctx, c, vpnPath(orgID, spaceID)+"/"+esc(connectionID))
}

// DeleteVPNConnection deletes a VPN connection; one already gone is not an error.
func (c *Client) DeleteVPNConnection(ctx context.Context, orgID, spaceID, connectionID string) error {
	return ignoreNotFound(c.JSON(ctx, http.MethodDelete, vpnPath(orgID, spaceID)+"/"+esc(connectionID), nil, nil))
}

// DeleteVPN deletes one VPN of a connection.
func (c *Client) DeleteVPN(ctx context.Context, orgID, spaceID, connectionID, vpnID string) error {
	return c.JSON(ctx, http.MethodDelete, vpnPath(orgID, spaceID)+"/"+esc(connectionID)+"/vpns/"+esc(vpnID), nil, nil)
}

// ---------------------------------------------------------------- TLS contexts

// PrivateSpaceTLSContext is a TLS context of a private space's ingress.
type PrivateSpaceTLSContext struct {
	rawJSON
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	KeyStore *struct {
		FileName       string   `json:"fileName"`
		Type           string   `json:"type"`
		CN             string   `json:"cn"`
		SAN            []string `json:"san"`
		ExpirationDate string   `json:"expirationDate"`
	} `json:"keyStore,omitempty"`
	TrustStore *struct {
		FileName       string `json:"fileName"`
		Type           string `json:"type"`
		ExpirationDate string `json:"expirationDate"`
	} `json:"trustStore,omitempty"`
	Ciphers map[string]bool `json:"ciphers"`
}

// PrivateSpaceKeyStore is a TLS context's key store: PEM (Certificate and Key) or
// JKS (KeystoreBase64, StorePassphrase and Alias).
type PrivateSpaceKeyStore struct {
	Source              string `json:"source"`
	Certificate         string `json:"certificate,omitempty"`
	Key                 string `json:"key,omitempty"`
	CertificateFileName string `json:"certificateFileName,omitempty"`
	KeyFileName         string `json:"keyFileName,omitempty"`
	KeystoreBase64      string `json:"keystoreBase64,omitempty"`
	StorePassphrase     string `json:"storePassphrase,omitempty"`
	Alias               string `json:"alias,omitempty"`
	KeystoreFileName    string `json:"keystoreFileName,omitempty"`
	KeyPassphrase       string `json:"keyPassphrase,omitempty"`
}

// PrivateSpaceTLSRequest creates or replaces a private space TLS context. Ciphers
// maps cipher names (aes128GcmSha256, tlsAes256GcmSha384, ...) to whether
// they are enabled.
type PrivateSpaceTLSRequest struct {
	Name      string `json:"name,omitempty"`
	TLSConfig *struct {
		KeyStore PrivateSpaceKeyStore `json:"keyStore"`
	} `json:"tlsConfig,omitempty"`
	Ciphers map[string]bool `json:"ciphers,omitempty"`
}

// WithKeyStore sets the request's key store.
func (r *PrivateSpaceTLSRequest) WithKeyStore(ks PrivateSpaceKeyStore) *PrivateSpaceTLSRequest {
	r.TLSConfig = &struct {
		KeyStore PrivateSpaceKeyStore `json:"keyStore"`
	}{ks}
	return r
}

func tlsContextsPath(orgID, spaceID string) string {
	return privateSpacePath(orgID, spaceID) + "/tlsContexts"
}

// ListPrivateSpaceTLSContexts lists a private space's TLS contexts.
func (c *Client) ListPrivateSpaceTLSContexts(ctx context.Context, orgID, spaceID string) ([]PrivateSpaceTLSContext, error) {
	return list[PrivateSpaceTLSContext](ctx, c, tlsContextsPath(orgID, spaceID))
}

// FindPrivateSpaceTLSContext finds a TLS context by ID or name; the API has
// no single-context GET.
func (c *Client) FindPrivateSpaceTLSContext(ctx context.Context, orgID, spaceID, idOrName string) (*PrivateSpaceTLSContext, error) {
	all, err := c.ListPrivateSpaceTLSContexts(ctx, orgID, spaceID)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == idOrName || all[i].Name == idOrName {
			return &all[i], nil
		}
	}
	return nil, notFound("TLS context", idOrName)
}

// CreatePrivateSpaceTLSContext creates a TLS context. The platform answers
// with no body, so the new context is found by name.
func (c *Client) CreatePrivateSpaceTLSContext(ctx context.Context, orgID, spaceID string, req *PrivateSpaceTLSRequest) (*PrivateSpaceTLSContext, error) {
	if err := c.JSON(ctx, http.MethodPost, tlsContextsPath(orgID, spaceID), req, nil); err != nil {
		return nil, err
	}
	return c.FindPrivateSpaceTLSContext(ctx, orgID, spaceID, req.Name)
}

// UpdatePrivateSpaceTLSContext replaces a TLS context's name, key store or
// ciphers (PUT).
func (c *Client) UpdatePrivateSpaceTLSContext(ctx context.Context, orgID, spaceID, tlsID string, req *PrivateSpaceTLSRequest) (*PrivateSpaceTLSContext, error) {
	return do[PrivateSpaceTLSContext](ctx, c, http.MethodPut, tlsContextsPath(orgID, spaceID)+"/"+esc(tlsID), req)
}

// DeletePrivateSpaceTLSContext deletes a TLS context; one already gone is
// not an error.
func (c *Client) DeletePrivateSpaceTLSContext(ctx context.Context, orgID, spaceID, tlsID string) error {
	return ignoreNotFound(c.JSON(ctx, http.MethodDelete, tlsContextsPath(orgID, spaceID)+"/"+esc(tlsID), nil, nil))
}
