package controller

import (
	"embed"
	"fmt"
	"strings"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

//go:embed scripts/annotation/annotation-*
var annotationManagementSources embed.FS

var annotationStepNames = map[string]string{
	"read-config": "读取实际配置", "configure": "保存配置", "verify": "验证配置",
	"rollback": "恢复原配置", "check-data-migration": "检查数据迁移", "migrate-data": "迁移数据目录",
}

func annotationManagementSource(name string) string {
	data, err := annotationManagementSources.ReadFile("scripts/annotation/annotation-" + name)
	if err != nil {
		panic(err)
	}
	return serviceinstall.NormalizeScript(string(data))
}

func annotationManagementProgram(app, mode string) string {
	application := "labelstudio-" + strings.ToLower(mode) + ".py"
	if app == "ytlabel" {
		application = "ytlabel.py"
	}
	program := annotationManagementSource(application)
	if app == "ytlabel" {
		program = annotationManagementSource("import-source.py") + "\n" + program
	} else {
		program += "\n" + annotationManagementSource("labelstudio-url.py")
	}
	return annotationManagementSource("management-common.py") + "\n" + program
}

func annotationManagementDefinition(app, mode string) *serviceinstall.ManagementDefinition {
	port, upload, memory, maxUpload := 8080, 250, 8192, int64(2048)
	if app == "ytlabel" {
		port, upload, memory, maxUpload = 9924, 500, 2048, 16384
	}
	minimum, minimumMemory, maximumMemory := int64(1), int64(128), int64(16384)
	prelude := strings.NewReplacer("@@APP@@", app, "@@MODE@@", mode, "@@COMMON@@", annotationManagementProgram(app, mode), "@@APPLICATION@@", "").Replace(annotationManagementSource("management.sh"))
	step := func(action string, timeout int) serviceinstall.CommandStep {
		return serviceinstall.CommandStep{Name: annotationStepNames[action], RunAs: "ROOT", TimeoutSeconds: timeout, Script: "annotation_manage " + action + " <<'PY'\n" + annotationManagementSource(action+".py") + "\nPY\n"}
	}
	configResources := []serviceinstall.ResourceDefinition{{Key: "management-state", Type: "PATH_WRITE", Value: "${OPS_CONFIG_ROOT}/annotation-management.json"}}
	if app == "ytlabel" {
		configResources = append(configResources, serviceinstall.ResourceDefinition{Key: "config", Type: "PATH_READ", Value: "${OPS_CONFIG_ROOT}/ytlabel.json"}, serviceinstall.ResourceDefinition{Key: "network-config", Type: "PATH_WRITE", Value: "${OPS_CONFIG_ROOT}/ytlabel.json"}, serviceinstall.ResourceDefinition{Key: "launcher", Type: "PATH_WRITE", Value: "${OPS_INSTALL_ROOT}/bin/ytlabel-launch.py"})
	} else if mode == "NATIVE" {
		configResources = append(configResources, serviceinstall.ResourceDefinition{Key: "runtime-config", Type: "PATH_WRITE", Value: "${OPS_CONFIG_ROOT}/runtime.env"}, serviceinstall.ResourceDefinition{Key: "launcher", Type: "PATH_WRITE", Value: "${OPS_INSTALL_ROOT}/bin/run"})
	}
	if mode == "NATIVE" {
		configResources = append(configResources, serviceinstall.ResourceDefinition{Key: "service", Type: "UNIT", Value: "${OPS_SOFTWARE_LOCATOR}"})
	}
	configure := serviceinstall.ManagementOperation{Key: "configure", Name: "保存配置", Group: "配置", Rebind: true, ResultFormat: "TEXT", RuntimeModes: []string{mode}, Steps: []serviceinstall.CommandStep{step("configure", 1800)}, VerifySteps: []serviceinstall.CommandStep{step("verify", 240)}, RollbackSteps: []serviceinstall.CommandStep{step("rollback", 360)}, Resources: append(configResources, serviceinstall.ResourceDefinition{Key: "main", Type: "PORT", Value: "${OPS_PARAM_WEB_PORT}", BindAddress: "${OPS_PARAM_BIND_ADDRESS}", Protocol: "TCP"})}
	migrate := serviceinstall.ManagementOperation{Key: "migrate-data", Name: "迁移数据目录", Group: "数据", Rebind: true, ResultFormat: "TEXT", RuntimeModes: []string{mode}, Parameters: []serviceinstall.ParameterDefinition{{Key: "DATA_DIRECTORY", Label: "新数据目录", Type: "PATH", Required: true}}, Steps: []serviceinstall.CommandStep{step("migrate-data", 1800)}, VerifySteps: []serviceinstall.CommandStep{step("verify", 240)}, RollbackSteps: []serviceinstall.CommandStep{step("rollback", 360)}, Resources: append(append([]serviceinstall.ResourceDefinition{}, configResources...), serviceinstall.ResourceDefinition{Key: "active-data", Type: "PATH_WRITE", Value: "${OPS_PARAM_DATA_DIRECTORY}"})}
	parameters := []serviceinstall.ParameterDefinition{
		{Key: "BIND_ADDRESS", Label: "监听地址", Type: "TEXT", Default: "0.0.0.0", Required: true},
		{Key: "WEB_PORT", Label: "访问端口", Type: "PORT", Default: fmt.Sprint(port), Required: true},
		{Key: "MAX_UPLOAD_MIB", Label: "单次上传上限（MiB）", Type: "INTEGER", Default: fmt.Sprint(upload), Required: true, Min: &minimum, Max: &maxUpload},
		{Key: "SOURCE_DIRECTORY", Label: "素材目录", Type: "PATH"},
		{Key: "MEMORY_MIB", Label: "内存上限（MiB）", Type: "INTEGER", Default: fmt.Sprint(memory), Required: true, Min: &minimumMemory, Max: &maximumMemory},
	}
	if app != "ytlabel" {
		parameters = append(parameters, serviceinstall.ParameterDefinition{Key: "PUBLIC_URL", Label: "对外访问地址", Type: "TEXT"})
		parameters[3].Label = "只读素材目录"
	} else {
		parameters[3].Label = "素材导入目录"
	}
	return &serviceinstall.ManagementDefinition{ScriptPrelude: prelude, Parameters: parameters, Operations: []serviceinstall.ManagementOperation{
		{Key: "read-config", Name: "读取配置", Group: "配置", ReadOnly: true, ResultFormat: "TABLE", RuntimeModes: []string{mode}, Steps: []serviceinstall.CommandStep{step("read-config", 30)}}, configure,
		{Key: "check-data-migration", Name: "检查数据迁移", Group: "数据", ReadOnly: true, ResultFormat: "TABLE", RuntimeModes: []string{mode}, Parameters: migrate.Parameters, Steps: []serviceinstall.CommandStep{step("check-data-migration", 30)}}, migrate,
	}}
}
