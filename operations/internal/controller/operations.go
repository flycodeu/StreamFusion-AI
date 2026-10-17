package controller

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

type target struct {
	sshTarget
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	InstallRoot string          `json:"installRoot"`
	Observation json.RawMessage `json:"observation"`
	ObservedAt  string          `json:"observedAt"`
}
type instance struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	TargetID      string          `json:"targetId"`
	Version       string          `json:"version"`
	DraftRevision string          `json:"draftRevision"`
	Generation    string          `json:"appliedGeneration"`
	Config        deploy.Config   `json:"config"`
	Current       string          `json:"currentReleaseId"`
	Previous      string          `json:"previousReleaseId"`
	Observation   json.RawMessage `json:"observation"`
	ObservedAt    string          `json:"observedAt"`
}
type plan struct {
	MaintenancePermit  string          `json:"maintenancePermit,omitempty"`
	ID                 string          `json:"id"`
	Kind               string          `json:"kind"`
	TargetID           string          `json:"targetId"`
	InstanceID         string          `json:"instanceId"`
	ExpectedVersion    string          `json:"expectedVersion"`
	ExpectedGeneration string          `json:"expectedGeneration"`
	Candidate          deploy.Release  `json:"candidate"`
	Baseline           *deploy.Release `json:"baseline"`
	ExpiresAt          string          `json:"expiresAt"`
	InputHash          string          `json:"inputHash"`
}
type importInput struct {
	Recipe         string `json:"recipe,omitempty"`
	Name           string `json:"name"`
	SHA            string `json:"archiveSha256"`
	Size           int64  `json:"byteSize"`
	IdempotencyKey string `json:"idempotencyKey"`
}

func jsonText(v any) string { b, _ := json.Marshal(v); return string(b) }
func validName(n string) bool {
	return len(strings.TrimSpace(n)) > 0 && len([]rune(n)) <= 60 && !strings.ContainsAny(n, "\r\n\x00")
}
func (h *HTTP) opsError(w http.ResponseWriter, r *http.Request, e error) {
	if errors.Is(e, sql.ErrNoRows) {
		h.fail(w, r, 404, "NOT_FOUND", "对象不存在")
		return
	}
	h.internal(w, r, e)
}
func (s *Store) controllerID() string {
	var id string
	_ = s.DB.QueryRow("SELECT controller_id FROM controller_state WHERE id=1").Scan(&id)
	return id
}
func (s *Store) readTarget(id string) (target, error) {
	var t target
	var raw string
	e := s.DB.QueryRow("SELECT id,name,install_root,observation_json,observed_at,kind,address,ssh_port,ssh_user,host_fingerprint,coalesce(credential_id,''),coalesce(CAST(credential_revision AS TEXT),'') FROM targets WHERE id=?", id).Scan(&t.ID, &t.Name, &t.InstallRoot, &raw, &t.ObservedAt, &t.Kind, &t.Address, &t.Port, &t.User, &t.Fingerprint, &t.CredentialID, &t.Revision)
	t.Observation = json.RawMessage(raw)
	return t, e
}
func (s *Store) readInstance(id string) (instance, error) {
	var i instance
	var conf, raw string
	e := s.DB.QueryRow(`SELECT i.id,i.name,i.target_id,CAST(i.version AS TEXT),CAST(i.draft_revision AS TEXT),CAST(i.applied_generation AS TEXT),i.draft_json,coalesce(i.current_release_id,''),coalesce(i.previous_release_id,''),coalesce(o.payload_json,'{}'),coalesce(o.observed_at,'') FROM instances i LEFT JOIN observations o ON o.instance_id=i.id WHERE i.id=?`, id).Scan(&i.ID, &i.Name, &i.TargetID, &i.Version, &i.DraftRevision, &i.Generation, &conf, &i.Current, &i.Previous, &raw, &i.ObservedAt)
	if e == nil {
		e = json.Unmarshal([]byte(conf), &i.Config)
	}
	i.Observation = json.RawMessage(raw)
	return i, e
}
func (s *Store) readRelease(id string) (deploy.Release, error) {
	var r deploy.Release
	var raw string
	e := s.DB.QueryRow("SELECT snapshot_json FROM releases WHERE id=?", id).Scan(&raw)
	if e == nil {
		e = deploy.StrictJSON([]byte(raw), &r)
	}
	return r, e
}
func (s *Store) artifactPath(id string) string { return filepath.Join(s.Dir, "packages", id+".tar") }
func (s *Store) uploadPath(id string) string   { return filepath.Join(s.Dir, "packages", id+".partial") }

