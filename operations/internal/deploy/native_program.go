package deploy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (x *executor) prepareNativeProgram(ctx context.Context, req Request, dir, root, user string, uid, gid int, parts map[string]string) error {
	d := req.Installation.Definition
	if builtinStep(req, "NATIVE_PROGRAM") != nil {
		return x.prepareBuiltinNative(ctx, req, root, user, uid, gid, parts)
	}
	if d.Native.BuildRecipe == "GIT_DEB" {
		return x.prepareGitProgram(ctx, req, root, parts)
	}
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
	build := func(phase string, command []string) error {
		if err := x.checkpoint(req, phase); err != nil {
			return err
		}
		loggedContext, closeLog := x.installationLogContext(ctx, req, phase)
		defer closeLog()
		_, err := runNativeBuild(loggedContext, user, source, command[0], command[1:]...)
		if err != nil {
			return errors.New(phase + "_FAILED")
		}
		return nil
	}
	switch d.Native.BuildRecipe {
	case "FFMPEG_SOURCE":
		if err := build("NATIVE_FFMPEG_CONFIGURE", deployCommand("native-ffmpeg-configure")); err != nil {
			return err
		}
		if err := build("NATIVE_FFMPEG_BUILD", deployCommand("native-make-build", "-j"+nativeBuildJobs())); err != nil {
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
		if err := build("NATIVE_PYTHON_CONFIGURE", deployCommand("native-python-configure", "--prefix="+prefix)); err != nil {
			return err
		}
		if err := build("NATIVE_PYTHON_BUILD", deployCommand("native-make-build", "-j"+nativeBuildJobs())); err != nil {
			return err
		}
		if err := build("NATIVE_PYTHON_INSTALL", deployCommand("native-python-install")); err != nil {
			return err
		}
	case "REDIS_SOURCE":
		if err := build("NATIVE_REDIS_BUILD", deployCommand("native-redis-build", "-j"+nativeBuildJobs())); err != nil {
			return err
		}
		for _, binary := range []string{"redis-server", "redis-cli"} {
			if err := copyNativeFile(filepath.Join(source, "src", binary), filepath.Join(root, "bin", binary)); err != nil {
				return err
			}
		}
	case "NGINX_SOURCE":
		if err := build("NATIVE_NGINX_CONFIGURE", deployCommand("native-nginx-configure", "--prefix="+root, "--conf-path="+filepath.Join(root, "config/nginx.conf"), "--pid-path="+filepath.Join(root, "data/nginx.pid"))); err != nil {
			return err
		}
		if err := build("NATIVE_NGINX_BUILD", deployCommand("native-make-build", "-j"+nativeBuildJobs())); err != nil {
			return err
		}
		if err := copyNativeFile(filepath.Join(source, "objs/nginx"), filepath.Join(root, "bin/nginx")); err != nil {
			return err
		}
	case "ZLM_SOURCE":
		output := filepath.Join(workspace, "build")
		if err := build("NATIVE_ZLM_CONFIGURE", deployCommand("native-zlm-configure", source, output)); err != nil {
			return err
		}
		if err := build("NATIVE_ZLM_BUILD", deployCommand("native-zlm-build", output, nativeBuildJobs())); err != nil {
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
	return deployTemplate("native/maven-wrapper.sh.tmpl",
		posixSingleQuote(filepath.Join(root, "runtime/jdk")),
		posixSingleQuote(filepath.Join(root, "runtime/maven/bin/mvn")))
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
			initLine = deployTemplate("config/mysql-bootstrap.cnf.tmpl", filepath.Join(config, "bootstrap.sql"))
		}
		if err = write("my.cnf", deployTemplate("config/mysql-server.cnf.tmpl", root, data, data, data, c.HostPort, c.BindAddress, initLine), false); err != nil {
			return err
		}
		if err = write("health.cnf", deployTemplate("config/mysql-health.cnf.tmpl", safeOptionSecret(secret), data), true); err != nil {
			return err
		}
		if !initialize {
			return nil
		}
		sql := deployTemplate("config/mysql-root.sql.tmpl", secret)
		if c.Database != "" {
			sql += deployTemplate("config/mysql-database.sql.tmpl", c.Database)
		}
		if c.Username != "" {
			sql += deployTemplate("config/mysql-user.sql.tmpl", c.Username, userSecret, c.Database, c.Username)
		}
		if err = write("bootstrap.sql", sql, true); err != nil {
			return err
		}
		if builtinFlow(req) {
			return nil
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
		if _, err = x.loggedSourceCommand(ctx, req, "NATIVE_MYSQL_INITIALIZE", "native-mysql-initialize", filepath.Join(root, "runtime/mysql/bin/mysqld"), "--basedir="+filepath.Join(root, "runtime/mysql"), "--datadir="+data, "--user="+user); err != nil {
			return errors.New("NATIVE_MYSQL_INITIALIZE_FAILED")
		}
	case "redis":
		hashed := sha256.Sum256([]byte(secret))
		if err = write("users.acl", deployTemplate("config/redis.acl.tmpl", hex.EncodeToString(hashed[:])), true); err != nil {
			return err
		}
		return write("redis.conf", deployTemplate("config/redis.conf.tmpl", c.BindAddress, c.HostPort, config, data, c.MemoryMiB*3/4), false)
	case "nginx":
		if err = write("nginx.conf", deployTemplate("config/nginx.conf.tmpl", data, data, data, data, data, data, c.BindAddress, c.HostPort, data, i.SoftwareID), false); err != nil {
			return err
		}
		if !initialize {
			return nil
		}
		if err = os.Mkdir(filepath.Join(data, "www"), 0750); err != nil {
			return err
		}
		return serviceFile(filepath.Join(data, "www/index.html"), []byte(deployScript("config/nginx-index.html")), gid, false)
	case "seaweedfs":
		if initialize {
			for _, name := range []string{"master", "volume", "filerldb2"} {
				if err = os.Mkdir(filepath.Join(data, name), 0750); err != nil {
					return err
				}
			}
		}
		body := seaweedS3Configuration(access, secret)
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
