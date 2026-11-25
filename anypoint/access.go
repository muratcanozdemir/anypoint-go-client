package anypoint

import (
	"context"
	"encoding/json"
	"net/http"
)

// Access management: organizations (business groups), environments, users,
// connected apps, roles and teams. Types and endpoints follow the Terraform
// provider's internal/client/accessmanagement.

// ---------------------------------------------------------------- organizations

// Organization is an Anypoint organization or business group.
type Organization struct {
	rawJSON
	ID                    string         `json:"id"`
	Name                  string         `json:"name"`
	CreatedAt             string         `json:"createdAt,omitempty"`
	UpdatedAt             string         `json:"updatedAt,omitempty"`
	OwnerID               string         `json:"ownerId,omitempty"`
	ClientID              string         `json:"clientId,omitempty"`
	IDProviderID          string         `json:"idprovider_id,omitempty"`
	IsFederated           bool           `json:"isFederated,omitempty"`
	ParentOrganizationIDs []string       `json:"parentOrganizationIds,omitempty"`
	SubOrganizationIDs    []string       `json:"subOrganizationIds,omitempty"`
	TenantOrganizationIDs []string       `json:"tenantOrganizationIds,omitempty"`
	MfaRequired           string         `json:"mfaRequired,omitempty"`
	OrgType               string         `json:"orgType,omitempty"`
	Domain                *string        `json:"domain,omitempty"`
	IsRoot                bool           `json:"isRoot"`
	IsMaster              bool           `json:"isMaster,omitempty"`
	Properties            map[string]any `json:"properties,omitempty"`
	Entitlements          map[string]any `json:"entitlements,omitempty"`
	Subscription          *Subscription  `json:"subscription,omitempty"`
	Environments          []Environment  `json:"environments,omitempty"`
	Owner                 *User          `json:"owner,omitempty"`
	SessionTimeout        int            `json:"sessionTimeout,omitempty"`
	SubOrganizations      []Organization `json:"subOrganizations,omitempty"` // in /hierarchy only
	ParentID              string         `json:"parentId,omitempty"`         // in /hierarchy and memberOfOrganizations
}

// Subscription is an organization's subscription.
type Subscription struct {
	Category   string `json:"category"`
	Type       string `json:"type"`
	Expiration string `json:"expiration"`
}

// CreateOrganizationRequest creates a business group under ParentOrganizationID.
type CreateOrganizationRequest struct {
	Name                 string         `json:"name"`
	ParentOrganizationID string         `json:"parentOrganizationId"`
	OwnerID              string         `json:"ownerId"`
	Entitlements         map[string]any `json:"entitlements"`
}

// UpdateOrganizationRequest replaces an organization's settings. Start from
// the current Entitlements: the API replaces the whole object.
type UpdateOrganizationRequest struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	OwnerID      string         `json:"ownerId"`
	Properties   map[string]any `json:"properties,omitempty"`
	Entitlements map[string]any `json:"entitlements"`
}

func orgPath(orgID string) string { return "/accounts/api/organizations/" + esc(orgID) }

// GetOrganization returns an organization or business group.
func (c *Client) GetOrganization(ctx context.Context, orgID string) (*Organization, error) {
	return get[Organization](ctx, c, orgPath(orgID))
}

// GetOrganizationHierarchy returns the organization with its business groups nested in SubOrganizations.
func (c *Client) GetOrganizationHierarchy(ctx context.Context, orgID string) (*Organization, error) {
	return get[Organization](ctx, c, orgPath(orgID)+"/hierarchy")
}

// CreateOrganization creates a business group.
func (c *Client) CreateOrganization(ctx context.Context, req *CreateOrganizationRequest) (*Organization, error) {
	return do[Organization](ctx, c, http.MethodPost, "/accounts/api/organizations", req)
}

// UpdateOrganization replaces an organization's name, owner, properties and entitlements.
func (c *Client) UpdateOrganization(ctx context.Context, orgID string, req *UpdateOrganizationRequest) (*Organization, error) {
	return do[Organization](ctx, c, http.MethodPut, orgPath(orgID), req)
}

// DeleteOrganization deletes a business group.
func (c *Client) DeleteOrganization(ctx context.Context, orgID string) error {
	return c.JSON(ctx, http.MethodDelete, orgPath(orgID), map[string]string{"id": orgID}, nil)
}

