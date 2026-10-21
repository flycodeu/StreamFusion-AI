// Package serviceinstall defines trusted service recipes shared by the
// controller and the restricted target executor. It never executes commands.
package serviceinstall

import "errors"

type NativePart struct {
	Name            string `json:"name"`
	URL             string `json:"url"`
	SHA256          string `json:"sha256"`
	ByteSize        int64  `json:"byteSize"`
	Format          string `json:"format"`
	StripComponents int    `json:"stripComponents"`
}
type NativeDefinition struct {
	Parts         []NativePart `json:"parts"`
	BuildRecipe   string       `json:"buildRecipe"`
	RequiredTools []string     `json:"requiredTools"`
}
type PortDescriptor struct {
	Key           string `json:"key"`
	Label         string `json:"label"`
	Protocol      string `json:"protocol"`
	DefaultPort   int    `json:"defaultPort"`
	ContainerPort int    `json:"containerPort"`
	Required      bool   `json:"required"`
	NativeOnly    bool   `json:"nativeOnly,omitempty"`
}
type Definition struct {
	Category     string            `json:"category,omitempty"`
	Kind         string            `json:"kind,omitempty"`
	Native       *NativeDefinition `json:"native,omitempty"`
	Ports        []PortDescriptor  `json:"ports,omitempty"`
	Key          string            `json:"key"`
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Revision     string            `json:"revision"`
	Platform     string            `json:"platform"`
	Image        string            `json:"image"`
	Digest       string            `json:"digest"`
	ImageID      string            `json:"imageId"`
	OfficialURL  string            `json:"officialUrl"`
	RuntimeModes []string          `json:"runtimeModes"`
	SourceModes  []string          `json:"sourceModes"`
	Port         int               `json:"port"`
}

var legacyCatalog = []Definition{
	{Key: "mysql", Name: "MySQL", Version: "8.4.11", Revision: "mysql-docker-v1", Platform: "linux/amd64", Image: "docker.io/library/mysql:8.4", Digest: "sha256:6f3a50835c54d61ffdcf78d71381ba268c27804cd96755231d2fd6f180f822f0", ImageID: "sha256:4aae07a2c195abb9b3e92449443b39b51212f555a35ff8f93da0e82683f45de8", OfficialURL: "https://hub.docker.com/_/mysql", Port: 3306},
	{Key: "redis", Name: "Redis", Version: "7.4.11", Revision: "redis-docker-v1", Platform: "linux/amd64", Image: "docker.io/library/redis:7.4", Digest: "sha256:cd745595f143052dd6a743bc5651d3ce4b03979fe5c99c7fcfab73461f6f217b", ImageID: "sha256:d24fb73aa9caa76060b48f711af3831f1e95286c745da622c00419ed864cd291", OfficialURL: "https://hub.docker.com/_/redis", Port: 6379},
	{Key: "nginx", Name: "Nginx", Version: "1.30.6", Revision: "nginx-docker-v1", Platform: "linux/amd64", Image: "docker.io/library/nginx:stable", Digest: "sha256:ae4728846c1b66db51449372bd4230027d10dc951dad9ea0c05ca75ccdccfab7", ImageID: "sha256:8f59ff5046a70e421d35ed7d1e862a963215c9689b36cb8f64a6c42f9cf2955d", OfficialURL: "https://hub.docker.com/_/nginx", Port: 80},
	{Key: "seaweedfs", Name: "SeaweedFS", Version: "4.48", Revision: "seaweedfs-s3-docker-v1", Platform: "linux/amd64", Image: "docker.io/chrislusf/seaweedfs:4.48", Digest: "sha256:aba492e2a4e4c90bff795745e8e660affa1f09e7650f5981bd7bccd1a06cd931", ImageID: "sha256:7b5ca1f11aa4c91593612d2d4a9e253ec9152a4bd13a19e551290a042a4c52f5", OfficialURL: "https://github.com/seaweedfs/seaweedfs/releases/tag/4.48", Port: 8333},
}

