package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/muratcanozdemir/anypoint-go-client/anypoint"
)

// anypoint-cli-api-manager-plugin: api-mgr:*.

func apiMgrCmd() *Cmd {
	return group("api-mgr", "API Manager",
		group("alert", "API alerts",
			leaf("add", "APIINSTANCEID NAME", "Add a monitoring alert to an API instance", cmdAlertAdd(false)),
			leaf("add-legacy", "APIINSTANCEID NAME", "Add a legacy API Manager alert to an API instance", cmdAlertAdd(true)),
			leaf("list", "APIINSTANCEID", "List an API instance's monitoring alerts", cmdAlertList(false)).alias("ls"),
			leaf("list-legacy", "APIINSTANCEID", "List an API instance's legacy alerts", cmdAlertList(true)),
		),
		group("api", "API instances",
			leaf("autodiscovery", "APIINSTANCEID GATEWAYVERSION", "Show the autodiscovery properties for a gateway version", cmdAPIAutodiscovery),
			leaf("change-specification", "APIINSTANCEID ASSETVERSION", "Change the asset version of an API instance", cmdAPIChangeSpec),
			leaf("classify", "DESTENVNAME APIINSTANCEID", "Move an unclassified API instance into an environment", cmdAPIClassify),
			leaf("delete", "APIINSTANCEID", "Delete an API instance", cmdAPIDelete).alias("rm"),
			leaf("deploy", "APIINSTANCEID", "Deploy an API instance's proxy", cmdAPIDeploy(false)),
			leaf("deprecate", "APIINSTANCEID", "Deprecate an API instance", cmdAPIDeprecate(true)),
			leaf("describe", "APIINSTANCEID", "Show an API instance", cmdAPIDescribe),
			leaf("download-proxy", "APIINSTANCEID TARGETPATH", "Download an API instance's proxy application", cmdAPIDownloadProxy),
			leaf("edit", "APIINSTANCEID", "Edit an API instance", cmdAPIEdit),
			leaf("list", "", "List API instances", cmdAPIList).alias("ls"),
			leaf("manage", "ASSETID ASSETVERSION [GROUPID]", "Manage an Exchange asset as an API instance", cmdAPIManage),
			leaf("promote", "APIINSTANCEID SOURCEENVID", "Promote an API instance from another environment into the selected one", cmdAPIPromote),
			leaf("redeploy", "APIINSTANCEID", "Redeploy an API instance's proxy", cmdAPIDeploy(true)),
			leaf("undeprecate", "APIINSTANCEID", "Undeprecate an API instance", cmdAPIDeprecate(false)),
		),
		group("application", "Client applications",
			leaf("add-owner", "APPLICATIONID ID ENTITYTYPE", "Add an owner (user or team) to a client application", cmdAppAddOwner),
			leaf("import", "PROVIDERID CLIENTID", "Import a client of an identity provider as a client application", cmdAppImport),
			leaf("possible-owners-list", "", "List who can own client applications", cmdAppPossibleOwners),
		),
		group("client-provider", "Client identity providers",
			leaf("list-importable", "", "List the providers client applications can be imported from", cmdClientProvidersImportable),
		),
		group("contract", "API contracts",
			leaf("delete", "APIINSTANCEID CLIENTID", "Delete the contract of a client application", cmdContractDelete).alias("rm"),
			leaf("list", "APIINSTANCEID [SEARCHTEXT]", "List an API instance's contracts", cmdContractList).alias("ls"),
		),
		group("policy", "API policies",
			leaf("apply", "APIINSTANCEID POLICYID", "Apply a policy to an API instance", cmdPolicyApply),
			leaf("describe", "POLICYID", "Show a policy and its configuration properties", cmdPolicyDescribe),
			leaf("disable", "APIINSTANCEID POLICYID", "Disable an applied policy", cmdPolicyDisable(true)),
			leaf("edit", "APIINSTANCEID POLICYID", "Edit an applied policy", cmdPolicyEdit),
			leaf("enable", "APIINSTANCEID POLICYID", "Enable an applied policy", cmdPolicyDisable(false)),
			leaf("list", "[APIINSTANCEID]", "List an API instance's policies, or the available ones", cmdPolicyList).alias("ls"),
			leaf("order", "APIINSTANCEID [ORDER]", "Show or set the order of an API instance's policies", cmdPolicyOrder),
			leaf("remove", "APIINSTANCEID POLICYID", "Remove an applied policy", cmdPolicyRemove).alias("rm"),
		),
		group("tier", "SLA tiers",
			leaf("add", "APIINSTANCEID", "Add an SLA tier", cmdTierAdd),
			leaf("copy", "SOURCEAPIINSTANCEID TARGETAPIINSTANCEID", "Make the target's SLA tiers match the source's", cmdTierCopy),
			leaf("delete", "APIINSTANCEID TIERID", "Delete an SLA tier", cmdTierDelete).alias("rm"),
			leaf("list", "APIINSTANCEID [SEARCHTEXT]", "List an API instance's SLA tiers", cmdTierList).alias("ls"),
		),
		group("upstream", "API upstreams",
			leaf("add", "APIINSTANCEID URI", "Add an upstream", cmdUpstreamAdd),
			leaf("bulk-edit", "APIINSTANCEID", "Set the TLS context of all upstreams", cmdUpstreamBulkEdit),
			leaf("delete", "APIINSTANCEID UPSTREAMID", "Delete an upstream", cmdUpstreamDelete).alias("rm"),
			leaf("edit", "APIINSTANCEID UPSTREAMID", "Edit an upstream", cmdUpstreamEdit),
			leaf("list", "APIINSTANCEID", "List an API instance's upstreams", cmdUpstreamList).alias("ls"),
		),
	)
}

