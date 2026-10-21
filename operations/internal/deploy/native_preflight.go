package deploy

import (
	"context"
	"errors"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (x *executor) nativeInstallPreflight(i serviceinstall.Request) serviceinstall.Preflight {
	root := serviceinstall.InstallRoot(x.root, i.SoftwareID, i.Config)
	result := serviceinstall.Preflight{Ready: true, Root: root, DataDirectory: filepath.Join(root, "data"), ConfigDirectory: filepath.Join(root, "config"), BinaryDirectory: filepath.Dir(filepath.Join(root, nativeBinaryRelative(i.Definition))), LogsDirectory: filepath.Join(root, "logs"), Checks: []serviceinstall.Check{}, PortMappings: serviceinstall.PortMappings(i.Definition, i.Config)}
	if i.Definition.Kind != "TOOL" {
		result.Endpoint = net.JoinHostPort(i.Config.BindAddress, strconv.Itoa(i.Config.HostPort))
	}
	check := func(key, code, message string) {
		status := "PASS"
		if code != "" {
			status = "FAIL"
			result.Ready = false
		}
		result.Checks = append(result.Checks, serviceinstall.Check{Key: key, Status: status, Code: code, Message: message})
	}
	osRelease, err := os.ReadFile("/etc/os-release")
	if err != nil || !strings.Contains(string(osRelease), "ID=ubuntu\n") || !strings.Contains(string(osRelease), `VERSION_ID="24.04"`) {
		check("PLATFORM", "NATIVE_PLATFORM_UNSUPPORTED", "原生配方要求Ubuntu24.04 amd64")
	} else {
		check("PLATFORM", "", "系统版本符合固定原生配方")
	}
	if i.Definition.Native == nil {
		check("RECIPE", "NATIVE_RECIPE_UNAVAILABLE", "没有可信原生材料")
		return result
	}
	for _, command := range i.Definition.Native.RequiredTools {
		resolved, err := filepath.EvalSymlinks(command)
		if err != nil || secureAncestors(resolved) != nil {
			check("TOOL", "NATIVE_DEPENDENCY_MISSING", "缺少受保护工具 "+command+"，请先准备依赖；不会自动联网安装")
		} else {
			check("TOOL", "", command+" 已存在")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dependencies := []string{}
	switch i.Definition.Key {
	case "ffmpeg":
		dependencies = []string{"openssl", "x264"}
	case "python":
		dependencies = []string{"openssl", "sqlite3", "libffi", "zlib", "liblzma"}
	case "redis":
		dependencies = []string{"openssl"}
	case "nginx":
		dependencies = []string{"openssl", "libpcre2-8", "zlib"}
	case "zlm":
		dependencies = []string{"openssl", "libsrtp2"}
	}
	for _, dependency := range dependencies {
		if _, err = x.softwareRunner(ctx, "/usr/bin/pkg-config", "--exists", dependency); err != nil {
			check("LIBRARY_"+dependency, "NATIVE_DEPENDENCY_MISSING", "源码构建缺少"+dependency+"开发文件；请先准备依赖，不会联网补装或静默关闭能力")
		}
	}
	if i.Definition.Key == "python" {
		if info, err := os.Stat("/usr/include/bzlib.h"); err != nil || !info.Mode().IsRegular() {
			check("LIBRARY_bzip2", "NATIVE_DEPENDENCY_MISSING", "Python需要libbz2开发头文件；请先准备依赖，不自动安装")
		}
	}
	if i.Definition.Key == "mysql" {
		if _, err = x.softwareRunner(ctx, "/usr/sbin/ldconfig", "-p"); err != nil {
			check("LIBRARIES", "NATIVE_DEPENDENCY_CHECK_UNAVAILABLE", "无法读取动态库目录")
		} else if out, err := x.softwareRunner(ctx, "/usr/sbin/ldconfig", "-p"); err != nil || !strings.Contains(string(out), "libaio.so.1") {
			check("LIBRARIES", "NATIVE_DEPENDENCY_MISSING", "MySQL需要libaio.so.1；请先安装兼容依赖，不创建伪造软链接")
		}
	}
	if _, _, _, err = x.serviceSecrets(i); err != nil {
		check("CREDENTIAL", err.Error(), "凭据格式不符合该配方")
	}
	parent := existingServiceParent(filepath.Dir(root))
	if secureAncestors(parent) != nil {
		check("DIRECTORY", "NATIVE_DIRECTORY_NOT_PROTECTED", "目标父目录须root拥有且不可由普通账号改写")
	}
	if nativeServiceAncestors(parent, filepath.Join(x.root, "software")) != nil {
		check("DIRECTORY_TRAVERSE", "NATIVE_DIRECTORY_NOT_TRAVERSABLE", "专用服务账号不能穿越安装目录的父路径；请选择可穿越的专用数据根目录")
	}
	if _, err = os.Lstat(root); !os.IsNotExist(err) {
		check("DIRECTORY", "SERVICE_DATA_DIRECTORY_EXISTS", "实例目录已存在，请核对原安装")
	}
	needed := uint64(1 << 30)
	if i.Source.ByteSize > 0 {
		needed += uint64(i.Source.ByteSize) * 8
	}
	if free, err := availableBytes(parent); err != nil || free < needed {
		check("SPACE", "SERVICE_SPACE_INSUFFICIENT", "不能确认足够的安装与构建空间")
	}
	for _, mapping := range result.PortMappings {
		if err = availableServicePort(mapping); err != nil {
			check("PORT_"+mapping.Key, "SERVICE_PORT_UNAVAILABLE", "端口"+mapping.Key+"不可绑定或已占用")
		} else {
			check("PORT_"+mapping.Key, "", "当前可绑定，启动时仍复检")
		}
		// Docker may publish through NAT without a listening host socket.
		if _, err := os.Stat("/var/run/docker.sock"); err == nil {
			owner, err := x.servicePublishedPortFor(ctx, mapping.BindAddress, mapping.HostPort, mapping.Protocol)
			if err != nil || owner != "" {
				check("PUBLISHED_"+mapping.Key, "SERVICE_PORT_PUBLISHED", "端口已由容器发布或无法确认发布状态")
			}
		}
	}
	if result.Ready {
		check("PLAN", "", "原生预检通过；此步骤没有创建账号、unit或数据")
	}
	return result
}

func nativeServiceAncestors(directory, managedSoftwareParent string) error {
	for current := directory; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("NATIVE_DIRECTORY_NOT_TRAVERSABLE")
		}
		// The protected software binding directory is the only ancestor whose
		// traversal bit is adjusted by this installer. Custom ancestors are kept.
		if current != managedSoftwareParent && info.Mode().Perm()&0001 == 0 {
			return errors.New("NATIVE_DIRECTORY_NOT_TRAVERSABLE")
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func nativeUserName(id string) string { return "sfops" + strings.ReplaceAll(id, "-", "")[:16] }
func nativeUnit(id string) string     { return "sfops-service-" + id + ".service" }
func nativeUnitPath(id string) string { return filepath.Join("/etc/systemd/system", nativeUnit(id)) }
func ensureNativeParent(root string) error {
	parent := filepath.Dir(root)
	existing := existingServiceParent(parent)
	if err := secureAncestors(existing); err != nil {
		return err
	}
	if err := os.MkdirAll(parent, 0711); err != nil {
		return err
	}
	// Existing software binding JSON remains0600; traverse-only access lets a
	// dedicated service reach its own0750 directory without listing its peers.
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() {
		return errors.New("NATIVE_DIRECTORY_NOT_PROTECTED")
	}
	if info.Mode().Perm()&0022 != 0 {
		return errors.New("NATIVE_DIRECTORY_NOT_PROTECTED")
	}
	return os.Chmod(parent, info.Mode().Perm()|0111)
}
