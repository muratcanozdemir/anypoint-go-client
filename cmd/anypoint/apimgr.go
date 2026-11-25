package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/muratcanozdemir/anypoint-go-client/anypoint"
	"github.com/muratcanozdemir/anypoint-go-client/internal/yamlx"
)

// ---------------------------------------------------------------- policies

var (
	jwtHeadersRE  = regexp.MustCompile(`attributes.headers\[([^\]]+)]`)
	customKeyRE   = regexp.MustCompile(`#\[([^\[]+)\[([^\]]+)]]`)
	inboundFields = map[string]bool{"methodRegex": true, "headers": true, "host": true, "uriTemplateRegex": true}
)

// policyConfig reads --config or --configFile as anypoint-cli-v4 does,
// quoting header names in JWT expressions.
func policyConfig(config, configFile string) (anypoint.Doc, error) {
	if config != "" && configFile != "" {
		return nil, errors.New("the parameters --configFile and --config cannot be used simultaneously")
	}
	if configFile != "" {
		if !strings.EqualFold(filepath.Ext(configFile), ".json") {
			return nil, errors.New("configuration file must be a JSON file")
		}
		b, err := os.ReadFile(expandHome(configFile))
		if err != nil {
			return nil, fmt.Errorf("there was an error loading the JSON file: %w", err)
		}
		var d anypoint.Doc
		if err := json.Unmarshal(b, &d); err != nil {
			return nil, fmt.Errorf("there was an error loading the JSON file: %w", err)
		}
		if d == nil {
			d = anypoint.Doc{}
		}
		return d, nil
	}
	if config == "" {
		config = "{}"
	}
	var d anypoint.Doc
	if err := json.Unmarshal([]byte(config), &d); err != nil {
		return nil, fmt.Errorf("--config must be a JSON object: %w", err)
	}
	if jwt, ok := d["jwtExpression"].(string); ok && jwtHeadersRE.MatchString(jwt) {
		d["jwtExpression"] = jwtHeadersRE.ReplaceAllString(jwt, "attributes.headers['$1']")
		if ck, ok := d["customKeyExpression"].(string); ok && customKeyRE.MatchString(ck) {
			d["customKeyExpression"] = customKeyRE.ReplaceAllString(ck, "#[$1['$2']]")
		}
	}
	return d, nil
}

// pointcut parses --pointcut and checks its fields (outbound policies only
// support methodRegex and headers).
func pointcut(s string, outbound bool) (any, error) {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, fmt.Errorf("--pointcut must be JSON: %w", err)
	}
	entries, ok := v.([]any)
	if !ok {
		entries = []any{v}
	}
	for _, e := range entries {
		m, _ := e.(map[string]any)
		var bad []string
		for k := range m {
			if !inboundFields[k] || (outbound && (k == "host" || k == "uriTemplateRegex")) {
				bad = append(bad, k)
			}
		}
		if len(bad) > 0 {
			sort.Strings(bad)
			kind, allowed := "Inbound", `"methodRegex", "headers", "host" and "uriTemplateRegex"`
			if outbound {
				kind, allowed = "Outbound", `"methodRegex" and "headers"`
			}
			return nil, fmt.Errorf("%s policies only support %s in pointcut. Invalid fields: %s", kind, allowed, strings.Join(bad, ", "))
		}
	}
	return v, nil
}

func cmdPolicyApply(x *Ctx, args []string) error {
	fs := x.Flags()
	version := fs.String("policyVersion", "", "Mule 4 policy asset `version` (POLICYID is then the asset ID)")
	groupID := fs.String("groupId", "", "Mule 4 policy asset group `ID` (default: MuleSoft's)")
	config := fs.String("config", "", "policy configuration as a JSON `object`")
	aliasFlag(fs, "c", "config")
	configFile := fs.String("configFile", "", "policy configuration JSON `file`")
	pc := fs.String("pointcut", "", `JSON pointcut, e.g. [{"methodRegex":"GET|PUT","uriTemplateRegex":"/users*"}]`)
	upstreamID := fs.String("upstreamId", "", "apply as an outbound policy of this upstream `ID`")
	label := fs.String("label", "", "policy `label`")
	allowDup := fs.Bool("allowDuplicated", false, "allow applying the same policy more than once")
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	apiID, policyID := pos[0], pos[1]
	conf, err := policyConfig(*config, *configFile)
	if err != nil {
		return err
	}
	body := anypoint.Doc{"apiVersionId": apiID, "configurationData": conf, "id": nil, "pointcutData": nil, "policyTemplateId": policyID}
	if *pc != "" {
		if body["pointcutData"], err = pointcut(*pc, *upstreamID != ""); err != nil {
			return err
		}
	}
	if *upstreamID != "" {
		body["upstreamId"] = *upstreamID
	}
	if *label != "" {
		body["label"] = *label
	}
	if *version != "" {
		g := *groupID
		if g == "" {
			g = anypoint.MuleSoftGroupID
		}
		body["assetId"], body["groupId"], body["assetVersion"] = policyID, g, *version
	}
	p, err := c.ApplyAPIPolicy(x.ctx, org, env, apiID, body, *allowDup)
	if err != nil {
		var he *anypoint.HTTPError
		if errors.As(err, &he) && strings.Contains(he.Error(), `"route" could not be found using "request path"`) {
			return errors.New("an incorrect API Instance ID was provided")
		}
		return err
	}
	return x.Print(p, func() { fmt.Fprintf(x.Out, "Policy applied. New policy ID: %q\n", p.ID) })
}

