package serviceinstall

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// Built-in templates change commands while the existing material, credential,
// identity and receipt checks continue to define the installation boundary.
func IsBuiltinCommand(d Definition) bool { return d.Commands != nil && d.Commands.Builtin }

func builtinBase(key, version string) (Definition, error) {
	if d, err := FindOfficial(key, version); err == nil {
		return d, nil
	}
	return Find(key, version)
}

// BareDefinition restores only editor metadata. All other fields still take
// part in the comparison against the fixed installation contract.
func BareDefinition(d Definition) Definition {
	if IsBuiltinCommand(d) {
		d.Commands = nil
		d.Parameters = nil
		d.Management = nil
		d.DefaultMemoryMiB = 0
		d.DefaultBindAddress = ""
		d.Web = nil
	}
	return d
}

func BuiltinCommandCatalog() []Definition {
	all := append(Catalog(), OfficialCatalog()...)
	all = append(all, NodeCatalog()...)
	all = append(all, NodeEnvironmentRecipes()...)
	for i := range all {
		all[i].Commands = builtinCommands(all[i])
		all[i], _ = UpgradeBuiltinCommandFlow(all[i])
		if all[i].Key == "nginx" {
			all[i].DefaultBindAddress = "0.0.0.0"
			all[i].Web = &WebAccessDefinition{Scheme: "http", PortKey: "main", Path: "/"}
		}
	}
	return all
}

func (d Definition) validateBuiltinCommands() error {
	base, err := builtinBase(d.Key, d.Version)
	if err != nil || !reflect.DeepEqual(BareDefinition(d), base) || d.Commands.Revision < 1 {
		return errors.New("BUILTIN_TEMPLATE_CONTRACT_CHANGED")
	}
	wanted := builtinCommands(base)
	if d.Commands.Kind != wanted.Kind || d.Commands.Locator != wanted.Locator || d.Commands.BinaryPath != wanted.BinaryPath || !reflect.DeepEqual(d.Commands.Distributions, wanted.Distributions) {
		return errors.New("BUILTIN_TEMPLATE_CONTRACT_CHANGED")
	}
	raw, err := json.Marshal(d)
	if err != nil || len(raw) > 48<<10 {
		return errors.New("COMMAND_TEMPLATE_TOO_LARGE")
	}
	if d.Commands.FlowVersion == 1 {
		base.Commands = wanted
		flow, err := UpgradeBuiltinCommandFlow(base)
		if err != nil {
			return err
		}
		return validateFlowSteps(d, flow.Commands)
	}
	if d.Commands.FlowVersion != 0 || len(d.Commands.Steps) != len(wanted.Steps) {
		return errors.New("BUILTIN_TEMPLATE_CONTRACT_CHANGED")
	}
	for i, step := range d.Commands.Steps {
		original := wanted.Steps[i]
		if step.ID != original.ID || step.Phase != original.Phase || step.RuntimeMode != original.RuntimeMode || !reflect.DeepEqual(step.SourceModes, original.SourceModes) {
			return errors.New("BUILTIN_TEMPLATE_CONTRACT_CHANGED")
		}
		if strings.TrimSpace(step.Name) == "" || len(step.Name) > 120 || strings.TrimSpace(step.Script) == "" || len(step.Script) > 16384 || strings.ContainsRune(step.Script, 0) || !slices.Contains([]string{"ROOT", "ACCOUNT"}, step.RunAs) || step.TimeoutSeconds < 1 || step.TimeoutSeconds > 3600 || len(step.WorkingDirectory) > 300 || strings.ContainsAny(step.WorkingDirectory, "\x00\r\n") {
			return errors.New("COMMAND_STEP_INVALID")
		}
	}
	return nil
}