// apiSetup parses the flags and resolves the organization and environment.
func apiSetup(x *Ctx, fs *flag.FlagSet, args []string) (pos []string, c *anypoint.Client, org, env string, err error) {
	if pos, c, err = x.Setup(fs, args); err != nil {
		return
	}
	org, env, err = x.OrgEnv(c)
	return
}

// setFlags returns the names of the flags given on the command line.
func setFlags(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) {
		set[f.Name] = true
		if target, ok := strings.CutPrefix(f.Usage, "same as -"); ok { // an aliasFlag
			set[target] = true
		}
	})
	return set
}

// notFoundAs replaces a 404 error with msg, as anypoint-cli-v4 does.
func notFoundAs(err error, msg string) error {
	if anypoint.IsNotFound(err) {
		return errors.New(msg)
	}
	return err
}

func paginationFlags(fs *flag.FlagSet, sortDefault string) (limit, offset *int, sort *string) {
	return fs.Int("limit", 10, "number of results"), fs.Int("offset", 0, "results to skip"),
		fs.String("sort", sortDefault, "`field` to sort by")
}

// ---------------------------------------------------------------- list / describe

func cmdAPIList(x *Ctx, args []string) error {
	fs := x.Flags()
	limit, offset, sort := paginationFlags(fs, "createdDate")
	assetID := fs.String("assetId", "", "only instances of this asset `ID`")
	apiVersion := fs.String("apiVersion", "", "only instances of this API (product) `version`")
	label := fs.String("instanceLabel", "", "only instances with this `label`")
	_, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	page, err := c.ListAPIInstances(x.ctx, org, env, anypoint.APIListOptions{AssetID: *assetID, ProductVersion: *apiVersion,
		InstanceLabel: *label, Limit: *limit, Offset: *offset, Sort: *sort})
	if err != nil {
		return err
	}
	var apis []anypoint.APIInstance
	names := map[int]string{}
	for _, a := range page.Assets {
		for _, api := range a.APIs {
			names[api.ID] = a.ExchangeAssetName
			apis = append(apis, api)
		}
	}
	return x.Print(apis, func() {
		t := newTable(x.Out, "ASSET ID", "INSTANCE ID", "ASSET VERSION", "EXCHANGE ASSET NAME", "PRODUCT VERSION", "INSTANCE LABEL", "DEPRECATED", "PUBLIC")
		for _, a := range apis {
			t.row(a.AssetID, strconv.Itoa(a.ID), a.AssetVersion, names[a.ID], a.ProductVersion, a.InstanceLabel, yn(a.Deprecated), yn(a.IsPublic))
		}
		t.flush()
	})
}

var endpointTypes = map[string]string{"http": "HTTP", "raml": "RAML/OAS", "rest": "RAML/OAS", "wsdl": "WSDL"}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func cmdAPIDescribe(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	a, err := c.GetAPIInstance(x.ctx, org, env, pos[0], true)
	if err != nil {
		return notFoundAs(err, fmt.Sprintf("API with ID %q not found", pos[0]))
	}
	return x.Print(a, func() {
		kv := []string{"ID", strconv.Itoa(a.ID), "Instance Label", a.InstanceLabel, "Asset ID", a.AssetID,
			"Asset Version", a.AssetVersion, "Asset Group ID", a.GroupID, "Deprecated", yn(a.Deprecated),
			"Tags", strings.Join(a.Tags, ", "), "Product Version", a.ProductVersion, "API Type", a.Technology,
			"Is Public", yn(a.IsPublic), "Environment ID", a.EnvironmentID, "Last Active", ago(a.LastActiveDate),
			"Endpoint URI", a.EndpointURI}
		e := a.Endpoint
		if e != nil && a.Technology != "flexGateway" {
			kv = append(kv, "Mule 4 or above", yn(deref(e.MuleVersion4OrAbove)))
		}
		d := a.Deployment
		if d == nil {
			d = &anypoint.APIDeployment{}
		}
		if d.TargetID != "" {
			kv = append(kv, "Deployment TargetID", d.TargetID.String())
		}
		if e == nil {
			e = &anypoint.APIEndpoint{}
		}
		w := deref(e.WSDLConfig)
		tls := deref(e.TLSContexts)
		in, out := deref(tls.Inbound), deref(tls.Outbound)
		timeout := ""
		if e.ResponseTimeout != nil {
			timeout = strconv.Itoa(*e.ResponseTimeout)
		}
		for _, row := range [][2]string{
			{"Application Name", d.ApplicationName}, {"Deployment Environment", d.EnvironmentName},
			{"Deployment Gateway Version", d.GatewayVersion}, {"Deployment Type", e.DeploymentType},
			{"Proxy URI", deref(e.ProxyURI)}, {"Response Timeout", timeout}, {"Implementation URI", deref(e.URI)},
			{"Type", endpointTypes[e.Type]}, {"Service Name", w.Name}, {"Service Namespace", w.Namespace}, {"Service Port", w.Port},
			{"Inbound TLS Secret Group ID", in.SecretGroupID}, {"Inbound TLS Context ID", in.TLSContextID}, {"Inbound TLS Context name", in.Name},
			{"Outbound TLS Secret Group ID", out.SecretGroupID}, {"Outbound TLS Context ID", out.TLSContextID}, {"Outbound TLS Context name", out.Name},
		} {
			if row[1] != "" {
				kv = append(kv, row[0], row[1])
			}
		}
		printKV(x.Out, kv...)
	})
}