func cmdPolicyEdit(x *Ctx, args []string) error {
	fs := x.Flags()
	config := fs.String("config", "", "new configuration as a JSON `object`")
	aliasFlag(fs, "c", "config")
	configFile := fs.String("configFile", "", "new configuration JSON `file`")
	pc := fs.String("pointcut", "", "new JSON pointcut")
	aliasFlag(fs, "p", "pointcut")
	label := fs.String("label", "", "new `label` (\"null\" removes it)")
	allowDup := fs.Bool("allowDuplicated", false, "allow the same policy more than once")
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	apiID, policyID := pos[0], pos[1]
	cur, err := c.GetAPIPolicy(x.ctx, org, env, apiID, policyID)
	if err != nil {
		if anypoint.IsNotFound(err) {
			return fmt.Errorf("policy with ID %q not found in API ID %q", policyID, apiID)
		}
		return err
	}
	var all anypoint.Doc
	if err := json.Unmarshal(cur.RawJSON(), &all); err != nil {
		return err
	}
	body := anypoint.Doc{"apiVersionId": all["apiId"]}
	for _, k := range []string{"configurationData", "id", "pointcutData", "policyTemplateId", "groupId", "assetId", "assetVersion", "label"} {
		if v, ok := all[k]; ok {
			body[k] = v
		}
	}
	if s, ok := body["policyTemplateId"].(string); ok {
		if n, err := strconv.Atoi(s); err == nil {
			body["policyTemplateId"] = n
		}
	}
	if *config != "" || *configFile != "" {
		if body["configurationData"], err = policyConfig(*config, *configFile); err != nil {
			return err
		}
	}
	if *pc != "" {
		if body["pointcutData"], err = pointcut(*pc, cur.UpstreamID != "" || len(cur.UpstreamIDs) > 0); err != nil {
			return err
		}
	}
	switch *label {
	case "":
	case "null":
		body["label"] = nil
	default:
		body["label"] = *label
	}
	if err := c.UpdateAPIPolicy(x.ctx, org, env, apiID, policyID, body, *allowDup); err != nil {
		return notFoundAs(err, fmt.Sprintf("API instance %s not found", apiID))
	}
	return x.Done("Policy with ID %q edited.", policyID)
}

func cmdPolicyDisable(disable bool) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		pos, c, org, env, err := apiSetup(x, x.Flags(), args)
		if err != nil {
			return err
		}
		if err := c.SetAPIPolicyDisabled(x.ctx, org, env, pos[0], pos[1], disable); err != nil {
			return err
		}
		verb := "disabled"
		if !disable {
			verb = "enabled"
		}
		return x.Done("Policy with ID %q %s in API with ID %q.", pos[1], verb, pos[0])
	}
}

func cmdPolicyRemove(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := c.RemoveAPIPolicy(x.ctx, org, env, pos[0], pos[1]); err != nil {
		return notFoundAs(err, "API instance or applied policy not found")
	}
	return x.Done("Policy with ID %q removed from API with ID %q", pos[1], pos[0])
}

