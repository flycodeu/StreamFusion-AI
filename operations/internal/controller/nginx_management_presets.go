package controller

import (
	"embed"
	"strconv"
	"strings"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

//go:embed management_presets/nginx*.sh
var nginxManagementSources embed.FS

func nginxManagementScript(name string) string {
	data, err := nginxManagementSources.ReadFile("management_presets/" + name + ".sh")
	if err != nil {
		panic(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func nginxManagementCommand(official bool, names ...string) string {
	result := ""
	for _, name := range names {
		result += nginxManagementScript("nginx-"+name) + "\n"
	}
	return strings.ReplaceAll(result, "@OFFICIAL@", strconv.FormatBool(official))
}

func nginxManagementDefinition(d serviceinstall.Definition) *serviceinstall.ManagementDefinition {
	if d.Key != "nginx" || !serviceinstall.IsBuiltinCommand(d) {
		return nil
	}
	step := func(name string, scripts ...string) serviceinstall.CommandStep {
		return serviceinstall.CommandStep{Name: name, Script: nginxManagementCommand(serviceinstall.IsOfficial(d), scripts...), RunAs: "ROOT", TimeoutSeconds: 90}
	}
	text := func(key, label, value string) serviceinstall.ParameterDefinition {
		return serviceinstall.ParameterDefinition{Key: key, Label: label, Type: "TEXT", Default: value, Required: true}
	}
	boolean := func(key, label string, value bool) serviceinstall.ParameterDefinition {
		return serviceinstall.ParameterDefinition{Key: key, Label: label, Type: "BOOLEAN", Default: strconv.FormatBool(value), Required: true}
	}
	site := text("SITE", "站点名称", "default")
	port := serviceinstall.ParameterDefinition{Key: "PORT", Label: "服务器端口", Type: "PORT", Default: "80", Required: true}
	bind := text("BIND_ADDRESS", "监听地址", "127.0.0.1")
	siteResources := func(release bool) []serviceinstall.ResourceDefinition {
		resources := []serviceinstall.ResourceDefinition{{Key: "site", Type: "PATH_WRITE", Value: "${OPS_CONFIG_ROOT}/ops-sites/${OPS_PARAM_SITE}.conf", Release: release}, {Key: "disabled-site", Type: "PATH_WRITE", Value: "${OPS_CONFIG_ROOT}/ops-sites/${OPS_PARAM_SITE}.conf.disabled", Release: release}}
		if serviceinstall.IsOfficial(d) {
			resources = append(resources, serviceinstall.ResourceDefinition{Key: "system-site", Type: "PATH_WRITE", Value: "/etc/nginx/conf.d/ops-${OPS_SOFTWARE_ID}-${OPS_PARAM_SITE}.conf", Release: release})
		}
		return resources
	}
	mutation := func(key, name, script string, parameters []serviceinstall.ParameterDefinition) serviceinstall.ManagementOperation {
		return serviceinstall.ManagementOperation{Key: key, Name: name, Group: "站点", Parameters: parameters, Resources: siteResources(false), ResultFormat: "TEXT", Steps: []serviceinstall.CommandStep{step(name, "context", "site", "transaction", script)}, VerifySteps: []serviceinstall.CommandStep{step("核验站点配置", "context", "site", "verify")}}
	}
	m := &serviceinstall.ManagementDefinition{Resources: []serviceinstall.ResourceDefinition{{Key: "site-directory", Type: "PATH_WRITE", Value: "${OPS_CONFIG_ROOT}/ops-sites"}}, Operations: []serviceinstall.ManagementOperation{{Key: "sites", Name: "查看站点", Group: "站点", ReadOnly: true, ResultFormat: "TABLE", Steps: []serviceinstall.CommandStep{step("读取站点列表", "context", "list")}}}}
	for _, kind := range []string{"static", "proxy"} {
		parameters := []serviceinstall.ParameterDefinition{site, text("SERVER_NAME", "域名", "_"), port, bind}
		label := "静态站点"
		if kind == "static" {
			parameters = append(parameters, serviceinstall.ParameterDefinition{Key: "STATIC_ROOT", Label: "服务器静态文件目录", Type: "PATH", Default: "/var/www/html", Required: true}, text("INDEX", "首页文件", "index.html"), boolean("SPA", "前端路由回退", false))
		} else {
			label = "反向代理"
			min, max := int64(1), int64(3600)
			parameters = append(parameters, text("UPSTREAM", "后端地址", "http://127.0.0.1:8080"), text("LOCATION", "访问路径", "/"), boolean("STRIP_PREFIX", "移除访问路径前缀", false), boolean("WEBSOCKET", "WebSocket", false), boolean("SSE", "SSE", false), serviceinstall.ParameterDefinition{Key: "READ_TIMEOUT", Label: "读取超时（秒）", Type: "INTEGER", Default: "60", Required: true, Min: &min, Max: &max})
		}
		op := mutation("publish-"+kind, "发布"+label, "publish", parameters)
		op.Resources = append(op.Resources, serviceinstall.ResourceDefinition{Key: "listener", Type: "PORT", Value: "${OPS_PARAM_PORT}", Protocol: "TCP", BindAddress: "${OPS_PARAM_BIND_ADDRESS}"})
		if kind == "static" {
			op.Resources = append(op.Resources, serviceinstall.ResourceDefinition{Key: "static-files", Type: "PATH_READ", Value: "${OPS_PARAM_STATIC_ROOT}"})
		}
		m.Operations = append(m.Operations, op)
	}
	m.Operations = append(m.Operations, mutation("set-site-state", "启用或停用站点", "toggle", []serviceinstall.ParameterDefinition{site, boolean("ENABLED", "启用站点", true)}))
	remove := mutation("delete-site", "删除站点", "delete", []serviceinstall.ParameterDefinition{site})
	remove.Resources = siteResources(true)
	m.Operations = append(m.Operations, remove)
	if strings.Contains(strings.Join(d.RuntimeModes, ","), "NATIVE") {
		release := serviceinstall.ManagementOperation{Key: "release-port", Name: "释放空闲监听端口", Group: "站点", RuntimeModes: []string{"NATIVE"}, Parameters: []serviceinstall.ParameterDefinition{port, bind}, Resources: []serviceinstall.ResourceDefinition{{Key: "listener", Type: "PORT", Value: "${OPS_PARAM_PORT}", Protocol: "TCP", BindAddress: "${OPS_PARAM_BIND_ADDRESS}", Release: true}}, ResultFormat: "TEXT", Steps: []serviceinstall.CommandStep{step("释放端口预留", "release-port")}, VerifySteps: []serviceinstall.CommandStep{step("核验端口预留", "release-port")}}
		m.Operations = append(m.Operations, release)
	}
	return m
}
