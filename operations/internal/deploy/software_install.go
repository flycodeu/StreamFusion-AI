package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

const serviceLabel = "com.streamfusion.ops."

type serviceCreation struct {
	SoftwareID  string `json:"softwareId"`
	JobID       string `json:"jobId"`
	ConfigHash  string `json:"configHash"`
	ContainerID string `json:"containerId"`
}

func serviceName(id string) string { return "sfops-service-" + id }

func serviceLabels(o *SoftwareOwnership) map[string]string {
	return map[string]string{
		serviceLabel + "controller": o.ControllerID, serviceLabel + "target": o.TargetID,
		serviceLabel + "software": o.SoftwareID, serviceLabel + "job": o.JobID,
		serviceLabel + "config": o.ConfigHash, serviceLabel + "service": "1",
	}
}

func serviceOwnershipMatches(s SoftwareRequest, name, image string, labels map[string]string) bool {
	o := s.Ownership
	if o == nil || s.Validate(true) != nil || o.SoftwareID != s.ID || name != "/"+serviceName(s.ID) || image != o.ImageID {
		return false
	}
	expected := serviceLabels(o)
	for key, value := range expected {
		if labels[key] != value {
			return false
		}
	}
	for key := range labels {
		if (strings.HasPrefix(key, serviceLabel) || strings.HasPrefix(key, "sfops.") || strings.HasPrefix(key, "streamfusion.ops.")) && expected[key] == "" {
			return false
		}
	}
	return true
}

func (x *executor) installOwnership(req Request) *SoftwareOwnership {
	i := req.Installation
	return &SoftwareOwnership{ControllerID: req.ControllerID, TargetID: req.TargetID, SoftwareID: i.SoftwareID, JobID: req.JobID, ConfigHash: Digest(i.Config), ImageID: i.Definition.ImageID}
}

