package deploy

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/streamfusion-ai/operations/internal/serviceinstall"
)

// Official preparation only reads host files and asks apt to simulate. It does
// not refresh indexes, download packages, create data or change repositories.
func (x *executor) officialInstallPreflight(i serviceinstall.Request) serviceinstall.Preflight {
	p := serviceinstall.Preflight{Ready: true, Checks: []serviceinstall.Check{}, PortMappings: []serviceinstall.PortMapping{}}
	check := func(key, code, message string) {
		status := "PASS"
		if code != "" {
			status, p.Ready = "FAIL", false
		}
		p.Checks = append(p.Checks, serviceinstall.Check{Key: key, Status: status, Code: code, Message: message})
	}
	spec, err := serviceinstall.OfficialRecipeSpec(i.Definition)
	if err != nil || i.Validate() != nil {
		check("RECIPE", "OFFICIAL_RECIPE_INVALID", "官方安装定义或固定配置无效")
		return p
	}
	p.Root, p.ConfigDirectory, p.DataDirectory, p.LogsDirectory, p.BinaryDirectory = spec.Paths.Root, spec.Paths.Config, spec.Paths.Data, spec.Paths.Logs, filepath.Dir(spec.Paths.Binary)
	if i.Definition.Port != 0 {
		p.Endpoint = net.JoinHostPort(i.Config.BindAddress, strconv.Itoa(i.Config.HostPort))
		p.PortMappings = []serviceinstall.PortMapping{{Key: "main", BindAddress: i.Config.BindAddress, HostPort: i.Config.HostPort, ContainerPort: i.Config.HostPort, Protocol: "TCP"}}
	}
	if i.Definition.Key == "docker" {
		p.Endpoint = "unix:///var/run/docker.sock"
	}
	release, err := os.ReadFile("/etc/os-release")
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 || err != nil || !strings.Contains(string(release), "ID=ubuntu\n") || !strings.Contains(string(release), `VERSION_ID="24.04"`) {
		check("PLATFORM", "OFFICIAL_PLATFORM_UNSUPPORTED", "官方安装要求 Ubuntu 24.04 amd64 和受控 root 执行器")
		return p
	}
	check("PLATFORM", "", "Ubuntu 24.04 amd64 系统安装")
	for _, command := range append([]string{"/usr/bin/apt-get", "/usr/bin/apt-cache", "/usr/bin/dpkg-query", "/usr/bin/dpkg", "/usr/bin/systemctl"}, i.Definition.Native.RequiredTools...) {
		resolved, resolveErr := filepath.EvalSymlinks(command)
		if resolveErr != nil || secureAncestors(resolved) != nil {
			check("TOOLS", "OFFICIAL_TOOL_UNAVAILABLE", "缺少受保护系统工具 "+command)
			return p
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	installed, err := x.installedDockerPackages(ctx)
	if err != nil {
		check("PACKAGES", "OFFICIAL_PACKAGE_STATE_UNAVAILABLE", "无法完整确认软件包状态，请管理员先修复未完成的包事务")
		return p
	}
	if err = officialInstallAbsent(spec, installed); err != nil {
		check("EXISTING", err.Error(), "发现已有软件、服务或专属目录；请使用自行安装后的检测登记")
		return p
	}
	for _, mapping := range p.PortMappings {
		if availableServicePort(mapping) != nil {
			check("PORT", "OFFICIAL_PORT_UNAVAILABLE", "官方默认端口已占用")
			return p
		}
	}
	if i.Definition.Key == "docker" {
		if err = x.dockerEngineAbsent(ctx); err != nil {
			check("DOCKER_ENGINE", err.Error(), dockerPreflightMessage(err))
			return p
		}
	}
	plan, err := x.officialCandidatePlan(ctx, i.Definition, installed)
	if err != nil {
		check("PLAN", err.Error(), "无法生成只新增软件包的官方安装计划；不会升级或删除已有软件，请检查软件源和本机索引")
		return p
	}
	p.Official = plan
	check("PLAN", "", "只读检查通过；确认后目标主机联网刷新索引并复核完整包计划，变化时停止安装")
	return p
}

func officialInstallAbsent(spec serviceinstall.OfficialSpec, installed map[string]string) error {
	for _, name := range spec.Packages {
		if installed[name] != "" {
			return errors.New("OFFICIAL_PACKAGE_ALREADY_INSTALLED")
		}
	}
	paths := append([]string{spec.Paths.Binary}, spec.Paths.OwnedPaths...)
	if spec.Paths.Unit != "" {
		for _, parent := range []string{"/etc/systemd/system", "/usr/lib/systemd/system", "/lib/systemd/system", "/run/systemd/system"} {
			paths = append(paths, filepath.Join(parent, spec.Paths.Unit), filepath.Join(parent, spec.Paths.Unit+".d"))
		}
	}
	for _, name := range paths {
		if _, err := os.Lstat(name); !os.IsNotExist(err) {
			return errors.New("OFFICIAL_EXISTING_CONFIGURATION_OR_DATA")
		}
		if secureAncestors(existingServiceParent(filepath.Dir(name))) != nil {
			return errors.New("OFFICIAL_PATH_NOT_PROTECTED")
		}
	}
	return nil
}

func (x *executor) officialCandidatePlan(ctx context.Context, d serviceinstall.Definition, installed map[string]string) (*serviceinstall.OfficialPlan, error) {
	spec, err := serviceinstall.OfficialRecipeSpec(d)
	if err != nil {
		return nil, err
	}
	plan := &serviceinstall.OfficialPlan{Manager: "APT", OS: "ubuntu24.04", Architecture: "amd64", Paths: spec.Paths}
	if d.Key == "docker" {
		pinned, _ := serviceinstall.Find("docker", "29.9.2-ubuntu24.04")
		packages, err := dockerRecipePackages(pinned)
		if err != nil {
			return nil, err
		}
		for index, pkg := range packages {
			plan.Packages = append(plan.Packages, serviceinstall.OfficialPackage{Name: pkg.Name, Version: pkg.Version, Origin: d.Native.Parts[index].URL, Direct: true})
		}
		// These are the external Depends/Pre-Depends of the five hash-pinned
		// DEBs. APT resolves their transitive dependencies; the full DEB plan is
		// simulated again before applying, so this is not a dependency solver.
		dependencies := []string{"init-system-helpers", "iptables", "nftables", "libc6", "libseccomp2", "libsystemd0"}
		output, simulateErr := x.softwareRunner(ctx, "/usr/bin/apt-get", officialAptSimulateArgs(dependencies)...)
		if simulateErr != nil {
			return nil, errors.New("OFFICIAL_APT_PLAN_UNAVAILABLE")
		}
		additional, parseErr := parseOfficialAptPlan(output, nil, installed)
		if parseErr != nil {
			return nil, parseErr
		}
		if err = x.verifyOfficialPackageOrigins(ctx, additional); err != nil {
			return nil, err
		}
		plan.Packages = append(plan.Packages, additional...)
		sort.Slice(plan.Packages, func(i, j int) bool { return plan.Packages[i].Name < plan.Packages[j].Name })
	} else {
		args := officialAptSimulateArgs(spec.Packages)
		out, err := x.softwareRunner(ctx, "/usr/bin/apt-get", args...)
		if err != nil {
			return nil, errors.New("OFFICIAL_APT_PLAN_UNAVAILABLE")
		}
		plan.Packages, err = parseOfficialAptPlan(out, spec.Packages, installed)
		if err != nil {
			return nil, err
		}
		if err = x.verifyOfficialPackageOrigins(ctx, plan.Packages); err != nil {
			return nil, err
		}
	}
	plan.Commands = serviceinstall.OfficialInstallCommands(d, plan.Packages)
	return plan, plan.Validate(d)
}

func (x *executor) verifyOfficialPackageOrigins(ctx context.Context, packages []serviceinstall.OfficialPackage) error {
	if len(packages) == 0 {
		return nil
	}
	names := []string{"policy"}
	for _, p := range packages {
		names = append(names, p.Name)
	}
	policy, err := x.softwareRunner(ctx, "/usr/bin/apt-cache", names...)
	if err != nil {
		return errors.New("OFFICIAL_PACKAGE_ORIGIN_UNAVAILABLE")
	}
	indexes, err := x.softwareRunner(ctx, "/usr/bin/apt-cache", "policy")
	if err != nil {
		return errors.New("OFFICIAL_PACKAGE_ORIGIN_UNAVAILABLE")
	}
	return verifyOfficialPolicy(policy, packages, indexes)
}

func officialAptSimulateArgs(packages []string) []string {
	args := []string{"--simulate", "--no-remove", "--no-install-recommends", "-o", "APT::Get::AllowUnauthenticated=false", "-o", "Acquire::AllowInsecureRepositories=false", "install", "--"}
	return append(args, packages...)
}

var officialInstLine = regexp.MustCompile(`^Inst ([a-z0-9][a-z0-9+.-]*)(?::amd64)? \(([^ ]+) (Ubuntu:24\.04/[^\[\]]+?)(?: \[(?:amd64|all)\])?\)$`)

func parseOfficialAptPlan(output []byte, direct []string, installed map[string]string) ([]serviceinstall.OfficialPackage, error) {
	if len(output) >= 64<<10 {
		return nil, errors.New("OFFICIAL_PACKAGE_PLAN_TOO_LARGE")
	}
	wanted, seen := map[string]bool{}, map[string]bool{}
	for _, name := range direct {
		wanted[name] = true
	}
	packages := []serviceinstall.OfficialPackage{}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "Remv ") || strings.HasPrefix(line, "Purg ") {
			return nil, errors.New("OFFICIAL_PACKAGE_PLAN_REMOVES_SOFTWARE")
		}
		if !strings.HasPrefix(line, "Inst ") {
			continue
		}
		m := officialInstLine.FindStringSubmatch(line)
		if len(m) != 4 || seen[m[1]] || installed[m[1]] != "" {
			return nil, errors.New("OFFICIAL_PACKAGE_PLAN_UPGRADES_OR_UNTRUSTED")
		}
		origins := strings.Split(strings.TrimSpace(m[3]), ", ")
		for _, origin := range origins {
			if origin != "Ubuntu:24.04/noble" && origin != "Ubuntu:24.04/noble-updates" && origin != "Ubuntu:24.04/noble-security" {
				return nil, errors.New("OFFICIAL_PACKAGE_ORIGIN_UNTRUSTED")
			}
		}
		sort.Strings(origins)
		seen[m[1]] = true
		packages = append(packages, serviceinstall.OfficialPackage{Name: m[1], Version: m[2], Origin: strings.Join(origins, ", "), Direct: wanted[m[1]]})
		if len(packages) > 200 {
			return nil, errors.New("OFFICIAL_PACKAGE_PLAN_TOO_LARGE")
		}
	}
	for name := range wanted {
		if !seen[name] {
			return nil, errors.New("OFFICIAL_PACKAGE_PLAN_INCOMPLETE")
		}
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].Name < packages[j].Name })
	return packages, nil
}

