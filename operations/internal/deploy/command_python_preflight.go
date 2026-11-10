package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

type commandPythonProbe struct {
	Version          []int    `json:"version"`
	MissingModules   []string `json:"missingModules"`
	MissingLibraries []string `json:"missingLibraries"`
	SQLiteVersion    []int    `json:"sqliteVersion"`
}

// Only the maintained recipe keys declare an exact runtime requirement. A
// user template's display/catalog grouping never changes its Python version.
func commandPythonRequirements(key string) (version, profile string) {
	switch key {
	case "custom-ytlabel-py312-native-online", "custom-ytlabel-py312-native-offline":
		return "3.12", "ytlabel"
	case "custom-ytlabel-native-online", "custom-ytlabel-native-offline":
		return "3.9/3.10", "ytlabel"
	case "custom-label-studio-native-online", "custom-label-studio-native-offline":
		return "3.12", "label-studio"
	}
	return "3", ""
}

func (x *executor) commandPythonPreflight(i serviceinstall.Request) []serviceinstall.Check {
	binary, declared := i.Config.Parameters["PYTHON_BINARY"], false
	for _, parameter := range i.Definition.Parameters {
		if parameter.Key == "PYTHON_BINARY" && parameter.Type == "PATH" {
			declared = true
			if binary == "" {
				binary = parameter.Default
			}
		}
	}
	if !declared {
		return nil
	}
	fail := func(code, message string) []serviceinstall.Check {
		return []serviceinstall.Check{{Key: "PYTHON", Status: "FAIL", Code: code, Message: message}}
	}
	if !filepath.IsAbs(binary) || filepath.Clean(binary) != binary || len(binary) > 512 || strings.ContainsAny(binary, "\x00\r\n") {
		return fail("COMMAND_PYTHON_PATH_INVALID", "请选择 Python 可执行文件的完整路径")
	}
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return fail("COMMAND_PYTHON_NOT_FOUND", "所选 Python 不存在，请先安装兼容版本或选择已有解释器")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return fail("COMMAND_PYTHON_NOT_EXECUTABLE", "所选 Python 不可执行，请检查文件与执行权限")
	}
	if secureAncestors(resolved) != nil {
		return fail("COMMAND_PYTHON_PATH_UNSAFE", "Python 路径须由管理员维护，请选择系统或 Ops 安装的解释器")
	}
	_, profile := commandPythonRequirements(i.Definition.Key)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, err := x.softwareRunner(ctx, resolved, "-I", "-S", "-B", "-c", deployScript("software/python-preflight.py"), profile)
	var probe commandPythonProbe
	if err != nil || StrictJSON(out, &probe) != nil || len(probe.Version) != 3 || len(probe.MissingModules) > 3 || len(probe.MissingLibraries) > 3 {
		return fail("COMMAND_PYTHON_CHECK_UNAVAILABLE", "无法确认 Python 环境，请检查解释器、读取权限和运行状态")
	}
	return commandPythonChecks(i.Definition.Key, probe)
}

func commandPythonChecks(key string, probe commandPythonProbe) []serviceinstall.Check {
	checks := []serviceinstall.Check{}
	fail := func(key, code, message string) {
		checks = append(checks, serviceinstall.Check{Key: key, Status: "FAIL", Code: code, Message: message})
	}
	required, profile := commandPythonRequirements(key)
	major, minor := probe.Version[0], probe.Version[1]
	if major != 3 || required == "3.12" && minor != 12 || required == "3.9/3.10" && minor != 9 && minor != 10 {
		fail("PYTHON_VERSION", "COMMAND_PYTHON_VERSION_UNSUPPORTED", fmt.Sprintf("此安装方案需要 Python %s，所选解释器为 %d.%d", required, major, minor))
	}
	for _, module := range []string{"venv", "ensurepip", "sqlite3", "ctypes"} {
		if slices.Contains(probe.MissingModules, module) {
			fail("PYTHON_MODULE_"+module, "COMMAND_PYTHON_MODULE_MISSING", "Python 缺少 "+module+"，请先安装对应的 Python 运行组件")
		}
	}
	if profile == "ytlabel" {
		for _, library := range []string{"libGL.so.1", "libglib-2.0.so.0", "libgthread-2.0.so.0"} {
			if slices.Contains(probe.MissingLibraries, library) {
				fail("PYTHON_LIBRARY_"+library, "COMMAND_PYTHON_LIBRARY_MISSING", "ytlabel 缺少系统库 "+library+"，请先安装对应系统组件")
			}
		}
	}
	if profile == "label-studio" && !slices.Contains(probe.MissingModules, "sqlite3") && (len(probe.SQLiteVersion) != 3 || probe.SQLiteVersion[0] < 3 || probe.SQLiteVersion[0] == 3 && probe.SQLiteVersion[1] < 35) {
		fail("PYTHON_SQLITE", "COMMAND_PYTHON_SQLITE_UNSUPPORTED", "Label Studio 需要 Python 内置 SQLite 3.35 以上，请选择兼容解释器")
	}
	if len(checks) == 0 {
		checks = append(checks, serviceinstall.Check{Key: "PYTHON", Status: "PASS", Code: "COMMAND_PYTHON_READY", Message: fmt.Sprintf("Python %d.%d 运行环境已就绪", major, minor)})
	}
	return checks
}
