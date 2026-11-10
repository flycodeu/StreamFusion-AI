package controller

import (
	"embed"
	"fmt"
	"strings"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

//go:embed scripts/additional-software/manage-config-*.py
var additionalConfigurationScripts embed.FS

func additionalConfigurationSource(name string) string {
	value, err := additionalConfigurationScripts.ReadFile("scripts/additional-software/manage-config-" + name + ".py")
	if err != nil {
		panic(err)
	}
	return serviceinstall.NormalizeScript(string(value))
}

func additionalConfiguration(app additionalSoftware, mode string) *serviceinstall.ManagementDefinition {
	port := map[string]int{"jenkins": 8080, "postgresql": 5432, "gitlab": 80, "prometheus": 9090, "nacos": 8848}[app.key]
	program := additionalConfigurationSource("common")
	if mode == "DOCKER" {
		program += "\n" + serviceConfigurationDockerSource() + "\n" + additionalConfigurationSource("docker")
	} else {
		program += "\n" + additionalConfigurationSource("native")
	}
	program += "\n" + additionalConfigurationSource(app.key) + "\ntry:\n    main()\nexcept Exception as error:\n    print(str(error) if isinstance(error, RuntimeError) else '配置操作未完成，请检查服务配置与运行状态', file=sys.stderr)\n    sys.exit(1)\n"
	program = strings.NewReplacer("@@APP@@", app.key, "@@MODE@@", mode, "@@PORT@@", fmt.Sprint(port)).Replace(program)
	context := strings.NewReplacer("@@APP@@", app.key, "@@VERSION@@", app.version).Replace(additionalScript("common"))
	prelude := "additional_context() {\n" + context + "\n}\nadditional_manage() {\n  umask 077\n  command -v python3 >/dev/null || { printf '%s\\n' '配置管理需要已有 python3，请先安装 Python 3 后重试' >&2; return 1; }\n  python3 -I -S - \"$OPS_INSTALL_ROOT\" \"$1\" <<'OPS_ADDITIONAL_CONFIG_PY'\n" + program + "\nOPS_ADDITIONAL_CONFIG_PY\n}\n"
	minimum, maximum := int64(0), int64(1048576)
	parameters := []serviceinstall.ParameterDefinition{
		{Key: "BIND_ADDRESS", Label: "监听地址", Type: "TEXT", Default: "0.0.0.0", Required: true},
		{Key: "WEB_PORT", Label: "服务端口", Type: "PORT", Default: fmt.Sprint(port), Required: true},
		{Key: "MEMORY_MIB", Label: "内存上限（MiB，0 为不限）", Type: "INTEGER", Default: fmt.Sprint(app.memory), Min: &minimum, Max: &maximum, Required: true},
	}
	resources := []serviceinstall.ResourceDefinition{{Key: "main", Type: "PORT", Protocol: "TCP", Value: "${OPS_PARAM_WEB_PORT}", BindAddress: "${OPS_PARAM_BIND_ADDRESS}"}}
	if app.key == "prometheus" {
		parameters = append(parameters, serviceinstall.ParameterDefinition{Key: "RETENTION", Label: "数据保留时间", Type: "TEXT", Default: "15d", Required: true})
	}
	if app.key == "gitlab" {
		ssh := "2222"
		config := "${OPS_DATA_ROOT}/gitlab-config/gitlab.rb"
		if mode == "NATIVE" {
			ssh, config = "22", "/etc/gitlab/gitlab.rb"
			resources = append(resources, serviceinstall.ResourceDefinition{Key: "memory", Type: "PATH_WRITE", Value: "${OPS_CONFIG_ROOT}/gitlab-memory.conf"}, serviceinstall.ResourceDefinition{Key: "memory-unit", Type: "PATH_WRITE", Value: "/etc/systemd/system/gitlab-runsvdir.service.d/streamfusion-ops-${OPS_SOFTWARE_ID}.conf"})
		} else {
			resources = append(resources, serviceinstall.ResourceDefinition{Key: "SSH", Type: "PORT", Protocol: "TCP", Value: "${OPS_PARAM_SSH_PORT}", BindAddress: "${OPS_PARAM_BIND_ADDRESS}"})
		}
		parameters = append(parameters, serviceinstall.ParameterDefinition{Key: "EXTERNAL_URL", Label: "访问地址", Type: "TEXT", Default: "http://gitlab.example.com", Required: true}, serviceinstall.ParameterDefinition{Key: "SSH_PORT", Label: "Git SSH 公示端口", Type: "PORT", Default: ssh, Required: true})
		resources = append(resources, serviceinstall.ResourceDefinition{Key: "gitlab-config", Type: "PATH_WRITE", Value: config})
	}
	if app.key == "nacos" {
		for _, item := range []struct {
			key, label, resource string
			port                 int
		}{{"CONSOLE_PORT", "控制台端口", "CONSOLE", 8080}, {"GRPC_PORT", "客户端 gRPC 端口", "GRPC", 9848}, {"CLUSTER_GRPC_PORT", "服务间 gRPC 端口", "CLUSTER_GRPC", 9849}} {
			parameters = append(parameters, serviceinstall.ParameterDefinition{Key: item.key, Label: item.label, Type: "PORT", Default: fmt.Sprint(item.port), Required: true})
			resources = append(resources, serviceinstall.ResourceDefinition{Key: item.resource, Type: "PORT", Protocol: "TCP", Value: "${OPS_PARAM_" + item.key + "}", BindAddress: "${OPS_PARAM_BIND_ADDRESS}"})
		}
	}
	resource := "CONTAINER"
	if mode == "NATIVE" {
		resource = "UNIT"
		if app.key != "gitlab" {
			resources = append(resources, serviceinstall.ResourceDefinition{Key: "unit-config", Type: "PATH_WRITE", Value: "/etc/systemd/system/${OPS_SOFTWARE_LOCATOR}"})
		}
		if app.key == "postgresql" {
			resources = append(resources, serviceinstall.ResourceDefinition{Key: "postgres-config", Type: "PATH_WRITE", Value: "${OPS_CONFIG_ROOT}/postgresql.conf"})
		} else if app.key != "gitlab" {
			resources = append(resources, serviceinstall.ResourceDefinition{Key: "launcher", Type: "PATH_WRITE", Value: "${OPS_INSTALL_ROOT}/bin/run"})
			if app.key == "nacos" {
				resources = append(resources, serviceinstall.ResourceDefinition{Key: "nacos-config", Type: "PATH_WRITE", Value: "${OPS_CONFIG_ROOT}/application.properties"})
			}
		}
	}
	step := func(key, name string, timeout int) serviceinstall.CommandStep {
		return serviceinstall.CommandStep{Name: name, RunAs: "ROOT", Script: "additional_manage " + key, TimeoutSeconds: timeout}
	}
	return &serviceinstall.ManagementDefinition{ScriptPrelude: prelude, Parameters: parameters,
		Resources: []serviceinstall.ResourceDefinition{{Key: "installation", Type: "PATH_READ", Value: "${OPS_INSTALL_ROOT}"}, {Key: "service", Type: resource, Value: "${OPS_SOFTWARE_LOCATOR}"}},
		Operations: []serviceinstall.ManagementOperation{
			{Key: "diagnostics", Name: "检查服务与配置", Group: "诊断", ReadOnly: true, ResultFormat: "TEXT", RuntimeModes: []string{mode}, Steps: []serviceinstall.CommandStep{{Name: "查看服务健康与配置文件", Script: additionalContextScript(app, "manage-diagnostics"), RunAs: "ROOT", TimeoutSeconds: 30}}},
			{Key: "read-config", Name: "读取配置", Group: "配置", ReadOnly: true, ResultFormat: "TABLE", RuntimeModes: []string{mode}, Steps: []serviceinstall.CommandStep{step("read-config", "读取实际配置", 30)}},
			{Key: "configure", Name: "保存配置", Group: "配置", Rebind: true, ResultFormat: "TEXT", RuntimeModes: []string{mode}, Resources: resources, Steps: []serviceinstall.CommandStep{step("configure", "保存配置并恢复运行状态", 1800)}, VerifySteps: []serviceinstall.CommandStep{step("verify", "验证配置与服务状态", 240)}, RollbackSteps: []serviceinstall.CommandStep{step("rollback", "恢复原配置与运行状态", 1800)}},
		},
	}
}