// ---------------------------------------------------------------- environments

// Environment is an Anypoint environment.
type Environment struct {
	rawJSON
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Type           string  `json:"type"`
	IsProduction   bool    `json:"isProduction"`
	OrganizationID string  `json:"organizationId,omitempty"`
	ClientID       string  `json:"clientId,omitempty"`
	ArcNamespace   *string `json:"arcNamespace,omitempty"`
}

// CreateEnvironmentRequest creates an environment. Type is design, sandbox or production.
type CreateEnvironmentRequest struct {
	Name           string `json:"name"`
	Type           string `json:"type,omitempty"`
	IsProduction   bool   `json:"isProduction,omitempty"`
	OrganizationID string `json:"organizationId,omitempty"` // sent by anypoint-cli-v4
}

// UpdateEnvironmentRequest changes the fields that are set.
type UpdateEnvironmentRequest struct {
	Name         *string `json:"name,omitempty"`
	Type         *string `json:"type,omitempty"`
	IsProduction *bool   `json:"isProduction,omitempty"`
}

func envPath(orgID, envID string) string { return orgPath(orgID) + "/environments/" + esc(envID) }

// ListEnvironments lists the environments of an organization.
func (c *Client) ListEnvironments(ctx context.Context, orgID string) ([]Environment, error) {
	return list[Environment](ctx, c, orgPath(orgID)+"/environments", "data")
}

// GetEnvironment returns an environment by ID.
func (c *Client) GetEnvironment(ctx context.Context, orgID, envID string) (*Environment, error) {
	return get[Environment](ctx, c, envPath(orgID, envID))
}

// CreateEnvironment creates an environment.
func (c *Client) CreateEnvironment(ctx context.Context, orgID string, req *CreateEnvironmentRequest) (*Environment, error) {
	return do[Environment](ctx, c, http.MethodPost, orgPath(orgID)+"/environments", req)
}

// UpdateEnvironment changes an environment.
func (c *Client) UpdateEnvironment(ctx context.Context, orgID, envID string, req *UpdateEnvironmentRequest) (*Environment, error) {
	return do[Environment](ctx, c, http.MethodPut, envPath(orgID, envID), req)
}

// DeleteEnvironment deletes an environment.
func (c *Client) DeleteEnvironment(ctx context.Context, orgID, envID string) error {
	return c.JSON(ctx, http.MethodDelete, envPath(orgID, envID), nil, nil)
}

// ---------------------------------------------------------------- users

// User is an Anypoint user.
type User struct {
	rawJSON
	ID                    string         `json:"id"`
	Username              string         `json:"username"`
	FirstName             string         `json:"firstName"`
	LastName              string         `json:"lastName"`
	Email                 string         `json:"email"`
	OrganizationID        string         `json:"organizationId,omitempty"`
	Enabled               bool           `json:"enabled"`
	Type                  string         `json:"type,omitempty"`
	CreatedAt             string         `json:"createdAt,omitempty"`
	LastLogin             string         `json:"lastLogin,omitempty"`
	Organization          *Organization  `json:"organization,omitempty"`
	MemberOfOrganizations []Organization `json:"memberOfOrganizations,omitempty"`
	// OrganizationPreferences maps an organization ID to preferences such as defaultEnvironment.
	OrganizationPreferences map[string]map[string]any `json:"organizationPreferences,omitempty"`
}

// CurrentUser returns the authenticated user (/accounts/api/users/me). It
// fails for connected apps acting on their own behalf; use [Client.Me] there.
func (c *Client) CurrentUser(ctx context.Context) (*User, error) {
	return get[User](ctx, c, "/accounts/api/users/me")
}

// ListUsers lists the users of an organization.
func (c *Client) ListUsers(ctx context.Context, orgID string) ([]User, error) {
	return paged[User](ctx, c, orgPath(orgID)+"/users", nil, "data")
}

// ---------------------------------------------------------------- connected apps

