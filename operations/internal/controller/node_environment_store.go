package controller

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

type nodeEnvironment struct {
	ID                       string                            `json:"id"`
	HostID                   string                            `json:"hostId"`
	Name                     string                            `json:"name"`
	Account                  string                            `json:"account"`
	AccountUID               string                            `json:"accountUid"`
	RootPath                 string                            `json:"rootPath"`
	ManagerType              string                            `json:"managerType"`
	ManagementMode           string                            `json:"managementMode"`
	NVMVersion               string                            `json:"nvmVersion"`
	NVMHash                  string                            `json:"nvmHash"`
	DesiredDefaultSoftwareID string                            `json:"desiredDefaultSoftwareId"`
	ObservedDefaultVersion   string                            `json:"observedDefaultVersion"`
	Observation              deploy.NodeEnvironmentObservation `json:"observation"`
	ObservedAt               string                            `json:"observedAt"`
	ErrorCode                string                            `json:"errorCode"`
	Version                  string                            `json:"version"`
	LastJobID                string                            `json:"lastJobId"`
	CreatedAt                string                            `json:"createdAt"`
	UpdatedAt                string                            `json:"updatedAt"`
	Versions                 []softwareInstance                `json:"versions"`
}

const nodeEnvironmentColumns = `id,host_id,name,account,CAST(account_uid AS TEXT),root_path,manager_type,management_mode,nvm_version,nvm_hash,coalesce(desired_default_software_id,''),observed_default_version,observation_json,observed_at,error_code,CAST(version AS TEXT),last_job_id,created_at,updated_at`

func scanNodeEnvironment(row interface{ Scan(...any) error }) (nodeEnvironment, error) {
	var n nodeEnvironment
	var raw string
	err := row.Scan(&n.ID, &n.HostID, &n.Name, &n.Account, &n.AccountUID, &n.RootPath, &n.ManagerType, &n.ManagementMode, &n.NVMVersion, &n.NVMHash, &n.DesiredDefaultSoftwareID, &n.ObservedDefaultVersion, &raw, &n.ObservedAt, &n.ErrorCode, &n.Version, &n.LastJobID, &n.CreatedAt, &n.UpdatedAt)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &n.Observation)
	}
	n.Versions = []softwareInstance{}
	return n, err
}

func (s *Store) readNodeEnvironment(id string) (nodeEnvironment, error) {
	return scanNodeEnvironment(s.DB.QueryRow("SELECT "+nodeEnvironmentColumns+" FROM node_environments WHERE id=?", id))
}

func (s *Store) nodeEnvironmentVersions(id string) ([]softwareInstance, error) {
	rows, err := s.DB.Query("SELECT "+softwareColumns+" FROM software_instances s JOIN targets t ON t.id=s.host_id WHERE s.node_environment_id=? ORDER BY s.created_at DESC", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []softwareInstance{}
	for rows.Next() {
		v, e := scanSoftware(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (n nodeEnvironment) context() serviceinstall.NodeContext {
	return serviceinstall.NodeContext{EnvironmentID: n.ID, EnvironmentVersion: n.Version, Account: n.Account, AccountUID: n.AccountUID, RootPath: n.RootPath, NVMVersion: n.NVMVersion, NVMHash: n.NVMHash, ExpectedDefault: n.ObservedDefaultVersion}
}

func nodeTargetUnlocked(tx *sql.Tx, hostID string) error {
	var count int
	if err := tx.QueryRow("SELECT count(*) FROM operation_locks WHERE resource_key=?", "target:"+hostID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("OPERATION_LOCKED")
	}
	return nil
}

func nodeEnvironmentCurrent(tx *sql.Tx, n nodeEnvironment) error {
	current, err := scanNodeEnvironment(tx.QueryRow("SELECT "+nodeEnvironmentColumns+" FROM node_environments WHERE id=?", n.ID))
	if err != nil {
		return err
	}
	if current.Version != n.Version || current.ManagementMode == "RELEASED" || current.RootPath != n.RootPath || current.AccountUID != n.AccountUID {
		return errors.New("NODE_ENVIRONMENT_CHANGED")
	}
	return nil
}

func nodeObservationMatches(n nodeEnvironment, o deploy.NodeEnvironmentObservation) bool {
	if o.EnvironmentID != n.ID || o.Account != n.Account || o.AccountUID != n.AccountUID || o.RootPath != n.RootPath || o.ManagerType != n.ManagerType || !deploy.Hex.MatchString(o.Fingerprint) {
		return false
	}
	frozen := o
	frozen.ObservedAt = ""
	frozen.Fingerprint = ""
	if deploy.Digest(frozen) != o.Fingerprint {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, o.ObservedAt); err != nil {
		return false
	}
	uid, err := strconv.ParseUint(o.AccountUID, 10, 32)
	if err != nil || uid == 0 || !serviceinstall.NodeAccountPattern.MatchString(o.Account) || o.Account == "root" || len(o.Versions) > 64 {
		return false
	}
	if o.Exists && n.ManagementMode == "MANAGED" && (!serviceinstall.TrustedNodeManager(o.NVMVersion, o.NVMHash) || o.NVMVersion != n.NVMVersion || o.NVMHash != n.NVMHash) {
		return false
	}
	seen := map[string]bool{}
	for _, v := range o.Versions {
		if seen[v.Version] || !serviceinstall.NodeVersionPattern.MatchString(v.Version) || !deploy.Hex.MatchString(v.BinaryHash) {
			return false
		}
		seen[v.Version] = true
		if n.ManagementMode == "MANAGED" && v.BinaryPath != serviceinstall.NodeVersionRoot(n.context(), v.Version)+"/bin/node" {
			return false
		}
		if n.ManagementMode != "MANAGED" {
			expected := ""
			if o.ManagerType == "SYSTEM" && o.RootPath == "/usr" {
				expected = "/usr/bin/node"
			} else if o.ManagerType == "NVM" && strings.HasPrefix(o.RootPath, "/home/") && strings.HasSuffix(o.RootPath, "/.nvm") {
				expected = o.RootPath + "/versions/node/" + v.Version + "/bin/node"
			}
			if expected == "" || v.BinaryPath != expected {
				return false
			}
		}
		if len(v.GlobalPackages) > 256 || len(v.NPMVersion) > 64 || len(v.PNPMVersion) > 64 {
			return false
		}
	}
	return o.DefaultVersion == "" || seen[o.DefaultVersion]
}

func storeNodeObservation(tx *sql.Tx, id string, o deploy.NodeEnvironmentObservation) error {
	_, err := tx.Exec("UPDATE node_environments SET nvm_version=?,nvm_hash=?,observed_default_version=?,observation_json=?,observed_at=?,error_code='',version=version+1,updated_at=? WHERE id=?", o.NVMVersion, o.NVMHash, o.DefaultVersion, jsonText(o), o.ObservedAt, Now(), id)
	return err
}
