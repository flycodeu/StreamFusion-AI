package controller

import (
	"strings"
	"testing"
)

func TestProjectDirectoryPreservedDuringHostMetadataUpdate(t *testing.T) {
	_, b := setup(t)
	b.login()
	host := dataOf(t, b.call("POST", "/hosts", map[string]any{
		"name": "project host", "kind": "LOCAL", "installRoot": "/srv/streamfusion-project",
	}, 201))
	id := host["id"].(string)
	updated := dataOf(t, b.call("PATCH", "/hosts/"+id, map[string]any{
		"name": "renamed project", "kind": "LOCAL", "expectedVersion": "1",
	}, 200))
	if updated["installRoot"] != "/srv/streamfusion-project" {
		t.Fatal("metadata update changed project directory", updated)
	}
	updated = dataOf(t, b.call("PATCH", "/hosts/"+id, map[string]any{
		"name": "renamed project", "kind": "LOCAL", "expectedVersion": "2", "installRoot": "/opt/project-before-install",
	}, 200))
	if updated["installRoot"] != "/opt/project-before-install" {
		t.Fatal("unreferenced directory could not be changed", updated)
	}
}

func TestProjectDirectoryRejectsUnboundedOrNonDedicatedPath(t *testing.T) {
	_, b := setup(t)
	b.login()
	for _, root := range []string{"/", "/opt", "/etc/project", "/opt/project/../mysql", "/opt/project with spaces", "/opt/" + strings.Repeat("x", 240)} {
		b.call("POST", "/hosts", map[string]any{"name": "invalid root", "kind": "LOCAL", "installRoot": root}, 400)
	}
}
