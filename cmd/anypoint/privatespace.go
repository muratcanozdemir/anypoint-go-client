package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/muratcanozdemir/anypoint-go-client/anypoint"
)

// CloudHub 2.0 private spaces. anypoint-cli-v4 has no commands for them;
// these follow the Terraform provider's resources (private space, network,
// firewall rules, advanced config, associations, upgrade, transit gateway,
// VPN connection, TLS context). SPACE is a private space's ID or name.

func privateSpaceCmd() *Cmd {
	return group("private-space", "CloudHub 2.0 private spaces",
		leaf("list", "", "List private spaces", cmdPSList).alias("ls"),
		leaf("describe", "SPACE", "Show a private space", cmdPSDescribe),
		leaf("create", "NAME", "Create a private space", cmdPSCreate),
		leaf("update", "SPACE", "Rename a private space or change its IAM role and egress settings", cmdPSUpdate),
		leaf("delete", "SPACE", "Delete a private space", cmdPSDelete).alias("rm"),
		leaf("network", "SPACE", "Set a private space's region, CIDR block and reserved CIDRs", cmdPSNetwork),
		group("firewall-rules", "Firewall rules",
			leaf("list", "SPACE", "List the firewall rules", cmdPSFirewallList).alias("ls"),
			leaf("add", "SPACE TYPE PROTOCOL CIDRBLOCK FROMPORT [TOPORT]", "Add a rule (TYPE inbound or outbound)", cmdPSFirewallAdd),
			leaf("remove", "SPACE INDEX", "Remove a rule by its index in list", cmdPSFirewallRemove)),
		leaf("ingress", "SPACE", "Set the ingress settings (advanced configuration)", cmdPSIngress),
		group("association", "Environments that deploy to a private space",
			leaf("list", "SPACE", "List the associations", cmdPSAssocList).alias("ls"),
			leaf("create", "SPACE", "Associate environments (--associate, repeatable)", cmdPSAssocCreate),
			leaf("delete", "SPACE ASSOCIATIONID", "Remove an association", cmdPSAssocDelete).alias("rm")),
		group("upgrade", "Scheduled upgrades",
			leaf("status", "SPACE", "Show the scheduled upgrade", cmdPSUpgradeStatus),
			leaf("schedule", "SPACE DATE", "Schedule the upgrade (DATE as YYYY-MM-DD)", cmdPSUpgradeSchedule),
			leaf("cancel", "SPACE", "Cancel the scheduled upgrade", cmdPSUpgradeCancel)),
		group("transit-gateway", "AWS transit gateway attachments",
			leaf("list", "SPACE", "List transit gateways", cmdTGWList).alias("ls"),
			leaf("describe", "SPACE TGW", "Show a transit gateway", cmdTGWDescribe),
			leaf("create", "SPACE NAME", "Attach the transit gateway an AWS RAM resource share holds", cmdTGWCreate),
			leaf("rename", "SPACE TGW NAME", "Rename a transit gateway", cmdTGWRename),
			leaf("routes", "SPACE TGW", "Replace a transit gateway's routes (--route, repeatable)", cmdTGWRoutes),
			leaf("delete", "SPACE TGW", "Detach and de-register a transit gateway", cmdTGWDelete).alias("rm")),
		group("vpn", "VPN connections",
			leaf("describe", "SPACE CONNECTIONID", "Show a VPN connection", cmdVPNDescribe),
			leaf("create", "SPACE NAME", "Create a VPN connection", cmdVPNCreate),
			leaf("delete", "SPACE CONNECTIONID", "Delete a VPN connection, or one VPN of it (--vpn)", cmdVPNDelete).alias("rm")),
		group("tls-context", "TLS contexts of the private space's ingress",
			leaf("list", "SPACE", "List TLS contexts", cmdPSTLSList).alias("ls"),
			leaf("describe", "SPACE TLSCONTEXT", "Show a TLS context", cmdPSTLSDescribe),
			leaf("create", "SPACE NAME", "Create a TLS context from a PEM certificate and key or a JKS keystore", cmdPSTLSCreate),
			leaf("update", "SPACE TLSCONTEXT", "Change a TLS context's name, key store or ciphers", cmdPSTLSUpdate),
			leaf("delete", "SPACE TLSCONTEXT", "Delete a TLS context", cmdPSTLSDelete).alias("rm")),
	)
}