func legacyDefinitions() []Definition {
	out := make([]Definition, len(legacyCatalog))
	copy(out, legacyCatalog)
	for i := range out {
		out[i].RuntimeModes = []string{"DOCKER"}
		out[i].SourceModes = []string{"REGISTRY", "ARCHIVE"}
	}
	return out
}
func Catalog() []Definition {
	return append(install03Definitions(), additionalDefinitions()...)
}

// Keep frozen definitions unchanged: existing jobs retain their input hashes.
func install03Definitions() []Definition {
	out := legacyDefinitions()
	out = append(out,
		Definition{Key: "mediamtx", Name: "MediaMTX", Version: "1.21.2", Revision: "mediamtx-v1", Platform: "linux/amd64", Image: "docker.io/bluenviron/mediamtx:1.21.2", Digest: "sha256:85ca858277593daee5a9c4a9c30b0aa5c85e7a259a525c36fae41ecc88384151", ImageID: "sha256:275752dbd7765b5b4829669f0884d6ae565b3e091f9d4d887b5e526a2e98eba6", OfficialURL: "https://mediamtx.org/docs/kickoff/install", Port: 8554},
		Definition{Key: "zlm", Name: "ZLMediaKit", Version: "2026.10.21", Revision: "zlm-v1", Platform: "linux/amd64", Image: "docker.io/zlmediakit/zlmediakit:master", Digest: "sha256:00e6b9cbcf2e29884ff7f3b36d7f9088109ba1362048688fa3017f33aca6ed0e", ImageID: "sha256:db5f6b13904fb30e8e4bb60104a05aae6a741e00795091fe466fa6ec3d464ea5", OfficialURL: "https://github.com/ZLMediaKit/ZLMediaKit", Port: 8088},
		Definition{Key: "java", Name: "Java", Version: "21", Kind: "TOOL", Revision: "java-21-v1", Platform: "linux/amd64", Image: "docker.io/library/eclipse-temurin:21-jre", Digest: "sha256:cff19e6215689161eb6162c11b86b0c60ddf802164f2eaf48d570f8fb79a36c5", ImageID: "sha256:0795e0f1cbe674a8989352b0c0fbe5a940b711a0e5b0a0edd8a011c799b32bea", OfficialURL: "https://adoptium.net/temurin/releases"},
		Definition{Key: "java", Name: "Java", Version: "17", Kind: "TOOL", Revision: "java-17-v1", Platform: "linux/amd64", Image: "docker.io/library/eclipse-temurin:17-jre", Digest: "sha256:207ecae0b2b104dfc6dfa763d1e9cd2041cb344800a180e24cb1a28ed533ff90", ImageID: "sha256:0007898ed8b242b52ec2e855fba24dd105cac09e19930ea309a971694446aa2c", OfficialURL: "https://adoptium.net/temurin/releases"},
		Definition{Key: "maven", Name: "Maven", Version: "3.9.11", Kind: "TOOL", Revision: "maven-v1", Platform: "linux/amd64", Image: "docker.io/library/maven:3.9.11-eclipse-temurin-21-noble", Digest: "sha256:6fdc855a6ed81d288ca7ca37ac6ff5e9308b612485c0801d70b25a858c83d237", ImageID: "sha256:b9611ad9b2f6ebef1c9369a32d794718fc28ebe2b9bb37b81a64b731370907bd", OfficialURL: "https://maven.apache.org/install.html"})
	for i := range out {
		d := &out[i]
		if d.Kind == "" {
			d.Kind = "SERVICE"
		}
		d.Native = nativeDefinition(d.Key, d.Version)
		d.Ports = recipePorts(d.Key, d.Port)
		d.RuntimeModes = []string{"DOCKER"}
		d.SourceModes = []string{"REGISTRY", "ARCHIVE"}
		if d.Native != nil {
			d.RuntimeModes = append(d.RuntimeModes, "NATIVE")
			d.SourceModes = append(d.SourceModes, "ONLINE")
		}
	}
	return out
}

