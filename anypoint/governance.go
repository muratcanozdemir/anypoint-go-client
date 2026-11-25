package anypoint

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// API Governance profiles, as anypoint-cli-v4's governance plugin uses them.

const governanceBase = "/governance/xapi/api/v1"

// GovernanceProfile applies rulesets to the APIs matching a filter.
type GovernanceProfile struct {
	rawJSON
	ID                 string              `json:"id,omitempty"`
	Org                string              `json:"org,omitempty"`
	Name               string              `json:"name"`
	Description        string              `json:"description"`
	Filter             string              `json:"filter"`
	Rulesets           []GovernanceRuleset `json:"rulesets"`
	Allowing           []any               `json:"allowing"`
	Denying            []any               `json:"denying"`
	NotificationConfig *NotificationConfig `json:"notificationConfig,omitempty"`
}

// GovernanceRuleset references a ruleset asset ("gav://group/asset/version").
type GovernanceRuleset struct {
	URI string `json:"uri"`
}

// NotificationConfig says who hears about profile failures.
type NotificationConfig struct {
	Enabled       bool                   `json:"enabled"`
	Notifications []GovernanceNotifyRule `json:"notifications"`
}

// GovernanceNotifyRule is one notification of a profile.
type GovernanceNotifyRule struct {
	Condition  string                `json:"condition"`
	Enabled    bool                  `json:"enabled"`
	Recipients []GovernanceRecipient `json:"recipients"`
}

// GovernanceRecipient is a notification recipient: Contact, Publisher, or
// Others with an email address in Value.
type GovernanceRecipient struct {
	ContactType      string `json:"contactType"`
	NotificationType string `json:"notificationType"`
	Label            string `json:"label"`
	Value            string `json:"value"`
}

// GovernanceProfileRequest creates or replaces a profile.
type GovernanceProfileRequest struct {
	ID                 string              `json:"id,omitempty"`
	Org                string              `json:"org"`
	Name               string              `json:"name"`
	Description        string              `json:"description"`
	Filter             string              `json:"filter"`
	Rulesets           []string            `json:"rulesets"`
	Allowing           []any               `json:"allowing"`
	Denying            []any               `json:"denying"`
	NotificationConfig *NotificationConfig `json:"notificationConfig,omitempty"`
}

// governanceRequestID makes the X-Request-Id anypoint-cli-v4 sends, so the
// service's logs can be matched to a call.
func governanceRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("anypoint-cli-governance-%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (c *Client) governance(ctx context.Context, method, path string, ifMatch bool, in, out any) error {
	id := governanceRequestID()
	h := http.Header{"X-Request-Id": {id}}
	if ifMatch {
		h.Set("If-Match", "*")
	}
	if err := c.jsonH(ctx, method, path, h, in, out); err != nil {
		return fmt.Errorf("%w (requestId %s)", err, id)
	}
	return nil
}

// ListGovernanceProfiles lists an organization's profiles.
func (c *Client) ListGovernanceProfiles(ctx context.Context, orgID string) ([]GovernanceProfile, error) {
	var out []GovernanceProfile
	err := c.governance(ctx, http.MethodGet, withQuery(governanceBase+"/profiles", url.Values{"organization": {orgID}}), false, nil, &out)
	return out, err
}

// GetGovernanceProfile returns a profile.
func (c *Client) GetGovernanceProfile(ctx context.Context, profileID string) (*GovernanceProfile, error) {
	var p GovernanceProfile
	if err := c.governance(ctx, http.MethodGet, governanceBase+"/profiles/"+esc(profileID), false, nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// CreateGovernanceProfile creates a profile.
func (c *Client) CreateGovernanceProfile(ctx context.Context, req GovernanceProfileRequest) (*GovernanceProfile, error) {
	var p GovernanceProfile
	if err := c.governance(ctx, http.MethodPost, governanceBase+"/profiles", false, req, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// UpdateGovernanceProfile replaces a profile.
func (c *Client) UpdateGovernanceProfile(ctx context.Context, profileID string, req GovernanceProfileRequest) error {
	return c.governance(ctx, http.MethodPut, governanceBase+"/profiles/"+esc(profileID), true, req, nil)
}

// DeleteGovernanceProfile deletes a profile.
func (c *Client) DeleteGovernanceProfile(ctx context.Context, profileID string) error {
	return c.governance(ctx, http.MethodDelete, governanceBase+"/profiles/"+esc(profileID), true, nil, nil)
}

// GovernancePlan lists the ruleset URIs (gav://...) that apply to an API
// (groupID/assetID, both empty for any) under a filter such as
// "tag:t1,scope:rest-api".
func (c *Client) GovernancePlan(ctx context.Context, orgID, groupID, assetID, filter string) ([]string, error) {
	q := url.Values{"organization": {orgID}, "groupId": {groupID}, "assetId": {assetID}, "filter": {filter}}
	var out struct {
		Profiles []struct {
			Rulesets []GovernanceRuleset `json:"rulesets"`
		} `json:"profiles"`
	}
	if err := c.governance(ctx, http.MethodPost, withQuery(governanceBase+"/plan/explain", q), false, nil, &out); err != nil {
		return nil, err
	}
	var uris []string
	for _, p := range out.Profiles {
		for _, r := range p.Rulesets {
			uris = append(uris, r.URI)
		}
	}
	return uris, nil
}

// AssetType returns an Exchange asset's type ("ruleset", "rest-api", ...);
// an empty version means the latest.
func (c *Client) AssetType(ctx context.Context, groupID, assetID, version string) (string, error) {
	var a struct {
		Type string `json:"type"`
	}
	p := "/exchange/api/v2/assets/" + esc(groupID) + "/" + esc(assetID)
	if version != "" {
		p += "/" + esc(version)
	}
	if err := c.JSON(ctx, http.MethodGet, p, nil, &a); err != nil {
		return "", err
	}
	return a.Type, nil
}

// GAV formats a ruleset reference as the governance service expects it.
func GAV(groupID, assetID, version string) string {
	return "gav://" + strings.Join([]string{groupID, assetID, version}, "/")
}
