package controller

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

// A host gate and four shared probe slots bound all observation work. The
// scheduler never waits on a busy host; durable items remain queued for its next tick.
func (h *HTTP) acquireObservation(ctx context.Context, id string, background bool) (func(), error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	h.observationMu.Lock()
	defer h.observationMu.Unlock()
	if h.observationHosts == nil {
		h.observationHosts = map[string]struct{}{}
	}
	if _, busy := h.observationHosts[id]; busy {
		return nil, errors.New("HOST_BUSY")
	}
	if background && len(h.hostCheckSlots) >= 3 {
		return nil, errors.New("CHECK_BUSY")
	}
	select {
	case h.hostCheckSlots <- struct{}{}:
	default:
		return nil, errors.New("CHECK_BUSY")
	}
	h.observationHosts[id] = struct{}{}
	return func() {
		h.observationMu.Lock()
		delete(h.observationHosts, id)
		<-h.hostCheckSlots
		h.observationMu.Unlock()
	}, nil
}

type observationPlan struct {
	Host                     target
	Enabled                  bool
	ResourceDue, SoftwareDue int64
	Cursor                   string
	Failures                 int
	AuthPaused               bool
	Identity                 string
}
type observationWork struct {
	Item     observationBatchItem
	Software softwareInstance
}

func (h *HTTP) RunObservations(ctx context.Context) {
	// Only read-only attempts are restartable. Mutation jobs remain untouched.
	h.store.mu.Lock()
	_, e := h.store.DB.Exec("UPDATE observation_batch_items SET status='QUEUED',error_code='INTERRUPTED' WHERE status='RUNNING'")
	if e == nil {
		_, e = h.store.DB.Exec("UPDATE observation_batches SET status='QUEUED' WHERE status='RUNNING'")
	}
	if e == nil {
		_, e = h.store.DB.Exec("UPDATE observation_plans SET resource_due=max(resource_due,?+(random() & 3)),software_due=max(software_due,?+(random() & 3))", time.Now().Unix(), time.Now().Unix())
	}
	h.store.mu.Unlock()
	if e != nil {
		h.logger.Printf("traceId=observation event=RECOVERY_FAILED")
		return
	}
	var workers sync.WaitGroup
	defer workers.Wait()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		h.scheduleObservations(ctx, &workers)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (h *HTTP) observationPlans() ([]observationPlan, error) {
	h.store.mu.Lock()
	_, e := h.store.DB.Exec("INSERT INTO observation_plans(host_id) SELECT id FROM targets WHERE true ON CONFLICT(host_id) DO NOTHING")
	h.store.mu.Unlock()
	if e != nil {
		return nil, e
	}
	rows, e := h.store.DB.Query("SELECT host_id,enabled,identity,resource_due,software_due,software_cursor,failures,auth_paused FROM observation_plans ORDER BY min(resource_due,software_due),host_id")
	if e != nil {
		return nil, e
	}
	var plans []observationPlan
	for rows.Next() {
		var p observationPlan
		if e = rows.Scan(&p.Host.ID, &p.Enabled, &p.Identity, &p.ResourceDue, &p.SoftwareDue, &p.Cursor, &p.Failures, &p.AuthPaused); e != nil {
			break
		}
		plans = append(plans, p)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return nil, e
	}
	for n := range plans {
		plans[n].Host, e = h.store.readTarget(plans[n].Host.ID)
		if e != nil {
			return nil, e
		}
	}
	return plans, nil
}
func (h *HTTP) scheduleObservations(ctx context.Context, workers *sync.WaitGroup) {
	if ctx.Err() != nil {
		return
	}
	plans, e := h.observationPlans()
	if e != nil {
		h.logger.Printf("traceId=observation event=PLAN_READ_FAILED")
		return
	}
	for _, p := range plans {
		now := time.Now().Unix()
		if p.Identity != observationIdentity(p.Host) {
			p.AuthPaused = false
			p.ResourceDue = 0
			p.SoftwareDue = 0
			p.Cursor = ""
			p.Failures = 0
		}
		pending, e := h.pendingObservationItems(p.Host.ID)
		if e != nil {
			continue
		}
		automatic := p.Enabled && !p.AuthPaused && (p.ResourceDue <= now || p.SoftwareDue <= now)
		if len(pending) == 0 && !automatic {
			continue
		}
		release, e := h.acquireObservation(ctx, p.Host.ID, len(pending) == 0)
		if e != nil {
			continue
		}
		workers.Add(1)
		go func(p observationPlan, items []observationBatchItem) {
			defer workers.Done()
			defer release()
			h.collectObservationCycle(ctx, p, items)
		}(p, pending)
	}
	h.finishObservationBatches()
}

func (h *HTTP) collectObservationCycle(parent context.Context, p observationPlan, items []observationBatchItem) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	trace := "obs-" + randomToken()[:20]
	resource := p.Enabled && !p.AuthPaused && p.ResourceDue <= time.Now().Unix()
	work := []observationWork{}
	seen := map[string]bool{}
	for _, item := range items {
		if !h.startObservationItem(item) {
			continue
		}
		if item.Kind == "HOST" {
			if item.ConfigHash != observationIdentity(p.Host) {
				h.completeObservationItem(item, "SKIPPED", "CONFIG_CHANGED")
				continue
			}
			resource = true
			continue
		}
		i, e := h.store.readSoftware(item.ObjectID)
		if e != nil || i.ManagementMode == "RELEASED" {
			h.completeObservationItem(item, "SKIPPED", "SOFTWARE_RELEASED")
			continue
		}
		if i.Installation != nil && !installationBound(i) {
			h.completeObservationItem(item, "SKIPPED", "INSTALLATION_NOT_BOUND")
			continue
		}
		if softwareObservationIdentity(p.Host, i) != item.ConfigHash {
			h.completeObservationItem(item, "SKIPPED", "CONFIG_CHANGED")
			continue
		}
		work = append(work, observationWork{item, i})
		seen[i.ID] = true
	}
	more := false
	if p.Enabled && !p.AuthPaused && p.SoftwareDue <= time.Now().Unix() {
		ids, e := h.dueSoftwareIDs(p.Host.ID, p.Cursor)
		if e != nil {
			return
		}
		for _, id := range ids {
			if len(work) >= 100 {
				more = true
				break
			}
			i, e := h.store.readSoftware(id)
			if e != nil {
				continue
			}
			if i.Installation != nil && !installationBound(i) {
				continue
			}
			if !seen[id] {
				work = append(work, observationWork{Software: i})
				seen[id] = true
			}
		}
	}
	failedCode := ""
	if resource {
		probeCtx, stop := context.WithTimeout(ctx, 10*time.Second)
		m := h.probeHostMetrics(probeCtx, p.Host)
		stop()
		e := h.saveHostObservation(p.Host, m, trace)
		code := m.Code
		if e != nil {
			code = "HOST_CHANGED"
		}
		if code != "READY" && code != "PARTIAL" {
			failedCode = code
		}
		for _, item := range items {
			if item.Kind == "HOST" {
				if !h.observationActorValid(item.BatchID) {
					h.completeObservationItem(item, "SKIPPED", "AUTH_REVOKED")
					continue
				}
				state := "SUCCEEDED"
				if failedCode != "" {
					state = "FAILED"
				}
				h.completeObservationItem(item, state, failedCode)
			}
		}
	}
	if len(work) > 0 {
		e := h.withSoftwareRunner(ctx, p.Host, func(run deploy.SoftwareCommandRunner) error {
			remaining := 240 << 10
			boundedRun := func(c context.Context, command string, args ...string) ([]byte, error) {
				out, e := run(c, command, args...)
				remaining -= len(out)
				if remaining < 0 {
					return nil, errors.New("SOFTWARE_OBSERVATION_TOO_LARGE")
				}
				return out, e
			}
			facts := map[string]softwareObservation{}
			for _, w := range work {
				if ctx.Err() != nil {
					more = true
					break
				}
				if w.Item.BatchID != "" && !h.observationActorValid(w.Item.BatchID) {
					h.completeObservationItem(w.Item, "SKIPPED", "AUTH_REVOKED")
					continue
				}
				probeCtx, stop := context.WithTimeout(ctx, 10*time.Second)
				obs, exists := facts[w.Software.ID]
				if !exists {
					obs = h.probeSoftware(probeCtx, p.Host, w.Software, boundedRun)
					facts[w.Software.ID] = obs
				}
				stop()
				if w.Item.BatchID == "" {
					p.Cursor = w.Software.ID
				}
				code := obs.ErrorCode
				if e := h.saveSoftwareObservation(p.Host, w.Software, obs, trace, observationPrincipal{BatchID: w.Item.BatchID}); e != nil {
					code = observationErrorCode(e)
				} else if saved, e := h.store.readSoftware(w.Software.ID); e == nil && saved.Attempt.Code != "READY" {
					code = saved.Attempt.Code
				}
				if code != "" {
					failedCode = code
				}
				if w.Item.BatchID != "" {
					state := "SUCCEEDED"
					if code != "" {
						state = "FAILED"
					}
					h.completeObservationItem(w.Item, state, code)
				}
			}
			return nil
		})
		if e != nil {
			failedCode, _ = hostSSHError(e)
			for _, w := range work {
				obs := failedSoftwareObservation(w.Software, failedCode)
				_ = h.saveSoftwareObservation(p.Host, w.Software, obs, trace, observationPrincipal{BatchID: w.Item.BatchID})
				if w.Item.BatchID != "" {
					h.completeObservationItem(w.Item, "FAILED", failedCode)
				}
			}
		}
	}
	if !more {
		p.Cursor = ""
	}
	// Time-budget exhaustion returns untouched items to the durable queue.
	h.store.mu.Lock()
	for _, item := range items {
		_, _ = h.store.DB.Exec("UPDATE observation_batch_items SET status='QUEUED',error_code='CYCLE_DEFERRED' WHERE batch_id=? AND object_id=? AND status='RUNNING'", item.BatchID, item.ObjectID)
	}
	delay := int64(30)
	if failedCode != "" {
		p.Failures++
		steps := []int64{30, 60, 120, 300}
		idx := p.Failures - 1
		if idx >= len(steps) {
			idx = len(steps) - 1
		}
		delay = steps[idx]
		delay += delay * int64(rand.IntN(21)-10) / 100
	} else {
		p.Failures = 0
	}
	if failedCode == "SSH_AUTH_FAILED" || failedCode == "SSH_HOST_KEY_CHANGED" || failedCode == "CREDENTIAL_UNAVAILABLE" {
		p.AuthPaused = true
	}
	if resource {
		p.ResourceDue = time.Now().Unix() + delay
	}
	if len(work) > 0 || p.SoftwareDue <= time.Now().Unix() {
		p.SoftwareDue = time.Now().Unix() + 60
		if failedCode != "" {
			p.SoftwareDue = time.Now().Unix() + delay
		}
		if more {
			p.SoftwareDue = time.Now().Unix() + 1
		}
	}
	_, e := h.store.DB.Exec("UPDATE observation_plans SET identity=?,resource_due=?,software_due=?,software_cursor=?,failures=?,auth_paused=? WHERE host_id=?", observationIdentity(p.Host), p.ResourceDue, p.SoftwareDue, p.Cursor, p.Failures, p.AuthPaused, p.Host.ID)
	h.store.mu.Unlock()
	if e != nil {
		h.logger.Printf("traceId=%s event=OBSERVATION_SAVE_FAILED hostId=%s", trace, p.Host.ID)
	}
}

func observationErrorCode(e error) string {
	switch e.Error() {
	case "HOST_CHANGED", "VERSION_CONFLICT", "AUTH_REVOKED", "CREDENTIAL_UNAVAILABLE":
		return e.Error()
	default:
		return "OBSERVATION_STORE_FAILED"
	}
}
func (h *HTTP) dueSoftwareIDs(host, cursor string) ([]string, error) {
	rows, e := h.store.DB.Query("SELECT id FROM software_instances WHERE host_id=? AND management_mode<>'RELEASED' AND id>? ORDER BY id LIMIT 101", host, cursor)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
