package controller

import (
	"embed"
	"fmt"
	"strings"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

const ytlabelCommit = "08930fd95fe0ea0b407346ebb2d10929e5b45245"
const ytlabelSourceSHA256 = "5c318684852128c812d13581a561aedbed36998b43d74c10f6798f37aee7205e"
const ytlabelSourceURL = "https://codeload.github.com/yuturuishi/ytlabel/tar.gz/" + ytlabelCommit

//go:embed scripts/annotation/ytlabel*
var ytlabelSources embed.FS

func ytlabelSource(name string) string {
	data, err := ytlabelSources.ReadFile("scripts/annotation/ytlabel-" + name)
	if err != nil {
		panic(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// ytlabel writes its isolated unit in configure; prepare only uses the shared
// account helper, so do not ship the unused generic write_unit function.
func ytlabelServiceAccountScript() string {
	script, _, found := strings.Cut(additionalScript("native-service"), "\nwrite_unit() {")
	if !found {
		panic("missing shared service-account boundary")
	}
	return script + "\n"
}

func ytlabelCommand(name string) string {
	common := strings.NewReplacer("@@APP@@", "ytlabel", "@@VERSION@@", "4.0").Replace(additionalScript("common"))
	script := ytlabelSource(name + ".sh")
	switch name {
	case "prepare":
		common += ytlabelServiceAccountScript()
	case "material":
		script = strings.ReplaceAll(script, "@@EXTRACT@@", ytlabelSource("extract.py"))
	case "configure":
		script = strings.ReplaceAll(script, "@@LAUNCH@@", ytlabelSource("launch.py"))
	case "uninstall", "delete-data":
		common += additionalScript("mount-check")
	}
	return common + "\n" + strings.NewReplacer("@@SOURCE_URL@@", ytlabelSourceURL, "@@SOURCE_SHA256@@", ytlabelSourceSHA256, "@@COMMIT@@", ytlabelCommit).Replace(script)
}

func ytlabelDefinitions() []serviceinstall.Definition {
	minimum, maximum := int64(1), int64(16384)
	definitions := []serviceinstall.Definition{}
	for _, source := range []string{"ONLINE", "ARCHIVE"} {
		d := serviceinstall.Definition{
			Key: "custom-ytlabel-native-" + map[string]string{"ONLINE": "online", "ARCHIVE": "offline"}[source], CatalogKey: "ytlabel", Name: "ytlabel", Category: "ANNOTATION", Kind: "SERVICE", Version: "4.0", Revision: "1", Platform: "linux/amd64", OfficialURL: "https://github.com/yuturuishi/ytlabel",
			RuntimeModes: []string{"NATIVE"}, SourceModes: []string{source}, Port: 9924, DefaultMemoryMiB: 2048, DefaultBindAddress: "0.0.0.0",
			Web: &serviceinstall.WebAccessDefinition{Scheme: "http", PortKey: "main", Path: "/", Embed: true, URLParameter: "PUBLIC_URL"},
			Parameters: []serviceinstall.ParameterDefinition{
				{Key: "PYTHON_BINARY", Label: "Python 3.9/3.10 可执行文件", Type: "PATH", Default: "/usr/bin/python3.10", Required: true},
				{Key: "MAX_UPLOAD_MIB", Label: "单次上传上限（MiB）", Type: "INTEGER", Default: "500", Min: &minimum, Max: &maximum, Required: true},
				{Key: "SOURCE_DIRECTORY", Label: "素材导入目录", Type: "PATH"},
				{Key: "PUBLIC_URL", Label: "访问地址", Type: "TEXT"},
			},
			Commands:   &serviceinstall.CommandRecipe{FlowVersion: 1, Revision: 1, Kind: "SYSTEMD", Locator: "ops-app-${OPS_SOFTWARE_ID}.service", BinaryPath: "${OPS_INSTALL_ROOT}/bin/run"},
			Management: ytlabelManagementDefinition(),
		}
		for index, step := range []struct {
			phase, name, script string
			timeout             int
		}{
			{"INSTALL", "检查 Python、系统依赖与数据目录", "prepare", 60},
			{"INSTALL", "核验固定源码并安装独立依赖", "material", 1800},
			{"INSTALL", "配置上传上限、素材目录并启动", "configure", 1800},
			{"VERIFY", "验证标注界面与服务状态", "verify", 180},
			{"UNINSTALL", "卸载程序并保留标注数据", "uninstall", 120},
			{"DELETE_DATA", "删除本实例标注数据与配置", "delete-data", 120},
		} {
			d.Commands.Steps = append(d.Commands.Steps, serviceinstall.CommandStep{ID: fmt.Sprintf("step-%d", index+1), Kind: "COMMAND", Phase: step.phase, Name: step.name, Script: ytlabelCommand(step.script), RunAs: "ROOT", TimeoutSeconds: step.timeout})
		}
		definitions = append(definitions, d)
	}
	return definitions
}
