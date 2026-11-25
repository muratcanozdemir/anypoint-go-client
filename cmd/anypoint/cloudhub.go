package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/muratcanozdemir/anypoint-go-client/anypoint"
)

// anypoint-cli-cloudhub-plugin, cloudhub:*: CloudHub 1.0 VPCs, dedicated
// load balancers, regions and runtimes. The plugin's runtime-mgr commands
// (CloudHub 1.0 and 2.0 applications) are in cloudhub_apps.go.

func cloudhubCmd() *Cmd {
	return group("cloudhub", "CloudHub 1.0 networking, regions and runtimes",
		group("load-balancer", "Dedicated load balancers",
			group("allowlist", "Allowed client IP ranges",
				leaf("add", "NAME CIDRBLOCK", "Allow a CIDR block", cmdLBAllowlist(true, false)),
				leaf("remove", "NAME CIDRBLOCK", "Remove an allowed CIDR block", cmdLBAllowlist(false, false))),
			leaf("create", "[NAME] [VPC] [CERTIFICATE] [PRIVATEKEY]", "Create a load balancer", cmdLBCreate),
			leaf("delete", "NAME", "Delete a load balancer", cmdLBDelete).alias("rm"),
			leaf("describe", "[NAME]", "Show a load balancer", cmdLBDescribe),
			group("dynamic-ips", "Dynamic IPs",
				leaf("disable", "NAME", "Use static IPs", cmdLBDynamicIPs(false)),
				leaf("enable", "NAME", "Use dynamic IPs", cmdLBDynamicIPs(true))),
			leaf("list", "", "List load balancers", cmdLBList).alias("ls"),
			group("mappings", "Proxy mappings",
				leaf("add", "NAME INPUTURI APPNAME APPURI [CERTIFICATENAME]", "Add a mapping (first in order)", cmdLBMappingAdd),
				leaf("describe", "NAME [CERTIFICATENAME]", "List the mappings of a certificate", cmdLBMappingDescribe),
				leaf("remove", "NAME INDEX [CERTIFICATENAME]", "Remove a mapping", cmdLBMappingRemove)),
			group("ssl-endpoint", "Certificates",
				leaf("add", "NAME CERTIFICATE PRIVATEKEY", "Add a certificate", cmdLBSSLAdd),
				leaf("describe", "NAME CERTIFICATENAME", "Show a certificate", cmdLBSSLDescribe),
				leaf("remove", "NAME CERTIFICATENAME", "Remove a certificate", cmdLBSSLRemove),
				leaf("setDefault", "NAME CERTIFICATENAME", "Make a certificate the default", cmdLBSSLSetDefault)),
			leaf("start", "[NAME]", "Start a load balancer", cmdLBState("STARTED")),
			leaf("stop", "[NAME]", "Stop a load balancer", cmdLBState("STOPPED")),
			group("whitelist", "Deprecated: use allowlist",
				leaf("add", "NAME CIDRBLOCK", "Allow a CIDR block (use allowlist add)", cmdLBAllowlist(true, true)),
				leaf("remove", "NAME CIDRBLOCK", "Remove an allowed CIDR block (use allowlist remove)", cmdLBAllowlist(false, true))),
		),
		group("region", "Regions",
			leaf("list", "[FILE]", "List regions", cmdRegionList).alias("ls")),
		group("runtime", "Runtimes",
			leaf("list", "", "List the available Mule runtimes", cmdCloudHubRuntimeList).alias("ls")),
		group("vpc", "VPCs",
			group("business-groups", "Business groups a VPC is shared with",
				leaf("add", "VPC BUSINESSGROUPS...", "Share a VPC with business groups", cmdVPCAssoc("sharedWith", true)),
				leaf("remove", "VPC BUSINESSGROUPS...", "Stop sharing a VPC with business groups", cmdVPCAssoc("sharedWith", false))),
			leaf("create", "NAME REGION CIDRBLOCK [ENVIRONMENTS...]", "Create a VPC", cmdVPCCreate),
			leaf("delete", "NAME", "Delete a VPC", cmdVPCDelete).alias("rm"),
			leaf("describe", "[NAME]", "Show a VPC", cmdVPCDescribe),
			group("dns-servers", "Internal DNS",
				leaf("set", "NAME", "Set the DNS servers and the domains they resolve", cmdVPCDNS(true)),
				leaf("unset", "NAME", "Remove the internal DNS settings", cmdVPCDNS(false))),
			group("environments", "Environments a VPC serves",
				leaf("add", "VPC ENVIRONMENTS...", "Associate environments", cmdVPCAssoc("associatedEnvironments", true)),
				leaf("remove", "VPC ENVIRONMENTS...", "Dissociate environments", cmdVPCAssoc("associatedEnvironments", false))),
			group("firewall-rules", "Firewall rules",
				leaf("add", "VPC CIDRBLOCK PROTOCOL FROMPORT TOPORT", "Add a firewall rule", cmdFirewallAdd),
				leaf("describe", "NAME", "List the firewall rules", cmdFirewallDescribe),
				leaf("remove", "VPC INDEX", "Remove a firewall rule", cmdFirewallRemove)),
			leaf("list", "", "List VPCs", cmdVPCList).alias("ls"),
		),
	)
}

