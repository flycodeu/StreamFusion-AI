package deploy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func Digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func stamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func atomicJSON(filename string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(filename), ".record-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		os.Remove(tmp)
		return e
	}
	if e = os.Rename(tmp, filename); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(filename))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func readJSON(filename string, v any) error {
	b, e := os.ReadFile(filename)
	if e != nil {
		return e
	}
	if len(b) > 128<<10 {
		return errors.New("RECORD_TOO_LARGE")
	}
	return StrictJSON(b, v)
}

// InitTarget is an offline, explicit administrator command. No HTTP request
// installs executables or changes sudo policy.
func InitTarget(root, controller, target, socket, dockerBinary string) error {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return errors.New("LINUX_ROOT_REQUIRED")
	}
	if !ValidRoot(root) || !ID.MatchString(controller) || !ID.MatchString(target) || !filepath.IsAbs(socket) || filepath.Clean(socket) != socket {
		return errors.New("TARGET_IDENTITY_OR_PATH_INVALID")
	}
	if e := secureAncestors(filepath.Dir(root)); e != nil {
		return e
	}
	if e := secureAncestors(dockerBinary); e != nil {
		return e
	}
	if _, e := os.Lstat(root); !os.IsNotExist(e) {
		return errors.New("TARGET_ROOT_EXISTS")
	}
	if e := os.Mkdir(root, 0755); e != nil {
		return e
	}
	for _, d := range []string{"jobs", "instances", "packages"} {
		if e := os.Mkdir(filepath.Join(root, d), 0700); e != nil {
			return e
		}
	}
	source, e := os.Executable()
	if e != nil {
		return e
	}
	src, e := os.Open(source)
	if e != nil {
		return e
	}
	defer src.Close()
	dst, e := os.OpenFile(filepath.Join(root, "executor"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0755)
	if e != nil {
		return e
	}
	_, e = io.Copy(dst, src)
	if e == nil {
		e = dst.Sync()
	}
	ce := dst.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return atomicJSON(filepath.Join(root, "owner.json"), Owner{1, Version, controller, target, socket, dockerBinary})
}

type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.Len()
	if remaining > 0 {
		keep := n
		if keep > remaining {
			keep = remaining
		}
		b.Buffer.Write(p[:keep])
	}
	return n, nil
}

type executor struct {
	root    string
	owner   Owner
	secrets map[string]string
}