func builtinCommands(d Definition) *CommandRecipe {
	c := &CommandRecipe{Builtin: true, Revision: 1, Kind: d.Kind, Steps: []CommandStep{}}
	if c.Kind == "" {
		c.Kind = "SERVICE"
	}
	add := func(id, phase, name, mode, script string, sources ...string) {
		c.Steps = append(c.Steps, CommandStep{ID: id, Phase: phase, Name: name, RuntimeMode: mode, SourceModes: sources, Script: script, RunAs: "ROOT", TimeoutSeconds: 1800})
	}
	if d.Key == "node" || d.Key == "nvm" || d.Key == "npm" || d.Key == "pnpm" {
		return builtinNodeCommands(d)
	}
	if IsOfficial(d) {
		spec, _ := OfficialRecipeSpec(d)
		add("OFFICIAL_REFRESH", "INSTALL", "刷新软件包索引", "NATIVE", commandScript("builtin/official-refresh.sh"))
		add("OFFICIAL_PACKAGES_APPLY", "INSTALL", "安装已确认的软件包", "NATIVE", commandScript("builtin/official-install.sh"))
		if spec.Paths.Unit != "" {
			add("OFFICIAL_START", "INSTALL", "启动系统服务", "NATIVE", commandScript("builtin/official-start.sh"))
			add("OFFICIAL_STOP", "UNINSTALL", "停止系统服务", "NATIVE", commandScript("builtin/official-stop.sh"))
		}
		add("VERIFY", "VERIFY", "验证程序", "NATIVE", builtinVerify(d, "NATIVE"))
		add("OFFICIAL_PACKAGES_REMOVE", "UNINSTALL", "卸载本次软件包", "NATIVE", commandScript("builtin/packages-remove.sh"))
		add("DELETE_DATA", "DELETE_DATA", "删除本次数据与配置", "NATIVE", commandScript("builtin/delete-owned-paths.sh"))
		return c
	}
	if slices.Contains(d.RuntimeModes, "DOCKER") {
		add("SERVICE_IMAGE_PULL", "INSTALL", "拉取固定镜像", "DOCKER", commandScript("docker/pull.sh"), "REGISTRY")
		add("SERVICE_IMAGE_LOAD", "INSTALL", "加载离线镜像", "DOCKER", commandScript("docker/load.sh"), "ARCHIVE")
		if d.Kind != "TOOL" {
			add("SERVICE_CREATE", "INSTALL", "创建容器", "DOCKER", builtinDockerCreate(d))
			add("SERVICE_INITIALIZING", "INSTALL", "启动容器", "DOCKER", commandScript("docker/start.sh"))
			add("DOCKER_STOP", "UNINSTALL", "停止容器", "DOCKER", commandScript("docker/stop.sh"))
			add("DOCKER_REMOVE", "UNINSTALL", "移除容器", "DOCKER", commandScript("docker/remove.sh"))
		} else {
			add("DOCKER_IMAGE_REMOVE", "UNINSTALL", "移除未被引用的镜像", "DOCKER", commandScript("docker/remove-image.sh"))
		}
		add("VERIFY_DOCKER", "VERIFY", "验证容器程序", "DOCKER", builtinVerify(d, "DOCKER"))
	}
	if slices.Contains(d.RuntimeModes, "NATIVE") && d.Native != nil {
		if d.Key == "docker" {
			add("DOCKER_PACKAGES_APPLY", "INSTALL", "安装 Docker 软件包", "NATIVE", commandScript("builtin/docker-install.sh"))
			add("DOCKER_START", "INSTALL", "启动 Docker 服务", "NATIVE", commandScript("builtin/docker-start.sh"))
			add("DOCKER_PACKAGES_REMOVE", "UNINSTALL", "卸载 Docker 软件包", "NATIVE", commandScript("builtin/packages-remove.sh"))
		} else {
			add("NATIVE_PROGRAM", "INSTALL", "安装程序文件", "NATIVE", builtinNativeProgram(d))
			if strings.HasSuffix(d.Native.BuildRecipe, "_SOURCE") {
				c.Steps[len(c.Steps)-1].RunAs = "ACCOUNT"
			}
			if d.Key == "mysql" {
				add("NATIVE_MYSQL_INITIALIZE", "INSTALL", "初始化数据库目录", "NATIVE", commandScript("native/mysql-initialize.sh"))
			}
			if d.Kind != "TOOL" {
				add("NATIVE_START", "INSTALL", "启动系统服务", "NATIVE", commandScript("native/service-start.sh"))
				add("NATIVE_STOP", "UNINSTALL", "停止系统服务", "NATIVE", commandScript("native/service-stop.sh"))
				add("NATIVE_REMOVE_UNIT", "UNINSTALL", "移除本次服务单元", "NATIVE", commandScript("native/service-remove.sh"))
			}
			add("NATIVE_REMOVE_PROGRAM", "UNINSTALL", "移除本次程序", "NATIVE", commandScript("native/program-remove.sh"))
		}
		add("VERIFY_NATIVE", "VERIFY", "验证本机程序", "NATIVE", builtinVerify(d, "NATIVE"))
		if d.Key == "codex" || d.Key == "claude" || d.Key == "python" || d.Key == "ffmpeg" {
			c.Steps[len(c.Steps)-1].RunAs = "ACCOUNT"
		}
	}
	add("DELETE_DATA", "DELETE_DATA", "删除本次数据与配置", "", commandScript("builtin/delete-install-root.sh"))
	return c
}

