package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

const defaultNodeRegistry = "https://registry.npmjs.org/"

type NodeRegistryObservation struct {
	Status       string `json:"status"`
	URL          string `json:"url,omitempty"`
	ConfigPath   string `json:"configPath"`
	ConfigSHA256 string `json:"configSha256"`
	Mode         uint32 `json:"mode"`
	Exists       bool   `json:"exists"`
}

func (o *NodeRegistryObservation) Validate(n serviceinstall.NodeContext) error {
	if o == nil {
		return errors.New("NODE_REGISTRY_OBSERVATION_INVALID")
	}
	home := strings.Split(n.RootPath, "/.local/share/streamfusion-ops/node/")[0]
	if !strings.HasPrefix(home, "/home/") || o.ConfigPath != path.Join(home, ".npmrc") || o.Mode > 0777 || o.Mode&0022 != 0 || o.ConfigSHA256 != "" && !Hex.MatchString(o.ConfigSHA256) {
		return errors.New("NODE_REGISTRY_OBSERVATION_INVALID")
	}
	switch o.Status {
	case "DEFAULT", "CONFIGURED":
		if serviceinstall.ValidateNodeRegistryURL(o.URL) != nil {
			return errors.New("NODE_REGISTRY_OBSERVATION_INVALID")
		}
		if o.Status == "DEFAULT" && o.URL != defaultNodeRegistry || o.Status == "CONFIGURED" && (!o.Exists || o.ConfigSHA256 == "") {
			return errors.New("NODE_REGISTRY_OBSERVATION_INVALID")
		}
	case "REDACTED", "UNAVAILABLE", "UNSAFE":
		if o.URL != "" {
			return errors.New("NODE_REGISTRY_OBSERVATION_INVALID")
		}
	default:
		return errors.New("NODE_REGISTRY_OBSERVATION_INVALID")
	}
	if o.Status != "UNSAFE" && (o.Exists != (o.ConfigSHA256 != "") || !o.Exists && o.Mode != 0) {
		return errors.New("NODE_REGISTRY_OBSERVATION_INVALID")
	}
	return nil
}

type nodeRegistryFile struct {
	Path, Hash string
	Mode       uint32
	Exists     bool
	Content    []byte
}

func readNodeRegistryFile(n serviceinstall.NodeContext) (nodeRegistryFile, error) {
	var f nodeRegistryFile
	_, account, err := resolveNodeAccount(n, false)
	if err != nil {
		return f, err
	}
	f.Path = filepath.Join(account.HomeDir, ".npmrc")
	for p := account.HomeDir; p != "/"; p = filepath.Dir(p) {
		st, e := os.Lstat(p)
		if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return f, errors.New("NODE_REGISTRY_PATH_UNSAFE")
		}
	}
	st, err := os.Lstat(account.HomeDir)
	if err != nil {
		return f, errors.New("NODE_REGISTRY_PATH_UNSAFE")
	}
	uid, _ := nativeFileOwner(st)
	if strconv.Itoa(uid) != n.AccountUID || st.Mode().Perm()&0022 != 0 {
		return f, errors.New("NODE_REGISTRY_PATH_UNSAFE")
	}
	st, err = os.Lstat(f.Path)
	if os.IsNotExist(err) {
		return f, nil
	}
	if err != nil {
		return f, errors.New("NODE_REGISTRY_PATH_UNSAFE")
	}
	uid, _ = nativeFileOwner(st)
	if !st.Mode().IsRegular() || st.Size() > 128<<10 || strconv.Itoa(uid) != n.AccountUID || st.Mode().Perm()&0022 != 0 {
		return f, errors.New("NODE_REGISTRY_PATH_UNSAFE")
	}
	root, err := os.OpenRoot(account.HomeDir)
	if err != nil {
		return f, errors.New("NODE_REGISTRY_PATH_UNSAFE")
	}
	defer root.Close()
	file, err := root.Open(".npmrc")
	if err != nil {
		return f, errors.New("NODE_REGISTRY_PATH_UNSAFE")
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(st, actual) {
		return f, errors.New("NODE_REGISTRY_PATH_UNSAFE")
	}
	raw, err := io.ReadAll(io.LimitReader(file, (128<<10)+1))
	if err != nil || len(raw) > 128<<10 {
		return f, errors.New("NODE_REGISTRY_PATH_UNSAFE")
	}
	f.Hash = nodeBytesSHA(raw)
	f.Content = raw
	f.Mode = uint32(st.Mode().Perm())
	f.Exists = true
	return f, nil
}

