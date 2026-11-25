package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/muratcanozdemir/anypoint-go-client/anypoint"
)

// mulesoft-anypoint-cli-agent-fabric-plugin (agent-network:*). Setting up
// the network's Flex Gateways only talks to the control plane and is
// implemented here. The project commands build, run, publish and deploy an
// agent-network project; they are not implemented yet.

const agentProjectReason = "not implemented yet (agent-network projects are built with MuleSoft's agent-fabric-transformation jar and Maven, and run locally with Docker); use anypoint-cli-v4 for it"

func agentNetworkCmd() *Cmd {
	return group("agent-network", "Agent Fabric networks",
		group("project", "Agent network projects",
			unsupported("agent-console", "", agentProjectReason),
			group("broker", "Brokers", unsupported("create", "", agentProjectReason)),
			unsupported("build", "", agentProjectReason),
			unsupported("create", "", agentProjectReason),
			unsupported("deploy", "", agentProjectReason),
			unsupported("export-logs", "", agentProjectReason),
			group("mcp", "MCP servers", unsupported("introspect", "", agentProjectReason)),
			unsupported("publish", "", agentProjectReason),
			unsupported("run-local", "", agentProjectReason),
			unsupported("secrets", "", agentProjectReason),
			unsupported("stop-local", "", agentProjectReason),
			unsupported("undeploy", "", agentProjectReason),
			unsupported("unpublish", "", agentProjectReason)),
		group("setup", "Agent network environment",
			unsupported("dependencies", "", agentProjectReason),
			leaf("gateways", "", "Create (or update) the Flex Gateways an agent network runs behind", cmdAgentGateways)),
	)
}

// agentGatewayPoll is how often setup:gateways checks gateway status.
var agentGatewayPoll = 2 * time.Second

const agentGatewayUpdateTimeout = 15 * time.Minute

// agentGatewayBody is the managed gateway an agent network uses.
func agentGatewayBody(name, targetID, version, size, publicURL string) anypoint.Doc {
	return anypoint.Doc{"name": name, "targetId": targetID, "releaseChannel": "edge", "runtimeVersion": version, "size": size,
		"configuration": anypoint.Doc{
			"ingress":    anypoint.Doc{"publicUrl": publicURL, "forwardSslSession": false, "lastMileSecurity": false},
			"logging":    anypoint.Doc{"level": "info", "forwardLogs": true},
			"properties": anypoint.Doc{"upstreamResponseTimeout": 60, "connectionIdleTimeout": 60},
			"tracing":    anypoint.Doc{"enabled": true, "sampling": 100}}}
}

func cmdAgentGateways(x *Ctx, args []string) error {
	fs := x.Flags()
	space := fs.String("target-space", "agent-network-space", "`name` of the private space, Runtime Fabric or shared space to set up")
	aliasFlag(fs, "t", "target-space")
	single := fs.String("gateway", "", "gateway `name` for single-gateway mode (recommended); not with --ingress-gw/--egress-gw")
	aliasFlag(fs, "g", "gateway")
	ingress := fs.String("ingress-gw", "", "ingress gateway `name` (separate-gateway mode)")
	aliasFlag(fs, "i", "ingress-gw")
	egress := fs.String("egress-gw", "", "egress gateway `name` (separate-gateway mode; needs --ingress-gw)")
	aliasFlag(fs, "e", "egress-gw")
	noWait := fs.Bool("dont-wait-for-gateways-running", false, "return without waiting for the gateways to run")
	update := fs.Bool("update", false, "move existing gateways to their release channel's latest runtime")
	fs.Bool("debug", false, "accepted for anypoint-cli-v4 compatibility; use --verbose to log requests")
	_, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	if *single != "" && (*ingress != "" || *egress != "") {
		return errors.New("--gateway cannot be used with --ingress-gw or --egress-gw")
	}
	if *egress != "" && *ingress == "" {
		return errors.New("--egress-gw requires --ingress-gw")
	}
	fmt.Fprintln(x.Err, "Setting up agent network gateways...")
	targets, err := c.ListGatewayTargets(x.ctx, org, env)
	if err != nil {
		return err
	}
	var target *anypoint.GatewayTarget
	var names []string
	for i := range targets {
		names = append(names, targets[i].Name)
		if targets[i].Name == *space {
			target = &targets[i]
		}
	}
	if target == nil {
		return fmt.Errorf("target space %q not found in the environment. Available: %s", *space, strings.Join(names, ", "))
	}
	shared := target.Type == "shared-space"
	ingressName, egressName := cmp(*single, *ingress), *egress
	switch {
	case shared && egressName != "":
		return errors.New("separate ingress and egress gateways are not supported in a shared space")
	case ingressName == "" && shared:
		ingressName = "agent-network-shared-gw"
	case ingressName == "":
		ingressName = "agent-network-gw"
	}
	var domains *anypoint.TargetDomains
	if !shared {
		if domains, err = c.GetTargetDomains(x.ctx, org, env, target.ID); err != nil {
			return err
		}
		if len(domains.Domains) == 0 {
			return fmt.Errorf("no domains found for target space %s", *space)
		}
	}
	existing, err := c.ListAllGateways(x.ctx, org, env)
	if err != nil {
		return err
	}
	ingressSize := "large"
	if egressName != "" {
		ingressSize = "small"
	}
	var created []string
	provision := func(kind, name, size, publicURL string) error {
		for _, g := range existing {
			if g.Name != name {
				continue
			}
			if g.TargetID != target.ID {
				return fmt.Errorf("a gateway named %s already exists in another target space of this environment", name)
			}
			if !*update {
				fmt.Fprintf(x.Err, "%s gateway '%s' already exists in target space '%s'. Skipping...\n", kind, name, *space)
				return nil
			}
			fmt.Fprintf(x.Err, "%s gateway '%s' already exists in target space '%s'. Checking runtime version...\n", kind, name, *space)
			return reconcileGatewayVersion(x, c, org, env, g.ID, *noWait)
		}
		versions, err := c.GetGatewayVersions(x.ctx)
		if err != nil {
			return err
		}
		version := versions.LatestVersion("edge")
		if version == "" {
			return errors.New("no Flex Gateway versions found for release channel edge")
		}
		fmt.Fprintf(x.Err, "Setting %s in %s...\n", name, *space)
		g, err := c.CreateManagedGateway(x.ctx, org, env, agentGatewayBody(name, target.ID, version, size, publicURL))
		if err != nil {
			if strings.Contains(err.Error(), "Insufficient resources") {
				return fmt.Errorf("not enough resources to create the %s gateway %s: %w", strings.ToLower(kind), name, err)
			}
			return fmt.Errorf("setting up the %s gateway %s failed: %w", strings.ToLower(kind), name, err)
		}
		created = append(created, g.ID)
		return nil
	}
	ingressURL := ""
	switch target.Type {
	case "shared-space":
	case "runtime-fabric":
		ingressURL = strings.Replace(domains.Domains[0], "*", ingressName, 1)
	default:
		ingressURL = "https://" + strings.Replace(domains.Domains[0], "*", ingressName+"-"+domains.AppUniqueID, 1)
	}
	if err := provision("Ingress", ingressName, ingressSize, ingressURL); err != nil {
		return err
	}
	if egressName != "" {
		egressURL := ""
		if target.Type == "runtime-fabric" {
			egressURL = strings.Replace(domains.Domains[0], "*", egressName, 1)
		}
		if err := provision("Egress", egressName, "large", egressURL); err != nil {
			return err
		}
	}
	if len(created) > 0 {
		which := ingressName
		if egressName != "" {
			which += " and " + egressName
		}
		if *noWait {
			fmt.Fprintf(x.Err, "Check the gateways %s status manually by logging in to the Anypoint Platform.\n", which)
		} else {
			fmt.Fprintf(x.Err, "Waiting for %s up and running...\n", which)
			if err := waitGateways(x, c, org, env, created); err != nil {
				return err
			}
		}
	}
	return x.Done("Agent network gateways setup completed successfully.")
}