func (h *HTTP) operationsRoute(w http.ResponseWriter, r *http.Request, s *session, path string) bool {
	if h.bindingsRoute(w, r, s, path) {
		return true
	}
	switch r.Method + " " + path {
	case "GET /plugins":
		h.listPlugins(w, r)
		return true
	case "GET /plugin-installations":
		h.listPluginInstallations(w, r)
		return true
	case "GET /targets":
		h.listTargets(w, r)
		return true
	case "POST /targets":
		h.addTarget(w, r, s)
		return true
	case "GET /instances":
		h.listInstances(w, r)
		return true
	case "POST /instances":
		h.saveInstance(w, r, s, "")
		return true
	case "GET /artifacts":
		h.listArtifacts(w, r)
		return true
	case "POST /artifact-imports":
		h.acceptImport(w, r, s)
		return true
	case "GET /jobs":
		h.listJobs(w, r)
		return true
	}
	p := strings.Split(strings.Trim(path, "/"), "/")
	if len(p) == 3 {
		switch r.Method + " " + p[0] + " " + p[2] {
		case "GET plugins versions":
			h.listPluginVersions(w, r, p[1])
			return true
		case "POST targets plugin-install-plans":
			h.createPluginPlan(w, r, s, p[1])
			return true
		case "POST plugin-install-plans submit":
			h.submitPluginPlan(w, r, s, p[1])
			return true
		case "POST plugin-installations verify":
			h.verifyPluginInstallation(w, r, s, p[1])
			return true
		case "POST plugin-installations remove":
			h.removePluginInstallation(w, r, s, p[1])
			return true
		case "POST jobs cancel":
			h.cancelPluginJob(w, r, s, p[1])
			return true
		case "GET jobs logs":
			h.pluginJobLogs(w, r, p[1])
			return true
		}
	}
	if len(p) < 2 || !deploy.ID.MatchString(p[1]) {
		return false
	}
	if len(p) == 2 && p[0] == "plugin-installations" && r.Method == "GET" {
		h.getPluginInstallation(w, r, p[1])
		return true
	}
	if len(p) == 2 && p[0] == "instances" && r.Method == "PATCH" {
		h.saveInstance(w, r, s, p[1])
		return true
	}
	if len(p) == 2 && r.Method == "GET" {
		switch p[0] {
		case "jobs":
			h.getJob(w, r, p[1])
			return true
		case "plans":
			var raw string
			if e := h.store.DB.QueryRow("SELECT snapshot_json FROM operation_plans WHERE id=?", p[1]).Scan(&raw); e != nil {
				h.opsError(w, r, e)
			} else {
				h.success(w, r, json.RawMessage(raw))
			}
			return true
		case "targets":
			t, e := h.store.readTarget(p[1])
			if e != nil {
				h.opsError(w, r, e)
			} else {
				h.success(w, r, t)
			}
			return true
		case "instances":
			i, e := h.store.readInstance(p[1])
			if e != nil {
				h.opsError(w, r, e)
			} else {
				h.success(w, r, i)
			}
			return true
		}
	}
	if len(p) == 3 {
		switch r.Method + " " + p[0] + " " + p[2] {
		case "POST bindings probes":
			h.probeBinding(w, r, p[1])
			return true
		case "POST targets probes":
			t, e := h.store.readTarget(p[1])
			if e != nil {
				h.opsError(w, r, e)
				return true
			}
			out, e := h.probe(t)
			if e != nil {
				h.fail(w, r, 409, "TARGET_NOT_READY", "目标执行器、身份或Docker不可用，请核对目标安装与控制器日志")
				h.logger.Printf("traceId=%s targetId=%s probe=%v", r.Context().Value(traceKey{}), t.ID, e)
			} else {
				h.success(w, r, out)
			}
			return true
		case "POST instances plans":
			h.createPlan(w, r, s, p[1])
			return true
		case "GET instances releases":
			h.listReleases(w, r, p[1])
			return true
		case "GET instances logs":
			h.instanceLogs(w, r, p[1])
			return true
		case "GET instances preview-registration":
			h.previewRegistration(w, r, p[1])
			return true
		case "POST instances observations":
			h.observeInstance(w, r, p[1])
			return true
		case "POST plans submit":
			h.submitPlan(w, r, s, p[1])
			return true
		case "PUT jobs input":
			h.uploadImport(w, r, s, p[1])
			return true
		case "POST jobs reconcile":
			h.reconcile(w, r, p[1], false)
			return true
		case "POST jobs recover":
			h.reconcile(w, r, p[1], true)
			return true
		}
	}
	if len(p) == 4 && p[0] == "jobs" && p[2] == "input" && p[3] == "complete" && r.Method == "POST" {
		h.completeImport(w, r, p[1])
		return true
	}
	return false
}