// ---------------------------------------------------------------- manage / edit

var (
	apiTypes       = []string{"http", "raml", "wsdl", "mcp", "a2a", "llm", "grpc"}
	apiSchemes     = []string{"http", "https"}
	apiDeployments = []string{"cloudhub", "cloudhub2", "hybrid", "rtf"}
)

// apiOpts holds the manage/edit flags; nil means "not given" (JavaScript's
// undefined in anypoint-cli-v4), which matters for edit and for the body.
type apiOpts struct {
	WithProxy, RefUserDomain, Mule4, IsFlex, IsCloudHub    *bool
	DeploymentType, Type, URI, Scheme, Port, Path, Label   *string
	ProviderID, ServiceName, ServiceNamespace, ServicePort *string
	EndpointURI, InSG, InTLS, OutSG, OutTLS, Routing       *string
	ResponseTimeout                                        *int
}

// apiOptFlags registers the manage/edit flags and returns a function that
// reads the given ones after parsing.
func apiOptFlags(fs *flag.FlagSet) func() (*apiOpts, error) {
	bools := map[string]*bool{}
	for _, b := range []struct{ name, short, usage string }{
		{"withProxy", "p", "the API is reached through a proxy"},
		{"referencesUserDomain", "r", "the proxy uses a Mule 3 shared domain (hybrid)"},
		{"muleVersion4OrAbove", "m", "the API runs on Mule 4 or above"},
		{"isFlex", "f", "the API runs on Flex Gateway"},
	} {
		bools[b.name] = fs.Bool(b.name, false, b.usage)
		aliasFlag(fs, b.short, b.name)
	}
	strs := map[string]*string{}
	for _, s := range []struct{ name, usage string }{
		{"deploymentType", "`type` of deployment: " + strings.Join(apiDeployments, ", ")},
		{"type", "endpoint `type`: " + strings.Join(apiTypes, ", ")},
		{"uri", "implementation `URI`"},
		{"scheme", "proxy `scheme`: http or https"},
		{"port", "proxy `port`"},
		{"path", "proxy `path`"},
		{"providerId", "client provider `ID`"},
		{"apiInstanceLabel", "instance `label`"},
		{"serviceName", "WSDL service `name`"},
		{"serviceNamespace", "WSDL service `namespace`"},
		{"servicePort", "WSDL service `port`"},
		{"endpointUri", "consumer endpoint `URL`"},
		{"inboundSecretGroupId", "inbound TLS context secret group `ID`"},
		{"inboundTlsContextId", "inbound TLS context `ID` (\"null\" removes it)"},
		{"outboundSecretGroupId", "outbound TLS context secret group `ID`"},
		{"outboundTlsContextId", "outbound TLS context `ID` (\"null\" removes it)"},
		{"routing", `routing rules as JSON, e.g. [{"label":"Rule 1","rules":{"path":"/path"},"upstreams":[{"id":"upstream-id","weight":100}]}]`},
	} {
		strs[s.name] = fs.String(s.name, "", s.usage)
	}
	timeout := fs.Int("responseTimeout", 0, "response timeout in `ms`")
	return func() (*apiOpts, error) {
		set := setFlags(fs)
		b := func(n string) *bool {
			if set[n] {
				return bools[n]
			}
			return nil
		}
		s := func(n string) *string {
			if set[n] {
				return strs[n]
			}
			return nil
		}
		o := &apiOpts{WithProxy: b("withProxy"), RefUserDomain: b("referencesUserDomain"), Mule4: b("muleVersion4OrAbove"), IsFlex: b("isFlex"),
			DeploymentType: s("deploymentType"), Type: s("type"), URI: s("uri"), Scheme: s("scheme"), Port: s("port"), Path: s("path"),
			Label: s("apiInstanceLabel"), ProviderID: s("providerId"), ServiceName: s("serviceName"), ServiceNamespace: s("serviceNamespace"),
			ServicePort: s("servicePort"), EndpointURI: s("endpointUri"), InSG: s("inboundSecretGroupId"), InTLS: s("inboundTlsContextId"),
			OutSG: s("outboundSecretGroupId"), OutTLS: s("outboundTlsContextId"), Routing: s("routing")}
		if set["responseTimeout"] {
			o.ResponseTimeout = timeout
		}
		for _, chk := range []struct {
			name    string
			v       *string
			allowed []string
		}{{"deploymentType", o.DeploymentType, apiDeployments}, {"type", o.Type, apiTypes}, {"scheme", o.Scheme, apiSchemes}} {
			if chk.v != nil {
				if err := oneOf(chk.name, *chk.v, chk.allowed...); err != nil {
					return nil, err
				}
			}
		}
		for _, dep := range [][2]string{{"inboundSecretGroupId", "inboundTlsContextId"}, {"outboundSecretGroupId", "outboundTlsContextId"}} {
			if set[dep[0]] && !set[dep[1]] {
				return nil, fmt.Errorf("--%s needs --%s", dep[0], dep[1])
			}
			if set[dep[1]] && *strs[dep[1]] != "null" && !set[dep[0]] {
				return nil, fmt.Errorf("--%s needs --%s", dep[1], dep[0])
			}
		}
		if o.EndpointURI != nil && *o.EndpointURI != "" {
			if u, err := url.Parse(*o.EndpointURI); err != nil || u.Scheme == "" || u.Host == "" {
				return nil, errors.New("option `--endpointUri` should be a valid URL")
			}
		}
		return o, nil
	}
}

