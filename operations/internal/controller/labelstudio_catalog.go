package controller

import (
	"embed"
	"fmt"
	"strings"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

//go:embed scripts/annotation/labelstudio*.sh
var labelStudioScripts embed.FS

const labelStudioVersion = "1.23.2"
const labelStudioWheelSHA = "c575b2552d0b271554224c5e56ab2ce9449c6e62a0b4851bc8c706d8fe651137"
const labelStudioWheelURL = "https://files.pythonhosted.org/packages/a6/05/0056b02f03b6a7616e460096bb3a08a0e1170bedcee7cdacb63ae5191cf8/label_studio-1.23.2-py3-none-any.whl"

// The image digest and ID are pinned to the official Linux amd64 manifest.
// Metadata evidence is kept with the OPS-ANNOTATION-15 delivery record.
const labelStudioImageDigest = "sha256:844f82c5b08de3f77a57c66f480e0036d3c76c6eff53699e8d47686613faa922"
const labelStudioImageID = "sha256:bd2ee580197715ce85dfd175172e440de67ec9bc0d313b82712c017575b96870"

func labelStudioScript(names ...string) string {
	var result strings.Builder
	for _, name := range append([]string{"common"}, names...) {
		data, err := labelStudioScripts.ReadFile("scripts/annotation/labelstudio-" + name + ".sh")
		if err != nil {
			panic(err)
		}
		result.WriteString(strings.ReplaceAll(string(data), "\r\n", "\n"))
		result.WriteByte('\n')
	}
	return strings.NewReplacer("@@VERSION@@", labelStudioVersion, "@@WHEEL_SHA@@", labelStudioWheelSHA, "@@WHEEL_URL@@", labelStudioWheelURL, "@@IMAGE_DIGEST@@", labelStudioImageDigest, "@@IMAGE_ID@@", labelStudioImageID).Replace(result.String())
}

func labelStudioDefinitions() []serviceinstall.Definition {
	step := func(phase, name string, timeout int, scripts ...string) serviceinstall.CommandStep {
		return serviceinstall.CommandStep{Kind: "COMMAND", Phase: phase, Name: name, Script: labelStudioScript(scripts...), RunAs: "ROOT", TimeoutSeconds: timeout}
	}
	var result []serviceinstall.Definition
	for _, mode := range []string{"DOCKER", "NATIVE"} {
		for _, offline := range []bool{false, true} {
			source, suffix := "ONLINE", "online"
			if mode == "DOCKER" {
				source = "REGISTRY"
			}
			if offline {
				source, suffix = "ARCHIVE", "offline"
			}
			minUpload, maxUpload := int64(1), int64(2048)
			definition := serviceinstall.Definition{
				Key:        "custom-label-studio-" + strings.ToLower(mode) + "-" + suffix,
				CatalogKey: "label-studio", Name: "Label Studio", Version: labelStudioVersion, Revision: "1", Kind: "SERVICE", Category: "ANNOTATION", Platform: "linux/amd64",
				OfficialURL: "https://labelstud.io/guide/install", RuntimeModes: []string{mode}, SourceModes: []string{source}, DefaultMemoryMiB: 8192, DefaultBindAddress: "0.0.0.0", Port: 8080,
				Web: &serviceinstall.WebAccessDefinition{Scheme: "http", Path: "/", PortKey: "main", URLParameter: "PUBLIC_URL"},
				Parameters: []serviceinstall.ParameterDefinition{
					{Key: "MAX_UPLOAD_MIB", Label: "单次上传上限（MiB）", Type: "INTEGER", Default: "250", Required: true, Min: &minUpload, Max: &maxUpload},
					{Key: "SOURCE_DIRECTORY", Label: "只读数据集目录", Type: "PATH"},
					{Key: "PUBLIC_URL", Label: "对外访问地址", Type: "TEXT"},
				},
				Commands: &serviceinstall.CommandRecipe{FlowVersion: 1, Revision: 1, Kind: "SYSTEMD", Locator: "ops-labelstudio-${OPS_SOFTWARE_ID}.service", BinaryPath: "${OPS_INSTALL_ROOT}/bin/run"},
			}
			definition.Commands.Steps = []serviceinstall.CommandStep{step("INSTALL", "核对安装目录与参数", 30, "check")}
			if mode == "DOCKER" {
				definition.Commands.Kind, definition.Commands.Locator, definition.Commands.BinaryPath = "DOCKER", "ops-labelstudio-${OPS_SOFTWARE_ID}", ""
				definition.Image, definition.Digest, definition.ImageID = "docker.io/heartexlabs/label-studio:"+labelStudioVersion, labelStudioImageDigest, labelStudioImageID
				definition.Commands.Steps = append(definition.Commands.Steps, step("INSTALL", "获取并核验官方镜像", 1800, "docker-material"), step("INSTALL", "配置挂载与上传限制并启动", 120, "docker-create"))
			} else {
				definition.Parameters = append(definition.Parameters, serviceinstall.ParameterDefinition{Key: "PYTHON_BINARY", Label: "Python 3.12 可执行文件", Type: "PATH", Default: "/usr/bin/python3.12", Required: true})
				definition.Commands.Steps = append(definition.Commands.Steps, step("INSTALL", "安装到独立 Python 环境", 1800, "native-material"), step("INSTALL", "配置目录与上传限制并启动", 120, "native-create"))
			}
			definition.Commands.Steps = append(definition.Commands.Steps, step("VERIFY", "核对版本、配置与访问状态", 600, "verify"), step("UNINSTALL", "卸载程序并保留标注数据", 180, "mount-check", "uninstall"), step("DELETE_DATA", "删除本次标注数据和配置", 120, "mount-check", "delete-data"))
			for index := range definition.Commands.Steps {
				definition.Commands.Steps[index].ID = fmt.Sprintf("labelstudio-%d", index+1)
			}
			definition.Management = annotationManagementDefinition("label-studio", mode)
			result = append(result, definition)
		}
	}
	return result
}