// Use the host's configured Ubuntu indexes, including signed Ubuntu mirrors.
// APT authenticates indexes/packages; the planner additionally checks the exact
// selected repository and release instead of assuming a mirror hostname.
func verifyOfficialPolicy(output []byte, packages []serviceinstall.OfficialPackage, indexes []byte) error {
	if len(output) >= 64<<10 {
		return errors.New("OFFICIAL_PACKAGE_POLICY_TOO_LARGE")
	}
	trusted, err := officialUbuntuIndexes(indexes)
	if err != nil {
		return err
	}
	wanted := map[string]serviceinstall.OfficialPackage{}
	for _, p := range packages {
		wanted[p.Name] = p
	}
	seen := map[string]bool{}
	current, version := "", ""
	for _, line := range strings.Split(string(output), "\n") {
		if len(line) > 0 && line[0] != ' ' && strings.HasSuffix(line, ":") {
			current = strings.TrimSuffix(strings.TrimSuffix(line, ":"), ":amd64")
			version = ""
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "***" {
			fields = fields[1:]
		}
		if len(fields) == 2 {
			if _, err := strconv.Atoi(fields[1]); err == nil {
				version = fields[0]
				continue
			}
		}
		pkg, ok := wanted[current]
		if !ok || version != pkg.Version || len(fields) < 2 {
			continue
		}
		if _, err := strconv.Atoi(fields[0]); err != nil {
			continue
		}
		u, err := url.Parse(fields[1])
		if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
			return errors.New("OFFICIAL_PACKAGE_ORIGIN_UNTRUSTED")
		}
		if u.Hostname() == "" || len(fields) != 5 || fields[3] != "amd64" || fields[4] != "Packages" || !trusted[strings.Join(fields[1:], " ")] {
			return errors.New("OFFICIAL_PACKAGE_ORIGIN_UNTRUSTED")
		}
		dist := strings.SplitN(fields[2], "/", 2)
		if len(dist) != 2 || (dist[0] != "noble" && dist[0] != "noble-updates" && dist[0] != "noble-security") {
			return errors.New("OFFICIAL_PACKAGE_ORIGIN_UNTRUSTED")
		}
		seen[current] = true
	}
	for name := range wanted {
		if !seen[name] {
			return errors.New("OFFICIAL_PACKAGE_ORIGIN_UNAVAILABLE")
		}
	}
	return nil
}

