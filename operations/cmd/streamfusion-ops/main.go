package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/streamfusion-ai/operations/internal/controller"
	"github.com/streamfusion-ai/operations/internal/deploy"
	"github.com/streamfusion-ai/operations/internal/serviceinstall"
	"github.com/streamfusion-ai/operations/internal/webui"
	"golang.org/x/term"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: streamfusion-ops <serve|stop|init-admin|backup|restore|export-key|native-package|material-catalog|material-library-check|import-materials|target-init|target-verify|target-repair|target-upgrade|version> --help")
	}
	if args[0] == "material-catalog" {
		return writeMaterialCatalog(args[1:], os.Stdout)
	}
	if args[0] == "material-library-check" {
		return checkMaterialLibrary(args[1:], os.Stdout)
	}
	if args[0] == "native-package" {
		flags := flag.NewFlagSet("native-package", flag.ContinueOnError)
		key := flags.String("recipe", "", "内置软件配方，例如zlm、maven、java")
		version := flags.String("recipe-version", "", "配方版本；多版本默认选择目录第一项")
		out := flags.String("out", "", "不存在的输出材料文件")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || *key == "" || *out == "" {
			return errors.New("native-package需要--recipe与--out，可选--recipe-version")
		}
		selected := *version
		if selected == "" {
			for _, d := range serviceinstall.Catalog() {
				if d.Key == *key && d.Native != nil {
					selected = d.Version
					break
				}
			}
		}
		d, err := serviceinstall.Find(*key, selected)
		if err != nil || d.Native == nil {
			return errors.New("NATIVE_RECIPE_NOT_FOUND")
		}
		path, err := filepath.Abs(*out)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		if err = serviceinstall.PrepareNativeMaterial(ctx, d, path); err != nil {
			return err
		}
		fmt.Printf("NATIVE_MATERIAL_READY: %s；上传时选择%s %s 原生配方并核对SHA-256\n", path, d.Name, d.Version)
		return nil
	}
	if args[0] == "target-firewall-recover" {
		f := flag.NewFlagSet("target-firewall-recover", flag.ContinueOnError)
		root := f.String("root", "", "受管目标目录")
		job := f.String("job", "", "原防火墙作业ID")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return errors.New("NO_POSITIONAL_ARGUMENTS")
		}
		return deploy.RecoverTargetFirewall(*root, *job)
	}
	if args[0] == "target-exec" {
		if len(args) != 1 {
			return errors.New("NO_EXECUTOR_ARGUMENTS")
		}
		return deploy.Execute(os.Stdin, os.Stdout)
	}
	if args[0] == "binding-probe" {
		if len(args) != 1 {
			return errors.New("NO_PROBE_ARGUMENTS")
		}
		return deploy.RunBindingProbe(os.Stdin, os.Stdout)
	}
	if args[0] == "target-init" {
		f := flag.NewFlagSet("target-init", flag.ContinueOnError)
		root := f.String("root", "", "新的受管目标目录")
		cid := f.String("controller-id", "", "控制器ID")
		tid := f.String("target-id", "", "目标ID")
		socket := f.String("docker-socket", "/var/run/docker.sock", "已存在的Docker socket")
		dockerBinary := f.String("docker-binary", "/usr/bin/docker", "受保护的Docker入口")
		if e := f.Parse(args[1:]); e != nil {
			return e
		}
		if f.NArg() != 0 {
			return errors.New("NO_POSITIONAL_ARGUMENTS")
		}
		return deploy.InitTarget(*root, *cid, *tid, *socket, *dockerBinary)
	}
	if args[0] == "target-upgrade" {
		f := flag.NewFlagSet("target-upgrade", flag.ContinueOnError)
		root := f.String("root", "", "已有受管目标目录")
		binary := f.String("binary", "", "新的Linux执行器二进制")
		sha256 := f.String("sha256", "", "新二进制的可信SHA-256")
		cid := f.String("controller-id", "", "已有控制器ID")
		tid := f.String("target-id", "", "已有目标ID")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return errors.New("NO_POSITIONAL_ARGUMENTS")
		}
		backup, err := deploy.UpgradeTarget(*root, *binary, *sha256, *cid, *tid)
		if err != nil {
			return err
		}
		fmt.Printf("TARGET_UPGRADED: 旧执行器保留于 %s；请重新检查主机能力\n", backup)
		return nil
	}
	if args[0] == "target-repair" {
		f := flag.NewFlagSet("target-repair", flag.ContinueOnError)
		root := f.String("root", "", "已有受管目标目录")
		cid := f.String("controller-id", "", "已有控制器ID")
		tid := f.String("target-id", "", "已有目标ID")
		expected := f.String("expected-sha256", "", "原安装作业记录的可信执行器SHA-256")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return errors.New("NO_POSITIONAL_ARGUMENTS")
		}
		if err := deploy.RepairTargetPermissions(*root, *cid, *tid, *expected); err != nil {
			return err
		}
		fmt.Println("TARGET_PERMISSIONS_REPAIRED")
		return nil
	}
	if args[0] == "target-verify" {
		f := flag.NewFlagSet("target-verify", flag.ContinueOnError)
		root := f.String("root", "", "已有受管目标目录")
		cid := f.String("controller-id", "", "已有控制器ID")
		tid := f.String("target-id", "", "已有目标ID")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return errors.New("NO_POSITIONAL_ARGUMENTS")
		}
		if err := deploy.VerifyTarget(*root, *cid, *tid); err != nil {
			return err
		}
		fmt.Println("TARGET_IDENTITY_VERIFIED")
		return nil
	}
	if args[0] == "version" {
		fmt.Println(controller.Version)
		return nil
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	state := flags.String("state", "./state", "私有状态目录")
	listen := flags.String("listen", "127.0.0.1:19090", "固定监听地址")
	username := flags.String("username", "admin", "初始化管理员账号")
	passwordFile := flags.String("password-file", "", "非交互初始化使用的受保护密码文件")
	out := flags.String("out", "", "输出文件（必须不存在）")
	archive := flags.String("archive", "", "恢复归档")
	key := flags.String("key", "", "恢复密钥文件")
	cert := flags.String("tls-cert", "", "HTTPS证书路径")
	tlsKey := flags.String("tls-key", "", "HTTPS私钥路径")
	platformTokenFile := flags.String("platform-token-file", "", "Platform 服务身份令牌文件；不配置则关闭预览实例目录")
	stopTimeout := flags.Duration("timeout", 15*time.Second, "关闭等待时间，1s至2m")
	materialLibrary := flags.String("material-library", "", "本地材料库目录；serve默认读取程序相邻materials，缺失则跳过")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("不支持位置参数")
	}
	materialLibraryExplicit := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "material-library" {
			materialLibraryExplicit = true
		}
	})
	if (args[0] == "import-materials" || materialLibraryExplicit) && strings.TrimSpace(*materialLibrary) == "" {
		return errors.New("MATERIAL_LIBRARY_REQUIRED: 请使用--material-library指定有效目录")
	}
	if args[0] == "stop" {
		if *stopTimeout < time.Second || *stopTimeout > 2*time.Minute {
			return errors.New("STOP_TIMEOUT_INVALID: 等待时间须为1s至2m")
		}
		return controller.Stop(*state, *stopTimeout)
	}
	if args[0] == "restore" {
		if *archive == "" || *key == "" {
			return errors.New("restore 需要 --archive --key --state（新路径）")
		}
		return controller.Restore(*archive, *key, *state)
	}
	switch args[0] {
	case "serve", "init-admin", "backup", "export-key", "check-state", "import-materials":
	default:
		return errors.New("未知命令")
	}
	if args[0] == "serve" {
		host, _, err := net.SplitHostPort(*listen)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return errors.New("监听地址必须为明确的 IP 地址")
		}
		if (*cert == "") != (*tlsKey == "") {
			return errors.New("TLS_CERT_PAIR: 证书和私钥必须同时提供")
		}
		if !ip.IsLoopback() && *cert == "" {
			return errors.New("TLS_REQUIRED: 非回环监听必须配置 TLS；默认建议 SSH 隧道")
		}
		if ip.IsUnspecified() {
			return errors.New("LISTEN_EXPLICIT_IP: 请指定服务器 IP，不能使用通配监听")
		}
	}
	s, err := controller.Open(*state)
	if err != nil {
		if args[0] == "serve" && strings.Contains(err.Error(), "STATE_LOCKED") {
			return fmt.Errorf("%w\n请先执行 streamfusion-ops stop --state %q；前台实例也可在原终端按 Ctrl+C。不要删除锁文件", err, *state)
		}
		return err
	}
	defer s.Close()
	if args[0] == "check-state" {
		var count int
		if err = s.DB.QueryRow("SELECT count(*) FROM admins").Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return errors.New("ADMIN_REQUIRED")
		}
		fmt.Println("STATE_OK")
		return nil
	}
	switch args[0] {
	case "import-materials":
		return importMaterialLibrary(s, *materialLibrary, true, "", os.Stdout)
	case "init-admin":
		var count int
		if err = s.DB.QueryRow("SELECT count(*) FROM admins").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return errors.New("ALREADY_INITIALIZED: 管理员已存在")
		}
		var password string
		if *passwordFile != "" {
			info, e := os.Lstat(*passwordFile)
			if e != nil {
				return e
			}
			if !info.Mode().IsRegular() || info.Size() > 128 {
				return errors.New("PASSWORD_FILE_INVALID")
			}
			if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
				return errors.New("PASSWORD_FILE_PRIVATE: 密码文件权限须为0600")
			}
			data, e := os.ReadFile(*passwordFile)
			if e != nil {
				return e
			}
			password = strings.TrimRight(string(data), "\r\n")
		} else {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return errors.New("初始化需要本机终端交互，或 --password-file 指定0600私有文件")
			}
			fmt.Fprint(os.Stderr, "管理员密码: ")
			data, e := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if e != nil {
				return e
			}
			fmt.Fprint(os.Stderr, "再次输入: ")
			confirm, e := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if e != nil {
				return e
			}
			if string(data) != string(confirm) {
				return errors.New("两次密码不一致")
			}
			password = string(data)
		}
		if err = s.InitAdmin(*username, password); err != nil {
			return err
		}
		fmt.Println("管理员已初始化")
		return nil
	case "backup":
		if *out == "" {
			return errors.New("需要 --out")
		}
		return s.BackupTo(*out)
	case "export-key":
		if *out == "" {
			return errors.New("需要 --out")
		}
		f, e := os.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, e = f.Write(s.Key)
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil {
			os.Remove(*out)
			return e
		}
		return closeErr
	}
	var admins int
	if err = s.DB.QueryRow("SELECT count(*) FROM admins").Scan(&admins); err != nil {
		return err
	}
	if admins == 0 {
		return errors.New("ADMIN_REQUIRED: 请先用 init-admin 初始化管理员")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if err = importMaterialLibrary(s, *materialLibrary, materialLibraryExplicit, executable, os.Stdout); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return fmt.Errorf("LISTEN_FAILED: %w", err)
	}
	defer listener.Close()
	if !webui.Built() {
		return errors.New("WEB_NOT_BUILT: 请先运行构建脚本生成内嵌页面")
	}
	rotating, err := controller.NewLog(s.Dir)
	if err != nil {
		return err
	}
	defer rotating.Close()
	logger := log.New(io.MultiWriter(os.Stderr, rotating), "", log.LstdFlags|log.LUTC)
	handler, err := controller.NewHTTP(s, controller.OriginFor(*listen, *cert != ""), webui.Handler(), logger)
	if err != nil {
		return err
	}
	if err = handler.ConfigurePlatformTokenFile(*platformTokenFile); err != nil {
		return err
	}
	// Host preparation performs a bounded SSH upload/install in the request and
	// records UNKNOWN on disconnect. Keep the server deadline above its 3-minute
	// operation bound so the UI can receive the durable result.
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 240 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, ErrorLog: logger}
	stop, done := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer done()
	cleanupControl, err := s.LocalControl(stop, *listen, done)
	if err != nil {
		return err
	}
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); handler.RunOperations(stop) }()
	observationDone := make(chan struct{})
	go func() { defer close(observationDone); handler.RunObservations(stop) }()
	shutdownDone := make(chan struct{})
	defer func() { done(); <-workerDone; <-observationDone; <-shutdownDone; cleanupControl() }()
	go func() {
		defer close(shutdownDone)
		<-stop.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		logger.Printf("traceId=local event=STOPPING")
	}()
	logger.Printf("traceId=local event=STARTED release=%s listen=%s", controller.Version, *listen)
	if *cert != "" {
		err = server.ServeTLS(listener, *cert, *tlsKey)
	} else {
		err = server.Serve(listener)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func writeMaterialCatalog(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("material-catalog", flag.ContinueOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("NO_POSITIONAL_ARGUMENTS")
	}
	return json.NewEncoder(output).Encode(serviceinstall.Catalog())
}

func importMaterialLibrary(store *controller.Store, selected string, explicit bool, executable string, output io.Writer) error {
	if !explicit {
		selected = filepath.Join(filepath.Dir(executable), "materials")
		if _, err := os.Lstat(selected); os.IsNotExist(err) {
			return nil
		}
		if _, err := os.Lstat(filepath.Join(selected, "library.json")); os.IsNotExist(err) {
			return nil
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	result, err := store.ImportInstallationMaterialLibrary(ctx, selected)
	if outputErr := json.NewEncoder(output).Encode(result); outputErr != nil {
		return outputErr
	}
	return err
}
