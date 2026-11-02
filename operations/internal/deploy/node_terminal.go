package deploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// Terminal integration owns only the exact marked block. It never sources a
// user's profile as root, and writes run as the selected ordinary account.
type NodeTerminalProfile struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Connected bool   `json:"connected"`
	Mode      uint32 `json:"mode,omitempty"`
}
type NodeTerminalObservation struct {
	Status   string                `json:"status"`
	Profiles []NodeTerminalProfile `json:"profiles"`
}

func (o *NodeTerminalObservation) Validate(n serviceinstall.NodeContext) error {
	if o == nil {
		return errors.New("NODE_TERMINAL_INVALID")
	}
	home := strings.Split(n.RootPath, "/.local/share/streamfusion-ops/node/")[0]
	if !strings.HasPrefix(home, "/home/") || path.Clean(home) != home {
		return errors.New("NODE_TERMINAL_INVALID")
	}
	if o.Status == "UNSUPPORTED" || o.Status == "CONFLICT" {
		if len(o.Profiles) != 0 {
			return errors.New("NODE_TERMINAL_INVALID")
		}
		return nil
	}
	if o.Status != "CONNECTED" && o.Status != "NOT_CONNECTED" || len(o.Profiles) != 2 {
		return errors.New("NODE_TERMINAL_INVALID")
	}
	connected := true
	for i, p := range o.Profiles {
		if p.Mode > 0777 || p.Mode&0022 != 0 {
			return errors.New("NODE_TERMINAL_INVALID")
		}
		if p.SHA256 != "" && !Hex.MatchString(p.SHA256) {
			return errors.New("NODE_TERMINAL_INVALID")
		}
		if path.Dir(p.Path) != home || i == 0 && path.Base(p.Path) != ".bashrc" || i == 1 && path.Base(p.Path) != ".bash_profile" && path.Base(p.Path) != ".bash_login" && path.Base(p.Path) != ".profile" {
			return errors.New("NODE_TERMINAL_INVALID")
		}
		if p.Connected && p.SHA256 == "" {
			return errors.New("NODE_TERMINAL_INVALID")
		}
		connected = connected && p.Connected
	}
	if (o.Status == "CONNECTED") != connected {
		return errors.New("NODE_TERMINAL_INVALID")
	}
	return nil
}

type nodeTerminalFile struct {
	Path       string `json:"path"`
	BeforeHash string `json:"beforeHash"`
	AfterHash  string `json:"afterHash"`
	Mode       uint32 `json:"mode"`
	Content    []byte `json:"-"`
}
type nodeTerminalPlan struct {
	Files []nodeTerminalFile `json:"files"`
}

