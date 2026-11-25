package anypoint

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
)

// Secrets Manager: secret groups and the secrets in them. Endpoints and
// request shapes follow anypoint-cli-v4's secrets-mgr plugin; certificate
// pinsets, which the CLI lacks, follow the Terraform provider.

// SecretKind is the collection of a secret group that holds a kind of secret.
type SecretKind string

// The kinds of secrets.
const (
	SharedSecrets      SecretKind = "sharedSecrets"
	Certificates       SecretKind = "certificates"
	Keystores          SecretKind = "keystores"
	Truststores        SecretKind = "truststores"
	TLSContexts        SecretKind = "tlsContexts"
	CertificatePinsets SecretKind = "certificatePinsets"
)

// SecretMeta carries a secret's (or group's) ID and audit data.
type SecretMeta struct {
	ID         string `json:"id"`
	CreatedAt  string `json:"createdAt,omitempty"`
	ModifiedAt string `json:"modifiedAt,omitempty"`
	ModifiedBy string `json:"modifiedBy,omitempty"`
}

// SecretGroup groups secrets of one environment.
type SecretGroup struct {
	rawJSON
	Meta         SecretMeta `json:"meta"`
	Name         string     `json:"name"`
	Downloadable bool       `json:"downloadable"`
	CurrentState string     `json:"currentState,omitempty"`
}

// Secret is any kind of secret as returned by the API; only the fields of
// its kind are set.
type Secret struct {
	rawJSON
	Meta           SecretMeta `json:"meta"`
	Name           string     `json:"name"`
	Type           string     `json:"type,omitempty"`   // shared secret, keystore, truststore and certificate type
	Target         string     `json:"target,omitempty"` // TLS context: Mule, FlexGateway or SecurityFabric
	ExpirationDate string     `json:"expirationDate,omitempty"`
	// shared secrets (secret values are never returned)
	Username    string `json:"username,omitempty"`
	AccessKeyID string `json:"accessKeyId,omitempty"`
	// keystores, truststores, certificates
	KeystoreFileName    string `json:"keystoreFileName,omitempty"`
	KeyFileName         string `json:"keyFileName,omitempty"`
	CertificateFileName string `json:"certificateFileName,omitempty"`
	CapathFileName      string `json:"capathFileName,omitempty"`
	TruststoreFileName  string `json:"truststoreFileName,omitempty"`
	Alias               string `json:"alias,omitempty"`
	Algorithm           string `json:"algorithm,omitempty"`
	// TLS contexts
	Keystore              *SecretRef             `json:"keystore,omitempty"`
	Truststore            *SecretRef             `json:"truststore,omitempty"`
	CipherSuites          []string               `json:"cipherSuites,omitempty"`
	AcceptableTLSVersions *AcceptableTLSVersions `json:"acceptableTlsVersions,omitempty"`
	Insecure              bool                   `json:"insecure,omitempty"`
	MinTLSVersion         string                 `json:"minTlsVersion,omitempty"`
	MaxTLSVersion         string                 `json:"maxTlsVersion,omitempty"`
	AlpnProtocols         []string               `json:"alpnProtocols,omitempty"`
	InboundSettings       *TLSInboundSettings    `json:"inboundSettings,omitempty"`
	OutboundSettings      *TLSOutboundSettings   `json:"outboundSettings,omitempty"`
}

// SecretRef points a TLS context at a keystore or truststore of the same
// group, e.g. {"path": "keystores/<id>"}.
type SecretRef struct {
	Path string `json:"path"`
}

// AcceptableTLSVersions is a Mule TLS context's protocol selection.
type AcceptableTLSVersions struct {
	TLSv1Dot1 bool `json:"tlsV1Dot1"`
	TLSv1Dot2 bool `json:"tlsV1Dot2"`
}

// TLSInboundSettings configures a Flex Gateway TLS context's server side.
type TLSInboundSettings struct {
	EnableClientCertValidation bool `json:"enableClientCertValidation"`
}

// TLSOutboundSettings configures a Flex Gateway TLS context's client side.
type TLSOutboundSettings struct {
	SkipServerCertValidation bool `json:"skipServerCertValidation"`
}

// SecretResult is the API's answer to a create, replace or modify.
type SecretResult struct {
	rawJSON
	ID      string `json:"id,omitempty"`
	Message string `json:"message,omitempty"`
}