// npm ships its INI parser. Reusing it preserves npm's arrays, scoped keys and
// authentication values without introducing another parser or exposing them.
const nodeRegistryInspectScript = `const fs=require('fs'),crypto=require('crypto');
const ini=require(process.argv[1]);const file=process.argv[2];
let raw='';try{raw=fs.readFileSync(file,'utf8');}catch(e){if(e.code!=='ENOENT')process.exit(1);}
if(Buffer.byteLength(raw)>131072)process.exit(1);
const config=ini.parse(raw);const value=config.registry;delete config.registry;
function sorted(v){if(Array.isArray(v))return v.map(sorted);if(v&&typeof v==='object')return Object.fromEntries(Object.keys(v).sort().map(k=>[k,sorted(v[k])]));return v;}
const otherHash=crypto.createHash('sha256').update(JSON.stringify(sorted(config))).digest('hex');
let registry='',configured=value!==undefined,redacted=false;
if(configured){if(typeof value!=='string'||value.length>2048){redacted=true;}else{try{const u=new URL(value);if(!['http:','https:'].includes(u.protocol)||u.username||u.password||u.search||u.hash||/[\s\x00-\x1f\x7f]/.test(value)){redacted=true;}else{registry=value;}}catch{redacted=true;}}}
process.stdout.write(JSON.stringify({registry,configured,redacted,otherHash}));`

type nodeRegistryInspection struct {
	Registry   string `json:"registry"`
	Configured bool   `json:"configured"`
	Redacted   bool   `json:"redacted"`
	OtherHash  string `json:"otherHash"`
}

func (x *executor) inspectNodeRegistry(ctx context.Context, n serviceinstall.NodeContext, v NodeVersionObservation, file string) (nodeRegistryInspection, error) {
	var result nodeRegistryInspection
	_, account, err := resolveNodeAccount(n, false)
	if err != nil {
		return result, err
	}
	ini := path.Join(serviceinstall.NodeVersionRoot(n, v.Version), "lib/node_modules/npm/node_modules/ini")
	out, err := x.softwareRunner(ctx, "/usr/sbin/runuser", "-u", n.Account, "--", "/usr/bin/env", "-i", "HOME="+account.HomeDir, "PATH=/usr/bin:/bin", v.BinaryPath, "--no-warnings", "-e", nodeRegistryInspectScript, ini, file)
	if err != nil || len(out) > 4096 || json.Unmarshal(out, &result) != nil || !Hex.MatchString(result.OtherHash) {
		return result, errors.New("NODE_REGISTRY_READ_FAILED")
	}
	return result, nil
}
func nodeRegistryVersion(obs *NodeEnvironmentObservation, selected string) *NodeVersionObservation {
	if selected == "" {
		selected = obs.DefaultVersion
	}
	if selected != "" {
		if v := nodeVersion(obs, selected); v != nil && v.NPMVersion != "" {
			return v
		}
	}
	if selected != "" {
		return nil
	}
	for i := range obs.Versions {
		if obs.Versions[i].NPMVersion != "" {
			return &obs.Versions[i]
		}
	}
	return nil
}
func (x *executor) observeNodeRegistry(ctx context.Context, n serviceinstall.NodeContext, obs *NodeEnvironmentObservation) *NodeRegistryObservation {
	home := strings.Split(n.RootPath, "/.local/share/streamfusion-ops/node/")[0]
	result := &NodeRegistryObservation{Status: "UNSAFE", ConfigPath: path.Join(home, ".npmrc")}
	file, err := readNodeRegistryFile(n)
	if err != nil {
		return result
	}
	result.ConfigSHA256, result.Mode, result.Exists = file.Hash, file.Mode, file.Exists
	v := nodeRegistryVersion(obs, "")
	if v == nil {
		result.Status = "UNAVAILABLE"
		return result
	}
	parsed, err := x.inspectNodeRegistry(ctx, n, *v, file.Path)
	if err != nil {
		result.Status = "UNAVAILABLE"
		return result
	}
	if parsed.Redacted || parsed.Configured && serviceinstall.ValidateNodeRegistryURL(parsed.Registry) != nil {
		result.Status = "REDACTED"
		return result
	}
	result.Status, result.URL = "DEFAULT", defaultNodeRegistry
	if parsed.Configured {
		result.Status, result.URL = "CONFIGURED", parsed.Registry
	}
	return result
}

type nodeRegistryMarker struct {
	JobID, InputHash, RegistryURL, Path, BeforeHash, AfterHash, OtherHash string
	Mode                                                                  uint32
}