// psSetup parses flags and resolves SPACE (the first argument) to an ID.
func psSetup(x *Ctx, fs *flag.FlagSet, args []string) (pos []string, c *anypoint.Client, org string, space *anypoint.PrivateSpace, err error) {
	if pos, c, org, err = orgSetup(x, fs, args); err != nil {
		return
	}
	space, err = c.FindPrivateSpace(x.ctx, org, pos[0])
	return
}

// optional returns a pointer to an optBool's value when it was given.
func optional(o *optBool, set map[string]bool) *bool {
	if v, given := o.get(set); given {
		return &v
	}
	return nil
}

// ---------------------------------------------------------------- spaces

func cmdPSList(x *Ctx, args []string) error {
	_, c, org, err := orgSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	spaces, err := c.ListPrivateSpaces(x.ctx, org)
	if err != nil {
		return err
	}
	return x.Print(spaces, func() {
		t := newTable(x.Out, "ID", "NAME", "REGION", "STATUS", "CIDR BLOCK")
		for _, s := range spaces {
			t.row(s.ID, s.Name, cmp(s.Region, s.Network.Region), s.Status, s.Network.CIDRBlock)
		}
		t.flush()
	})
}

func showPrivateSpace(x *Ctx, s *anypoint.PrivateSpace) error {
	return x.Print(s, func() {
		printKV(x.Out, "ID", s.ID, "Name", s.Name, "Region", cmp(s.Region, s.Network.Region), "Status", s.Status,
			"Version", s.Version, "Provisioning", strings.TrimSpace(s.Provisioning.Status+" "+s.Provisioning.Message),
			"CIDR block", s.Network.CIDRBlock, "Reserved CIDRs", strings.Join(s.Network.ReservedCIDRs, ", "),
			"Inbound static IPs", strings.Join(s.Network.InboundStaticIPs, ", "),
			"Outbound static IPs", strings.Join(s.Network.OutboundStaticIPs, ", "), "DNS target", s.Network.DNSTarget,
			"Environments", strings.TrimSpace(s.Environments.Type+" "+strings.Join(s.Environments.BusinessGroups, ", ")),
			"IAM role", strconv.FormatBool(s.EnableIAMRole), "Egress", strconv.FormatBool(s.EnableEgress),
			"Firewall rules", strconv.Itoa(len(s.ManagedFirewallRules)), "Mule apps", strconv.Itoa(s.MuleAppDeploymentCount))
	})
}

func cmdPSDescribe(x *Ctx, args []string) error {
	_, _, _, s, err := psSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	return showPrivateSpace(x, s)
}

func cmdPSCreate(x *Ctx, args []string) error {
	fs := x.Flags()
	region := fs.String("region", "", "AWS `region` (e.g. us-east-1)")
	iam := fs.Bool("enable-iam-role", false, "give apps an AWS IAM role")
	egress := fs.Bool("enable-egress", false, "enable egress")
	cidr := fs.String("cidr-block", "", "also set the network's `CIDR` block (e.g. 10.0.0.0/22)")
	reserved := multi(fs, "reserved-cidr", "a `CIDR` the space must not use (repeatable; with --cidr-block)")
	pos, c, org, err := orgSetup(x, fs, args)
	if err != nil {
		return err
	}
	if err := need("region", *region); err != nil {
		return err
	}
	req := &anypoint.CreatePrivateSpaceRequest{Name: pos[0], Region: *region}
	if *iam {
		req.EnableIAMRole = iam
	}
	if *egress {
		req.EnableEgress = egress
	}
	if len(*reserved) > 0 && *cidr == "" {
		return errors.New("--reserved-cidr needs --cidr-block")
	}
	s, err := c.CreatePrivateSpace(x.ctx, org, req)
	if err != nil {
		return err
	}
	if *cidr != "" {
		n := anypoint.PrivateSpaceNetwork{Region: *region, CIDRBlock: *cidr, ReservedCIDRs: *reserved}
		if s, err = c.SetPrivateSpaceNetwork(x.ctx, org, s.ID, n); err != nil {
			return fmt.Errorf("private space %s was created, but setting its network failed: %w", req.Name, err)
		}
	}
	return showPrivateSpace(x, s)
}

func cmdPSUpdate(x *Ctx, args []string) error {
	fs := x.Flags()
	name := fs.String("name", "", "new `name`")
	iam := newOptBool(fs, "enable-iam-role", "give apps an AWS IAM role")
	egress := newOptBool(fs, "enable-egress", "enable egress")
	_, c, org, s, err := psSetup(x, fs, args)
	if err != nil {
		return err
	}
	set := setFlags(fs)
	req := &anypoint.UpdatePrivateSpaceRequest{EnableIAMRole: optional(iam, set), EnableEgress: optional(egress, set)}
	if *name != "" {
		req.Name = name
	}
	if req.Name == nil && req.EnableIAMRole == nil && req.EnableEgress == nil {
		return errors.New("nothing to update: give --name, --enable-iam-role or --enable-egress")
	}
	if s, err = c.UpdatePrivateSpace(x.ctx, org, s.ID, req); err != nil {
		return err
	}
	return showPrivateSpace(x, s)
}

