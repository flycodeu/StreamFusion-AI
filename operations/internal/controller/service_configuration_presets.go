package controller

import (
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

//go:embed scripts/service-configuration/*
var serviceConfigurationSources embed.FS

func serviceConfigurationSource(name string) string {
	data, err := serviceConfigurationSources.ReadFile("scripts/service-configuration/" + name)
	if err != nil {
		panic(err)
	}
	return serviceinstall.NormalizeScript(string(data))
}

func serviceConfigurationDockerSource() string { return serviceConfigurationSource("docker.py") }

func serviceConfigurationPrelude(app string, official bool, adapter string) string {
	program := "import sys\napp=" + fmt.Sprintf("%q", app) + "\nofficial=" + map[bool]string{false: "False", true: "True"}[official] + "\naction=sys.argv[1]\n"
	program += serviceConfigurationDockerSource() + "\n" + serviceConfigurationSource("common.py") + "\n" + adapter
	program += "\ncontainer_name='sfops-service-'+sid\ntry:\n    execute()\nexcept Exception as error:\n    print(str(error) if isinstance(error, RuntimeError) else '配置操作未完成，请检查服务状态', file=sys.stderr)\n    sys.exit(1)\n"
	return "service_configuration() {\n  command -v python3 >/dev/null || { printf '%s\\n' '配置管理需要服务器已有 Python 3，请先准备运行环境'; return 1; }\n  python3 -I -S - \"$1\" <<'OPS_SERVICE_PY'\n" + program + "\nOPS_SERVICE_PY\n}\n"
}

// Keep the existing account/site operations, sharing their source components
// once instead of repeating setup scripts in every action and verification.
func compactServiceManagement(d serviceinstall.Definition, m *serviceinstall.ManagementDefinition) {
	type fragment struct{ source, function string }
	fragments := []fragment{}
	if d.Key == "mysql" {
		entries, _ := mysqlManagementSources.ReadDir("management_presets")
		for _, entry := range entries {
			name := strings.TrimSuffix(entry.Name(), ".sh")
			if name != "mysql-common" && name != "mysql-literal" && name != "mysql-account-name" && name != "mysql-database-name" && name != "mysql-password" && name != "mysql-verify-password" {
				continue
			}
			source := mysqlManagementScript(name)
			if name == "mysql-common" {
				options, binary := "mysql-options-native", "$OPS_INSTALL_ROOT/runtime/mysql/bin/mysql"
				if serviceinstall.IsOfficial(d) {
					options, binary = "mysql-options-official", "/usr/bin/mysql"
				}
				source = strings.NewReplacer("@NATIVE_OPTIONS@", mysqlManagementScript(options), "@NATIVE_MYSQL@", binary).Replace(source)
			}
			fragments = append(fragments, fragment{source, "ops_" + strings.ReplaceAll(name, "-", "_")})
		}
	} else if d.Key == "nginx" {
		entries, _ := nginxManagementSources.ReadDir("management_presets")
		for _, entry := range entries {
			name := strings.TrimSuffix(entry.Name(), ".sh")
			if name == "nginx-context" || name == "nginx-site" || name == "nginx-transaction" || name == "nginx-publish" || name == "nginx-verify" || name == "nginx-release-port" {
				source := strings.ReplaceAll(nginxManagementScript(name), "@OFFICIAL@", fmt.Sprint(serviceinstall.IsOfficial(d)))
				fragments = append(fragments, fragment{source, "ops_" + strings.ReplaceAll(name, "-", "_")})
			}
		}
	}
	sort.Slice(fragments, func(i, j int) bool { return len(fragments[i].source) > len(fragments[j].source) })
	for _, fragment := range fragments {
		used := false
		for index := range m.Operations {
			op := &m.Operations[index]
			for _, steps := range [][]serviceinstall.CommandStep{op.Steps, op.VerifySteps, op.RollbackSteps} {
				for index := range steps {
					if strings.Contains(steps[index].Script, fragment.source) {
						steps[index].Script = strings.ReplaceAll(steps[index].Script, fragment.source, fragment.function+"\n")
						used = true
					}
				}
			}
		}
		if used {
			m.ScriptPrelude += fragment.function + "() {\n" + fragment.source + "\n}\n"
		}
	}
}

func serviceConfigurationPreset(d serviceinstall.Definition, existing *serviceinstall.ManagementDefinition) *serviceinstall.ManagementDefinition {
	if !serviceinstall.IsBuiltinCommand(d) || d.Key != "nginx" && d.Key != "mysql" && d.Key != "redis" {
		return existing
	}
	m := existing
	if m == nil {
		m = &serviceinstall.ManagementDefinition{}
	}
	compactServiceManagement(d, m)
	// Site listeners and legacy dynamic settings are independent of the
	// application's primary listener and persistent configuration.
	renames := map[string]string{}
	if d.Key == "nginx" {
		renames["BIND_ADDRESS"] = "SITE_BIND_ADDRESS"
	} else if d.Key == "mysql" {
		renames["MAX_CONNECTIONS"] = "DYNAMIC_MAX_CONNECTIONS"
	}
	for old, key := range renames {
		m.ScriptPrelude = strings.ReplaceAll(m.ScriptPrelude, "OPS_PARAM_"+old, "OPS_PARAM_"+key)
		for index := range m.Operations {
			op := &m.Operations[index]
			for _, steps := range [][]serviceinstall.CommandStep{op.Steps, op.VerifySteps, op.RollbackSteps} {
				for index := range steps {
					steps[index].Script = strings.ReplaceAll(steps[index].Script, "OPS_PARAM_"+old, "OPS_PARAM_"+key)
				}
			}
			for index := range op.Parameters {
				if op.Parameters[index].Key == old {
					op.Parameters[index].Key = key
				}
			}
			for index := range op.Resources {
				op.Resources[index].Value = strings.ReplaceAll(op.Resources[index].Value, "OPS_PARAM_"+old, "OPS_PARAM_"+key)
				op.Resources[index].BindAddress = strings.ReplaceAll(op.Resources[index].BindAddress, "OPS_PARAM_"+old, "OPS_PARAM_"+key)
			}
		}
	}
	m.ScriptPrelude += serviceConfigurationPrelude(d.Key, serviceinstall.IsOfficial(d), serviceConfigurationSource(d.Key+".py"))
	text := func(key, label, value string) serviceinstall.ParameterDefinition {
		return serviceinstall.ParameterDefinition{Key: key, Label: label, Type: "TEXT", Default: value, Required: true}
	}
	number := func(key, label string, value, min, max int64) serviceinstall.ParameterDefinition {
		return serviceinstall.ParameterDefinition{Key: key, Label: label, Type: "INTEGER", Default: fmt.Sprint(value), Required: true, Min: &min, Max: &max}
	}
	port := serviceinstall.ParameterDefinition{Key: "CONFIG_PORT", Label: "IPv4 服务端口", Type: "PORT", Default: fmt.Sprint(d.Port), Required: true}
	m.Parameters = append(m.Parameters, text("BIND_ADDRESS", "IPv4 监听地址", "0.0.0.0"), port, number("MEMORY_MIB", "内存上限（MiB，0 为不限）", 0, 0, 16384))
	switch d.Key {
	case "nginx":
		m.Parameters = append(m.Parameters, number("MAX_UPLOAD_MIB", "上传上限（MiB，0 为不限）", 50, 0, 16384), number("MAX_CONNECTIONS", "每进程连接数", 1024, 16, 65536))
	case "mysql":
		m.Parameters = append(m.Parameters, number("MAX_CONNECTIONS", "最大连接数", 151, 10, 10000), number("BUFFER_POOL_MIB", "缓冲池（MiB）", 128, 64, 12288))
	case "redis":
		m.Parameters = append(m.Parameters, number("MAX_MEMORY_MIB", "数据内存上限（MiB）", 512, 0, 16384), number("MAX_CLIENTS", "最大连接数", 10000, 1, 100000))
	}
	step := func(action, name string, timeout int) serviceinstall.CommandStep {
		return serviceinstall.CommandStep{Name: name, RunAs: "ROOT", TimeoutSeconds: timeout, Script: "service_configuration " + action + "\n"}
	}
	configKey := "service-config"
	if d.Key == "mysql" {
		configKey = "mysql-config"
	}
	memoryUnit := "sfops-service-${OPS_SOFTWARE_ID}.service"
	resources := []serviceinstall.ResourceDefinition{{Key: configKey, Type: "PATH_WRITE", Value: "${OPS_CONFIG_ROOT}"}, {Key: "memory-config", Type: "PATH_WRITE", Value: "/etc/systemd/system/" + memoryUnit + ".d/ops-config-${OPS_SOFTWARE_ID}.conf"}}
	if serviceinstall.IsOfficial(d) {
		spec, _ := serviceinstall.OfficialRecipeSpec(d)
		resources[0].Value = spec.Paths.Config
		resources[1].Value = "/etc/systemd/system/" + spec.Paths.Unit + ".d/ops-config-${OPS_SOFTWARE_ID}.conf"
	}
	if d.Key == "mysql" {
		resources = resources[1:] // The existing MySQL shared resource covers its configuration directory.
		data := "${OPS_DATA_ROOT}/mysqld-auto.cnf"
		if serviceinstall.IsOfficial(d) {
			data = "/var/lib/mysql/mysqld-auto.cnf"
		}
		resources = append(resources, serviceinstall.ResourceDefinition{Key: "persisted-settings", Type: "PATH_WRITE", Value: data})
		for index := range m.Operations {
			if m.Operations[index].Key == "update-settings" {
				m.Operations[index].Resources = append(m.Operations[index].Resources, serviceinstall.ResourceDefinition{Key: "persisted-settings", Type: "PATH_WRITE", Value: data})
			}
		}
	}
	file := func(key, value string) {
		resources = append(resources, serviceinstall.ResourceDefinition{Key: key, Type: "PATH_WRITE", Value: value})
	}
	switch d.Key {
	case "nginx":
		if serviceinstall.IsOfficial(d) {
			m.Resources = append(m.Resources, serviceinstall.ResourceDefinition{Key: "global-config", Type: "PATH_WRITE", Value: "/etc/nginx/nginx.conf"}, serviceinstall.ResourceDefinition{Key: "default-site", Type: "PATH_WRITE", Value: "/etc/nginx/sites-available/default"})
		} else {
			m.Resources = append(m.Resources, serviceinstall.ResourceDefinition{Key: "global-config", Type: "PATH_WRITE", Value: "${OPS_CONFIG_ROOT}/nginx.conf"}, serviceinstall.ResourceDefinition{Key: "site-base", Type: "PATH_WRITE", Value: "${OPS_CONFIG_ROOT}/ops-sites/base.conf.saved"})
		}
	case "redis":
		if serviceinstall.IsOfficial(d) {
			file("redis-config", "/etc/redis/redis.conf")
		} else {
			file("redis-config", "${OPS_CONFIG_ROOT}/redis.conf")
		}
	case "mysql":
		if serviceinstall.IsOfficial(d) {
			file("server-config", "/etc/mysql/mysql.conf.d/mysqld.cnf")
		} else {
			file("server-config", "${OPS_CONFIG_ROOT}/my.cnf")
			file("container-config", "${OPS_CONFIG_ROOT}/service-settings.cnf")
		}
	}
	configure := serviceinstall.ManagementOperation{Key: "configure", Name: "保存配置", Group: "配置", Rebind: true, ResultFormat: "TEXT", Steps: []serviceinstall.CommandStep{step("configure", "保存并应用配置", 240)}, VerifySteps: []serviceinstall.CommandStep{step("verify", "验证配置与服务", 120)}, RollbackSteps: []serviceinstall.CommandStep{step("rollback", "恢复原配置", 240)}, Resources: append(resources, serviceinstall.ResourceDefinition{Key: "main", Type: "PORT", Value: "${OPS_PARAM_CONFIG_PORT}", BindAddress: "${OPS_PARAM_BIND_ADDRESS}", Protocol: "TCP"})}
	read := serviceinstall.ManagementOperation{Key: "read-config", Name: "读取配置", Group: "配置", ReadOnly: true, ResultFormat: "TABLE", Steps: []serviceinstall.CommandStep{step("read-config", "读取实际配置", 45)}}
	m.Operations = append([]serviceinstall.ManagementOperation{read, configure}, m.Operations...)
	return m
}
