package controller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestJenkinsCatalogDockerPlanAndManagement(t *testing.T) {
	s, b := setup(t)
	b.login()
	key := "custom-jenkins-docker-online"
	var catalog struct{ Data []serviceinstall.Definition }
	if err := json.Unmarshal(b.call("GET", "/software-recipes", nil, 200).Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	var definition serviceinstall.Definition
	for _, item := range catalog.Data {
		if item.Key == key {
			definition = item
			break
		}
	}
	if definition.CatalogKey != "jenkins" || definition.Category != "BUILD_TOOLS" || definition.Web == nil ||
		definition.Web.PortKey != "main" || !strings.Contains(definition.Image, "jenkins/jenkins:") || definition.Digest == "" {
		t.Fatal("published Jenkins recipe is missing the expected Docker and Web definition")
	}
	var create string
	for _, step := range definition.Commands.Steps {
		if strings.Contains(step.Script, "/var/jenkins_home") {
			create = step.Script
			break
		}
	}
	if !strings.Contains(create, "$root/data:/var/jenkins_home") || !strings.Contains(create, "JAVA_OPTS=-XX:-UseBASE64Intrinsics") || strings.Contains(create, "docker.sock") || strings.Contains(create, "--privileged") {
		t.Fatal("Jenkins container must keep its home and use the protected launcher without mounting the host Docker socket or escalating container privilege")
	}

	password := strings.Repeat("0123", 8)
	host, fixture := installSSHFixture(t, s, b, func(req deploy.Request) ([]byte, bool) {
		if req.Action != "SOFTWARE_LOGS" || req.Software == nil {
			return nil, false
		}
		fact := deploy.SoftwareObservation{Kind: req.Software.Kind, Locator: req.Software.Locator, IdentityHash: req.Software.IdentityHash, ObservedAt: Now(), State: "RUNNING",
			Log: "2026-11-23T10:00:00.253987981Z " + password + "\nJenkins is fully up and running"}
		result, err := json.Marshal(deploy.Result{ProtocolVersion: 1, ExecutorVersion: deploy.Version, ControllerID: req.ControllerID, TargetID: req.TargetID,
			Status: "SUCCEEDED", Phase: "SOFTWARE_OBSERVED", Software: &fact})
		if err != nil {
			t.Error(err)
		}
		return result, true
	})
	config := serviceinstall.Config{Name: "CI Jenkins", RuntimeMode: "DOCKER", StorageRoot: "/data/ops-ci", BindAddress: "127.0.0.1", HostPort: 18080, MemoryMiB: definition.DefaultMemoryMiB}
	var response struct{ Data softwareInstallPlan }
	body := b.call("POST", "/hosts/"+host+"/software-install-plans", map[string]any{
		"expectedHostVersion": "1", "recipeKey": key, "recipeVersion": definition.Version, "recipeRevision": definition.Revision,
		"config": config, "source": map[string]string{"type": "REGISTRY"},
	}, 200).Body.Bytes()
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	plan := response.Data
	matrixCheckPlanPathsAndPorts(t, plan)
	if plan.Source.Type != "REGISTRY" || len(plan.PortMappings) != 1 || plan.PortMappings[0].ContainerPort != 8080 || plan.PortMappings[0].HostPort != 18080 {
		t.Fatal("Jenkins online plan must reserve the configured HTTP port and no agent port")
	}
	if fixture.InstallCount.Load() != 0 {
		t.Fatal("previewing an install plan mutated the host")
	}
	receipt := dataOf(t, b.call("POST", "/software-install-plans/"+plan.ID+"/submit", map[string]string{"idempotencyKey": "jenkins-fixture-install"}, 202))
	job, id := receipt["jobId"].(string), receipt["softwareInstanceId"].(string)
	var raw string
	if err := s.DB.QueryRow("SELECT input_json FROM jobs WHERE id=?", job).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	b.h.processSoftware(context.Background(), job, raw)
	installed, err := s.readSoftware(id)
	if err != nil || !installationBound(installed) || installed.ActualState != "RUNNING" || installed.Installation.Definition.CatalogKey != "jenkins" {
		t.Fatal("verified Jenkins software instance was not bound", err)
	}
	matrixCheckJobFinished(t, s, job)
	if fixture.InstallCount.Load() != 1 {
		t.Fatal("installation did not execute exactly once")
	}
	matrixServiceAction(t, s, b, id, "STOP", "STOPPED")
	matrixServiceAction(t, s, b, id, "START", "RUNNING")
	logs := b.call("GET", "/software-instances/"+id+"/logs", nil, 200).Body.String()
	if strings.Contains(logs, password) || !strings.Contains(logs, "[REDACTED]") || !strings.Contains(logs, "Jenkins is fully up and running") {
		t.Fatal("Jenkins software log exposed first-run credential or lost ordinary output")
	}
}