func (x *executor) serviceInstallPreflight(i serviceinstall.Request) serviceinstall.Preflight {
	if i.Config.Mode() == "NATIVE" {
		return x.nativeInstallPreflight(i)
	}
	root := serviceinstall.InstallRoot(x.root, i.SoftwareID, i.Config)
	p := serviceinstall.Preflight{Ready: true, Root: root, DataDirectory: filepath.Join(root, "data"), ConfigDirectory: filepath.Join(root, "config"), Endpoint: net.JoinHostPort(i.Config.BindAddress, strconv.Itoa(i.Config.HostPort)), Checks: []serviceinstall.Check{}, PortMappings: serviceinstall.PortMappings(i.Definition, i.Config)}
	p.BinaryDirectory = ""
	p.LogsDirectory = ""
	check := func(key, code, message string) {
		status := "PASS"
		if code != "" {
			status, p.Ready = "FAIL", false
		}
		p.Checks = append(p.Checks, serviceinstall.Check{Key: key, Status: status, Code: code, Message: message})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, _, _, err := x.serviceSecrets(i); err != nil {
		check("CREDENTIAL", err.Error(), "凭据格式不满足所选固定配方要求，请修改凭据后重新检查")
	} else {
		check("CREDENTIAL", "", "凭据引用与配方格式有效，不回显秘密")
	}
	if _, err := x.softwareRunner(ctx, "/usr/bin/docker", "version", "--format", "{{.Server.Version}}"); err != nil {
		check("DOCKER", "DOCKER_NOT_READY", "Docker Engine不可访问")
	} else {
		check("DOCKER", "", "Docker Engine可访问，无需Compose")
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		check("DIRECTORY", "SERVICE_DATA_DIRECTORY_EXISTS", "软件专用目录已存在，请核对原安装记录")
	} else {
		check("DIRECTORY", "", "将创建独立配置与数据目录")
	}
	if out, err := x.softwareRunner(ctx, "/usr/bin/docker", "container", "ls", "-a", "--filter", "name=^/"+serviceName(i.SoftwareID)+"$", "--format", "{{.ID}}"); err != nil {
		check("CONTAINER", "SERVICE_CONTAINER_CHECK_UNAVAILABLE", "无法确认容器名称占用")
	} else if strings.TrimSpace(string(out)) != "" {
		check("CONTAINER", "SERVICE_CONTAINER_EXISTS", "同一安装身份的容器已经存在")
	} else {
		check("CONTAINER", "", "专用容器名称可用")
	}
	for _, mapping := range p.PortMappings {
		if err := availableServicePort(mapping); err != nil {
			check("PORT_"+mapping.Key, "SERVICE_PORT_UNAVAILABLE", "所选地址或"+mapping.Key+"端口已被占用")
		} else {
			check("PORT_"+mapping.Key, "", "端口当前可用，启动前将复检")
		}
		{
			if owner, err := x.servicePublishedPortFor(ctx, mapping.BindAddress, mapping.HostPort, mapping.Protocol); err != nil {
				check("PUBLISHED_PORT", "SERVICE_PUBLISHED_PORTS_UNKNOWN", "无法确认Docker发布端口")
			} else if owner != "" {
				check("PUBLISHED_PORT", "SERVICE_PORT_PUBLISHED", "端口已由"+owner+"发布")
			}
		}
	}
	free, err := availableBytes(existingServiceParent(filepath.Dir(root)))
	needed := uint64(512 << 20)
	if i.Source.Type == "ARCHIVE" {
		needed += uint64(i.Source.ByteSize) * 2
	} else {
		needed += 2 << 30
	}
	if err != nil {
		check("SPACE", "SERVICE_SPACE_UNKNOWN", "无法确认项目目录剩余空间")
	} else if free < needed {
		check("SPACE", "SERVICE_SPACE_INSUFFICIENT", "项目目录空间不足以暂存和初始化")
	} else {
		check("SPACE", "", "项目目录空间检查通过")
	}
	storage, err := x.softwareRunner(ctx, "/usr/bin/docker", "info", "--format", "{{.DockerRootDir}}")
	storageRoot := strings.TrimSpace(string(storage))
	if err != nil || !filepath.IsAbs(storageRoot) || filepath.Clean(storageRoot) != storageRoot {
		check("DOCKER_SPACE", "DOCKER_STORAGE_UNAVAILABLE", "无法确认Docker存储位置与空间")
	} else if dockerFree, statErr := availableBytes(storageRoot); statErr != nil {
		check("DOCKER_SPACE", "DOCKER_STORAGE_UNAVAILABLE", "无法读取Docker存储所在文件系统")
	} else if dockerFree < max(needed, 3<<30) {
		check("DOCKER_SPACE", "DOCKER_STORAGE_INSUFFICIENT", "Docker存储不足以加载镜像及初始化服务")
	} else {
		check("DOCKER_SPACE", "", "Docker存储所在文件系统空间检查通过")
	}
	return p
}

func (x *executor) serviceDocker(req Request, phase string, limit time.Duration, args ...string) ([]byte, error) {
	if err := x.checkpoint(req, phase); err != nil {
		return nil, err
	}
	if deadline, err := time.Parse(time.RFC3339, req.DeadlineAt); err == nil {
		limit = min(limit, time.Until(deadline))
	}
	if limit <= 0 {
		return nil, errors.New("DEADLINE_EXCEEDED")
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	out, err := x.softwareRunner(ctx, "/usr/bin/docker", args...)
	text := x.redactSoftwareSecrets(strings.TrimSpace(string(out)))
	if len(text) > 8<<10 {
		text = text[len(text)-(8<<10):]
	}
	x.appendServiceLog(req, phase, text)
	return out, err
}

func (x *executor) servicePublishedPort(ctx context.Context, bind string, port int) (string, error) {
	return x.servicePublishedPortFor(ctx, bind, port, "TCP")
}
func (x *executor) servicePublishedPortFor(ctx context.Context, bind string, port int, wantedProtocol string) (string, error) {
	out, err := x.softwareRunner(ctx, "/usr/bin/docker", "container", "ls", "--format", `{"id":{{json .ID}},"name":{{json .Names}},"ports":{{json .Ports}}}`)
	if err != nil || len(out) >= 64<<10 {
		return "", errors.New("SERVICE_PUBLISHED_PORTS_UNKNOWN")
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > 256 {
		return "", errors.New("SERVICE_PUBLISHED_PORTS_LIMIT")
	}
	for _, line := range lines {
		if line == "" {
			continue
		}
		var actual struct{ ID, Name, Ports string }
		if json.Unmarshal([]byte(line), &actual) != nil || actual.ID == "" || len(actual.Name) > 128 {
			return "", errors.New("SERVICE_PUBLISHED_PORTS_INVALID")
		}
		for _, mapping := range strings.Split(actual.Ports, ",") {
			socket, protocol, published := strings.Cut(strings.TrimSpace(mapping), "->")
			if !published || !strings.HasSuffix(protocol, "/"+strings.ToLower(wantedProtocol)) {
				continue
			}
			colon := strings.LastIndex(socket, ":")
			if colon < 1 {
				return "", errors.New("SERVICE_PUBLISHED_PORTS_INVALID")
			}
			ip := strings.Trim(socket[:colon], "[]")
			ports := strings.Split(socket[colon+1:], "-")
			low, parseErr := strconv.Atoi(ports[0])
			high := low
			if len(ports) == 2 {
				high, parseErr = strconv.Atoi(ports[1])
			}
			if parseErr != nil || len(ports) > 2 || low < 1 || high > 65535 || high < low || net.ParseIP(ip) == nil {
				return "", errors.New("SERVICE_PUBLISHED_PORTS_INVALID")
			}
			if port >= low && port <= high && (ip == bind || net.ParseIP(ip).IsUnspecified()) {
				return actual.Name, nil
			}
		}
	}
	return "", nil
}

func (x *executor) readServiceInstallation(req Request, output io.Writer) error {
	if err := validateSoftwareInput(req); err != nil {
		return err
	}
	dir := filepath.Join(x.root, "jobs", req.JobID)
	var intent Request
	if readJSON(filepath.Join(dir, "intent.json"), &intent) != nil || intent.JobID != req.JobID || intent.InputHash != req.InputHash {
		return errors.New("SERVICE_INSTALL_NOT_STARTED_OR_IDENTITY_MISMATCH")
	}
	if req.Action == "PROGRESS" {
		var progress Result
		if err := readJSON(filepath.Join(dir, "progress.json"), &progress); err != nil {
			return err
		}
		if progress.JobID != req.JobID || progress.InputHash != req.InputHash {
			return errors.New("PROGRESS_IDENTITY_MISMATCH")
		}
		return json.NewEncoder(output).Encode(progress)
	}
	f, err := os.Open(filepath.Join(dir, "service-install.log"))
	if err != nil {
		return errors.New("SERVICE_INSTALL_LOG_NOT_AVAILABLE")
	}
	defer f.Close()
	// The transport envelope is capped at 128KiB. A 16KiB text tail remains
	// below that cap even when every byte needs a six-byte JSON escape.
	const tailLimit int64 = 16 << 10
	info, err := f.Stat()
	if err != nil {
		return err
	}
	offset := max(int64(0), info.Size()-tailLimit)
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(f, tailLimit))
	if err != nil {
		return err
	}
	if offset > 0 {
		for len(data) > 0 && !utf8.RuneStart(data[0]) {
			data = data[1:]
		}
	}
	return json.NewEncoder(output).Encode(map[string]any{"text": x.redactSoftwareSecrets(string(data)), "limited": true, "source": "TARGET_EXECUTION"})
}

func (x *executor) appendServiceLog(req Request, phase, text string) {
	file := filepath.Join(x.root, "jobs", req.JobID, "service-install.log")
	info, err := os.Stat(file)
	if err == nil && info.Size() >= 128<<10 {
		return
	}
	f, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s phase=%s\n%s\n", stamp(), phase, x.redactSoftwareSecrets(text))
}

func (x *executor) serviceImage(req Request) error {
	i := req.Installation
	if i.Source.Type == "REGISTRY" {
		if _, err := x.serviceDocker(req, "SERVICE_IMAGE_PULL", 8*time.Minute, "pull", "--platform", "linux/amd64", i.Definition.Image+"@"+i.Definition.Digest); err != nil {
			return errors.New("SERVICE_IMAGE_PULL_FAILED")
		}
	} else {
		if x.installationInput == nil {
			return errors.New("SERVICE_ARCHIVE_REQUIRED")
		}
		packages := filepath.Join(x.root, "packages")
		if err := os.MkdirAll(packages, 0700); err != nil {
			return err
		}
		if err := secureAncestors(packages); err != nil {
			return err
		}
		partial := filepath.Join(x.root, "jobs", req.JobID, "service-image.partial")
		f, err := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return errors.New("SERVICE_ARCHIVE_TRANSFER_EXISTS")
		}
		_, err = io.CopyN(f, x.installationInput, i.Source.ByteSize)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return errors.New("SERVICE_ARCHIVE_TRANSFER_FAILED")
		}
		if _, err = serviceinstall.ValidateArchive(partial, i.Source.SHA256, i.Source.ByteSize, i.Definition); err != nil {
			return err
		}
		pkg := filepath.Join(packages, "service-"+i.Source.SHA256+".tar")
		if err = os.Rename(partial, pkg); err != nil {
			return err
		}
		if _, err = x.serviceDocker(req, "SERVICE_IMAGE_LOAD", 5*time.Minute, "image", "load", "--input", pkg); err != nil {
			return errors.New("SERVICE_IMAGE_LOAD_FAILED")
		}
	}
	out, err := x.serviceDocker(req, "SERVICE_IMAGE_VERIFY", 10*time.Second, "image", "inspect", "--format", `{"id":{{json .Id}},"os":{{json .Os}},"arch":{{json .Architecture}}}`, i.Definition.ImageID)
	var actual struct{ ID, OS, Arch string }
	if err != nil || json.Unmarshal(out, &actual) != nil || actual.ID != i.Definition.ImageID || actual.OS != "linux" || actual.Arch != "amd64" {
		return errors.New("SERVICE_IMAGE_IDENTITY_MISMATCH")
	}
	return nil
}