func is(p *bool) bool { return p != nil && *p }

func sv(p *string) string { return deref(p) }

func isCloudHub(dt string) bool { return dt == "cloudhub" || dt == "CH" }
func isRTF(dt string) bool      { return dt == "rtf" || dt == "RF" }
func isHybrid(dt string) bool   { return dt == "hybrid" || dt == "HY" }
func isCH2(dt string) bool      { return dt == "cloudhub2" || dt == "CH2" }

// prepare normalizes the options as anypoint-cli-v4's prepareOptions.
func (o *apiOpts) prepare(convertType bool) {
	if o.DeploymentType == nil && is(o.IsFlex) {
		o.DeploymentType = new("hybrid")
	}
	if o.DeploymentType == nil {
		o.DeploymentType = new("cloudhub")
	}
	o.IsCloudHub = new(isCloudHub(*o.DeploymentType))
	if convertType && o.Type != nil {
		switch *o.Type {
		case "raml":
			if is(o.WithProxy) {
				o.Type = new("rest-api")
			}
		case "http":
			o.Type = new("http-api")
		case "wsdl":
			o.Type = new("soap-api")
		}
	}
	if short, ok := map[string]string{"cloudhub": "CH", "hybrid": "HY", "rtf": "RF", "cloudhub2": "CH2"}[*o.DeploymentType]; ok {
		o.DeploymentType = new(short)
	}
	if !is(o.WithProxy) {
		o.IsCloudHub, o.RefUserDomain = nil, nil
	}
	if is(o.IsCloudHub) {
		o.RefUserDomain = nil
	}
	if is(o.RefUserDomain) {
		o.Port, o.IsCloudHub = nil, new(false)
	}
	if isRTF(*o.DeploymentType) {
		switch sv(o.Scheme) {
		case "http":
			o.Port = new("8081")
		case "https":
			o.Port = new("8082")
		}
	}
}

func tlsRef(id, group *string) (v any, present bool) {
	switch {
	case id == nil:
		return nil, false
	case *id == "null":
		return nil, true
	}
	return anypoint.Doc{"secretGroupId": sv(group), "tlsContextId": *id}, true
}

// body builds the API instance document as anypoint-cli-v4's composeAPIReqBody.
func (o *apiOpts) body(assetID, assetVersion, groupID string) (anypoint.Doc, error) {
	orNil := func(p any) any {
		switch v := p.(type) {
		case *string:
			if v == nil {
				return nil
			}
			return *v
		case *bool:
			if v == nil {
				return nil
			}
			return *v
		case *int:
			if v == nil {
				return nil
			}
			return *v
		}
		return p
	}
	endpoint := anypoint.Doc{
		"type": orNil(o.Type), "proxyUri": nil, "isCloudHub": nil, "deploymentType": orNil(o.DeploymentType),
		"referencesUserDomain": orNil(o.RefUserDomain), "responseTimeout": orNil(o.ResponseTimeout),
		"muleVersion4OrAbove": orNil(o.Mule4),
	}
	if is(o.IsCloudHub) {
		endpoint["isCloudHub"] = true
	}
	if o.Routing == nil {
		endpoint["uri"] = orNil(o.URI)
	}
	body := anypoint.Doc{"endpoint": endpoint, "instanceLabel": orNil(o.Label),
		"spec": anypoint.Doc{"assetId": assetID, "version": assetVersion, "groupId": groupID}}
	if is(o.IsFlex) {
		body["technology"] = "flexGateway"
	}
	if o.EndpointURI != nil {
		body["endpointUri"] = *o.EndpointURI
	}
	if is(o.WithProxy) {
		proxy := sv(o.Scheme) + "://0.0.0.0"
		if sv(o.Port) != "" {
			proxy += ":" + *o.Port
		}
		proxy += sv(o.Path)
		endpoint["proxyUri"] = proxy
	}
	in, inSet := tlsRef(o.InTLS, o.InSG)
	out, outSet := tlsRef(o.OutTLS, o.OutSG)
	if inSet || outSet {
		tls := anypoint.Doc{}
		if inSet {
			tls["inbound"] = in
		}
		if outSet {
			tls["outbound"] = out
		}
		endpoint["tlsContexts"] = tls
	}
	if o.ProviderID != nil {
		body["providerId"] = *o.ProviderID
	}
	if t := sv(o.Type); (t == "soap-api" || t == "wsdl") && is(o.Mule4) && is(o.WithProxy) {
		endpoint["wsdlConfig"] = anypoint.Doc{"name": sv(o.ServiceName), "namespace": sv(o.ServiceNamespace), "port": sv(o.ServicePort)}
	}
	if o.Routing != nil {
		var routing any
		if err := json.Unmarshal([]byte(*o.Routing), &routing); err != nil {
			return nil, fmt.Errorf("--routing must be JSON: %w", err)
		}
		body["routing"] = routing
	}
	return body, nil
}