func nodeBytesSHA(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func nodeTerminalBlock(n serviceinstall.NodeContext) []byte {
	// Context roots are validated before use. Bash single quotes are escaped even
	// though controller-generated HOME paths normally contain only plain names.
	dir := "'" + strings.ReplaceAll(path.Join(n.RootPath, "nvm"), "'", "'\"'\"'") + "'"
	return []byte("# >>> streamfusion-ops node " + n.EnvironmentID + " >>>\n" +
		"if [ -n \"${BASH_VERSION:-}\" ] && [ -r " + dir + "/nvm.sh ]; then\n" +
		"  export NVM_DIR=" + dir + "\n  . \"$NVM_DIR/nvm.sh\" --no-use\n" +
		"  if [ \"$(nvm version default 2>/dev/null)\" != N/A ]; then nvm use --silent default >/dev/null 2>&1; fi\nfi\n" +
		"# <<< streamfusion-ops node " + n.EnvironmentID + " <<<\n")
}
func nodeTerminalContent(raw []byte, n serviceinstall.NodeContext) ([]byte, bool, error) {
	block := nodeTerminalBlock(n)
	clean := raw
	connected := bytes.Count(raw, block) == 1
	if connected {
		clean = bytes.Replace(raw, block, nil, 1)
	}
	// A foreign manager or a partial/edited Ops block must be resolved explicitly.
	if bytes.Contains(clean, []byte("nvm.sh")) || bytes.Contains(clean, []byte("NVM_DIR")) || bytes.Contains(clean, []byte("streamfusion-ops node")) {
		return nil, false, errors.New("NODE_TERMINAL_CONFLICT")
	}
	if connected {
		return raw, true, nil
	}
	result := append([]byte(nil), raw...)
	if len(result) > 0 && result[len(result)-1] != '\n' {
		result = append(result, '\n')
	}
	return append(result, block...), false, nil
}

func nodeTerminalFiles(n serviceinstall.NodeContext) (*NodeTerminalObservation, nodeTerminalPlan, error) {
	observation := &NodeTerminalObservation{Status: "NOT_CONNECTED", Profiles: []NodeTerminalProfile{}}
	var plan nodeTerminalPlan
	_, account, err := resolveNodeAccount(n, false)
	if err != nil {
		return nil, plan, err
	}
	// Only local Bash accounts are supported; /etc/passwd is data, never shell input.
	passwd, err := os.ReadFile("/etc/passwd")
	if err != nil || len(passwd) > 1<<20 {
		return nil, plan, errors.New("NODE_ACCOUNT_UNAVAILABLE")
	}
	shell := ""
	for _, line := range strings.Split(string(passwd), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) == 7 && fields[0] == n.Account && fields[2] == n.AccountUID && fields[5] == account.HomeDir {
			shell = fields[6]
		}
	}
	if shell != "/bin/bash" && shell != "/usr/bin/bash" {
		observation.Status = "UNSUPPORTED"
		return observation, plan, nil
	}
	// No symlink ancestor may redirect a profile into another account's tree.
	for p := account.HomeDir; p != "/"; p = filepath.Dir(p) {
		st, e := os.Lstat(p)
		if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return nil, plan, errors.New("NODE_TERMINAL_PATH_UNSAFE")
		}
	}
	homeInfo, err := os.Lstat(account.HomeDir)
	if err != nil {
		return nil, plan, err
	}
	uid, _ := nativeFileOwner(homeInfo)
	if strconv.Itoa(uid) != n.AccountUID || homeInfo.Mode().Perm()&0022 != 0 {
		return nil, plan, errors.New("NODE_TERMINAL_PATH_UNSAFE")
	}
	login := ".profile"
	for _, name := range []string{".bash_profile", ".bash_login", ".profile"} {
		if _, e := os.Lstat(filepath.Join(account.HomeDir, name)); e == nil {
			login = name
			break
		} else if !os.IsNotExist(e) {
			return nil, plan, errors.New("NODE_TERMINAL_PATH_UNSAFE")
		}
	}
	all := true
	homeRoot, e := os.OpenRoot(account.HomeDir)
	if e != nil {
		return nil, plan, errors.New("NODE_TERMINAL_PATH_UNSAFE")
	}
	defer homeRoot.Close()
	for _, name := range []string{".bashrc", login} {
		file := filepath.Join(account.HomeDir, name)
		item := nodeTerminalFile{Path: file, Mode: 0600}
		raw := []byte(nil)
		st, e := os.Lstat(file)
		if e == nil {
			owner, _ := nativeFileOwner(st)
			if !st.Mode().IsRegular() || st.Size() > 128<<10 || strconv.Itoa(owner) != n.AccountUID || st.Mode().Perm()&0022 != 0 {
				return nil, plan, errors.New("NODE_TERMINAL_PATH_UNSAFE")
			}
			opened, openErr := homeRoot.Open(name)
			if openErr != nil {
				return nil, plan, errors.New("NODE_TERMINAL_PATH_UNSAFE")
			}
			actual, statErr := opened.Stat()
			if statErr != nil || !os.SameFile(st, actual) {
				opened.Close()
				return nil, plan, errors.New("NODE_TERMINAL_PATH_UNSAFE")
			}
			raw, e = io.ReadAll(io.LimitReader(opened, (128<<10)+1))
			opened.Close()
			if e != nil || len(raw) > 128<<10 {
				return nil, plan, errors.New("NODE_TERMINAL_PATH_UNSAFE")
			}
			item.Mode = uint32(st.Mode().Perm())
			item.BeforeHash = nodeBytesSHA(raw)
		} else if !os.IsNotExist(e) {
			return nil, plan, errors.New("NODE_TERMINAL_PATH_UNSAFE")
		}
		content, connected, e := nodeTerminalContent(raw, n)
		if e != nil {
			observation.Status = "CONFLICT"
			observation.Profiles = nil
			return observation, nodeTerminalPlan{}, nil
		}
		item.Content = content
		item.AfterHash = nodeBytesSHA(content)
		plan.Files = append(plan.Files, item)
		observation.Profiles = append(observation.Profiles, NodeTerminalProfile{Path: file, SHA256: item.BeforeHash, Connected: connected, Mode: item.Mode})
		all = all && connected
	}
	if all {
		observation.Status = "CONNECTED"
	}
	return observation, plan, nil
}

