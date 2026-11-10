package deploy

import (
	"encoding/json"
	"net"
	"strconv"
	"strings"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
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
	apiHost := "0.0.0.0"
	if native {
		apiHost = "127.0.0.1"
	}
	return []byte(deployTemplate("config/mediamtx.json.tmpl",
		configurationJSONString(net.JoinHostPort(apiHost, strconv.Itoa(ports["api"]))), configurationJSONString(secret), configurationJSONString(user),
		ports["hls"] > 0, configurationJSONString(address("hls")),
		ports["rtmp"] > 0, configurationJSONString(address("rtmp")), configurationJSONString(address("main")),
		ports["srt"] > 0, configurationJSONString(address("srt")),
		ports["webrtc"] > 0, configurationJSONString(c.BindAddress), configurationJSONString(address("webrtc")), configurationJSONString(address("webrtcUdp"))))
}

func configurationJSONString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func seaweedS3Configuration(access, secret string) []byte {
	return []byte(deployTemplate("config/seaweedfs-s3.json.tmpl", configurationJSONString(access), configurationJSONString(secret)))
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
	return deployTemplate("config/zlm.ini.tmpl", secret, data, data, ports["main"], data, ports["rtsp"], ports["rtmp"], ports["rtc"], i.Config.BindAddress, ports["srt"], max(ports["rtpTcp"], ports["rtpUdp"]), data, data, bind)
}

func safeOptionSecret(value string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
}
