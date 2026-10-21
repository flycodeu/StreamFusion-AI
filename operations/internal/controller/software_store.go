package controller

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

type softwareInstance struct {
	ConfigurationJobID   string                   `json:"configurationJobId,omitempty"`
	Installation         *serviceinstall.Request  `json:"installation,omitempty"`
	InstallationStatus   string                   `json:"installationStatus"`
	InstallationJobID    string                   `json:"installationJobId,omitempty"`
	InstallationEvidence *serviceinstall.Evidence `json:"installationEvidence,omitempty"`
	ControllerID         string                   `json:"-"`
	ID                   string                   `json:"id"`
	Name                 string                   `json:"name"`
	HostID               string                   `json:"hostId"`
	HostName             string                   `json:"hostName"`
	SoftwareType         string                   `json:"softwareType"`
	SoftwareVersion      string                   `json:"softwareVersion"`
	RuntimeKind          string                   `json:"runtimeKind"`
	Locator              string                   `json:"locator"`
	ManagementMode       string                   `json:"managementMode"`
	DesiredState         string                   `json:"desiredState"`
	ActualState          string                   `json:"actualState"`
	Observation          softwareObservation      `json:"observation"`
	Freshness            string                   `json:"freshness"`
	Version              string                   `json:"version"`
	LastJobID            string                   `json:"lastJobId"`
	ErrorCode            string                   `json:"errorCode"`
	DataOwnership        string                   `json:"dataOwnership"`
	CreatedAt            string                   `json:"createdAt"`
	UpdatedAt            string                   `json:"updatedAt"`
	IdentityHash         string                   `json:"-"`
	BindingRevision      int64                    `json:"-"`
	Attempt              observationAttempt       `json:"latestAttempt"`
	ObservationRevision  int64                    `json:"observationRevision"`
}

type softwareObservation struct {
	*deploy.SoftwareObservation
	ErrorCode string `json:"errorCode"`
	Message   string `json:"message"`
	Source    string `json:"source"`
}

const softwareColumns = `s.id,s.name,s.host_id,t.name,s.software_type,s.software_version,s.runtime_kind,s.locator,s.management_mode,s.desired_state,s.actual_state,s.observation_json,CAST(s.version AS TEXT),s.last_job_id,s.error_code,s.data_ownership,s.created_at,s.updated_at,s.identity_hash,s.binding_revision,s.observed_at,s.attempt_json,s.observation_revision,s.observation_identity,t.kind,t.address,t.ssh_port,t.ssh_user,t.host_fingerprint,coalesce(t.credential_id,''),coalesce(CAST(t.credential_revision AS TEXT),''),s.installation_json,s.installation_status,CASE WHEN s.configuration_job_id<>'' THEN s.configuration_job_id ELSE coalesce((SELECT j.id FROM jobs j WHERE j.software_plan_id=s.install_plan_id),'') END,(SELECT controller_id FROM controller_state WHERE id=1),coalesce((SELECT j.result_json FROM jobs j WHERE j.software_plan_id=s.install_plan_id),'{}'),s.installation_evidence_json,s.configuration_job_id`

