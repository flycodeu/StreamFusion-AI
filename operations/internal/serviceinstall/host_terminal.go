package serviceinstall

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
)

// HostTerminalInstaller returns the same self-contained file for SSH preparation
// and manual installation. A prepared host needs neither a controller nor an agent.
func HostTerminalInstaller() string {
	return strings.Replace(commandScript("host-terminal/install.sh"), "@LAUNCHER@", strings.TrimSuffix(hostTerminalLauncher(), "\n"), 1)
}

func hostTerminalLauncher() string {
	commands := map[string]string{"java": "java", "maven": "mvn", "python": "python3"}
	var images []string
	for _, recipe := range Catalog() {
		command := commands[recipe.Key]
		if command != "" && recipe.Kind == "TOOL" && slices.Contains(recipe.RuntimeModes, "DOCKER") {
			images = append(images, strings.Join([]string{command, recipe.Version, recipe.ImageID, recipe.Digest}, "|"))
		}
	}
	return strings.Replace(commandScript("host-terminal/sf-docker.sh"), "@TOOL_IMAGES@", strings.Join(images, "\n"), 1)
}

// HostTerminalLauncherSHA identifies the exact executable written by the installer.
func HostTerminalLauncherSHA() string {
	hash := sha256.Sum256([]byte(hostTerminalLauncher()))
	return hex.EncodeToString(hash[:])
}

// HostTerminalProbe is read-only. A newer controller can reconcile an older
// install after the operator has already updated the launcher to its current build.
func HostTerminalProbe(expected string) string {
	if decoded, err := hex.DecodeString(expected); err != nil || len(decoded) != sha256.Size {
		return "exit 2\n"
	}
	return strings.NewReplacer("@EXPECTED_SHA@", expected, "@CURRENT_SHA@", HostTerminalLauncherSHA()).Replace(commandScript("host-terminal/probe.sh"))
}
