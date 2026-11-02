package controller

import (
	"database/sql"
	"errors"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

type nodeInstallInput struct {
	EnvironmentID      string `json:"environmentId"`
	EnvironmentVersion string `json:"environmentVersion"`
	MakeDefault        bool   `json:"makeDefault"`
	ConnectTerminal    bool   `json:"connectTerminal,omitempty"`
}

func nodeVersionDataPaths(n serviceinstall.NodeContext, version string) []string {
	return []string{n.RootPath + "/cache/" + version, n.RootPath + "/backups/" + version, n.RootPath + "/nvm/.cache/bin/node-" + version + "-linux-x64"}
}

func validateNodeActionMembers(tx *sql.Tx, id string, c deploy.NodeEnvironmentRequest) error {
	var count int
	if c.Action == "REMOVE_ENVIRONMENT" {
		if err := tx.QueryRow("SELECT count(*) FROM software_instances WHERE node_environment_id=? AND management_mode<>'RELEASED' AND installation_status NOT IN ('FAILED','CANCELLED','UNINSTALLED')", id).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return errors.New("NODE_ENVIRONMENT_NOT_EMPTY")
		}
	}
	if c.NodeVersion != "" {
		if err := tx.QueryRow("SELECT count(*) FROM software_instances WHERE node_environment_id=? AND software_version=? AND management_mode='MANAGED' AND installation_status='READY'", id, c.NodeVersion).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return errors.New("NODE_VERSION_NOT_MANAGED")
		}
	}
	return nil
}

func nodeUnchangedVersions(before, after []deploy.NodeVersionObservation, changed string) bool {
	expected := map[string]string{}
	for _, v := range before {
		if v.Version != changed {
			expected[v.Version] = deploy.Digest(v)
		}
	}
	for _, v := range after {
		if v.Version == changed {
			continue
		}
		if expected[v.Version] != deploy.Digest(v) {
			return false
		}
		delete(expected, v.Version)
	}
	return len(expected) == 0
}

func (h *HTTP) resolveNodeInstall(host string, d serviceinstall.Definition, in *nodeInstallInput) (*serviceinstall.NodeContext, error) {
	if !serviceinstall.IsNode(d) {
		if in != nil {
			return nil, errors.New("NODE_CONTEXT_INVALID")
		}
		return nil, nil
	}
	if in == nil {
		return nil, errors.New("NODE_ENVIRONMENT_REQUIRED")
	}
	e, err := h.store.readNodeEnvironment(in.EnvironmentID)
	if err != nil {
		return nil, err
	}
	if e.HostID != host || e.Version != in.EnvironmentVersion || e.ManagementMode != "MANAGED" || !e.Observation.Exists {
		return nil, errors.New("NODE_ENVIRONMENT_CHANGED")
	}
	n := e.context()
	n.MakeDefault = in.MakeDefault
	n.ConnectTerminal = in.ConnectTerminal
	return &n, n.Validate()
}

func checkNodeInstallTx(tx *sql.Tx, p softwareInstallPlan) error {
	if p.Node == nil {
		return nil
	}
	n := p.Node
	e, err := scanNodeEnvironment(tx.QueryRow("SELECT "+nodeEnvironmentColumns+" FROM node_environments WHERE id=?", n.EnvironmentID))
	if err != nil {
		return err
	}
	if e.HostID != p.TargetID || e.Version != n.EnvironmentVersion || e.ManagementMode != "MANAGED" || e.RootPath != n.RootPath || e.AccountUID != n.AccountUID || !e.Observation.Exists {
		return errors.New("NODE_ENVIRONMENT_CHANGED")
	}
	var count int
	err = tx.QueryRow("SELECT count(*) FROM software_instances WHERE node_environment_id=? AND software_version=? AND management_mode<>'RELEASED' AND installation_status NOT IN ('FAILED','CANCELLED','UNINSTALLED')", e.ID, p.Definition.Version).Scan(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return errors.New("NODE_VERSION_EXISTS")
	}
	return nodeTargetUnlocked(tx, p.TargetID)
}