func scanSoftware(row interface{ Scan(...any) error }) (softwareInstance, error) {
	var s softwareInstance
	var raw, observed string
	var attempt, identity string
	var installation string
	var installReceipt, evidenceRaw string
	var host target
	err := row.Scan(&s.ID, &s.Name, &s.HostID, &s.HostName, &s.SoftwareType, &s.SoftwareVersion, &s.RuntimeKind, &s.Locator, &s.ManagementMode, &s.DesiredState, &s.ActualState, &raw, &s.Version, &s.LastJobID, &s.ErrorCode, &s.DataOwnership, &s.CreatedAt, &s.UpdatedAt, &s.IdentityHash, &s.BindingRevision, &observed, &attempt, &s.ObservationRevision, &identity, &host.Kind, &host.Address, &host.Port, &host.User, &host.Fingerprint, &host.CredentialID, &host.Revision, &installation, &s.InstallationStatus, &s.InstallationJobID, &s.ControllerID, &installReceipt, &evidenceRaw, &s.ConfigurationJobID)
	if err != nil {
		return s, err
	}
	if installation != "{}" && installation != "" {
		var installed serviceinstall.Request
		if err = deploy.StrictJSON([]byte(installation), &installed); err != nil {
			return s, err
		}
		s.Installation = &installed
		var receipt deploy.Result
		if err = json.Unmarshal([]byte(installReceipt), &receipt); err != nil {
			return s, err
		}
		s.InstallationEvidence = receipt.Installation
		if evidenceRaw != "{}" && evidenceRaw != "" {
			var evidence serviceinstall.Evidence
			if err = json.Unmarshal([]byte(evidenceRaw), &evidence); err != nil {
				return s, err
			}
			s.InstallationEvidence = &evidence
		}
	}
	s.Observation.SoftwareObservation = &deploy.SoftwareObservation{State: "UNKNOWN"}
	if err = json.Unmarshal([]byte(raw), &s.Observation); err != nil {
		return s, err
	}
	if err = json.Unmarshal([]byte(attempt), &s.Attempt); err != nil {
		return s, err
	}
	if s.Attempt.Code != "" && s.Attempt.Code != "READY" && s.ErrorCode == "" {
		s.ErrorCode = s.Attempt.Code
	}
	if identity != "" && identity != observationIdentity(host) {
		s.Observation = softwareObservation{SoftwareObservation: &deploy.SoftwareObservation{State: "UNKNOWN"}}
		observed = ""
		s.Attempt = observationAttempt{Code: "CONFIG_CHANGED"}
		s.ActualState = "UNKNOWN"
	}
	s.Freshness = observationFreshness(observed, 3*time.Minute)
	if s.ManagementMode == "OBSERVE" && s.Observation.IdentityHash != "" {
		s.IdentityHash = s.Observation.IdentityHash
	}
	return s, nil
}

func (s *Store) readSoftware(id string) (softwareInstance, error) {
	return scanSoftware(s.DB.QueryRow("SELECT "+softwareColumns+" FROM software_instances s JOIN targets t ON t.id=s.host_id WHERE s.id=?", id))
}

func softwareRequest(s softwareInstance) *deploy.SoftwareRequest {
	locator := s.Locator
	if s.ManagementMode == "OBSERVE" && s.Observation.SoftwareObservation != nil && s.Observation.Locator != "" {
		locator = s.Observation.Locator
	}
	req := &deploy.SoftwareRequest{ID: s.ID, Version: s.BindingRevision, Kind: s.RuntimeKind, Locator: locator, IdentityHash: s.IdentityHash, SoftwareKey: s.SoftwareType}
	if s.Installation != nil && s.IdentityHash != "" && s.ManagementMode == "MANAGED" {
		req.Ownership = &deploy.SoftwareOwnership{ControllerID: s.ControllerID, TargetID: s.HostID, SoftwareID: s.ID, JobID: s.InstallationJobID, ConfigHash: serviceinstall.Digest(s.Installation.Config), ImageID: s.Installation.Definition.ImageID}
		if s.Installation.Config.Mode() == "NATIVE" {
			req.Ownership.RuntimeMode = "NATIVE"
			req.Ownership.ImageID = ""
			if s.InstallationEvidence != nil {
				req.Ownership.BinaryPath = s.InstallationEvidence.BinaryPath
				req.Ownership.BinaryHash = s.InstallationEvidence.BinaryHash
			}
		} else if s.RuntimeKind == "TOOL" {
			req.Ownership.RuntimeMode = "DOCKER"
		}

	}
	return req
}

func softwareBusy(q interface{ QueryRow(string, ...any) *sql.Row }, id string) (bool, error) {
	var n int
	err := q.QueryRow("SELECT count(*) FROM operation_locks WHERE resource_key=?", "software:"+id).Scan(&n)
	return n > 0, err
}