func recipePorts(key string, port int) []PortDescriptor {
	if key == "java" || key == "maven" {
		return nil
	}
	main := PortDescriptor{Key: "main", Label: "访问端口", Protocol: "TCP", DefaultPort: port, ContainerPort: port, Required: true}
	if key == "nginx" {
		main.ContainerPort = 80
	}
	if key == "zlm" {
		main.ContainerPort = 80
	}
	out := []PortDescriptor{main}
	add := func(key, label, protocol string, p int, required, native bool) {
		out = append(out, PortDescriptor{Key: key, Label: label, Protocol: protocol, DefaultPort: p, ContainerPort: p, Required: required, NativeOnly: native})
	}
	switch key {
	case "seaweedfs":
		for _, p := range []struct {
			k string
			p int
		}{{"master", 9333}, {"volume", 8081}, {"filer", 8888}, {"masterGrpc", 19333}, {"volumeGrpc", 18081}, {"filerGrpc", 18888}} {
			add(p.k, p.k+" 内部端口", "TCP", p.p, true, true)
		}
		add("s3Grpc", "S3 内部 gRPC（同访问 IP）", "TCP", 18333, true, true)
	case "mediamtx":
		add("api", "控制 API（本机）", "TCP", 9997, true, false)
		add("rtmp", "RTMP", "TCP", 1935, false, false)
		add("hls", "HLS", "TCP", 8888, false, false)
		add("webrtc", "WebRTC 信令", "TCP", 8889, false, false)
		add("webrtcUdp", "WebRTC 媒体", "UDP", 8189, false, false)
		add("srt", "SRT", "UDP", 8890, false, false)
	case "zlm":
		add("rtsp", "RTSP", "TCP", 8554, true, false)
		out[len(out)-1].ContainerPort = 554
		add("rtmp", "RTMP", "TCP", 1935, false, false)
		add("rtc", "WebRTC 媒体", "UDP", 8000, false, false)
		add("srt", "SRT", "UDP", 9000, false, false)
		add("rtpTcp", "RTP TCP", "TCP", 10000, false, false)
		add("rtpUdp", "RTP UDP", "UDP", 10000, false, false)
	}
	return out
}

func PortValue(d Definition, c Config, key string) int {
	if key == "main" {
		return c.HostPort
	}
	if value, ok := c.Ports[key]; ok {
		return value
	}
	for _, p := range d.Ports {
		if p.Key == key {
			if p.Required {
				return p.DefaultPort
			}
			return 0
		}
	}
	return 0
}
func PortMappings(d Definition, c Config) []PortMapping {
	if d.Kind == "TOOL" {
		return nil
	}
	ports := d.Ports
	if len(ports) == 0 {
		ports = recipePorts(d.Key, d.Port)
	}
	out := []PortMapping{}
	for _, p := range ports {
		if p.NativeOnly && c.Mode() != "NATIVE" {
			continue
		}
		value := PortValue(d, c, p.Key)
		if value == 0 {
			continue
		}
		bind := c.BindAddress
		if p.NativeOnly && p.Key != "s3Grpc" || p.Key == "api" {
			bind = "127.0.0.1"
		}
		inner := p.ContainerPort
		if c.Mode() == "NATIVE" || p.Protocol == "UDP" || d.Key == "zlm" && (p.Key == "rtpTcp" || p.Key == "rtpUdp") {
			inner = value
		}
		out = append(out, PortMapping{Key: p.Key, BindAddress: bind, HostPort: value, ContainerPort: inner, Protocol: p.Protocol})
	}
	return out
}

func Find(key, version string) (Definition, error) {
	for _, d := range Catalog() {
		if d.Key == key && d.Version == version {
			return d, nil
		}
	}
	return Definition{}, errors.New("SERVICE_RECIPE_NOT_FOUND")
}
