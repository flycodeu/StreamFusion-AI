package deploy

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type PreviewObservation struct {
	ProtocolVersion int    `json:"protocolVersion"`
	NodeID          string `json:"nodeId"`
	BootID          string `json:"bootId"`
	ConfigVersion   string `json:"configVersion"`
	State           string `json:"state"`
	MaxSessions     int    `json:"maxSessions"`
	MaxTranscodes   int    `json:"maxTranscodes"`
}
type previewMaintenance struct {
	ProtocolVersion int    `json:"protocolVersion"`
	NodeID          string `json:"nodeId"`
	State           string `json:"state"`
	PermitID        string `json:"permitId"`
	BootID          string `json:"bootId"`
	ConfigVersion   string `json:"configVersion"`
	JobID           string `json:"jobId"`
	ExpiresAt       int64  `json:"expiresAt"`
}

func (x *executor) previewIdentity(r Release) string {
	return filepath.Join(x.root, "instances", r.InstanceID, "preview-identity")
}

func (x *executor) preparePreviewIdentity(r Release) error {
	dir := x.previewIdentity(r)
	if info, err := os.Lstat(dir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("PREVIEW_IDENTITY_UNSAFE")
		}
		for _, name := range []string{"machine-token", "tls-password", "zlm-secret", "identity.p12", "node.der", "connection.json"} {
			info, err = os.Lstat(filepath.Join(dir, name))
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
				return errors.New("PREVIEW_IDENTITY_DAMAGED")
			}
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return err
	}
	temp, err := os.MkdirTemp(filepath.Dir(dir), ".preview-identity-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	for _, name := range []string{"machine-token", "tls-password", "zlm-secret"} {
		secret := make([]byte, 32)
		if _, err = rand.Read(secret); err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(temp, name), []byte(hex.EncodeToString(secret)), 0600); err != nil {
			return err
		}
	}
	p := r.Config.Preview
	san := "IP:" + r.Config.BindAddress
	if net.ParseIP(p.ControlHost) != nil {
		san += ",IP:" + p.ControlHost
	} else {
		san += ",DNS:" + p.ControlHost
	}
	base := []string{"run", "--rm", "--network", "none", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--memory", "512m", "--pids-limit", "128", "--entrypoint", "keytool", "--mount", "type=bind,source=" + temp + ",target=/identity", r.ImageID}
	_, err = x.docker(append(base, "-genkeypair", "-alias", "node", "-keyalg", "EC", "-groupname", "secp256r1", "-dname", "CN="+p.NodeID, "-ext", "SAN="+san, "-validity", "365", "-storetype", "PKCS12", "-keystore", "/identity/identity.p12", "-storepass:file", "/identity/tls-password", "-noprompt")...)
	if err != nil {
		return errors.New("PREVIEW_CERTIFICATE_FAILED")
	}
	der, err := x.docker(append(base, "-exportcert", "-alias", "node", "-keystore", "/identity/identity.p12", "-storepass:file", "/identity/tls-password")...)
	if err != nil {
		return errors.New("PREVIEW_CERTIFICATE_FAILED")
	}
	if _, err = x509.ParseCertificate(der); err != nil {
		return errors.New("PREVIEW_CERTIFICATE_INVALID")
	}
	if err = os.WriteFile(filepath.Join(temp, "node.der"), der, 0600); err != nil {
		return err
	}
	hash := sha256.Sum256(der)
	if err = atomicJSON(filepath.Join(temp, "connection.json"), map[string]string{"nodeId": p.NodeID, "name": p.NodeID, "controlUri": "https://" + p.ControlHost + ":9443", "certificateSha256": hex.EncodeToString(hash[:]), "tokenFile": "/REPLACE/with/protected/platform/machine-token", "mediaAddress": p.MediaAddress}); err != nil {
		return err
	}
	for _, name := range []string{"", "machine-token", "tls-password", "zlm-secret", "identity.p12", "node.der", "connection.json"} {
		file := filepath.Join(temp, name)
		mode := os.FileMode(0600)
		if name == "" {
			mode = 0700
		}
		if err = os.Chmod(file, mode); err != nil {
			return err
		}
		if err = os.Chown(file, 10001, 10001); err != nil {
			return err
		}
	}
	return os.Rename(temp, dir)
}

