package controller

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/deploy"
)

type mavenSettingsPreset struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Content string `json:"content"`
}

const mavenSettingsDefaultContent = `<?xml version="1.0" encoding="UTF-8"?>
<settings xmlns="http://maven.apache.org/SETTINGS/1.0.0"
          xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
          xsi:schemaLocation="http://maven.apache.org/SETTINGS/1.0.0 https://maven.apache.org/xsd/settings-1.0.0.xsd">
  <mirrors>
    <mirror>
      <id>aliyunmaven</id>
      <mirrorOf>central</mirrorOf>
      <name>阿里云公共仓库</name>
      <url>https://maven.aliyun.com/repository/public</url>
    </mirror>
  </mirrors>
</settings>
`

func mavenSettingsPresets() []mavenSettingsPreset {
	return []mavenSettingsPreset{
		{
			Key:     "aliyun",
			Name:    "阿里云公共仓库",
			Summary: "将 central 镜像至 maven.aliyun.com/repository/public",
			Content: mavenSettingsDefaultContent,
		},
		{
			Key:     "huaweicloud",
			Name:    "华为云 Maven 镜像",
			Summary: "将 central 镜像至 repo.huaweicloud.com/repository/maven",
			Content: `<?xml version="1.0" encoding="UTF-8"?>
<settings xmlns="http://maven.apache.org/SETTINGS/1.0.0"
          xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
          xsi:schemaLocation="http://maven.apache.org/SETTINGS/1.0.0 https://maven.apache.org/xsd/settings-1.0.0.xsd">
  <mirrors>
    <mirror>
      <id>huaweicloud</id>
      <mirrorOf>central</mirrorOf>
      <name>华为云开源镜像站</name>
      <url>https://repo.huaweicloud.com/repository/maven/</url>
    </mirror>
  </mirrors>
</settings>
`,
		},
		{
			Key:     "official",
			Name:    "Maven 官方默认",
			Summary: "不配置第三方镜像，直接连接 Maven Central",
			Content: `<?xml version="1.0" encoding="UTF-8"?>
<settings xmlns="http://maven.apache.org/SETTINGS/1.0.0"
          xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
          xsi:schemaLocation="http://maven.apache.org/SETTINGS/1.0.0 https://maven.apache.org/xsd/settings-1.0.0.xsd">
  <mirrors>
  </mirrors>
</settings>
`,
		},
	}
}

func (h *HTTP) mavenConfigRoute(w http.ResponseWriter, r *http.Request, actor *session, path string) bool {
	p := strings.Split(strings.Trim(path, "/"), "/")
	if len(p) != 3 || p[0] != "software-instances" || !deploy.ID.MatchString(p[1]) || p[2] != "maven-settings" {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		h.readMavenSettings(w, r, p[1])
	case http.MethodPut:
		h.saveMavenSettings(w, r, actor, p[1])
	default:
		h.fail(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "不支持该 Maven 配置请求方法")
	}
	return true
}

func (h *HTTP) mavenSettingsTarget(w http.ResponseWriter, r *http.Request, id string) (softwareInstance, target, deploy.MavenConfigRequest, bool) {
	item, err := h.store.readSoftware(id)
	if err != nil {
		h.opsError(w, r, err)
		return item, target{}, deploy.MavenConfigRequest{}, false
	}
	if item.SoftwareType != "MAVEN" || item.ManagementMode != "MANAGED" || item.InstallationStatus != "READY" || item.Installation == nil {
		h.fail(w, r, http.StatusConflict, "MAVEN_CONFIG_UNAVAILABLE", "仅已完成平台托管安装的 Maven 实例支持在线配置 settings.xml")
		return item, target{}, deploy.MavenConfigRequest{}, false
	}
	host, err := h.store.readTarget(item.HostID)
	if err != nil {
		h.opsError(w, r, err)
		return item, host, deploy.MavenConfigRequest{}, false
	}
	mode := "DOCKER"
	if item.Installation.Official != nil {
		mode = "OFFICIAL"
	} else if item.Installation.Config.Mode() == "NATIVE" {
		mode = "NATIVE"
	}
	req := deploy.MavenConfigRequest{
		SoftwareID:  item.ID,
		RuntimeMode: mode,
		InstallRoot: softwareManagementRoot(item, host),
	}
	return item, host, req, true
}