func (x *executor) installService(req Request, dir string) (Result, error) {
	// Custom storage can live outside the target root. Its metadata binding
	// still belongs to the protected executor, rather than the data directory.
	if err := os.MkdirAll(filepath.Dir(x.softwareFile(req.Installation.SoftwareID)), 0700); err != nil {
		return softwareResult(req), err
	}
	if req.Installation.Config.Mode() == "NATIVE" {
		return x.installNative(req, dir)
	}
	if req.Installation.Definition.Kind == "TOOL" {
		return x.installDockerTool(req, dir)
	}
	result := softwareResult(req)
	i := req.Installation
	preflight := x.serviceInstallPreflight(*i)
	for _, check := range preflight.Checks {
		if check.Status != "PASS" {
			return result, errors.New(check.Code)
		}
	}
	if err := x.serviceImage(req); err != nil {
		return result, err
	}
	args, err := x.prepareService(req, preflight.Root)
	if err != nil {
		return result, err
	}
	creation := serviceCreation{SoftwareID: i.SoftwareID, JobID: req.JobID, ConfigHash: Digest(i.Config)}
	if err = atomicJSON(filepath.Join(dir, "service-create.json"), creation); err != nil {
		return result, err
	}
	out, createErr := x.serviceDocker(req, "SERVICE_CREATE", 45*time.Second, args...)
	id := strings.TrimSpace(string(out))
	if createErr != nil || !Hex.MatchString(id) {
		// A lost reply may hide a created object. Resolve only its exact name
		// and labels; this path never repeats create or initializes data again.
		return x.reconcileServiceInstallation(req, dir), nil
	}
	creation.ContainerID = id
	if err = atomicJSON(filepath.Join(dir, "service-create.json"), creation); err != nil {
		return result, nil
	}
	s, actual, err := x.createdService(req, id)
	if err != nil {
		return result, nil
	}
	if err = atomicJSON(x.softwareFile(i.SoftwareID), softwareBinding{Software: s, JobID: req.JobID}); err != nil {
		return result, nil
	}
	if err = atomicJSON(filepath.Join(dir, "service-start.json"), creation); err != nil {
		return result, nil
	}
	_, startErr := x.serviceDocker(req, "SERVICE_INITIALIZING", 45*time.Second, "container", "start", "--", id)
	if startErr == nil {
		version, healthErr := x.waitServiceHealth(req, s, 2*time.Minute)
		if healthErr == nil {
			return x.serviceInstallResult(req, s, version, "READY", "SUCCEEDED"), nil
		}
	}
	// A confirmed owned container remains a managed object even if unhealthy.
	// Preserve its binding/identity so the operator can view logs and stop it.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	actual, err = ObserveSoftware(ctx, s, x.softwareRunner)
	if err != nil || actual.State == "UNKNOWN" {
		return result, nil
	}
	result = x.serviceInstallResult(req, s, "", "FAILED", "FAILED")
	result.ErrorCode = "SERVICE_HEALTH_FAILED"
	return result, nil
}