func builtinVerify(d Definition, mode string) string {
	command := ""
	switch d.Key {
	case "java", "maven", "python", "ffmpeg", "git", "codex", "claude", "mysql", "redis", "nginx", "seaweedfs", "mediamtx", "zlm", "docker":
		command = commandScript("verify/programs/" + d.Key + ".sh")
	}
	binary, args, _ := strings.Cut(command, " ")
	if mode == "DOCKER" {
		if d.Kind == "TOOL" {
			return commandScript("verify/docker-tool.sh.tmpl", binary, args)
		}
		return commandScript("verify/docker-service.sh.tmpl", command)
	}
	if command == "" {
		args = "--version"
	}
	probe := commandScript("verify/native.sh.tmpl", args)
	if d.Native != nil {
		switch d.Native.BuildRecipe {
		case "GIT_DEB":
			probe = commandScript("verify/git-libraries.sh") + probe
		case "DEB":
			name := "redis"
			if d.Key == "nginx" {
				name = "nginx"
			}
			probe = commandScript("verify/"+name+"-libraries.sh") + probe
		}
	}
	return probe
}

// Shell argument lists are rendered from the validated plan at execution time;
// unlike $@ they name the exact inputs, and the expanded command is logged.
func builtinDockerCreate(d Definition) string {
	return commandScript("docker/create.sh")
}

func builtinNativeProgram(d Definition) string {
	if d.Native == nil {
		return commandScript("native/program-unavailable.sh")
	}
	parts := []string{}
	for i, p := range d.Native.Parts {
		name := fmt.Sprintf("${OPS_PART_%d}", i)
		dest := "$OPS_INSTALL_ROOT/runtime"
		if d.Native.BuildRecipe == "MAVEN" {
			if i == 0 {
				dest += "/maven"
			} else {
				dest += "/jdk"
			}
		}
		if d.Native.BuildRecipe == "MYSQL_BINARY" {
			dest += "/mysql"
		}
		if slices.Contains([]string{"FFMPEG_SOURCE", "PYTHON_SOURCE", "REDIS_SOURCE", "NGINX_SOURCE", "ZLM_SOURCE"}, d.Native.BuildRecipe) {
			dest = "$OPS_BUILD_ROOT/source"
			if i > 0 {
				dest += "/" + map[string]string{"toolkit.tar.gz": "3rdpart/ZLToolKit", "jsoncpp.tar.gz": "3rdpart/jsoncpp", "media-server.tar.gz": "3rdpart/media-server", "pybind11.tar.gz": "3rdpart/pybind11", "webassist.tar.gz": "www/webassist"}[p.Name]
			}
		}
		if d.Native.BuildRecipe == "CODEX_BINARY" || d.Native.BuildRecipe == "BINARY" {
			dest += "/vendor"
		}
		if d.Native.BuildRecipe == "GIT_DEB" {
			dest += "/git"
		}
		if d.Native.BuildRecipe == "DEB" {
			dest += fmt.Sprintf("/package-%d", i)
		}
		parts = append(parts, commandScript("native/prepare-directory.sh.tmpl", dest))
		if p.Format == "RAW" {
			parts = append(parts, commandScript("native/install-raw.sh.tmpl", name))
		} else {
			parts = append(parts, commandScript("native/copy-part.sh.tmpl", name, dest))
		}
	}
	script := strings.Join(parts, "\n")
	build := ""
	switch d.Native.BuildRecipe {
	case "CODEX_BINARY":
		script += commandScript("native/codex-binary.sh")
	case "BINARY":
		binary := "weed"
		if d.Key == "mediamtx" {
			binary = "mediamtx"
		}
		script += commandScript("native/vendor-binary.sh.tmpl", binary)
	case "MAVEN":
		script += commandScript("native/maven-wrapper.sh")
	case "GIT_DEB":
		script += commandScript("native/git-wrapper.sh")
	case "DEB":
		if d.Key == "nginx" {
			script += commandScript("native/nginx-deb.sh")
		} else {
			script += builtinRedisDeb(d)
		}
	case "NGINX_SOURCE":
		build = commandScript("native/nginx-source.sh")
	case "REDIS_SOURCE":
		build = commandScript("native/redis-source.sh")
	case "FFMPEG_SOURCE":
		build = commandScript("native/ffmpeg-source.sh")
	case "PYTHON_SOURCE":
		build = commandScript("native/python-source.sh")
	case "ZLM_SOURCE":
		build = commandScript("native/zlm-source.sh")
	}
	if build != "" {
		script += commandScript("native/source-directory.sh") + build
	}
	return script
}

