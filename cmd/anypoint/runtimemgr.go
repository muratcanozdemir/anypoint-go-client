package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/muratcanozdemir/anypoint-go-client/anypoint"
)

// anypoint-cli-runtime-mgr-plugin: runtime-mgr:* for hybrid runtimes,
// Runtime Fabric targets and managed Flex Gateways.

func runtimeMgrCmd() *Cmd {
	rm := group("runtime-mgr", "Runtime Manager",
		group("cluster", "Hybrid clusters",
			group("add", "Add to a cluster",
				leaf("server", "CLUSTERID SERVERID [SERVERIP]", "Add a server to a cluster (SERVERIP for unicast clusters)", cmdClusterAddServer)),
			leaf("create", "NAME", "Create a cluster", cmdClusterCreate),
			leaf("delete", "CLUSTERID", "Delete a cluster", cmdHybridDelete("Cluster", (*anypoint.Client).DeleteCluster)).alias("rm"),
			leaf("describe", "CLUSTERID", "Show a cluster", cmdGroupDescribe(true)),
			leaf("list", "", "List clusters", cmdGroupList(true)).alias("ls"),
			leaf("modify", "CLUSTERID", "Rename a cluster", cmdHybridRename("Cluster", (*anypoint.Client).RenameCluster)),
			group("remove", "Remove from a cluster",
				leaf("server", "CLUSTERID SERVERID", "Remove a server from a cluster", cmdClusterRemoveServer)),
		),
		group("gateways", "Flex Gateways",
			group("managed", "Managed Flex Gateways",
				leaf("create", "NAME TARGETID SIZE", "Create a managed gateway (SIZE: small or large)", cmdGatewayCreate),
				leaf("delete", "MANAGEDGATEWAYID", "Delete a managed gateway", cmdGatewayDelete).alias("rm"),
				leaf("describe", "MANAGEDGATEWAYID", "Show a managed gateway", cmdGatewayDescribe),
				leaf("edit", "MANAGEDGATEWAYID", "Edit a managed gateway", cmdGatewayEdit),
				leaf("list", "", "List managed gateways", cmdGatewayList).alias("ls"),
				leaf("start", "MANAGEDGATEWAYID", "Start a managed gateway", cmdGatewayStatus("STARTED")),
				leaf("stop", "MANAGEDGATEWAYID", "Stop a managed gateway", cmdGatewayStatus("STOPPED")),
			),
		),
		group("rtf", "Runtime Fabrics",
			leaf("list", "", "List Runtime Fabrics", cmdRTFList).alias("ls"),
		),
		group("server", "Hybrid servers",
			leaf("delete", "SERVERID", "Delete a server", cmdHybridDelete("Server", (*anypoint.Client).DeleteServer)).alias("rm"),
			leaf("describe", "SERVERID", "Show a server", cmdServerDescribe),
			leaf("list", "", "List servers", cmdServerList).alias("ls"),
			leaf("modify", "SERVERID", "Rename a server", cmdHybridRename("Server", (*anypoint.Client).RenameServer)),
			leaf("token", "", "Print the token to register a server", cmdServerToken),
		),
		group("serverGroup", "Hybrid server groups",
			group("add", "Add to a server group",
				leaf("server", "SERVERGROUPID SERVERID", "Add a server to a server group", cmdGroupServer(true))),
			leaf("create", "NAME [SERVERIDS...]", "Create a server group", cmdServerGroupCreate),
			leaf("delete", "SERVERGROUPID", "Delete a server group", cmdHybridDelete("Server group", (*anypoint.Client).DeleteServerGroup)).alias("rm"),
			leaf("describe", "SERVERGROUPID", "Show a server group", cmdGroupDescribe(false)),
			leaf("list", "", "List server groups", cmdGroupList(false)).alias("ls"),
			leaf("modify", "SERVERGROUPID", "Rename a server group", cmdHybridRename("Server group", (*anypoint.Client).RenameServerGroup)),
			group("remove", "Remove from a server group",
				leaf("server", "SERVERGROUPID SERVERID", "Remove a server from a server group", cmdGroupServer(false))),
		),
		group("standalone-alert", "Hybrid alerts",
			leaf("create", "NAME", "Create an alert", cmdHybridAlertCreate),
			leaf("describe", "ALERTID", "Show an alert", cmdHybridAlertDescribe),
			leaf("list", "", "List alerts", cmdHybridAlertList).alias("ls"),
			leaf("modify", "ALERTID", "Modify an alert", cmdHybridAlertModify),
		),
		group("standalone-application", "Hybrid applications",
			leaf("artifact", "IDENTIFIER DIRECTORY", "Download an application's archive", cmdHybridAppArtifact),
			leaf("copy", "SOURCE TARGET TARGETIDENTIFIER", "Copy an application ([org[:env]/]app) to a server, group or cluster", cmdHybridAppCopy),
			leaf("delete", "IDENTIFIER", "Undeploy an application", cmdHybridAppDelete).alias("rm"),
			leaf("deploy", "TARGETIDENTIFIER NAME ZIPFILE", "Deploy (or redeploy) an application to a server, group or cluster", cmdHybridAppDeploy),
			leaf("describe", "APPID", "Show an application", cmdHybridAppDescribe),
			leaf("list", "", "List applications", cmdHybridAppList).alias("ls"),
			leaf("modify", "IDENTIFIER [ZIPFILE]", "Redeploy an application or change its Insight and logging settings", cmdHybridAppModify),
			leaf("restart", "IDENTIFIER", "Restart an application", cmdHybridAppRestart),
			leaf("start", "IDENTIFIER", "Start an application", cmdHybridAppState("STARTED")),
			leaf("stop", "IDENTIFIER", "Stop an application", cmdHybridAppState("STOPPED")),
		),
	)
	rm.Sub = append(rm.Sub, cloudhubRuntimeMgrCmds()...)
	slices.SortFunc(rm.Sub, func(a, b *Cmd) int { return strings.Compare(a.Name, b.Name) })
	return rm
}

// agoAny renders an epoch-milliseconds or RFC 3339 time relative to now.
func agoAny(v any) string {
	switch t := v.(type) {
	case float64:
		return ago(time.UnixMilli(int64(t)).UTC().Format(time.RFC3339))
	case string:
		if n, err := strconv.ParseInt(t, 10, 64); err == nil {
			return agoAny(float64(n))
		}
		return ago(t)
	}
	return ""
}

// ---------------------------------------------------------------- servers, groups, clusters

func cmdServerList(x *Ctx, args []string) error {
	_, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	servers, err := c.ListServers(x.ctx, org, env)
	if err != nil {
		return err
	}
	return x.Print(servers, func() {
		t := newTable(x.Out, "ID", "NAME", "SERVER TYPE", "TYPE", "CLUSTER ID", "CLUSTER NAME", "SERVER GROUP ID", "SERVER GROUP NAME", "STATUS", "UPDATED")
		for _, s := range servers {
			t.row(s.ID.String(), s.Name, s.ServerType, s.Type, s.ClusterID.String(), s.ClusterName, s.ServerGroupID.String(), s.ServerGroupName, s.Status, agoAny(s.TimeUpdated))
		}
		t.flush()
	})
}

