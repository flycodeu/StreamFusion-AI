package serviceinstall

import (
	"errors"
	"path"
	"regexp"
	"strings"
)

const NodeNVMVersion = "0.40.8"
const NodeNVMHash = "a64806abf6aab85c456f9ffa2a3be737e35c3a1e6ac99a44ea20c69bdc923467"

func NodeEnvironmentRecipes() []Definition {
	nvm := Definition{Key: "nvm", Name: "nvm", Version: "0.40.8", Kind: "TOOL", Category: "RUNTIMES", Revision: "nvm-0.40.8-v1", Platform: "linux/amd64", OfficialURL: "https://github.com/nvm-sh/nvm", RuntimeModes: []string{"NATIVE"}, SourceModes: []string{"OFFICIAL", "ARCHIVE"}, Native: &NativeDefinition{BuildRecipe: "NVM_MANAGER", Parts: []NativePart{
		{Name: "nvm.sh", URL: "https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.8/nvm.sh", SHA256: "a64806abf6aab85c456f9ffa2a3be737e35c3a1e6ac99a44ea20c69bdc923467", ByteSize: 172906, Format: "RAW"},
		{Name: "nvm-exec", URL: "https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.8/nvm-exec", SHA256: "20786579783f26dffd0d38c0f794fb887e763df06a51ec842b2370a4fef0daca", ByteSize: 610, Format: "RAW"},
	}}}
	npm := Definition{Key: "npm", Name: "npm", Version: "11.6.2", Kind: "TOOL", Category: "BUILD_TOOLS", Revision: "npm-11.6.2-v1", Platform: "linux/amd64", OfficialURL: "https://www.npmjs.com/package/npm/v/11.6.2", RuntimeModes: []string{"NATIVE"}, SourceModes: []string{"OFFICIAL", "ARCHIVE"}, Native: &NativeDefinition{BuildRecipe: "NODE_PACKAGE_MANAGER", Parts: []NativePart{{Name: "npm-11.6.2.tgz", URL: "https://registry.npmjs.org/npm/-/npm-11.6.2.tgz", SHA256: "585f95094ee5cb2788ee11d90f2a518a7c9ef6e083fa141d0b63ca3383675a20", ByteSize: 2663834, Format: "TAR_GZIP", StripComponents: 1}}}}
	pnpm := Definition{Key: "pnpm", Name: "pnpm", Version: "10.20.0", Kind: "TOOL", Category: "BUILD_TOOLS", Revision: "pnpm-10.20.0-v1", Platform: "linux/amd64", OfficialURL: "https://www.npmjs.com/package/pnpm/v/10.20.0", RuntimeModes: []string{"NATIVE"}, SourceModes: []string{"OFFICIAL", "ARCHIVE"}, Native: &NativeDefinition{BuildRecipe: "NODE_PACKAGE_MANAGER", Parts: []NativePart{{Name: "pnpm-10.20.0.tgz", URL: "https://registry.npmjs.org/pnpm/-/pnpm-10.20.0.tgz", SHA256: "47a3352808501b8d1ef20112273b6a5dcfa53d28a55bcce36d268e878bd6bfe9", ByteSize: 4196952, Format: "TAR_GZIP", StripComponents: 1}}}}
	return []Definition{nvm, npm, pnpm}
}

func NodeEnvironmentRecipe(action, version string) (Definition, error) {
	key := ""
	switch action {
	case "INITIALIZE", "UPDATE_NVM":
		key = "nvm"
		if version == "" {
			version = NodeNVMVersion
		}
	case "UPDATE_NPM":
		key = "npm"
	case "INSTALL_PNPM", "UPDATE_PNPM":
		key = "pnpm"
	}
	for _, d := range NodeEnvironmentRecipes() {
		if d.Key == key && d.Version == version {
			return d, nil
		}
	}
	return Definition{}, errors.New("NODE_PACKAGE_RECIPE_NOT_FOUND")
}