func cmdPSDelete(x *Ctx, args []string) error {
	_, c, org, s, err := psSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := c.DeletePrivateSpace(x.ctx, org, s.ID); err != nil {
		return err
	}
	return x.Done("Private space %s deleted", s.Name)
}

func cmdPSNetwork(x *Ctx, args []string) error {
	fs := x.Flags()
	region := fs.String("region", "", "AWS `region`")
	cidr := fs.String("cidr-block", "", "the space's `CIDR` block (e.g. 10.0.0.0/22)")
	reserved := multi(fs, "reserved-cidr", "a `CIDR` the space must not use (repeatable)")
	_, c, org, s, err := psSetup(x, fs, args)
	if err != nil {
		return err
	}
	if err := need("cidr-block", *cidr); err != nil {
		return err
	}
	n := anypoint.PrivateSpaceNetwork{Region: cmp(*region, cmp(s.Region, s.Network.Region)), CIDRBlock: *cidr, ReservedCIDRs: *reserved}
	if s, err = c.SetPrivateSpaceNetwork(x.ctx, org, s.ID, n); err != nil {
		return err
	}
	return showPrivateSpace(x, s)
}

// ---------------------------------------------------------------- firewall rules

func printFirewall(x *Ctx, rules []anypoint.PrivateSpaceFirewallRule) error {
	return x.Print(rules, func() {
		t := newTable(x.Out, "INDEX", "TYPE", "PROTOCOL", "CIDR BLOCK", "FROM PORT", "TO PORT")
		for i, r := range rules {
			t.row(strconv.Itoa(i), r.Type, r.Protocol, r.CIDRBlock, strconv.Itoa(r.FromPort), strconv.Itoa(r.ToPort))
		}
		t.flush()
	})
}

func cmdPSFirewallList(x *Ctx, args []string) error {
	_, _, _, s, err := psSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	return printFirewall(x, s.ManagedFirewallRules)
}

func cmdPSFirewallAdd(x *Ctx, args []string) error {
	pos, c, org, s, err := psSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := oneOf("TYPE", pos[1], "inbound", "outbound"); err != nil {
		return err
	}
	if err := oneOf("PROTOCOL", strings.ToLower(pos[2]), "tcp", "udp", "icmp", "-1"); err != nil {
		return err
	}
	from, err := strconv.Atoi(pos[4])
	if err != nil {
		return fmt.Errorf("FROMPORT must be a number, got %q", pos[4])
	}
	to := from
	if p := arg(pos, 5); p != "" {
		if to, err = strconv.Atoi(p); err != nil {
			return fmt.Errorf("TOPORT must be a number, got %q", p)
		}
	}
	rule := anypoint.PrivateSpaceFirewallRule{Type: pos[1], Protocol: strings.ToLower(pos[2]), CIDRBlock: pos[3], FromPort: from, ToPort: to}
	if s, err = c.SetPrivateSpaceFirewallRules(x.ctx, org, s.ID, append(slices.Clone(s.ManagedFirewallRules), rule)); err != nil {
		return err
	}
	return printFirewall(x, s.ManagedFirewallRules)
}

func cmdPSFirewallRemove(x *Ctx, args []string) error {
	pos, c, org, s, err := psSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	i, err := strconv.Atoi(pos[1])
	if err != nil || i < 0 || i >= len(s.ManagedFirewallRules) {
		return fmt.Errorf("INDEX must be 0 to %d (see firewall-rules list), got %q", len(s.ManagedFirewallRules)-1, pos[1])
	}
	if s, err = c.SetPrivateSpaceFirewallRules(x.ctx, org, s.ID, slices.Delete(slices.Clone(s.ManagedFirewallRules), i, i+1)); err != nil {
		return err
	}
	return printFirewall(x, s.ManagedFirewallRules)
}

// ---------------------------------------------------------------- ingress (advanced config)