func cmdServerDescribe(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	s, err := c.GetServer(x.ctx, org, env, pos[0])
	if err != nil {
		return err
	}
	return x.Print(s, func() {
		kv := []string{"Id", s.ID.String(), "Name", s.Name, "Created", agoAny(s.TimeCreated), "Updated", agoAny(s.TimeUpdated),
			"Server Type", s.ServerType, "Type", s.Type, "Mule Version", s.MuleVersion, "Agent Version", s.AgentVersion,
			"Gateway Version", s.GatewayVersion, "Cluster Id", s.ClusterID.String(), "Cluster Name", s.ClusterName,
			"Server Group Id", s.ServerGroupID.String(), "Server Group Name", s.ServerGroupName, "Status", s.Status}
		for i, a := range s.Addresses {
			kv = append(kv, fmt.Sprintf("Address %d IP", i+1), a.IP, fmt.Sprintf("Address %d Network Interface", i+1), a.NetworkInterface)
		}
		printKV(x.Out, kv...)
	})
}

func cmdServerToken(x *Ctx, args []string) error {
	_, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	tok, err := c.GetServerRegistrationToken(x.ctx, org, env)
	if err != nil {
		return err
	}
	return x.Print(map[string]string{"data": tok}, func() { fmt.Fprintln(x.Out, tok) })
}

// ctxT abbreviates context.Context in method-expression types.
type ctxT = context.Context

type hybridFn func(*anypoint.Client, ctxT, string, string, string) error

func cmdHybridDelete(what string, del hybridFn) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		pos, c, org, env, err := apiSetup(x, x.Flags(), args)
		if err != nil {
			return err
		}
		if err := del(c, x.ctx, org, env, pos[0]); err != nil {
			return err
		}
		return x.Done("%s with ID %s deleted", what, pos[0])
	}
}

func cmdHybridRename(what string, rename func(*anypoint.Client, ctxT, string, string, string, string) error) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		fs := x.Flags()
		name := fs.String("name", "", "(required) new `name`")
		pos, c, org, env, err := apiSetup(x, fs, args)
		if err != nil {
			return err
		}
		if err := need("name", *name); err != nil {
			return err
		}
		if err := rename(c, x.ctx, org, env, pos[0], *name); err != nil {
			return err
		}
		return x.Done("%s with ID %q modified", what, pos[0])
	}
}

func cmdGroupList(cluster bool) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		_, c, org, env, err := apiSetup(x, x.Flags(), args)
		if err != nil {
			return err
		}
		list := c.ListServerGroups
		if cluster {
			list = c.ListClusters
		}
		groups, err := list(x.ctx, org, env)
		if err != nil {
			return err
		}
		return x.Print(groups, func() {
			heads := []string{"ID", "NAME", "SERVERS NUM", "SERVERS IDS", "TYPE", "STATUS", "UPDATED"}
			if cluster {
				heads = []string{"ID", "NAME", "MULTICAST", "SERVERS NUM", "SERVERS IDS", "PRIMARY NODE ID", "STATUS", "UPDATED"}
			}
			t := newTable(x.Out, heads...)
			for _, g := range groups {
				var ids []string
				for _, s := range g.Servers {
					ids = append(ids, s.ID.String())
				}
				n, sids := strconv.Itoa(len(g.Servers)), strings.Join(ids, ", ")
				if cluster {
					t.row(g.ID.String(), g.Name, enabled(g.MulticastEnabled), n, sids, g.PrimaryNodeID.String(), g.Status, agoAny(g.TimeUpdated))
				} else {
					t.row(g.ID.String(), g.Name, n, sids, g.Type, g.Status, agoAny(g.TimeUpdated))
				}
			}
			t.flush()
		})
	}
}

func enabled(b bool) string {
	if b {
		return "Enabled"
	}
	return "Disabled"
}

func cmdGroupDescribe(cluster bool) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		pos, c, org, env, err := apiSetup(x, x.Flags(), args)
		if err != nil {
			return err
		}
		get := c.GetServerGroup
		if cluster {
			get = c.GetCluster
		}
		g, err := get(x.ctx, org, env, pos[0])
		if err != nil {
			return err
		}
		return x.Print(g, func() {
			kv := []string{"Id", g.ID.String(), "Name", g.Name, "Created", agoAny(g.TimeCreated), "Updated", agoAny(g.TimeUpdated),
				"Status", g.Status, "Type", g.Type}
			if cluster {
				kv = append(kv, "Multicast", enabled(g.MulticastEnabled), "Primary Node ID", g.PrimaryNodeID.String())
			}
			kv = append(kv, "Servers Num", strconv.Itoa(len(g.Servers)))
			for i, s := range g.Servers {
				n := strconv.Itoa(i + 1)
				kv = append(kv, "Server "+n+" ID", s.ID.String(), "Server "+n+" Name", s.Name, "Server "+n+" Status", s.Status)
			}
			printKV(x.Out, kv...)
		})
	}
}

func cmdClusterCreate(x *Ctx, args []string) error {
	fs := x.Flags()
	multicast := fs.Bool("multicast", false, "enable multicast")
	servers := multi(fs, "server", "(required) server `ID[:IP]` (repeatable; the IP is needed for unicast clusters)")
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	if len(*servers) == 0 {
		return errors.New("missing required flag --server")
	}
	var members []anypoint.ClusterMember
	for _, s := range *servers {
		id, ip, _ := strings.Cut(s, ":")
		members = append(members, anypoint.ClusterMember{ServerID: id, ServerIP: ip})
	}
	cl, err := c.CreateCluster(x.ctx, org, env, pos[0], *multicast, members)
	if err != nil {
		return err
	}
	return x.Print(cl, func() { fmt.Fprintf(x.Out, "Created cluster with ID %s\n", cl.ID) })
}

func cmdClusterAddServer(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := c.AddServerToCluster(x.ctx, org, env, pos[0], anypoint.ClusterMember{ServerID: pos[1], ServerIP: arg(pos, 2)}); err != nil {
		return err
	}
	return x.Done("Server %s added to cluster %s", pos[1], pos[0])
}

func cmdClusterRemoveServer(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := c.RemoveServerFromCluster(x.ctx, org, env, pos[0], pos[1]); err != nil {
		return err
	}
	return x.Done("Server %s removed from cluster %s", pos[1], pos[0])
}

func cmdServerGroupCreate(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := c.CreateServerGroup(x.ctx, org, env, pos[0], pos[1:]); err != nil {
		return err
	}
	return x.Done("Server group created")
}

func cmdGroupServer(add bool) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		pos, c, org, env, err := apiSetup(x, x.Flags(), args)
		if err != nil {
			return err
		}
		if add {
			if err := c.AddServerToGroup(x.ctx, org, env, pos[0], pos[1]); err != nil {
				return err
			}
			return x.Done("Server %s added to server group %s", pos[1], pos[0])
		}
		if err := c.RemoveServerFromGroup(x.ctx, org, env, pos[0], pos[1]); err != nil {
			return err
		}
		return x.Done("Server %s removed from server group %s", pos[1], pos[0])
	}
}