// required fails when an optional-looking positional argument is missing
// (anypoint-cli-v4 declares some required arguments as optional).
func required(pos []string, names ...string) error {
	for i, n := range names {
		if arg(pos, i) == "" {
			return fmt.Errorf("missing argument %s", n)
		}
	}
	return nil
}

// ---------------------------------------------------------------- regions, runtimes

func cmdRegionList(x *Ctx, args []string) error {
	_, c, _, err := orgSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	regions, err := c.ListRegions(x.ctx)
	if err != nil {
		return err
	}
	return x.Print(regions, func() {
		t := newTable(x.Out, "REGION", "DESCRIPTION")
		for _, r := range regions {
			t.row(r.ID, r.Name)
		}
		t.flush()
	})
}

func cmdCloudHubRuntimeList(x *Ctx, args []string) error {
	_, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	versions, err := c.ListMuleVersions(x.ctx, org, env)
	if err != nil {
		return err
	}
	return x.Print(versions, func() {
		t := newTable(x.Out, "RUNTIME", "RELEASE CHANNEL", "JAVA VERSION")
		for _, v := range versions {
			if v.State == "ACTIVE" || v.State == "DEFAULT" {
				t.row(v.Version, cmp(v.ReleaseChannel, "NONE"), cmp(v.JavaVersion, "8"))
			}
		}
		t.flush()
	})
}