// ConnectedApp is a connected application.
type ConnectedApp struct {
	rawJSON
	ClientID                     string   `json:"client_id"`
	ClientSecret                 string   `json:"client_secret,omitempty"`
	ClientName                   string   `json:"client_name"`
	OrgID                        string   `json:"org_id,omitempty"`
	OwnerOrgID                   string   `json:"owner_org_id,omitempty"`
	OwnerUserID                  string   `json:"owner_user_id,omitempty"`
	GrantTypes                   []string `json:"grant_types"`
	RedirectURIs                 []string `json:"redirect_uris"`
	PublicKeys                   []string `json:"public_keys"`
	Scopes                       []string `json:"scopes"`
	Audience                     string   `json:"audience,omitempty"`
	ClientURI                    string   `json:"client_uri,omitempty"`
	Enabled                      bool     `json:"enabled"`
	PolicyURI                    *string  `json:"policy_uri,omitempty"`
	TosURI                       *string  `json:"tos_uri,omitempty"`
	CertExpiry                   *string  `json:"cert_expiry,omitempty"`
	GenerateIssClaimWithoutToken bool     `json:"generate_iss_claim_without_token,omitempty"`
	IDProviderID                 *string  `json:"idprovider_id,omitempty"`
	IsDynamic                    bool     `json:"is_dynamic,omitempty"`
	IPAllowlistExcluded          bool     `json:"ip_allowlist_excluded,omitempty"`
	PKCEEnforcement              string   `json:"pkce_enforcement,omitempty"`
	SkipUserConsent              bool     `json:"skip_user_consent,omitempty"`
	CreatedAt                    string   `json:"created_at,omitempty"`
	UpdatedAt                    string   `json:"updated_at,omitempty"`
	ClientSecretUpdatedAt        string   `json:"client_secret_updated_at,omitempty"`
}

// CreateConnectedAppRequest creates a connected application.
type CreateConnectedAppRequest struct {
	ClientName   string   `json:"client_name"`
	GrantTypes   []string `json:"grant_types"`
	RedirectURIs []string `json:"redirect_uris"`
	PublicKeys   []string `json:"public_keys"`
	Scopes       []string `json:"scopes,omitempty"`
	Audience     string   `json:"audience,omitempty"`
	ClientURI    string   `json:"client_uri,omitempty"`
}