func cmdRTFList(x *Ctx, args []string) error {
	_, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	targets, err := c.ListRuntimeTargets(x.ctx, org, env)
	if err != nil {
		return err
	}
	return x.Print(targets, func() {
		t := newTable(x.Out, "ID", "NAME", "VERSION", "AVAILABLE", "PROVIDER")
		for _, r := range targets {
			t.row(r.ID, r.Name, r.Version, yn(r.IsAvailableForDeployments), r.Provider)
		}
		t.flush()
	})
}

// ---------------------------------------------------------------- applications

func cmdHybridAppList(x *Ctx, args []string) error {
	fs := x.Flags()
	limit, offset, sortBy := paginationFlags(fs, "timeCreated")
	_, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	apps, err := c.ListHybridApps(x.ctx, org, env, anypoint.HybridListOptions{Limit: *limit, Offset: *offset, Sort: *sortBy})
	if err != nil {
		return err
	}
	return x.Print(apps, func() {
		t := newTable(x.Out, "ID", "NAME", "TARGET ID", "TARGET NAME", "STATUS", "UPDATED")
		for _, a := range apps {
			t.row(a.ID.String(), a.Artifact.Name, a.Target.ID.String(), a.Target.Name, a.LastReportedStatus, agoAny(a.TimeUpdated))
		}
		t.flush()
	})
}

// findApp finds an application by ID or name; nil (with a message) when absent.
func findHybridApp(x *Ctx, c *anypoint.Client, org, env, ident string) (*anypoint.HybridApp, error) {
	a, err := c.FindHybridApp(x.ctx, org, env, ident)
	if anypoint.IsNotFound(err) {
		return nil, fmt.Errorf("application identified by %s not found", ident)
	}
	return a, err
}

func cmdHybridAppDescribe(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	a, err := findHybridApp(x, c, org, env, pos[0])
	if err != nil {
		return err
	}
	return x.Print(a, func() {
		printKV(x.Out, "Id", a.ID.String(), "Name", a.Name, "Status", a.LastReportedStatus, "Desired Status", a.DesiredStatus,
			"Created", agoAny(a.TimeCreated), "Updated", agoAny(a.TimeUpdated), "File Name", a.Artifact.FileName,
			"File Updated", agoAny(a.Artifact.TimeUpdated), "File Checksum", a.Artifact.FileChecksum,
			"Target ID", a.Target.ID.String(), "Target Name", a.Target.Name, "Target Type", a.Target.Type, "Target Status", a.Target.Status)
	})
}

func cmdHybridAppState(status string) func(*Ctx, []string) error {
	return func(x *Ctx, args []string) error {
		pos, c, org, env, err := apiSetup(x, x.Flags(), args)
		if err != nil {
			return err
		}
		a, err := findHybridApp(x, c, org, env, pos[0])
		if err != nil {
			return err
		}
		if err := c.SetHybridAppStatus(x.ctx, org, env, a.ID.String(), status); err != nil {
			return err
		}
		return x.Done("Application state updated")
	}
}

func cmdHybridAppRestart(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	a, err := findHybridApp(x, c, org, env, pos[0])
	if err != nil {
		return err
	}
	if err := c.RestartHybridApp(x.ctx, org, env, a.ID.String()); err != nil {
		return err
	}
	return x.Done("Restarting %s", a.ID)
}

func cmdHybridAppDelete(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	a, err := findHybridApp(x, c, org, env, pos[0])
	if err != nil {
		return err
	}
	if err := c.DeleteHybridApp(x.ctx, org, env, a.ID.String()); err != nil {
		return err
	}
	return x.Done("Application with ID %s deleted", a.ID)
}

func cmdHybridAppArtifact(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(pos[1], 0o750); err != nil {
		return err
	}
	a, err := findHybridApp(x, c, org, env, pos[0])
	if err != nil {
		return err
	}
	dst := filepath.Join(pos[1], safeFileName(fmt.Sprintf("app_%s_artifact.zip", a.ID)))
	fmt.Fprintf(x.Err, "Downloading artifact of app %s to %s\n", a.ID, dst)
	body, err := c.DownloadHybridAppArtifact(x.ctx, org, env, a.ID.String())
	if err != nil {
		return err
	}
	defer body.Close()
	if _, err := writeFile(dst, body); err != nil {
		return err
	}
	return x.Done("Artifact downloaded to %s", dst)
}

// hybridTarget finds a server, server group or cluster by ID or name.
func hybridTargetByIdent(x *Ctx, c *anypoint.Client, org, env, ident string) (string, error) {
	servers, err := c.ListServers(x.ctx, org, env)
	if err != nil {
		return "", err
	}
	groups, err := c.ListServerGroups(x.ctx, org, env)
	if err != nil {
		return "", err
	}
	clusters, err := c.ListClusters(x.ctx, org, env)
	if err != nil {
		return "", err
	}
	for _, s := range servers {
		if s.Name == ident || s.ID.String() == ident {
			return s.ID.String(), nil
		}
	}
	for _, g := range append(groups, clusters...) {
		if g.Name == ident || g.ID.String() == ident {
			return g.ID.String(), nil
		}
	}
	return "", fmt.Errorf("target identified by %q not found", ident)
}

func openArchive(p string) (anypoint.HybridFile, error) {
	b, err := os.ReadFile(expandHome(p))
	if err != nil {
		return anypoint.HybridFile{}, err
	}
	return anypoint.HybridFile{Name: filepath.Base(p), Content: bytes.NewReader(b)}, nil
}

func cmdHybridAppDeploy(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	file, err := openArchive(pos[2])
	if err != nil {
		return err
	}
	targetID, err := hybridTargetByIdent(x, c, org, env, pos[0])
	if err != nil {
		return err
	}
	apps, err := c.ListHybridApps(x.ctx, org, env, anypoint.HybridListOptions{TargetID: targetID})
	if err != nil {
		return err
	}
	for _, a := range apps {
		if a.Name == pos[1] && a.Target.ID.String() == targetID {
			fmt.Fprintf(x.Err, "Redeploying %s with a new artifact...\n", a.ID)
			if _, err := c.RedeployHybridApp(x.ctx, org, env, a.ID.String(), file); err != nil {
				return err
			}
			return x.Done("Application with ID %q redeployed", a.ID)
		}
	}
	fmt.Fprintf(x.Err, "Deploying %s ...\n", pos[1])
	a, err := c.DeployHybridApp(x.ctx, org, env, pos[1], targetID, file)
	if err != nil {
		return err
	}
	return x.Print(a, func() { fmt.Fprintf(x.Out, "Application with ID %s created\n", a.ID) })
}

var supportedLogLevels = []string{"TRACE", "DEBUG", "INFO", "WARN", "ERROR", "FATAL", "OFF"}

const (
	analyticsHandler = "mule.agent.tracking.handler.analytics"
	trackingService  = "mule.agent.tracking.service"
	loggingService   = "mule.agent.logging.service"
)