func mavenConfigFailure(err error) (int, string, string) {
	code := executorFailureCode(err)
	if code == "EXECUTOR_UPGRADE_REQUIRED" || code == "EXECUTOR_RESPONSE_INVALID" {
		return http.StatusConflict, "MAVEN_CONFIG_UPGRADE_REQUIRED", "目标服务器接入组件版本较旧，请前往「服务器管理」点击「更新接入组件」后重试"
	}
	if msg, ok := configCapabilityMessages[code]; ok {
		return http.StatusConflict, code, msg
	}
	if msg, ok := executorFailureMessages[code]; ok {
		return http.StatusConflict, code, msg
	}
	return http.StatusConflict, code, "读取或保存 Maven 配置失败，请核对目标主机连接与接入组件"
}

func (h *HTTP) readMavenSettings(w http.ResponseWriter, r *http.Request, id string) {
	_, host, reqCfg, ok := h.mavenSettingsTarget(w, r, id)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var result deploy.MavenConfigResult
	err := h.store.callTarget(ctx, host, deploy.Request{
		Action:      deploy.MavenConfigReadAction,
		MavenConfig: &reqCfg,
		TraceID:     r.Context().Value(traceKey{}).(string),
	}, "", &result)
	if err != nil {
		status, code, msg := mavenConfigFailure(err)
		h.fail(w, r, status, code, msg)
		return
	}
	if result.HasSecret && !h.sensitiveResponseAllowed(r) {
		h.fail(w, r, http.StatusForbidden, "MAVEN_CONFIG_TLS_REQUIRED", "包含服务器密码的 settings.xml 仅允许通过 HTTPS 或本机地址读取")
		return
	}
	h.success(w, r, map[string]any{
		"path":      result.Path,
		"scope":     result.Scope,
		"exists":    result.Exists,
		"content":   result.Content,
		"revision":  result.Revision,
		"hasSecret": result.HasSecret,
		"presets":   mavenSettingsPresets(),
	})
}

func (h *HTTP) recordAudit(actor *session, action, object, outcome, trace string) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	tx, err := h.beginAuthorized(actor)
	if err != nil {
		return
	}
	defer tx.Rollback()
	if auditTx(tx, actor.Username, action, object, outcome, trace) == nil {
		_ = tx.Commit()
	}
}

func (h *HTTP) saveMavenSettings(w http.ResponseWriter, r *http.Request, actor *session, id string) {
	var in struct {
		Content          string `json:"content"`
		ExpectedRevision string `json:"expectedRevision"`
	}
	if err := decode(w, r, &in); err != nil {
		h.fail(w, r, http.StatusBadRequest, "INVALID_INPUT", err.Error())
		return
	}
	item, host, reqCfg, ok := h.mavenSettingsTarget(w, r, id)
	if !ok {
		return
	}
	if host.Maintenance {
		h.fail(w, r, http.StatusConflict, "HOST_MAINTENANCE", "目标服务器处于维护模式，暂不允许修改 Maven 配置")
		return
	}
	normalized := deploy.NormalizeConfigText(in.Content)
	if line, err := deploy.ValidateMavenSettingsXML(normalized); err != nil {
		code := err.Error()
		msg := configCapabilityMessages[code]
		if code == "MAVEN_SETTINGS_XML_INVALID" && line > 0 {
			msg = fmt.Sprintf("XML 语法错误（第 %d 行），请修正后再保存", line)
		}
		h.reply(w, r, http.StatusBadRequest, code, msg, map[string]int{"line": line})
		return
	}
	sha := deploy.ContentSHA256Hex(normalized)
	envelope, err := seal(h.store.Key, "maven-settings/"+item.ID, 1, []byte(normalized))
	if err != nil {
		h.internal(w, r, err)
		return
	}
	reqCfg.ExpectedRevision = strings.TrimSpace(in.ExpectedRevision)
	reqCfg.ContentSHA256 = sha
	reqCfg.ContentEnvelope = envelope

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var result deploy.MavenConfigResult
	err = h.store.callTarget(ctx, host, deploy.Request{
		Action:      deploy.MavenConfigWriteAction,
		MavenConfig: &reqCfg,
		TraceID:     r.Context().Value(traceKey{}).(string),
	}, "", &result)
	if err != nil {
		status, code, msg := mavenConfigFailure(err)
		h.fail(w, r, status, code, msg)
		return
	}
	h.recordAudit(actor, "SOFTWARE_MAVEN_SETTINGS_SAVED", item.ID, "SUCCESS", r.Context().Value(traceKey{}).(string))
	h.success(w, r, result)
}