func cmdPSIngress(x *Ctx, args []string) error {
	fs := x.Flags()
	timeout := fs.String("read-response-timeout", "", "ingress read timeout in `seconds`")
	protocol := fs.String("protocol", "", "ingress `protocol` (the default is https-redirect)")
	portLog := fs.String("port-log-level", "", "`level` of the ingress port logs (ERROR, WARN, INFO, DEBUG)")
	filters := multi(fs, "log-filter", "`IP=LEVEL` log level for a client IP (repeatable)")
	iam := newOptBool(fs, "enable-iam-role", "give apps an AWS IAM role")
	reset := fs.Bool("reset", false, "go back to the defaults (300 s, https-redirect, ERROR port logs, no filters, no IAM role) before applying the other flags")
	_, c, org, s, err := psSetup(x, fs, args)
	if err != nil {
		return err
	}
	cfg := anypoint.PrivateSpaceAdvancedConfig{IngressConfiguration: s.IngressConfiguration, EnableIAMRole: s.EnableIAMRole}
	if *reset {
		cfg = anypoint.DefaultPrivateSpaceAdvancedConfig()
	}
	in := &cfg.IngressConfiguration
	if *timeout != "" {
		if _, err := strconv.Atoi(*timeout); err != nil {
			return fmt.Errorf("--read-response-timeout must be a number of seconds, got %q", *timeout)
		}
		in.ReadResponseTimeout = anypoint.FlexID(*timeout)
	}
	if *protocol != "" {
		in.Protocol = *protocol
	}
	if *portLog != "" || len(*filters) > 0 {
		logs := anypoint.PrivateSpaceIngressLog{}
		if in.Logs != nil {
			logs = *in.Logs
		}
		if *portLog != "" {
			logs.PortLogLevel = *portLog
		}
		if len(*filters) > 0 {
			logs.Filters = logs.Filters[:0:0]
			for _, f := range *filters {
				ip, level, ok := strings.Cut(f, "=")
				if !ok {
					return fmt.Errorf("--log-filter must be IP=LEVEL, got %q", f)
				}
				logs.Filters = append(logs.Filters, anypoint.PrivateSpaceLogFilter{IP: ip, Level: level})
			}
		}
		in.Logs = &logs
	}
	if b := optional(iam, setFlags(fs)); b != nil {
		cfg.EnableIAMRole = *b
	}
	if s, err = c.SetPrivateSpaceAdvancedConfig(x.ctx, org, s.ID, cfg); err != nil {
		return err
	}
	return showPrivateSpace(x, s)
}

// ---------------------------------------------------------------- associations

func printAssociations(x *Ctx, as []anypoint.PrivateSpaceAssociation) error {
	return x.Print(as, func() {
		t := newTable(x.Out, "ID", "ORGANIZATION", "ENVIRONMENT")
		for _, a := range as {
			t.row(a.ID, a.OrganizationID, a.EnvironmentID)
		}
		t.flush()
	})
}

func cmdPSAssocList(x *Ctx, args []string) error {
	_, c, org, s, err := psSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	as, err := c.ListPrivateSpaceAssociations(x.ctx, org, s.ID)
	if err != nil {
		return err
	}
	return printAssociations(x, as)
}

func cmdPSAssocCreate(x *Ctx, args []string) error {
	fs := x.Flags()
	envs := multi(fs, "associate", "`[ORGID:]ENV` an environment ID, or all, production or sandbox, in this or another business group (repeatable)")
	_, c, org, s, err := psSetup(x, fs, args)
	if err != nil {
		return err
	}
	if len(*envs) == 0 {
		return errors.New("missing required flag --associate")
	}
	var reqs []anypoint.AssociationRequest
	for _, e := range *envs {
		o, env, ok := strings.Cut(e, ":")
		if !ok {
			o, env = org, e
		}
		reqs = append(reqs, anypoint.AssociationRequest{OrganizationID: o, Environment: env})
	}
	as, err := c.CreatePrivateSpaceAssociations(x.ctx, org, s.ID, reqs)
	if err != nil {
		return err
	}
	return printAssociations(x, as)
}

func cmdPSAssocDelete(x *Ctx, args []string) error {
	pos, c, org, s, err := psSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := c.DeletePrivateSpaceAssociation(x.ctx, org, s.ID, pos[1]); err != nil {
		return err
	}
	return x.Done("Association %s removed from %s", pos[1], s.Name)
}

// ---------------------------------------------------------------- upgrades

func printUpgrade(x *Ctx, u *anypoint.PrivateSpaceUpgrade) error {
	return x.Print(u, func() { printKV(x.Out, "Status", u.Status, "Scheduled for", u.ScheduledUpdateTime) })
}

