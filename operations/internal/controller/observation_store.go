package controller

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

type observationAttempt struct {
	Code    string `json:"code"`
	At      string `json:"at"`
	TraceID string `json:"traceId,omitempty"`
}
type observationPrincipal struct {
	Session *session
	BatchID string
}

const (
	hostObservationFreshnessTTL     = 10 * time.Minute
	softwareObservationFreshnessTTL = 15 * time.Minute
	firewallObservationFreshnessTTL = 15 * time.Minute
	hostCapabilityFreshnessTTL      = 30 * time.Minute
)

type hostObservation struct {
	HostID    string             `json:"hostId"`
	Confirmed *hostMetrics       `json:"lastConfirmed,omitempty"`
	Attempt   observationAttempt `json:"latestAttempt"`
	Revision  int64              `json:"revision"`
	Enabled   bool               `json:"enabled"`
	Freshness string             `json:"freshness"`
}

func observationIdentity(t target) string { return deploy.Digest(t.sshTarget) }
func softwareObservationIdentity(t target, i softwareInstance) string {
	return deploy.Digest(struct {
		Host, Kind, Locator, Version string
		Binding                      int64
	}{observationIdentity(t), i.RuntimeKind, i.Locator, i.Version, i.BindingRevision})
}
func observationFreshness(at string, ttl time.Duration) string {
	t, e := time.Parse(time.RFC3339Nano, at)
	if e != nil {
		return "UNOBSERVED"
	}
	if d := time.Since(t); d >= 0 && d < ttl {
		return "FRESH"
	}
	return "STALE"
}
func (s *Store) readHostObservation(t target) (hostObservation, error) {
	o := hostObservation{HostID: t.ID, Enabled: true, Freshness: "UNOBSERVED", Attempt: observationAttempt{Code: "NOT_CHECKED"}}
	var identity, confirmed, attempt string
	e := s.DB.QueryRow("SELECT identity,confirmed_json,attempt_json,revision FROM host_observation_snapshots WHERE host_id=?", t.ID).Scan(&identity, &confirmed, &attempt, &o.Revision)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return o, e
	}
	if e == nil && identity == observationIdentity(t) {
		if confirmed != "{}" {
			if e = json.Unmarshal([]byte(confirmed), &o.Confirmed); e != nil {
				return o, e
			}
		}
		if e = json.Unmarshal([]byte(attempt), &o.Attempt); e != nil {
			return o, e
		}
		if o.Confirmed != nil {
			o.Freshness = observationFreshness(o.Confirmed.ObservedAt, hostObservationFreshnessTTL)
		}
	}
	e = s.DB.QueryRow("SELECT enabled FROM observation_plans WHERE host_id=?", t.ID).Scan(&o.Enabled)
	if errors.Is(e, sql.ErrNoRows) {
		e = nil
	}
	return o, e
}

