package deploy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (x *executor) zlmCompose(r Release) (string, error) {
	if err := r.Config.Validate(); err != nil {
		return "", err
	}
	z := r.Config.ZLM
	secret := x.secrets[z.SecretCredentialID+":"+z.SecretRevision]
	if !Hex.MatchString(secret) {
		return "", errors.New("ZLM_SECRET_UNAVAILABLE")
	}
	dir := filepath.Join(x.root, "instances", r.InstanceID, "releases", r.ID)
	configDir := filepath.Join(dir, "zlm-config")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return "", err
	}
	if err := os.Chown(configDir, 10001, 10001); err != nil {
		return "", err
	}
	config := zlmINI(r.InstanceID, *z, secret)
	configPath := filepath.Join(configDir, "config.ini")
	if err := immutablePrivateFile(configPath, []byte(config)); err != nil {
		return "", err
	}
	if err := os.Chown(configPath, 10001, 10001); err != nil {
		return "", err
	}
	stateDir := filepath.Join(x.root, "instances", r.InstanceID, "zlm-state")
	if err := os.MkdirAll(stateDir, 0750); err != nil {
		return "", err
	}
	info, err := os.Lstat(stateDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("ZLM_STATE_UNSAFE")
	}
	if err := os.Chown(stateDir, 10001, 10001); err != nil {
		return "", err
	}
	name := "sfops-" + strings.ReplaceAll(r.InstanceID, "-", "")
	service := map[string]any{
		"image": r.ImageID, "pull_policy": "never", "container_name": name,
		"entrypoint": []string{"/opt/media/MediaServer"},
		"command":    []string{"-c", "/config/config.ini", "-l", "4", "--log-dir", "/state/logs", "--log-size", "1", "--log-slice", "2", "-t", "4"},
		"user":       "10001:10001", "init": true, "read_only": true, "restart": "unless-stopped",
		"cap_drop": []string{"ALL"}, "security_opt": []string{"no-new-privileges:true"},
		"pids_limit": 512, "mem_limit": strconv.Itoa(r.Config.MemoryMiB) + "m", "cpus": z.CPUs,
		"tmpfs":   []string{"/tmp:size=64m,mode=1777"},
		"ports":   []string{fmt.Sprintf("%s:%d:8080/tcp", r.Config.BindAddress, z.APIPort), fmt.Sprintf("%s:%d:%d/tcp", r.Config.BindAddress, z.RTCPort, z.RTCPort), fmt.Sprintf("%s:%d:%d/udp", r.Config.BindAddress, z.RTCPort, z.RTCPort)},
		"volumes": []map[string]any{{"type": "bind", "source": configDir, "target": "/config", "read_only": true}, {"type": "bind", "source": stateDir, "target": "/state"}},
		"labels":  map[string]string{"com.streamfusion.ops.controller": x.owner.ControllerID, "com.streamfusion.ops.instance": r.InstanceID, "com.streamfusion.ops.release": r.ID, "com.streamfusion.ops.config": Digest(r.Config), "com.streamfusion.ops.recipe": ZLMRecipe},
		"logging": map[string]any{"driver": "json-file", "options": map[string]string{"max-size": "10m", "max-file": "3"}},
	}
	file := filepath.Join(dir, "compose.json")
	return file, immutableJSON(file, map[string]any{"services": map[string]any{"app": service}})
}

func immutablePrivateFile(path string, expected []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return errors.New("ZLM_CONFIG_UNSAFE")
		}
		actual, err := os.ReadFile(path)
		if err != nil || string(actual) != string(expected) {
			return errors.New("RELEASE_MATERIAL_CONFLICT")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(expected)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func zlmINI(instanceID string, z ZLMConfig, secret string) string {
	return fmt.Sprintf(`[api]
apiDebug=0
secret=%s

[general]
mediaServerId=sfops-%s
listen_ip=0.0.0.0
streamNoneReaderDelayMS=120000

[hook]
enable=0

[http]
port=8080
sslport=0
allow_ip_range=127.0.0.1,::1,%s

[rtsp]
port=8554
sslport=0
directProxy=0

[rtmp]
port=1935
sslport=0

[rtc]
port=%d
tcpPort=%d
externIP=%s
signalingPort=0
signalingSslPort=0
enableTurn=0

[shell]
port=0

[srt]
port=0

[rtp_proxy]
port=0

[ffmpeg]
bin=/usr/bin/ffmpeg
cmd=%%s -hide_banner -nostdin -nostats -rtsp_transport tcp -timeout 10000000 -threads 2 -i %%s -map 0:v:0 -an -c:v libx264 -preset veryfast -tune zerolatency -threads 4 -pix_fmt yuv420p -profile:v baseline -bf 0 -g 40 -keyint_min 20 -sc_threshold 0 -crf 23 -maxrate 6M -bufsize 3M -f flv %%s
log=/dev/null

[protocol]
paced_sender_ms=20
enable_audio=0
`, secret, strings.ReplaceAll(instanceID, "-", ""), strings.Join(z.PlatformSourceIPs, ","), z.RTCPort, z.RTCPort, z.MediaAddress)
}

func zlmPorts(c Config) error {
	for _, port := range []int{c.ZLM.APIPort, c.ZLM.RTCPort} {
		listener, err := net.Listen("tcp", net.JoinHostPort(c.BindAddress, strconv.Itoa(port)))
		if err != nil {
			return errors.New("ZLM_PORT_IN_USE")
		}
		listener.Close()
	}
	packet, err := net.ListenPacket("udp", net.JoinHostPort(c.BindAddress, strconv.Itoa(c.ZLM.RTCPort)))
	if err != nil {
		return errors.New("ZLM_PORT_IN_USE")
	}
	return packet.Close()
}

func (x *executor) zlmObserved(r Release) (Result, error) {
	actual, err := x.inspected(r)
	if err != nil {
		return Result{}, err
	}
	end := time.Now().Add(time.Duration(r.Config.HealthSeconds) * time.Second)
	for time.Now().Before(end) {
		out, err := x.docker("exec", actual.ContainerID, "curl", "--noproxy", "*", "--silent", "--max-time", "2", "http://127.0.0.1:8080/index/api/getServerConfig")
		if err == nil && len(out) <= 64<<10 {
			var result struct {
				Code int              `json:"code"`
				Data []map[string]any `json:"data"`
			}
			if json.Unmarshal(out, &result) == nil && result.Code == 0 && len(result.Data) == 1 {
				settings := result.Data[0]
				if settings["rtc.externIP"] == r.Config.ZLM.MediaAddress && settings["http.allow_ip_range"] == "127.0.0.1,::1,"+strings.Join(r.Config.ZLM.PlatformSourceIPs, ",") && fmt.Sprint(settings["rtc.port"]) == strconv.Itoa(r.Config.ZLM.RTCPort) && fmt.Sprint(settings["api.apiDebug"]) == "0" {
					actual.Health = "APPLICATION_READY"
					return actual, nil
				}
			}
		}
		time.Sleep(time.Second)
	}
	return Result{}, errors.New("ZLM_HEALTH_FAILED")
}