func cmdPSUpgradeStatus(x *Ctx, args []string) error {
	_, c, org, s, err := psSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	u, err := c.GetPrivateSpaceUpgrade(x.ctx, org, s.ID)
	if err != nil {
		return err
	}
	return printUpgrade(x, u)
}

func cmdPSUpgradeSchedule(x *Ctx, args []string) error {
	fs := x.Flags()
	optIn := fs.Bool("opt-in", false, "opt in to the upgrade")
	pos, c, org, s, err := psSetup(x, fs, args)
	if err != nil {
		return err
	}
	if len(pos[1]) != len("2006-01-02") || strings.Count(pos[1], "-") != 2 {
		return fmt.Errorf("DATE must be YYYY-MM-DD, got %q", pos[1])
	}
	u, err := c.SchedulePrivateSpaceUpgrade(x.ctx, org, s.ID, pos[1], *optIn)
	if err != nil {
		return err
	}
	return printUpgrade(x, u)
}

func cmdPSUpgradeCancel(x *Ctx, args []string) error {
	_, c, org, s, err := psSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := c.CancelPrivateSpaceUpgrade(x.ctx, org, s.ID); err != nil {
		return err
	}
	return x.Done("Upgrade of %s cancelled", s.Name)
}

// ---------------------------------------------------------------- transit gateways

func findTGW(x *Ctx, c *anypoint.Client, org, spaceID, idOrName string) (*anypoint.TransitGateway, error) {
	tgws, err := c.ListTransitGateways(x.ctx, org, spaceID)
	if err != nil {
		return nil, err
	}
	for i := range tgws {
		if tgws[i].ID == idOrName || tgws[i].Name == idOrName {
			return &tgws[i], nil
		}
	}
	return nil, fmt.Errorf("no transit gateway %q in this private space", idOrName)
}

func showTGW(x *Ctx, t *anypoint.TransitGateway) error {
	return x.Print(t, func() {
		printKV(x.Out, "ID", t.ID, "Name", t.Name, "Region", t.Spec.Region,
			"Resource share", strings.TrimSpace(t.Spec.ResourceShare.ID+" (account "+t.Spec.ResourceShare.Account+")"),
			"Gateway", t.Status.Gateway, "Attachment", t.Status.Attachment,
			"AWS transit gateway", t.Status.AWSTransitGatewayID(), "Routes", strings.Join(t.Status.Routes, ", "))
	})
}

func cmdTGWList(x *Ctx, args []string) error {
	_, c, org, s, err := psSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	tgws, err := c.ListTransitGateways(x.ctx, org, s.ID)
	if err != nil {
		return err
	}
	return x.Print(tgws, func() {
		t := newTable(x.Out, "ID", "NAME", "GATEWAY", "ATTACHMENT", "AWS TRANSIT GATEWAY", "ROUTES")
		for _, g := range tgws {
			t.row(g.ID, g.Name, g.Status.Gateway, g.Status.Attachment, g.Status.AWSTransitGatewayID(), strings.Join(g.Status.Routes, ","))
		}
		t.flush()
	})
}

func cmdTGWDescribe(x *Ctx, args []string) error {
	pos, c, org, s, err := psSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	t, err := findTGW(x, c, org, s.ID, pos[1])
	if err != nil {
		return err
	}
	return showTGW(x, t)
}

func cmdTGWCreate(x *Ctx, args []string) error {
	fs := x.Flags()
	share := fs.String("resource-share-id", "", "AWS RAM resource share `ID`")
	account := fs.String("resource-share-account", "", "AWS `account` that owns the transit gateway")
	routes := multi(fs, "route", "`CIDR` to route through the transit gateway (repeatable, at least one)")
	pos, c, org, s, err := psSetup(x, fs, args)
	if err != nil {
		return err
	}
	if err := need("resource-share-id", *share, "resource-share-account", *account); err != nil {
		return err
	}
	if len(*routes) == 0 {
		return errors.New("missing required flag --route")
	}
	t, err := c.CreateTransitGateway(x.ctx, org, s.ID, &anypoint.CreateTransitGatewayRequest{
		Name: pos[1], ResourceShareID: *share, ResourceShareAccount: *account, Routes: *routes})
	if err != nil {
		return err
	}
	return showTGW(x, t)
}

func cmdTGWRename(x *Ctx, args []string) error {
	pos, c, org, s, err := psSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	t, err := findTGW(x, c, org, s.ID, pos[1])
	if err != nil {
		return err
	}
	if err := c.RenameTransitGateway(x.ctx, org, t.ID, pos[2]); err != nil {
		return err
	}
	return x.Done("Transit gateway %s renamed to %s", t.ID, pos[2])
}