func (x *executor) previewCompose(r Release) (string, error) {
	if err := r.Config.Validate(); err != nil {
		return "", err
	}
	if err := x.preparePreviewIdentity(r); err != nil {
		return "", err
	}
	p := r.Config.Preview
	dir := filepath.Join(x.root, "instances", r.InstanceID, "releases", r.ID)
	configDir := filepath.Join(dir, "preview-config")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return "", err
	}
	if err := os.Chown(configDir, 10001, 10001); err != nil {
		return "", err
	}
	budget := map[string]any{"maxSources": p.MaxSources, "maxViewers": p.MaxViewers, "maxTranscodes": p.MaxTranscodes, "maxStarting": p.MaxStarting, "ingressKbps": p.IngressKbps, "egressKbps": p.EgressKbps, "sourceUpperKbps": p.SourceUpperKbps, "outputUpperKbps": p.OutputUpperKbps}
	config := map[string]any{"nodeId": p.NodeID, "bindHost": "0.0.0.0", "controlPort": 9443, "keyStore": "/identity/identity.p12", "keyStorePasswordFile": "/identity/tls-password", "tokenFile": "/identity/machine-token", "workDir": "/state/ownership", "zlmApi": "http://127.0.0.1:8080", "zlmExecutable": "/opt/media/MediaServer", "zlmConfig": "/state/config.ini", "zlmSecretFile": "/identity/zlm-secret", "ffmpegExecutable": "/usr/bin/ffmpeg", "mediaAddress": p.MediaAddress, "allowedTargets": p.AllowedTargets, "maxSessions": p.MaxViewers, "maxTranscodes": p.MaxTranscodes, "budget": budget}
	filename := filepath.Join(configDir, "node.json")
	if err := immutableJSON(filename, config); err != nil {
		return "", err
	}
	if err := os.Chown(filename, 10001, 10001); err != nil {
		return "", err
	}
	dataDir := filepath.Join(x.root, "instances", r.InstanceID, "preview-state")
	if err := os.MkdirAll(dataDir, 0750); err != nil {
		return "", err
	}
	info, err := os.Lstat(dataDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("PREVIEW_STATE_UNSAFE")
	}
	name := "sfops-" + strings.ReplaceAll(r.InstanceID, "-", "")
	service := map[string]any{
		"image": r.ImageID, "pull_policy": "never", "container_name": name,
		"entrypoint": []string{"/opt/preview/entrypoint.sh"}, "command": []string{}, "user": "0:0",
		"init": true, "restart": "no", "read_only": true, "cap_drop": []string{"ALL"},
		"cap_add":      []string{"NET_ADMIN", "SETUID", "SETGID", "CHOWN", "DAC_OVERRIDE", "SETPCAP"},
		"security_opt": []string{"no-new-privileges:true"}, "pids_limit": 1024,
		"mem_limit": strconv.Itoa(r.Config.MemoryMiB) + "m", "cpus": p.CPUs, "stop_grace_period": "25s",
		"tmpfs":   []string{"/tmp:size=64m,mode=1777"},
		"ports":   []string{r.Config.BindAddress + ":9443:9443/tcp", r.Config.BindAddress + ":18000:18000/tcp", r.Config.BindAddress + ":18000:18000/udp"},
		"volumes": []map[string]any{{"type": "bind", "source": configDir, "target": "/config", "read_only": true}, {"type": "bind", "source": x.previewIdentity(r), "target": "/identity", "read_only": true}, {"type": "bind", "source": dataDir, "target": "/state"}},
		"labels":  map[string]string{"com.streamfusion.ops.controller": x.owner.ControllerID, "com.streamfusion.ops.instance": r.InstanceID, "com.streamfusion.ops.release": r.ID, "com.streamfusion.ops.config": Digest(r.Config), "com.streamfusion.ops.recipe": PreviewRecipe},
		"logging": map[string]any{"driver": "json-file", "options": map[string]string{"max-size": "10m", "max-file": "3"}},
	}
	file := filepath.Join(dir, "compose.json")
	return file, immutableJSON(file, map[string]any{"services": map[string]any{"app": service}})
}