func checkNodeSoftwareActionTx(tx *sql.Tx, i softwareInstance, action string, clear bool) error {
	if i.NodeEnvironmentID == "" {
		if clear {
			return errors.New("NODE_CONTEXT_INVALID")
		}
		return nil
	}
	if action != "SOFTWARE_UNINSTALL" {
		return errors.New("NODE_ACTION_INVALID")
	}
	e, err := scanNodeEnvironment(tx.QueryRow("SELECT "+nodeEnvironmentColumns+" FROM node_environments WHERE id=?", i.NodeEnvironmentID))
	if err != nil {
		return err
	}
	if e.ManagementMode != "MANAGED" || !e.Observation.Exists {
		return errors.New("NODE_ENVIRONMENT_CHANGED")
	}
	if e.ObservedDefaultVersion == i.SoftwareVersion || e.DesiredDefaultSoftwareID == i.ID {
		var count int
		err = tx.QueryRow("SELECT count(*) FROM software_instances WHERE node_environment_id=? AND id<>? AND management_mode='MANAGED' AND installation_status='READY'", e.ID, i.ID).Scan(&count)
		if err != nil {
			return err
		}
		if count > 0 {
			return errors.New("NODE_DEFAULT_IN_USE")
		}
		if !clear {
			return errors.New("NODE_CLEAR_DEFAULT_REQUIRED")
		}
	} else if clear {
		return errors.New("NODE_CLEAR_DEFAULT_INVALID")
	}
	return nil
}

// Node versions share one account-scoped environment. Commit its observed
// default with the existing software receipt, never in a separate transaction.
func commitNodeSoftwareObservation(tx *sql.Tx, job string, n *serviceinstall.NodeContext, result deploy.Result, softwareID, version string, removed bool) error {
	e, err := scanNodeEnvironment(tx.QueryRow("SELECT "+nodeEnvironmentColumns+" FROM node_environments WHERE id=?", n.EnvironmentID))
	if err != nil {
		return err
	}
	if e.LastJobID != job {
		return errors.New("NODE_ENVIRONMENT_CHANGED")
	}
	if result.Status != "SUCCEEDED" {
		_, err = tx.Exec("UPDATE node_environments SET error_code=?,version=version+1,updated_at=? WHERE id=?", result.ErrorCode, Now(), e.ID)
		return err
	}
	o := result.NodeEnvironment
	if o == nil || !nodeObservationMatches(e, *o) || !o.Exists {
		return errors.New("NODE_RESULT_MISMATCH")
	}
	if !removed && n.ConnectTerminal && (o.Terminal == nil || o.Terminal.Status != "CONNECTED") {
		return errors.New("NODE_TERMINAL_UNCONFIRMED")
	}
	if !nodeUnchangedVersions(e.Observation.Versions, o.Versions, version) {
		return errors.New("NODE_SIBLING_VERSION_CHANGED")
	}
	if _, err = time.Parse(time.RFC3339Nano, o.ObservedAt); err != nil {
		return errors.New("NODE_RESULT_MISMATCH")
	}
	present := false
	for _, v := range o.Versions {
		if v.Version == version {
			present = true
			if v.BinaryPath != serviceinstall.NodeVersionRoot(*n, version)+"/bin/node" || !deploy.Hex.MatchString(v.BinaryHash) {
				return errors.New("NODE_RESULT_MISMATCH")
			}
		}
	}
	if present == removed {
		return errors.New("NODE_RESULT_MISMATCH")
	}
	expected := n.ExpectedDefault
	if !removed && n.MakeDefault {
		expected = version
	}
	if removed && expected == version {
		expected = ""
	}
	if o.DefaultVersion != expected {
		return errors.New("NODE_RESULT_MISMATCH")
	}
	if err = storeNodeObservation(tx, e.ID, *o); err != nil {
		return err
	}
	if !removed && n.MakeDefault {
		_, err = tx.Exec("UPDATE node_environments SET desired_default_software_id=? WHERE id=?", softwareID, e.ID)
	}
	if removed {
		_, err = tx.Exec("UPDATE node_environments SET desired_default_software_id=NULL WHERE id=? AND desired_default_software_id=?", e.ID, softwareID)
	}
	return err
}