// policyConfigText renders an applied policy's configuration without the
// properties API Manager adds to every policy.
func policyConfigText(conf anypoint.Doc) string {
	common := map[string]bool{"policyId": true, "endpointUri": true, "isRamlEndpoint": true, "isWsdlEndpoint": true,
		"isHttpEndpoint": true, "apiId": true, "order": true, "autodiscoveryApiName": true, "groupId": true, "autodiscoveryInstanceName": true}
	keys := make([]string, 0, len(conf))
	for k := range conf {
		if !common[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var lines []string
	for _, k := range keys {
		v := conf[k]
		s, ok := v.(string)
		switch {
		case ok && s == "":
			s = "[Empty]"
		case !ok:
			b, _ := json.Marshal(v)
			s = string(b)
		}
		lines = append(lines, k+": "+s)
	}
	return strings.Join(lines, "; ")
}

func cmdPolicyList(x *Ctx, args []string) error {
	fs := x.Flags()
	mule4 := fs.Bool("muleVersion4OrAbove", false, "list the Mule 4 policies of Exchange instead of the Mule 3 templates")
	aliasFlag(fs, "m", "muleVersion4OrAbove")
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	switch {
	case arg(pos, 0) != "":
		policies, err := c.ListAPIPolicies(x.ctx, org, env, pos[0])
		if err != nil {
			return notFoundAs(err, fmt.Sprintf("API instance %s or applied policy not found", pos[0]))
		}
		return x.Print(policies, func() {
			t := newTable(x.Out, "ID", "TEMPLATE ID", "ASSET ID", "ASSET VERSION", "LABEL", "STATUS", "CONFIGURATION", "UPDATED")
			for _, p := range policies {
				status := "Enabled"
				if p.Disabled {
					status = "Disabled"
				}
				t.row(p.ID.String(), p.PolicyTemplateID.String(), p.AssetID, p.AssetVersion, p.Label, status,
					policyConfigText(p.ConfigurationData), ago(p.Audit.Changed()))
			}
			t.flush()
		})
	case *mule4:
		assets, err := c.ListPolicyAssets(x.ctx)
		if err != nil {
			return err
		}
		return x.Print(assets, func() {
			t := newTable(x.Out, "GROUP ID", "ASSET ID", "VERSION", "NAME", "DESCRIPTION")
			for _, a := range assets {
				t.row(a.GroupID, a.AssetID, a.Version, a.Name, a.Description)
			}
			t.flush()
		})
	}
	templates, err := c.ListPolicyTemplates(x.ctx, org)
	if err != nil {
		return err
	}
	return x.Print(templates, func() {
		t := newTable(x.Out, "ID", "DESCRIPTION", "CATEGORY", "CONFIGURATION")
		for _, p := range templates {
			var conf []string
			for _, ce := range p.Configuration {
				conf = append(conf, ce.PropertyName+": "+ce.Type)
			}
			t.row(p.ID.String(), p.Description, p.Category, strings.Join(conf, "; "))
		}
		t.flush()
	})
}

// policyInfo is what policy describe shows.
type policyInfo struct {
	Name          string                         `json:"name"`
	Category      string                         `json:"category,omitempty"`
	Description   string                         `json:"description,omitempty"`
	Configuration []anypoint.PolicyConfiguration `json:"configuration,omitempty"`
}

// policyFromYAML reads a policy definition.
func policyFromYAML(src []byte) (*policyInfo, error) {
	m, err := yamlx.Map(src)
	if err != nil {
		return nil, fmt.Errorf("policy definition YAML: %w", err)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var p policyInfo
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("policy definition: %w", err)
	}
	return &p, nil
}

func cmdPolicyDescribe(x *Ctx, args []string) error {
	fs := x.Flags()
	version := fs.String("policyVersion", "", "Mule 4 policy asset `version` (POLICYID is then the asset ID)")
	groupID := fs.String("groupId", "", "Mule 4 policy asset group `ID` (default: MuleSoft's)")
	pos, c, org, err := orgSetup(x, fs, args)
	if err != nil {
		return err
	}
	var info *policyInfo
	if *version != "" {
		a, err := c.GetPolicyAsset(x.ctx, *groupID, pos[0], *version)
		if err != nil {
			return err
		}
		info = &policyInfo{Name: a.Name, Description: a.Description}
		for _, f := range a.Files {
			if f.Classifier == "policy-definition" && f.Link() != "" {
				body, err := fetch(x, c, f.Link())
				if err != nil {
					return err
				}
				src, err := readAllClose(body)
				if err != nil {
					return err
				}
				if info, err = policyFromYAML(src); err != nil {
					return err
				}
				break
			}
		}
	} else {
		t, err := c.GetPolicyTemplate(x.ctx, org, pos[0])
		if err != nil {
			return notFoundAs(err, fmt.Sprintf("Policy %s was not found", pos[0]))
		}
		if t.Definition != "" {
			if info, err = policyFromYAML([]byte(t.Definition)); err != nil {
				return err
			}
		} else {
			info = &policyInfo{Name: t.Name, Category: t.Category, Description: t.Description, Configuration: t.Configuration}
		}
	}
	return x.Print(info, func() {
		kv := []string{"Name", info.Name, "Category", info.Category, "Description", strings.TrimSpace(info.Description)}
		if len(info.Configuration) == 0 {
			kv = append(kv, "Config", "This policy does not require any configuration.")
		} else {
			kv = append(kv, "Config", "Configuration Properties")
			kv = policyConfigRows(kv, info.Configuration, "")
		}
		printKV(x.Out, kv...)
	})
}

func policyConfigRows(kv []string, confs []anypoint.PolicyConfiguration, nestedUnder string) []string {
	for _, ce := range confs {
		var parts []string
		desc := ce.Description
		if desc == "" {
			desc = ce.Name
		}
		if desc != "" {
			parts = append(parts, "Description: "+strings.TrimSpace(desc))
		}
		parts = append(parts, "Required: "+strconv.FormatBool(!ce.Optional))
		typ := ce.Type
		if ce.AllowMultiple {
			typ = "Array of " + typ
		}
		parts = append(parts, "Type: "+typ)
		if len(ce.Options) > 0 {
			var opts []string
			for _, o := range ce.Options {
				opts = append(opts, fmt.Sprint(o.Value))
			}
			parts = append(parts, "Options: "+strings.Join(opts, ", "))
		}
		if ce.DefaultValue != nil && ce.DefaultValue != "" && ce.DefaultValue != false {
			b, _ := json.Marshal(ce.DefaultValue)
			parts = append(parts, "Default: "+string(b))
		}
		if ce.DependsOnKey != "" {
			dep := "Depends on: " + ce.DependsOnKey
			if ce.DependsOnValue != nil && ce.DependsOnValue != "" {
				dep += "=" + fmt.Sprint(ce.DependsOnValue)
			}
			parts = append(parts, dep)
		}
		if nestedUnder != "" {
			parts = append(parts, "Nested under: "+nestedUnder)
		}
		kv = append(kv, ce.PropertyName, strings.Join(parts, "; "))
		if len(ce.Configuration) > 0 {
			kv = policyConfigRows(kv, ce.Configuration, ce.PropertyName)
		}
	}
	return kv
}

func cmdPolicyOrder(x *Ctx, args []string) error {
	fs := x.Flags()
	comma := fs.Bool("commaSeparated", false, "ORDER is a comma-separated list of policy IDs instead of JSON")
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	apiID := pos[0]
	policies, err := c.ListAPIPolicies(x.ctx, org, env, apiID)
	if err != nil {
		return notFoundAs(err, fmt.Sprintf("API instance %s or its policies not found", apiID))
	}
	current := []anypoint.PolicyOrder{}
	for _, p := range policies {
		if p.Order != nil {
			current = append(current, anypoint.PolicyOrder{ID: atoiOr(p.ID.String()), Order: *p.Order})
		}
	}
	if arg(pos, 1) == "" {
		if len(policies) == 0 {
			return x.Done("API instance %s has no policies configured", apiID)
		}
		return x.Print(policies, func() {
			t := newTable(x.Out, "ID", "ASSET ID", "ORDER")
			for _, p := range policies {
				o := "-"
				if p.Order != nil {
					o = strconv.Itoa(*p.Order)
				}
				t.row(p.ID.String(), p.AssetID, o)
			}
			t.flush()
			b, _ := json.Marshal(current)
			fmt.Fprintf(x.Out, "\nCopy and edit this to reorder the policies, or pass a comma-separated list of policy IDs with --commaSeparated:\n%s\n", b)
		})
	}
	var order []anypoint.PolicyOrder
	if *comma {
		for i, s := range strings.Split(pos[1], ",") {
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil {
				return fmt.Errorf("cannot parse %s as a policy id", s)
			}
			order = append(order, anypoint.PolicyOrder{ID: n, Order: i + 1})
		}
	} else if err := json.Unmarshal([]byte(pos[1]), &order); err != nil {
		return fmt.Errorf("ORDER must be a JSON array of {\"id\": ..., \"order\": ...}: %w", err)
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].Order < order[j].Order })
	seen, inCurrent := map[int]int{}, map[int]bool{}
	for _, p := range current {
		inCurrent[p.ID] = true
	}
	var dups []string
	for _, p := range order {
		if seen[p.ID]++; seen[p.ID] == 2 {
			dups = append(dups, strconv.Itoa(p.ID))
		}
	}
	if len(dups) > 0 {
		return fmt.Errorf("some policies are repeated in the order received: %s", strings.Join(dups, ","))
	}
	var missing []string
	for _, p := range current {
		if seen[p.ID] == 0 {
			missing = append(missing, strconv.Itoa(p.ID))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("all policies ids must be present when reordering them, missing %s", strings.Join(missing, ","))
	}
	for i := range order {
		if !inCurrent[order[i].ID] {
			return fmt.Errorf("the policy with id %d is not a policy currently set for the API or not able to be updated. API %s", order[i].ID, apiID)
		}
		order[i].Order = i + 1
	}
	if err := c.ReorderAPIPolicies(x.ctx, org, env, apiID, order); err != nil {
		return err
	}
	return x.Done("Policies order for API %s updated", apiID)
}

func atoiOr(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// ---------------------------------------------------------------- tiers

var periodMillis = map[string]int64{"ms": 1, "sec": 1000, "min": 60000, "hr": 3600000, "d": 86400000,
	"wk": 604800000, "mo": 2592000000, "yr": 31536000000}

// parseLimit reads "visible,requests,period", e.g. "true,100,min".
func parseLimit(s string) (anypoint.SLALimit, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 3 {
		return anypoint.SLALimit{}, fmt.Errorf("--limit %q: expected VISIBLE,REQUESTS,PERIOD, e.g. true,100,min", s)
	}
	n, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return anypoint.SLALimit{}, fmt.Errorf("--limit %q: %q is not a number of requests", s, parts[1])
	}
	ms, ok := periodMillis[strings.TrimSpace(parts[2])]
	if !ok {
		return anypoint.SLALimit{}, fmt.Errorf("invalid time period abbreviation: %q. Allowed values are: ms, sec, min, hr, d, wk, mo, yr", parts[2])
	}
	return anypoint.SLALimit{Visible: strings.TrimSpace(parts[0]) == "true", MaximumRequests: n, TimePeriodInMilliseconds: ms}, nil
}

func cmdTierAdd(x *Ctx, args []string) error {
	fs := x.Flags()
	name := fs.String("name", "", "(required) tier `name`")
	desc := fs.String("description", "", "tier `description`")
	limits := multi(fs, "limit", "rate `limit` VISIBLE,REQUESTS,PERIOD (repeatable), e.g. true,100,min; periods: ms, sec, min, hr, d, wk, mo, yr")
	aliasFlag(fs, "l", "limit")
	auto := fs.Bool("autoApprove", false, "approve contracts automatically")
	aliasFlag(fs, "a", "autoApprove")
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	if err := need("name", *name); err != nil {
		return err
	}
	tier := &anypoint.SLATier{Name: *name, Description: *desc, AutoApprove: *auto, Status: "ACTIVE", Limits: []anypoint.SLALimit{}}
	for _, l := range *limits {
		lim, err := parseLimit(l)
		if err != nil {
			return err
		}
		tier.Limits = append(tier.Limits, lim)
	}
	t, err := c.CreateSLATier(x.ctx, org, env, pos[0], tier)
	if err != nil {
		return err
	}
	return x.Print(t, func() { fmt.Fprintf(x.Out, "Created SLA Tier with ID %q\n", strconv.Itoa(t.ID)) })
}

func cmdTierList(x *Ctx, args []string) error {
	fs := x.Flags()
	limit, offset, sortBy := paginationFlags(fs, "id")
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	tiers, err := c.ListSLATiers(x.ctx, org, env, pos[0], anypoint.ListQuery{Limit: *limit, Offset: *offset, Sort: *sortBy, Query: arg(pos, 1), Ascending: true})
	if err != nil {
		return err
	}
	return x.Print(tiers, func() {
		t := newTable(x.Out, "NAME", "ID", "LIMITS", "APPLICATIONS", "STATUS", "APPROVAL", "UPDATED")
		for _, tr := range tiers {
			approval := "Manual"
			if tr.AutoApprove {
				approval = "Auto"
			}
			t.row(tr.Name, strconv.Itoa(tr.ID), strconv.Itoa(len(tr.Limits)), strconv.Itoa(tr.ApplicationCount), tr.Status, approval, ago(tr.Audit.Changed()))
		}
		t.flush()
	})
}

func cmdTierDelete(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := c.DeleteSLATier(x.ctx, org, env, pos[0], pos[1]); err != nil {
		return err
	}
	return x.Done("Deleted SLA tier with ID %q from API with ID %q", pos[1], pos[0])
}

func cmdTierCopy(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	src, dst := pos[0], pos[1]
	srcTiers, err := c.ListSLATiers(x.ctx, org, env, src, anypoint.ListQuery{})
	if err != nil {
		return err
	}
	dstTiers, err := c.ListSLATiers(x.ctx, org, env, dst, anypoint.ListQuery{})
	if err != nil {
		return err
	}
	fmt.Fprintf(x.Err, "Copying tiers from API ID %q to API ID %q\n", src, dst)
	remaining := map[string]*anypoint.SLATier{}
	for i := range dstTiers {
		remaining[dstTiers[i].Name] = &dstTiers[i]
	}
	for _, s := range srcTiers {
		if t, ok := remaining[s.Name]; ok {
			fmt.Fprintf(x.Err, "- Updating target tier %q\n", s.Name)
			t.Description, t.AutoApprove, t.Limits, t.Status = s.Description, s.AutoApprove, s.Limits, s.Status
			if _, err := c.UpdateSLATier(x.ctx, org, env, dst, t); err != nil {
				return err
			}
			delete(remaining, s.Name)
			continue
		}
		fmt.Fprintf(x.Err, "- Copying source tier %q\n", s.Name)
		if _, err := c.CreateSLATier(x.ctx, org, env, dst, &s); err != nil {
			return err
		}
	}
	for _, t := range dstTiers {
		if _, ok := remaining[t.Name]; ok {
			fmt.Fprintf(x.Err, "- Removing target tier %q\n", t.Name)
			if err := c.DeleteSLATier(x.ctx, org, env, dst, strconv.Itoa(t.ID)); err != nil {
				return err
			}
		}
	}
	return x.Done("Tiers copied from API ID %q to API ID %q", src, dst)
}

// ---------------------------------------------------------------- contracts

func cmdContractList(x *Ctx, args []string) error {
	fs := x.Flags()
	limit, offset, sortBy := paginationFlags(fs, "id")
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	contracts, err := c.ListContracts(x.ctx, org, env, pos[0], anypoint.ListQuery{Limit: *limit, Offset: *offset, Sort: *sortBy, Query: arg(pos, 1), Ascending: true})
	if err != nil {
		return err
	}
	return x.Print(contracts, func() {
		t := newTable(x.Out, "APPLICATION", "CLIENT ID", "CURRENT SLA TIER", "REQUESTED SLA TIER", "STATUS", "UPDATED")
		for _, ct := range contracts {
			cur, req := "N/A", "N/A"
			if ct.Tier != nil {
				cur = ct.Tier.Name
			}
			if ct.RequestedTier != nil {
				req = ct.RequestedTier.Name
			}
			t.row(ct.Application.Name, ct.Application.CoreServicesID, cur, req, ct.Status, ago(ct.Audit.Changed()))
		}
		t.flush()
	})
}

func cmdContractDelete(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	ct, err := c.GetContractByClientID(x.ctx, org, env, pos[0], pos[1])
	if err != nil {
		return notFoundAs(err, fmt.Sprintf("Contract with Client ID %s not found in API with ID %q", pos[1], pos[0]))
	}
	if err := c.DeleteContract(x.ctx, org, env, pos[0], ct.ID); err != nil {
		return err
	}
	return x.Done("Contract deleted from API instance with ID %q", pos[0])
}

// ---------------------------------------------------------------- alerts

func cmdAlertList(legacy bool) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		fs := x.Flags()
		limit, offset, sortBy := paginationFlags(fs, "id")
		pos, c, org, env, err := apiSetup(x, fs, args)
		if err != nil {
			return err
		}
		q := anypoint.ListQuery{Limit: *limit, Offset: *offset, Sort: *sortBy, Ascending: true}
		list := c.ListAPIAlerts
		if legacy {
			list = c.ListAPIAlertsLegacy
		}
		alerts, err := list(x.ctx, org, env, pos[0], q)
		if err != nil {
			return err
		}
		return x.Print(alerts, func() {
			t := newTable(x.Out, "ID", "NAME", "TYPE", "UPDATED", "ENABLED", "SEVERITY", "RECIPIENTS")
			for _, a := range alerts {
				changed := a.LastModified
				if changed == "" {
					changed = a.CreatedAt
				}
				if legacy {
					t.row(a.ID.String(), a.Name, a.Type, ago(changed), yesNo(a.Enabled), a.Severity, strconv.Itoa(len(a.Recipients)))
					continue
				}
				recipients := map[string]bool{}
				for _, n := range a.Notifications {
					for _, r := range n.Recipients {
						recipients[r] = true
					}
				}
				t.row(a.AlertID, a.Name, a.MetricType, ago(changed), yesNo(a.State != "paused"), a.Severity, strconv.Itoa(len(recipients)))
			}
			t.flush()
		})
	}
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

