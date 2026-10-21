package controller

import (
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/plugin"
)

func TestPluginToolPreflightUsesEngineWithoutCompose(t *testing.T) {
	s, b := setup(t)
	b.login()
	var toolCalls, composeCalls atomic.Int32
	var compose atomic.Value
	compose.Store("")
	id := pluginSSHFixture(t, s, b, true, func(req deploy.Request) ([]byte, bool) {
		if req.Action == "PROBE" {
			composeCalls.Add(1)
			return []byte(`{}`), true
		}
		if req.Action != "TOOL_PROBE" {
			return nil, false
		}
		toolCalls.Add(1)
		out, _ := json.Marshal(map[string]string{"dockerVersion": "27.5.1", "composeVersion": compose.Load().(string), "platform": "linux/amd64", "pluginCatalogHash": deploy.Digest(plugin.All()), "pendingJobId": ""})
		return out, true
	})
	input := map[string]string{"pluginKey": "maven-jdk-build", "version": "3.9.11-jdk-21", "recipeRevision": "maven-jdk-build-v1"}
	b.call("POST", "/hosts/"+id+"/plugin-install-plans", input, 200)
	input = map[string]string{"pluginKey": "java-runtime", "version": "21-jre", "recipeRevision": "java-runtime-v1"}
	p := dataOf(t, b.call("POST", "/hosts/"+id+"/plugin-install-plans", input, 200))
	// Compose arriving after preflight is irrelevant to the frozen tool plan.
	compose.Store("2.32.4")
	b.call("POST", "/plugin-install-plans/"+p["id"].(string)+"/submit", map[string]string{"idempotencyKey": "engine-only-tool-install"}, 202)
	if toolCalls.Load() != 3 || composeCalls.Load() != 0 {
		t.Fatal("tool installation used application Compose readiness", toolCalls.Load(), composeCalls.Load())
	}
}