func builtinRedisDeb(d Definition) string {
	script := commandScript("native/redis-directories.sh")
	for i, p := range d.Native.Parts {
		base := fmt.Sprintf("$OPS_INSTALL_ROOT/runtime/package-%d", i)
		switch {
		case strings.HasPrefix(p.Name, "redis-tools_"):
			script += commandScript("native/redis-tools.sh.tmpl", base, base)
		case strings.HasPrefix(p.Name, "liblzf1_"):
			script += commandScript("native/redis-lzf.sh.tmpl", base)
		case strings.HasPrefix(p.Name, "libjemalloc2_"):
			script += commandScript("native/redis-jemalloc.sh.tmpl", base)
		}
	}
	return script + commandScript("native/redis-wrappers.sh")
}

func builtinNodeCommands(d Definition) *CommandRecipe {
	installActions, removeActions := []string{"install"}, []string{"uninstall"}
	switch d.Key {
	case "nvm":
		installActions, removeActions = []string{"initialize", "update-nvm"}, []string{"remove-environment"}
	case "npm", "pnpm":
		installActions, removeActions = []string{"package-install|package-remove"}, []string{"package-install|package-remove"}
	}
	verify := commandScript("node/verify-node.sh")
	if d.Key == "nvm" {
		verify = commandScript("node/verify-nvm.sh")
	}
	if d.Key == "npm" || d.Key == "pnpm" {
		binary := "npm/bin/npm-cli.js"
		if d.Key == "pnpm" {
			binary = "pnpm/bin/pnpm.cjs"
		}
		verify = commandScript("node/verify-package.sh.tmpl", binary)
	}
	cleanup := nodeTemplateScript("clean-version")
	if d.Key == "nvm" {
		cleanup = commandScript("node/remove-environment.sh")
	}
	c := &CommandRecipe{Builtin: true, Revision: 1, Kind: "TOOL", Steps: []CommandStep{}}
	for _, s := range []struct{ id, phase, name, script string }{
		{"NODE_INSTALL", "INSTALL", "安装账号环境", nodeTemplateScript(installActions...)},
		{"NODE_VERIFY", "VERIFY", "验证账号环境", verify},
		{"NODE_UNINSTALL", "UNINSTALL", "卸载账号环境程序", nodeTemplateScript(removeActions...)},
		{"DELETE_DATA", "DELETE_DATA", "删除环境数据", cleanup},
	} {
		if d.Key == "npm" && (s.phase == "UNINSTALL" || s.phase == "DELETE_DATA") || d.Key == "pnpm" && s.phase == "DELETE_DATA" {
			continue
		}
		c.Steps = append(c.Steps, CommandStep{ID: s.id, Phase: s.phase, Name: s.name, RuntimeMode: "NATIVE", Script: s.script, RunAs: "ACCOUNT", TimeoutSeconds: 1800})
	}
	return c
}
