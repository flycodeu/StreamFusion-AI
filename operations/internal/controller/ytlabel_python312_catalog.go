package controller

import (
	"strings"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// Keep the upstream Python 3.9/3.10 recipe unchanged. This separate recipe uses
// the same source archive with two explicit wheel-compatible dependency pins.
func ytlabelPython312Definitions() []serviceinstall.Definition {
	definitions := ytlabelDefinitions()
	for i := range definitions {
		d := &definitions[i]
		d.Key = strings.Replace(d.Key, "custom-ytlabel-native-", "custom-ytlabel-py312-native-", 1)
		d.Name, d.Version = "ytlabel（Python 3.12）", "4.0-python312"
		for j := range d.Parameters {
			if d.Parameters[j].Key == "PYTHON_BINARY" {
				d.Parameters[j].Label, d.Parameters[j].Default = "Python 3.12 可执行文件", "/usr/bin/python3"
			}
		}
		d.Commands.Steps[0].Script = ytlabelPython312Command("prepare")
		d.Commands.Steps[1].Script = ytlabelPython312Command("material")
	}
	return definitions
}

func ytlabelPython312Command(name string) string {
	if name == "prepare" {
		common := strings.NewReplacer("@@APP@@", "ytlabel", "@@VERSION@@", "4.0").Replace(additionalScript("common"))
		return common + ytlabelServiceAccountScript() + "\n" + ytlabelSource("py312-prepare.sh")
	}
	script := ytlabelCommand(name)
	if name == "material" {
		marker := `readable_material=("$root/runtime/source")`
		if !strings.Contains(script, marker) || strings.Count(script, `-r "$root/runtime/source/requirements.txt"`) != 2 {
			panic("ytlabel material requirements boundary changed")
		}
		requirements := strings.ReplaceAll(ytlabelSource("py312-requirements.sh"), "@@REQUIREMENTS@@", ytlabelSource("py312-requirements.py"))
		script = strings.Replace(script, marker, requirements+"\n"+`readable_material=("$root/runtime/source" "$root/config/requirements.upstream.txt" "$root/config/requirements.effective.txt")`, 1)
		script = strings.ReplaceAll(script, `-r "$root/runtime/source/requirements.txt"`, `-r "$root/config/requirements.effective.txt"`)
	}
	return script
}
