package controller

import (
	"embed"
	"strings"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

//go:embed management_presets/mysql*.sh
var mysqlManagementSources embed.FS

func mysqlManagementScript(name string) string {
	data, err := mysqlManagementSources.ReadFile("management_presets/" + name + ".sh")
	if err != nil {
		panic(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func mysqlManagementCommand(name string, official bool) string {
	script := mysqlManagementScript("mysql-common")
	if strings.Contains(name, "password") || strings.Contains(name, "grant") || name == "mysql-create-database" || name == "mysql-verify-database" {
		script += "\n" + mysqlManagementScript("mysql-literal")
	}
	if strings.Contains(name, "password") || strings.Contains(name, "grant") {
		script += "\n" + mysqlManagementScript("mysql-account-name")
	}
	if strings.Contains(name, "grant") || name == "mysql-create-database" || name == "mysql-verify-database" {
		script += "\n" + mysqlManagementScript("mysql-database-name")
	}
	options, binary := "mysql-options-native", "$OPS_INSTALL_ROOT/runtime/mysql/bin/mysql"
	if official {
		options, binary = "mysql-options-official", "/usr/bin/mysql"
	}
	return strings.NewReplacer("@NATIVE_OPTIONS@", mysqlManagementScript(options), "@NATIVE_MYSQL@", binary).Replace(script + "\n" + mysqlManagementScript(name))
}

func mysqlManagementDefinition(d serviceinstall.Definition) *serviceinstall.ManagementDefinition {
	if d.Key != "mysql" || !serviceinstall.IsBuiltinCommand(d) {
		return nil
	}
	step := func(name, script string) serviceinstall.CommandStep {
		return serviceinstall.CommandStep{Name: name, Script: mysqlManagementCommand(script, serviceinstall.IsOfficial(d)), RunAs: "ROOT", TimeoutSeconds: 60}
	}
	op := func(key, name, group, script, verify string, parameters ...serviceinstall.ParameterDefinition) serviceinstall.ManagementOperation {
		operation := serviceinstall.ManagementOperation{Key: key, Name: name, Group: group, Parameters: parameters, ReadOnly: verify == "", ResultFormat: "TEXT", Steps: []serviceinstall.CommandStep{step(name, script)}}
		if verify == "" {
			operation.ResultFormat = "TABLE"
		} else {
			operation.VerifySteps = []serviceinstall.CommandStep{step("核验"+name, verify)}
		}
		return operation
	}
	text := func(key, label, value string) serviceinstall.ParameterDefinition {
		return serviceinstall.ParameterDefinition{Key: key, Label: label, Type: "TEXT", Default: value, Required: true}
	}
	choice := func(key, label, value string, options ...string) serviceinstall.ParameterDefinition {
		return serviceinstall.ParameterDefinition{Key: key, Label: label, Type: "SELECT", Default: value, Options: options, Required: true}
	}
	number := func(key, label, value string, min, max int64) serviceinstall.ParameterDefinition {
		return serviceinstall.ParameterDefinition{Key: key, Label: label, Type: "INTEGER", Default: value, Min: &min, Max: &max, Required: true}
	}
	account := []serviceinstall.ParameterDefinition{text("USERNAME", "业务账号", ""), text("HOST", "允许来源", "localhost")}
	password := append(append([]serviceinstall.ParameterDefinition{}, account...), serviceinstall.ParameterDefinition{Key: "PASSWORD", Label: "账号密码", Type: "CREDENTIAL", Required: true})
	grant := append(append([]serviceinstall.ParameterDefinition{}, account...), text("DATABASE", "数据库", ""), choice("PERMISSION", "权限", "只读", "只读", "读写"))
	resources := []serviceinstall.ResourceDefinition{{Key: "database-data", Type: "PATH_WRITE", Value: "${OPS_DATA_ROOT}"}, {Key: "mysql-config", Type: "PATH_WRITE", Value: "${OPS_CONFIG_ROOT}"}}
	if serviceinstall.IsOfficial(d) {
		resources = []serviceinstall.ResourceDefinition{{Key: "database-data", Type: "PATH_WRITE", Value: "/var/lib/mysql"}, {Key: "mysql-config", Type: "PATH_WRITE", Value: "/etc/mysql"}}
	}
	return &serviceinstall.ManagementDefinition{
		Parameters: []serviceinstall.ParameterDefinition{text("ADMIN_USER", "管理账号", "root"), {Key: "ADMIN_PASSWORD", Label: "管理密码", Type: "CREDENTIAL"}},
		Resources:  resources,
		Operations: []serviceinstall.ManagementOperation{
			op("databases", "查看数据库", "数据库", "mysql-databases", ""),
			op("create-database", "创建数据库", "数据库", "mysql-create-database", "mysql-verify-database", text("DATABASE", "数据库名称", ""), choice("CHARSET", "字符集", "utf8mb4", "utf8mb4"), choice("COLLATION", "排序规则", "utf8mb4_0900_ai_ci", "utf8mb4_0900_ai_ci", "utf8mb4_general_ci", "utf8mb4_bin")),
			op("users", "查看账号", "账号", "mysql-users", ""),
			op("create-user", "创建业务账号", "账号", "mysql-password", "mysql-verify-password", password...),
			op("change-password", "修改业务账号密码", "账号", "mysql-password", "mysql-verify-password", password...),
			op("grant-database", "授予数据库权限", "账号", "mysql-grant", "mysql-verify-grant", grant...),
			op("settings", "查看服务配置", "服务配置", "mysql-settings", ""),
			op("update-settings", "保存动态配置", "服务配置", "mysql-update-settings", "mysql-verify-settings", number("MAX_CONNECTIONS", "最大连接数", "151", 10, 10000), serviceinstall.ParameterDefinition{Key: "SLOW_QUERY_LOG", Label: "慢查询日志", Type: "BOOLEAN", Default: "false", Required: true}, number("LONG_QUERY_TIME", "慢查询阈值（秒）", "10", 0, 3600), choice("STORAGE_ENGINE", "默认存储引擎", "InnoDB", "InnoDB", "MyISAM")),
		},
	}
}