func (x *executor) docker(args ...string) ([]byte, error) {
	limit := 45 * time.Second
	if len(args) > 0 && args[0] == "pull" {
		limit = 8 * time.Minute
	}
	if len(args) > 1 && args[0] == "image" && args[1] == "load" {
		limit = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	if e := secureAncestors(x.owner.DockerBinary); e != nil {
		return nil, e
	}
	cmd := exec.CommandContext(ctx, x.owner.DockerBinary, append([]string{"--host", "unix://" + x.owner.DockerSocket}, args...)...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/root", "DOCKER_CONFIG=" + filepath.Join(x.root, "docker-config")}
	out := &cappedBuffer{limit: 64 << 10}
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	return out.Bytes(), err
}
func (x *executor) compose(r Release) (string, error) {
	if r.Config.ZLM != nil {
		return x.zlmCompose(r)
	}
	if r.Config.Preview != nil {
		return x.previewCompose(r)
	}
	dir := filepath.Join(x.root, "instances", r.InstanceID, "releases", r.ID)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	name := "sfops-" + strings.ReplaceAll(r.InstanceID, "-", "")
	dataDir := filepath.Join(x.root, "instances", r.InstanceID, "data")
	if e := os.Mkdir(dataDir, 0750); e == nil {
		if e = os.Chown(dataDir, 10001, 10001); e != nil {
			return "", e
		}
	} else if !os.IsExist(e) {
		return "", e
	}
	info, e := os.Lstat(dataDir)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("INSTANCE_DATA_PATH_UNSAFE")
	}
	confHash := Digest(r.Config)
	service := map[string]any{"image": r.ImageID, "pull_policy": "never", "container_name": name, "entrypoint": []string{"java", "-jar", "/app/app.jar"}, "command": []string{}, "user": "10001:10001", "init": true, "read_only": true, "tmpfs": []string{"/tmp:rw,noexec,nosuid,size=64m"}, "cap_drop": []string{"ALL"}, "security_opt": []string{"no-new-privileges:true"}, "restart": "unless-stopped", "mem_limit": strconv.Itoa(r.Config.MemoryMiB) + "m", "cpus": 1.0, "pids_limit": 256, "environment": map[string]string{"SERVER_PORT": strconv.Itoa(r.Config.ContainerPort), "SPRING_PROFILES_ACTIVE": r.Config.Profile}, "ports": []string{fmt.Sprintf("%s:%d:%d", r.Config.BindAddress, r.Config.HostPort, r.Config.ContainerPort)}, "labels": map[string]string{"com.streamfusion.ops.controller": x.owner.ControllerID, "com.streamfusion.ops.instance": r.InstanceID, "com.streamfusion.ops.release": r.ID, "com.streamfusion.ops.config": confHash}, "logging": map[string]any{"driver": "json-file", "options": map[string]string{"max-size": "10m", "max-file": "3"}}}
	env := service["environment"].(map[string]string)
	env["SERVER_ADDRESS"] = "0.0.0.0"
	env["SF_API_WORK_DIR"] = "/var/lib/streamfusion"
	service["volumes"] = []map[string]any{{"type": "bind", "source": dataDir, "target": "/var/lib/streamfusion"}}
	for _, b := range r.Bindings {
		if b.Validate() != nil {
			return "", errors.New("BINDING_INVALID")
		}
		secret := x.secrets[b.ID]
		if b.CredentialID != "" && secret == "" {
			return "", errors.New("CREDENTIAL_UNAVAILABLE")
		}
		if b.Kind == "MYSQL" {
			env["SPRING_DATASOURCE_URL"] = fmt.Sprintf("jdbc:mysql://%s:%d/%s", b.Host, b.Port, b.Database)
			env["SPRING_DATASOURCE_USERNAME"] = b.Username
			env["SPRING_DATASOURCE_PASSWORD"] = secret
		} else {
			env["SPRING_DATA_REDIS_HOST"] = b.Host
			env["SPRING_DATA_REDIS_PORT"] = strconv.Itoa(b.Port)
			env["SPRING_DATA_REDIS_USERNAME"] = b.Username
			env["SPRING_DATA_REDIS_PASSWORD"] = secret
			if b.Database != "" {
				env["SPRING_DATA_REDIS_DATABASE"] = b.Database
			}
		}
	}
	if r.Config.InitialPasswordCredentialID != "" {
		secret := x.secrets[r.Config.InitialPasswordCredentialID+":"+r.Config.InitialPasswordRevision]
		if secret == "" {
			return "", errors.New("INITIAL_PASSWORD_UNAVAILABLE")
		}
		env["SF_AUTH_INITIAL_PASSWORD"] = secret
	}
	for k, v := range env {
		env[k] = strings.ReplaceAll(v, "$", "$$")
	}
	filename := filepath.Join(dir, "compose.json")
	if _, e := os.Stat(filename); e == nil {
		var old any
		if e = readJSON(filename, &old); e != nil {
			return "", e
		}
		b, _ := json.Marshal(map[string]any{"services": map[string]any{"app": service}})
		existing, _ := os.ReadFile(filename)
		if !bytes.Equal(existing, b) {
			return "", errors.New("RELEASE_MATERIAL_CONFLICT")
		}
		return filename, nil
	}
	if e := atomicJSON(filename, map[string]any{"services": map[string]any{"app": service}}); e != nil {
		return "", e
	}
	return filename, nil
}
func (x *executor) inspected(r Release) (Result, error) {
	out, e := x.docker("container", "inspect", "sfops-"+strings.ReplaceAll(r.InstanceID, "-", ""))
	if e != nil {
		return Result{}, errors.New("CONTAINER_NOT_RUNNING")
	}
	var rows []struct {
		ID     string
		Image  string
		Config struct{ Labels map[string]string }
		State  struct{ Running bool }
	}
	if json.Unmarshal(out, &rows) != nil || len(rows) != 1 {
		return Result{}, errors.New("INSPECT_INVALID")
	}
	v := rows[0]
	l := v.Config.Labels
	if l["com.streamfusion.ops.controller"] != x.owner.ControllerID || l["com.streamfusion.ops.instance"] != r.InstanceID {
		return Result{}, errors.New("CONTAINER_NOT_OWNED")
	}
	if l["com.streamfusion.ops.release"] != r.ID || l["com.streamfusion.ops.config"] != Digest(r.Config) || v.Image != r.ImageID || !v.State.Running {
		return Result{}, errors.New("ACTUAL_RELEASE_MISMATCH")
	}
	return Result{ReleaseID: r.ID, ImageID: v.Image, ConfigHash: Digest(r.Config), ContainerID: v.ID, Health: "PROCESS_RUNNING", ObservedAt: stamp()}, nil
}
func (x *executor) healthy(r Release) (Result, error) {
	if r.Config.ZLM != nil {
		return x.zlmObserved(r)
	}
	if r.Config.Preview != nil {
		return x.previewHealth(r)
	}
	end := time.Now().Add(time.Duration(r.Config.HealthSeconds) * time.Second)
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	for time.Now().Before(end) {
		actual, e := x.inspected(r)
		if e == nil {
			url := fmt.Sprintf("http://%s:%d%s", r.Config.BindAddress, r.Config.HostPort, r.Config.HealthPath)
			resp, e := client.Get(url)
			if e == nil {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 8193))
				resp.Body.Close()
				var health struct{ Status string }
				if resp.StatusCode == 200 && len(body) <= 8192 && json.Unmarshal(body, &health) == nil && health.Status == "UP" {
					actual.Health = "APPLICATION_READY"
					return actual, nil
				}
			}
		}
		time.Sleep(time.Second)
	}
	return Result{}, errors.New("APPLICATION_HEALTH_FAILED")
}
func (x *executor) activate(r Release) error {
	file, e := x.compose(r)
	if e != nil {
		return e
	}
	_, e = x.docker("compose", "--project-name", "sfops-"+strings.ReplaceAll(r.InstanceID, "-", ""), "-f", file, "up", "-d", "--pull", "never", "--no-build", "--force-recreate")
	if e != nil {
		return errors.New("COMPOSE_ACTIVATE_FAILED")
	}
	return nil
}
func (x *executor) stopOwned(r Release) error {
	out, e := x.docker("container", "inspect", "sfops-"+strings.ReplaceAll(r.InstanceID, "-", ""))
	if e != nil {
		return errors.New("OWNERSHIP_INSPECT_FAILED")
	}
	var rows []struct {
		Config struct{ Labels map[string]string }
	}
	if json.Unmarshal(out, &rows) != nil || len(rows) != 1 || rows[0].Config.Labels["com.streamfusion.ops.controller"] != x.owner.ControllerID || rows[0].Config.Labels["com.streamfusion.ops.instance"] != r.InstanceID {
		return errors.New("CONTAINER_NOT_OWNED")
	}
	_, e = x.docker("rm", "-f", "sfops-"+strings.ReplaceAll(r.InstanceID, "-", ""))
	return e
}
func validateRelease(r *Release) error {
	if r == nil || !ID.MatchString(r.ID) || !ID.MatchString(r.InstanceID) || !ID.MatchString(r.ArtifactID) || !Hex.MatchString(r.ArchiveSHA256) || r.ByteSize < 1 || r.ByteSize > ArchiveLimitFor(r.Config.Recipe()) || !strings.HasPrefix(r.ImageID, "sha256:") || !Hex.MatchString(strings.TrimPrefix(r.ImageID, "sha256:")) {
		return errors.New("RELEASE_INVALID")
	}
	if len(r.Bindings) > 2 {
		return errors.New("BINDING_LIMIT")
	}
	if environment := r.RuntimeEnvironment; environment != nil {
		if r.Config.Recipe() != "java-compose-v1" || !ID.MatchString(environment.InstallationID) || !ID.MatchString(environment.TargetID) || environment.PluginVersion != "21-jre" || environment.RecipeRevision != "java-runtime-v1" || !Hex.MatchString(strings.TrimPrefix(environment.ImageID, "sha256:")) || !Hex.MatchString(strings.TrimPrefix(environment.ImageDigest, "sha256:")) {
			return errors.New("RUNTIME_ENVIRONMENT_INVALID")
		}
	}
	if (r.Config.Preview != nil || r.Config.ZLM != nil) && len(r.Bindings) != 0 {
		return errors.New("PREVIEW_BINDING_FORBIDDEN")
	}
	kinds := map[string]bool{}
	for _, b := range r.Bindings {
		if b.Validate() != nil || kinds[b.Kind] {
			return errors.New("BINDING_INVALID_OR_DUPLICATE_KIND")
		}
		kinds[b.Kind] = true
	}
	return r.Config.Validate()
}

