package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAgentNetworkGateways(t *testing.T) {
	old := agentGatewayPoll
	agentGatewayPoll = time.Millisecond
	t.Cleanup(func() { agentGatewayPoll = old })

	r := newRecorder(t)
	gwAPI := "/gatewaymanager/api/v1/organizations/" + rootOrg + "/environments/" + prodEnv + "/gateways"
	gwX := "/gatewaymanager/xapi/v1/organizations/" + rootOrg + "/environments/" + prodEnv + "/gateways"
	r.on("GET "+gwX+"/targets", map[string]any{"content": []any{
		map[string]any{"id": "ps-1", "name": "agent-network-space", "type": "private-space"},
		map[string]any{"id": "rtf-1", "name": "fabric", "type": "runtime-fabric"},
		map[string]any{"id": "sh-1", "name": "shared", "type": "shared-space"}}})
	r.on("GET /runtimefabric/api/organizations/"+rootOrg+"/targets/ps-1/environments/"+prodEnv+"/domains",
		map[string]any{"domains": []any{"*.ps.example.com"}, "appUniqueId": "abc123"})
	r.on("GET /runtimefabric/api/organizations/"+rootOrg+"/targets/rtf-1/environments/"+prodEnv+"/domains",
		map[string]any{"domains": []any{"*.rtf.example.com"}})
	r.on("GET /gatewaymanager/xapi/v1/gateway/versions", map[string]any{"channels": map[string]any{
		"edge": map[string]any{"versions": []any{map[string]any{"displayName": "1.9.0"}, map[string]any{"displayName": "1.10.1"}}}}})
	r.on("GET "+gwAPI, map[string]any{"content": []any{}, "totalElements": 0})
	n := 0
	r.on("POST "+gwAPI, func(*http.Request) reply { n++; return reply{201, map[string]any{"id": "new-" + string(rune('0'+n))}} })
	polls := 0
	status := func(*http.Request) reply {
		polls++
		return reply{200, map[string]any{"id": "x", "running": polls > 2, "ready": polls > 2}}
	}
	for _, id := range []string{"1", "2", "3", "4"} {
		r.on("GET "+gwX+"/new-"+id, status)
	}

	out := r.mustRun("agent-network:setup:gateways")
	if !strings.Contains(out, "completed successfully") || polls < 3 {
		t.Errorf("setup (polls %d):\n%s", polls, out)
	}
	b := r.last("POST " + gwAPI).JSON(t)
	jsonEq(t, b, "name", "agent-network-gw")
	jsonEq(t, b, "size", "large")
	jsonEq(t, b, "runtimeVersion", "1.10.1")
	jsonEq(t, b, "configuration.ingress.publicUrl", "https://agent-network-gw-abc123.ps.example.com")
	jsonEq(t, b, "configuration.tracing", map[string]any{"enabled": true, "sampling": 100})

	r.mustRun("agent-network:setup:gateways", "-t", "fabric", "-i", "in", "-e", "out", "--dont-wait-for-gateways-running")
	r.mu.Lock()
	var bodies []call
	for _, c := range r.calls {
		if c.key() == "POST "+gwAPI {
			bodies = append(bodies, c)
		}
	}
	r.mu.Unlock()
	in, eg := bodies[len(bodies)-2].JSON(t), bodies[len(bodies)-1].JSON(t)
	jsonEq(t, in, "size", "small")
	jsonEq(t, in, "configuration.ingress.publicUrl", "in.rtf.example.com")
	jsonEq(t, eg, "configuration.ingress.publicUrl", "out.rtf.example.com")

	r.mustRun("agent-network:setup:gateways", "--target-space", "shared")
	b = r.last("POST " + gwAPI).JSON(t)
	jsonEq(t, b, "name", "agent-network-shared-gw")
	jsonEq(t, b, "configuration.ingress.publicUrl", "")
	for _, bad := range [][]string{{"-g", "a", "-i", "b"}, {"-e", "x"}, {"-t", "shared", "-i", "a", "-e", "b"}, {"-t", "nowhere"}} {
		if _, err := r.run(append([]string{"agent-network:setup:gateways"}, bad...)...); err == nil {
			t.Errorf("setup %v must fail", bad)
		}
	}

	// existing gateways are skipped, or updated with --update
	r.on("GET "+gwAPI, map[string]any{"content": []any{map[string]any{"id": "g1", "name": "agent-network-gw", "targetId": "ps-1"}}, "totalElements": 1})
	before := countCalls(r, "POST "+gwAPI)
	if out := r.mustRun("agent-network:setup:gateways"); !strings.Contains(out, "Skipping") && countCalls(r, "POST "+gwAPI) != before {
		t.Errorf("an existing gateway must be skipped:\n%s", out)
	}
	if countCalls(r, "POST "+gwAPI) != before {
		t.Error("no gateway may be created when it exists")
	}
	updated := false
	r.on("GET "+gwAPI+"/g1", func(*http.Request) reply {
		v := "1.9.0"
		if updated {
			v = "1.10.1"
		}
		return reply{200, map[string]any{"id": "g1", "name": "agent-network-gw", "targetId": "ps-1", "releaseChannel": "edge",
			"runtimeVersion": v, "size": "large", "status": "APPLIED", "configuration": map[string]any{"logging": map[string]any{"level": "debug"}}}}
	})
	r.on("PUT "+gwAPI+"/g1", func(*http.Request) reply { updated = true; return reply{200, map[string]any{"id": "g1"}} })
	r.mustRun("agent-network:setup:gateways", "--update")
	b = r.last("PUT " + gwAPI + "/g1").JSON(t)
	jsonEq(t, b, "runtimeVersion", "1.10.1")
	jsonEq(t, b, "configuration.logging.level", "debug")

	r.on("GET "+gwAPI, map[string]any{"content": []any{map[string]any{"id": "g1", "name": "agent-network-gw", "targetId": "rtf-1"}}, "totalElements": 1})
	if _, err := r.run("agent-network:setup:gateways"); err == nil || !strings.Contains(err.Error(), "another target space") {
		t.Errorf("a name taken in another space: %v", err)
	}
}