func (x *executor) createdService(req Request, locator string) (SoftwareRequest, *SoftwareObservation, error) {
	i := req.Installation
	s := SoftwareRequest{ID: i.SoftwareID, Version: i.BindingRevision, Kind: "DOCKER", Locator: locator, SoftwareKey: strings.ToUpper(i.Definition.Key), Ownership: x.installOwnership(req)}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
	if err != nil {
		return s, nil, err
	}
	s.Locator, s.IdentityHash = actual.Locator, actual.IdentityHash
	return s, actual, nil
}

func (x *executor) serviceInstallResult(req Request, s SoftwareRequest, version, health, status string) Result {
	result := softwareResult(req)
	i := req.Installation
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	actual, err := ObserveSoftware(ctx, s, x.softwareRunner)
	if err != nil || actual.State == "UNKNOWN" {
		return result
	}
	result.Status, result.Phase, result.Software = status, "SERVICE_INSTALLED", actual
	if version != "" && health == "READY" {
		actual.Version, actual.VersionSource, actual.VersionStatus = version, "INSTALL_PROBE", "VERIFIED_AT_INSTALL"
	}
	result.ImageID, result.ContainerID, result.ConfigHash = i.Definition.ImageID, actual.Locator, Digest(i.Config)
	result.Health, result.ObservedVersion = health, version
	result.Installation = &serviceinstall.Evidence{SoftwareID: i.SoftwareID, BindingRevision: i.BindingRevision, RecipeKey: i.Definition.Key, RecipeRevision: i.Definition.Revision, ConfigHash: result.ConfigHash, ImageID: result.ImageID, ContainerID: actual.Locator, IdentityHash: actual.IdentityHash, ObservedVersion: version, Health: health, ObservedAt: stamp(), Kind: "DOCKER", Locator: actual.Locator}
	if health != "READY" {
		result.Phase = "SERVICE_HEALTH_FAILED"
	}
	return result
}

