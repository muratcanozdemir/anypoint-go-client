package anypoint

import (
	"context"
	"io"
	"net/http"
	"net/url"
)

// Application Manager: CloudHub 2.0 and Runtime Fabric deployments, as
// anypoint-cli-v4's runtime-mgr:application commands use them.

func deploymentsPath(orgID, envID string) string {
	return "/amc/application-manager/api/v2/organizations/" + esc(orgID) + "/environments/" + esc(envID) + "/deployments"
}

// Deployment is a CloudHub 2.0 or Runtime Fabric application deployment.
type Deployment struct {
	rawJSON
	ID                     string              `json:"id"`
	Name                   string              `json:"name"`
	Status                 string              `json:"status,omitempty"`
	DeploymentUpdateStatus string              `json:"deploymentUpdateStatus,omitempty"`
	LastModifiedDate       any                 `json:"lastModifiedDate,omitempty"`
	DesiredVersion         string              `json:"desiredVersion,omitempty"`
	LastSuccessfulVersion  string              `json:"lastSuccessfulVersion,omitempty"`
	Replicas               []DeploymentReplica `json:"replicas,omitempty"`
	Application            struct {
		Status        string         `json:"status,omitempty"`
		DesiredState  string         `json:"desiredState,omitempty"`
		VCores        FlexID         `json:"vCores,omitempty"`
		Ref           AppRef         `json:"ref"`
		Configuration map[string]any `json:"configuration,omitempty"`
	} `json:"application"`
	Target struct {
		Provider           string `json:"provider,omitempty"`
		TargetID           string `json:"targetId"`
		Replicas           int    `json:"replicas"`
		DeploymentSettings struct {
			RuntimeVersion string `json:"runtimeVersion,omitempty"`
			Runtime        *struct {
				Version        string `json:"version,omitempty"`
				ReleaseChannel string `json:"releaseChannel,omitempty"`
				Java           string `json:"java,omitempty"`
			} `json:"runtime,omitempty"`
			InstanceType   string `json:"instanceType,omitempty"`
			UpdateStrategy string `json:"updateStrategy,omitempty"`
			HTTP           struct {
				Inbound struct {
					PublicURL any `json:"publicUrl,omitempty"`
				} `json:"inbound"`
			} `json:"http"`
		} `json:"deploymentSettings"`
	} `json:"target"`
}

// AppRef is the Exchange asset an application is deployed from.
type AppRef struct {
	GroupID    string `json:"groupId,omitempty"`
	ArtifactID string `json:"artifactId,omitempty"`
	Version    string `json:"version,omitempty"`
	Packaging  string `json:"packaging,omitempty"`
}

// DeploymentReplica is one replica of a deployment.
type DeploymentReplica struct {
	State                    string `json:"state,omitempty"`
	Reason                   string `json:"reason,omitempty"`
	CurrentDeploymentVersion string `json:"currentDeploymentVersion,omitempty"`
}

// ListDeployments lists the deployments of an environment.
func (c *Client) ListDeployments(ctx context.Context, orgID, envID string) ([]Deployment, error) {
	return list[Deployment](ctx, c, deploymentsPath(orgID, envID), "items")
}

// GetDeployment returns a deployment by ID.
func (c *Client) GetDeployment(ctx context.Context, orgID, envID, id string) (*Deployment, error) {
	return get[Deployment](ctx, c, deploymentsPath(orgID, envID)+"/"+esc(id))
}

// CreateDeployment deploys an application from its document.
func (c *Client) CreateDeployment(ctx context.Context, orgID, envID string, body any) (*Deployment, error) {
	return do[Deployment](ctx, c, http.MethodPost, deploymentsPath(orgID, envID), body)
}

// UpdateDeployment changes (PATCH) a deployment.
func (c *Client) UpdateDeployment(ctx context.Context, orgID, envID, id string, body any) (*Deployment, error) {
	return do[Deployment](ctx, c, http.MethodPatch, deploymentsPath(orgID, envID)+"/"+esc(id), body)
}