func waitGateways(x *Ctx, c *anypoint.Client, org, env string, ids []string) error {
	for {
		select {
		case <-x.ctx.Done():
			return x.ctx.Err()
		case <-time.After(agentGatewayPoll):
		}
		all := true
		for _, id := range ids {
			g, err := c.GetManagedGateway(x.ctx, org, env, id)
			if err != nil {
				return err
			}
			all = all && g.Running && g.Ready
		}
		if all {
			return nil
		}
	}
}

// reconcileGatewayVersion moves a gateway to its channel's latest runtime.
func reconcileGatewayVersion(x *Ctx, c *anypoint.Client, org, env, id string, noWait bool) error {
	cur, err := c.GetGatewayDocument(x.ctx, org, env, id)
	if err != nil {
		return err
	}
	versions, err := c.GetGatewayVersions(x.ctx)
	if err != nil {
		return err
	}
	latest := versions.LatestVersion(cur.ReleaseChannel)
	if latest == "" {
		return fmt.Errorf("no Flex Gateway versions found for release channel %s", cur.ReleaseChannel)
	}
	if anypoint.CompareVersions(latest, cur.RuntimeVersion) <= 0 {
		fmt.Fprintf(x.Err, "Gateway '%s' is already on the latest runtime version (%s). Skipping update...\n", cur.Name, cur.RuntimeVersion)
		return nil
	}
	fmt.Fprintf(x.Err, "Updating gateway '%s' from runtime version '%s' to '%s'...\n", cur.Name, cur.RuntimeVersion, latest)
	var doc struct {
		Configuration json.RawMessage `json:"configuration"`
	}
	if err := json.Unmarshal(cur.RawJSON(), &doc); err != nil {
		return err
	}
	body := anypoint.Doc{"name": cur.Name, "targetId": cur.TargetID, "releaseChannel": cur.ReleaseChannel, "runtimeVersion": latest,
		"size": cur.Size, "configuration": doc.Configuration}
	if _, err := c.UpdateManagedGateway(x.ctx, org, env, id, body); err != nil {
		return fmt.Errorf("updating gateway %s to %s failed: %w", cur.Name, latest, err)
	}
	if noWait {
		fmt.Fprintf(x.Err, "Check the gateway '%s' update status manually by logging in to the Anypoint Platform.\n", cur.Name)
		return nil
	}
	deadline := time.Now().Add(agentGatewayUpdateTimeout)
	for {
		g, err := c.GetGatewayDocument(x.ctx, org, env, id)
		if err != nil {
			return err
		}
		status := strings.ToUpper(g.Status)
		if (status == "APPLIED" || status == "RUNNING") && g.RuntimeVersion == latest {
			fmt.Fprintf(x.Err, "Gateway '%s' updated to runtime version '%s'.\n", cur.Name, latest)
			return nil
		}
		if status == "FAILED" {
			return fmt.Errorf("updating gateway %s to %s failed (status FAILED)", cur.Name, latest)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("updating gateway %s to %s timed out after %s", cur.Name, latest, agentGatewayUpdateTimeout)
		}
		select {
		case <-x.ctx.Done():
			return x.ctx.Err()
		case <-time.After(agentGatewayPoll):
		}
	}
}