//go:embed node_terminal.sh
var nodeTerminalScript string

func (x *executor) connectNodeTerminal(ctx context.Context, n serviceinstall.NodeContext, job, dir string) error {
	return x.changeNodeTerminal(ctx, n, job, dir, false)
}
func (x *executor) changeNodeTerminal(ctx context.Context, n serviceinstall.NodeContext, job, dir string, remove bool) error {
	observation, plan, err := nodeTerminalFiles(n)
	if err != nil {
		return err
	}
	if observation.Status == "CONFLICT" || observation.Status == "UNSUPPORTED" {
		return errors.New("NODE_TERMINAL_" + observation.Status)
	}
	if n.TerminalHash != "" && n.TerminalHash != Digest(observation) {
		return errors.New("NODE_TERMINAL_CHANGED")
	}
	if remove {
		for i := range plan.Files {
			f := &plan.Files[i]
			if observation.Profiles[i].Connected {
				f.Content = bytes.Replace(f.Content, nodeTerminalBlock(n), nil, 1)
				f.AfterHash = nodeBytesSHA(f.Content)
			} else {
				f.Content = nil
				f.AfterHash = f.BeforeHash
			}
		}
	}
	// Persist only hashes/paths; profile contents may contain unrelated secrets.
	// This intent is durable before the first user-owned file is changed.
	if err = atomicJSON(filepath.Join(dir, "node-terminal.json"), plan); err != nil {
		return err
	}
	stage, err := os.MkdirTemp("/var/tmp", "sfops-node-terminal-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	_, account, err := resolveNodeAccount(n, false)
	if err != nil {
		return err
	}
	uid, _ := strconv.Atoi(account.Uid)
	gid, _ := strconv.Atoi(account.Gid)
	for i, f := range plan.Files {
		file := filepath.Join(stage, strconv.Itoa(i))
		if err = os.WriteFile(file, f.Content, 0400); err != nil {
			return err
		}
		if err = os.Chown(file, uid, gid); err != nil {
			return err
		}
	}
	if err = os.Chown(stage, uid, gid); err != nil {
		return err
	}
	if err = os.Chmod(stage, 0500); err != nil {
		return err
	}
	for i, f := range plan.Files {
		if f.BeforeHash == f.AfterHash {
			continue
		}
		args := []string{"-u", n.Account, "--", "/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "HOME=" + account.HomeDir, "LC_ALL=C", "/usr/bin/bash", "--noprofile", "--norc", "-c", nodeTerminalScript, "sfops-node-terminal", f.Path, f.BeforeHash, f.AfterHash, strconv.FormatUint(uint64(f.Mode), 8), filepath.Join(stage, strconv.Itoa(i)), path.Join(n.RootPath, "backups/terminal", job), n.AccountUID}
		if _, err = x.softwareRunner(ctx, "/usr/sbin/runuser", args...); err != nil {
			return errors.New("NODE_TERMINAL_RESULT_UNCONFIRMED")
		}
	}
	if !nodeTerminalPlanMatches(n, dir, !remove) {
		return errors.New("NODE_TERMINAL_RESULT_UNCONFIRMED")
	}
	return nil
}
func nodeTerminalPlanApplied(n serviceinstall.NodeContext, dir string) bool {
	return nodeTerminalPlanMatches(n, dir, true)
}
func nodeTerminalPlanMatches(n serviceinstall.NodeContext, dir string, connected bool) bool {
	var p nodeTerminalPlan
	if readJSON(filepath.Join(dir, "node-terminal.json"), &p) != nil || len(p.Files) != 2 {
		return false
	}
	observation, _, err := nodeTerminalFiles(n)
	if err != nil || connected && observation.Status != "CONNECTED" || !connected && observation.Status != "NOT_CONNECTED" || observation.Validate(n) != nil {
		return false
	}
	for i, f := range p.Files {
		if f.Path != observation.Profiles[i].Path || f.AfterHash != observation.Profiles[i].SHA256 || f.Mode != observation.Profiles[i].Mode {
			return false
		}
	}
	return true
}