// components indexes a target's agent components by name.
func components(x *Ctx, c *anypoint.Client, org, env, targetID string) (map[string]anypoint.TargetComponent, error) {
	cs, err := c.ListTargetComponents(x.ctx, org, env, targetID)
	if err != nil {
		return nil, err
	}
	out := map[string]anypoint.TargetComponent{}
	for _, cmp := range cs {
		out[cmp.Component.Name] = cmp
	}
	return out, nil
}

func component(cmps map[string]anypoint.TargetComponent, name string) (anypoint.TargetComponent, error) {
	cmp, ok := cmps[name]
	if !ok {
		return cmp, fmt.Errorf("the target has no %s agent component", name)
	}
	return cmp, nil
}

// confList returns a component configuration's list without the entries of app.
func confList(cmp anypoint.TargetComponent, key, nameKey, app string) []any {
	out := []any{}
	items, _ := cmp.Configuration[key].([]any)
	for _, it := range items {
		if m, ok := it.(map[string]any); ok && m[nameKey] == app {
			continue
		}
		out = append(out, it)
	}
	return out
}

// setInsight turns Insight (analytics handler and tracking) on or off for an app.
func setInsight(x *Ctx, c *anypoint.Client, org, env string, a *anypoint.HybridApp, cmps map[string]anypoint.TargetComponent, analytics, tracked bool) error {
	an, err := component(cmps, analyticsHandler)
	if err != nil {
		return err
	}
	if err := c.UpdateTargetComponent(x.ctx, org, env, a.Target.ID.String(), an.Component.ID.String(), anypoint.Doc{"enabled": analytics}); err != nil {
		return err
	}
	tr, err := component(cmps, trackingService)
	if err != nil {
		return err
	}
	apps := confList(tr, "trackedApplications", "appName", a.Name)
	body := anypoint.Doc{}
	if tracked {
		body["enabled"] = true
		apps = append(apps, anypoint.Doc{"appName": a.Name, "trackingLevel": "DEBUG"})
	}
	body["configuration"] = anypoint.Doc{"trackedApplications": apps}
	return c.UpdateTargetComponent(x.ctx, org, env, a.Target.ID.String(), tr.Component.ID.String(), body)
}

// setLogging replaces an app's logging scopes.
func setLogging(x *Ctx, c *anypoint.Client, org, env string, a *anypoint.HybridApp, cmps map[string]anypoint.TargetComponent, scopes []any) error {
	lg, err := component(cmps, loggingService)
	if err != nil {
		return err
	}
	levels := confList(lg, "applicationLogLevels", "artifactName", a.Name)
	levels = append(levels, anypoint.Doc{"artifactName": a.Name, "scopeLoggingConfigurations": scopes})
	return c.UpdateTargetComponent(x.ctx, org, env, a.Target.ID.String(), lg.Component.ID.String(),
		anypoint.Doc{"configuration": anypoint.Doc{"applicationLogLevels": levels}})
}

func cmdHybridAppModify(x *Ctx, args []string) error {
	fs := x.Flags()
	insight := fs.Bool("insight", false, "enable Insight for the application")
	noInsight := fs.Bool("no-insight", false, "disable Insight for the application")
	logOpt := fs.String("log", "", "log level for a scope, `LEVEL:scope` (e.g. DEBUG:org.mule); levels: "+strings.Join(supportedLogLevels, ", "))
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	a, err := findHybridApp(x, c, org, env, pos[0])
	if err != nil {
		return err
	}
	if zip := arg(pos, 1); zip != "" {
		file, err := openArchive(zip)
		if err != nil {
			return err
		}
		fmt.Fprintf(x.Err, "Redeploying %s with a new artifact...\n", a.ID)
		updated, err := c.RedeployHybridApp(x.ctx, org, env, a.ID.String(), file)
		if err != nil {
			return err
		}
		fmt.Fprintf(x.Err, "Application with ID %q redeployed\n", a.ID)
		if updated.ID != "" {
			a = updated
		}
	}
	if !*insight && !*noInsight && *logOpt == "" {
		return x.Done("Application %s modified", a.ID)
	}
	var scope []any
	if *logOpt != "" {
		level, sc, _ := strings.Cut(*logOpt, ":")
		if !slices.Contains(supportedLogLevels, level) {
			return fmt.Errorf("not supported log level %q. Supported log levels: %s", level, strings.Join(supportedLogLevels, ", "))
		}
		scope = []any{anypoint.Doc{"logLevel": level, "scope": sc}}
	}
	cmps, err := components(x, c, org, env, a.Target.ID.String())
	if err != nil {
		return err
	}
	if *insight || *noInsight {
		if err := setInsight(x, c, org, env, a, cmps, *insight, *insight); err != nil {
			return err
		}
		fmt.Fprintf(x.Err, "Insight %s\n", map[bool]string{true: "enabled", false: "disabled"}[*insight])
	}
	if scope != nil {
		if err := setLogging(x, c, org, env, a, cmps, scope); err != nil {
			return err
		}
		fmt.Fprintln(x.Err, "Logging setting modified")
	}
	return x.Done("Application %s modified", a.ID)
}

// appRef is "[org[:env]/]app" as anypoint-cli-v4 parses it.
type appRef struct{ org, env, app string }

func parseAppRef(raw string) appRef {
	if !strings.Contains(raw, ":") && strings.Count(raw, "/") <= 1 {
		if o, a, ok := strings.Cut(raw, "/"); ok {
			return appRef{org: o, app: a}
		}
		return appRef{app: raw}
	}
	scope, app, _ := strings.Cut(raw, "/")
	o, e, _ := strings.Cut(scope, ":")
	return appRef{org: o, env: e, app: app}
}

// scope resolves the organization and environment of an app reference,
// defaulting to the selected ones.
func (r appRef) scope(x *Ctx, c *anypoint.Client, org, env string) (string, string, error) {
	if r.org != "" {
		id, err := findBusinessGroup(x, c, r.org)
		if err != nil {
			return "", "", err
		}
		if id != org && r.env == "" {
			return "", "", fmt.Errorf("give the environment of organization %s as %s:ENV/%s", r.org, r.org, r.app)
		}
		org = id
	}
	if r.env != "" {
		envs, err := c.ListEnvironments(x.ctx, org)
		if err != nil {
			return "", "", err
		}
		found := ""
		for _, e := range envs {
			if e.ID == r.env || strings.EqualFold(e.Name, r.env) {
				found = e.ID
			}
		}
		if found == "" {
			return "", "", fmt.Errorf("environment %s not found", r.env)
		}
		env = found
	}
	return org, env, nil
}

// pollInterval is how often copy checks the new application's status.
var pollInterval = time.Second