func (o *apiOpts) checkTLS() error {
	if (o.InTLS != nil || o.OutTLS != nil) && !is(o.WithProxy) {
		return errors.New("TLS Contexts configuration are only enabled for proxies (--withProxy=true)")
	}
	return nil
}

// validateManage runs anypoint-cli-v4's api manage validators.
func (o *apiOpts) validateManage() error {
	dt, proxy := sv(o.DeploymentType), is(o.WithProxy)
	switch {
	case proxy && !is(o.IsFlex) && dt == "":
		return errors.New("missing required option `--deploymentType`")
	case proxy && o.Scheme == nil:
		return errors.New("option `--scheme` is required when proxy is used")
	case proxy && o.URI == nil:
		return errors.New("option `--uri` is required when proxy is used")
	case (isCloudHub(dt) || isRTF(dt)) && is(o.RefUserDomain):
		return errors.New(`option --referencesUserDomain can not be used when --deploymentType value is "cloudhub" or "rtf"`)
	case proxy && (isCloudHub(dt) || (isHybrid(dt) && !is(o.RefUserDomain))) && sv(o.Port) == "":
		return errors.New("option `--port` is required when proxy is used and `--deploymentType` is \"hybrid\" or \"cloudhub\" and `--referencesUserDomain` is not set")
	case proxy && o.Path == nil:
		return errors.New("option `--path` is required when proxy is used")
	case is(o.IsFlex) && dt != "" && dt != "hybrid":
		return errors.New("FlexGateway technology should be hybrid and have a proxy")
	case is(o.IsFlex) && is(o.Mule4):
		return errors.New("FlexGateway technology should be hybrid and should not be of technology Mule")
	case is(o.IsFlex) && sv(o.Type) == "wsdl":
		return errors.New("FlexGateway technology does not support wsdl")
	case sv(o.Type) == "wsdl" && is(o.Mule4) && proxy && (sv(o.ServiceName) == "" || sv(o.ServiceNamespace) == "" || sv(o.ServicePort) == ""):
		return errors.New("options --serviceName, --serviceNamespace, --servicePort are required when --type=wsdl, --muleVersion4OrAbove and --withProxy")
	}
	return o.checkTLS()
}

func cmdAPIManage(x *Ctx, args []string) error {
	fs := x.Flags()
	opts := apiOptFlags(fs)
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	o, err := opts()
	if err != nil {
		return err
	}
	if err := o.validateManage(); err != nil {
		return err
	}
	o.prepare(true)
	group := arg(pos, 2)
	if group == "" {
		group = org
	}
	body, err := o.body(pos[0], pos[1], group)
	if err != nil {
		return err
	}
	a, err := c.CreateAPIInstance(x.ctx, org, env, body)
	if err != nil {
		return err
	}
	return x.Print(a, func() { fmt.Fprintf(x.Out, "Created new API with ID: %d\n", a.ID) })
}

// fillFrom completes the options from the current instance, as
// anypoint-cli-v4's api edit does.
func (o *apiOpts) fillFrom(a *anypoint.APIInstance) error {
	e := a.Endpoint
	if e == nil {
		e = &anypoint.APIEndpoint{}
	}
	if o.RefUserDomain == nil {
		o.RefUserDomain = e.ReferencesUserDomain
	}
	if o.Mule4 == nil {
		o.Mule4 = e.MuleVersion4OrAbove
	}
	if o.DeploymentType == nil && e.DeploymentType != "" {
		o.DeploymentType = new(e.DeploymentType)
	}
	if o.Type == nil && e.Type != "" {
		o.Type = new(e.Type)
	}
	if o.URI == nil {
		o.URI = e.URI
	}
	if o.ResponseTimeout == nil {
		o.ResponseTimeout = e.ResponseTimeout
	}
	if o.WithProxy == nil {
		o.WithProxy = new(e.ProxyURI != nil)
	}
	if o.Label == nil {
		o.Label = new(a.InstanceLabel)
	}
	if w := e.WSDLConfig; w != nil {
		if o.ServiceName == nil && w.Name != "" {
			o.ServiceName = new(w.Name)
		}
		if o.ServiceNamespace == nil && w.Namespace != "" {
			o.ServiceNamespace = new(w.Namespace)
		}
		if o.ServicePort == nil && w.Port != "" {
			o.ServicePort = new(w.Port)
		}
	}
	if e.ProxyURI != nil {
		if u, err := url.Parse(*e.ProxyURI); err == nil {
			if o.Scheme == nil && u.Scheme != "" {
				o.Scheme = new(u.Scheme)
			}
			if o.Port == nil && u.Port() != "" {
				o.Port = new(u.Port())
			}
			if o.Path == nil {
				p := u.Path
				if u.RawQuery != "" {
					p += "?" + u.RawQuery
				}
				o.Path = new(p)
			}
		}
	}
	if is(o.WithProxy) && o.Scheme == nil {
		return errors.New("option --scheme is required when proxy is used")
	}
	return nil
}