func immutableJSON(file string, value any) error {
	expected, err := json.Marshal(value)
	if err != nil {
		return err
	}
	actual, err := os.ReadFile(file)
	if err == nil {
		if !bytes.Equal(actual, expected) {
			return errors.New("RELEASE_MATERIAL_CONFLICT")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	return atomicJSON(file, value)
}

// Preview credentials are read from the protected target and never enter
// controller jobs or browser responses. Only the dedicated service handoff may
// briefly carry the machine token to Platform.
func (x *executor) previewCall(r Release, path string, body any, dst any) error {
	der, err := os.ReadFile(filepath.Join(x.previewIdentity(r), "node.der"))
	if err != nil {
		return err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	token, err := os.ReadFile(filepath.Join(x.previewIdentity(r), "machine-token"))
	if err != nil || len(token) > 128 {
		return errors.New("PREVIEW_CREDENTIAL_UNAVAILABLE")
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: r.Config.Preview.ControlHost}}
	client := &http.Client{Timeout: 5 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	method := "GET"
	var input io.Reader
	if body != nil {
		method = "POST"
		data, e := json.Marshal(body)
		if e != nil {
			return e
		}
		input = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, "https://"+r.Config.BindAddress+":9443/preview-control/v1/"+path, input)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return errors.New("PREVIEW_CONTROL_UNREACHABLE")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 131073))
	if err != nil || len(data) > 131072 {
		return errors.New("PREVIEW_CONTROL_INVALID")
	}
	if response.StatusCode != 200 {
		var failure struct {
			ReasonCode string `json:"reasonCode"`
		}
		_ = json.Unmarshal(data, &failure)
		if !regexpReason(failure.ReasonCode) {
			return errors.New("PREVIEW_CONTROL_REJECTED")
		}
		return errors.New(failure.ReasonCode)
	}
	return json.Unmarshal(data, dst)
}

func regexpReason(value string) bool {
	if len(value) < 1 || len(value) > 80 {
		return false
	}
	for _, ch := range value {
		if ch != '_' && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') {
			return false
		}
	}
	return true
}

func (x *executor) previewHealth(r Release) (Result, error) {
	end := time.Now().Add(time.Duration(r.Config.HealthSeconds) * time.Second)
	for time.Now().Before(end) {
		actual, err := x.previewObserved(r)
		if err == nil {
			return actual, nil
		}
		time.Sleep(time.Second)
	}
	return Result{}, errors.New("PREVIEW_HEALTH_FAILED")
}

func (x *executor) previewObserved(r Release) (Result, error) {
	actual, err := x.inspected(r)
	if err != nil {
		return Result{}, err
	}
	var snapshot PreviewObservation
	if err = x.previewCall(r, "snapshot", nil, &snapshot); err != nil {
		return Result{}, err
	}
	if snapshot.ProtocolVersion != 1 || snapshot.NodeID != r.Config.Preview.NodeID || !ID.MatchString(snapshot.BootID) || !Hex.MatchString(snapshot.ConfigVersion) || (snapshot.State != "READY" && snapshot.State != "MAINTENANCE") || snapshot.MaxSessions != r.Config.Preview.MaxViewers || snapshot.MaxTranscodes != r.Config.Preview.MaxTranscodes {
		return Result{}, errors.New("PREVIEW_HEALTH_FAILED")
	}
	actual.Health = "APPLICATION_READY"
	actual.Preview = &snapshot
	return actual, nil
}

func (x *executor) previewMaintain(req Request, action string, r Release) error {
	var state previewMaintenance
	if err := x.previewCall(r, "maintenance", nil, &state); err != nil {
		return err
	}
	if state.ProtocolVersion != 1 || state.NodeID != r.Config.Preview.NodeID {
		return errors.New("PREVIEW_IDENTITY_MISMATCH")
	}
	if action == "FINISH" && state.JobID == "" {
		if _, err := os.Lstat(filepath.Join(x.root, "jobs", req.JobID, "preview-claim.json")); !os.IsNotExist(err) {
			return errors.New("PREVIEW_FENCE_LOST")
		}
		return nil
	} // No claim: the failed job did not stop the baseline.
	if state.PermitID != req.MaintenancePermit {
		return errors.New("MAINTENANCE_PERMIT_MISMATCH")
	}
	command := map[string]any{"protocolVersion": 1, "nodeId": r.Config.Preview.NodeID, "bootId": state.BootID, "configVersion": state.ConfigVersion, "action": action, "permitId": req.MaintenancePermit, "jobId": req.JobID, "deadline": time.Now().Add(10 * time.Second).UnixMilli()}
	var result previewMaintenance
	if err := x.previewCall(r, "maintenance", command, &result); err != nil {
		return err
	}
	expected := "CLAIMED"
	if action == "FINISH" {
		expected = "COMPLETE"
	}
	if result.State != expected || result.JobID != req.JobID || result.PermitID != req.MaintenancePermit {
		return errors.New("MAINTENANCE_EVIDENCE_MISMATCH")
	}
	if action == "CLAIM" {
		return x.recordPreviewClaim(req)
	}
	return nil
}