// SharedSecretRequest creates or replaces a shared secret. Type is
// UsernamePassword, SymmetricKey, S3Credential or Blob.
type SharedSecretRequest struct {
	Type            string `json:"type"`
	Name            string `json:"name"`
	ExpirationDate  string `json:"expirationDate,omitempty"`
	Username        string `json:"username,omitempty"`
	Password        string `json:"password,omitempty"`
	Key             string `json:"key,omitempty"`
	AccessKeyID     string `json:"accessKeyId,omitempty"`
	SecretAccessKey string `json:"secretAccessKey,omitempty"`
	Content         string `json:"content,omitempty"`
}

// TLSContextRequest creates or replaces a TLS context. For Mule set
// AcceptableTLSVersions and Insecure; for FlexGateway the min/max versions,
// ALPN protocols and the inbound/outbound settings.
type TLSContextRequest struct {
	Target                string                 `json:"target"` // Mule or FlexGateway
	Name                  string                 `json:"name"`
	ExpirationDate        string                 `json:"expirationDate,omitempty"`
	Keystore              *SecretRef             `json:"keystore,omitempty"`
	Truststore            *SecretRef             `json:"truststore,omitempty"`
	CipherSuites          []string               `json:"cipherSuites"`
	AcceptableTLSVersions *AcceptableTLSVersions `json:"acceptableTlsVersions,omitempty"`
	Insecure              *bool                  `json:"insecure,omitempty"`
	MinTLSVersion         string                 `json:"minTlsVersion,omitempty"`
	MaxTLSVersion         string                 `json:"maxTlsVersion,omitempty"`
	AlpnProtocols         []string               `json:"alpnProtocols,omitempty"`
	InboundSettings       *TLSInboundSettings    `json:"inboundSettings,omitempty"`
	OutboundSettings      *TLSOutboundSettings   `json:"outboundSettings,omitempty"`
}

// SecretFile is a file part of a store upload.
type SecretFile struct {
	FileName string
	Content  io.Reader
}

// SecretStoreRequest is the multipart upload of a keystore, truststore,
// certificate or certificate pinset. Fields are form values such as type,
// name, storePassphrase, keyPassphrase, alias, algorithm and expirationDate;
// Files are keyed by form field: keyStore, certificate, key and capath
// (keystores), trustStore (truststores), certStore (certificates),
// certificatePinset (pinsets).
type SecretStoreRequest struct {
	Fields map[string]string
	Files  map[string]SecretFile
}