// SetDeploymentState starts (STARTED) or stops (STOPPED) a deployment. The
// API answers 304 when it already is; see [IsStatus].
func (c *Client) SetDeploymentState(ctx context.Context, orgID, envID, id, state string) error {
	return c.JSON(ctx, http.MethodPatch, deploymentsPath(orgID, envID)+"/"+esc(id), Doc{"application": Doc{"desiredState": state}}, nil)
}

// DeleteDeployment deletes a deployment.
func (c *Client) DeleteDeployment(ctx context.Context, orgID, envID, id string) error {
	return c.JSON(ctx, http.MethodDelete, deploymentsPath(orgID, envID)+"/"+esc(id), nil, nil)
}

// DeploymentLog is one log record of a deployment spec.
type DeploymentLog struct {
	DocID     string `json:"docId"`
	Timestamp int64  `json:"timestamp"`
	LogLevel  string `json:"logLevel"`
	Message   string `json:"message"`
	Context   struct {
		Logger string `json:"logger"`
		Event  string `json:"event"`
	} `json:"context"`
}

// ListDeploymentLogs returns the latest log records of a deployment spec, or,
// with afterDocID, the records after that one in ascending order.
func (c *Client) ListDeploymentLogs(ctx context.Context, orgID, envID, id, specID, afterDocID string) ([]DeploymentLog, error) {
	p := deploymentsPath(orgID, envID) + "/" + esc(id) + "/specs/" + esc(specID) + "/logs"
	if afterDocID != "" {
		p = withQuery(p, url.Values{"descending": {"false"}, "afterDocId": {afterDocID}})
	}
	return list[DeploymentLog](ctx, c, p)
}

// DownloadDeploymentLogs returns the log file of a deployment spec.
func (c *Client) DownloadDeploymentLogs(ctx context.Context, orgID, envID, id, specID string) ([]byte, error) {
	resp, err := c.Do(ctx, http.MethodGet, deploymentsPath(orgID, envID)+"/"+esc(id)+"/specs/"+esc(specID)+"/logs/file", nil, http.Header{"Accept": {"*/*"}})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// FabricTarget is a deployment target as Runtime Fabric describes it.
type FabricTarget struct {
	rawJSON
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"` // runtime-fabric, shared-space, private-space
}

// GetFabricTarget returns a deployment target (a Runtime Fabric or a
// CloudHub 2.0 space).
func (c *Client) GetFabricTarget(ctx context.Context, orgID, targetID string) (*FabricTarget, error) {
	return get[FabricTarget](ctx, c, "/runtimefabric/api/organizations/"+esc(orgID)+"/targets/"+esc(targetID))
}

// UsageBasedPricing reports whether an organization is on usage-based
// pricing for Mule runtimes (instance types instead of vCores).
func (c *Client) UsageBasedPricing(ctx context.Context, orgID string) (bool, error) {
	var org struct {
		Entitlements struct {
			UsageBasedPricing *struct {
				MuleRuntimeIntegration struct {
					Enabled bool `json:"enabled"`
				} `json:"muleRuntimeIntegration"`
			} `json:"usageBasedPricing"`
		} `json:"entitlements"`
	}
	if err := c.JSON(ctx, http.MethodGet, "/accounts/api/organizations/"+esc(orgID), nil, &org); err != nil {
		return false, err
	}
	u := org.Entitlements.UsageBasedPricing
	return u != nil && u.MuleRuntimeIntegration.Enabled, nil
}

// LatestAssetVersion returns the latest version of an Exchange asset.
func (c *Client) LatestAssetVersion(ctx context.Context, groupID, assetID string) (string, error) {
	var a struct {
		Version string `json:"version"`
	}
	if err := c.JSON(ctx, http.MethodGet, "/exchange/api/v1/assets/"+esc(groupID)+"/"+esc(assetID), nil, &a); err != nil {
		return "", err
	}
	return a.Version, nil
}