func (x *executor) reconcileServiceInstallation(req Request, dir string) Result {
	if req.Installation.Config.Mode() == "NATIVE" {
		return x.reconcileNative(req, dir)
	}
	if req.Installation.Definition.Kind == "TOOL" {
		return x.reconcileDockerTool(req, dir)
	}
	unknown := softwareResult(req)
	unknown.ErrorCode = "SERVICE_INSTALL_RECONCILE_REQUIRED"
	var creation serviceCreation
	creationErr := readJSON(filepath.Join(dir, "service-create.json"), &creation)
	if os.IsNotExist(creationErr) {
		// The create boundary is durable before any long-lived container can
		// be created. A confirmed earlier checkpoint therefore permits a
		// terminal non-created receipt after transfer/configuration crashes.
		var progress Result
		allowed := map[string]bool{"SOFTWARE_PREFLIGHT": true, "SERVICE_IMAGE_PULL": true, "SERVICE_IMAGE_LOAD": true, "SERVICE_IMAGE_VERIFY": true, "SERVICE_CONFIGURE": true}
		_, bindingErr := os.Lstat(x.softwareFile(req.Installation.SoftwareID))
		if os.IsNotExist(bindingErr) && readJSON(filepath.Join(dir, "progress.json"), &progress) == nil && progress.JobID == req.JobID && progress.InputHash == req.InputHash && allowed[progress.Phase] {
			unknown.Status, unknown.Phase, unknown.ErrorCode = "FAILED", "SERVICE_NOT_CREATED", "SERVICE_INSTALL_INTERRUPTED_BEFORE_CREATE"
		}
		return unknown
	}
	if creationErr != nil || creation.SoftwareID != req.Installation.SoftwareID || creation.JobID != req.JobID || creation.ConfigHash != Digest(req.Installation.Config) {
		return unknown
	}
	locator := creation.ContainerID
	if locator == "" {
		locator = serviceName(creation.SoftwareID)
	}
	s, _, err := x.createdService(req, locator)
	if err != nil {
		return unknown
	}
	if err = os.MkdirAll(filepath.Dir(x.softwareFile(s.ID)), 0700); err != nil {
		return unknown
	}
	var existing softwareBinding
	if err = readJSON(x.softwareFile(s.ID), &existing); err == nil {
		if x.boundSoftware(s) != nil || existing.JobID != req.JobID {
			return unknown
		}
	} else if !os.IsNotExist(err) {
		return unknown
	} else if err = atomicJSON(x.softwareFile(s.ID), softwareBinding{Software: s, JobID: req.JobID}); err != nil {
		return unknown
	}
	version, healthErr := x.waitServiceHealth(req, s, 10*time.Second)
	if healthErr == nil {
		return x.serviceInstallResult(req, s, version, "READY", "SUCCEEDED")
	}
	// Recovery observes only; it does not replay a start boundary or rewrite
	// initial credentials. A known object can now be maintained explicitly.
	result := x.serviceInstallResult(req, s, "", "FAILED", "FAILED")
	if result.Status == "FAILED" {
		result.ErrorCode = "SERVICE_HEALTH_FAILED"
	}
	return result
}