func cmdHybridAppCopy(x *Ctx, args []string) (err error) {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	src, dst := parseAppRef(pos[0]), parseAppRef(pos[1])
	sOrg, sEnv, err := src.scope(x, c, org, env)
	if err != nil {
		return err
	}
	tOrg, tEnv, err := dst.scope(x, c, org, env)
	if err != nil {
		return err
	}
	byName := func(o, e, name string) (*anypoint.HybridApp, error) {
		apps, err := c.ListHybridApps(x.ctx, o, e, anypoint.HybridListOptions{})
		if err != nil {
			return nil, err
		}
		for i := range apps {
			if apps[i].Name == name {
				return &apps[i], nil
			}
		}
		return nil, nil
	}
	if existing, _ := byName(tOrg, tEnv, dst.app); existing != nil {
		return fmt.Errorf("application named %s already exists", dst.app)
	}
	fmt.Fprintln(x.Err, "Loading source application")
	sApp, err := byName(sOrg, sEnv, src.app)
	if err != nil {
		return fmt.Errorf("source app: %w", err)
	}
	if sApp == nil {
		return fmt.Errorf("source application named %q does not exist", src.app)
	}
	body, err := c.DownloadHybridAppArtifact(x.ctx, sOrg, sEnv, sApp.ID.String())
	if err != nil {
		return err
	}
	zip, err := readAllClose(body)
	if err != nil {
		return err
	}
	targetID, err := hybridTargetByIdent(x, c, tOrg, tEnv, pos[2])
	if err != nil {
		return err
	}
	fmt.Fprintln(x.Err, "Copying source application to target")
	name := sApp.Artifact.FileName
	if name == "" {
		name = dst.app + ".zip"
	}
	tApp, err := c.DeployHybridApp(x.ctx, tOrg, tEnv, dst.app, targetID, anypoint.HybridFile{Name: name, Content: bytes.NewReader(zip)})
	if err != nil {
		return err
	}
	fmt.Fprintf(x.Err, "Application copied with ID: %s\n", tApp.ID)
	defer func() { // undo a half-done copy
		if err != nil {
			_ = c.DeleteHybridApp(x.ctx, tOrg, tEnv, tApp.ID.String())
		}
	}()
	fmt.Fprintln(x.Err, "Waiting for application to set up")
	for {
		cur, err := c.GetHybridApp(x.ctx, tOrg, tEnv, tApp.ID.String())
		if err != nil {
			return err
		}
		if slices.Contains([]string{"STOPPED", "STARTED", "DEPLOYMENT_FAILED"}, cur.LastReportedStatus) {
			tApp = cur
			break
		}
		select {
		case <-x.ctx.Done():
			return x.ctx.Err()
		case <-time.After(pollInterval):
		}
	}
	want := sApp.DesiredStatus
	if want == "UPDATED" {
		want = "STARTED"
	}
	if want != "" && tApp.DesiredStatus != want {
		fmt.Fprintf(x.Err, "Updating target application state: %s\n", want)
		if err := c.SetHybridAppStatus(x.ctx, tOrg, tEnv, tApp.ID.String(), want); err != nil {
			return err
		}
	}
	fmt.Fprintln(x.Err, "Copying insight & logging settings")
	sCmps, err := components(x, c, sOrg, sEnv, sApp.Target.ID.String())
	if err != nil {
		return err
	}
	tCmps, err := components(x, c, tOrg, tEnv, tApp.Target.ID.String())
	if err != nil {
		return err
	}
	an, err := component(sCmps, analyticsHandler)
	if err != nil {
		return err
	}
	tr, err := component(sCmps, trackingService)
	if err != nil {
		return err
	}
	tracked := len(confList(tr, "trackedApplications", "appName", sApp.Name)) != len(listOf(tr.Configuration["trackedApplications"]))
	if err := setInsight(x, c, tOrg, tEnv, tApp, tCmps, an.Enabled, tracked); err != nil {
		return err
	}
	lg, err := component(sCmps, loggingService)
	if err != nil {
		return err
	}
	for _, it := range listOf(lg.Configuration["applicationLogLevels"]) {
		if m, ok := it.(map[string]any); ok && m["artifactName"] == sApp.Name {
			scopes := listOf(m["scopeLoggingConfigurations"])
			if err := setLogging(x, c, tOrg, tEnv, tApp, tCmps, scopes); err != nil {
				return err
			}
		}
	}
	return x.Print(tApp, func() { fmt.Fprintf(x.Out, "Application copied with ID: %s\n", tApp.ID) })
}

func listOf(v any) []any {
	l, _ := v.([]any)
	if l == nil {
		return []any{}
	}
	return l
}

// ---------------------------------------------------------------- alerts

var hybridAlertConditions = map[string][]string{
	"hybrid-application": {"application-undeployed", "application-failure", "application-success", "application-message-count",
		"application-error-count", "application-response-time"},
	"server": {"agent-version-change", "runtime-version-change", "server-cpu", "server-load-average", "server-memory",
		"server-thread-count", "server-up", "server-deletion", "server-down"},
	"server-group": {"server-group-up", "server-group-deletion", "server-group-down", "server-group-member-cpu",
		"server-group-member-load-average", "server-group-member-memory", "server-group-member-thread-count",
		"server-group-member-up", "server-group-member-down", "server-group-member-added", "server-group-member-removed"},
	"cluster": {"cluster-up", "cluster-deletion", "cluster-down", "cluster-visibility-change", "cluster-member-cpu",
		"cluster-member-load-average", "cluster-member-memory", "cluster-member-thread-count", "cluster-member-up",
		"cluster-member-down", "cluster-member-added", "cluster-member-removed"},
}

// hybridAlertFlags registers the alert flags and returns the body builder.
func hybridAlertFlags(fs *flag.FlagSet) func(x *Ctx, c *anypoint.Client, org, name string) (anypoint.Doc, error) {
	var resTypes, conds []string
	for rt, cs := range hybridAlertConditions {
		resTypes = append(resTypes, rt)
		conds = append(conds, cs...)
	}
	slices.Sort(resTypes)
	slices.Sort(conds)
	alertName := fs.String("name", "", "alert `name`")
	severity := fs.String("severity", "", "`severity`: INFO, WARNING, CRITICAL")
	resourceType := fs.String("resourceType", "", "resource `type`: "+strings.Join(resTypes, ", "))
	resource := fs.String("resource", "", "resource `name or ID` (default: all)")
	condition := fs.String("condition", "", "alert `condition`, valid per resource type (e.g. server-down, application-failure)")
	period := fs.Int("period", 0, "condition period in `minutes`")
	threshold := fs.Int("threshold", 0, "condition `threshold`")
	operator := fs.String("operator", "", "threshold `operator`: gt or lt")
	subject := fs.String("subject", "", "notification `subject`")
	content := fs.String("content", "", "notification `content`")
	recipients := multi(fs, "recipient", "`username` to notify (repeatable)")
	emails := multi(fs, "email", "`address` to notify (repeatable)")
	return func(x *Ctx, c *anypoint.Client, org, name string) (anypoint.Doc, error) {
		for _, chk := range [][]string{{"severity", *severity, "INFO", "WARNING", "CRITICAL"}, append([]string{"resourceType", *resourceType}, resTypes...),
			append([]string{"condition", *condition}, conds...), {"operator", *operator, "gt", "lt"}} {
			if err := oneOf(chk[0], chk[1], chk[2:]...); err != nil {
				return nil, err
			}
		}
		if *alertName != "" {
			name = *alertName
		}
		action := anypoint.Doc{"type": "email"}
		if *subject != "" {
			action["subject"] = *subject
		}
		if *content != "" {
			action["content"] = *content
		}
		cond := anypoint.Doc{"resources": []string{"*"}}
		if *resourceType != "" {
			cond["resourceType"] = *resourceType
		}
		if *condition != "" {
			cond["type"] = *condition
		}
		if *resource != "" {
			cond["resources"] = []string{*resource}
		}
		if *period != 0 {
			cond["period"] = *period
		}
		if *threshold != 0 {
			cond["threshold"] = *threshold
		}
		if *operator != "" {
			cond["operator"] = map[string]string{"gt": "GREATER_THAN", "lt": "LESS_THAN"}[*operator]
		}
		if len(*emails) > 0 {
			action["emails"] = []string(*emails)
		}
		if len(*recipients) > 0 {
			members, err := c.ListOrganizationMembers(x.ctx, org)
			if err != nil {
				return nil, err
			}
			ids := []string{}
			for _, u := range *recipients {
				for _, m := range members {
					if m.Username == u {
						ids = append(ids, m.ID)
					}
				}
			}
			action["userIds"] = ids
		}
		body := anypoint.Doc{"name": name, "condition": cond, "actions": []any{action}}
		if *severity != "" {
			body["severity"] = *severity
		}
		return body, nil
	}
}