func (x *executor) applyNodeRegistry(ctx context.Context, req Request, dir string, before *NodeEnvironmentObservation) error {
	n := req.NodeEnvironment
	v := nodeRegistryVersion(before, n.NodeVersion)
	if v == nil {
		return errors.New("NODE_REGISTRY_NPM_REQUIRED")
	}
	file, err := readNodeRegistryFile(n.Context)
	if err != nil {
		return err
	}
	if before.Registry == nil || before.Registry.Validate(n.Context) != nil || file.Hash != before.Registry.ConfigSHA256 || file.Mode != before.Registry.Mode || file.Exists != before.Registry.Exists {
		return errors.New("NODE_REGISTRY_CHANGED")
	}
	parsed, err := x.inspectNodeRegistry(ctx, n.Context, *v, file.Path)
	if err != nil {
		return err
	}
	_, account, err := resolveNodeAccount(n.Context, false)
	if err != nil {
		return err
	}
	uid, _ := strconv.Atoi(account.Uid)
	gid, _ := strconv.Atoi(account.Gid)
	stage, err := os.MkdirTemp("/var/tmp", "sfops-node-registry-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err = os.Chown(stage, uid, gid); err != nil {
		return err
	}
	candidate := filepath.Join(stage, "user.npmrc")
	global := filepath.Join(stage, "global.npmrc")
	for name, raw := range map[string][]byte{candidate: file.Content, global: nil} {
		if err = os.WriteFile(name, raw, 0600); err != nil {
			return err
		}
		if err = os.Chown(name, uid, gid); err != nil {
			return err
		}
	}
	// Work only on a private candidate. No project/global config, package script,
	// registry request, debug log or authentication value is emitted to job logs.
	args := []string{"-u", n.Context.Account, "--", "/usr/bin/env", "-i", "HOME=" + account.HomeDir, "PATH=" + path.Dir(v.BinaryPath) + ":/usr/bin:/bin", "NPM_CONFIG_LOGS_MAX=0", "NPM_CONFIG_LOGLEVEL=silent", "NPM_CONFIG_UPDATE_NOTIFIER=false", "NPM_CONFIG_IGNORE_SCRIPTS=true", "NPM_CONFIG_CACHE=" + filepath.Join(stage, "cache"), "/usr/bin/bash", "--noprofile", "--norc", "-c", `cd -- "$1"; shift; exec "$@"`, "sfops-npm-registry", stage, v.BinaryPath, path.Join(serviceinstall.NodeVersionRoot(n.Context, v.Version), "lib/node_modules/npm/bin/npm-cli.js"), "config", "set", "registry", n.RegistryURL, "--location=user", "--userconfig=" + candidate, "--globalconfig=" + global}
	if _, err = x.softwareRunner(ctx, "/usr/sbin/runuser", args...); err != nil {
		return errors.New("NODE_REGISTRY_CONFIG_REJECTED")
	}
	after, err := x.inspectNodeRegistry(ctx, n.Context, *v, candidate)
	if err != nil {
		return err
	}
	if after.Registry != n.RegistryURL || after.Redacted || parsed.OtherHash != after.OtherHash {
		return errors.New("NODE_REGISTRY_OTHER_CONFIG_CHANGED")
	}
	candidateInfo, err := os.Lstat(candidate)
	if err != nil || !candidateInfo.Mode().IsRegular() || candidateInfo.Size() > 128<<10 {
		return errors.New("NODE_REGISTRY_CONFIG_REJECTED")
	}
	hash, err := fileSHA(candidate)
	if err != nil {
		return err
	}
	mode := file.Mode
	if !file.Exists {
		mode = 0600
	}
	marker := nodeRegistryMarker{req.JobID, req.InputHash, n.RegistryURL, file.Path, file.Hash, hash, parsed.OtherHash, mode}
	if err = atomicJSON(filepath.Join(dir, "node-registry.json"), marker); err != nil {
		return err
	}
	args = []string{"-u", n.Context.Account, "--", "/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "HOME=" + account.HomeDir, "LC_ALL=C", "/usr/bin/bash", "--noprofile", "--norc", "-c", nodeTerminalScript, "sfops-node-registry", file.Path, file.Hash, hash, strconv.FormatUint(uint64(mode), 8), candidate, path.Join(n.Context.RootPath, "backups/npm-registry", req.JobID), n.Context.AccountUID}
	if _, err = x.softwareRunner(ctx, "/usr/sbin/runuser", args...); err != nil {
		return errors.New("NODE_REGISTRY_RESULT_UNCONFIRMED")
	}
	return nil
}
func (x *executor) nodeRegistryApplied(ctx context.Context, req Request, dir string, obs *NodeEnvironmentObservation) bool {
	var marker nodeRegistryMarker
	n := req.NodeEnvironment
	if readJSON(filepath.Join(dir, "node-registry.json"), &marker) != nil || marker.JobID != req.JobID || marker.InputHash != req.InputHash || marker.RegistryURL != n.RegistryURL {
		return false
	}
	r := obs.Registry
	if r == nil || r.Validate(n.Context) != nil || r.Status != "CONFIGURED" || r.URL != n.RegistryURL || r.ConfigPath != marker.Path || r.ConfigSHA256 != marker.AfterHash || r.Mode != marker.Mode {
		return false
	}
	v := nodeRegistryVersion(obs, n.NodeVersion)
	if v == nil {
		return false
	}
	parsed, err := x.inspectNodeRegistry(ctx, n.Context, *v, marker.Path)
	return err == nil && parsed.OtherHash == marker.OtherHash
}