// cmp returns s, or def when s is empty.
func cmp(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// ---------------------------------------------------------------- VPCs

func findVPC(x *Ctx, c *anypoint.Client, org, name string) (*anypoint.VPC, error) {
	v, err := c.FindVPC(x.ctx, org, name)
	if anypoint.IsNotFound(err) {
		return nil, fmt.Errorf("a VPC with the name %s was not found", name)
	}
	return v, err
}

// nameIndex maps environment and business group IDs to names.
type nameIndex struct{ envs, orgs map[string]string }

func vpcNames(x *Ctx, c *anypoint.Client, org string) (*nameIndex, error) {
	n := &nameIndex{envs: map[string]string{}, orgs: map[string]string{}}
	envs, err := c.ListEnvironments(x.ctx, org)
	if err != nil {
		return nil, err
	}
	for _, e := range envs {
		n.envs[e.ID] = e.Name
	}
	orgs, err := c.BusinessGroups(x.ctx)
	if err != nil {
		return nil, err
	}
	for _, o := range orgs {
		n.orgs[o.ID] = o.Name
	}
	return n, nil
}

func names(ids []string, idx map[string]string) string {
	var out []string
	for _, id := range ids {
		if n, ok := idx[id]; ok {
			out = append(out, n)
		}
	}
	return strings.Join(out, ", ")
}

func inherited(v *anypoint.VPC, org string) string {
	return map[bool]string{true: "No", false: "Yes"}[v.OwnerID == org]
}

func cmdVPCList(x *Ctx, args []string) error {
	fs := x.Flags()
	limit, offset, sortBy := paginationFlags(fs, "id")
	_, c, org, err := orgSetup(x, fs, args)
	if err != nil {
		return err
	}
	vpcs, err := c.ListVPCs(x.ctx, org, anypoint.VPCListOptions{Limit: *limit, Offset: *offset, Sort: *sortBy})
	if err != nil {
		return err
	}
	if vpcs == nil {
		vpcs = []anypoint.VPC{}
	}
	return x.Print(vpcs, func() {
		idx, err := vpcNames(x, c, org)
		if err != nil {
			idx = &nameIndex{}
		}
		t := newTable(x.Out, "NAME", "REGION", "CIDR BLOCK", "INHERITED", "DEFAULT", "ENVIRONMENTS", "BUSINESS GROUPS")
		for i := range vpcs {
			v := &vpcs[i]
			t.row(v.Name, v.Region, v.CIDRBlock, inherited(v, org), yesNo(v.IsDefault), names(v.AssociatedEnvironments, idx.envs), names(v.SharedWith, idx.orgs))
		}
		t.flush()
	})
}

func showVPC(x *Ctx, c *anypoint.Client, org string, v *anypoint.VPC) error {
	idx, err := vpcNames(x, c, org)
	if err != nil {
		return err
	}
	lbs, err := c.ListLoadBalancers(x.ctx, org, v.ID)
	if err != nil {
		return err
	}
	var lbNames []string
	for _, lb := range lbs {
		lbNames = append(lbNames, lb.Name)
	}
	return x.Print(v, func() {
		printKV(x.Out, "Name", v.Name, "Region", v.Region, "CIDR Block", v.CIDRBlock, "Inherited", inherited(v, org),
			"Organization default", yesNo(v.IsDefault), "Special domains", strings.Join(v.InternalDNS.SpecialDomains, ", "),
			"DNS Servers", strings.Join(v.InternalDNS.DNSServers, ", "), "Firewall rules", strconv.Itoa(len(v.FirewallRules)),
			"Environments", names(v.AssociatedEnvironments, idx.envs), "Business groups", names(v.SharedWith, idx.orgs),
			"Load balancers", strings.Join(lbNames, ", "))
	})
}

func cmdVPCDescribe(x *Ctx, args []string) error {
	pos, c, org, err := orgSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := required(pos, "NAME"); err != nil {
		return err
	}
	v, err := findVPC(x, c, org, pos[0])
	if err != nil {
		return err
	}
	return showVPC(x, c, org, v)
}

// envIDs resolves environment names (or IDs) of an organization.
func envIDs(x *Ctx, c *anypoint.Client, org string, idents []string) ([]string, error) {
	envs, err := c.ListEnvironments(x.ctx, org)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, n := range idents {
		i := slices.IndexFunc(envs, func(e anypoint.Environment) bool { return e.Name == n || e.ID == n })
		if i < 0 {
			return nil, fmt.Errorf("environment %s not found", n)
		}
		ids = append(ids, envs[i].ID)
	}
	return ids, nil
}

// groupIDs resolves business group names (or IDs) the user belongs to.
func groupIDs(x *Ctx, c *anypoint.Client, idents []string) ([]string, error) {
	orgs, err := c.BusinessGroups(x.ctx)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, n := range idents {
		i := slices.IndexFunc(orgs, func(o anypoint.Organization) bool { return o.Name == n || o.ID == n })
		if i < 0 {
			return nil, fmt.Errorf("business group %s not found", n)
		}
		ids = append(ids, orgs[i].ID)
	}
	return ids, nil
}

func cmdVPCCreate(x *Ctx, args []string) error {
	fs := x.Flags()
	def := fs.Bool("default", false, "make it the default VPC of its environments")
	pos, c, org, err := orgSetup(x, fs, args)
	if err != nil {
		return err
	}
	envs, err := envIDs(x, c, org, pos[3:])
	if err != nil {
		return err
	}
	v, err := c.CreateVPC(x.ctx, org, anypoint.Doc{"name": pos[0], "region": pos[1], "cidrBlock": pos[2], "isDefault": *def,
		"associatedEnvironments": envs, "sharedWith": []string{}, "internalDns": anypoint.Doc{}, "firewallRules": []any{}, "id": "", "ownerId": ""})
	if err != nil {
		return err
	}
	return showVPC(x, c, org, v)
}

func cmdVPCDelete(x *Ctx, args []string) error {
	fs := x.Flags()
	fs.Bool("force", false, "accepted for anypoint-cli-v4 compatibility; deletion never asks")
	aliasFlag(fs, "f", "force")
	pos, c, org, err := orgSetup(x, fs, args)
	if err != nil {
		return err
	}
	v, err := findVPC(x, c, org, pos[0])
	if err != nil {
		return err
	}
	if err := c.DeleteVPC(x.ctx, org, v.ID); err != nil {
		return err
	}
	return x.Done("VPC %s deleted successfully", pos[0])
}

func splitList(s string) []string {
	out := []string{}
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func cmdVPCDNS(set bool) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		fs := x.Flags()
		var server, domain *string
		if set {
			server = fs.String("server", "", "(required) DNS server `IPs`, comma-separated")
			domain = fs.String("domain", "", "(required) `domains` the servers resolve, comma-separated")
		}
		pos, c, org, err := orgSetup(x, fs, args)
		if err != nil {
			return err
		}
		dns := anypoint.Doc{"specialDomains": []string{}, "dnsServers": []string{}}
		if set {
			if err := need("server", *server, "domain", *domain); err != nil {
				return err
			}
			dns["specialDomains"], dns["dnsServers"] = splitList(*domain), splitList(*server)
		}
		v, err := findVPC(x, c, org, pos[0])
		if err != nil {
			return err
		}
		if err := c.UpdateVPC(x.ctx, org, v.ID, anypoint.Doc{"internalDns": dns}); err != nil {
			return err
		}
		return x.Done("VPC DNS modified successfully")
	}
}