func cmdHybridAlertCreate(x *Ctx, args []string) error {
	fs := x.Flags()
	build := hybridAlertFlags(fs)
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	body, err := build(x, c, org, pos[0])
	if err != nil {
		return err
	}
	if err := c.CreateHybridAlert(x.ctx, org, env, body); err != nil {
		return err
	}
	return x.Done("Hybrid alert created")
}

// merge deep-merges src into dst like lodash's merge: objects recursively,
// arrays index by index.
func merge(dst, src any) any {
	switch s := src.(type) {
	case map[string]any:
		d, ok := dst.(map[string]any)
		if !ok {
			d = map[string]any{}
		}
		for k, v := range s {
			d[k] = merge(d[k], v)
		}
		return d
	case []any:
		d, _ := dst.([]any)
		out := append([]any{}, d...)
		for i, v := range s {
			if i < len(out) {
				out[i] = merge(out[i], v)
			} else {
				out = append(out, merge(nil, v))
			}
		}
		return out
	}
	return src
}

// normalize turns typed values (Doc, []string, []any of Docs) into plain
// JSON values so merge sees maps and slices.
func normalize(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

func cmdHybridAlertModify(x *Ctx, args []string) error {
	fs := x.Flags()
	build := hybridAlertFlags(fs)
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	cur, err := c.GetHybridAlert(x.ctx, org, env, pos[0])
	if err != nil {
		return err
	}
	body, err := build(x, c, org, cur.Name)
	if err != nil {
		return err
	}
	var current map[string]any
	if err := json.Unmarshal(cur.RawJSON(), &current); err != nil {
		return err
	}
	for _, k := range []string{"id", "enabled", "productName", "organizationId", "environmentId", "lastModified", "isSystem", "createdAt"} {
		delete(current, k)
	}
	if err := c.UpdateHybridAlert(x.ctx, org, env, pos[0], merge(current, normalize(body))); err != nil {
		return err
	}
	return x.Done("Hybrid alert modified")
}

func cmdHybridAlertDescribe(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	a, err := c.GetHybridAlert(x.ctx, org, env, pos[0])
	if err != nil {
		return err
	}
	return x.Print(a, func() {
		var typ, subject, content string
		var emails, users []string
		if len(a.Actions) > 0 {
			act := a.Actions[0]
			typ, subject, content, emails, users = act.Type, act.Subject, act.Content, act.Emails, act.UserIDs
		}
		printKV(x.Out, "Id", a.ID.String(), "Created", agoAny(a.TimeCreated), "Updated", agoAny(a.TimeUpdated), "Name", a.Name,
			"Description", a.Description, "Enabled", yesNo(a.Enabled), "Severity", a.Severity, "Product", a.ProductName,
			"Condition Type", a.Condition.Type, "Condition Event", a.Condition.Event, "Condition Resource Type", a.Condition.ResourceType,
			"Condition Resources", strings.Join(a.Condition.Resources, ", "), "Notification Type", typ,
			"Notification Recipients (Emails)", strings.Join(emails, ", "), "Notification Recipients (User IDs)", strings.Join(users, ", "),
			"Notification Subject", subject, "Notification Content", content)
	})
}

func cmdHybridAlertList(x *Ctx, args []string) error {
	fs := x.Flags()
	limit, offset, sortBy := paginationFlags(fs, "createdAt")
	_, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	alerts, err := c.ListHybridAlerts(x.ctx, org, env, anypoint.HybridListOptions{Limit: *limit, Offset: *offset, Sort: *sortBy})
	if err != nil {
		return err
	}
	return x.Print(alerts, func() {
		t := newTable(x.Out, "NAME", "ID", "TYPE", "SEVERITY", "SOURCE", "SOURCE TYPE", "ENABLED", "UPDATED")
		for _, a := range alerts {
			t.row(a.Name, a.ID.String(), a.Condition.Type, a.Severity, strings.Join(a.Condition.Resources, ", "), a.Condition.ResourceType,
				yesNo(a.Enabled), agoAny(a.LastModified))
		}
		t.flush()
	})
}

// ---------------------------------------------------------------- managed gateways

func cmdGatewayList(x *Ctx, args []string) error {
	_, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	gws, err := c.ListGateways(x.ctx, org, env, "managed")
	if err != nil {
		return err
	}
	return x.Print(gws, func() {
		t := newTable(x.Out, "ID", "NAME", "TARGET NAME", "STATUS", "LAST UPDATED")
		for _, g := range gws {
			t.row(g.ID, g.Name, g.TargetName, g.Status, g.LastUpdated)
		}
		t.flush()
	})
}

func printGateway(x *Ctx, g *anypoint.Gateway) error {
	return x.Print(g, func() {
		cf := g.Configuration
		var urls []string
		for u := range strings.SplitSeq(cf.Ingress.PublicURL, ",") {
			if u = strings.TrimSpace(u); u != "" {
				urls = append(urls, u)
			}
		}
		printKV(x.Out, "ID", g.ID, "Name", g.Name, "Target Name", g.TargetName, "Target ID", g.TargetID, "Target Type", g.TargetType,
			"Status", g.Status, "Date Created", g.DateCreated, "Last Updated", g.LastUpdated, "Runtime Version", g.RuntimeVersion,
			"Release Channel", g.ReleaseChannel, "Size", g.Size, "Logging Level", cf.Logging.Level,
			"Forward Logs", strconv.FormatBool(cf.Logging.ForwardLogs), "Forward SSL Session", strconv.FormatBool(cf.Ingress.ForwardSSLSession),
			"Last Mile Security", strconv.FormatBool(cf.Ingress.LastMileSecurity), "Public URLs", strings.Join(urls, ", "),
			"Internal URL", cf.Ingress.InternalURL,
			"Connection Idle Timeout", strconv.Itoa(cf.Properties.ConnectionIdleTimeout),
			"Upstream Response Timeout", strconv.Itoa(cf.Properties.UpstreamResponseTimeout))
	})
}

func cmdGatewayDescribe(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	g, err := c.GetManagedGateway(x.ctx, org, env, pos[0])
	if err != nil {
		return err
	}
	return printGateway(x, g)
}

// gatewayFlags registers the managed gateway flags. Like anypoint-cli-v4,
// each falls back to an environment variable (PUBLIC_URL, LOGGING_LEVEL, ...)
// and, for create, to a default.
type gatewayFlags struct {
	fs                                       *flag.FlagSet
	publicURL, loggingLevel, labels, version *string
	channel                                  *string
	upstreamTimeout, idleTimeout, sampling   *int
	lastMile, noLastMile, fwdSSL, noFwdSSL   *bool
	fwdLogs, noFwdLogs, tracing, noTracing   *bool
	create                                   bool
	envSet                                   map[string]bool
}

var gatewayEnv = map[string]string{"publicUrl": "PUBLIC_URL", "lastMileSecurity": "LAST_MILE_SECURITY",
	"forwardSslSession": "FORWARD_SSL_SESSION", "upstreamResponseTimeout": "UPSTREAM_RESPONSE_TIMEOUT",
	"connectionIdleTimeout": "CONNECTION_IDLE_TIMEOUT", "loggingLevel": "LOGGING_LEVEL", "forwardLogs": "FORWARD_LOGS",
	"tracingEnabled": "TRACING_ENABLED", "tracingSampling": "TRACING_SAMPLING", "tracingLabels": "TRACING_LABELS",
	"version": "RUNTIME_VERSION", "releaseChannel": "RELEASE_CHANNEL"}

func newGatewayFlags(fs *flag.FlagSet, create bool) *gatewayFlags {
	g := &gatewayFlags{fs: fs, create: create, envSet: map[string]bool{}}
	str := func(name, def, usage string) *string {
		if v, ok := os.LookupEnv(gatewayEnv[name]); ok {
			def = v
			g.envSet[name] = true
		} else if !create {
			def = ""
		}
		return fs.String(name, def, usage+" (env "+gatewayEnv[name]+")")
	}
	num := func(name string, def int, usage string) *int {
		if v, ok := os.LookupEnv(gatewayEnv[name]); ok {
			if n, err := strconv.Atoi(v); err == nil {
				def = n
				g.envSet[name] = true
			}
		}
		return fs.Int(name, def, usage+" (env "+gatewayEnv[name]+")")
	}
	boolean := func(name string, def bool, usage string) (*bool, *bool) {
		if v, ok := os.LookupEnv(gatewayEnv[name]); ok {
			if b, err := strconv.ParseBool(v); err == nil {
				def = b
				g.envSet[name] = true
			}
		}
		return fs.Bool(name, def, usage+" (env "+gatewayEnv[name]+")"), fs.Bool("no-"+name, false, "turn off --"+name)
	}
	g.publicURL = str("publicUrl", "", "public `URL`s, comma-separated")
	g.lastMile, g.noLastMile = boolean("lastMileSecurity", false, "use TLS between the gateway and upstreams")
	g.fwdSSL, g.noFwdSSL = boolean("forwardSslSession", false, "forward the client TLS session")
	g.upstreamTimeout = num("upstreamResponseTimeout", 15, "upstream response timeout in `seconds`")
	g.idleTimeout = num("connectionIdleTimeout", 60, "connection idle timeout in `seconds`")
	g.loggingLevel = str("loggingLevel", "info", "logging `level`: debug, info, warn, error, fatal")
	g.fwdLogs, g.noFwdLogs = boolean("forwardLogs", true, "forward logs to Anypoint Monitoring")
	g.tracing, g.noTracing = boolean("tracingEnabled", false, "enable tracing")
	g.sampling = num("tracingSampling", 1, "tracing sampling `percentage`")
	g.labels = str("tracingLabels", "[]", "tracing labels as a JSON `array`")
	g.version = str("version", "", "runtime `version`, or latest (default: the channel's latest)")
	chUsage := "release `channel`: edge or lts"
	if create {
		chUsage = "(required) " + chUsage
	}
	g.channel = str("releaseChannel", "", chUsage)
	if !create {
		*g.channel = ""
		if v, ok := os.LookupEnv("RELEASE_CHANNEL"); ok {
			*g.channel = v
		}
	}
	return g
}

// given reports whether a flag was passed, or set through its environment variable.
func (g *gatewayFlags) given(set map[string]bool, name string) bool {
	return set[name] || set["no-"+name] || g.envSet[name]
}

func (g *gatewayFlags) boolValue(on, off *bool) bool { return *on && !*off }

func (g *gatewayFlags) validate() error {
	if err := oneOf("loggingLevel", *g.loggingLevel, "debug", "info", "warn", "error", "fatal"); err != nil {
		return err
	}
	return oneOf("releaseChannel", *g.channel, "edge", "lts")
}

func parseLabels(s string) (any, error) {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, fmt.Errorf("invalid format for tracingLabels: %w", err)
	}
	return v, nil
}

