package deploy

import (
	"encoding/json"
	"fmt"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
	"net"
	"strconv"
	"strings"
)

func mediaMTXConfiguration(i serviceinstall.Request, secret string, native bool) []byte {
	c := i.Config
	host := "0.0.0.0"
	if native {
		host = c.BindAddress
	}
	ports := map[string]int{}
	for _, p := range serviceinstall.PortMappings(i.Definition, c) {
		ports[p.Key] = p.ContainerPort
	}
	user := c.Username
	if user == "" {
		user = "ops"
	}
	address := func(key string) string { return net.JoinHostPort(host, strconv.Itoa(ports[key])) }
	cfg := map[string]any{"logLevel": "info", "logDestinations": []string{"stdout"}, "readTimeout": "10s", "writeTimeout": "10s", "authMethod": "internal", "authInternalUsers": []any{map[string]any{"user": user, "pass": secret, "ips": []string{}, "permissions": []any{map[string]string{"action": "api"}, map[string]string{"action": "publish"}, map[string]string{"action": "read"}}}}, "api": true, "apiAddress": net.JoinHostPort("0.0.0.0", strconv.Itoa(ports["api"])), "metrics": false, "pprof": false, "playback": false, "moq": false, "rtsp": true, "rtspTransports": []string{"tcp"}, "rtspAddress": address("main"), "rtmp": ports["rtmp"] > 0, "rtmpAddress": address("rtmp"), "hls": ports["hls"] > 0, "hlsAddress": address("hls"), "webrtc": ports["webrtc"] > 0, "webrtcAddress": address("webrtc"), "webrtcLocalUDPAddress": address("webrtcUdp"), "webrtcAdditionalHosts": []string{c.BindAddress}, "webrtcLocalTCPAddress": "", "srt": ports["srt"] > 0, "srtAddress": address("srt"), "paths": map[string]any{"all_others": map[string]any{}}}
	if native {
		cfg["apiAddress"] = net.JoinHostPort("127.0.0.1", strconv.Itoa(ports["api"]))
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	return data
}

func zlmConfiguration(i serviceinstall.Request, secret, data string, native bool) string {
	ports := map[string]int{}
	for _, p := range serviceinstall.PortMappings(i.Definition, i.Config) {
		ports[p.Key] = p.ContainerPort
	}
	bind := "0.0.0.0"
	if native {
		bind = i.Config.BindAddress
	}
	return fmt.Sprintf("[api]\napiDebug=0\nsecret=%s\nsnapRoot=%s/snap\ndownloadRoot=%s\n[http]\nport=%d\nsslport=0\nrootPath=%s/www\nallow_ip_range=\n[rtsp]\nport=%d\nsslport=0\n[rtmp]\nport=%d\nsslport=0\n[rtc]\nport=%d\ntcpPort=0\nexternIP=%s\n[srt]\nport=%d\n[rtp_proxy]\nport=%d\n[record]\nappName=record\nfilePath=%s/record\n[protocol]\nenable_mp4=0\nenable_hls=1\n[hls]\nfilePath=%s/hls\n[general]\nlisten_ip=%s\nenableVhost=0\n[shell]\nport=0\n[hook]\nenable=0\n", secret, data, data, ports["main"], data, ports["rtsp"], ports["rtmp"], ports["rtc"], i.Config.BindAddress, ports["srt"], max(ports["rtpTcp"], ports["rtpUdp"]), data, data, bind)
}

func safeOptionSecret(value string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
}
