package serviceinstall

import (
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"
)

type OfficialPackage struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Origin  string `json:"origin"`
	Direct  bool   `json:"direct"`
}

type OfficialCommand struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

type OfficialPaths struct {
	Root       string   `json:"root"`
	Binary     string   `json:"binary"`
	Config     string   `json:"config"`
	Data       string   `json:"data"`
	Logs       string   `json:"logs"`
	Unit       string   `json:"unit"`
	OwnedPaths []string `json:"ownedPaths"`
}

type OfficialPlan struct {
	Manager      string            `json:"manager"`
	OS           string            `json:"os"`
	Architecture string            `json:"architecture"`
	Packages     []OfficialPackage `json:"packages"`
	Commands     []OfficialCommand `json:"commands"`
	Paths        OfficialPaths     `json:"paths"`
}

type OfficialSpec struct {
	Packages []string
	Paths    OfficialPaths
}

func OfficialCatalog() []Definition {
	definitions := []Definition{}
	for _, row := range []struct{ key, name, version, kind string }{
		{"redis", "Redis（Ubuntu 官方仓库）", "ubuntu24.04-repository", "SERVICE"},
		{"nginx", "Nginx（Ubuntu 官方仓库）", "ubuntu24.04-repository", "SERVICE"},
		{"mysql", "MySQL（Ubuntu 官方仓库）", "ubuntu24.04-repository", "SERVICE"},
		{"java", "OpenJDK 17（Ubuntu 官方仓库）", "17-ubuntu24.04-repository", "TOOL"},
		{"java", "OpenJDK 21（Ubuntu 官方仓库）", "21-ubuntu24.04-repository", "TOOL"},
		{"maven", "Maven（Ubuntu 官方仓库）", "ubuntu24.04-repository", "TOOL"},
		{"python", "Python（Ubuntu 官方仓库）", "ubuntu24.04-repository", "TOOL"},
		{"ffmpeg", "FFmpeg（Ubuntu 官方仓库）", "ubuntu24.04-repository", "TOOL"},
		{"git", "Git（Ubuntu 官方仓库）", "ubuntu24.04-repository", "TOOL"},
		{"docker", "Docker Engine（官方系统包）", "29.9.2-official-ubuntu24.04", "SERVICE"},
	} {
		d := Definition{Key: row.key, Name: row.name, Version: row.version, Kind: row.kind, Revision: row.key + "-" + row.version + "-official-v1", Platform: "linux/amd64", OfficialURL: "https://ubuntu.com/server/docs/how-to/software/package-management/", RuntimeModes: []string{"NATIVE"}, SourceModes: []string{"OFFICIAL"}, Native: &NativeDefinition{BuildRecipe: "SYSTEM_APT", RequiredTools: []string{"/usr/bin/apt-get", "/usr/bin/apt-cache", "/usr/bin/dpkg-query", "/usr/bin/systemctl"}}}
		switch row.key {
		case "redis":
			d.Port = 6379
		case "nginx":
			d.Port = 80
		case "mysql":
			d.Port = 3306
		case "git":
			d.Native.RequiredTools = append(d.Native.RequiredTools, "/usr/bin/ssh")
		case "docker":
			pinned, _ := Find("docker", "29.9.2-ubuntu24.04")
			d.Native.BuildRecipe = "SYSTEM_DOCKER_DEB"
			d.Native.Parts = pinned.Native.Parts
			d.Native.RequiredTools = pinned.Native.RequiredTools
			d.OfficialURL = "https://docs.docker.com/engine/install/ubuntu/#install-from-a-package"
		}
		definitions = append(definitions, d)
	}
	return definitions
}

func FindOfficial(key, version string) (Definition, error) {
	for _, d := range OfficialCatalog() {
		if d.Key == key && d.Version == version {
			return d, nil
		}
	}
	return Definition{}, errors.New("OFFICIAL_RECIPE_NOT_FOUND")
}

func IsOfficial(d Definition) bool {
	expected, err := FindOfficial(d.Key, d.Version)
	return err == nil && reflect.DeepEqual(expected, BareDefinition(d))
}