// runtimeVersionFor resolves "" / "latest" to a channel's newest version and
// checks an explicit version exists in the channel.
func runtimeVersionFor(x *Ctx, c *anypoint.Client, version, channel string) (string, error) {
	vs, err := c.GetGatewayVersions(x.ctx)
	if err != nil {
		return "", err
	}
	if version == "" || version == "latest" {
		v := vs.LatestVersion(channel)
		if v == "" {
			return "", fmt.Errorf("no versions found for release channel: %s", channel)
		}
		return v, nil
	}
	if !slices.Contains(vs.Versions(channel), version) {
		return "", fmt.Errorf("invalid runtime version %q. Available versions for release channel %q are: %s",
			version, channel, strings.Join(vs.Versions(channel), ", "))
	}
	return version, nil
}

func cmdGatewayCreate(x *Ctx, args []string) error {
	fs := x.Flags()
	g := newGatewayFlags(fs, true)
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	if err := need("releaseChannel", *g.channel); err != nil {
		return err
	}
	if err := g.validate(); err != nil {
		return err
	}
	version, err := runtimeVersionFor(x, c, *g.version, *g.channel)
	if err != nil {
		return err
	}
	targets, err := c.ListGatewayTargets(x.ctx, org, env)
	if err != nil {
		return err
	}
	var ids []string
	for _, t := range targets {
		ids = append(ids, t.ID)
	}
	if !slices.Contains(ids, pos[1]) {
		return fmt.Errorf("target with id %s not found. Available targets are: %s", pos[1], strings.Join(ids, ", "))
	}
	tracingOn := g.boolValue(g.tracing, g.noTracing)
	tracing := anypoint.Doc{"enabled": tracingOn}
	if tracingOn {
		labels, err := parseLabels(*g.labels)
		if err != nil {
			return err
		}
		tracing["sampling"], tracing["labels"] = *g.sampling, labels
	}
	body := anypoint.Doc{"name": pos[0], "targetId": pos[1], "runtimeVersion": version, "releaseChannel": *g.channel, "size": pos[2],
		"configuration": anypoint.Doc{
			"ingress": anypoint.Doc{"publicUrl": *g.publicURL, "lastMileSecurity": g.boolValue(g.lastMile, g.noLastMile),
				"forwardSslSession": g.boolValue(g.fwdSSL, g.noFwdSSL)},
			"properties": anypoint.Doc{"upstreamResponseTimeout": *g.upstreamTimeout, "connectionIdleTimeout": *g.idleTimeout},
			"logging":    anypoint.Doc{"level": *g.loggingLevel, "forwardLogs": g.boolValue(g.fwdLogs, g.noFwdLogs)},
			"tracing":    tracing,
		}}
	gw, err := c.CreateManagedGateway(x.ctx, org, env, body)
	if err != nil {
		return err
	}
	fmt.Fprintln(x.Err, "Managed gateway created")
	return printGateway(x, gw)
}

