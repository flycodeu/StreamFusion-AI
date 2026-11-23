# StreamFusion Ops

Ops 是独立的 Linux 运维控制器，用于管理主机、软件、端口和应用发布。使用自己的管理员账号和状态目录。

## 运行控制器

### Linux 发行包

系统安装支持 Ubuntu 24.04、amd64 和 systemd。先通过发布渠道核对可信摘要，再解包并验证文件。校验失败时停止安装。

```bash
sha256sum -c SHA256SUMS
HASH=$(awk '$2 == "streamfusion-ops" {print $1}' SHA256SUMS)
bash install-linux.sh --binary ./streamfusion-ops --sha256 "$HASH"
sudo bash install-linux.sh --binary ./streamfusion-ops --sha256 "$HASH" --apply
```

安装脚本先展示计划，带 `--apply` 时安装服务并初始化管理员。可用 `--help` 查看路径、状态目录和端口选项。也可在解包目录前台运行。

```bash
./streamfusion-ops init-admin --state ./state --username admin
./streamfusion-ops serve --state ./state --listen 127.0.0.1:19090
```

已有管理员时跳过 `init-admin`。管理员密码在终端输入，自动化可用权限为 0600 的 `--password-file`。

### 从源码构建

使用 Go 1.26.5、Node.js 24 和 pnpm 10.28.0。在 `operations` 目录运行 `bash scripts/build-linux.sh`，或在 Windows 执行 `powershell -ExecutionPolicy Bypass -File scripts/build-linux.ps1`，生成 `release/streamfusion-ops-0.2.0-linux-amd64.tar.gz`。在 Windows 本机开发时，用以下命令构建并启动。

```powershell
pnpm --dir web install --frozen-lockfile
pnpm --dir web build
node scripts/embed-web.mjs
go build -trimpath -o streamfusion-ops.exe ./cmd/streamfusion-ops
.\streamfusion-ops.exe init-admin --state .\state --username admin
.\streamfusion-ops.exe serve --state .\state --listen 127.0.0.1:19090
```

