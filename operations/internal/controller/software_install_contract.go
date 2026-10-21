package controller

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/plugin"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

func installationRuntimeKind(d serviceinstall.Definition, c serviceinstall.Config) string {
	if d.Kind == "TOOL" {
		return "TOOL"
	}
	if c.Mode() == "NATIVE" {
		return "SYSTEMD"
	}
	return "DOCKER"
}

func installationDesiredState(d serviceinstall.Definition) string {
	if d.Kind == "TOOL" {
		return "INSTALLED"
	}
	return "RUNNING"
}

func installationBound(i softwareInstance) bool {
	return i.ManagementMode == "MANAGED" && i.BindingRevision > 0 && deploy.Hex.MatchString(i.IdentityHash) && i.Locator != "" && !strings.HasPrefix(i.Locator, "pending:")
}

func installationKind(e serviceinstall.Evidence) string {
	if e.Kind == "" {
		return "DOCKER"
	}
	return e.Kind
}

func installationLocator(e serviceinstall.Evidence) string {
	if e.Locator == "" {
		return e.ContainerID
	}
	return e.Locator
}

// New Docker tools remain usable by the existing JAR artifact chain. Both
// records refer to the same immutable image; no service process is invented.
func bridgeInstalledTool(tx *sql.Tx, req deploy.Request, evidence serviceinstall.Evidence) error {
	i := req.Installation
	if i == nil || !serviceinstall.UsesJavaBridge(i.Definition) || i.Config.Mode() != "DOCKER" {
		return nil
	}
	key, version := "java-runtime", i.Definition.Version+"-jre"
	if i.Definition.Key == "maven" {
		key, version = "maven-jdk-build", "3.9.11-jdk-21"
	}
	d, err := plugin.Find(key, version)
	if err != nil || d.Image != i.Definition.Image || d.Digest != i.Definition.Digest {
		return errors.New("TOOL_BRIDGE_RECIPE_MISMATCH")
	}
	var existingID, status, imageID string
	err = tx.QueryRow("SELECT id,status,image_id FROM plugin_installations WHERE target_id=? AND plugin_key=? AND plugin_version=? AND image_digest=?", req.TargetID, d.Key, d.Version, d.Digest).Scan(&existingID, &status, &imageID)
	if err == nil {
		// Preserve its ID, frozen artifact references and target material. A
		// byte-identical ready image can be reused without rewriting history.
		if status == "READY" && imageID == evidence.ImageID {
			return nil
		}
		return errors.New("TOOL_BRIDGE_EXISTING_RECORD_UNCONFIRMED")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.Exec(`INSERT INTO plugin_installations(id,target_id,plugin_key,plugin_version,kind,install_mode,image_ref,image_digest,image_id,recipe_revision,platform,status,observed_version,last_job_id,created_at,updated_at,observed_at) VALUES(?,?,?,?,?,'DOCKER_IMAGE',?,?,?,?,?,'READY',?,?,?,?,?)`, i.SoftwareID, req.TargetID, d.Key, d.Version, d.Kind, d.Image, d.Digest, evidence.ImageID, d.RecipeRevision, d.Platform, evidence.ObservedVersion, req.JobID, Now(), Now(), evidence.ObservedAt)
	return err
}

func checkInstalledTool(q interface{ QueryRow(string, ...any) *sql.Row }, targetID string, d serviceinstall.Definition, config serviceinstall.Config) error {
	if !serviceinstall.UsesJavaBridge(d) || config.Mode() != "DOCKER" {
		return nil
	}
	key, version := "java-runtime", d.Version+"-jre"
	if d.Key == "maven" {
		key, version = "maven-jdk-build", "3.9.11-jdk-21"
	}
	var count int
	if err := q.QueryRow("SELECT count(*) FROM plugin_installations WHERE target_id=? AND plugin_key=? AND plugin_version=? AND image_digest=?", targetID, key, version, d.Digest).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("TOOL_ALREADY_INSTALLED")
	}
	return nil
}

// Check every active protocol, including API-only loopback and UDP sockets.
// A pending plan is a short reservation; the target still verifies real sockets.
func reserveInstallPorts(tx *sql.Tx, targetID, actorID, excludeID string, d serviceinstall.Definition, c serviceinstall.Config) error {
	wanted := serviceinstall.PortMappings(d, c)
	if len(wanted) == 0 {
		return nil
	}
	rows, err := tx.Query(`SELECT id,actor_id,snapshot_json,'INSTALL' FROM software_install_plans WHERE target_id=? AND status='DRAFT' AND expires_at>? UNION ALL SELECT id,actor_id,snapshot_json,'CONFIGURE' FROM software_configuration_plans WHERE target_id=? AND status='DRAFT' AND expires_at>?`, targetID, time.Now().UTC().Format(time.RFC3339), targetID, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	type draft struct{ id, actor, raw, kind string }
	drafts := []draft{}
	for rows.Next() {
		var p draft
		if err = rows.Scan(&p.id, &p.actor, &p.raw, &p.kind); err != nil {
			break
		}
		drafts = append(drafts, p)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range drafts {
		if p.id == excludeID {
			continue
		}
		var frozen struct {
			Definition serviceinstall.Definition `json:"definition"`
			Config     serviceinstall.Config     `json:"config"`
		}
		// Private actor/host fields remain in the persisted snapshot; only its
		// validated recipe and network configuration participate in reservations.
		if err = json.Unmarshal([]byte(p.raw), &frozen); err != nil {
			return err
		}
		if err = serviceinstall.ValidateDefinition(frozen.Definition); err != nil {
			return err
		}
		if err = frozen.Config.Validate(frozen.Definition); err != nil {
			return err
		}
		conflict := false
		for _, a := range wanted {
			for _, b := range serviceinstall.PortMappings(frozen.Definition, frozen.Config) {
				if a.HostPort == b.HostPort && a.Protocol == b.Protocol && a.BindAddress == b.BindAddress {
					conflict = true
				}
			}
		}
		if !conflict {
			continue
		}
		if p.actor != actorID {
			return errors.New("INSTALL_PORT_RESERVED")
		}
		table := "software_install_plans"
		if p.kind == "CONFIGURE" {
			table = "software_configuration_plans"
		}
		if _, err = tx.Exec("UPDATE "+table+" SET status='SUPERSEDED' WHERE id=? AND status='DRAFT'", p.id); err != nil {
			return err
		}
	}
	return nil
}