func cmdAPIEdit(x *Ctx, args []string) error {
	fs := x.Flags()
	opts := apiOptFlags(fs)
	samePort := fs.Bool("updateApisInSamePort", false, "also update the APIs that share the proxy's port")
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	o, err := opts()
	if err != nil {
		return err
	}
	cur, err := c.GetAPIInstance(x.ctx, org, env, pos[0], false)
	if err != nil {
		return notFoundAs(err, fmt.Sprintf("API with ID %q not found", pos[0]))
	}
	if err := o.fillFrom(cur); err != nil {
		return err
	}
	if err := o.checkTLS(); err != nil {
		return err
	}
	o.prepare(false)
	body, err := o.body("", "", "")
	if err != nil {
		return err
	}
	delete(body, "spec")
	if cur.Technology == "flexGateway" && cur.Endpoint != nil && cur.Endpoint.DeploymentType != sv(o.DeploymentType) {
		return errors.New("option deploymentType cannot be changed for Flex Gateway APIs")
	}
	var q url.Values
	if *samePort {
		q = url.Values{"updateApisInSamePort": {"true"}}
	}
	a, err := c.UpdateAPIInstance(x.ctx, org, env, pos[0], body, q)
	if err != nil {
		return err
	}
	return x.Print(a, func() { fmt.Fprintf(x.Out, "Edited API with ID: %d\n", a.ID) })
}

// ---------------------------------------------------------------- small ones

func cmdAPIDelete(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := c.DeleteAPIInstance(x.ctx, org, env, pos[0]); err != nil {
		return notFoundAs(err, fmt.Sprintf("API with ID %q not found", pos[0]))
	}
	return x.Done("API deleted")
}

func cmdAPIDeprecate(deprecate bool) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		pos, c, org, env, err := apiSetup(x, x.Flags(), args)
		if err != nil {
			return err
		}
		if _, err := c.UpdateAPIInstance(x.ctx, org, env, pos[0], anypoint.Doc{"deprecated": deprecate}, nil); err != nil {
			return notFoundAs(err, fmt.Sprintf("API with ID %q not found", pos[0]))
		}
		verb := "deprecated"
		if !deprecate {
			verb = "undeprecated"
		}
		return x.Done("API instance with ID %q %s", pos[0], verb)
	}
}

func cmdAPIChangeSpec(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if _, err := c.UpdateAPIInstance(x.ctx, org, env, pos[0], anypoint.Doc{"assetVersion": pos[1]}, nil); err != nil {
		return err
	}
	return x.Done("API instance %q asset version changed to %q", pos[0], pos[1])
}

func cmdAPIAutodiscovery(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	props, err := c.GetAutodiscoveryProperties(x.ctx, org, env, pos[0], pos[1])
	if err != nil {
		return err
	}
	return x.Print(props, func() {
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		var kv []string
		for _, k := range keys {
			kv = append(kv, k, fmt.Sprint(props[k]))
		}
		printKV(x.Out, kv...)
	})
}

func cmdAPIDownloadProxy(x *Ctx, args []string) error {
	fs := x.Flags()
	gw := fs.String("gatewayVersion", "", "gateway `version` of the proxy (default 3.9.0)")
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(pos[1], 0o750); err != nil {
		return err
	}
	fmt.Fprintf(x.Err, "Downloading API Proxy to %s\n", pos[1])
	body, name, err := c.DownloadProxy(x.ctx, org, env, pos[0], *gw)
	if err != nil {
		return err
	}
	defer body.Close()
	if name == "" {
		name = pos[0] + "-proxy.jar"
	}
	dst := filepath.Join(pos[1], safeFileName(name))
	if _, err := writeFile(dst, body); err != nil {
		return err
	}
	return x.Done("API Proxy downloaded to %s", dst)
}

func cmdAPIClassify(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	destName, apiID := pos[0], pos[1]
	a, err := c.GetAPIInstance(x.ctx, org, env, apiID, false)
	if err != nil {
		return notFoundAs(err, fmt.Sprintf("API with ID %q not found in environment ID %q", apiID, env))
	}
	page, err := c.ListAPIInstances(x.ctx, org, env, anypoint.APIListOptions{GroupID: a.GroupID, AssetID: a.AssetID, AssetVersion: a.AssetVersion})
	if err != nil {
		return err
	}
	if page.Total < 1 || len(page.Assets) == 0 {
		return fmt.Errorf("API asset identified by GAV %q not found", a.GroupID+"/"+a.AssetID+"/"+a.AssetVersion)
	}
	dest, err := findEnv(x, c, destName)
	if err != nil {
		return err
	}
	if err := c.ClassifyAPI(x.ctx, org, page.Assets[0].ID.String(), apiID, dest.ID); err != nil {
		return err
	}
	return x.Done("API with ID %q classified in environment %q", apiID, destName)
}