// cmdVPCAssoc adds or removes environments (associatedEnvironments) or
// business groups (sharedWith).
func cmdVPCAssoc(field string, add bool) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		fs := x.Flags()
		var def *bool
		if field == "associatedEnvironments" && add {
			def = fs.Bool("default", false, "make it the default VPC of the environments")
		}
		pos, c, org, err := orgSetup(x, fs, args)
		if err != nil {
			return err
		}
		v, err := findVPC(x, c, org, pos[0])
		if err != nil {
			return err
		}
		cur := v.SharedWith
		var ids []string
		if field == "sharedWith" {
			ids, err = groupIDs(x, c, pos[1:])
		} else {
			cur = v.AssociatedEnvironments
			ids, err = envIDs(x, c, org, pos[1:])
		}
		if err != nil {
			return err
		}
		next := []string{}
		for _, id := range cur {
			if add || !slices.Contains(ids, id) {
				next = append(next, id)
			}
		}
		if add {
			for _, id := range ids {
				if !slices.Contains(next, id) {
					next = append(next, id)
				}
			}
		}
		body := anypoint.Doc{field: next}
		if def != nil && *def {
			body["isDefault"] = true
		}
		if err := c.UpdateVPC(x.ctx, org, v.ID, body); err != nil {
			return err
		}
		return x.Done("VPC association modified successfully")
	}
}

// vpcDoc returns a VPC's full document, as the API sent it, without its hash.
func vpcDoc(v *anypoint.VPC) (map[string]any, error) {
	var doc map[string]any
	if err := json.Unmarshal(v.RawJSON(), &doc); err != nil {
		return nil, err
	}
	delete(doc, "hash")
	return doc, nil
}

func cmdFirewallAdd(x *Ctx, args []string) error {
	pos, c, org, err := orgSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	from, err := strconv.Atoi(pos[3])
	if err != nil {
		return fmt.Errorf("invalid FROMPORT %q", pos[3])
	}
	to := from
	if pos[4] != "" {
		if to, err = strconv.Atoi(pos[4]); err != nil {
			return fmt.Errorf("invalid TOPORT %q", pos[4])
		}
	}
	v, err := findVPC(x, c, org, pos[0])
	if err != nil {
		return err
	}
	doc, err := vpcDoc(v)
	if err != nil {
		return err
	}
	rules, _ := doc["firewallRules"].([]any)
	doc["firewallRules"] = append(rules, anypoint.Doc{"cidrBlock": pos[1], "protocol": pos[2], "fromPort": from, "toPort": to})
	if err := c.UpdateVPC(x.ctx, org, v.ID, doc); err != nil {
		return err
	}
	return x.Done("VPC firewall modified successfully")
}

func cmdFirewallDescribe(x *Ctx, args []string) error {
	pos, c, org, err := orgSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	v, err := findVPC(x, c, org, pos[0])
	if err != nil {
		return err
	}
	rules := v.FirewallRules
	if rules == nil {
		rules = []anypoint.FirewallRule{}
	}
	return x.Print(rules, func() {
		t := newTable(x.Out, "INDEX", "CIDR BLOCK", "PROTOCOL", "FROM PORT", "TO PORT")
		for i, r := range rules {
			to := r.ToPort.String()
			if to == r.FromPort.String() {
				to = ""
			}
			t.row(strconv.Itoa(i), r.CIDRBlock, strings.ToUpper(r.Protocol), r.FromPort.String(), to)
		}
		t.flush()
	})
}