func cmdTGWRoutes(x *Ctx, args []string) error {
	fs := x.Flags()
	routes := multi(fs, "route", "`CIDR` to route through the transit gateway (repeatable; none clears them)")
	pos, c, org, s, err := psSetup(x, fs, args)
	if err != nil {
		return err
	}
	t, err := findTGW(x, c, org, s.ID, pos[1])
	if err != nil {
		return err
	}
	if err := c.SetTransitGatewayRoutes(x.ctx, org, s.ID, t.ID, t.Name, *routes); err != nil {
		return err
	}
	return x.Done("Routes of transit gateway %s set to %s", t.Name, cmp(strings.Join(*routes, ", "), "none"))
}

func cmdTGWDelete(x *Ctx, args []string) error {
	pos, c, org, s, err := psSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	id := pos[1]
	if t, err := findTGW(x, c, org, s.ID, pos[1]); err == nil {
		id = t.ID
	}
	if err := c.DeleteTransitGateway(x.ctx, org, s.ID, id); err != nil {
		return err
	}
	return x.Done("Transit gateway %s detached and de-registered", pos[1])
}

// ---------------------------------------------------------------- VPN connections

var vpnPollInterval = 5 * time.Second

func showVPN(x *Ctx, v *anypoint.VPNConnection) error {
	return x.Print(v, func() {
		printKV(x.Out, "ID", v.ID, "Name", v.Name)
		t := newTable(x.Out, "VPN ID", "NAME", "STATUS", "REMOTE IP", "LOCAL ASN", "REMOTE ASN", "STATIC ROUTES", "TUNNELS")
		for _, p := range v.VPNs {
			t.row(p.VPNID, p.Name, p.VPNConnectionStatus, p.RemoteIPAddress, string(p.LocalASN), string(p.RemoteASN),
				strings.Join(p.StaticRoutes, ","), strconv.Itoa(len(p.VPNTunnels)))
		}
		t.flush()
	})
}

func cmdVPNDescribe(x *Ctx, args []string) error {
	pos, c, org, s, err := psSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	v, err := c.GetVPNConnection(x.ctx, org, s.ID, pos[1])
	if err != nil {
		return err
	}
	return showVPN(x, v)
}

func cmdVPNCreate(x *Ctx, args []string) error {
	fs := x.Flags()
	file := fs.String("file", "", "JSON `file` with the VPNs ([{remoteIpAddress, localAsn, remoteAsn, staticRoutes, vpnTunnels: [{psk, ptpCidr, startupAction}]}])")
	remoteIP := fs.String("remote-ip", "", "the remote gateway's public `IP` (one VPN, instead of --file)")
	localASN := fs.String("local-asn", "", "the space's `ASN`")
	remoteASN := fs.String("remote-asn", "", "the remote `ASN` (for BGP)")
	static := multi(fs, "static-route", "`CIDR` routed statically instead of BGP (repeatable)")
	tunnels := multi(fs, "tunnel", "`PSK,PTPCIDR[,STARTUPACTION]` a tunnel (repeatable, usually two; STARTUPACTION start or add)")
	pos, c, org, s, err := psSetup(x, fs, args)
	if err != nil {
		return err
	}
	req := &anypoint.CreateVPNConnectionRequest{Name: pos[1]}
	switch {
	case *file != "" && *remoteIP != "":
		return errors.New("give --file or --remote-ip, not both")
	case *file != "":
		b, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &req.VPNs); err != nil {
			return fmt.Errorf("%s: %w", *file, err)
		}
	default:
		if err := need("remote-ip", *remoteIP, "local-asn", *localASN); err != nil {
			return err
		}
		vpn := anypoint.VPNRequest{RemoteIPAddress: *remoteIP, LocalASN: *localASN, RemoteASN: *remoteASN, StaticRoutes: *static}
		for _, t := range *tunnels {
			p := strings.Split(t, ",")
			if len(p) < 2 || len(p) > 3 {
				return fmt.Errorf("--tunnel must be PSK,PTPCIDR[,STARTUPACTION], got %q", t)
			}
			tr := anypoint.VPNTunnelRequest{PSK: p[0], PTPCIDR: p[1], StartupAction: "start"}
			if len(p) == 3 {
				tr.StartupAction = p[2]
			}
			vpn.VPNTunnels = append(vpn.VPNTunnels, tr)
		}
		if len(vpn.VPNTunnels) == 0 {
			return errors.New("missing required flag --tunnel")
		}
		req.VPNs = []anypoint.VPNRequest{vpn}
	}
	v, err := c.CreateVPNConnection(x.ctx, org, s.ID, req)
	if err != nil {
		return err
	}
	return showVPN(x, v)
}

