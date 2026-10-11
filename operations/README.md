# StreamFusion Ops

独立的 Linux 运维控制器。当前实现范围为 OPS-I01：启动、登录、控制器状态、凭据保护、审计和备份恢复。运行时只需要一个 Go 二进制，不依赖 Java、Python、Node、Docker、MySQL 或 Redis。

## 构建与运行

开发构建需要 Go 1.26.5、Node 24、pnpm 10.28.0。构建入口会先构建网页，再嵌入 Go 程序；不要直接构建未生成网页的检出目录。

在Windows开发机可执行 `powershell -ExecutionPolicy Bypass -File scripts/build-linux.ps1`，只生成Linux amd64发行包，不安装Windows服务。Linux开发机执行下面的Bash构建入口。二进制运行不需要这些开发工具。

```bash
cd operations
bash scripts/build-linux.sh
./release/0.1.0/streamfusion-ops init-admin --state ./state --username admin
./release/0.1.0/streamfusion-ops serve --state ./state --listen 127.0.0.1:19090
```

管理员密码在本机终端隐藏输入，长度6–64字节且包含英文字母和数字。非交互初始化可使用权限0600的临时密码文件（`--password-file`），不用命令参数传递密码。已有管理员时拒绝再次初始化。

Windows 本机开发运行可在 `operations` 目录执行：

```powershell
pnpm --dir web install --frozen-lockfile
pnpm --dir web build
node scripts/embed-web.mjs
go build -trimpath -o streamfusion-ops.exe ./cmd/streamfusion-ops
.\streamfusion-ops.exe init-admin --state .\state --username admin
.\streamfusion-ops.exe serve --state .\state --listen 127.0.0.1:19090
```

已有管理员时跳过 `init-admin`。Windows 仅提供开发运行，不提供服务安装。

默认只监听回环。远程访问可使用 `ssh -L 19090:127.0.0.1:19090 user@server`，然后打开本地19090。指定非回环IP时需要 `--tls-cert` 和 `--tls-key`；不接受通配监听或转发头。

## Linux 安装

面向 Ubuntu 24.04 / systemd / amd64。安装命令和预检详见脚本的 `--help`。未安装任何全局语言环境，也不触碰业务服务。

```bash
# 在可信构建环境取得SHA-256；不要使用不可信来源提供的摘要。
HASH=$(sha256sum release/0.1.0/streamfusion-ops | cut -d' ' -f1)
bash scripts/install-linux.sh --binary release/0.1.0/streamfusion-ops --sha256 "$HASH"
sudo bash scripts/install-linux.sh --binary release/0.1.0/streamfusion-ops --sha256 "$HASH" --apply
```

第一条安装命令只展示计划，第二条执行同一计划并在本机初始化管理员。可指定 `--prefix`、`--state`、`--port`；路径须为不含空格的规范绝对路径。需要 systemd、curl、sha256sum、useradd、runuser 等系统工具。卸载执行 `sudo bash scripts/uninstall-linux.sh --prefix /opt/streamfusion-ops --apply`，保留状态和服务账号。

发行包只含二进制、SHA256SUMS、安装/卸载脚本和说明。将包传到Linux目标后解包，在解包目录执行 `sha256sum -c SHA256SUMS`；文件内摘要须先与可信发行渠道核对，再执行 `sudo bash install-linux.sh --binary ./streamfusion-ops --sha256 <可信二进制摘要> --apply`。

## 备份与恢复

在线可在“控制器设置”下载一致性备份。CLI 备份、导出密钥和恢复需要停止控制器，以取得目录锁。

```bash
./streamfusion-ops backup --state ./state --out ./backup.tar.gz
./streamfusion-ops export-key --state ./state --out ./recovery.key
./streamfusion-ops restore --archive ./backup.tar.gz --key ./recovery.key --state ./restored-state
```

备份包含SQLite一致性快照和加密凭据，**不包含主密钥**。密钥必须单独保管。恢复只能创建不存在的目标目录；摘要、数据库完整性、外键、控制器身份和凭据解密全部通过后才完成。旧会话全部失效。摘要用于检测损坏，不代替来源可信性。

## 代码入口

- `cmd/streamfusion-ops/main.go`：本机命令和HTTP服务生命周期。
- `internal/controller/`：按职责分文件的SQLite、认证、凭据、备份、日志；没有通用工作流或插件框架。
- `internal/webui/web.go`：内嵌网页。
- `web/src/`：四个真实页面：总览、凭据、操作记录、控制器设置。
- `scripts/`：构建和Linux安装/卸载。

认证与业务平台独立。匿名会话10分钟；登录会话闲置30分钟、绝对8小时；连续5次失败锁定15分钟；全局每分钟最多60次密码校验。改密清除全部登录会话。写接口检查来源和CSRF；Session Cookie 为HttpOnly / SameSite Strict，HTTPS时开启Secure。日志最多20MiB×5，凭据修订最多500条，会话最多2000条。

## 检查与故障定位

```bash
go test ./...
go vet ./...
cd web && pnpm build
```

构建二进制后，Linux集成验收入口为 `sudo bash scripts/test-linux.sh /absolute/path/to/streamfusion-ops`；它使用唯一的测试单元、账号和目录，并在完成后清理。浏览器验收入口为 `python scripts/test-browser.py --binary ./streamfusion-ops.exe --output ../.run/ops-browser-new`，需要已有 Python/Playwright/Chromium，仅用于开发测试。

看进程日志及状态目录下 `logs/controller.log`。目录锁冲突返回STATE_LOCKED；端口冲突返回LISTEN_FAILED；高版本数据库返回SCHEMA_TOO_NEW；缺少已有凭据密钥返回KEY_MISSING。现行Schema为1，高版本状态库不能由旧程序打开。

## Git提交范围

可以提交整个 `operations/` 中未被忽略的源码、测试、schema.sql、Go依赖清单、前端依赖锁、脚本和说明。运行前先检查 `git status --short --ignored operations`。

禁止提交状态库及WAL、主密钥、凭据材料、密码文件、TLS密钥、日志、备份、安装产物、node_modules、生成网页和IDE配置。不要使用 `git add -f`。

## 当前范围

当前为 I01 控制器底座。主机注册、Docker安装、Java部署、Jenkins、源码构建、业务发布与回滚尚未实现。Linux安装范围为Ubuntu24.04/systemd/amd64，Windows仅支持本机开发运行。

`0.1.0` 是当前程序内版本。