// UpdateConnectedAppRequest changes the fields that are set.
type UpdateConnectedAppRequest struct {
	ClientName   *string  `json:"client_name,omitempty"`
	GrantTypes   []string `json:"grant_types,omitempty"`
	RedirectURIs []string `json:"redirect_uris,omitempty"`
	PublicKeys   []string `json:"public_keys,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
	Audience     *string  `json:"audience,omitempty"`
	ClientURI    *string  `json:"client_uri,omitempty"`
	Enabled      *bool    `json:"enabled,omitempty"`
}

// Scope is a connected app scope, optionally bound to an org and environments.
type Scope struct {
	Scope         string         `json:"scope"`
	ContextParams map[string]any `json:"context_params,omitempty"`
}

// ScopeCatalogEntry describes a scope that can be granted.
type ScopeCatalogEntry struct {
	rawJSON
	Scope        string `json:"scope"`
	DisplayName  string `json:"displayName"`
	Description  string `json:"description"`
	ProductLabel string `json:"productLabel"`
	Internal     bool   `json:"internal"`
}

func connectedAppPath(orgID, clientID string) string {
	return orgPath(orgID) + "/connectedApplications/" + esc(clientID)
}

// ListConnectedApps lists the connected applications of an organization.
func (c *Client) ListConnectedApps(ctx context.Context, orgID string) ([]ConnectedApp, error) {
	return paged[ConnectedApp](ctx, c, orgPath(orgID)+"/connectedApplications", map[string][]string{"includeUsage": {"true"}}, "data")
}

// GetConnectedApp returns a connected application.
func (c *Client) GetConnectedApp(ctx context.Context, orgID, clientID string) (*ConnectedApp, error) {
	return get[ConnectedApp](ctx, c, connectedAppPath(orgID, clientID))
}

// CreateConnectedApp creates a connected application; the response carries its secret.
func (c *Client) CreateConnectedApp(ctx context.Context, orgID string, req *CreateConnectedAppRequest) (*ConnectedApp, error) {
	return do[ConnectedApp](ctx, c, http.MethodPost, orgPath(orgID)+"/connectedApplications", req)
}

// UpdateConnectedApp changes a connected application.
func (c *Client) UpdateConnectedApp(ctx context.Context, orgID, clientID string, req *UpdateConnectedAppRequest) (*ConnectedApp, error) {
	return do[ConnectedApp](ctx, c, http.MethodPatch, connectedAppPath(orgID, clientID), req)
}

// DeleteConnectedApp deletes a connected application.
func (c *Client) DeleteConnectedApp(ctx context.Context, orgID, clientID string) error {
	return c.JSON(ctx, http.MethodDelete, connectedAppPath(orgID, clientID), nil, nil)
}

// ListConnectedAppScopes lists the scopes granted to a connected application.
func (c *Client) ListConnectedAppScopes(ctx context.Context, clientID string) ([]Scope, error) {
	return paged[Scope](ctx, c, "/accounts/api/connectedApplications/"+esc(clientID)+"/scopes", nil, "data")
}

// ReplaceConnectedAppScopes replaces all scopes of a connected application.
func (c *Client) ReplaceConnectedAppScopes(ctx context.Context, clientID string, scopes []Scope) error {
	return c.JSON(ctx, http.MethodPut, "/accounts/api/connectedApplications/"+esc(clientID)+"/scopes",
		map[string]any{"scopes": scopes}, nil)
}

// AddConnectedAppScopes adds scopes to a connected application.
func (c *Client) AddConnectedAppScopes(ctx context.Context, clientID string, scopes []Scope) error {
	return c.JSON(ctx, http.MethodPatch, "/accounts/api/connectedApplications/"+esc(clientID)+"/scopes",
		map[string]any{"scopes": scopes}, nil)
}

// RemoveConnectedAppScopes removes scopes from a connected application.
func (c *Client) RemoveConnectedAppScopes(ctx context.Context, orgID, clientID string, scopes []Scope) error {
	return c.JSON(ctx, http.MethodDelete, connectedAppPath(orgID, clientID)+"/scopes",
		map[string]any{"scopes": scopes}, nil)
}

// ListScopesCatalog lists the scopes that can be granted to connected apps.
func (c *Client) ListScopesCatalog(ctx context.Context) ([]ScopeCatalogEntry, error) {
	return list[ScopeCatalogEntry](ctx, c, "/accounts/api/cs/scopes", "data")
}

// ---------------------------------------------------------------- roles

// RoleGroup is a custom role (role group) of an organization.
type RoleGroup struct {
	rawJSON
	ID            string   `json:"role_group_id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	OrgID         string   `json:"org_id"`
	Editable      bool     `json:"editable"`
	ExternalNames []string `json:"external_names"`
	CreatedAt     string   `json:"created_at,omitempty"`
	UpdatedAt     string   `json:"updated_at,omitempty"`
}