func cmdAPIPromote(x *Ctx, args []string) error {
	fs := x.Flags()
	copyFlag := func(name, short, what string) *string {
		v := fs.String(name, "true", "copy the source's "+what+": true or false")
		aliasFlag(fs, short, name)
		return v
	}
	alerts, policies, tiers := copyFlag("copyAlerts", "a", "alerts"), copyFlag("copyPolicies", "p", "policies"), copyFlag("copyTiers", "t", "SLA tiers")
	providerID := fs.String("providerId", "", "client provider `ID` of the promoted instance")
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	for name, v := range map[string]string{"copyAlerts": *alerts, "copyPolicies": *policies, "copyTiers": *tiers} {
		if err := oneOf(name, v, "true", "false"); err != nil {
			return err
		}
	}
	apiID, source := pos[0], pos[1]
	if source == env {
		return fmt.Errorf("the source environment cannot be equal to the target environment %q", env)
	}
	src, err := c.GetAPIInstance(x.ctx, org, source, apiID, false)
	if err != nil {
		return notFoundAs(err, fmt.Sprintf("API with ID %q not found", apiID))
	}
	body := anypoint.Doc{
		"instanceLabel": strings.TrimSpace(src.InstanceLabel + " (Promoted)"),
		"promote": anypoint.Doc{"originApiId": src.ID,
			"alerts": anypoint.Doc{"allEntities": *alerts == "true"}, "policies": anypoint.Doc{"allEntities": *policies == "true"},
			"tiers": anypoint.Doc{"allEntities": *tiers == "true"}},
	}
	if *providerID != "" {
		body["providerId"] = *providerID
	}
	a, err := c.CreateAPIInstance(x.ctx, org, env, body)
	if err != nil {
		return err
	}
	target := env
	if e, err := findEnv(x, c, env); err == nil {
		target = e.Name
	}
	return x.Print(a, func() { fmt.Fprintf(x.Out, "API with ID %q promoted from %q to %q\n", apiID, source, target) })
}

// ---------------------------------------------------------------- deploy / redeploy

// deployEnv returns the environment of the API instance's path and the
// environment recorded in the deployment. With --environmentName the API
// lives in the "unclassified" environment and is deployed to the named one.
func deployEnv(x *Ctx, c *anypoint.Client, env, envName string) (apiEnv string, target *anypoint.Environment, err error) {
	if envName == "" {
		target, err = findEnv(x, c, env)
		return env, target, err
	}
	target, err = findEnv(x, c, envName)
	return "unclassified", target, err
}

// hybridTarget looks a hybrid target up, as a Flex Gateway or Mule target.
func hybridTarget(x *Ctx, c *anypoint.Client, org, env, targetID string, flex bool) (*anypoint.DeploymentTarget, error) {
	if flex {
		t, err := c.GetGatewayTarget(x.ctx, org, env, targetID)
		return t, notFoundAs(err, fmt.Sprintf("Deployment target with ID %q not found", targetID))
	}
	targets, err := c.ListDeploymentTargets(x.ctx, org, env)
	if err != nil {
		return nil, err
	}
	for i := range targets {
		if targets[i].ID.String() == targetID {
			return &targets[i], nil
		}
	}
	return nil, fmt.Errorf("deployment target with ID %q not found", targetID)
}

func gatewayVersionOf(t *anypoint.DeploymentTarget, flex bool) string {
	if flex && t.GatewayVersion == "" {
		return "1.0.0"
	}
	return t.GatewayVersion
}

// runtimeVersion checks a Runtime Fabric / CloudHub 2.0 target offers a version.
func runtimeVersion(x *Ctx, c *anypoint.Client, org, env, targetID, version string) error {
	t, err := c.GetRuntimeTarget(x.ctx, org, env, targetID)
	if err != nil {
		return err
	}
	if !t.HasRuntimeVersion(version) {
		return fmt.Errorf("Runtime Fabric with ID %q does not have version %q", targetID, version) //nolint:staticcheck // v4's message
	}
	return nil
}

func runtimeTargetDoc(targetID, version, java, channel string) anypoint.Doc {
	runtime := anypoint.Doc{}
	if java != "" {
		runtime["java"] = java
	}
	if channel != "" {
		runtime["releaseChannel"] = channel
	}
	if java != "" || channel != "" {
		runtime["version"] = version
	}
	return anypoint.Doc{"targetId": targetID, "deploymentSettings": anypoint.Doc{"runtimeVersion": version, "runtime": runtime}}
}

