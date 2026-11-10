package serviceinstall

import (
	"errors"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

var commandStepID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,79}$`)

// UpgradeBuiltinCommandFlow keeps edited commands and adds the protected file,
// identity and package-plan operations around them. Job snapshots are not changed.
func UpgradeBuiltinCommandFlow(d Definition) (Definition, error) {
	if !IsBuiltinCommand(d) || d.Commands.FlowVersion == 1 {
		return d, nil
	}
	if err := d.validateBuiltinCommands(); err != nil {
		return d, err
	}
	old := d.Commands
	c := *old
	c.FlowVersion, c.Steps = 1, nil
	system := func(action, name, mode string, phases ...string) {
		phase := "INSTALL"
		if len(phases) > 0 {
			phase = phases[0]
		}
		c.Steps = append(c.Steps, CommandStep{ID: action + "_" + mode, Kind: "SYSTEM", Action: action, Name: name, Phase: phase, RuntimeMode: mode, RunAs: "ROOT", TimeoutSeconds: 1800})
	}
	commands := func(mode string, ids ...string) {
		for _, id := range ids {
			for _, step := range old.Steps {
				if step.ID == id && (mode == "" || step.RuntimeMode == "" || step.RuntimeMode == mode) {
					step.Kind = "COMMAND"
					c.Steps = append(c.Steps, step)
				}
			}
		}
	}
	if slices.Contains([]string{"node", "nvm", "npm", "pnpm"}, d.Key) {
		system("INSTALL_MATERIAL", "获取并校验安装材料", "NATIVE")
		system("NODE_PREPARE", "检查账号环境并保存快照", "NATIVE")
		commands("NATIVE", "NODE_INSTALL", "NODE_VERIFY")
		if d.Key == "node" {
			c.Steps = expandNodeInstallSteps(c.Steps)
		}
		if d.Key == "node" {
			system("NODE_TERMINAL", "配置账号终端", "NATIVE")
		}
		system("INSTALL_CONFIRM", "核对安装结果", "NATIVE")
	} else if IsOfficial(d) {
		commands("NATIVE", "OFFICIAL_REFRESH")
		if d.Key == "docker" {
			system("INSTALL_MATERIAL", "获取并校验 Docker 软件包", "NATIVE")
		}
		system("OFFICIAL_PLAN", "核对软件包版本与依赖", "NATIVE")
		commands("NATIVE", "OFFICIAL_PACKAGES_APPLY")
		system("OFFICIAL_OWNERSHIP", "记录本次安装的软件包", "NATIVE")
		commands("NATIVE", "OFFICIAL_START", "VERIFY")
		system("INSTALL_CONFIRM", "核对安装结果", "NATIVE")
	} else {
		if slices.Contains(d.RuntimeModes, "DOCKER") {
			system("INSTALL_MATERIAL", "接收并校验离线镜像", "DOCKER")
			c.Steps[len(c.Steps)-1].SourceModes = []string{"ARCHIVE"}
			commands("DOCKER", "SERVICE_IMAGE_PULL", "SERVICE_IMAGE_LOAD")
			system("DOCKER_PREPARE", "核对镜像并生成容器配置", "DOCKER")
			commands("DOCKER", "SERVICE_CREATE", "SERVICE_INITIALIZING", "VERIFY_DOCKER")
			system("INSTALL_CONFIRM", "核对安装结果", "DOCKER")
		}
		if slices.Contains(d.RuntimeModes, "NATIVE") {
			system("INSTALL_MATERIAL", "获取并校验安装材料", "NATIVE")
			system("NATIVE_PREPARE", "准备安装目录与程序文件", "NATIVE")
			commands("NATIVE", "NATIVE_PROGRAM", "DOCKER_PACKAGES_APPLY")
			system("NATIVE_CONFIGURE", "配置程序与运行环境", "NATIVE")
			commands("NATIVE", "NATIVE_MYSQL_INITIALIZE", "NATIVE_START", "DOCKER_START", "VERIFY_NATIVE")
			system("INSTALL_CONFIRM", "核对安装结果", "NATIVE")
		}
	}
	system("UNINSTALL_PREPARE", "核对本次安装身份", "", "UNINSTALL")
	for _, step := range old.Steps {
		if step.Phase == "UNINSTALL" || step.Phase == "DELETE_DATA" {
			step.Kind = "COMMAND"
			if step.ID == "DELETE_DATA" && d.Key == "docker" && !IsOfficial(d) && step.Script == commandScript("builtin/delete-install-root.sh") {
				step.Script = commandScript("docker/remove-daemon-config.sh") + step.Script
			}
			c.Steps = append(c.Steps, step)
		}
	}
	system("UNINSTALL_CONFIRM", "核对卸载结果", "", "UNINSTALL")
	d.Commands = &c
	return d, nil
}

func expandNodeInstallSteps(steps []CommandStep) []CommandStep {
	result := []CommandStep{}
	for _, step := range steps {
		if step.ID == "NODE_INSTALL" && step.Script == nodeTemplateScript("install") {
			for _, item := range []struct{ id, name, script string }{
				{"NODE_STAGE", "准备 Node 安装包", commandScript("node/stage.sh")},
				{"NODE_INSTALL", "安装 Node.js", commandScript("node/install.sh")},
				{"NODE_DEFAULT", "设置默认 Node 版本", commandScript("node/default.sh")},
			} {
				copy := step
				copy.ID, copy.Name, copy.Script = item.id, item.name, item.script
				result = append(result, copy)
			}
		} else {
			result = append(result, step)
		}
		if step.ID == "NODE_VERIFY" {
			copy := step
			copy.ID, copy.Name = "NODE_VERIFY_NPM", "验证 npm 版本"
			copy.Script = commandScript("node/verify-npm.sh")
			result = append(result, copy)
		}
	}
	return result
}

func validateFlowSteps(d Definition, wanted *CommandRecipe) error {
	c := d.Commands
	if c.FlowVersion != 1 || len(c.Steps) < 2 || len(c.Steps) > 80 {
		return errors.New("COMMAND_STEP_INVALID")
	}
	seen := map[string]bool{}
	protected := []CommandStep{}
	for _, step := range wanted.Steps {
		if step.Kind == "SYSTEM" {
			protected = append(protected, step)
		}
	}
	next := 0
	for _, step := range c.Steps {
		if !commandStepID.MatchString(step.ID) || seen[step.ID] {
			return errors.New("COMMAND_STEP_INVALID")
		}
		seen[step.ID] = true
		if step.Kind == "SYSTEM" {
			if next >= len(protected) || !reflect.DeepEqual(step, protected[next]) {
				return errors.New("BUILTIN_TEMPLATE_CONTRACT_CHANGED")
			}
			next++
			continue
		}
		if step.Kind != "" && step.Kind != "COMMAND" || step.Action != "" || !slices.Contains([]string{"INSTALL", "VERIFY", "UNINSTALL", "DELETE_DATA"}, step.Phase) || step.RuntimeMode != "" && !slices.Contains(d.RuntimeModes, step.RuntimeMode) {
			return errors.New("COMMAND_STEP_INVALID")
		}
		for _, source := range step.SourceModes {
			if !slices.Contains(d.SourceModes, source) {
				return errors.New("COMMAND_STEP_INVALID")
			}
		}
		if strings.TrimSpace(step.Name) == "" || len(step.Name) > 120 || strings.TrimSpace(step.Script) == "" || len(step.Script) > 16384 || strings.ContainsRune(step.Script, 0) || !slices.Contains([]string{"ROOT", "ACCOUNT"}, step.RunAs) || step.TimeoutSeconds < 1 || step.TimeoutSeconds > 3600 || len(step.WorkingDirectory) > 300 || strings.ContainsAny(step.WorkingDirectory, "\x00\r\n") {
			return errors.New("COMMAND_STEP_INVALID")
		}
	}
	if next != len(protected) {
		return errors.New("BUILTIN_TEMPLATE_CONTRACT_CHANGED")
	}
	for _, mode := range d.RuntimeModes {
		for _, source := range d.SourceModes {
			install, uninstall := []CommandStep{}, []CommandStep{}
			for _, step := range c.Steps {
				if step.RuntimeMode != "" && step.RuntimeMode != mode || len(step.SourceModes) > 0 && !slices.Contains(step.SourceModes, source) {
					continue
				}
				if step.Phase == "INSTALL" || step.Phase == "VERIFY" {
					install = append(install, step)
				} else {
					uninstall = append(uninstall, step)
				}
			}
			if len(install) == 0 || install[len(install)-1].Action != "INSTALL_CONFIRM" || len(uninstall) < 2 || uninstall[0].Action != "UNINSTALL_PREPARE" || uninstall[len(uninstall)-1].Action != "UNINSTALL_CONFIRM" {
				return errors.New("COMMAND_STEP_ORDER_INVALID")
			}
		}
	}
	return nil
}