// Default notification of monitoring alerts; the ${...} placeholders are
// filled in by Anypoint Monitoring.
const (
	alertSubject = "${severity}: ${api} ${condition}"
	alertMessage = "The API ${api} has ${condition} of ${value} at ${timestamp}.\n" +
		"Threshold: ${condition} ${operator} ${threshold} for ${period}.\n\n" +
		"Environment: ${environment}\n${dashboardLink}\n${apiLink}"
)

func cmdAlertAdd(legacy bool) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		fs := x.Flags()
		severities, operators := []string{"info", "warning", "critical"}, []string{"eq", "ab", "abeq", "bleq", "bl"}
		if legacy {
			severities, operators = []string{"Info", "Warning", "Critical"}, []string{"gt", "lt", "eq"}
		}
		types := []string{"request-count", "response-code", "policy-violation", "response-time"}
		severity := fs.String("severity", "", "(required) `severity`: "+strings.Join(severities, ", "))
		typ := fs.String("type", "", "(required) alert `type`: "+strings.Join(types, ", "))
		operator := fs.String("operator", "", "(required) comparison `operator`: "+strings.Join(operators, ", "))
		threshold := fs.String("threshold", "", "(required) threshold `value`")
		periods := fs.String("periods", "", "(required) number of consecutive `periods`")
		recipients := multi(fs, "recipient", "`username` to notify (repeatable)")
		emails := multi(fs, "email", "`address` to notify (repeatable)")
		responseTime := fs.String("responseTime", "", "response time in `ms` (response-time alerts)")
		responseCodes := multi(fs, "responseCode", "response `code` (repeatable; response-code alerts)")
		policyID := fs.String("policyId", "", "applied policy `ID` (policy-violation alerts)")
		var subject, message, duration, durationUnit *string
		var enabled *bool
		if legacy {
			enabled = fs.Bool("enabled", false, "enable the alert")
			duration = fs.String("duration", "", "(required) period `length`")
			durationUnit = fs.String("durationUnit", "", "(required) period `unit`: days, hours, minutes")
		} else {
			subject = fs.String("subject", "", "notification `subject` (default: a template)")
			message = fs.String("message", "", "notification `message` (default: a template)")
		}
		pos, c, org, env, err := apiSetup(x, fs, args)
		if err != nil {
			return err
		}
		apiID, name := pos[0], pos[1]
		if err := need("severity", *severity, "type", *typ, "operator", *operator, "threshold", *threshold, "periods", *periods); err != nil {
			return err
		}
		for _, chk := range [][]string{append([]string{"severity", *severity}, severities...), append([]string{"type", *typ}, types...),
			append([]string{"operator", *operator}, operators...)} {
			if err := oneOf(chk[0], chk[1], chk[2:]...); err != nil {
				return err
			}
		}
		thr, err1 := strconv.ParseFloat(*threshold, 64)
		per, err2 := strconv.ParseFloat(*periods, 64)
		if err1 != nil || err2 != nil {
			return errors.New("--threshold and --periods must be numbers")
		}
		if len(*emails) == 0 && len(*recipients) == 0 {
			return errors.New("either `--recipient` or `--email` option must be provided")
		}
		var members []anypoint.User
		if len(*recipients) > 0 {
			if members, err = c.ListOrganizationMembers(x.ctx, org); err != nil {
				return err
			}
		}
		byName := map[string]anypoint.User{}
		for _, m := range members {
			byName[m.Username] = m
		}
		switch {
		case *typ == "response-code" && len(*responseCodes) == 0:
			return errors.New("--responseCode must be set when using --type response-code")
		case *typ == "policy-violation" && *policyID == "":
			return errors.New("--policyId must be set when using --type policy-violation")
		case legacy && *typ == "response-time" && *responseTime == "":
			return errors.New("--responseTime must be set when using --type response-time")
		}
		var tmplID, tmplName string
		if *typ == "policy-violation" {
			if tmplID, tmplName, err = appliedPolicyTemplate(x, c, org, env, apiID, *policyID); err != nil {
				return err
			}
		}

		if legacy {
			if err := need("duration", *duration, "durationUnit", *durationUnit); err != nil {
				return err
			}
			if err := oneOf("durationUnit", *durationUnit, "days", "hours", "minutes"); err != nil {
				return err
			}
			dur, err := strconv.ParseFloat(*duration, 64)
			if err != nil {
				return errors.New("--duration must be a number")
			}
			rcpts := []anypoint.Doc{}
			for _, e := range *emails {
				rcpts = append(rcpts, anypoint.Doc{"type": "email", "value": e})
			}
			for _, u := range *recipients {
				if m, ok := byName[u]; ok {
					rcpts = append(rcpts, anypoint.Doc{"type": "user", "value": m.ID, "firstName": m.FirstName, "lastName": m.LastName})
				}
			}
			cond := anypoint.Doc{"resourceType": "api-version", "aggregate": "COUNT",
				"operator": map[string]string{"gt": "GREATER_THAN", "lt": "LESS_THAN", "eq": "EQUAL"}[*operator], "value": thr}
			body := anypoint.Doc{"apiAlertsVersion": "1.0.0", "name": name,
				"type":    map[string]string{"request-count": "api-request-count", "response-code": "api-response-code", "policy-violation": "api-policy-violation", "response-time": "api-response-time"}[*typ],
				"enabled": *enabled, "severity": *severity, "recipients": rcpts, "condition": cond,
				"period": anypoint.Doc{"duration": anypoint.Doc{"count": dur, "weight": strings.ToUpper(*durationUnit)}, "repeat": per}}
			switch *typ {
			case "response-code":
				cond["filter"] = []anypoint.Doc{{"property": "responseCode", "operator": "IN", "values": []string(*responseCodes)}}
			case "policy-violation":
				body["policyId"] = *policyID
				body["policyTemplate"] = anypoint.Doc{"id": tmplID, "name": tmplName}
			case "response-time":
				cond["filter"] = []anypoint.Doc{{"property": "responseTime", "operator": "GREATER_THAN", "values": *responseTime}}
			}
			id, err := c.CreateAPIAlertLegacy(x.ctx, org, env, apiID, body)
			if err != nil {
				return err
			}
			return x.Done("Created new alert with ID %q in API ID %q", id, apiID)
		}

		addrs := append([]string{}, *emails...)
		for _, u := range *recipients {
			if m, ok := byName[u]; ok && m.Email != "" {
				addrs = append(addrs, m.Email)
			}
		}
		master, err := c.MasterOrgID(x.ctx)
		if err != nil {
			return err
		}
		resource := anypoint.Doc{"apiVersionId": apiID, "apiId": apiID, "type": "api", "deploymentType": "CLOUDHUB"}
		switch *typ {
		case "response-code":
			resource["subFilters"] = []anypoint.Doc{{"name": "response_code", "values": []string(*responseCodes)}}
		case "policy-violation":
			resource["subFilters"] = []anypoint.Doc{{"name": "policy_id", "values": []string{*policyID}}}
		}
		subj, msg := *subject, *message
		if subj == "" {
			subj = alertSubject
		}
		if msg == "" {
			msg = alertMessage
		}
		body := anypoint.Doc{"type": "basic", "name": name, "severity": *severity, "masterOrganizationId": master,
			"organizationId": org, "environmentId": env, "resourceType": "api", "deploymentType": "CLOUDHUB",
			"metricType": map[string]string{"request-count": "api_request_count", "response-code": "api_response_code_count",
				"policy-violation": "api_policy_violation_count", "response-time": "api_response_time"}[*typ],
			"resources": []anypoint.Doc{resource},
			"condition": anypoint.Doc{"operator": map[string]string{"eq": "equal", "ab": "above", "abeq": "aboveOrEquals",
				"bleq": "belowOrEquals", "bl": "below"}[*operator], "threshold": thr, "interval": per},
			"wildcardAlert": false,
			"notifications": []anypoint.Doc{{"type": "email", "recipients": addrs, "subject": subj, "message": msg}}}
		id, err := c.CreateAPIAlert(x.ctx, org, env, body)
		if err != nil {
			return err
		}
		return x.Done("Created new alert with ID %q in API ID %q", id, apiID)
	}
}

