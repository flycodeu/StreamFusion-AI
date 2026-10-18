package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/streamfusion-ai/operations/internal/controller"
	"github.com/streamfusion-ai/operations/internal/deploy"
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
		return errors.New("用法: streamfusion-ops <serve|stop|init-admin|backup|restore|export-key|version> --help")
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
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("不支持位置参数")
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
	case "serve", "init-admin", "backup", "export-key", "check-state":
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
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, ErrorLog: logger}
	stop, done := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer done()
	cleanupControl, err := s.LocalControl(stop, *listen, done)
	if err != nil {
		return err
	}
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); handler.RunOperations(stop) }()
	shutdownDone := make(chan struct{})
	defer func() { done(); <-workerDone; <-shutdownDone; cleanupControl() }()
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
