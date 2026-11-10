package controller

import (
	"database/sql"
	"embed"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

//go:embed scripts/additional-software/*.sh
var additionalSoftwareScripts embed.FS

type additionalSoftware struct {
	key, name, version, category, official, image, digest, imageID string
	url, sha256                                                    string
	memory                                                         int
}

var additionalSoftwareCatalog = []additionalSoftware{
	{key: "jenkins", name: "Jenkins", version: "2.580.2", category: "BUILD_TOOLS", official: "https://www.jenkins.io/doc/book/installing/", memory: 2048, image: "docker.io/jenkins/jenkins:2.580.2-lts-jdk21", digest: "sha256:19db581de85e7ce6a1ad89e55a0318d1114e4f103d2dc25493a235ff2b26d044", imageID: "sha256:212d4372963109ab1d9b0a20fa0d4644a253c6f16b063e591d2184a2b85f15f6", url: "https://get.jenkins.io/war-stable/2.580.2/jenkins.war", sha256: "d98032d2401c540a35613a82ed358921fcf3166d5e6eaaaf60a7941845be3251"},
	{key: "postgresql", name: "PostgreSQL", version: "18.6", category: "DATABASE", official: "https://www.postgresql.org/download/linux/ubuntu/", image: "docker.io/library/postgres:18.6-bookworm", digest: "sha256:02fb3245e038f33536cf13bc6c4e59f21d07b0cea3c1fe1c07c6e8aa8f083bcc", imageID: "sha256:107127087bab983e534546e8c30085e2ae04e4d805da656d1aee9340e5957fac", memory: 512},
	{key: "gitlab", name: "GitLab", version: "19.5.3-ce.0", category: "BUILD_TOOLS", official: "https://docs.gitlab.com/install/", image: "docker.io/gitlab/gitlab-ce:19.5.3-ce.0", digest: "sha256:60fc37e470aca439ead4b6e3ed8f578d9e85c954b84b611b5f323a32a9e47615", imageID: "sha256:f88c118808549ed0d65d94342db6eb5b11b53ba8692e6a4b8c87ef1a48687e41", memory: 8192},
	{key: "prometheus", name: "Prometheus", version: "3.16.0", category: "MONITORING", official: "https://prometheus.io/download/", image: "docker.io/prom/prometheus:v3.16.0", digest: "sha256:89c1a644817e9ad70fa2af76c84898e327c1de7063735f962e8313b1e28672c3", imageID: "sha256:9bd9cbec4bd4f33bb6ccedf8c25a5132f8e7b2fb158c9a223a54485e4332e962", memory: 512, url: "https://github.com/prometheus/prometheus/releases/download/v3.16.0/prometheus-3.16.0.linux-amd64.tar.gz", sha256: "68727009472a2c02e1ff7f062fd5b3e637285bffc8fc2d340c76fb449d6e0f34"},
	{key: "nacos", name: "Nacos", version: "3.3.0", category: "NETWORK", official: "https://nacos.io/docs/latest/quickstart/quick-start/", image: "docker.io/nacos/nacos-server:v3.3.0", digest: "sha256:8fa4724afe798993366ff5c1c648f0e30c8df4b49a2b605c84b6816be5bd320c", imageID: "sha256:37358943cfa7cf4b01367179c92b9c421b1cd15980ebe4b1d2bd96f227c198b8", memory: 2048, url: "https://github.com/alibaba/nacos/releases/download/3.3.0/nacos-server-3.3.0.tar.gz", sha256: "18d5ec3067e9e310401028ecc35e6e9c3a854c9b14d084f48b83bfea53208107"},
}

func additionalScript(name string) string {
	b, err := additionalSoftwareScripts.ReadFile("scripts/additional-software/" + name + ".sh")
	if err != nil {
		panic(err)
	}
	return serviceinstall.NormalizeScript(string(b))
}

func (app additionalSoftware) script(name string) string {
	replace := strings.NewReplacer("@@APP@@", app.key, "@@VERSION@@", app.version, "@@URL@@", app.url, "@@SHA256@@", app.sha256, "@@IMAGE@@", app.image, "@@DIGEST@@", app.digest, "@@IMAGE_ID@@", app.imageID, "@@MEMORY@@", fmt.Sprint(app.memory))
	helpers := ""
	if name == "native-material" {
		helpers += additionalScript("download")
	}
	if name == "docker-create" || name == "native-postgresql" || name == "native-gitlab" || name == "native-nacos" {
		helpers += additionalScript("secrets")
	}
	if name == "native-jenkins" || name == "native-prometheus" || name == "native-nacos" || name == "native-postgresql" {
		helpers += additionalScript("native-service")
	}
	if app.key == "postgresql" && (name == "native-prepare" || name == "native-postgresql") {
		helpers += additionalScript("validate-cidr")
	}
	if name == "delete-data" || name == "native-uninstall" {
		helpers += additionalScript("mount-check")
	}
	if name == "native-postgresql" || name == "native-gitlab" || name == "native-uninstall" && (app.key == "postgresql" || app.key == "gitlab") {
		helpers += additionalScript("packages")
	}
	return replace.Replace(additionalScript("common") + helpers + additionalScript(name))
}

func additionalStep(app additionalSoftware, phase, name, script string, timeout int) serviceinstall.CommandStep {
	return serviceinstall.CommandStep{Kind: "COMMAND", Phase: phase, Name: name, Script: additionalContextScript(app, script), RunAs: "ROOT", TimeoutSeconds: timeout}
}

func additionalContextScript(app additionalSoftware, name string) string {
	return "additional_context\n" + strings.TrimPrefix(app.script(name), strings.NewReplacer("@@APP@@", app.key, "@@VERSION@@", app.version).Replace(additionalScript("common")))
}

func additionalSoftwareDefinitions() []serviceinstall.Definition {
	out := []serviceinstall.Definition{}
	for _, app := range additionalSoftwareCatalog {
		for _, mode := range []string{"DOCKER", "NATIVE"} {
			for _, offline := range []bool{false, true} {
				source, suffix := "ONLINE", "online"
				if mode == "DOCKER" {
					source = "REGISTRY"
				}
				if offline {
					source, suffix = "ARCHIVE", "offline"
				}
				d := serviceinstall.Definition{Key: "custom-" + app.key + "-" + strings.ToLower(mode) + "-" + suffix, CatalogKey: app.key, Name: app.name, Version: app.version, Revision: "1", Kind: "SERVICE", Category: app.category, Platform: "linux/amd64", OfficialURL: app.official, RuntimeModes: []string{mode}, SourceModes: []string{source}, DefaultMemoryMiB: app.memory,
					Commands: &serviceinstall.CommandRecipe{FlowVersion: 1, Revision: 1, Kind: "SYSTEMD", Locator: "ops-app-${OPS_SOFTWARE_ID}.service"}}
				if mode == "DOCKER" {
					d.Image, d.Digest, d.ImageID = app.image, app.digest, app.imageID
					d.Commands.Kind, d.Commands.Locator = "DOCKER", "sfops-${OPS_SOFTWARE_ID}"
					d.Commands.Steps = []serviceinstall.CommandStep{additionalStep(app, "INSTALL", "获取并核验官方镜像", "docker-material", 1800), additionalStep(app, "INSTALL", "创建独立配置并启动容器", "docker-create", 1800), additionalStep(app, "VERIFY", "验证服务可用", "verify", 1200), additionalStep(app, "UNINSTALL", "移除本次容器并保留数据", "docker-uninstall", 180), additionalStep(app, "DELETE_DATA", "删除本次数据与配置", "delete-data", 120)}
				} else {
					d.Commands.BinaryPath = "${OPS_INSTALL_ROOT}/bin/run"
					if app.key == "gitlab" {
						d.Commands.Locator = "gitlab-runsvdir.service"
						d.Commands.BinaryPath = ""
					}
					if app.key == "postgresql" || app.key == "gitlab" {
						d.Commands.Distributions = []string{"ubuntu"}
					}
					d.Commands.Steps = []serviceinstall.CommandStep{additionalStep(app, "INSTALL", "检查依赖与安装位置", "native-prepare", 60), additionalStep(app, "INSTALL", "获取并核验官方安装文件", "native-material", 1800), additionalStep(app, "INSTALL", "配置并启动服务", "native-"+app.key, 1800), additionalStep(app, "VERIFY", "验证服务可用", "verify", 1200), additionalStep(app, "UNINSTALL", "卸载本次服务并保留数据", "native-uninstall", 600), additionalStep(app, "DELETE_DATA", "删除本次数据与配置", "delete-data", 180)}
				}
				d.Parameters, d.Port, d.Ports = additionalSoftwareParameters(app.key, mode)
				d.Management = additionalSoftwareManagement(app, mode)
				switch app.key {
				case "jenkins", "gitlab", "prometheus", "nacos":
					d.DefaultBindAddress = "0.0.0.0"
					d.Web = &serviceinstall.WebAccessDefinition{Scheme: "http", PortKey: "main", Path: "/"}
					if app.key == "gitlab" {
						d.Web.URLParameter = "EXTERNAL_URL"
					}
					if app.key == "nacos" {
						d.Web.PortKey = "CONSOLE"
					}
				}
				for index := range d.Commands.Steps {
					d.Commands.Steps[index].ID = fmt.Sprintf("step-%d", index+1)
				}
				out = append(out, d)
			}
		}
	}
	return out
}

func additionalSoftwareParameters(key, mode string) ([]serviceinstall.ParameterDefinition, int, []serviceinstall.PortDescriptor) {
	p := []serviceinstall.ParameterDefinition{}
	ports := []serviceinstall.PortDescriptor{}
	addPort := func(key, label string, port int) {
		ports = append(ports, serviceinstall.PortDescriptor{Key: key, Label: label, Protocol: "TCP", DefaultPort: port, ContainerPort: port, Required: true})
	}
	text := func(key, label, value string) {
		p = append(p, serviceinstall.ParameterDefinition{Key: key, Label: label, Type: "TEXT", Required: true, Default: value})
	}
	secret := func(key, label string) {
		p = append(p, serviceinstall.ParameterDefinition{Key: key, Label: label, Type: "CREDENTIAL", Required: true})
	}
	port := 0
	switch key {
	case "jenkins":
		port = 8080
		if mode == "NATIVE" {
			p = append(p, serviceinstall.ParameterDefinition{Key: "JAVA_BINARY", Label: "Java 21 或 25 可执行文件", Type: "PATH", Required: true, Default: "/usr/bin/java"})
		}
	case "postgresql":
		port = 5432
		text("DB_USER", "初始管理员", "opsadmin")
		text("DB_NAME", "初始数据库", "app")
		secret("DB_PASSWORD", "初始管理员密码")
		if mode == "NATIVE" {
			text("ALLOWED_CIDR", "允许连接的网段", "127.0.0.1/32")
		}
	case "gitlab":
		port = 80
		text("EXTERNAL_URL", "访问地址", "http://gitlab.example.com")
		secret("ROOT_PASSWORD", "初始 root 密码")
		if mode == "DOCKER" {
			ports = append(ports, serviceinstall.PortDescriptor{Key: "SSH", Label: "Git SSH", Protocol: "TCP", DefaultPort: 2222, ContainerPort: 22, Required: true})
		} else {
			p = append(p, serviceinstall.ParameterDefinition{Key: "SSH_PORT", Label: "服务器现有 SSH 端口", Type: "PORT", Required: true, Default: "22"})
		}
	case "prometheus":
		port = 9090
		text("RETENTION", "数据保留时间", "15d")
	case "nacos":
		port = 8848
		addPort("CONSOLE", "控制台", 8080)
		addPort("GRPC", "客户端 gRPC", 9848)
		addPort("CLUSTER_GRPC", "服务间 gRPC", 9849)
		secret("AUTH_TOKEN", "Token 签名密钥（Base64）")
		secret("AUTH_IDENTITY", "服务身份密钥")
		if mode == "NATIVE" {
			p = append(p, serviceinstall.ParameterDefinition{Key: "JAVA_BINARY", Label: "Java 17 以上可执行文件", Type: "PATH", Required: true, Default: "/usr/bin/java"})
		}
	}
	if len(ports) > 0 {
		ports = append([]serviceinstall.PortDescriptor{{Key: "main", Label: "服务端口", Protocol: "TCP", DefaultPort: port, ContainerPort: port, Required: true}}, ports...)
	}
	return p, port, ports
}

func additionalSoftwareManagement(app additionalSoftware, mode string) *serviceinstall.ManagementDefinition {
	return additionalConfiguration(app, mode)
}

func seedAdditionalSoftwareTemplates(tx *sql.Tx) error {
	for _, d := range additionalSoftwareDefinitions() {
		if err := d.ValidateCommands(); err != nil {
			return fmt.Errorf("%s: %w", d.Key, err)
		}
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("streamfusion-ops/"+d.Key)).String()
		if _, err := tx.Exec("INSERT OR IGNORE INTO command_templates(id,recipe_key,draft_json,published_json,revision,updated_at,builtin) VALUES(?,?,?,?,1,?,0)", id, d.Key, jsonText(d), jsonText(d), Now()); err != nil {
			return err
		}
	}
	return nil
}