func (r *SecretStoreRequest) encode() ([]byte, string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := writeFields(w, "", r.Fields); err != nil {
		return nil, "", err
	}
	for field, f := range r.Files {
		part, err := w.CreateFormFile(field, f.FileName)
		if err != nil {
			return nil, "", err
		}
		if _, err := io.Copy(part, f.Content); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return body.Bytes(), w.FormDataContentType(), nil
}

func secretGroupsPath(orgID, envID string) string {
	return "/secrets-manager/api/v1/organizations/" + esc(orgID) + "/environments/" + esc(envID) + "/secretGroups"
}

func secretsPath(orgID, envID, groupID string, kind SecretKind) string {
	return secretGroupsPath(orgID, envID) + "/" + esc(groupID) + "/" + string(kind)
}

// ListSecretGroups lists the secret groups of an environment.
func (c *Client) ListSecretGroups(ctx context.Context, orgID, envID string) ([]SecretGroup, error) {
	return list[SecretGroup](ctx, c, secretGroupsPath(orgID, envID))
}

// GetSecretGroup returns a secret group.
func (c *Client) GetSecretGroup(ctx context.Context, orgID, envID, groupID string) (*SecretGroup, error) {
	return get[SecretGroup](ctx, c, secretGroupsPath(orgID, envID)+"/"+esc(groupID))
}

// CreateSecretGroup creates a secret group.
func (c *Client) CreateSecretGroup(ctx context.Context, orgID, envID, name string, downloadable bool) (*SecretResult, error) {
	return do[SecretResult](ctx, c, http.MethodPost, secretGroupsPath(orgID, envID),
		map[string]any{"name": name, "downloadable": downloadable})
}

// UpdateSecretGroup changes a group's name (if not empty) and downloadable flag (if not nil).
func (c *Client) UpdateSecretGroup(ctx context.Context, orgID, envID, groupID, name string, downloadable *bool) (*SecretResult, error) {
	body := map[string]any{}
	if name != "" {
		body["name"] = name
	}
	if downloadable != nil {
		body["downloadable"] = *downloadable
	}
	return do[SecretResult](ctx, c, http.MethodPatch, secretGroupsPath(orgID, envID)+"/"+esc(groupID), body)
}

// DeleteSecretGroup deletes a secret group and its secrets.
func (c *Client) DeleteSecretGroup(ctx context.Context, orgID, envID, groupID string) error {
	return c.JSON(ctx, http.MethodDelete, secretGroupsPath(orgID, envID)+"/"+esc(groupID), nil, nil)
}

// ListSecrets lists the secrets of a kind in a group. For TLS contexts,
// target (Mule, FlexGateway) filters them; it is ignored otherwise.
func (c *Client) ListSecrets(ctx context.Context, orgID, envID, groupID string, kind SecretKind, target string) ([]Secret, error) {
	p := secretsPath(orgID, envID, groupID, kind)
	if kind == TLSContexts && target != "" {
		p = withQuery(p, url.Values{"type": {target}})
	}
	return list[Secret](ctx, c, p)
}

// GetSecret returns a secret.
func (c *Client) GetSecret(ctx context.Context, orgID, envID, groupID string, kind SecretKind, id string) (*Secret, error) {
	return get[Secret](ctx, c, secretsPath(orgID, envID, groupID, kind)+"/"+esc(id))
}

// sendSecret posts or puts a JSON request (*SharedSecretRequest,
// *TLSContextRequest) or a *SecretStoreRequest.
func (c *Client) sendSecret(ctx context.Context, method, path string, req any) (*SecretResult, error) {
	store, ok := req.(*SecretStoreRequest)
	if !ok {
		return do[SecretResult](ctx, c, method, path, req)
	}
	body, ct, err := store.encode()
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(ctx, method, path, body, http.Header{"Content-Type": {ct}})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out SecretResult
	if len(bytes.TrimSpace(data)) > 0 {
		if err := decode(data, &out); err != nil {
			return nil, err
		}
	}
	return &out, nil
}

// CreateSecret creates a secret from a *SharedSecretRequest,
// *TLSContextRequest or *SecretStoreRequest.
func (c *Client) CreateSecret(ctx context.Context, orgID, envID, groupID string, kind SecretKind, req any) (*SecretResult, error) {
	return c.sendSecret(ctx, http.MethodPost, secretsPath(orgID, envID, groupID, kind), req)
}

// ReplaceSecret replaces a secret's content, as CreateSecret.
func (c *Client) ReplaceSecret(ctx context.Context, orgID, envID, groupID string, kind SecretKind, id string, req any) (*SecretResult, error) {
	return c.sendSecret(ctx, http.MethodPut, secretsPath(orgID, envID, groupID, kind)+"/"+esc(id), req)
}

// ModifySecret changes a secret's name and/or expiration date (empty: unchanged).
func (c *Client) ModifySecret(ctx context.Context, orgID, envID, groupID string, kind SecretKind, id, name, expirationDate string) (*SecretResult, error) {
	body := map[string]string{}
	if name != "" {
		body["name"] = name
	}
	if expirationDate != "" {
		body["expirationDate"] = expirationDate
	}
	return do[SecretResult](ctx, c, http.MethodPatch, secretsPath(orgID, envID, groupID, kind)+"/"+esc(id), body)
}

// DeleteSecret deletes a secret.
func (c *Client) DeleteSecret(ctx context.Context, orgID, envID, groupID string, kind SecretKind, id string) error {
	return c.JSON(ctx, http.MethodDelete, secretsPath(orgID, envID, groupID, kind)+"/"+esc(id), nil, nil)
}

// SecretKeystoreID returns the keystore ID of a TLS context ("" if none).
func (s *Secret) SecretKeystoreID() string { return refID(s.Keystore, "keystores/") }

// SecretTruststoreID returns the truststore ID of a TLS context ("" if none).
func (s *Secret) SecretTruststoreID() string { return refID(s.Truststore, "truststores/") }

func refID(r *SecretRef, prefix string) string {
	if r == nil {
		return ""
	}
	if len(r.Path) > len(prefix) && r.Path[:len(prefix)] == prefix {
		return r.Path[len(prefix):]
	}
	return r.Path
}