func cmdVPNDelete(x *Ctx, args []string) error {
	fs := x.Flags()
	vpnID := fs.String("vpn", "", "delete only this `VPN` of the connection")
	wait := fs.Duration("wait", 2*time.Minute, "how long to wait for the connection to be gone (0: don't wait)")
	pos, c, org, s, err := psSetup(x, fs, args)
	if err != nil {
		return err
	}
	if *vpnID != "" {
		if err := c.DeleteVPN(x.ctx, org, s.ID, pos[1], *vpnID); err != nil {
			return err
		}
		return x.Done("VPN %s of connection %s deleted", *vpnID, pos[1])
	}
	if err := c.DeleteVPNConnection(x.ctx, org, s.ID, pos[1]); err != nil {
		return err
	}
	if *wait > 0 { // deletion is asynchronous
		ctx, cancel := context.WithTimeout(x.ctx, *wait)
		defer cancel()
		for {
			_, err := c.GetVPNConnection(ctx, org, s.ID, pos[1])
			if anypoint.IsNotFound(err) {
				break
			}
			if ctx.Err() != nil || err == nil {
				select {
				case <-time.After(vpnPollInterval):
					continue
				case <-ctx.Done():
					return fmt.Errorf("VPN connection %s is still being deleted after %s", pos[1], *wait)
				}
			}
			return fmt.Errorf("waiting for VPN connection %s to be deleted: %w", pos[1], err)
		}
	}
	return x.Done("VPN connection %s deleted", pos[1])
}

// ---------------------------------------------------------------- TLS contexts

func showPSTLS(x *Ctx, t *anypoint.PrivateSpaceTLSContext) error {
	return x.Print(t, func() {
		kv := []string{"ID", t.ID, "Name", t.Name, "Type", t.Type}
		if ks := t.KeyStore; ks != nil {
			kv = append(kv, "Key store", strings.TrimSpace(ks.Type+" "+ks.FileName), "CN", ks.CN,
				"SAN", strings.Join(ks.SAN, ", "), "Expires", ks.ExpirationDate)
		}
		var on []string
		for name, enabled := range t.Ciphers {
			if enabled {
				on = append(on, name)
			}
		}
		slices.Sort(on)
		printKV(x.Out, append(kv, "Ciphers", strings.Join(on, ", "))...)
	})
}

func cmdPSTLSList(x *Ctx, args []string) error {
	_, c, org, s, err := psSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	all, err := c.ListPrivateSpaceTLSContexts(x.ctx, org, s.ID)
	if err != nil {
		return err
	}
	return x.Print(all, func() {
		t := newTable(x.Out, "ID", "NAME", "TYPE", "CN", "EXPIRES")
		for _, tc := range all {
			cn, exp := "", ""
			if tc.KeyStore != nil {
				cn, exp = tc.KeyStore.CN, tc.KeyStore.ExpirationDate
			}
			t.row(tc.ID, tc.Name, tc.Type, cn, exp)
		}
		t.flush()
	})
}

func cmdPSTLSDescribe(x *Ctx, args []string) error {
	pos, c, org, s, err := psSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	t, err := c.FindPrivateSpaceTLSContext(x.ctx, org, s.ID, pos[1])
	if err != nil {
		return err
	}
	return showPSTLS(x, t)
}

// tlsFlags are the key store and cipher flags of create and update.
type tlsFlags struct {
	cert, key, keystore, storePass, alias, keyPass *string
	ciphers, noCiphers                             *multiFlag
}

func addTLSFlags(fs *flag.FlagSet) tlsFlags {
	return tlsFlags{
		cert:      fs.String("certificate", "", "PEM certificate (chain) `file`"),
		key:       fs.String("key", "", "PEM private key `file`"),
		keystore:  fs.String("keystore", "", "JKS keystore `file` (instead of --certificate and --key)"),
		storePass: fs.String("store-passphrase", "", "the JKS keystore's `passphrase`"),
		alias:     fs.String("alias", "", "the key's `alias` in the JKS keystore"),
		keyPass:   fs.String("key-passphrase", "", "the private key's `passphrase`"),
		ciphers:   multi(fs, "cipher", "enable a `cipher` (e.g. tlsAes256GcmSha384; repeatable)"),
		noCiphers: multi(fs, "disable-cipher", "disable a `cipher` (repeatable)"),
	}
}