// appliedPolicyTemplate finds an applied policy's template ID and name.
func appliedPolicyTemplate(x *Ctx, c *anypoint.Client, org, env, apiID, policyID string) (id, name string, err error) {
	p, err := c.GetAPIPolicy(x.ctx, org, env, apiID, policyID)
	if err != nil {
		return "", "", notFoundAs(err, fmt.Sprintf("Policy with ID %q not found", policyID))
	}
	if p.AssetID == "" {
		t, err := c.GetPolicyTemplate(x.ctx, org, p.PolicyTemplateID.String())
		if err != nil {
			return "", "", err
		}
		return t.ID.String(), t.Name, nil
	}
	a, err := c.GetPolicyAsset(x.ctx, p.GroupID, p.AssetID, p.AssetVersion)
	if err != nil {
		return "", "", err
	}
	return a.AssetID, a.Name, nil
}

// ---------------------------------------------------------------- upstreams

func upstreamTLS(id, group string, removable bool) (v any, set bool, err error) {
	switch {
	case removable && id == "null":
		return nil, true, nil
	case id == "" && group == "":
		return nil, false, nil
	case id == "" || group == "":
		return nil, false, errors.New("`--tlsContextId` and `--tlsContextSecretGroupId` options must be provided together")
	}
	return anypoint.Doc{"secretGroupId": group, "tlsContextId": id}, true, nil
}