func cmdFirewallRemove(x *Ctx, args []string) error {
	pos, c, org, err := orgSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	v, err := findVPC(x, c, org, pos[0])
	if err != nil {
		return err
	}
	i, err := strconv.Atoi(pos[1])
	if err != nil || i < 0 || i >= len(v.FirewallRules) {
		return fmt.Errorf("no firewall rule with index %s (the VPC has %d)", pos[1], len(v.FirewallRules))
	}
	doc, err := vpcDoc(v)
	if err != nil {
		return err
	}
	rules, _ := doc["firewallRules"].([]any)
	doc["firewallRules"] = slices.Delete(rules, i, i+1)
	if err := c.UpdateVPC(x.ctx, org, v.ID, doc); err != nil {
		return err
	}
	return x.Done("VPC firewall modified successfully")
}

// ---------------------------------------------------------------- load balancers

// vpcLB is a load balancer with the VPC it lives in.
type vpcLB struct {
	*anypoint.LoadBalancer
	vpc *anypoint.VPC
}

// MarshalJSON keeps -o json output the API's document.
func (l vpcLB) MarshalJSON() ([]byte, error) { return l.RawJSON(), nil }

func allLBs(x *Ctx, c *anypoint.Client, org string) ([]vpcLB, error) {
	vpcs, err := c.ListVPCs(x.ctx, org, anypoint.VPCListOptions{})
	if err != nil {
		return nil, err
	}
	out := []vpcLB{}
	for i := range vpcs {
		lbs, err := c.ListLoadBalancers(x.ctx, org, vpcs[i].ID)
		if err != nil {
			return nil, err
		}
		for j := range lbs {
			out = append(out, vpcLB{&lbs[j], &vpcs[i]})
		}
	}
	return out, nil
}

func findLB(x *Ctx, c *anypoint.Client, org, name string) (vpcLB, error) {
	lbs, err := allLBs(x, c, org)
	if err != nil {
		return vpcLB{}, err
	}
	for _, lb := range lbs {
		if lb.Name == name {
			return lb, nil
		}
	}
	return vpcLB{}, fmt.Errorf("a load balancer with the name %s was not found", name)
}

func (l vpcLB) defaultCN() string {
	if l.DefaultSSLEndpoint >= 0 && l.DefaultSSLEndpoint < len(l.SSLEndpoints) {
		return l.SSLEndpoints[l.DefaultSSLEndpoint].PublicKeyCN
	}
	return ""
}

// endpoint finds a certificate by common name; "" means the default one.
func (l vpcLB) endpoint(cn string) (int, error) {
	if cn == "" {
		return l.DefaultSSLEndpoint, nil
	}
	i := slices.IndexFunc(l.SSLEndpoints, func(e anypoint.SSLEndpoint) bool { return e.PublicKeyCN == cn })
	if i < 0 {
		return -1, fmt.Errorf("an SSL endpoint with Common Name %s was not found", cn)
	}
	return i, nil
}

func lbSetup(x *Ctx, fs *flag.FlagSet, args []string) ([]string, *anypoint.Client, string, vpcLB, error) {
	pos, c, org, err := orgSetup(x, fs, args)
	if err != nil {
		return nil, nil, "", vpcLB{}, err
	}
	if err := required(pos, "NAME"); err != nil {
		return nil, nil, "", vpcLB{}, err
	}
	lb, err := findLB(x, c, org, pos[0])
	return pos, c, org, lb, err
}

func patchLB(x *Ctx, c *anypoint.Client, org string, lb vpcLB, ops ...anypoint.PatchOp) (*anypoint.LoadBalancer, error) {
	return c.PatchLoadBalancer(x.ctx, org, lb.vpc.ID, lb.ID, ops)
}

func cmdLBList(x *Ctx, args []string) error {
	_, c, org, err := orgSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	lbs, err := allLBs(x, c, org)
	if err != nil {
		return err
	}
	return x.Print(lbs, func() {
		t := newTable(x.Out, "NAME", "DOMAIN", "STATUS", "VPC", "IPS", "PRIMARY CERTIFICATE")
		for _, lb := range lbs {
			t.row(lb.Name, lb.Domain, lb.State, lb.vpc.Name, strings.Join(lb.IPAddresses, ", "), lb.defaultCN())
		}
		t.flush()
	})
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}