func OfficialRecipeSpec(d Definition) (OfficialSpec, error) {
	if !IsOfficial(d) {
		return OfficialSpec{}, errors.New("OFFICIAL_RECIPE_UNTRUSTED")
	}
	p := OfficialPaths{Root: "/", OwnedPaths: []string{}}
	var packages []string
	switch d.Key {
	case "redis":
		packages = []string{"redis-server", "redis-tools"}
		p.Binary, p.Config, p.Data, p.Logs, p.Unit = "/usr/bin/redis-server", "/etc/redis", "/var/lib/redis", "/var/log/redis", "redis-server.service"
		p.OwnedPaths = []string{p.Config, p.Data, p.Logs}
	case "nginx":
		packages = []string{"nginx", "nginx-common"}
		p.Binary, p.Config, p.Data, p.Logs, p.Unit = "/usr/sbin/nginx", "/etc/nginx", "/var/www/html", "/var/log/nginx", "nginx.service"
		p.OwnedPaths = []string{p.Config, p.Data, p.Logs}
	case "mysql":
		packages = []string{"mysql-server", "mysql-server-8.0", "mysql-client-8.0", "mysql-client-core-8.0", "mysql-server-core-8.0"}
		p.Binary, p.Config, p.Data, p.Logs, p.Unit = "/usr/sbin/mysqld", "/etc/mysql", "/var/lib/mysql", "/var/log/mysql", "mysql.service"
		p.OwnedPaths = []string{p.Config, p.Data, p.Logs}
	case "java":
		major := strings.SplitN(d.Version, "-", 2)[0]
		packages = []string{"openjdk-" + major + "-jdk-headless", "openjdk-" + major + "-jre-headless"}
		p.Binary = "/usr/lib/jvm/java-" + major + "-openjdk-amd64/bin/java"
	case "maven":
		packages, p.Binary = []string{"maven"}, "/usr/share/maven/bin/mvn"
	case "python":
		packages, p.Binary = []string{"python3", "python3-minimal", "python3.12", "python3.12-minimal", "python3-venv", "python3.12-venv"}, "/usr/bin/python3.12"
	case "ffmpeg":
		packages, p.Binary = []string{"ffmpeg"}, "/usr/bin/ffmpeg"
	case "git":
		packages, p.Binary = []string{"git", "git-man"}, "/usr/bin/git"
	case "docker":
		packages = []string{"containerd.io", "docker-ce-cli", "docker-ce", "docker-buildx-plugin", "docker-compose-plugin"}
		p.Binary, p.Config, p.Data, p.Logs, p.Unit = "/usr/bin/dockerd", "/etc/docker", "/var/lib/docker", "", "docker.service"
		p.OwnedPaths = []string{p.Config, p.Data}
	}
	return OfficialSpec{Packages: packages, Paths: p}, nil
}

func (c Config) validateOfficial(d Definition) error {
	expected := Config{Name: c.Name, RuntimeMode: "NATIVE", HostPort: d.Port, MemoryMiB: 512, Parameters: c.Parameters, ParameterCredentials: c.ParameterCredentials}
	if d.Port != 0 {
		expected.BindAddress = "127.0.0.1"
	}
	if d.Key == "nginx" {
		expected.BindAddress = "0.0.0.0"
	}
	if c.Name == "" || strings.TrimSpace(c.Name) != c.Name || len(c.Name) > 120 || strings.ContainsAny(c.Name, "\r\n\x00") || !reflect.DeepEqual(c, expected) {
		return errors.New("OFFICIAL_CONFIG_FIXED")
	}
	return nil
}

var officialPackageName = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{0,100}$`)
var officialPackageVersion = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.+:~_-]{0,150}$`)

func OfficialInstallCommands(d Definition, packages []OfficialPackage) []OfficialCommand {
	var fixed struct{ Refresh, Install, Start OfficialCommand }
	if err := json.Unmarshal([]byte(commandScript("builtin/official-plan.json")), &fixed); err != nil {
		panic(err)
	}
	args := fixed.Install.Args
	if d.Native != nil && d.Native.BuildRecipe == "SYSTEM_DOCKER_DEB" {
		for _, part := range d.Native.Parts {
			args = append(args, "./"+part.Name)
		}
		for _, p := range packages {
			if !p.Direct {
				args = append(args, p.Name+"="+p.Version)
			}
		}
	} else {
		for _, p := range packages {
			args = append(args, p.Name+"="+p.Version)
		}
	}
	fixed.Install.Args = args
	commands := []OfficialCommand{fixed.Refresh, fixed.Install}
	spec, err := OfficialRecipeSpec(d)
	if err == nil && spec.Paths.Unit != "" {
		fixed.Start.Args = append(fixed.Start.Args, spec.Paths.Unit)
		commands = append(commands, fixed.Start)
	}
	return commands
}

func (p *OfficialPlan) Validate(d Definition) error {
	if p == nil {
		return errors.New("OFFICIAL_PLAN_INVALID")
	}
	spec, err := OfficialRecipeSpec(d)
	if err != nil || p.Manager != "APT" || p.OS != "ubuntu24.04" || p.Architecture != "amd64" || !reflect.DeepEqual(p.Paths, spec.Paths) || len(p.Packages) < len(spec.Packages) || len(p.Packages) > 200 || !reflect.DeepEqual(p.Commands, OfficialInstallCommands(d, p.Packages)) {
		return errors.New("OFFICIAL_PLAN_INVALID")
	}
	wanted, seen := map[string]bool{}, map[string]bool{}
	for _, name := range spec.Packages {
		wanted[name] = true
	}
	for _, part := range p.Packages {
		if !officialPackageName.MatchString(part.Name) || !officialPackageVersion.MatchString(part.Version) || seen[part.Name] || part.Direct != wanted[part.Name] || part.Origin == "" || len(part.Origin) > 512 || strings.ContainsAny(part.Origin, "\r\n\x00") {
			return errors.New("OFFICIAL_PACKAGE_INVALID")
		}
		seen[part.Name] = true
	}
	for name := range wanted {
		if !seen[name] {
			return errors.New("OFFICIAL_PLAN_INCOMPLETE")
		}
	}
	return nil
}