func cmdUpstreamAdd(x *Ctx, args []string) error {
	fs := x.Flags()
	label := fs.String("label", "", "upstream `label`")
	tlsID := fs.String("tlsContextId", "", "TLS context `ID`")
	tlsGroup := fs.String("tlsContextSecretGroupId", "", "secret group `ID` of the TLS context")
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	body := anypoint.Doc{"uri": pos[1]}
	if *label != "" {
		body["label"] = *label
	}
	tls, set, err := upstreamTLS(*tlsID, *tlsGroup, false)
	if err != nil {
		return err
	}
	if set {
		body["tlsContext"] = tls
	}
	u, err := c.CreateUpstream(x.ctx, org, env, pos[0], body)
	if err != nil {
		return err
	}
	return x.Print(u, func() { fmt.Fprintf(x.Out, "Created upstream with ID %q\n", u.ID) })
}

func cmdUpstreamEdit(x *Ctx, args []string) error {
	fs := x.Flags()
	uri := fs.String("uri", "", "new upstream `URI`")
	label := fs.String("label", "", "new `label`")
	tlsID := fs.String("tlsContextId", "", "TLS context `ID` (\"null\" removes it)")
	tlsGroup := fs.String("tlsContextSecretGroupId", "", "secret group `ID` of the TLS context")
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	body := anypoint.Doc{}
	if *uri != "" {
		body["uri"] = *uri
	}
	if *label != "" {
		body["label"] = *label
	}
	tls, set, err := upstreamTLS(*tlsID, *tlsGroup, true)
	if err != nil {
		return err
	}
	if set {
		body["tlsContext"] = tls
	}
	if err := c.UpdateUpstream(x.ctx, org, env, pos[0], pos[1], body); err != nil {
		return notFoundAs(err, "API instance or upstream not found")
	}
	return x.Done("Upstream with ID %q edited", pos[1])
}