func endpointKV(e anypoint.SSLEndpoint) []string {
	kv := []string{"Common Name", e.PublicKeyCN}
	if len(e.PublicKeySANs) > 0 {
		kv = append(kv, "Subject Alternate Names", strings.Join(e.PublicKeySANs, ", "))
	}
	if e.ClientCertCN != "" {
		kv = append(kv, "Client Certificate", e.ClientCertCN, "Client verification", capitalize(e.VerifyClientMode))
	}
	if e.RevocationListDigest != "" {
		kv = append(kv, "Revocation list serial", e.RevocationListSerial, "CRL last update", e.RevocationListLastUpdate,
			"CRL next update", e.RevocationListNextUpdate)
	}
	return append(kv, "Proxy mappings", strconv.Itoa(len(e.Mappings)))
}

func cmdLBDescribe(x *Ctx, args []string) error {
	_, _, _, lb, err := lbSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	return x.Print(lb, func() {
		kv := []string{"Name", lb.Name, "Domain", lb.Domain, "State", lb.State, "VPC", lb.vpc.Name,
			"IPs", strings.Join(lb.IPAddresses, ", "), "TLSv1", enabled(lb.TLSv1)}
		if len(lb.SSLEndpoints) == 1 {
			kv = append(kv, endpointKV(lb.SSLEndpoints[0])...)
		} else {
			cert := func(e anypoint.SSLEndpoint) string {
				return fmt.Sprintf("%s (%s)", e.PublicKeyCN, strings.Join(e.PublicKeySANs, ", "))
			}
			var primary string
			var others []string
			for i, e := range lb.SSLEndpoints {
				if i == lb.DefaultSSLEndpoint {
					primary = cert(e)
				} else {
					others = append(others, cert(e))
				}
			}
			kv = append(kv, "Primary Certificate (SANs)", primary, "Additional Certificates (SANs)", strings.Join(others, "\n"))
		}
		printKV(x.Out, append(kv, "Allowlisted IPs", strings.Join(lb.IPAllowlist, ", "))...)
	})
}

// certFlags registers the client certificate options of create and ssl-endpoint add.
func certFlags(fs *flag.FlagSet) func(map[string]any) error {
	clientCert := fs.String("clientCertificate", "", "client CA certificate `file` (PEM) to verify clients with")
	mode := fs.String("verificationMode", "", "client verification `mode`: on, off, optional (default optional)")
	crl := fs.String("crl", "", "certificate revocation list `file`")
	return func(ep map[string]any) error {
		if err := oneOf("verificationMode", *mode, "on", "off", "optional"); err != nil {
			return err
		}
		if *clientCert != "" {
			b, err := os.ReadFile(expandHome(*clientCert))
			if err != nil {
				return err
			}
			ep["clientCert"], ep["verifyClientMode"] = string(b), cmp(*mode, "optional")
		}
		if *crl != "" {
			b, err := os.ReadFile(expandHome(*crl))
			if err != nil {
				return err
			}
			ep["revocationList"] = string(b)
		}
		return nil
	}
}

func readKeyPair(cert, key string) (string, string, error) {
	c, err := os.ReadFile(expandHome(cert))
	if err != nil {
		return "", "", err
	}
	k, err := os.ReadFile(expandHome(key))
	if err != nil {
		return "", "", err
	}
	return string(c), string(k), nil
}

