package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (x *executor) prepareNativeProgram(ctx context.Context, req Request, dir, root, user string, uid, gid int, parts map[string]string) error {
	d := req.Installation.Definition
	if d.Native.BuildRecipe == "DEB" {
		return x.prepareDebProgram(ctx, req, root, parts)
	}
	if d.Native.BuildRecipe == "RAW_BINARY" {
		return copyNativeFile(parts[d.Native.Parts[0].Name], filepath.Join(root, nativeBinaryRelative(d)))
	}
	if d.Native.BuildRecipe == "CODEX_BINARY" {
		destination := filepath.Join(root, "runtime/vendor")
		if err := extractNativePart(ctx, d.Native.Parts[0], parts[d.Native.Parts[0].Name], destination); err != nil {
			return err
		}
		return copyNativeFile(filepath.Join(destination, "codex-x86_64-unknown-linux-musl"), filepath.Join(root, "bin/codex"))
	}
	if d.Native.BuildRecipe == "JDK" {
		return extractNativePart(ctx, d.Native.Parts[0], parts[d.Native.Parts[0].Name], filepath.Join(root, "runtime"))
	}
	if d.Native.BuildRecipe == "MAVEN" {
		for index, part := range d.Native.Parts {
			destination := "maven"
			if index == 1 {
				destination = "jdk"
			}
			if err := extractNativePart(ctx, part, parts[part.Name], filepath.Join(root, "runtime", destination)); err != nil {
				return err
			}
		}
		wrapper := nativeMavenWrapper(root)
		if err := serviceFile(filepath.Join(root, "bin/mvn"), []byte(wrapper), 0, false); err != nil {
			return err
		}
		return os.Chmod(filepath.Join(root, "bin/mvn"), 0755)
	}
	if d.Native.BuildRecipe == "MYSQL_BINARY" {
		return extractNativePart(ctx, d.Native.Parts[0], parts[d.Native.Parts[0].Name], filepath.Join(root, "runtime/mysql"))
	}
	if d.Native.BuildRecipe == "BINARY" {
		destination := filepath.Join(root, "runtime/vendor")
		if err := extractNativePart(ctx, d.Native.Parts[0], parts[d.Native.Parts[0].Name], destination); err != nil {
			return err
		}
		binary := "weed"
		if d.Key == "mediamtx" {
			binary = "mediamtx"
		}
		return copyNativeFile(filepath.Join(destination, binary), filepath.Join(root, "bin", binary))
	}
	buildParent := filepath.Join(x.root, "native-build")
	if err := os.MkdirAll(buildParent, 0711); err != nil {
		return err
	}
	workspace := filepath.Join(buildParent, req.JobID)
	if err := os.Mkdir(workspace, 0700); err != nil {
		return err
	}
	defer os.RemoveAll(workspace)
	source := filepath.Join(workspace, "source")
	for index, part := range d.Native.Parts {
		destination := source
		if index > 0 {
			destination = filepath.Join(source, map[string]string{"toolkit.tar.gz": "3rdpart/ZLToolKit", "jsoncpp.tar.gz": "3rdpart/jsoncpp", "media-server.tar.gz": "3rdpart/media-server", "pybind11.tar.gz": "3rdpart/pybind11", "webassist.tar.gz": "www/webassist"}[part.Name])
		}
		if err := extractNativePart(ctx, part, parts[part.Name], destination); err != nil {
			return err
		}
	}
	if err := chownNativeTree(workspace, uid, gid); err != nil {
		return err
	}
	build := func(phase, command string, args ...string) error {
		if err := x.checkpoint(req, phase); err != nil {
			return err
		}
		output, err := runNativeBuild(ctx, user, source, command, args...)
		x.appendServiceLog(req, phase, x.redactSoftwareSecrets(string(output)))
		if err != nil {
			return errors.New(phase + "_FAILED")
		}
		return nil
	}
	switch d.Native.BuildRecipe {
	case "FFMPEG_SOURCE":
		if err := build("NATIVE_FFMPEG_CONFIGURE", "/bin/sh", "./configure", "--disable-doc", "--disable-ffplay", "--disable-debug", "--enable-gpl", "--enable-version3", "--enable-libx264", "--enable-openssl"); err != nil {
			return err
		}
		if err := build("NATIVE_FFMPEG_BUILD", "/usr/bin/make", "-j"+nativeBuildJobs()); err != nil {
			return err
		}
		for _, name := range []string{"ffmpeg", "ffprobe"} {
			if err := copyNativeFile(filepath.Join(source, name), filepath.Join(root, "bin", name)); err != nil {
				return err
			}
		}
	case "PYTHON_SOURCE":
		prefix := filepath.Join(root, "runtime/python")
		if err := os.Chown(filepath.Join(root, "runtime"), 0, gid); err != nil {
			return err
		}
		if err := os.Mkdir(prefix, 0750); err != nil {
			return err
		}
		if err := os.Chown(prefix, uid, gid); err != nil {
			return err
		}
		if err := build("NATIVE_PYTHON_CONFIGURE", "/bin/sh", "./configure", "--prefix="+prefix, "--with-ensurepip=install"); err != nil {
			return err
		}
		if err := build("NATIVE_PYTHON_BUILD", "/usr/bin/make", "-j"+nativeBuildJobs()); err != nil {
			return err
		}
		if err := build("NATIVE_PYTHON_INSTALL", "/usr/bin/make", "altinstall"); err != nil {
			return err
		}
	case "REDIS_SOURCE":
		if err := build("NATIVE_REDIS_BUILD", "/usr/bin/make", "-j"+nativeBuildJobs(), "MALLOC=libc", "BUILD_TLS=yes", "redis-server", "redis-cli"); err != nil {
			return err
		}
		for _, binary := range []string{"redis-server", "redis-cli"} {
			if err := copyNativeFile(filepath.Join(source, "src", binary), filepath.Join(root, "bin", binary)); err != nil {
				return err
			}
		}
	case "NGINX_SOURCE":
		if err := build("NATIVE_NGINX_CONFIGURE", "/bin/sh", "./configure", "--prefix="+root, "--conf-path="+filepath.Join(root, "config/nginx.conf"), "--pid-path="+filepath.Join(root, "data/nginx.pid"), "--error-log-path=stderr", "--http-log-path=/dev/stdout", "--with-http_ssl_module", "--with-http_v2_module"); err != nil {
			return err
		}
		if err := build("NATIVE_NGINX_BUILD", "/usr/bin/make", "-j"+nativeBuildJobs()); err != nil {
			return err
		}
		if err := copyNativeFile(filepath.Join(source, "objs/nginx"), filepath.Join(root, "bin/nginx")); err != nil {
			return err
		}
	case "ZLM_SOURCE":
		output := filepath.Join(workspace, "build")
		if err := build("NATIVE_ZLM_CONFIGURE", "/usr/bin/cmake", "-S", source, "-B", output, "-DCMAKE_BUILD_TYPE=Release", "-DENABLE_TESTS=OFF", "-DENABLE_PYTHON=OFF", "-DENABLE_OPENSSL=ON", "-DENABLE_WEBRTC=ON", "-DENABLE_SRT=ON", "-DDISABLE_REPORT=ON"); err != nil {
			return err
		}
		if err := build("NATIVE_ZLM_BUILD", "/usr/bin/cmake", "--build", output, "--parallel", nativeBuildJobs(), "--target", "MediaServer"); err != nil {
			return err
		}
		if err := copyNativeFile(filepath.Join(source, "release/linux/Release/MediaServer"), filepath.Join(root, "bin/MediaServer")); err != nil {
			return err
		}
	default:
		return errors.New("NATIVE_BUILD_RECIPE_UNSUPPORTED")
	}
	// Only this Job's source/build tree is removed after all compiler children
	// have exited. No service data, external packages or previous binaries move.
	if err := os.RemoveAll(workspace); err != nil {
		return errors.New("NATIVE_BUILD_CLEANUP_FAILED")
	}
	return nil
}

func posixSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
func nativeMavenWrapper(root string) string {
	return "#!/bin/sh\nJAVA_HOME=" + posixSingleQuote(filepath.Join(root, "runtime/jdk")) + "\nexport JAVA_HOME\nexec " + posixSingleQuote(filepath.Join(root, "runtime/maven/bin/mvn")) + " \"$@\"\n"
}

func (x *executor) writeNativeConfiguration(req Request, root string, gid int) error {
	return x.nativeConfigurationAt(req, root, filepath.Join(root, "config"), gid, true)
}
func (x *executor) nativeConfigurationAt(req Request, root, outputConfig string, gid int, initialize bool) error {
	i := req.Installation
	c := i.Config
	data := filepath.Join(root, "data")
	config := filepath.Join(root, "config")
	secret, userSecret, access, err := x.serviceSecrets(*i)
	if err != nil {
		return err
	}
	write := func(name, text string, sensitive bool) error {
		return serviceFile(filepath.Join(outputConfig, name), []byte(text), gid, sensitive)
	}
	switch i.Definition.Key {
	case "mysql":
		initLine := ""
		if initialize {
			initLine = "init-file=" + filepath.Join(config, "bootstrap.sql") + "\n"
		}
		if err = write("my.cnf", fmt.Sprintf("[mysqld]\nbasedir=%s/runtime/mysql\ndatadir=%s\nsocket=%s/mysql.sock\npid-file=%s/mysql.pid\nport=%d\nbind-address=%s\nmysqlx=0\nlog-error=/proc/self/fd/2\n%s", root, data, data, data, c.HostPort, c.BindAddress, initLine), false); err != nil {
			return err
		}
		if err = write("health.cnf", fmt.Sprintf("[client]\nuser=root\npassword=\"%s\"\nprotocol=socket\nsocket=%s/mysql.sock\n", safeOptionSecret(secret), data), true); err != nil {
			return err
		}
		if !initialize {
			return nil
		}
		sql := "ALTER USER 'root'@'localhost' IDENTIFIED BY '" + secret + "';\n"
		if c.Database != "" {
			sql += "CREATE DATABASE IF NOT EXISTS `" + c.Database + "`;\n"
		}
		if c.Username != "" {
			sql += "CREATE USER IF NOT EXISTS '" + c.Username + "'@'%' IDENTIFIED BY '" + userSecret + "';\nGRANT ALL ON `" + c.Database + "`.* TO '" + c.Username + "'@'%';\n"
		}
		if err = write("bootstrap.sql", sql, true); err != nil {
			return err
		}
		// Initialization is offline: no TCP listener and no default remote root.
		user := nativeUserName(i.SoftwareID)
		initializationCtx, initializationCancel := context.WithTimeout(context.Background(), nativeDeadline(req))
		defer initializationCancel()
		uidOut, uidErr := x.softwareRunner(initializationCtx, "/usr/bin/id", "-u", user)
		uid, uidParseErr := strconv.Atoi(strings.TrimSpace(string(uidOut)))
		if uidErr != nil || uidParseErr != nil || uid < 1 {
			return errors.New("NATIVE_ACCOUNT_ID_INVALID")
		}
		if err = chownNativeTree(data, uid, gid); err != nil {
			return err
		}
		if err = os.Chown(data, uid, gid); err != nil {
			return err
		}
		ctx := initializationCtx
		// mysqld --initialize performs only filesystem setup; the unit's init-file
		// sets the supplied password before the first network-serving startup.
		if _, err = x.softwareRunner(ctx, filepath.Join(root, "runtime/mysql/bin/mysqld"), "--no-defaults", "--initialize-insecure", "--basedir="+filepath.Join(root, "runtime/mysql"), "--datadir="+data, "--user="+user); err != nil {
			return errors.New("NATIVE_MYSQL_INITIALIZE_FAILED")
		}
	case "redis":
		hashed := sha256.Sum256([]byte(secret))
		if err = write("users.acl", "user default on #"+hex.EncodeToString(hashed[:])+" ~* &* +@all\n", true); err != nil {
			return err
		}
		return write("redis.conf", fmt.Sprintf("bind %s\nport %d\nprotected-mode yes\ndaemonize no\naclfile %s/users.acl\ndir %s\nappendonly yes\nappendfsync everysec\nmaxmemory %dmb\nmaxmemory-policy noeviction\nlogfile \"\"\n", c.BindAddress, c.HostPort, config, data, c.MemoryMiB*3/4), false)
	case "nginx":
		if err = write("nginx.conf", fmt.Sprintf("pid %s/nginx.pid;\nerror_log stderr;\nevents {}\nhttp { access_log /dev/stdout; client_body_temp_path %s/client_temp; proxy_temp_path %s/proxy_temp; fastcgi_temp_path %s/fastcgi_temp; uwsgi_temp_path %s/uwsgi_temp; scgi_temp_path %s/scgi_temp; server { listen %s:%d; root %s/www; location = /__sfops_health { default_type text/plain; return 200 \"%s\"; } } }\n", data, data, data, data, data, data, c.BindAddress, c.HostPort, data, i.SoftwareID), false); err != nil {
			return err
		}
		if !initialize {
			return nil
		}
		if err = os.Mkdir(filepath.Join(data, "www"), 0750); err != nil {
			return err
		}
		return serviceFile(filepath.Join(data, "www/index.html"), []byte("Nginx is running\n"), gid, false)
	case "seaweedfs":
		if initialize {
			for _, name := range []string{"master", "volume", "filerldb2"} {
				if err = os.Mkdir(filepath.Join(data, name), 0750); err != nil {
					return err
				}
			}
		}
		identity := map[string]any{"identities": []any{map[string]any{"name": "ops-managed", "credentials": []any{map[string]string{"accessKey": access, "secretKey": secret}}, "actions": []string{"Admin", "Read", "List", "Tagging", "Write"}}}}
		body, _ := json.Marshal(identity)
		return serviceFile(filepath.Join(outputConfig, "s3.json"), body, gid, true)
	case "mediamtx":
		return serviceFile(filepath.Join(outputConfig, "mediamtx.yml"), mediaMTXConfiguration(*i, secret, true), gid, true)
	case "zlm":
		certificate := filepath.Join(root, "runtime/dtls.pem")
		if initialize {
			if err = createNativeCertificate(certificate, i.SoftwareID, gid); err != nil {
				return err
			}
		}
		if err = validateNativeCertificate(certificate); err != nil {
			return err
		}
		return write("config.ini", zlmConfiguration(*i, secret, data, true), true)
	default:
		return errors.New("NATIVE_CONFIG_UNSUPPORTED")
	}
	return nil
}