func cmdUpstreamBulkEdit(x *Ctx, args []string) error {
	fs := x.Flags()
	tlsID := fs.String("tlsContextId", "", "(required) TLS context `ID` for all upstreams (\"null\" removes it)")
	tlsGroup := fs.String("tlsContextSecretGroupId", "", "secret group `ID` of the TLS context")
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	if err := need("tlsContextId", *tlsID); err != nil {
		return err
	}
	if *tlsID != "null" && *tlsGroup == "" {
		return errors.New("--tlsContextSecretGroupId must be provided when setting a TLS Context ID")
	}
	ups, err := c.ListUpstreams(x.ctx, org, env, pos[0])
	if err != nil {
		return notFoundAs(err, "API instance not found")
	}
	if len(ups) == 0 {
		return x.Done("No upstreams found for this API instance")
	}
	fmt.Fprintf(x.Err, "Found %d upstream(s) to update\n", len(ups))
	tls, _, _ := upstreamTLS(*tlsID, *tlsGroup, true)
	var list []anypoint.Doc
	for _, u := range ups {
		var label any
		if u.Label != "" {
			label = u.Label
		}
		list = append(list, anypoint.Doc{"id": u.ID, "label": label, "uri": u.URI, "tlsContext": tls})
	}
	if err := c.UpdateAPIInstanceXAPI(x.ctx, org, env, pos[0], anypoint.Doc{"upstreams": list}); err != nil {
		return notFoundAs(err, "API instance not found")
	}
	return x.Done("Successfully updated TLS Context for all %d upstream(s)", len(ups))
}