func cmdLBCreate(x *Ctx, args []string) error {
	fs := x.Flags()
	httpMode := fs.String("http", "redirect", "plain HTTP `mode`: on, off, redirect")
	certOpts := certFlags(fs)
	tlsv1 := fs.Bool("tlsv1", false, "accept TLS 1.0 clients")
	fs.Bool("no-tlsv1", false, "turn off --tlsv1")
	dynamic := fs.Bool("dynamic-ips", false, "use dynamic instead of static IPs")
	fs.Bool("no-dynamic-ips", false, "turn off --dynamic-ips")
	pos, c, org, err := orgSetup(x, fs, args)
	if err != nil {
		return err
	}
	if err := required(pos, "NAME", "VPC", "CERTIFICATE", "PRIVATEKEY"); err != nil {
		return err
	}
	if err := oneOf("http", *httpMode, "on", "off", "redirect"); err != nil {
		return err
	}
	set := setFlags(fs)
	pub, priv, err := readKeyPair(pos[2], pos[3])
	if err != nil {
		return err
	}
	ep := map[string]any{"publicKey": pub, "privateKey": priv,
		"mappings": []any{anypoint.Doc{"inputUri": "/{app}/", "appName": "{app}", "appUri": "/", "upstreamProtocol": "http"}}}
	if err := certOpts(ep); err != nil {
		return err
	}
	v, err := findVPC(x, c, org, pos[1])
	if err != nil {
		return err
	}
	lb, err := c.CreateLoadBalancer(x.ctx, org, v.ID, anypoint.Doc{"name": pos[0], "state": "STARTED", "httpMode": *httpMode,
		"defaultSslEndpoint": 0, "tlsv1": *tlsv1 && !set["no-tlsv1"], "sslEndpoints": []any{ep}, "staticIPsDisabled": *dynamic && !set["no-dynamic-ips"]})
	if err != nil {
		return err
	}
	return x.Print(lb, func() {
		fmt.Fprintf(x.Out, "Created Load balancer %s with Common Name %s\n", lb.Name, vpcLB{LoadBalancer: lb}.defaultCN())
	})
}

func cmdLBDelete(x *Ctx, args []string) error {
	_, c, org, lb, err := lbSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := c.DeleteLoadBalancer(x.ctx, org, lb.vpc.ID, lb.ID); err != nil {
		return err
	}
	return x.Done("%s deleted", lb.Name)
}

func cmdLBState(state string) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		_, c, org, lb, err := lbSetup(x, x.Flags(), args)
		if err != nil {
			return err
		}
		if _, err := patchLB(x, c, org, lb, anypoint.PatchOp{Op: "replace", Path: "/state", Value: state}); err != nil {
			return err
		}
		return x.Done("%s is %s", lb.Name, map[string]string{"STARTED": "starting", "STOPPED": "stopping"}[state])
	}
}

func cmdLBDynamicIPs(enable bool) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		_, c, org, lb, err := lbSetup(x, x.Flags(), args)
		if err != nil {
			return err
		}
		if _, err := patchLB(x, c, org, lb, anypoint.PatchOp{Op: "replace", Path: "/staticIPsDisabled", Value: enable}); err != nil {
			return err
		}
		return x.Done("Load balancer %s updated successfully", lb.Name)
	}
}

func cmdLBAllowlist(add, deprecated bool) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		if deprecated {
			fmt.Fprintln(x.Err, "Mulesoft recommends using allowlist in compliance with our Inclusive Product Language Program.")
		}
		pos, c, org, lb, err := lbSetup(x, x.Flags(), args)
		if err != nil {
			return err
		}
		op := anypoint.PatchOp{Op: "add", Path: "/ipAllowlist/-", Value: pos[1]}
		if !add {
			i := slices.Index(lb.IPAllowlist, pos[1])
			if i < 0 {
				return fmt.Errorf("no allowlist rule matching %s was found", pos[1])
			}
			op = anypoint.PatchOp{Op: "remove", Path: "/ipAllowlist/" + strconv.Itoa(i)}
		}
		if _, err := patchLB(x, c, org, lb, op); err != nil {
			return err
		}
		return x.Done("Allowlist for Load balancer %s updated successfully", lb.Name)
	}
}

func cmdLBMappingAdd(x *Ctx, args []string) error {
	fs := x.Flags()
	proto := fs.String("upstreamProtocol", "http", "`protocol` to the application: http or https")
	pos, c, org, lb, err := lbSetup(x, fs, args)
	if err != nil {
		return err
	}
	i, err := lb.endpoint(arg(pos, 4))
	if err != nil {
		return err
	}
	_, err = patchLB(x, c, org, lb, anypoint.PatchOp{Op: "add", Path: fmt.Sprintf("/sslEndpoints/%d/mappings/0", i),
		Value: anypoint.LBMapping{InputURI: pos[1], AppName: pos[2], AppURI: pos[3], UpstreamProtocol: *proto}})
	if err != nil {
		return err
	}
	return x.Done("Mappings for Load balancer %s updated successfully", lb.Name)
}

