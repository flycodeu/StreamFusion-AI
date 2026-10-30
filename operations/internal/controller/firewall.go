package controller

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/deploy"
)

type firewallPlan struct {
	ID        string                  `json:"id"`
	HostID    string                  `json:"hostId"`
	Change    deploy.FirewallChange   `json:"change"`
	Snapshot  deploy.FirewallSnapshot `json:"snapshot"`
	ExpiresAt string                  `json:"expiresAt"`
}
type firewallView struct {
	Snapshot  deploy.FirewallSnapshot `json:"snapshot"`
	Code      string                  `json:"code"`
	AttemptAt string                  `json:"attemptAt"`
	TraceID   string                  `json:"traceId"`
	Freshness string                  `json:"freshness"`
}

func (h *HTTP) firewallRoute(w http.ResponseWriter, r *http.Request, s *session, path string) bool {
	p := strings.Split(strings.Trim(path, "/"), "/")
	if len(p) == 3 && p[0] == "hosts" && deploy.ID.MatchString(p[1]) {
		switch r.Method + " " + p[2] {
		case "GET ports":
			h.getHostFirewall(w, r, p[1])
			return true
		case "POST ports-observe":
			h.observeHostFirewall(w, r, s, p[1])
			return true
		case "POST firewall-plans":
			h.createFirewallPlan(w, r, s, p[1])
			return true
		}
	}
	if len(p) == 3 && p[0] == "firewall-plans" && p[2] == "submit" && r.Method == "POST" {
		h.submitFirewallPlan(w, r, s, p[1])
		return true
	}
	if len(p) == 3 && p[0] == "jobs" && p[2] == "firewall-confirm" && r.Method == "POST" {
		h.confirmFirewallJob(w, r, s, p[1])
		return true
	}
	return false
}
func (h *HTTP) getHostFirewall(w http.ResponseWriter, r *http.Request, id string) {
	t, e := h.store.readTarget(id)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	v := firewallView{Snapshot: deploy.FirewallSnapshot{Status: "UNOBSERVED", Ports: []deploy.HostPort{}, ManagedRules: []deploy.FirewallRule{}}}
	var raw, snapshot string
	e = h.store.DB.QueryRow("SELECT target_snapshot,payload_json,attempt_code,attempt_at,trace_id FROM host_firewall_observations WHERE host_id=?", id).Scan(&snapshot, &raw, &v.Code, &v.AttemptAt, &v.TraceID)
	if e == nil && snapshot == pluginTargetSnapshot(t) {
		if deploy.StrictJSON([]byte(raw), &v.Snapshot) != nil {
			h.internal(w, r, errors.New("FIREWALL_SNAPSHOT_DAMAGED"))
			return
		}
	} else if e != nil && !errors.Is(e, sql.ErrNoRows) {
		h.internal(w, r, e)
		return
	} else {
		v.Code = "FIREWALL_NOT_OBSERVED"
	}
	v.Freshness = observationFreshness(v.Snapshot.ObservedAt, firewallObservationFreshnessTTL)
	h.success(w, r, v)
}
func (h *HTTP) observeHostFirewall(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var in struct {
		ExpectedVersion string `json:"expectedVersion"`
	}
	if decode(w, r, &in) != nil {
		h.fail(w, r, 400, "INVALID_INPUT", "请提交主机版本")
		return
	}
	t, e := h.store.readTarget(id)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	if t.Version != in.ExpectedVersion {
		h.fail(w, r, 409, "HOST_CHANGED", "主机已变化，请刷新")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	release, e := h.acquireObservation(ctx, id, false)
	if e != nil {
		h.fail(w, r, 429, "HOST_CHECK_BUSY", "主机正在检测")
		return
	}
	defer release()
	v := firewallView{AttemptAt: Now(), TraceID: r.Context().Value(traceKey{}).(string)}
	e = h.store.callTarget(ctx, t, deploy.Request{Action: "FIREWALL_OBSERVE"}, "", &v.Snapshot)
	if e != nil {
		v.Code = "FIREWALL_EXECUTOR_REQUIRED"
		v.Snapshot = deploy.FirewallSnapshot{Status: "UNAVAILABLE", Code: v.Code, ObservedAt: Now(), Ports: []deploy.HostPort{}, ManagedRules: []deploy.FirewallRule{}}
		_ = h.withSoftwareRunner(ctx, t, func(run deploy.SoftwareCommandRunner) error {
			ports, docker, code, e := deploy.ObserveHostPorts(ctx, run)
			if e == nil {
				v.Snapshot.Ports, v.Snapshot.DockerPorts, v.Snapshot.DockerCode = ports, docker, code
			}
			return e
		})
	} else {
		v.Code = v.Snapshot.Code
	}
	h.store.mu.Lock()
	tx, e := h.beginAuthorized(s)
	if e == nil {
		defer tx.Rollback()
		current, e2 := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", id))
		if e2 != nil || pluginTargetSnapshot(current) != pluginTargetSnapshot(t) {
			e = errors.New("HOST_CHANGED")
		} else {
			_, e = tx.Exec(`INSERT INTO host_firewall_observations VALUES(?,?,?,?,?,?) ON CONFLICT(host_id) DO UPDATE SET payload_json=CASE WHEN excluded.attempt_code='' OR host_firewall_observations.target_snapshot<>excluded.target_snapshot THEN excluded.payload_json ELSE host_firewall_observations.payload_json END,target_snapshot=excluded.target_snapshot,attempt_code=excluded.attempt_code,attempt_at=excluded.attempt_at,trace_id=excluded.trace_id`, id, pluginTargetSnapshot(t), jsonText(v.Snapshot), v.Code, v.AttemptAt, v.TraceID)
			if e == nil {
				e = auditTx(tx, s.Username, "HOST_PORTS_OBSERVED", id, v.Code, v.TraceID)
			}
			if e == nil {
				e = tx.Commit()
			}
		}
	}
	h.store.mu.Unlock()
	if e != nil {
		if e.Error() == "HOST_CHANGED" || errors.Is(e, errSessionRevoked) {
			h.fail(w, r, 409, "HOST_CHANGED", "检测期间主机或会话已变化")
		} else {
			h.internal(w, r, e)
		}
		return
	}
	h.getHostFirewall(w, r, id)
}
func (h *HTTP) firewallPreflight(ctx context.Context, t target, c deploy.FirewallChange) (deploy.FirewallSnapshot, error) {
	var cap executorCapabilities
	if e := h.store.callTarget(ctx, t, deploy.Request{Action: "CAPABILITIES"}, "", &cap); e != nil {
		return deploy.FirewallSnapshot{}, e
	}
	if cap.Owner.ControllerID != h.store.controllerID() || cap.Owner.TargetID != t.ID || cap.Owner.ExecutorVersion != deploy.Version || cap.FirewallVersion != deploy.FirewallCapabilityVersion {
		return deploy.FirewallSnapshot{}, errors.New("FIREWALL_EXECUTOR_UPGRADE_REQUIRED")
	}
	var check struct {
		Snapshot deploy.FirewallSnapshot `json:"snapshot"`
		Code     string                  `json:"code"`
	}
	if e := h.store.callTarget(ctx, t, deploy.Request{Action: "FIREWALL_CHECK", Firewall: &c}, "", &check); e != nil {
		return check.Snapshot, e
	}
	if check.Code != "" {
		return check.Snapshot, errors.New(check.Code)
	}
	return check.Snapshot, nil
}
func (h *HTTP) createFirewallPlan(w http.ResponseWriter, r *http.Request, s *session, id string) {
	var in struct {
		ExpectedVersion string              `json:"expectedVersion"`
		Operation       string              `json:"operation"`
		Rule            deploy.FirewallRule `json:"rule"`
		Hash            string              `json:"snapshotHash"`
	}
	if decode(w, r, &in) != nil {
		h.fail(w, r, 400, "INVALID_INPUT", "请检查端口、协议、来源和主机版本")
		return
	}
	t, e := h.store.readTarget(id)
	if e != nil {
		h.opsError(w, r, e)
		return
	}
	if t.Version != in.ExpectedVersion || t.Maintenance {
		h.fail(w, r, 409, "HOST_CHANGED_OR_MAINTENANCE", "请刷新主机或退出维护")
		return
	}
	port := t.Port
	if t.Kind == "LOCAL" {
		_, p, _ := net.SplitHostPort(h.origin.Host)
		port, _ = strconv.Atoi(p)
		if (in.Operation == "ENABLE" || in.Operation == "DISABLE") && !net.ParseIP(h.origin.Hostname()).IsLoopback() {
			h.fail(w, r, 409, "LOCAL_REMOTE_FIREWALL_UNSUPPORTED", "远程访问控制器本机时请从服务器控制台调整全局防火墙；该入口仅支持回环控制器")
			return
		}
	}
	if in.Operation == "ADD" {
		in.Rule.ID = uuid.NewString()
	}
	c := deploy.FirewallChange{Operation: in.Operation, Rule: in.Rule, ExpectedHash: in.Hash, Remote: t.Kind == "SSH", ManagementPort: port}
	if e = c.Validate(); e != nil {
		h.fail(w, r, 400, e.Error(), "端口1–65535，范围至多100个；来源填写规范IPv4/IPv6网段")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	snapshot, e := h.firewallPreflight(ctx, t, c)
	if e != nil {
		h.fail(w, r, 409, firewallErrorCode(e), firewallErrorMessage(e))
		return
	}
	p := firewallPlan{uuid.NewString(), id, c, snapshot, time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339)}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, e := h.beginAuthorized(s)
	if e != nil {
		h.internal(w, r, e)
		return
	}
	defer tx.Rollback()
	current, e := scanHost(tx.QueryRow("SELECT "+hostColumns+" FROM targets WHERE id=?", id))
	if e != nil || current.Maintenance || pluginTargetSnapshot(current) != pluginTargetSnapshot(t) {
		h.fail(w, r, 409, "HOST_CHANGED", "主机在预检期间已变化")
		return
	}
	_, e = tx.Exec("INSERT INTO firewall_plans VALUES(?,?,?,?,?,'DRAFT',?,?)", p.ID, s.AdminID, id, pluginTargetSnapshot(t), jsonText(p), p.ExpiresAt, Now())
	if e == nil {
		e = tx.Commit()
	}
	if e != nil {
		h.internal(w, r, e)
		return
	}
	h.success(w, r, p)
}
func firewallErrorCode(e error) string {
	c := e.Error()
	if len(c) < 80 && !strings.ContainsAny(c, " :\n\r\t/") {
		return c
	}
	return "FIREWALL_PREFLIGHT_UNAVAILABLE"
}
func firewallErrorMessage(e error) string {
	switch firewallErrorCode(e) {
	case "SSH_ALLOW_RULE_REQUIRED":
		return "启用前请先为检测到的SSH来源和实际监听端口新增放行规则"
	case "SSH_RULE_CONFLICT":
		return "已有拒绝、限流或复杂规则可能遮挡SSH放行，请由管理员核对规则顺序后重新检测"
	case "MANAGEMENT_PORT_PROTECTED":
		return "不能禁止或撤销SSH/Ops管理端口的放行"
	case "DOCKER_PORT_UFW_UNSUPPORTED":
		return "该端口由Docker发布，UFW规则不能保证阻断；请在软件配置维护映射"
	case "FIREWALL_SNAPSHOT_CHANGED":
		return "规则已变化，请重新检测后预检"
	case "FIREWALL_EXECUTOR_UPGRADE_REQUIRED":
		return "请从主机安装准备升级执行器后重新检查"
	case "FIREWALL_BACKEND_CONFLICT":
		return "主机正在使用 firewalld 或独立 nftables 服务，请先确定唯一防火墙管理方式"
	case "FIREWALL_BACKEND_UNCONFIRMED":
		return "无法确认主机的防火墙管理服务，请检查 systemd 状态后重新检测"
	case "UFW_IPV6_DISABLED":
		return "该主机的 UFW 未启用 IPv6，请使用 IPv4 来源或先由管理员调整 IPv6 配置"
	}
	return "请核对执行器、UFW、主机连接和检测结果，错误码见下方"
}