func (x *executor) recordPreviewClaim(req Request) error {
	return atomicJSON(filepath.Join(x.root, "jobs", req.JobID, "preview-claim.json"), map[string]string{"jobId": req.JobID, "permitId": req.MaintenancePermit})
}

func previewPorts(c Config) error {
	tcp := []net.Listener{}
	defer func() {
		for _, l := range tcp {
			l.Close()
		}
	}()
	for _, port := range []int{9443, 18000} {
		l, e := net.Listen("tcp", net.JoinHostPort(c.BindAddress, strconv.Itoa(port)))
		if e != nil {
			return errors.New("PREVIEW_PORT_IN_USE")
		}
		tcp = append(tcp, l)
	}
	udp, e := net.ListenPacket("udp", net.JoinHostPort(c.BindAddress, "18000"))
	if e != nil {
		return errors.New("PREVIEW_PORT_IN_USE")
	}
	return udp.Close()
}

func (x *executor) previewRegistration(r Release) (map[string]any, error) {
	var connection map[string]string
	if err := readJSON(filepath.Join(x.previewIdentity(r), "connection.json"), &connection); err != nil {
		return nil, err
	}
	return map[string]any{"connection": connection, "secureTransferFrom": filepath.Join(x.previewIdentity(r), "machine-token"), "instruction": fmt.Sprintf("安全复制 machine-token 到 Platform 的受保护文件，修改 tokenFile；注册 %s、检查并明确启用后，在原预览中心验证首帧。", r.Config.Preview.NodeID)}, nil
}

type PreviewConnection struct {
	NodeID            string `json:"nodeId"`
	Name              string `json:"name"`
	ControlURI        string `json:"controlUri"`
	CertificateSHA256 string `json:"certificateSha256"`
	MediaAddress      string `json:"mediaAddress"`
}

type PreviewHandoff struct {
	Connection   PreviewConnection `json:"connection"`
	MachineToken string            `json:"machineToken"`
}

// This material is only returned through the executor's protected local/SSH
// channel to the controller's dedicated Platform service endpoint.
func (x *executor) previewHandoff(r Release) (PreviewHandoff, error) {
	identity := x.previewIdentity(r)
	for index, path := range []string{identity, filepath.Join(identity, "connection.json")} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 || index == 0 && !info.IsDir() || index == 1 && !info.Mode().IsRegular() {
			return PreviewHandoff{}, errors.New("PREVIEW_IDENTITY_UNSAFE")
		}
	}
	var connection struct {
		PreviewConnection
		TokenFile string `json:"tokenFile"`
	}
	if err := readJSON(filepath.Join(identity, "connection.json"), &connection); err != nil {
		return PreviewHandoff{}, errors.New("PREVIEW_CONNECTION_UNAVAILABLE")
	}
	file := filepath.Join(identity, "machine-token")
	info, err := os.Lstat(file)
	if err != nil || !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 || info.Size() > 128 {
		return PreviewHandoff{}, errors.New("PREVIEW_CREDENTIAL_UNAVAILABLE")
	}
	token, err := os.ReadFile(file)
	if err != nil || !Hex.MatchString(strings.TrimSpace(string(token))) {
		return PreviewHandoff{}, errors.New("PREVIEW_CREDENTIAL_UNAVAILABLE")
	}
	return PreviewHandoff{Connection: connection.PreviewConnection, MachineToken: strings.TrimSpace(string(token))}, nil
}

func (x *executor) previewClaimed(req Request) bool {
	var state previewMaintenance
	file := filepath.Join(x.root, "instances", req.Candidate.InstanceID, "preview-state", "ownership", "preview-maintenance.json")
	return readJSON(file, &state) == nil && state.ProtocolVersion == 1 && state.NodeID == req.Candidate.Config.Preview.NodeID && state.State == "CLAIMED" && state.JobID == req.JobID && state.PermitID == req.MaintenancePermit
}