func NodeCatalog() []Definition {
	rows := []struct {
		version, hash string
		size          int64
	}{{"v22.23.4", "1805bcd35e6815559bd2eb97db438d3fdb0eb3a5e6a977b7c1badca76337e9ce", 31029996}, {"v24.22.2", "48cc0e21e048790e54c0cf5749c2725005313d652651c40b7a21feb9697aaf75", 32039312}}
	result := []Definition{}
	for _, r := range rows {
		name := "node-" + r.version + "-linux-x64.tar.xz"
		result = append(result, Definition{Key: "node", Name: "Node.js", Version: r.version, Kind: "TOOL", Category: "RUNTIMES", Revision: "node-" + r.version + "-nvm-v1", Platform: "linux/amd64", OfficialURL: "https://nodejs.org/en/download/archive/" + r.version, RuntimeModes: []string{"NATIVE"}, SourceModes: []string{"OFFICIAL", "ARCHIVE"}, Native: &NativeDefinition{BuildRecipe: "NVM_BINARY", RequiredTools: []string{"/usr/bin/bash", "/usr/sbin/runuser", "/usr/bin/xz", "/usr/bin/tar"}, Parts: []NativePart{{Name: name, URL: "https://nodejs.org/dist/" + r.version + "/" + name, SHA256: r.hash, ByteSize: r.size, Format: "TAR_XZ", StripComponents: 1}}}})
	}
	return result
}

func FindNode(version string) (Definition, error) {
	for _, d := range NodeCatalog() {
		if d.Version == version {
			return d, nil
		}
	}
	return Definition{}, errors.New("NODE_RECIPE_NOT_FOUND")
}

// NodeContext identifies one account-owned nvm installation. It is frozen in
// the installation plan; callers cannot substitute a different HOME or uid.
type NodeContext struct {
	EnvironmentID      string `json:"environmentId"`
	EnvironmentVersion string `json:"environmentVersion"`
	Account            string `json:"account"`
	AccountUID         string `json:"accountUid"`
	RootPath           string `json:"rootPath"`
	NVMVersion         string `json:"nvmVersion"`
	NVMHash            string `json:"nvmHash"`
	ExpectedDefault    string `json:"expectedDefault"`
	MakeDefault        bool   `json:"makeDefault,omitempty"`
	ConnectTerminal    bool   `json:"connectTerminal,omitempty"`
	TerminalHash       string `json:"terminalHash,omitempty"`
	ObserveRegistry    bool   `json:"observeRegistry,omitempty"`
}

var NodeVersionPattern = regexp.MustCompile(`^v[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$`)
var NodeAccountPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
var nodeUIDPattern = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)

func (n NodeContext) Validate() error {
	if n.TerminalHash != "" && (!n.ConnectTerminal || !sha.MatchString(n.TerminalHash)) {
		return errors.New("NODE_TERMINAL_HASH_INVALID")
	}
	if !uuid.MatchString(n.EnvironmentID) || !revision.MatchString(n.EnvironmentVersion) || !NodeAccountPattern.MatchString(n.Account) || n.Account == "root" || !nodeUIDPattern.MatchString(n.AccountUID) || !ValidNodeRoot(n.RootPath) || !TrustedNodeManager(n.NVMVersion, n.NVMHash) || n.ExpectedDefault != "" && !NodeVersionPattern.MatchString(n.ExpectedDefault) {
		return errors.New("NODE_ENVIRONMENT_INVALID")
	}
	return nil
}

func ValidNodeRoot(root string) bool {
	return strings.HasPrefix(root, "/home/") && path.Clean(root) == root && len(root) < 400 && !strings.ContainsAny(root, "\x00\r\n\t ") && strings.Contains(root, "/.local/share/streamfusion-ops/node/") && uuid.MatchString(path.Base(root))
}

func NodeVersionRoot(n NodeContext, version string) string {
	return path.Join(n.RootPath, "nvm", "versions", "node", version)
}

func IsNode(d Definition) bool {
	return d.Key == "node" && d.Native != nil && d.Native.BuildRecipe == "NVM_BINARY"
}

// Keep accepted manager recipes when adding a newer default. Historical Node
// bindings remain valid after a controller upgrade.
func TrustedNodeManager(version, hash string) bool {
	for _, d := range NodeEnvironmentRecipes() {
		if d.Key == "nvm" && d.Version == version && len(d.Native.Parts) > 0 && d.Native.Parts[0].SHA256 == hash {
			return true
		}
	}
	return false
}