// Execute reads a bounded JSON envelope followed by the declared archive.
// Lost replies never imply a safe retry: intent and pending survive process exit.
func Execute(input io.Reader, output io.Writer) error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		return errors.New("LINUX_EXECUTOR_REQUIRED")
	}
	bin, e := os.Executable()
	if e != nil {
		return e
	}
	root := filepath.Dir(bin)
	if !ValidRoot(root) || filepath.Base(bin) != "executor" {
		return errors.New("EXECUTOR_INSTALL_REQUIRED")
	}
	if e = secureAncestors(bin); e != nil {
		return e
	}
	x := executor{root: root}
	if e = readJSON(filepath.Join(root, "owner.json"), &x.owner); e != nil {
		return e
	}
	br := bufio.NewReaderSize(io.LimitReader(input, PreviewArchiveLimit+(128<<10)), 64<<10)
	line, e := br.ReadSlice('\n')
	if e != nil || len(line) > 64<<10 {
		return errors.New("ENVELOPE_INVALID")
	}
	var req Request
	if e = StrictJSON([]byte(line), &req); e != nil {
		return e
	}
	x.secrets = req.Secrets
	req.Secrets = nil
	if req.ProtocolVersion != 1 || req.ExecutorVersion != Version || req.ControllerID != x.owner.ControllerID || req.TargetID != x.owner.TargetID || x.owner.ExecutorVersion != Version {
		return errors.New("EXECUTOR_IDENTITY_MISMATCH")
	}
	if req.Action == "PROBE" {
		osRelease, e := os.ReadFile("/etc/os-release")
		if e != nil || !strings.Contains(string(osRelease), "ID=ubuntu\n") || !strings.Contains(string(osRelease), `VERSION_ID="24.04"`) {
			return errors.New("TARGET_PLATFORM_UNSUPPORTED")
		}
		v, e := x.docker("version", "--format", "{{.Server.Version}}")
		if e != nil {
			return errors.New("DOCKER_UNAVAILABLE")
		}
		c, e := x.docker("compose", "version", "--short")
		if e != nil {
			return errors.New("COMPOSE_UNAVAILABLE")
		}
		pendingJob := ""
		storageRoot, free, err := x.dockerStorage()
		if err != nil {
			return err
		}
		var pending Request
		if readJSON(filepath.Join(root, "pending.json"), &pending) == nil {
			pendingJob = pending.JobID
		} else if _, e := os.Lstat(filepath.Join(root, "pending.json")); !os.IsNotExist(e) {
			return errors.New("TARGET_PENDING_DAMAGED")
		}
		return json.NewEncoder(output).Encode(map[string]any{"owner": x.owner, "dockerVersion": strings.TrimSpace(string(v)), "composeVersion": strings.TrimSpace(string(c)), "platform": "linux/amd64", "pendingJobId": pendingJob, "dockerRootDir": storageRoot, "dockerAvailableBytes": free, "pluginCatalogHash": pluginCatalogHash(), "previewRecipe": PreviewRecipe, "zlmRecipe": ZLMRecipe, "javaJarRecipe": "java-jar-v1"})
	}
	if req.Action == "JAVA_RUNTIME_EXPORT" {
		return x.exportRuntime(req, output)
	}
	if req.Action == "BINDING_PROBE" {
		if req.Binding == nil || req.Binding.Validate() != nil {
			return errors.New("BINDING_INVALID")
		}
		value, err := x.bindingProbe(req)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(value)
	}
	if req.Action == "PREVIEW_REGISTRATION" {
		if validateRelease(req.Candidate) != nil || req.Candidate.Config.Preview == nil {
			return errors.New("PREVIEW_RELEASE_REQUIRED")
		}
		if _, e = x.inspected(*req.Candidate); e != nil {
			return e
		}
		value, err := x.previewRegistration(*req.Candidate)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(value)
	}
	if req.Action == "PREVIEW_HANDOFF" {
		if validateRelease(req.Candidate) != nil || req.Candidate.Config.Preview == nil {
			return errors.New("PREVIEW_RELEASE_REQUIRED")
		}
		lock, err := x.handoffLock()
		if err != nil {
			return err
		}
		defer lock.Close()
		if _, e = x.inspected(*req.Candidate); e != nil {
			return e
		}
		value, err := x.previewHandoff(*req.Candidate)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(value)
	}
	if req.Action == "ZLM_HANDOFF" {
		if validateRelease(req.Candidate) != nil || req.Candidate.Config.ZLM == nil {
			return errors.New("ZLM_RELEASE_REQUIRED")
		}
		lock, err := x.handoffLock()
		if err != nil {
			return err
		}
		defer lock.Close()
		actual, err := x.zlmObserved(*req.Candidate)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(actual)
	}
	if req.Action == "INSPECT" || req.Action == "LOGS" {
		if e = validateRelease(req.Candidate); e != nil {
			return e
		}
		actual, e := x.inspected(*req.Candidate)
		if e != nil {
			return e
		}
		if req.Action == "INSPECT" {
			if req.Candidate.Config.ZLM != nil {
				actual, e = x.zlmObserved(*req.Candidate)
				if e != nil {
					return e
				}
			} else if req.Candidate.Config.Preview != nil {
				actual, e = x.previewObserved(*req.Candidate)
				if e != nil {
					return e
				}
			}
			return json.NewEncoder(output).Encode(actual)
		}
		out, e := x.docker("logs", "--timestamps", "--tail", "200", actual.ContainerID)
		if e != nil {
			return errors.New("LOG_READ_FAILED")
		}
		text := string(out)
		for _, secret := range x.secrets {
			if secret != "" {
				text = strings.ReplaceAll(text, secret, "[REDACTED]")
			}
		}
		if len(text) > 24000 {
			text = text[len(text)-24000:]
		}
		return json.NewEncoder(output).Encode(map[string]any{"text": text, "limited": true})
	}
	if req.Action == "PROGRESS" {
		if !ID.MatchString(req.JobID) || !Hex.MatchString(req.InputHash) {
			return errors.New("JOB_ID_INVALID")
		}
		var progress Result
		if e = readJSON(filepath.Join(root, "jobs", req.JobID, "progress.json"), &progress); e != nil {
			return e
		}
		if progress.JobID != req.JobID || progress.InputHash != req.InputHash {
			return errors.New("PROGRESS_IDENTITY_MISMATCH")
		}
		return json.NewEncoder(output).Encode(progress)
	}
	if req.Action == "PLUGIN_VERIFY" {
		def, err := requestPluginDefinition(req)
		if err != nil {
			return err
		}
		if !ID.MatchString(req.PluginInstallationID) {
			return errors.New("PLUGIN_ID_INVALID")
		}
		result, err := x.probePlugin(def)
		if err != nil {
			result.Status, result.ErrorCode = "FAILED", err.Error()
		}
		return json.NewEncoder(output).Encode(result)
	}
	if req.Action == "PLUGIN_LOGS" {
		text, err := x.pluginLogs(req)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(map[string]any{"text": text, "limited": true})
	}
	if req.Action == "PLUGIN_INSTALL" || req.Action == "PLUGIN_REMOVE" || (req.Plugin != nil && (req.Action == "STATUS" || req.Action == "ACK")) {
		return x.executePlugin(req, output)
	}
	if !ID.MatchString(req.JobID) || !Hex.MatchString(req.InputHash) {
		return errors.New("JOB_IDENTITY_INVALID")
	}
	lock, e := targetLock(root)
	if e != nil {
		return e
	}
	defer lock.Close()
	dir := filepath.Join(root, "jobs", req.JobID)
	resultFile := filepath.Join(dir, "result.json")
	pendingFile := filepath.Join(root, "pending.json")
	var intent Request
	if req.Action == "STATUS" || req.Action == "ACK" || req.Action == "RECOVER" {
		if e = readJSON(filepath.Join(dir, "intent.json"), &intent); e != nil {
			if os.IsNotExist(e) && (req.Action == "STATUS" || req.Action == "RECOVER") {
				if _, pe := os.Lstat(pendingFile); os.IsNotExist(pe) {
					return x.recordNotStarted(req, dir, output)
				}
			}
			var pending Request
			if readJSON(pendingFile, &pending) != nil || pending.JobID != req.JobID || pending.InputHash != req.InputHash {
				return errors.New("TARGET_INTENT_MISMATCH")
			}
			intent = pending
		}
		if intent.InputHash != req.InputHash {
			return errors.New("TARGET_INTENT_MISMATCH")
		}
		var res Result
		if e = readJSON(resultFile, &res); e != nil {
			if e = readJSON(filepath.Join(dir, "progress.json"), &res); e != nil {
				res = Result{ProtocolVersion: 1, ExecutorVersion: Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "UNKNOWN", Phase: "INTENT_RECORDED", ObservedAt: stamp()}
			}
		}
		if req.Action == "STATUS" {
			if res.Status == "SUCCEEDED" {
				if _, e = x.healthy(*intent.Candidate); e != nil {
					res.Status = "UNKNOWN"
					res.ErrorCode = "ACTUAL_STATE_DRIFT"
				}
			}
			if res.Status == "FAILED" && intent.Baseline != nil {
				if _, e = x.healthy(*intent.Baseline); e != nil {
					res.Status = "UNKNOWN"
					res.ErrorCode = "RECOVERY_STATE_DRIFT"
				}
			}
			if res.Status == "FAILED" && intent.Baseline == nil && x.verifyStopped(*intent.Candidate) != nil {
				res.Status = "UNKNOWN"
				res.ErrorCode = "RECOVERY_STATE_DRIFT"
			}
			return json.NewEncoder(output).Encode(res)
		}
		if req.Action == "ACK" {
			if res.Status != "SUCCEEDED" && res.Status != "FAILED" {
				return errors.New("RESULT_NOT_FINAL")
			}
			if Digest(res) != req.ResultHash {
				return errors.New("ACK_RESULT_MISMATCH")
			}
			var ack struct{ ResultHash string }
			if readJSON(filepath.Join(dir, "ack.json"), &ack) == nil {
				if ack.ResultHash != req.ResultHash {
					return errors.New("ACK_CONFLICT")
				}
				var pending Request
				if readJSON(pendingFile, &pending) == nil && pending.JobID == req.JobID && pending.InputHash == req.InputHash {
					if e = os.Remove(pendingFile); e != nil {
						return e
					}
				}
				res.Ack = true
				return json.NewEncoder(output).Encode(res)
			}
			var pending Request
			if readJSON(pendingFile, &pending) != nil || pending.JobID != req.JobID || pending.InputHash != req.InputHash {
				return errors.New("PENDING_IDENTITY_MISMATCH")
			}
			if res.Status == "SUCCEEDED" {
				_, e = x.healthy(*intent.Candidate)
			} else if intent.Baseline != nil {
				_, e = x.healthy(*intent.Baseline)
			} else {
				e = x.verifyStopped(*intent.Candidate)
			}
			if e != nil {
				return errors.New("ACK_ACTUAL_STATE_DRIFT")
			}
			if intent.Baseline != nil && intent.Candidate.Config.Preview != nil {
				current := *intent.Candidate
				if res.Status == "FAILED" {
					current = *intent.Baseline
				}
				if e = x.previewMaintain(intent, "FINISH", current); e != nil {
					return e
				}
			}
			if e = atomicJSON(filepath.Join(dir, "ack.json"), map[string]string{"ResultHash": req.ResultHash}); e != nil {
				return e
			}
			if e = os.Remove(pendingFile); e != nil {
				return e
			}
			parent, e := os.Open(root)
			if e != nil {
				return e
			}
			e = parent.Sync()
			parent.Close()
			if e != nil {
				return e
			}
			res.Ack = true
			return json.NewEncoder(output).Encode(res)
		}
		var pending Request
		if readJSON(pendingFile, &pending) != nil || pending.JobID != req.JobID || pending.InputHash != req.InputHash {
			return errors.New("RECOVERY_PENDING_MISMATCH")
		}
		return x.recover(intent, resultFile, output, "RECOVERY_REQUESTED")
	}
	if req.Action != "ACTIVATE" {
		return errors.New("ACTION_NOT_ALLOWED")
	}
	if e = validateRelease(req.Candidate); e != nil {
		return e
	}
	if req.Baseline != nil {
		if e = validateRelease(req.Baseline); e != nil {
			return e
		}
		if req.Baseline.InstanceID != req.Candidate.InstanceID || req.Baseline.Config.DataCompatibility != req.Candidate.Config.DataCompatibility {
			return errors.New("RECOVERY_COMPATIBILITY_MISMATCH")
		}
	}
	if e = ValidatePreviewTransition(req.Candidate, req.Baseline, req.MaintenancePermit); e != nil {
		return e
	}
	frozen := req
	frozen.InputHash = ""
	frozen.Secrets = nil
	frozen.Action = "ACTIVATE"
	if Digest(frozen) != req.InputHash {
		return errors.New("INPUT_HASH_MISMATCH")
	}
	if readJSON(filepath.Join(dir, "intent.json"), &intent) == nil {
		if intent.InputHash != req.InputHash {
			return errors.New("IDEMPOTENCY_CONFLICT")
		}
		var res Result
		if e = readJSON(resultFile, &res); e != nil {
			return errors.New("UNKNOWN_RECONCILE_REQUIRED")
		}
		return json.NewEncoder(output).Encode(res)
	}
	if _, e = os.Lstat(pendingFile); !os.IsNotExist(e) {
		return errors.New("TARGET_PENDING")
	}
	deadline, e := time.Parse(time.RFC3339, req.DeadlineAt)
	if e != nil || time.Now().After(deadline) {
		return errors.New("DEADLINE_EXCEEDED")
	}
	if req.Baseline != nil {
		if _, e = x.healthy(*req.Baseline); e != nil {
			return errors.New("BASELINE_NOT_HEALTHY")
		}
	} else {
		name := "sfops-" + strings.ReplaceAll(req.Candidate.InstanceID, "-", "")
		if out, e := x.docker("ps", "-a", "--filter", "name=^/"+name+"$", "--format", "{{.ID}}"); e != nil || len(bytes.TrimSpace(out)) > 0 {
			return errors.New("EXISTING_CONTAINER_CONFLICT")
		}
		if req.Candidate.Config.ZLM != nil {
			if e = zlmPorts(req.Candidate.Config); e != nil {
				return e
			}
		} else if req.Candidate.Config.Preview != nil {
			if e = previewPorts(req.Candidate.Config); e != nil {
				return e
			}
		} else {
			listener, e := net.Listen("tcp", net.JoinHostPort(req.Candidate.Config.BindAddress, strconv.Itoa(req.Candidate.Config.HostPort)))
			if e != nil {
				return errors.New("PORT_IN_USE")
			}
			listener.Close()
		}
	}
	if _, e = x.docker("compose", "version", "--short"); e != nil {
		return errors.New("COMPOSE_UNAVAILABLE")
	}
	if e = os.Mkdir(dir, 0700); e != nil {
		return e
	}
	if e = atomicJSON(pendingFile, req); e != nil {
		return e
	}
	if e = atomicJSON(filepath.Join(dir, "intent.json"), req); e != nil {
		return e
	}
	if e = x.checkpoint(req, "TRANSFER"); e != nil {
		return e
	}
	pkg := filepath.Join(root, "packages", req.Candidate.ArchiveSHA256+".tar")
	if _, e = os.Stat(pkg); os.IsNotExist(e) {
		partial := pkg + ".partial"
		dst, e := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		_, e = io.CopyN(dst, br, req.Candidate.ByteSize)
		if e == nil {
			e = dst.Sync()
		}
		dst.Close()
		if e != nil {
			return e
		}
		image, e := ValidateRecipeArchive(partial, req.Candidate.ArchiveSHA256, req.Candidate.ByteSize, req.Candidate.Config.Recipe())
		if e != nil || image != req.Candidate.ImageID {
			return errors.New("TARGET_ARCHIVE_INVALID")
		}
		if e = os.Rename(partial, pkg); e != nil {
			return e
		}
	} else {
		image, e := ValidateRecipeArchive(pkg, req.Candidate.ArchiveSHA256, req.Candidate.ByteSize, req.Candidate.Config.Recipe())
		if e != nil || image != req.Candidate.ImageID {
			return errors.New("TARGET_CACHE_INVALID")
		}
	}
	if e = x.checkpoint(req, "IMPORT"); e != nil {
		return e
	}
	tag, e := archiveTag(pkg)
	if e != nil {
		return x.recover(req, resultFile, output, "ARCHIVE_MANIFEST_INVALID")
	}
	if e = x.checkImageTag(tag, req.Candidate.ImageID); e != nil {
		return x.recover(req, resultFile, output, e.Error())
	}
	if _, e = x.docker("image", "load", "-i", pkg); e != nil {
		return x.recover(req, resultFile, output, "IMAGE_LOAD_FAILED")
	}
	if req.Baseline != nil && req.Candidate.Config.Preview != nil {
		if e = x.previewMaintain(req, "CLAIM", *req.Baseline); e != nil {
			return x.recover(req, resultFile, output, e.Error())
		}
	}
	if e = x.checkpoint(req, "ACTIVATE"); e != nil {
		return e
	}
	if e = x.activate(*req.Candidate); e == nil {
		if e = x.checkpoint(req, "HEALTH"); e != nil {
			return e
		}
		var actual Result
		actual, e = x.healthy(*req.Candidate)
		if e == nil && req.Baseline != nil && req.Candidate.Config.Preview != nil && (actual.Preview == nil || actual.Preview.State != "MAINTENANCE") {
			e = errors.New("PREVIEW_FENCE_NOT_PRESERVED")
		}
		if e == nil {
			actual.ProtocolVersion = 1
			actual.ExecutorVersion = Version
			actual.ControllerID = req.ControllerID
			actual.TargetID = req.TargetID
			actual.JobID = req.JobID
			actual.InputHash = req.InputHash
			actual.Status = "SUCCEEDED"
			actual.Phase = "HEALTH_VERIFIED"
			if e = atomicJSON(resultFile, actual); e != nil {
				return e
			}
			return json.NewEncoder(output).Encode(actual)
		}
	}
	return x.recover(req, resultFile, output, e.Error())
}