func cmdGatewayEdit(x *Ctx, args []string) error {
	fs := x.Flags()
	g := newGatewayFlags(fs, false)
	pos, c, org, env, err := apiSetup(x, fs, args)
	if err != nil {
		return err
	}
	set := setFlags(fs)
	changed := false
	for name := range gatewayEnv {
		changed = changed || g.given(set, name)
	}
	if !changed {
		return errors.New("no edit flags provided. Please specify at least one flag to edit the managed gateway")
	}
	if err := g.validate(); err != nil {
		return err
	}
	cur, err := c.GetManagedGateway(x.ctx, org, env, pos[0])
	if err != nil {
		return err
	}
	cf := cur.Configuration
	channel := cur.ReleaseChannel
	if *g.channel != "" {
		channel = *g.channel
	}
	version := cur.RuntimeVersion
	switch {
	case *g.version != "":
		if version, err = runtimeVersionFor(x, c, *g.version, channel); err != nil {
			return err
		}
	case *g.channel != "" && *g.channel != cur.ReleaseChannel:
		if version, err = runtimeVersionFor(x, c, "", channel); err != nil {
			return err
		}
	}
	pickB := func(name string, on, off *bool, curV bool) bool {
		if g.given(set, name) {
			return g.boolValue(on, off)
		}
		return curV
	}
	pickI := func(name string, v *int, curV int) int {
		if g.given(set, name) {
			return *v
		}
		return curV
	}
	pickS := func(name string, v *string, curV string) string {
		if g.given(set, name) {
			return *v
		}
		return curV
	}
	curTracing := cf.Tracing != nil && cf.Tracing.Enabled
	tracingOn := pickB("tracingEnabled", g.tracing, g.noTracing, curTracing)
	if !tracingOn && (g.given(set, "tracingLabels") || g.given(set, "tracingSampling")) {
		return errors.New("tracing is disabled. Please enable it using the --tracingEnabled flag to set tracing labels and/or sampling")
	}
	tracing := anypoint.Doc{"enabled": tracingOn}
	if tracingOn {
		var labels any = []any{}
		if g.given(set, "tracingLabels") {
			if labels, err = parseLabels(*g.labels); err != nil {
				return err
			}
		} else if cf.Tracing != nil && cf.Tracing.Labels != nil {
			labels = cf.Tracing.Labels
		}
		sampling := 1
		if cf.Tracing != nil && cf.Tracing.Sampling != nil {
			sampling = *cf.Tracing.Sampling
		}
		tracing["sampling"], tracing["labels"] = pickI("tracingSampling", g.sampling, sampling), labels
	}
	body := anypoint.Doc{"name": cur.Name, "targetId": cur.TargetID, "runtimeVersion": version, "releaseChannel": channel, "size": cur.Size,
		"configuration": anypoint.Doc{
			"ingress": anypoint.Doc{"publicUrl": pickS("publicUrl", g.publicURL, cf.Ingress.PublicURL),
				"lastMileSecurity":  pickB("lastMileSecurity", g.lastMile, g.noLastMile, cf.Ingress.LastMileSecurity),
				"forwardSslSession": pickB("forwardSslSession", g.fwdSSL, g.noFwdSSL, cf.Ingress.ForwardSSLSession)},
			"properties": anypoint.Doc{"upstreamResponseTimeout": pickI("upstreamResponseTimeout", g.upstreamTimeout, cf.Properties.UpstreamResponseTimeout),
				"connectionIdleTimeout": pickI("connectionIdleTimeout", g.idleTimeout, cf.Properties.ConnectionIdleTimeout)},
			"logging": anypoint.Doc{"level": pickS("loggingLevel", g.loggingLevel, cf.Logging.Level),
				"forwardLogs": pickB("forwardLogs", g.fwdLogs, g.noFwdLogs, cf.Logging.ForwardLogs)},
			"tracing": tracing,
		}}
	gw, err := c.UpdateManagedGateway(x.ctx, org, env, pos[0], body)
	if err != nil {
		return err
	}
	fmt.Fprintln(x.Err, "Managed gateway is being updated")
	return printGateway(x, gw)
}

func cmdGatewayDelete(x *Ctx, args []string) error {
	pos, c, org, env, err := apiSetup(x, x.Flags(), args)
	if err != nil {
		return err
	}
	if err := c.DeleteManagedGateway(x.ctx, org, env, pos[0]); err != nil {
		if !strings.Contains(err.Error(), "associated APIs") {
			return err
		}
		apis, lerr := c.ListManagedGatewayAPIs(x.ctx, org, env, pos[0])
		if lerr != nil {
			return err
		}
		return x.Print(apis, func() {
			fmt.Fprintln(x.Out, "Error: The Managed Gateway contains existing APIs. Please delete them before deleting the Gateway:")
			t := newTable(x.Out, "API NAME", "INSTANCE ID", "DATE ADDED")
			for _, a := range apis {
				t.row(a.APIName, a.InstanceID.String(), a.DateAdded)
			}
			t.flush()
		})
	}
	return x.Done("Managed gateway is being deleted")
}

func cmdGatewayStatus(desired string) func(*Ctx, []string) error {
	allowed := map[string][]string{"STARTED": {"STOPPED", "STOPPING"}, "STOPPED": {"RUNNING", "STARTING"}}
	return func(x *Ctx, args []string) error {
		pos, c, org, env, err := apiSetup(x, x.Flags(), args)
		if err != nil {
			return err
		}
		g, err := c.GetManagedGateway(x.ctx, org, env, pos[0])
		if err != nil {
			return err
		}
		verb := strings.ToLower(desired)
		if !slices.Contains(allowed[desired], g.Status) {
			return x.Done("Cannot change managed gateway status to %s since the gateway is currently %s", verb, strings.ToLower(g.Status))
		}
		if err := c.SetManagedGatewayStatus(x.ctx, org, env, pos[0], desired); err != nil {
			return err
		}
		return x.Done("Updating Managed Gateway status to %s", verb)
	}
}