func cmdUpstreamDelete(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := c.DeleteUpstream(x.ctx, org, env, pos[0], pos[1]); err != nil {
		return notFoundAs(err, "API instance or upstream not found")
	}
	return x.Done("Deleted upstream with ID %q from API with ID %q", pos[1], pos[0])
}

func cmdUpstreamList(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	ups, err := c.ListUpstreams(x.ctx, org, env, pos[0])
	if err != nil {
		return notFoundAs(err, "API instance not found")
	}
	return x.Print(ups, func() {
		t := newTable(x.Out, "ID", "LABEL", "URL", "TLS CONTEXT", "UPDATED")
		for _, u := range ups {
			tls := ""
			if u.TLSContext != nil {
				tls = u.TLSContext.Name
			}
			t.row(u.ID, u.Label, u.URI, tls, ago(u.Audit.Changed()))
		}
		t.flush()
	})
}

// ---------------------------------------------------------------- applications

func cmdAppAddOwner(x *Ctx, args []string) error {
	pos, c, org, err := orgSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := c.AddApplicationOwner(x.ctx, org, pos[0], pos[1], pos[2]); err != nil {
		return notFoundAs(err, "Application or user does not exist")
	}
	return x.Done("Owner added to application successfully")
}

func cmdAppImport(x *Ctx, args []string) error {
	fs := x.Flags()
	desc := fs.String("description", "", "application `description`")
	appURL := fs.String("url", "", "application `URL`")
	pos, c, org, err := orgSetup(x, fs, args)
	if err != nil {
		return err
	}
	id, err := c.ImportClientApplication(x.ctx, org, pos[0], pos[1], *desc, *appURL)
	if err != nil {
		return notFoundAs(err, "Provider or client application does not exist")
	}
	return x.Done("Client application imported successfully. Application ID: %q", id)
}

func cmdAppPossibleOwners(x *Ctx, args []string) error {
	fs := x.Flags()
	search := fs.String("search", "", "search `text`")
	limit := fs.Int("limit", 0, "number of results")
	offset := fs.Int("offset", 0, "results to skip")
	_, c, org, err := orgSetup(x, fs, args)
	if err != nil {
		return err
	}
	owners, err := c.ListPossibleApplicationOwners(x.ctx, org, *search, *limit, *offset)
	if err != nil {
		return err
	}
	return x.Print(owners, func() {
		t := newTable(x.Out, "ID", "NAME", "EMAIL", "USERNAME", "ENTITY TYPE")
		for _, o := range owners {
			t.row(o.ID, strings.TrimSpace(o.FirstName+" "+o.LastName), o.Email, o.Username, o.EntityType)
		}
		t.flush()
	})
}

func cmdClientProvidersImportable(x *Ctx, args []string) error {
	_, c, org, err := orgSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	ps, err := c.ListImportableClientProviders(x.ctx, org)
	if err != nil {
		return err
	}
	return x.Print(ps, func() {
		t := newTable(x.Out, "PROVIDER ID", "NAME")
		for _, p := range ps {
			t.row(p.ProviderID, p.Name)
		}
		t.flush()
	})
}

// orgSetup parses the flags and resolves the organization only.
func orgSetup(x *Ctx, fs *flag.FlagSet, args []string) (pos []string, c *anypoint.Client, org string, err error) {
	if pos, c, err = x.Setup(fs, args); err != nil {
		return
	}
	org, err = c.OrgID(x.ctx)
	return
}

func readAllClose(r io.ReadCloser) ([]byte, error) {
	defer r.Close()
	return io.ReadAll(r)
}
