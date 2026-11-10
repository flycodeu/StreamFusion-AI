package controller

import (
	"strconv"
	"testing"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func TestManagementResourceReservationFollowsConfirmedWebEntry(t *testing.T) {
	s, _, id, template, _ := readyManagementFixture(t)
	item, err := s.readSoftware(id)
	if err != nil || item.Installation == nil || item.Installation.Definition.Web == nil {
		t.Fatal("Web installation fixture unavailable", err)
	}
	current := serviceinstall.ResolvedResource{Key: "main", Type: "PORT", Value: "19924", BindAddress: "0.0.0.0", Protocol: "TCP", IdentityHash: deploy.Digest("confirmed")}
	if _, err := s.DB.Exec("INSERT INTO software_management_state(software_id,template_id,template_revision,definition_json,resources_json,updated_at) VALUES(?,?,?,?,?,?)", id, template.ID, template.Revision, jsonText(*template.Published), jsonText([]serviceinstall.ResolvedResource{current}), Now()); err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	old := serviceinstall.ResolvedResource{Type: "PORT", Value: strconv.Itoa(item.Installation.Config.HostPort), BindAddress: item.Installation.Config.BindAddress, Protocol: "TCP"}
	other := "11111111-1111-4111-8111-111111111111"
	if err := reserveManagementResources(tx, item.HostID, other, "", []serviceinstall.ResolvedResource{old}); err != nil {
		t.Fatal("obsolete installation listener remained reserved", err)
	}
	if err := reserveManagementResources(tx, item.HostID, other, "", []serviceinstall.ResolvedResource{current}); err == nil || err.Error() != "MANAGEMENT_RESOURCE_CONFLICT" {
		t.Fatal("confirmed Web listener was not protected", err)
	}
}

func TestManagementResourceSnapshotReplacesOnlyTheDeclaredWebEntry(t *testing.T) {
	old := serviceinstall.ResolvedResource{Key: "main", Type: "PORT", Value: "9924", BindAddress: "127.0.0.1", Protocol: "TCP", IdentityHash: deploy.Digest("old")}
	site := serviceinstall.ResolvedResource{Key: "listener", Type: "PORT", Value: "8081", BindAddress: "127.0.0.1", Protocol: "TCP", IdentityHash: deploy.Digest("site")}
	d := ytlabelDefinitions()[0]
	d.Management = &serviceinstall.ManagementDefinition{Operations: []serviceinstall.ManagementOperation{{Key: "network", Resources: []serviceinstall.ResourceDefinition{{Key: "main", Type: "PORT", Value: "19924", Protocol: "TCP", BindAddress: "0.0.0.0"}}}}}
	d.Management.Operations[0].Name, d.Management.Operations[0].ResultFormat = "网络配置", "TEXT"
	d.Management.Operations[0].Steps = []serviceinstall.CommandStep{{Name: "配置", Script: "true", RunAs: "ROOT", TimeoutSeconds: 1}}
	d.Management.Operations[0].VerifySteps = d.Management.Operations[0].Steps
	m := serviceinstall.ManagementRequest{Definition: d, OperationKey: "network", SoftwareID: "11111111-1111-4111-8111-111111111111", BindingRevision: 1, Root: "/opt/streamfusion-ops-target/software/11111111-1111-4111-8111-111111111111"}
	op, _ := d.ManagementOperation("network")
	p := storedSoftwareOperationPlan{PreviousResources: []serviceinstall.ResolvedResource{old, site}, Binding: deploy.SoftwareRequest{Locator: "test.service"}, softwareOperationPlan: softwareOperationPlan{Management: m, Operation: op}}
	declared, err := m.ResolveResources(p.Binding.Locator)
	if err != nil || len(declared) != 1 {
		t.Fatal(declared, err)
	}
	declared[0].IdentityHash = deploy.Digest("new")
	if err := verifyManagementResourceSnapshot(p, append([]serviceinstall.ResolvedResource{site}, declared...)); err != nil {
		t.Fatal("verified Web replacement rejected", err)
	}
	if verifyManagementResourceSnapshot(p, append([]serviceinstall.ResolvedResource{old, site}, declared...)) == nil {
		t.Fatal("obsolete Web listener remained in the accepted snapshot")
	}
	if verifyManagementResourceSnapshot(p, declared) == nil {
		t.Fatal("Web replacement released an unrelated site")
	}
	forged := site
	forged.Key = "main"
	if verifyManagementResourceSnapshot(p, append([]serviceinstall.ResolvedResource{forged}, declared...)) == nil {
		t.Fatal("receipt changed a site's label into the Web entry")
	}
	p.Operation.ReadOnly = true
	if err := verifyManagementResourceSnapshot(p, []serviceinstall.ResolvedResource{old, site}); err != nil {
		t.Fatal("read-only snapshot changed claims", err)
	}
}
