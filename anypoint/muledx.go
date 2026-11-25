package anypoint

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Mule DX: the Mule runtimes and JDKs Anypoint Code Builder installs, as
// anypoint-cli-v4's dx-mule plugin downloads them.

// MuleDXRuntime is a Mule runtime version Code Builder supports.
type MuleDXRuntime struct {
	Version        string `json:"version"`
	Latest         bool   `json:"latest"`
	CompatibleJDKs []struct {
		Version string `json:"version"`
		Latest  bool   `json:"latest"`
	} `json:"compatibleJDKs,omitempty"`
}

// ListMuleDXRuntimes lists the supported runtime versions.
func (c *Client) ListMuleDXRuntimes(ctx context.Context) ([]MuleDXRuntime, error) {
	var out struct {
		SupportedRuntimeVersions []MuleDXRuntime `json:"supportedRuntimeVersions"`
	}
	err := c.JSON(ctx, http.MethodGet, "/muledx/api/runtime/all", nil, &out)
	return out.SupportedRuntimeVersions, err
}

func (c *Client) stream(ctx context.Context, path string) (io.ReadCloser, error) {
	resp, err := c.Do(ctx, http.MethodGet, path, nil, http.Header{"Accept": {"*/*"}})
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// DownloadMuleDXRuntime streams a Mule runtime distribution (.tar.gz).
func (c *Client) DownloadMuleDXRuntime(ctx context.Context, version string) (io.ReadCloser, error) {
	return c.stream(ctx, "/muledx/api/runtime/download/"+esc(version))
}

// DownloadMuleDXJDK streams a JDK (.tar.gz or .zip) for an OS (mac, linux,
// windows) and architecture (x64, arm64).
func (c *Client) DownloadMuleDXJDK(ctx context.Context, version, osName, arch string) (io.ReadCloser, error) {
	return c.stream(ctx, withQuery("/muledx/api/jdk/download", url.Values{"version": {version}, "os": {osName}, "arch": {arch}}))
}

// ExchangeMavenURL is the base URL of Exchange's Maven facade for the
// client's control plane: maven.<host> on MuleSoft's clouds, the control
// plane itself elsewhere (a proxy or a test server).
func (c *Client) ExchangeMavenURL() string {
	u, err := url.Parse(c.base)
	if err != nil || !strings.HasSuffix(u.Hostname(), ".mulesoft.com") {
		return c.base
	}
	u.Host = "maven." + u.Host
	return u.String()
}

// DownloadExchangeMavenArtifact streams an Exchange asset's jar
// (assetId-version[-classifier].jar) from the Maven facade, signed in.
func (c *Client) DownloadExchangeMavenArtifact(ctx context.Context, groupID, assetID, version, classifier string) (io.ReadCloser, error) {
	name := assetID + "-" + version
	if classifier != "" {
		name += "-" + classifier
	}
	return c.stream(ctx, c.ExchangeMavenURL()+"/api/v2/maven/"+strings.ReplaceAll(groupID, ".", "/")+"/"+
		esc(assetID)+"/"+esc(version)+"/"+esc(name)+".jar")
}