func officialUbuntuIndexes(output []byte) (map[string]bool, error) {
	if len(output) >= 64<<10 {
		return nil, errors.New("OFFICIAL_PACKAGE_POLICY_TOO_LARGE")
	}
	trusted := map[string]bool{}
	current := ""
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 5 && fields[4] == "Packages" {
			current = strings.Join(fields[1:], " ")
			continue
		}
		if current == "" || len(fields) < 2 || fields[0] != "release" {
			continue
		}
		metadata := map[string]string{}
		for _, item := range strings.Split(strings.Join(fields[1:], ""), ",") {
			if key, value, ok := strings.Cut(item, "="); ok {
				metadata[key] = value
			}
		}
		archive := metadata["a"]
		trusted[current] = metadata["o"] == "Ubuntu" && metadata["l"] == "Ubuntu" && metadata["v"] == "24.04" && metadata["n"] == "noble" && (archive == "noble" || archive == "noble-updates" || archive == "noble-security") && metadata["b"] == "amd64"
		current = ""
	}
	return trusted, nil
}

func validOfficialOwnership(s SoftwareRequest) bool {
	o := s.Ownership
	if o == nil || o.RuntimeMode != "NATIVE" || o.Official == nil || !Hex.MatchString(o.BinaryHash) {
		return false
	}
	for _, d := range serviceinstall.OfficialCatalog() {
		if strings.ToUpper(d.Key) != s.SoftwareKey || o.Official.Validate(d) != nil {
			continue
		}
		p := o.Official.Paths
		return o.BinaryPath == p.Binary && ((d.Kind == "TOOL" && s.Kind == "TOOL" && s.Locator == p.Binary) || (d.Kind == "SERVICE" && s.Kind == "SYSTEMD" && s.Locator == p.Unit))
	}
	return false
}