func cmdAPIDeploy(redeploy bool) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		fs := x.Flags()
		target := fs.String("target", "", "hybrid, Runtime Fabric or CloudHub 2.0 target `ID`")
		appName := fs.String("applicationName", "", "proxy application `name`")
		envName := fs.String("environmentName", "", "deploy an unclassified API to this `environment`")
		gw := fs.String("gatewayVersion", "", "gateway (runtime) `version`")
		overwrite := fs.Bool("overwrite", false, "overwrite an existing CloudHub application")
		java := fs.String("javaVersion", "", "Java `version`")
		channel := fs.String("releaseChannel", "", "runtime release `channel`")
		pos, c, org, env, err := apiSetup(x, fs, args)
		if err != nil {
			return err
		}
		apiID := pos[0]
		apiEnv, tgtEnv, err := deployEnv(x, c, env, *envName)
		if err != nil {
			return err
		}
		a, err := c.GetAPIInstance(x.ctx, org, apiEnv, apiID, false)
		if err != nil {
			return notFoundAs(err, fmt.Sprintf("API with ID %q not found in environment ID %q", apiID, apiEnv))
		}
		flex := a.Technology == "flexGateway"
		if !redeploy {
			dt := ""
			if a.Endpoint != nil {
				dt = a.Endpoint.DeploymentType
			}
			var body anypoint.Doc
			switch {
			case isCloudHub(dt):
				if err := need("applicationName", *appName, "gatewayVersion", *gw); err != nil {
					return err
				}
				body = anypoint.Doc{"applicationName": *appName, "gatewayVersion": *gw, "overwrite": *overwrite, "type": "CH",
					"environmentId": tgtEnv.ID, "environmentName": tgtEnv.Name}
				if *java != "" {
					body["javaVersion"] = *java
				}
				if *channel != "" {
					body["releaseChannel"] = *channel
				}
			case isHybrid(dt):
				if err := need("target", *target); err != nil {
					return err
				}
				t, err := hybridTarget(x, c, org, apiEnv, *target, flex)
				if err != nil {
					return err
				}
				body = anypoint.Doc{"type": "HY", "gatewayVersion": gatewayVersionOf(t, flex), "targetId": *target,
					"targetName": t.Name, "targetType": t.Type, "environmentId": tgtEnv.ID, "environmentName": tgtEnv.Name}
			case isRTF(dt), isCH2(dt):
				if err := need("target", *target, "gatewayVersion", *gw); err != nil {
					return err
				}
				if err := runtimeVersion(x, c, org, apiEnv, *target, *gw); err != nil {
					return err
				}
				typ := "RF"
				if isCH2(dt) {
					typ = "CH2"
				}
				body = anypoint.Doc{"name": *appName, "gatewayVersion": *gw, "targetId": *target, "type": typ,
					"target": runtimeTargetDoc(*target, *gw, *java, *channel)}
			default:
				return fmt.Errorf("API with ID %q has an unknown deployment type %q", apiID, dt)
			}
			if err := c.CreateAPIDeployment(x.ctx, org, apiEnv, apiID, body); err != nil {
				return err
			}
			return x.Done("Deployed API with ID %q", apiID)
		}

		deployments, err := c.ListAPIDeployments(x.ctx, org, apiEnv, apiID)
		if err != nil || len(deployments) == 0 {
			if err == nil || anypoint.IsNotFound(err) {
				return fmt.Errorf("environment/API with ID %q is not deployed", apiEnv+"/"+apiID)
			}
			return err
		}
		body := deployments[0]
		depID := fmt.Sprint(body["id"])
		if f, ok := body["id"].(float64); ok {
			depID = strconv.FormatFloat(f, 'f', -1, 64)
		}
		typ, _ := body["type"].(string)
		drop := func(keys ...string) {
			for _, k := range keys {
				delete(body, k)
			}
		}
		moveEnv := func() {
			if *envName != "" {
				body["apiVersionId"] = body["apiId"]
				delete(body, "apiId")
				body["environmentName"], body["environmentId"] = tgtEnv.Name, tgtEnv.ID
			}
		}
		set := setFlags(fs)
		switch {
		case isCloudHub(typ):
			drop("masterOrganizationId", "organizationId", "remoteSystemId", "targetType")
			for k, v := range map[string]string{"applicationName": *appName, "gatewayVersion": *gw, "javaVersion": *java, "releaseChannel": *channel} {
				if v != "" {
					body[k] = v
				}
			}
			if set["overwrite"] {
				body["overwrite"] = *overwrite
			}
			moveEnv()
		case isHybrid(typ):
			drop("masterOrganizationId", "organizationId", "remoteSystemId")
			if !flex {
				if s, ok := body["targetId"].(string); ok {
					if n, err := strconv.Atoi(s); err == nil {
						body["targetId"] = n
					}
				}
			}
			moveEnv()
			if *target != "" {
				t, err := hybridTarget(x, c, org, apiEnv, *target, flex)
				if err != nil {
					return err
				}
				body["gatewayVersion"], body["targetId"], body["targetName"], body["targetType"] = gatewayVersionOf(t, flex), *target, t.Name, t.Type
			}
		case isRTF(typ), isCH2(typ):
			name := body["applicationName"]
			drop("masterOrganizationId", "organizationId", "remoteSystemId", "targetType", "applicationName")
			body["name"] = name
			if *target != "" || *gw != "" {
				tgt, ver := *target, *gw
				if tgt == "" {
					tgt = fmt.Sprint(body["targetId"])
				}
				if ver == "" {
					ver = fmt.Sprint(body["gatewayVersion"])
				}
				if err := runtimeVersion(x, c, org, apiEnv, tgt, ver); err != nil {
					return err
				}
				j, ch := *java, *channel
				if j == "" {
					j, _ = body["javaVersion"].(string)
				}
				if ch == "" {
					ch, _ = body["releaseChannel"].(string)
				}
				body["targetId"], body["gatewayVersion"], body["target"] = tgt, ver, runtimeTargetDoc(tgt, ver, j, ch)
			}
		}
		if body["expectedStatus"] == "undeployed" {
			body["expectedStatus"] = "deployed"
		}
		if err := c.UpdateAPIDeployment(x.ctx, org, apiEnv, apiID, depID, body); err != nil {
			return err
		}
		return x.Done("Redeployed API with ID %q", apiID)
	}
}