func (x *executor) verifyServiceInstallation(req Request, result Result) error {
	if req.Installation.Config.Mode() == "NATIVE" || req.Installation.Definition.Kind == "TOOL" {
		return x.verifyOwnedInstallation(req, result)
	}
	if result.Installation == nil || result.Software == nil {
		return errors.New("SERVICE_INSTALL_RECEIPT_MISSING")
	}
	i, evidence := req.Installation, result.Installation
	if evidence.SoftwareID != i.SoftwareID || evidence.BindingRevision != i.BindingRevision || evidence.RecipeKey != i.Definition.Key || evidence.RecipeRevision != i.Definition.Revision || evidence.ConfigHash != Digest(i.Config) || evidence.ImageID != i.Definition.ImageID || evidence.ContainerID != result.Software.Locator || evidence.IdentityHash != result.Software.IdentityHash {
		return errors.New("SERVICE_INSTALL_RECEIPT_MISMATCH")
	}
	s := SoftwareRequest{ID: i.SoftwareID, Version: i.BindingRevision, Kind: "DOCKER", Locator: evidence.ContainerID, IdentityHash: evidence.IdentityHash, SoftwareKey: strings.ToUpper(i.Definition.Key), Ownership: x.installOwnership(req)}
	return x.boundSoftware(s)
}

func availableServicePort(p serviceinstall.PortMapping) error {
	endpoint := net.JoinHostPort(p.BindAddress, strconv.Itoa(p.HostPort))
	if p.Protocol == "UDP" {
		l, err := net.ListenPacket("udp", endpoint)
		if err != nil {
			return err
		}
		return l.Close()
	}
	l, err := net.Listen("tcp", endpoint)
	if err != nil {
		return err
	}
	return l.Close()
}
