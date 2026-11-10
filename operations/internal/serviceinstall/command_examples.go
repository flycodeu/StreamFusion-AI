package serviceinstall

import "fmt"

// Examples are editable drafts, never automatically published. Source hashes are
// taken from the same pinned catalog as built-in installations.
func CommandExamples() []Definition {
	out := []Definition{}
	step := func(phase, name, script string) CommandStep {
		return CommandStep{Phase: phase, Name: name, Script: script, RunAs: "ROOT", TimeoutSeconds: 1200}
	}
	for _, base := range Catalog() {
		if base.Key != "java" || base.Native == nil || len(base.Native.Parts) != 1 || base.Native.BuildRecipe != "JDK" {
			continue
		}
		p := base.Native.Parts[0]
		for _, source := range []string{"ONLINE", "ARCHIVE"} {
			suffix := "online"
			if source == "ARCHIVE" {
				suffix = "offline"
			}
			d := Definition{Key: "custom-java-" + base.Version + "-" + suffix, CatalogKey: "java", Name: "Java", Version: base.Version, Revision: "1", Kind: "TOOL", Category: "RUNTIMES", Platform: base.Platform, OfficialURL: base.OfficialURL, RuntimeModes: []string{"NATIVE"}, SourceModes: []string{source}, Commands: &CommandRecipe{Kind: "TOOL", Locator: "${OPS_INSTALL_ROOT}/runtime/bin/java"}}
			input := commandScript("examples/java-offline-material.sh")
			if source == "ONLINE" {
				input = commandScript("examples/java-online-material.sh.tmpl", p.URL)
			}
			install := input + commandScript("examples/java-extract.sh.tmpl", p.SHA256, p.StripComponents)
			d.Commands.Steps = []CommandStep{
				step("INSTALL", "获取并解压 JDK", install),
				step("VERIFY", "验证 Java 与编译器", commandScript("examples/java-verify.sh")),
				step("UNINSTALL", "移除本次 JDK", commandScript("examples/java-uninstall.sh")),
				step("DELETE_DATA", "删除本次数据目录", commandScript("examples/delete-data.sh")),
			}
			out = append(out, d)
		}
	}
	base, _ := Find("nginx", "1.30.6")
	if base.Key != "" {
		for _, source := range []string{"REGISTRY", "ARCHIVE"} {
			suffix := "online"
			install := commandScript("examples/nginx-online-material.sh.tmpl", base.Image, base.Digest)
			if source == "ARCHIVE" {
				suffix = "offline"
				install = commandScript("examples/nginx-offline-material.sh")
			}
			install += commandScript("examples/nginx-create.sh.tmpl", base.ImageID, base.ImageID, base.ImageID)
			d := Definition{Key: "custom-nginx-" + suffix, CatalogKey: "nginx", Name: "Nginx", Version: base.Version, Revision: "1", Kind: "SERVICE", Category: "NETWORK", Platform: base.Platform, OfficialURL: base.OfficialURL, RuntimeModes: []string{"DOCKER"}, SourceModes: []string{source}, Port: 80, DefaultBindAddress: "0.0.0.0", Web: &WebAccessDefinition{Scheme: "http", PortKey: "main", Path: "/"}, Commands: &CommandRecipe{Kind: "DOCKER", Locator: "sfops-${OPS_SOFTWARE_ID}", Steps: []CommandStep{
				step("INSTALL", "获取镜像并启动容器", install), step("VERIFY", "验证 Nginx", commandScript("examples/nginx-verify.sh")), step("UNINSTALL", "移除本次容器", commandScript("examples/nginx-uninstall.sh")), step("DELETE_DATA", "删除本次数据目录", commandScript("examples/delete-data.sh")),
			}}}
			out = append(out, d)
		}
	}
	for index := range out {
		out[index].Commands.FlowVersion = 1
		for step := range out[index].Commands.Steps {
			out[index].Commands.Steps[step].Kind = "COMMAND"
			out[index].Commands.Steps[step].ID = fmt.Sprintf("command-%d", step+1)
		}
	}
	return out
}