func (x *executor) checkImageTag(tag, imageID string) error {
	out, err := x.docker("image", "inspect", "--format", "{{.Id}}", tag)
	if err == nil {
		if strings.TrimSpace(string(out)) != imageID {
			return errors.New("IMAGE_TAG_COLLISION")
		}
		return nil
	}
	if strings.Contains(string(out), "No such image") || strings.Contains(string(out), "No such object") {
		return nil
	}
	return errors.New("IMAGE_TAG_INSPECT_FAILED")
}
func (x *executor) recover(req Request, resultFile string, output io.Writer, reason string) error {
	if e := x.checkpoint(req, "RECOVERY"); e != nil {
		return e
	}
	res := Result{ProtocolVersion: 1, ExecutorVersion: Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "UNKNOWN", Phase: "RECOVERY_REQUIRED", ErrorCode: reason, ObservedAt: stamp()}
	if req.Baseline != nil {
		var activateError error
		previewClaimed := req.Candidate.Config.Preview != nil && x.previewClaimed(req)
		if previewClaimed {
			activateError = x.recordPreviewClaim(req)
		}
		if activateError == nil && (req.Candidate.Config.Preview == nil || previewClaimed) {
			activateError = x.activate(*req.Baseline)
		}
		if activateError == nil {
			if actual, e := x.healthy(*req.Baseline); e == nil {
				res.ReleaseID = actual.ReleaseID
				res.ImageID = actual.ImageID
				res.ContainerID = actual.ContainerID
				res.ConfigHash = actual.ConfigHash
				res.Health = actual.Health
				res.Preview = actual.Preview
				res.Status = "FAILED"
				res.Phase = "BASELINE_RESTORED"
				if req.Candidate.Config.Preview != nil && !previewClaimed {
					res.Phase = "BASELINE_UNCHANGED"
				}
			}
		}
	} else {
		name := "sfops-" + strings.ReplaceAll(req.Candidate.InstanceID, "-", "")
		out, e := x.docker("ps", "-a", "--filter", "name=^/"+name+"$", "--format", "{{.ID}}")
		if e == nil {
			if len(bytes.TrimSpace(out)) == 0 || x.stopOwned(*req.Candidate) == nil {
				res.Status = "FAILED"
				res.Phase = "EMPTY_BASELINE_RESTORED"
				res.Health = "STOPPED"
			}
		}
	}
	if e := atomicJSON(resultFile, res); e != nil {
		return e
	}
	return json.NewEncoder(output).Encode(res)
}
func (x *executor) checkpoint(req Request, phase string) error {
	if req.Plugin != nil {
		if e := x.pluginLog(req, phase, ""); e != nil {
			return e
		}
	}
	return atomicJSON(filepath.Join(x.root, "jobs", req.JobID, "progress.json"), Result{ProtocolVersion: 1, ExecutorVersion: Version, ControllerID: req.ControllerID, TargetID: req.TargetID, JobID: req.JobID, InputHash: req.InputHash, Status: "UNKNOWN", Phase: phase, ObservedAt: stamp()})
}
