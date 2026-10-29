package controller

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const targetPackageLimit int64 = 256 << 20

func localTargetPlatformSupported(goos, goarch string) bool {
	return goos == "linux" && goarch == "amd64"
}

type targetPreparationPackage struct {
	Available   bool   `json:"available"`
	Code        string `json:"code"`
	Message     string `json:"message"`
	FileName    string `json:"fileName"`
	SHA256      string `json:"sha256"`
	ByteSize    int64  `json:"byteSize"`
	DownloadURL string `json:"downloadUrl"`
}

type targetPreparationCommands struct {
	Preview   string `json:"preview"`
	Apply     string `json:"apply"`
	Upgrade   string `json:"upgrade"`
	Authorize string `json:"authorize"`
}

type targetPreparationAuthorization struct {
	Scope   string `json:"scope"`
	Command string `json:"command"`
}

type targetPreparationStep struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Status  string `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type hostPreparation struct {
	HostID              string                         `json:"hostId"`
	ExpectedHostVersion string                         `json:"expectedHostVersion"`
	ControllerID        string                         `json:"controllerId"`
	TargetID            string                         `json:"targetId"`
	InstallRoot         string                         `json:"installRoot"`
	Account             string                         `json:"account"`
	Platform            string                         `json:"platform"`
	Package             targetPreparationPackage       `json:"package"`
	Commands            targetPreparationCommands      `json:"commands"`
	Authorization       targetPreparationAuthorization `json:"authorization"`
	Steps               []targetPreparationStep        `json:"steps"`
	RemoteJob           any                            `json:"remoteJob,omitempty"`
}

type targetPackageFile struct {
	Name string
	Path string
	Size int64
	Hash string
}

type verifiedTargetPackage struct {
	Binary targetPackageFile
	Script targetPackageFile
}

func targetReleaseDirectory() string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(executable), "release", "0.2.0")
}

func readTargetPackage(directory string) (verifiedTargetPackage, error) {
	var result verifiedTargetPackage
	info, err := os.Lstat(directory)
	if err != nil {
		return result, errors.New("TARGET_PACKAGE_UNAVAILABLE")
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return result, errors.New("TARGET_PACKAGE_UNSAFE")
	}
	manifestPath := filepath.Join(directory, "SHA256SUMS")
	info, err = os.Lstat(manifestPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return result, errors.New("TARGET_PACKAGE_MANIFEST_INVALID")
	}
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return result, errors.New("TARGET_PACKAGE_MANIFEST_INVALID")
	}
	hashes := map[string]string{}
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 || !validSHA256(fields[0]) {
			return result, errors.New("TARGET_PACKAGE_MANIFEST_INVALID")
		}
		name := strings.TrimPrefix(fields[1], "*")
		if _, exists := hashes[name]; exists {
			return result, errors.New("TARGET_PACKAGE_MANIFEST_INVALID")
		}
		hashes[name] = fields[0]
	}
	for _, spec := range []struct {
		name  string
		limit int64
		out   *targetPackageFile
	}{{"streamfusion-ops", targetPackageLimit, &result.Binary}, {"install-target.sh", 64 << 10, &result.Script}} {
		if !validSHA256(hashes[spec.name]) {
			return result, errors.New("TARGET_PACKAGE_MANIFEST_INVALID")
		}
		path := filepath.Join(directory, spec.name)
		file, e := os.Open(path)
		if e != nil {
			return result, errors.New("TARGET_PACKAGE_UNAVAILABLE")
		}
		info, e := os.Lstat(path)
		if e != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > spec.limit {
			file.Close()
			return result, errors.New("TARGET_PACKAGE_UNSAFE")
		}
		if spec.name == "streamfusion-ops" {
			var header [20]byte
			_, e = io.ReadFull(file, header[:])
			if e != nil || string(header[:4]) != "\x7fELF" || header[4] != 2 || header[5] != 1 || header[18] != 62 || header[19] != 0 {
				file.Close()
				return result, errors.New("TARGET_PACKAGE_PLATFORM_MISMATCH")
			}
			if _, e = file.Seek(0, 0); e != nil {
				file.Close()
				return result, errors.New("TARGET_PACKAGE_UNAVAILABLE")
			}
		}
		hash := sha256.New()
		n, e := io.Copy(hash, io.LimitReader(file, spec.limit+1))
		file.Close()
		if e != nil || n != info.Size() || hex.EncodeToString(hash.Sum(nil)) != hashes[spec.name] {
			return result, errors.New("TARGET_PACKAGE_CHECKSUM_MISMATCH")
		}
		*spec.out = targetPackageFile{Name: spec.name, Path: path, Size: n, Hash: hashes[spec.name]}
	}
	return result, nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			if c < 'a' || c > 'f' {
				return false
			}
		}
	}
	return true
}

func targetPackageMessage(code string) string {
	switch code {
	case "TARGET_PACKAGE_CHECKSUM_MISMATCH":
		return "Linux发行材料摘要不匹配，请重新构建并核对SHA256SUMS"
	case "TARGET_PACKAGE_PLATFORM_MISMATCH":
		return "目标准备材料不是Linux amd64程序，不能使用Windows二进制"
	case "TARGET_PACKAGE_MANIFEST_INVALID":
		return "Linux发行材料缺少有效SHA256SUMS，请使用完整发行目录"
	case "TARGET_PACKAGE_UNSAFE":
		return "Linux发行材料路径或大小不符合要求，请使用普通文件"
	default:
		return "Linux目标准备材料尚未生成，请先构建程序旁release/0.2.0发行目录"
	}
}

func preparationForHost(t target, controllerID, directory string) (hostPreparation, verifiedTargetPackage) {
	p := hostPreparation{HostID: t.ID, ExpectedHostVersion: t.Version, ControllerID: controllerID, TargetID: t.ID, InstallRoot: t.InstallRoot, Account: t.User, Platform: "linux/amd64"}
	if t.Kind == "LOCAL" && runtime.GOOS == "linux" {
		if current, err := user.Current(); err == nil && sshUserPattern.MatchString(current.Username) {
			p.Account = current.Username
		}
	}
	p.Package.FileName = "streamfusion-ops-target-linux-amd64-" + t.ID + ".tar.gz"
	material, err := readTargetPackage(directory)
	if err != nil {
		p.Package.Code, p.Package.Message = err.Error(), targetPackageMessage(err.Error())
	} else {
		p.Package.Available = true
		p.Package.Code, p.Package.Message = "TARGET_PACKAGE_READY", "Linux amd64执行器和初始化脚本已核对摘要"
		p.Package.SHA256, p.Package.ByteSize = material.Binary.Hash, material.Binary.Size
		p.Package.DownloadURL = api + "/hosts/" + t.ID + "/preparation/download?expectedVersion=" + t.Version
	}
	if t.Kind == "LOCAL" && !localTargetPlatformSupported(runtime.GOOS, runtime.GOARCH) {
		p.Package.Available = false
		p.Package.Code, p.Package.Message = "LINUX_TARGET_REQUIRED", "当前本机不是Linux amd64，请新增Linux amd64远程主机后准备安装"
		p.Package.DownloadURL = ""
	}
	p.Authorization = targetPreparationAuthorization{Scope: "仅允许指定受管执行器的target-exec入口", Command: "'" + t.InstallRoot + "/executor' target-exec"}
	if p.Account != "root" {
		p.Authorization.Command = "/usr/bin/sudo -n " + p.Authorization.Command
	}
	sha := p.Package.SHA256
	if sha == "" {
		sha = "REPLACE_WITH_VERIFIED_SHA256"
	}
	if sshUserPattern.MatchString(p.Account) {
		p.Commands.Preview = "bash ./install.sh --preview"
		prefix := "sudo "
		if p.Account == "root" {
			prefix = ""
		}
		p.Commands.Apply = "bash ./install.sh"
		p.Commands.Authorize = "bash ./install.sh"
		p.Commands.Upgrade = prefix + "./streamfusion-ops target-upgrade --binary \"$(realpath ./streamfusion-ops)\" --sha256 " + sha + " --root " + t.InstallRoot + " --controller-id " + controllerID + " --target-id " + t.ID
	}
	expired := true
	if expiry, e := time.Parse(time.RFC3339Nano, t.Capabilities.ExpiresAt); e == nil {
		expired = !expiry.After(time.Now())
	}
	appendStep := func(key, label string, fact hostCapability) {
		if expired || fact.Status == "" {
			fact = hostCapability{Status: "UNKNOWN", Code: "HOST_CHECK_REQUIRED", Message: "请重新检查主机能力，以确认当前状态"}
		}
		p.Steps = append(p.Steps, targetPreparationStep{Key: key, Label: label, Status: fact.Status, Code: fact.Code, Message: fact.Message})
	}
	appendStep("connection", "主机连接", t.Capabilities.Reachability)
	appendStep("readOnly", "只读观察", t.Capabilities.ReadOnly)
	executorFact, authorizationFact := t.Capabilities.Mutation, t.Capabilities.Mutation
	if strings.HasPrefix(t.Capabilities.Mutation.Code, "SUDO_") {
		executorFact = hostCapability{Status: "UNKNOWN", Code: "EXECUTOR_IDENTITY_UNCONFIRMED", Message: "受限授权未通过，尚不能验证执行器身份"}
	} else if t.Capabilities.Mutation.Status != "AVAILABLE" {
		authorizationFact = hostCapability{Status: "UNKNOWN", Code: "EXECUTOR_AUTHORIZATION_UNCONFIRMED", Message: "先准备匹配的执行器，再核对受限变更授权"}
	}
	appendStep("executor", "受管执行器", executorFact)
	appendStep("authorization", "受限变更授权", authorizationFact)
	appendStep("dockerEngine", "Docker Engine", t.Capabilities.Docker)
	appendStep("dockerCompose", "Docker Compose（应用发布）", t.Capabilities.DockerCompose)
	status := "UNAVAILABLE"
	if p.Package.Available {
		status = "AVAILABLE"
	}
	p.Steps = append(p.Steps, targetPreparationStep{Key: "package", Label: "Linux准备材料", Status: status, Code: p.Package.Code, Message: p.Package.Message})
	return p, material
}

func (h *HTTP) hostPreparation(w http.ResponseWriter, r *http.Request, id string) {
	t, err := h.store.readTarget(id)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	p, _ := preparationForHost(t, h.store.controllerID(), targetReleaseDirectory())
	p.RemoteJob = h.latestPreparationJob(id)
	h.success(w, r, p)
}

func (h *HTTP) downloadHostPreparation(w http.ResponseWriter, r *http.Request, id string) {
	t, err := h.store.readTarget(id)
	if err != nil {
		h.opsError(w, r, err)
		return
	}
	if r.URL.Query().Get("expectedVersion") != t.Version {
		h.fail(w, r, 409, "VERSION_CONFLICT", "主机连接已变化或缺少版本，请重新打开安装准备")
		return
	}
	p, material := preparationForHost(t, h.store.controllerID(), targetReleaseDirectory())
	if !p.Package.Available {
		h.fail(w, r, 409, p.Package.Code, p.Package.Message)
		return
	}
	archive, err := createTargetPreparationArchive(p, material)
	if err != nil {
		h.fail(w, r, 409, "TARGET_PACKAGE_CHANGED", "准备材料在下载期间发生变化，请重新构建或重试")
		return
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	info, err := archive.Stat()
	if err != nil {
		h.internal(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+p.Package.FileName+"\"")
	http.ServeContent(w, r, p.Package.FileName, info.ModTime(), archive)
}

func createTargetPreparationArchive(p hostPreparation, material verifiedTargetPackage) (_ *os.File, err error) {
	file, err := os.CreateTemp("", "sfops-target-preparation-*.tar.gz")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			file.Close()
			os.Remove(file.Name())
		}
	}()
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	for _, item := range []targetPackageFile{material.Binary} {
		source, e := os.Open(item.Path)
		if e != nil {
			return nil, e
		}
		info, e := os.Lstat(item.Path)
		if e != nil || !info.Mode().IsRegular() || info.Size() != item.Size {
			source.Close()
			return nil, errors.New("TARGET_PACKAGE_CHANGED")
		}
		if e = tw.WriteHeader(&tar.Header{Name: item.Name, Mode: 0755, Size: item.Size}); e != nil {
			source.Close()
			return nil, e
		}
		hash := sha256.New()
		n, e := io.Copy(io.MultiWriter(tw, hash), io.LimitReader(source, item.Size+1))
		source.Close()
		if e != nil || n != item.Size || hex.EncodeToString(hash.Sum(nil)) != item.Hash {
			return nil, errors.New("TARGET_PACKAGE_CHANGED")
		}
	}
	script, err := os.Open(material.Script.Path)
	if err != nil {
		return nil, err
	}
	helper, err := io.ReadAll(io.LimitReader(script, (64<<10)+1))
	script.Close()
	if err != nil || int64(len(helper)) != material.Script.Size || fmt.Sprintf("%x", sha256.Sum256(helper)) != material.Script.Hash {
		return nil, errors.New("TARGET_PACKAGE_CHANGED")
	}
	readme, installer := targetSetupReadme(p), targetInstaller(p, string(helper))
	checksums := fmt.Sprintf("%s  streamfusion-ops\n%x  install.sh\n%x  HOST-SETUP.md\n", material.Binary.Hash, sha256.Sum256([]byte(installer)), sha256.Sum256([]byte(readme)))
	for _, item := range []struct{ name, content string }{{"install.sh", installer}, {"HOST-SETUP.md", readme}, {"SHA256SUMS", checksums}} {
		mode := int64(0644)
		if item.name == "install.sh" {
			mode = 0755
		}
		if err = tw.WriteHeader(&tar.Header{Name: item.name, Mode: mode, Size: int64(len(item.content))}); err != nil {
			return nil, err
		}
		if _, err = io.WriteString(tw, item.content); err != nil {
			return nil, err
		}
	}
	if err = tw.Close(); err != nil {
		return nil, err
	}
	if err = gz.Close(); err != nil {
		return nil, err
	}
	_, err = file.Seek(0, 0)
	return file, err
}