// A probe can update observation facts only. Identity is checked in the same
// transaction as publication; rename/maintenance do not invalidate resources.
func (h *HTTP) saveHostObservation(t target, m *hostMetrics, trace string) error {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, e := h.store.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	current, e := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", t.ID))
	if e != nil {
		return e
	}
	if observationIdentity(current) != observationIdentity(t) {
		return errors.New("HOST_CHANGED")
	}
	if !credentialCurrent(tx, t) && (m.Code == "READY" || m.Code == "PARTIAL") {
		return errors.New("CREDENTIAL_UNAVAILABLE")
	}
	confirmed := "{}"
	if m.Code == "READY" || m.Code == "PARTIAL" {
		confirmed = jsonText(m)
	}
	_, e = tx.Exec(`INSERT INTO host_observation_snapshots(host_id,identity,confirmed_json,attempt_json,revision) VALUES(?,?,?,?,1)
ON CONFLICT(host_id) DO UPDATE SET identity=excluded.identity,confirmed_json=CASE WHEN excluded.confirmed_json<>'{}' THEN excluded.confirmed_json WHEN identity=excluded.identity THEN confirmed_json ELSE '{}' END,attempt_json=excluded.attempt_json,revision=revision+1`, t.ID, observationIdentity(t), confirmed, jsonText(observationAttempt{m.Code, m.ObservedAt, trace}))
	if e == nil {
		e = tx.Commit()
	}
	return e
}
func credentialCurrent(tx *sql.Tx, t target) bool {
	if t.Kind == "LOCAL" {
		return true
	}
	var n int
	e := tx.QueryRow("SELECT count(*) FROM credential_refs WHERE id=? AND CAST(revision AS TEXT)=? AND revoked_at IS NULL", t.CredentialID, t.Revision).Scan(&n)
	return e == nil && n == 1
}
func (h *HTTP) saveSoftwareObservation(t target, i softwareInstance, obs softwareObservation, trace string, principals ...observationPrincipal) error {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, e := h.store.DB.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, principal := range principals {
		var n int
		if principal.Session != nil {
			e = tx.QueryRow("SELECT count(*) FROM sessions s JOIN admins a ON a.id=s.admin_id WHERE s.token_hash=? AND s.generation=a.session_generation AND s.expires_at>? AND s.last_seen>?", principal.Session.Hash, time.Now().Unix(), time.Now().Add(-30*time.Minute).Unix()).Scan(&n)
		} else if principal.BatchID != "" {
			e = tx.QueryRow("SELECT count(*) FROM observation_batches b JOIN admins a ON a.id=b.actor_id WHERE b.id=? AND b.actor_generation=a.session_generation", principal.BatchID).Scan(&n)
		} else {
			continue
		}
		if e != nil {
			return e
		}
		if n != 1 {
			return errors.New("AUTH_REVOKED")
		}
	}
	current, e := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", t.ID))
	if e != nil {
		return e
	}
	if observationIdentity(current) != observationIdentity(t) {
		return errors.New("HOST_CHANGED")
	}
	if !credentialCurrent(tx, t) && obs.ErrorCode == "" {
		return errors.New("CREDENTIAL_UNAVAILABLE")
	}
	latest, e := scanSoftware(tx.QueryRow("SELECT "+softwareColumns+" FROM software_instances s JOIN targets t ON t.id=s.host_id WHERE s.id=?", i.ID))
	if e != nil {
		return e
	}
	if latest.Version != i.Version || latest.ManagementMode == "RELEASED" {
		return errors.New("VERSION_CONFLICT")
	}
	if obs.ErrorCode == "" && obs.Kind == "DOCKER" {
		var n int
		if e = tx.QueryRow("SELECT count(*) FROM software_instances WHERE id<>? AND host_id=? AND management_mode<>'RELEASED' AND runtime_kind='DOCKER' AND (locator=? OR json_extract(observation_json,'$.locator')=?)", i.ID, i.HostID, obs.Locator, obs.Locator).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			obs = failedSoftwareObservation(i, "SOFTWARE_ALREADY_REGISTERED")
		}
	}
	code := obs.ErrorCode
	if code == "" {
		code = "READY"
	}
	busy, e := softwareBusy(tx, i.ID)
	if e != nil {
		return e
	}
	// Even a known running process cannot resolve a pending mutation or ACK.
	if obs.ErrorCode == "" {
		_, e = tx.Exec(`UPDATE software_instances SET observation_json=?,observed_at=?,attempt_json=?,observation_identity=?,observation_revision=observation_revision+1,
actual_state=CASE WHEN ? THEN actual_state ELSE ? END,error_code=CASE WHEN ? THEN error_code ELSE '' END WHERE id=?`, jsonText(obs), obs.ObservedAt, jsonText(observationAttempt{code, Now(), trace}), observationIdentity(t), busy, obs.State, busy, i.ID)
	} else {
		_, e = tx.Exec("UPDATE software_instances SET actual_state=CASE WHEN ? THEN actual_state ELSE 'UNKNOWN' END,attempt_json=?,observation_revision=observation_revision+1 WHERE id=?", busy, jsonText(observationAttempt{code, Now(), trace}), i.ID)
	}
	if e == nil {
		e = tx.Commit()
	}
	return e
}

func migrateObservationIdentities(tx *sql.Tx) error {
	rows, e := tx.Query("SELECT " + hostColumns + " FROM targets")
	if e != nil {
		return e
	}
	var hosts []target
	for rows.Next() {
		host, e := scanHost(rows)
		if e != nil {
			rows.Close()
			return e
		}
		hosts = append(hosts, host)
	}
	if e = rowsError(rows, nil); e != nil {
		return e
	}
	for _, host := range hosts {
		if _, e = tx.Exec("UPDATE software_instances SET observation_identity=? WHERE host_id=? AND observed_at<>''", observationIdentity(host), host.ID); e != nil {
			return e
		}
	}
	return nil
}