远程访问可使用 `ssh -L 19090:127.0.0.1:19090 user@server`。监听指定的非回环地址时，需通过 `--tls-cert`、`--tls-key` 提供可信证书和私钥。默认控制台地址为 [http://127.0.0.1:19090](http://127.0.0.1:19090)。

前台运行按 `Ctrl+C` 退出，也可执行 `streamfusion-ops stop --state <实际状态目录>`。systemd 安装使用 `sudo systemctl stop streamfusion-ops`。如需卸载，用发行包中的 `sudo bash uninstall-linux.sh --prefix /opt/streamfusion-ops --apply`。卸载脚本会保留状态目录。

## 接入主机

1. 在「主机管理」新增 Linux 主机，选择 SSH 凭据。通过服务器控制台或管理员确认指纹后保存。
2. 点击「检查能力」，分别查看 SSH、只读观察、变更权限和 Docker。检测超时时先重查。Docker 已检出时，可进一步核对执行器能否访问 Engine。
3. 在「安装准备」在线安装接入组件，或下载该主机专用的离线包，上传并解压后运行 `bash install.sh`，再回页面检查结果。已有受管主机可在同页更新组件。需要 sudo 时使用当前 SSH 账号的密码。

默认受管目录为 `/opt/streamfusion-ops-target`。组件接入会在 Ubuntu 24.04 上准备 `mysql`、`redis-cli`、`psql`、`curl` 和 `sf-docker`。缺少宿主机客户端时需要可用的 APT 软件源。现有主机更新组件，或在「终端工具维护」检查并修复工具。

## 安装和管理软件

- **安装新软件**。在「软件中心 → 软件安装」选主机、软件、版本和方式，核对依赖、凭据引用、实际目录与端口后确认。Docker 可以拉取固定镜像或使用离线材料。原生配方按页面声明使用官方安装或离线材料。Java、Maven、Python 等工具也可用本机已有的 Docker 镜像。[材料库说明](https://github.com/flycodeu/StreamFusion-AI/blob/main/operations/materials/README.md)
- **登记已有软件**。在「软件管理」自动识别或手动登记。只读检查可以先了解运行状态，确认接管后才能执行受管操作。软件详情可复制与当前安装方式、地址和端口匹配的终端命令。
- **变更配置**。在软件详情中读取实际配置，预览差异后保存。支持的字段和管理动作以各软件已发布模板为准。
- **解除管理和卸载**。解除管理只撤销 Ops 绑定。卸载仅支持归属已确认的 Ops 安装，默认保留业务数据。

安装 Docker 软件时，同一笔作业会检查宿主机终端命令，Ubuntu 24.04 缺项时自动准备。作业因连接中断显示待确认时，先核对原作业。

## 端口和发布

### 项目维护与项目发布

在「项目维护」选择项目，设置一次 Jenkins 实例、账号和 API Token。Jenkins 需要 Pipeline、Git 插件。账号需要 Overall/Read、Job/Create、Job/Read、Job/Build，以及读取作业配置的 Job/ExtendedRead 或 Job/Configure。构建节点须能拉取项目仓库，并提供模块需要的 Linux 构建工具。

| 维护项 | 用途 |
| --- | --- |
| Git 仓库、分支、模块源码目录 | 指定构建的代码 |
| 模块类型 | Java 后端使用 Maven Wrapper 测试、打包 JAR。前端按锁文件使用 pnpm 或 npm 测试、构建 dist。算法和识别为 Python 的其他模块使用 uv、pytest 测试并归档源码和测试报告 |
| 运行配置 | 为上线选择环境、运行服务器和该服务器上的受管实例。要查看服务日志，可选择实际运行该模块的容器或 systemd 服务 |

在「项目发布」左侧选项目，右侧按模块操作。

- **QA**。点击后 Ops 首次自动创建对应的 Jenkins 作业，后续复用。页面显示排队、构建阶段及制品；尚未选运行服务器的模块也可做 QA。
- **QA 日志**。打开对应构建的控制台输出，构建号来自 Jenkins。
- **上线**。从「版本制品」导入已验证的制品后，点击模块行的「上线」，选择制品、核对预检并确认。系统使用项目维护中保存的上线目标。
- **验证**。向已配置的上线目标查询当前运行状态，查看检查时间和结果。
- **服务日志**。查看模块所配置的运行服务输出；Jenkins 自身的维护日志位于「软件管理」。

- **端口管理**。在已有 UFW 的 Ubuntu 主机上查看监听与规则，按来源和 TCP/UDP 维护 Ops 创建的规则。
- **Java 应用发布**。在「版本制品」上传符合配方的 Docker save 归档，或上传可执行 JAR 并选择受支持的 Java 21 环境。在「项目发布」完成预检并发布。可回退上一成功版本，业务数据按自身备份恢复。应用 Compose 发布需要 Docker Engine 和 Compose。
- **预览节点**。单独扩容时，按节点手册部署、在平台引入并启用。[预览节点操作手册](https://github.com/flycodeu/StreamFusion-AI/blob/main/operations/preview-nodes.md)

## 备份与故障定位

网页「系统设置」可下载状态备份。CLI 备份、导出密钥和恢复须先停止控制器。

```bash
./streamfusion-ops backup --state ./state --out ./backup.tar.gz
./streamfusion-ops export-key --state ./state --out ./recovery.key
./streamfusion-ops restore --archive ./backup.tar.gz --key ./recovery.key --state ./restored-state
```

备份包含状态数据库和加密凭据。主密钥、镜像归档和安装材料应分别保管。恢复使用新的状态目录。

启动故障查看状态目录下的 `logs/controller.log` 和 `/ops/api/v1/health`。出现 `STATE_LOCKED` 时，通过原状态目录停止占用它的进程。安装、发布或回退结果待确认时，在原作业查看执行阶段和输出，再核对服务器状态。

## 开发检查

以下检查在 `operations` 目录运行。

```bash
pnpm --dir web test
pnpm --dir web build
node scripts/embed-web.mjs
go test -timeout 15m ./...
go vet ./...
```

Go 命令入口在 `cmd/streamfusion-ops`，控制器和目标执行器分别在 `internal/controller`、`internal/deploy`，界面在 `web/src`。