// RoleGroupRequest creates or replaces a role group.
type RoleGroupRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Role is a permission set that can be assigned to role groups and teams.
type Role struct {
	rawJSON
	RoleID      string `json:"role_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Internal    bool   `json:"internal"`
}

// RoleAssignment is a role assigned to a role group or a team, in a context
// (org_id, envId, ...).
type RoleAssignment struct {
	rawJSON
	RoleGroupAssignmentID string            `json:"role_group_assignment_id,omitempty"`
	RoleGroupID           string            `json:"role_group_id,omitempty"`
	RoleID                string            `json:"role_id"`
	OrgID                 string            `json:"org_id,omitempty"`
	Name                  string            `json:"name,omitempty"`
	Description           string            `json:"description,omitempty"`
	Internal              bool              `json:"internal,omitempty"`
	ContextParams         map[string]string `json:"context_params"`
	CreatedAt             string            `json:"created_at,omitempty"`
}

// AssignRoleRequest assigns a role in a context, e.g. {"org": orgID, "envId": envID}.
type AssignRoleRequest struct {
	RoleID        string            `json:"role_id"`
	ContextParams map[string]string `json:"context_params"`
}

func roleGroupPath(orgID, id string) string { return orgPath(orgID) + "/rolegroups/" + esc(id) }

// ListRoles lists the roles that can be assigned.
func (c *Client) ListRoles(ctx context.Context) ([]Role, error) {
	return paged[Role](ctx, c, "/accounts/api/roles", nil, "data")
}

// ListRoleGroups lists the role groups of an organization.
func (c *Client) ListRoleGroups(ctx context.Context, orgID string) ([]RoleGroup, error) {
	return paged[RoleGroup](ctx, c, orgPath(orgID)+"/rolegroups", nil, "data")
}

// GetRoleGroup returns a role group.
func (c *Client) GetRoleGroup(ctx context.Context, orgID, id string) (*RoleGroup, error) {
	return get[RoleGroup](ctx, c, roleGroupPath(orgID, id))
}

// CreateRoleGroup creates a role group.
func (c *Client) CreateRoleGroup(ctx context.Context, orgID string, req *RoleGroupRequest) (*RoleGroup, error) {
	return do[RoleGroup](ctx, c, http.MethodPost, orgPath(orgID)+"/rolegroups", req)
}

// UpdateRoleGroup replaces a role group's name and description.
func (c *Client) UpdateRoleGroup(ctx context.Context, orgID, id string, req *RoleGroupRequest) (*RoleGroup, error) {
	return do[RoleGroup](ctx, c, http.MethodPut, roleGroupPath(orgID, id), req)
}

// DeleteRoleGroup deletes a role group.
func (c *Client) DeleteRoleGroup(ctx context.Context, orgID, id string) error {
	return c.JSON(ctx, http.MethodDelete, roleGroupPath(orgID, id), nil, nil)
}

// ListRoleGroupRoles lists the roles assigned to a role group.
func (c *Client) ListRoleGroupRoles(ctx context.Context, orgID, id string) ([]RoleAssignment, error) {
	return paged[RoleAssignment](ctx, c, roleGroupPath(orgID, id)+"/roles", nil, "data")
}

// AssignRoleGroupRoles assigns roles to a role group.
//
// Some roles are assigned with an empty response; the result is then nil and
// the assignments can be read back with ListRoleGroupRoles.
func (c *Client) AssignRoleGroupRoles(ctx context.Context, orgID, id string, roles []AssignRoleRequest) ([]RoleAssignment, error) {
	var raw json.RawMessage
	if err := c.JSON(ctx, http.MethodPost, roleGroupPath(orgID, id)+"/roles", roles, &raw); err != nil {
		return nil, err
	}
	return decodeList[RoleAssignment](raw)
}

// UnassignRoleGroupRoles removes role assignments from a role group.
func (c *Client) UnassignRoleGroupRoles(ctx context.Context, orgID, id string, roles []AssignRoleRequest) error {
	return c.JSON(ctx, http.MethodDelete, roleGroupPath(orgID, id)+"/roles", roles, nil)
}

// ListRoleGroupUsers lists the users in a role group.
func (c *Client) ListRoleGroupUsers(ctx context.Context, orgID, id string) ([]User, error) {
	return paged[User](ctx, c, roleGroupPath(orgID, id)+"/users", nil, "data")
}

// AddRoleGroupUser adds a user to a role group.
func (c *Client) AddRoleGroupUser(ctx context.Context, orgID, id, userID string) error {
	return c.JSON(ctx, http.MethodPost, roleGroupPath(orgID, id)+"/users/"+esc(userID), nil, nil)
}

// RemoveRoleGroupUser removes a user from a role group.
func (c *Client) RemoveRoleGroupUser(ctx context.Context, orgID, id, userID string) error {
	return c.JSON(ctx, http.MethodDelete, roleGroupPath(orgID, id)+"/users/"+esc(userID), nil, nil)
}

// ---------------------------------------------------------------- teams

// Team is an access management team.
type Team struct {
	rawJSON
	ID              string   `json:"team_id"`
	TeamName        string   `json:"team_name"`
	OrgID           string   `json:"org_id"`
	TeamType        string   `json:"team_type"`
	AncestorTeamIDs []string `json:"ancestor_team_ids"`
	CreatedAt       string   `json:"created_at,omitempty"`
	UpdatedAt       string   `json:"updated_at,omitempty"`
}

// CreateTeamRequest creates a team. TeamType is "internal" or "default".
type CreateTeamRequest struct {
	TeamName     string `json:"team_name"`
	ParentTeamID string `json:"parent_team_id,omitempty"`
	TeamType     string `json:"team_type"`
}

// UpdateTeamRequest changes the fields that are set.
type UpdateTeamRequest struct {
	TeamName *string `json:"team_name,omitempty"`
	TeamType *string `json:"team_type,omitempty"`
}

// TeamMember is a member of a team.
type TeamMember struct {
	rawJSON
	ID                          string `json:"id"`
	IdentityType                string `json:"identity_type"`
	Name                        string `json:"name"`
	MembershipType              string `json:"membership_type"`
	IsAssignedViaExternalGroups bool   `json:"is_assigned_via_external_groups"`
	CreatedAt                   string `json:"created_at,omitempty"`
	UpdatedAt                   string `json:"updated_at,omitempty"`
}

func teamPath(orgID, id string) string { return orgPath(orgID) + "/teams/" + esc(id) }

// ListTeams lists the teams of an organization.
func (c *Client) ListTeams(ctx context.Context, orgID string) ([]Team, error) {
	return paged[Team](ctx, c, orgPath(orgID)+"/teams", nil, "data")
}

// GetTeam returns a team.
func (c *Client) GetTeam(ctx context.Context, orgID, id string) (*Team, error) {
	return get[Team](ctx, c, teamPath(orgID, id))
}

// CreateTeam creates a team.
func (c *Client) CreateTeam(ctx context.Context, orgID string, req *CreateTeamRequest) (*Team, error) {
	return do[Team](ctx, c, http.MethodPost, orgPath(orgID)+"/teams", req)
}

// UpdateTeam changes a team.
func (c *Client) UpdateTeam(ctx context.Context, orgID, id string, req *UpdateTeamRequest) (*Team, error) {
	return do[Team](ctx, c, http.MethodPatch, teamPath(orgID, id), req)
}

// MoveTeam changes a team's parent.
func (c *Client) MoveTeam(ctx context.Context, orgID, id, parentTeamID string) error {
	return c.JSON(ctx, http.MethodPut, teamPath(orgID, id)+"/parent", map[string]string{"parent_team_id": parentTeamID}, nil)
}

// DeleteTeam deletes a team.
func (c *Client) DeleteTeam(ctx context.Context, orgID, id string) error {
	return c.JSON(ctx, http.MethodDelete, teamPath(orgID, id), nil, nil)
}

// ListTeamMembers lists the members of a team.
func (c *Client) ListTeamMembers(ctx context.Context, orgID, id string) ([]TeamMember, error) {
	return paged[TeamMember](ctx, c, teamPath(orgID, id)+"/members", nil, "data")
}

// AddTeamMember adds a user to a team; membershipType is "member" (the
// default, for "") or "maintainer".
func (c *Client) AddTeamMember(ctx context.Context, orgID, id, userID, membershipType string) error {
	if membershipType == "" {
		membershipType = "member"
	}
	return c.JSON(ctx, http.MethodPut, teamPath(orgID, id)+"/members/"+esc(userID),
		map[string]string{"membership_type": membershipType}, nil)
}

// RemoveTeamMember removes a user from a team.
func (c *Client) RemoveTeamMember(ctx context.Context, orgID, id, userID string) error {
	return c.JSON(ctx, http.MethodDelete, teamPath(orgID, id)+"/members/"+esc(userID), nil, nil)
}

// ListTeamRoles lists the roles assigned to a team.
func (c *Client) ListTeamRoles(ctx context.Context, orgID, id string) ([]RoleAssignment, error) {
	return paged[RoleAssignment](ctx, c, teamPath(orgID, id)+"/roles", nil, "data")
}

// AssignTeamRoles assigns roles to a team.
//
// Some roles are assigned with an empty response; the result is then nil and
// the assignments can be read back with ListTeamRoles.
func (c *Client) AssignTeamRoles(ctx context.Context, orgID, id string, roles []AssignRoleRequest) ([]RoleAssignment, error) {
	var raw json.RawMessage
	if err := c.JSON(ctx, http.MethodPost, teamPath(orgID, id)+"/roles", roles, &raw); err != nil {
		return nil, err
	}
	return decodeList[RoleAssignment](raw)
}

// UnassignTeamRoles removes role assignments from a team.
func (c *Client) UnassignTeamRoles(ctx context.Context, orgID, id string, roles []AssignRoleRequest) error {
	return c.JSON(ctx, http.MethodDelete, teamPath(orgID, id)+"/roles", roles, nil)
}
