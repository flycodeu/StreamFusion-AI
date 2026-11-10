package controller

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func managementResourceIdentity(resource serviceinstall.ResolvedResource) string {
	return deploy.Digest([]string{resource.Type, resource.Value, resource.Protocol, resource.BindAddress})
}

func verifyManagementResourceSnapshot(plan storedSoftwareOperationPlan, actual []serviceinstall.ResolvedResource) error {
	declared, err := plan.Management.ResolveResources(plan.Binding.Locator)
	if err != nil || len(actual) > 256 {
		return errors.New("MANAGEMENT_RESOURCES_UNCONFIRMED")
	}
	expected := map[string]serviceinstall.ResolvedResource{}
	updated := map[string]bool{}
	resources := plan.PreviousResources
	if !plan.Operation.ReadOnly {
		resources, err = serviceinstall.MergeManagementResourceClaims(plan.Management, resources, declared)
		if err != nil {
			return err
		}
	}
	for _, resource := range resources {
		expected[managementResourceIdentity(resource)] = resource
	}
	if !plan.Operation.ReadOnly {
		for _, resource := range declared {
			key := managementResourceIdentity(resource)
			if resource.Release {
				delete(expected, key)
			} else {
				expected[key], updated[key] = resource, true
			}
		}
	}
	if len(expected) != len(actual) {
		return errors.New("MANAGEMENT_RESOURCES_UNCONFIRMED")
	}
	seen := map[string]bool{}
	for _, resource := range actual {
		key := managementResourceIdentity(resource)
		prior, exists := expected[key]
		if !exists || seen[key] || resource.Key != prior.Key || resource.Release || !deploy.Hex.MatchString(resource.IdentityHash) || !updated[key] && resource.IdentityHash != prior.IdentityHash {
			return errors.New("MANAGEMENT_RESOURCES_UNCONFIRMED")
		}
		seen[key] = true
	}
	return nil
}

func installationResources(root, id string, definition serviceinstall.Definition, config serviceinstall.Config) []serviceinstall.ResolvedResource {
	installRoot := serviceinstall.InstallRoot(root, id, config)
	resources := []serviceinstall.ResolvedResource{{Type: "PATH_WRITE", Value: installRoot}}
	if serviceinstall.IsOfficial(definition) {
		if spec, err := serviceinstall.OfficialRecipeSpec(definition); err == nil {
			for _, owned := range spec.Paths.OwnedPaths {
				resources = append(resources, serviceinstall.ResolvedResource{Type: "PATH_WRITE", Value: owned})
			}
			if spec.Paths.Unit != "" {
				resources = append(resources, serviceinstall.ResolvedResource{Type: "UNIT", Value: spec.Paths.Unit})
			}
		}
	}
	for _, port := range serviceinstall.PortMappings(definition, config) {
		resources = append(resources, serviceinstall.ResolvedResource{Key: port.Key, Type: "PORT", Value: strconv.Itoa(port.HostPort), BindAddress: port.BindAddress, Protocol: port.Protocol})
	}
	if definition.Commands != nil && !serviceinstall.IsBuiltinCommand(definition) {
		locator := strings.NewReplacer("${OPS_INSTALL_ROOT}", installRoot, "${OPS_SOFTWARE_ID}", id).Replace(definition.Commands.Locator)
		if !strings.Contains(locator, "${") {
			switch definition.Commands.Kind {
			case "SYSTEMD":
				resources = append(resources, serviceinstall.ResolvedResource{Type: "UNIT", Value: locator})
			case "DOCKER":
				resources = append(resources, serviceinstall.ResolvedResource{Type: "CONTAINER", Value: locator})
			}
		}
	}
	return resources
}

func resourcesConflict(wanted, existing []serviceinstall.ResolvedResource) bool {
	for _, a := range wanted {
		for _, b := range existing {
			if serviceinstall.ResourceConflict(a, b) {
				return true
			}
		}
	}
	return false
}

type resourceClaimExemptions struct {
	AbsentInstallations map[string]string
	VerifiedPorts       map[string]string
}

func verifiedManagementPorts(resources []serviceinstall.ResolvedResource) []serviceinstall.ResolvedResource {
	ports := []serviceinstall.ResolvedResource{}
	for _, resource := range resources {
		if resource.Type == "PORT" && !resource.Release && deploy.Hex.MatchString(resource.IdentityHash) {
			ports = append(ports, resource)
		}
	}
	return ports
}

func (s *Store) confirmedFailedManagementPorts(host target, checked serviceinstall.ManagementPreflight) (map[string]string, error) {
	var confirmed map[string]string
	ports := verifiedManagementPorts(checked.Resources)
	if !checked.Ready || len(ports) == 0 {
		return confirmed, nil
	}
	items, err := s.failedCommandInstallations(host.ID)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		claims := installationResources(host.InstallRoot, item.ID, item.Installation.Definition, item.Installation.Config)
		if resourcesConflict(ports, claims) {
			if confirmed == nil {
				confirmed = map[string]string{}
			}
			confirmed[item.ID] = item.Version
		}
	}
	return confirmed, nil
}