// keyStore builds the key store from the flags; nil when none was given.
func (f tlsFlags) keyStore() (*anypoint.PrivateSpaceKeyStore, error) {
	switch {
	case *f.keystore != "" && (*f.cert != "" || *f.key != ""):
		return nil, errors.New("give --keystore or --certificate and --key, not both")
	case *f.keystore != "":
		if err := need("store-passphrase", *f.storePass, "alias", *f.alias); err != nil {
			return nil, err
		}
		b, err := os.ReadFile(*f.keystore)
		if err != nil {
			return nil, err
		}
		return &anypoint.PrivateSpaceKeyStore{Source: "JKS", KeystoreBase64: base64.StdEncoding.EncodeToString(b),
			KeystoreFileName: filepath.Base(*f.keystore), StorePassphrase: *f.storePass, Alias: *f.alias, KeyPassphrase: *f.keyPass}, nil
	case *f.cert != "" || *f.key != "":
		if err := need("certificate", *f.cert, "key", *f.key); err != nil {
			return nil, err
		}
		cert, err := os.ReadFile(*f.cert)
		if err != nil {
			return nil, err
		}
		key, err := os.ReadFile(*f.key)
		if err != nil {
			return nil, err
		}
		return &anypoint.PrivateSpaceKeyStore{Source: "PEM", Certificate: string(cert), Key: string(key),
			CertificateFileName: filepath.Base(*f.cert), KeyFileName: filepath.Base(*f.key), KeyPassphrase: *f.keyPass}, nil
	}
	return nil, nil
}

// cipherSet applies --cipher and --disable-cipher to base.
func (f tlsFlags) cipherSet(base map[string]bool) map[string]bool {
	if len(*f.ciphers) == 0 && len(*f.noCiphers) == 0 {
		return nil
	}
	out := map[string]bool{}
	maps.Copy(out, base)
	for _, c := range *f.ciphers {
		out[c] = true
	}
	for _, c := range *f.noCiphers {
		out[c] = false
	}
	return out
}

func cmdPSTLSCreate(x *Ctx, args []string) error {
	fs := x.Flags()
	f := addTLSFlags(fs)
	pos, c, org, s, err := psSetup(x, fs, args)
	if err != nil {
		return err
	}
	ks, err := f.keyStore()
	if err != nil {
		return err
	}
	if ks == nil {
		return errors.New("give --certificate and --key, or --keystore")
	}
	req := (&anypoint.PrivateSpaceTLSRequest{Name: pos[1], Ciphers: f.cipherSet(nil)}).WithKeyStore(*ks)
	t, err := c.CreatePrivateSpaceTLSContext(x.ctx, org, s.ID, req)
	if err != nil {
		return err
	}
	return showPSTLS(x, t)
}

func cmdPSTLSUpdate(x *Ctx, args []string) error {
	fs := x.Flags()
	name := fs.String("name", "", "new `name`")
	f := addTLSFlags(fs)
	pos, c, org, s, err := psSetup(x, fs, args)
	if err != nil {
		return err
	}
	cur, err := c.FindPrivateSpaceTLSContext(x.ctx, org, s.ID, pos[1])
	if err != nil {
		return err
	}
	ks, err := f.keyStore()
	if err != nil {
		return err
	}
	req := &anypoint.PrivateSpaceTLSRequest{Name: *name, Ciphers: f.cipherSet(cur.Ciphers)}
	if ks != nil {
		req.WithKeyStore(*ks)
	}
	if req.Name == "" && req.TLSConfig == nil && req.Ciphers == nil {
		return errors.New("nothing to update: give --name, a key store or --cipher/--disable-cipher")
	}
	t, err := c.UpdatePrivateSpaceTLSContext(x.ctx, org, s.ID, cur.ID, req)
	if err != nil {
		return err
	}
	if t.ID == "" { // an empty answer: read it back
		if t, err = c.FindPrivateSpaceTLSContext(x.ctx, org, s.ID, cur.ID); err != nil {
			return err
		}
	}
	return showPSTLS(x, t)
}

func cmdPSTLSDelete(x *Ctx, args []string) error {
	pos, c, org, s, err := psSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	id := pos[1]
	if t, err := c.FindPrivateSpaceTLSContext(x.ctx, org, s.ID, pos[1]); err == nil {
		id = t.ID
	}
	if err := c.DeletePrivateSpaceTLSContext(x.ctx, org, s.ID, id); err != nil {
		return err
	}
	return x.Done("TLS context %s deleted", pos[1])
}
