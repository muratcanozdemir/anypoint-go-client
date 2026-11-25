package anypoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/muratcanozdemir/anypoint-go-client/internal/jsonx"
)

var (
	// ErrNoOrganization is returned when no organization was configured and the
	// caller's home organization cannot be determined.
	ErrNoOrganization = errors.New("cannot determine organization")
	// ErrNoEnvironment is returned when an environment is needed, none was
	// configured, and the organization has none to default to.
	ErrNoEnvironment = errors.New("no environment available")
)

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsID reports whether s looks like an Anypoint ID (a UUID).
func IsID(s string) bool { return uuidRE.MatchString(s) }

// Me returns /accounts/api/me for the authenticated user or connected app.
func (c *Client) Me(ctx context.Context) (map[string]any, error) {
	c.mu.Lock()
	me := c.me
	c.mu.Unlock()
	if me != nil {
		return me, nil
	}
	if err := c.JSON(ctx, http.MethodGet, "/accounts/api/me", nil, &me); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.me = me
	c.mu.Unlock()
	return me, nil
}

// homeOrgID picks the caller's organization the same way the Terraform provider does.
func homeOrgID(me map[string]any) string {
	for _, p := range []string{"client.org_id", "user.organization.id", "user.properties.cs_auth.activeOrganizationId"} {
		if s := jsonx.Str(jsonx.Dig(me, p)); s != "" {
			return s
		}
	}
	return ""
}

// OrgID resolves the configured organization (ID or name), or falls back to
// the caller's home organization.
func (c *Client) OrgID(ctx context.Context) (string, error) {
	c.mu.Lock()
	id := c.orgID
	c.mu.Unlock()
	if id != "" {
		return id, nil
	}
	id, err := c.resolveOrg(ctx)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.orgID = id
	c.mu.Unlock()
	return id, nil
}

func (c *Client) resolveOrg(ctx context.Context) (string, error) {
	want := c.org
	if IsID(want) {
		return want, nil
	}
	me, err := c.Me(ctx)
	if err != nil {
		return "", err
	}
	if want == "" {
		if home := homeOrgID(me); home != "" {
			return home, nil
		}
		return "", ErrNoOrganization
	}
	orgs, err := c.BusinessGroups(ctx)
	if err != nil {
		return "", err
	}
	for _, o := range orgs {
		if strings.EqualFold(o.Name, want) {
			return o.ID, nil
		}
	}
	return "", notFound("organization", want)
}

// BusinessGroups lists the organizations the caller can access: a user's
// memberOfOrganizations, or, for a connected app, its organization's
// hierarchy (flattened, parents first).
func (c *Client) BusinessGroups(ctx context.Context) ([]Organization, error) {
	me, err := c.Me(ctx)
	if err != nil {
		return nil, err
	}
	if raw, err := json.Marshal(jsonx.Dig(me, "user.memberOfOrganizations")); err == nil {
		if orgs, err := decodeList[Organization](raw); err == nil && len(orgs) > 0 {
			return orgs, nil
		}
	}
	root := homeOrgID(me)
	if root == "" {
		return nil, ErrNoOrganization
	}
	h, err := c.GetOrganizationHierarchy(ctx, root)
	if err != nil {
		return nil, err
	}
	var out []Organization
	var walk func(o Organization, parent string)
	walk = func(o Organization, parent string) {
		subs := o.SubOrganizations
		o.SubOrganizations = nil
		if o.ParentID == "" {
			o.ParentID = parent
		}
		o.IsRoot = parent == ""
		out = append(out, o)
		for _, s := range subs {
			walk(s, o.ID)
		}
	}
	walk(*h, "")
	return out, nil
}

// Envs lists the environments of the resolved organization.
func (c *Client) Envs(ctx context.Context) ([]Environment, error) {
	org, err := c.OrgID(ctx)
	if err != nil {
		return nil, err
	}
	return c.ListEnvironments(ctx, org)
}

// EnvID resolves the configured environment (ID or case-insensitive name).
func (c *Client) EnvID(ctx context.Context) (string, error) {
	c.mu.Lock()
	id := c.envID
	c.mu.Unlock()
	if id != "" {
		return id, nil
	}
	id, err := c.resolveEnv(ctx)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.envID = id
	c.mu.Unlock()
	return id, nil
}

func (c *Client) resolveEnv(ctx context.Context) (string, error) {
	want := c.env
	if IsID(want) {
		return want, nil
	}
	envs, err := c.Envs(ctx)
	if err != nil {
		return "", err
	}
	if want == "" {
		return c.defaultEnv(ctx, envs)
	}
	var names []string
	for _, e := range envs {
		if strings.EqualFold(e.Name, want) {
			return e.ID, nil
		}
		names = append(names, e.Name)
	}
	return "", fmt.Errorf("environment %q not found (available: %s): %w", want, strings.Join(names, ", "), ErrNotFound)
}

// defaultEnv picks the environment anypoint-cli-v4 uses when none is given:
// the user's preferred environment for the organization, else the first
// production environment, else the first one.
func (c *Client) defaultEnv(ctx context.Context, envs []Environment) (string, error) {
	if len(envs) == 0 {
		return "", ErrNoEnvironment
	}
	org, err := c.OrgID(ctx)
	if err != nil {
		return "", err
	}
	if me, err := c.Me(ctx); err == nil {
		pref := jsonx.Str(jsonx.Dig(me, "user.organizationPreferences."+org+".defaultEnvironment"))
		for _, e := range envs {
			if pref != "" && e.ID == pref {
				return e.ID, nil
			}
		}
	}
	for _, e := range envs {
		if e.IsProduction {
			return e.ID, nil
		}
	}
	return envs[0].ID, nil
}

// Expand replaces {org} and {env} placeholders with the resolved IDs,
// resolving each only when it is present.
func (c *Client) Expand(ctx context.Context, path string) (string, error) {
	if strings.Contains(path, "{org}") {
		id, err := c.OrgID(ctx)
		if err != nil {
			return "", err
		}
		path = strings.ReplaceAll(path, "{org}", id)
	}
	if strings.Contains(path, "{env}") {
		id, err := c.EnvID(ctx)
		if err != nil {
			return "", err
		}
		path = strings.ReplaceAll(path, "{env}", id)
	}
	return path, nil
}

// MasterOrgID returns the root (master) organization of the caller.
func (c *Client) MasterOrgID(ctx context.Context) (string, error) {
	me, err := c.Me(ctx)
	if err != nil {
		return "", err
	}
	for _, o := range jsonx.Maps(jsonx.Dig(me, "user.memberOfOrganizations")) {
		if b, _ := o["isMaster"].(bool); b {
			return jsonx.Str(o["id"]), nil
		}
	}
	if id := homeOrgID(me); id != "" {
		return id, nil
	}
	return "", ErrNoOrganization
}