func cmdLBMappingDescribe(x *Ctx, args []string) error {
	pos, _, _, lb, err := lbSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	i, err := lb.endpoint(arg(pos, 1))
	if err != nil {
		return err
	}
	if i < 0 || i >= len(lb.SSLEndpoints) {
		return errors.New("the load balancer has no certificates")
	}
	maps := lb.SSLEndpoints[i].Mappings
	if maps == nil {
		maps = []anypoint.LBMapping{}
	}
	return x.Print(maps, func() {
		t := newTable(x.Out, "INDEX", "INPUT URI", "APP NAME", "APP URI", "UPSTREAM PROTOCOL")
		for i, m := range maps {
			t.row(strconv.Itoa(i), m.InputURI, m.AppName, m.AppURI, m.UpstreamProtocol)
		}
		t.flush()
	})
}

func cmdLBMappingRemove(x *Ctx, args []string) error {
	pos, c, org, lb, err := lbSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	i, err := lb.endpoint(arg(pos, 2))
	if err != nil {
		return err
	}
	if n, err := strconv.Atoi(pos[1]); err != nil || n < 0 {
		return fmt.Errorf("invalid mapping INDEX %q", pos[1])
	}
	if _, err := patchLB(x, c, org, lb, anypoint.PatchOp{Op: "remove", Path: fmt.Sprintf("/sslEndpoints/%d/mappings/%s", i, pos[1])}); err != nil {
		return err
	}
	return x.Done("Mappings for Load balancer %s updated successfully", lb.Name)
}

func cmdLBSSLAdd(x *Ctx, args []string) error {
	fs := x.Flags()
	certOpts := certFlags(fs)
	pos, c, org, err := orgSetup(x, fs, args)
	if err != nil {
		return err
	}
	pub, priv, err := readKeyPair(pos[1], pos[2])
	if err != nil {
		return err
	}
	ep := map[string]any{"publicKey": pub, "privateKey": priv,
		"mappings": []any{anypoint.Doc{"inputUri": "/{app}/", "appName": "{app}", "appUri": "/"}}}
	if err := certOpts(ep); err != nil {
		return err
	}
	lb, err := findLB(x, c, org, pos[0])
	if err != nil {
		return err
	}
	if _, err := patchLB(x, c, org, lb, anypoint.PatchOp{Op: "add", Path: "/sslEndpoints/-", Value: ep}); err != nil {
		return err
	}
	return x.Done("Load balancer %s updated successfully", lb.Name)
}

func cmdLBSSLDescribe(x *Ctx, args []string) error {
	pos, _, _, lb, err := lbSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	i, err := lb.endpoint(pos[1])
	if err != nil {
		return err
	}
	e := lb.SSLEndpoints[i]
	return x.Print(e.Mappings, func() { printKV(x.Out, endpointKV(e)...) })
}

func cmdLBSSLRemove(x *Ctx, args []string) error {
	pos, c, org, lb, err := lbSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if pos[1] == lb.defaultCN() {
		return errors.New("the default SSL endpoint cannot be deleted")
	}
	i, err := lb.endpoint(pos[1])
	if err != nil {
		return err
	}
	ops := []anypoint.PatchOp{{Op: "remove", Path: "/sslEndpoints/" + strconv.Itoa(i)}}
	if i < lb.DefaultSSLEndpoint {
		ops = append(ops, anypoint.PatchOp{Op: "replace", Path: "/defaultSslEndpoint", Value: lb.DefaultSSLEndpoint - 1})
	}
	if _, err := patchLB(x, c, org, lb, ops...); err != nil {
		return err
	}
	return x.Done("Load balancer %s updated successfully", lb.Name)
}

func cmdLBSSLSetDefault(x *Ctx, args []string) error {
	fs := x.Flags()
	httpMode := fs.String("http", "", "plain HTTP `mode`: on, off, redirect")
	pos, c, org, lb, err := lbSetup(x, fs, args)
	if err != nil {
		return err
	}
	if err := oneOf("http", *httpMode, "on", "off", "redirect"); err != nil {
		return err
	}
	i, err := lb.endpoint(pos[1])
	if err != nil {
		return err
	}
	ops := []anypoint.PatchOp{{Op: "replace", Path: "/defaultSslEndpoint", Value: i}}
	if *httpMode != "" {
		ops = append(ops, anypoint.PatchOp{Op: "replace", Path: "/httpMode", Value: *httpMode})
	}
	if _, err := patchLB(x, c, org, lb, ops...); err != nil {
		return err
	}
	return x.Done("Load balancer %s updated successfully", lb.Name)
}
