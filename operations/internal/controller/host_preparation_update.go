package controller

import (
	"context"
	"io"
	"strings"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

type preparationUpdate struct {
	Status        string `json:"status"`
	Code          string `json:"code,omitempty"`
	CurrentSHA256 string `json:"currentSHA256,omitempty"`
	PackageSHA256 string `json:"packageSHA256,omitempty"`
}

// An old successful receipt is provenance, not a current observation. Both are
// required before an unattended update; an offline installation stays manual.
func (h *HTTP) knownPreparationBinary(t target, sha string) bool {
	rows, err := h.store.DB.Query("SELECT input_json FROM jobs WHERE target_id=? AND kind='HOST_PREPARE' AND status='SUCCEEDED' ORDER BY created_at DESC,id DESC", t.ID)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var snapshot preparationSnapshot
		if rows.Scan(&raw) != nil || deploy.StrictJSON([]byte(raw), &snapshot) != nil {
			continue
		}
		if snapshot.Host.ID == t.ID && snapshot.Host.sshTarget == t.sshTarget && snapshot.Host.InstallRoot == t.InstallRoot && (snapshot.BinarySHA == sha || snapshot.RecoveryBinarySHA == sha) {
			return true
		}
	}
	return false
}

// ownJob only excludes the update job whose target lock we already hold.
// Other software/import jobs, pending ACKs and unknown operations still block.
func (h *HTTP) checkPreparationUpdate(ctx context.Context, t target, ownJob string) preparationUpdate {
	u := preparationUpdate{Status: "BLOCKED"}
	current, err := h.store.readTarget(t.ID)
	if err != nil || current.Version != t.Version || current.sshTarget != t.sshTarget || current.InstallRoot != t.InstallRoot {
		u.Code = "PREPARATION_HOST_CHANGED"
		return u
	}
	if current.Maintenance {
		u.Code = "HOST_MAINTENANCE"
		return u
	}
	if t.Kind != "SSH" {
		u.Status = "MANUAL_REQUIRED"
		u.Code = "SSH_TARGET_REQUIRED"
		return u
	}
	var busy int
	err = h.store.DB.QueryRow(`SELECT (SELECT count(*) FROM jobs WHERE id<>? AND (target_id=? OR (kind='IMPORT' AND json_extract(CASE WHEN json_valid(input_json) THEN input_json ELSE '{}' END,'$.runtimeEnvironment.targetId')=?)) AND (status IN ('QUEUED','RUNNING','UNKNOWN') OR target_ack_pending=1)) + (SELECT count(*) FROM operation_locks WHERE resource_key=? AND job_id<>?)`, ownJob, t.ID, t.ID, "target:"+t.ID, ownJob).Scan(&busy)
	if err != nil || busy != 0 {
		u.Code = "HOST_BUSY"
		return u
	}
	if ownJob != "" {
		var held int
		if h.store.DB.QueryRow("SELECT count(*) FROM jobs j JOIN operation_locks l ON l.job_id=j.id WHERE j.id=? AND j.target_id=? AND j.kind='HOST_PREPARE' AND j.status='RUNNING' AND l.resource_key=?", ownJob, t.ID, "target:"+t.ID).Scan(&held) != nil || held != 1 {
			u.Code = "HOST_BUSY"
			return u
		}
	}
	material, err := readTargetPackage(h.targetPackageDir)
	if err != nil {
		u.Code = err.Error()
		return u
	}
	u.PackageSHA256 = material.Binary.Hash
	if ready, code := h.verifyPreparation(ctx, t, ""); !ready {
		u.Status = "MANUAL_REQUIRED"
		u.Code = code
		return u
	}
	client, closeClient, err := h.store.connectSSH(ctx, t)
	if err != nil {
		u.Code, _ = hostSSHError(err)
		return u
	}
	defer closeClient()
	out := &limitedOutput{limit: 512}
	if runSSHCommand(client, "sha256sum -- "+shellQuote(t.InstallRoot+"/executor"), nil, out, io.Discard) != nil {
		u.Status = "MANUAL_REQUIRED"
		u.Code = "EXECUTOR_HASH_UNAVAILABLE"
		return u
	}
	parts := strings.Fields(out.String())
	if len(parts) != 2 || !validSHA256(parts[0]) || parts[1] != t.InstallRoot+"/executor" {
		u.Status = "MANUAL_REQUIRED"
		u.Code = "EXECUTOR_HASH_MISMATCH"
		return u
	}
	u.CurrentSHA256 = parts[0]
	if u.CurrentSHA256 == u.PackageSHA256 {
		u.Status = "CURRENT"
		return u
	}
	if !h.knownPreparationBinary(t, u.CurrentSHA256) {
		u.Status = "MANUAL_REQUIRED"
		u.Code = "PREPARATION_UPDATE_UNKNOWN_BINARY"
		return u
	}
	u.Status = "AVAILABLE"
	return u
}

func (h *HTTP) verifyPreparationUpdate(ctx context.Context, t target, job string, snapshot preparationSnapshot) string {
	material, err := readTargetPackage(h.targetPackageDir)
	if err != nil {
		return err.Error()
	}
	if material.Binary.Hash != snapshot.BinarySHA || material.Script.Hash != snapshot.ScriptSHA {
		return "PREPARATION_MATERIAL_CHANGED"
	}
	u := h.checkPreparationUpdate(ctx, t, job)
	if u.PackageSHA256 != "" && u.PackageSHA256 != snapshot.BinarySHA {
		return "PREPARATION_MATERIAL_CHANGED"
	}
	if u.Status != "AVAILABLE" || u.CurrentSHA256 != snapshot.UpdateFromSHA {
		if u.Code != "" {
			return u.Code
		}
		return "PREPARATION_UPDATE_CHANGED"
	}
	return ""
}