// Preflight reserves declared resources against active instances and plans.
// The target independently checks real objects and their identity immediately
// before execution. These declarations are not a sandbox for trusted scripts.
func reserveManagementResources(tx *sql.Tx, targetID, softwareID, excludePlan string, wanted []serviceinstall.ResolvedResource, exemptions ...resourceClaimExemptions) error {
	if len(wanted) == 0 {
		return nil
	}
	var root string
	if err := tx.QueryRow("SELECT install_root FROM targets WHERE id=?", targetID).Scan(&root); err != nil {
		return err
	}
	rows, err := tx.Query("SELECT "+softwareColumns+" FROM software_instances s JOIN targets t ON t.id=s.host_id WHERE s.host_id=? AND s.id<>? AND s.management_mode<>'RELEASED' AND s.installation_status<>'UNINSTALLED'", targetID, softwareID)
	if err != nil {
		return err
	}
	instances := []softwareInstance{}
	for rows.Next() {
		item, scanErr := scanSoftware(rows)
		if scanErr != nil {
			err = scanErr
			break
		}
		instances = append(instances, item)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	proof := resourceClaimExemptions{}
	if len(exemptions) > 0 {
		proof = exemptions[0]
	}
	verifiedPorts := verifiedManagementPorts(wanted)
	remaining := map[string]bool{}
	for id := range proof.AbsentInstallations {
		remaining[id] = true
	}
	for id := range proof.VerifiedPorts {
		remaining[id] = true
	}
	for _, item := range instances {
		claimed := []serviceinstall.ResolvedResource{}
		if item.Installation != nil {
			claimed = installationResources(root, item.ID, item.Installation.Definition, item.Installation.Config)
		}
		// A fresh target check can release a failed attempt's unused service
		// claims. Its files and later management resources remain protected.
		absentVersion, absent := proof.AbsentInstallations[item.ID]
		portVersion, portConfirmed := proof.VerifiedPorts[item.ID]
		if absent || portConfirmed {
			if !unboundFailedCommandInstall(item) || absent && absentVersion != item.Version || portConfirmed && portVersion != item.Version {
				return errors.New("MANAGEMENT_RESOURCE_CONFLICT")
			}
			var final int
			if err = tx.QueryRow("SELECT count(*) FROM jobs WHERE id=? AND software_plan_id=(SELECT install_plan_id FROM software_instances WHERE id=?) AND kind='SOFTWARE_INSTALL' AND status='FAILED' AND target_ack_pending=0", item.LastJobID, item.ID).Scan(&final); err != nil {
				return err
			}
			if final != 1 {
				return errors.New("MANAGEMENT_RESOURCE_CONFLICT")
			}
			retained := claimed[:0]
			for _, resource := range claimed {
				skip := absent && failedInstallServiceResource(resource) || portConfirmed && resource.Type == "PORT" && resourcesConflict(verifiedPorts, []serviceinstall.ResolvedResource{resource})
				if !skip {
					retained = append(retained, resource)
				}
			}
			claimed = retained
			delete(remaining, item.ID)
		}
		switch item.RuntimeKind {
		case "SYSTEMD":
			claimed = append(claimed, serviceinstall.ResolvedResource{Type: "UNIT", Value: item.Locator})
		case "DOCKER":
			claimed = append(claimed, serviceinstall.ResolvedResource{Type: "CONTAINER", Value: item.Locator})
		case "TOOL":
			if len(item.Locator) > 0 && item.Locator[0] == '/' {
				claimed = append(claimed, serviceinstall.ResolvedResource{Type: "PATH_READ", Value: item.Locator})
			}
		}
		state, stateErr := scanSoftwareManagement(tx, item.ID)
		if stateErr != nil {
			return stateErr
		}
		if item.Installation != nil {
			claimed = serviceinstall.OverlayWebPortClaims(item.Installation.Definition, claimed, state.Resources)
		} else {
			claimed = append(claimed, state.Resources...)
		}
		if resourcesConflict(wanted, claimed) {
			return errors.New("MANAGEMENT_RESOURCE_CONFLICT")
		}
	}
	if len(remaining) > 0 {
		return errors.New("MANAGEMENT_RESOURCE_CONFLICT")
	}
	rows, err = tx.Query(`SELECT id,snapshot_json FROM software_install_plans WHERE target_id=? AND status='DRAFT' AND expires_at>?
UNION ALL SELECT id,snapshot_json FROM software_configuration_plans WHERE target_id=? AND status='DRAFT' AND expires_at>?
UNION ALL SELECT p.id,p.snapshot_json FROM software_install_plans p JOIN jobs j ON j.software_plan_id=p.id WHERE p.target_id=? AND (j.status IN ('QUEUED','RUNNING','UNKNOWN') OR j.target_ack_pending=1)
UNION ALL SELECT p.id,p.snapshot_json FROM software_configuration_plans p JOIN jobs j ON j.configuration_plan_id=p.id WHERE p.target_id=? AND (j.status IN ('QUEUED','RUNNING','UNKNOWN') OR j.target_ack_pending=1)`, targetID, Now(), targetID, Now(), targetID, targetID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, raw string
		if err = rows.Scan(&id, &raw); err != nil {
			return err
		}
		if id == excludePlan {
			continue
		}
		var p struct {
			Kind       string                            `json:"kind"`
			SoftwareID string                            `json:"softwareId"`
			Definition serviceinstall.Definition         `json:"definition"`
			Config     serviceinstall.Config             `json:"config"`
			Resources  []serviceinstall.ResolvedResource `json:"resources"`
		}
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return err
		}
		if p.SoftwareID == softwareID {
			continue
		}
		claimed := p.Resources
		if p.Kind != "OPERATION" {
			claimed = installationResources(root, p.SoftwareID, p.Definition, p.Config)
		}
		if resourcesConflict(wanted, claimed) {
			return errors.New("MANAGEMENT_RESOURCE_RESERVED")
		}
	}
	return rows.Err()
}
