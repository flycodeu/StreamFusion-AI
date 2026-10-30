# StreamFusion Ops

独立的 Linux 运维控制器，包含主机连接、已有软件管理、UFW端口管理、六种服务和Java/Maven/Python/FFmpeg/编程工具安装、配置维护及单实例发布链。支持固定配方的Docker和Ubuntu24.04原生安装；工程实现、Linux实机和负责人验收分别记录。运行时只需一个Go二进制；应用Compose发布需Engine和Compose，普通Docker安装只需Engine，原生安装检查各自依赖。

## 构建与运行

开发构建需要 Go 1.26.5、Node 24、pnpm 10.28.0。构建入口会先构建网页，再嵌入 Go 程序；不要直接构建未生成网页的检出目录。

在Windows开发机可执行 `powershell -ExecutionPolicy Bypass -File scripts/build-linux.ps1`，只生成Linux amd64发行包，不安装Windows服务。Linux开发机执行下面的Bash构建入口。二进制运行不需要这些开发工具。

```bash
cd operations
bash scripts/build-linux.sh
./release/0.2.0/streamfusion-ops init-admin --state ./state --username admin
./release/0.2.0/streamfusion-ops serve --state ./state --listen 127.0.0.1:19090
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

前台运行可在启动终端按 `Ctrl+C` 正常退出。也可在另一个终端关闭使用同一状态目录的控制器：

```powershell
.\streamfusion-ops.exe stop --state .\state
```

`stop` 等待状态锁释放后才返回，默认最多等待15秒，可用 `--timeout 30s` 调整。关闭后可以重新执行 `serve`。该命令不删除账号、配置、凭据或状态库；重复关闭返回“控制器已停止”。运行中的发布若因退出失去确认，重启后按原作业核对，不自动重发。

Linux 手动运行同样使用 `./streamfusion-ops stop --state /实际状态目录`；systemd 安装则执行 `sudo systemctl stop streamfusion-ops`，自定义名称替换为对应服务名，避免服务管理器按重启策略再次启动。

默认只监听回环。远程访问可使用 `ssh -L 19090:127.0.0.1:19090 user@server`，然后打开本地19090。指定非回环IP时需要 `--tls-cert` 和 `--tls-key`；不接受通配监听或转发头。

## Linux 安装

面向 Ubuntu 24.04 / systemd / amd64。安装命令和预检详见脚本的 `--help`。未安装任何全局语言环境，也不触碰业务服务。

```bash
# 在可信构建环境取得SHA-256；不要使用不可信来源提供的摘要。
HASH=$(sha256sum release/0.2.0/streamfusion-ops | cut -d' ' -f1)
bash scripts/install-linux.sh --binary release/0.2.0/streamfusion-ops --sha256 "$HASH"
sudo bash scripts/install-linux.sh --binary release/0.2.0/streamfusion-ops --sha256 "$HASH" --apply
```

第一条安装命令只展示计划，第二条执行同一计划并在本机初始化管理员。可指定 `--prefix`、`--state`、`--port`；路径须为不含空格的规范绝对路径。需要 systemd、curl、sha256sum、useradd、runuser 等系统工具。卸载执行 `sudo bash scripts/uninstall-linux.sh --prefix /opt/streamfusion-ops --apply`，保留状态和服务账号。

发行包包含二进制、SHA256SUMS、安装/卸载脚本和说明，可选附带 `materials/` 离线材料库。将包传到Linux目标后解包，在解包目录执行 `sha256sum -c SHA256SUMS`；文件内摘要须先与可信发行渠道核对，再执行 `sudo bash install-linux.sh --binary ./streamfusion-ops --sha256 <可信二进制摘要> --apply`。

## 备份与恢复

在线可在“系统设置”下载一致性备份。CLI 备份、导出密钥和恢复需要停止控制器，以取得目录锁。

```bash
./streamfusion-ops backup --state ./state --out ./backup.tar.gz
./streamfusion-ops export-key --state ./state --out ./recovery.key
./streamfusion-ops restore --archive ./backup.tar.gz --key ./recovery.key --state ./restored-state
```

备份包含SQLite一致性快照和加密凭据，**不包含主密钥及镜像归档**。镜像缓存和目标发行材料须另行保留；恢复后缺少归档时拒绝发布和回滚。密钥必须单独保管。恢复只能创建不存在的目标目录；摘要、数据库完整性、外键、控制器身份和凭据解密全部通过后才完成。旧会话全部失效。摘要用于检测损坏，不代替来源可信性。

## 代码入口

- `cmd/streamfusion-ops/main.go`：本机命令和HTTP服务生命周期。
- `internal/controller/`：SQLite、认证、凭据、备份、主机连接、软件纳管、端口规则、固定配方和发布作业，按职责分文件。
- `internal/webui/web.go`：内嵌网页。
- `web/src/`：运行总览、主机管理、端口管理、软件管理、应用实例、版本制品、发布作业、连接凭据、操作记录和系统设置。主机连接、软件登记、软件详情与安装配方独立维护。
- `scripts/`：构建和Linux安装/卸载。

认证与业务平台独立。匿名会话10分钟；登录会话闲置30分钟、绝对8小时；连续5次失败锁定15分钟；全局每分钟最多60次密码校验。改密清除全部登录会话。写接口检查来源和CSRF；Session Cookie 为HttpOnly / SameSite Strict，HTTPS时开启Secure。日志最多20MiB×5，凭据修订最多500条，会话最多2000条。

## 检查与故障定位

```bash
go test ./...
go vet ./...
cd web && pnpm build
```

构建二进制后，Linux集成验收入口为 `sudo bash scripts/test-linux.sh /absolute/path/to/streamfusion-ops`；它使用唯一的测试单元、账号和目录，并在完成后清理。浏览器验收需要已有 Python/Playwright/Chromium，仅用于开发测试；输出到仓库外的临时目录，检查结果后清理。Windows 示例：

```powershell
python scripts/test-browser.py --binary ./streamfusion-ops.exe --output "$env:TEMP/sfops-browser-$(New-Guid)"
```

看进程日志及状态目录下 `logs/controller.log`。目录锁冲突返回STATE_LOCKED；端口冲突返回LISTEN_FAILED；高版本数据库返回SCHEMA_TOO_NEW；缺少已有凭据密钥返回KEY_MISSING。现行Schema为14，增量维护安装/配置、防火墙、主机接入及软件卸载作业；原主机ID、凭据、实例、观察、作业和历史发布关系保留。每次增量升级前生成对应 `before-schema-N` 一致性归档，再事务升级，旧迁移摘要保持不变。旧程序拒绝打开高版本数据库；代码回退须用原密钥与对应升级前备份恢复到新目录。备份不包含大镜像归档，恢复后文件缺失会阻断新安装，不能把数据库恢复当作材料齐备。

`STATE_LOCKED` 表示该目录仍被进程持有，锁文件存在本身不表示故障，不能通过删除它关闭服务。优先用 `stop --state`；旧版后台实例未提供本地关闭记录时返回 `STOP_UNAVAILABLE`，需核实监听端口、可执行文件和启动参数后关闭对应进程，随后使用新二进制。

## Git提交范围

可以提交整个 `operations/` 中未被忽略的源码、测试、schema.sql、Go依赖清单、前端依赖锁、脚本和说明。运行前先检查 `git status --short --ignored operations`。

禁止提交状态库及WAL、主密钥、凭据材料、密码文件、TLS密钥、日志、备份、安装产物、node_modules、生成网页和IDE配置。不要使用 `git add -f`。

## 当前范围

主机管理提供新增、编辑、指纹确认、检查能力、维护和受引用保护的移除登记。填写名称、项目目录、IP或域名、SSH端口、账号，选择私钥或密码凭据；修订号由服务端绑定。指纹通过服务器控制台或管理员核对后确认，后续SSH连接严格校验。高级配置可选择控制器本机。

项目目录沿用主机唯一的 `installRoot`，默认 `/opt/streamfusion-ops-target`；可选择 `/opt`、`/srv` 或 `/var/lib` 下的专用规范子目录，最长240字符。目录在主机详情和安装计划可见：`instances/` 保存新应用版本与数据，`plugins/` 保存环境记录，`packages/` 保存离线材料，`jobs/` 保存持久回执。Docker镜像仍由Docker存储。登记只保存路径，首次授权部署时按相同路径初始化executor；不搬迁已有软件。已有部署/软件引用或未决责任时禁止切换目录；仅编辑名称而省略目录保持原路径。

SSH连通、只读观察、执行器变更授权、Docker/Compose能力分别显示结果与观测时间。5分钟后仍显示上次结果并标为待更新；变更授权的有效期独立校验。无Docker、未安装执行器也可登记并查看主机；部署仍须通过具体配方预检。四项能力检查仍按需执行；后台资源与已登记软件观察独立运行。全局SSH连接池和日志游标尚未实现。

运行总览按已登记主机展示资源与系统信息。Ops启动后后台约每30秒采集资源、每60秒检查已登记软件，不依赖浏览器或登录会话。每台Linux主机使用固定只读脚本采集CPU/内存/根分区/负载/运行时间/逐网卡流量，不使用sudo；CPU和速率取约1秒差值，计数回退不给速率。共享4个检查槽，纯后台最多占3槽，同主机仅1个观察周期；单探针10秒、周期20秒，资源16KiB、软件240KiB，软件每块最多100项。失败按30/60/120/300秒退避并加抖动；认证/指纹失败暂停自动认证，修正连接配置或恢复监测后重试。只读周期使用专用SSH连接，尚无跨周期连接池。

最近确认结果和时间、最近尝试和错误分别保存在SQLite，重启或离页不清空。资源90秒、软件3分钟后标为待更新，仍显示上次数值/状态；最新采集失败立即提示当前无法确认，不当作软件已停止。主机连接身份变化隔离旧数据并拒绝在途旧结果；改名称或维护状态不清空资源。观察修订与配置版本分离，不自动改绑定身份或解除UNKNOWN作业责任。Windows本机不支持完整Linux资源采集；没有历史趋势或GPU专项采集。

页面只读取持久快照：`GET /ops/api/v1/host-observations`、`GET /ops/api/v1/hosts/{id}/observation`；原`/{id}/metrics`也只读快照。软件中心“一键检测”可选当前筛选或全部有效登记，单批1–1000项，服务端冻结范围、分块执行并保留进度；相同在途范围合并，重启恢复只读排队项，取消仅影响未执行项。批次接口为`POST/GET /ops/api/v1/observation-batches`及`GET /{id}`、`POST /{id}/cancel`；主机监测通过`PATCH /ops/api/v1/hosts/{id}/monitoring`暂停/恢复。批次只向发起账号开放，写操作沿用会话、Origin、CSRF和配置核验；最多保留7天/1000批/10万明细及10万幂等回执，未结束批次最多100个。暂停自动监测仍允许手动检测。软件与端口展示最近确认事实，权限不足留空，不代填默认值。

主机维护暂停新的Ops变更预检、提交和排队准入，已有软件继续运行，运行中或未知作业继续核对。编辑连接受版本校验及未决作业保护；存在部署记录时不能把原主机改指向其他服务器或目录。移除登记不会卸载软件，有引用或历史责任时拒绝移除。

主机API统一为 `/ops/api/v1/hosts`，详情/编辑/删除使用 `/{id}`，指纹发现使用 `/fingerprint`，能力检查使用 `/{id}/checks`，维护使用 `/{id}/maintenance`，插件预检使用 `/{id}/plugin-install-plans`。原 `/targets` 主机入口已移除；调用方需切换，实例和作业中的稳定 `targetId` 保持。Platform预览交接接口保持原路径。

I02 当前实现本机/SSH Linux目标登记、固定 Java Compose 制品发布和应用回滚；S4另接入预览节点固定配方、维护许可与版本回退，操作见[预览节点维护](preview-nodes.md)，工程验证与负责人验收分开。Docker Engine 可从主机或软件目录发起固定官方包安装，真实系统安装仍需实机验收；项目组合发布、应用源码构建及Jenkins尚未实现。I02仍需当前项目后端和真实第二台主机验收。Linux安装范围为Ubuntu24.04/systemd/amd64，Windows仅支持本机开发运行。

软件管理分为“软件中心”和“软件目录”。软件中心使用卡片，主机和软件详情按标签组织，底部操作固定。已有 systemd 服务与 Docker 容器可先只读登记，经普通 SSH 固定探针检查身份、状态和时间；不要求先安装执行器或 Docker。Docker 观察仍需要实际 socket 读取权限，systemd 使用明确的规范 unit 名称。Java、Python、FFmpeg 工具只检查固定系统命令，不提供启停。声明版本与实际观测版本分别展示，SSH 不通不会显示为已停止。卡片将状态、版本、主机和端口放在主视图；路径及身份依据进入详情标签，发现结果在弹窗内滚动。

“自动识别”选择主机后按需读取已安装unit、容器和固定工具，候选可带入登记表单；扫描本身不创建记录或接管服务。识别MySQL/MariaDB、Redis、ZLM、MediaMTX（含rtsp-simple-server）、MinIO、SeaweedFS、Nginx、Jenkins、Java、Python、FFmpeg的明确名称或镜像仓库基名；自定义命名可手动登记。systemd通过unit文件归属读取dpkg/rpm安装包版本，明确标记来源，不能证明运行进程已加载该版本。可读时补充MainPID的程序路径及其监听端口；普通SSH权限可能无法取得，不请求sudo，也不以默认端口填补。Docker显示镜像引用和实际返回的端口信息，不进入容器执行版本命令，镜像tag不代表程序版本。常见MySQL/Redis配置只提取明确的端口、数字绑定地址和位置，作为主机级配置线索；include、自定义位置或启动参数可能覆盖它们，不能当作实际有效配置或协议健康。不会读取密码、容器环境变量或完整配置正文。

发现API为 `POST /ops/api/v1/hosts/{id}/software-discovery`，请求携带 `expectedVersion`，响应包含候选、配置线索、各来源状态、时间和不完整标记。候选及软件观察可附带 `versionSource`、`versionStatus`、`packageName`、`executablePath`、`serviceFile`、`listeningPorts`。请求复用一条SSH连接，四来源独立Session，整体20秒、候选100、报告64KiB；unit元数据最多24项，超过保留候选并标记未探测。某源无权限/缺失不覆盖其余结果，辅助元数据不改变生命周期身份或实际运行状态。Windows控制器本机不能伪装成Linux扫描，需登记远程Linux主机。当前不是后台持续发现，也没有全局SSH连接池。

确认接管后，启停通过受保护的目标执行器完成；容器冻结真实 ID，unit 冻结定义文件身份，不自动切换到同名重建对象。目标必须报告 `softwareLifecycleVersion=1`，不支持时先升级执行器，不会建立未决软件作业。变更沿用版本校验、幂等、主机锁、UNKNOWN 核对和 ACK。日志只读取最近200行并有字节上限，常见凭据字段脱敏；第三方程序仍应避免向自身日志写入秘密。解除管理只撤销绑定，保留远端程序、配置和业务数据。Ops 尚无 Platform 业务依赖图，停止外部服务前需由操作者确认业务影响。

软件 API 为 `/ops/api/v1/software-instances`，详情使用 `/{id}`，检查/接管/启停/日志/解除分别使用 `/{id}/observe`、`adopt`、`actions`、`logs`、`unmanage`。写操作沿用登录、来源、CSRF、记录版本和幂等校验。新工具安装使用统一安装计划，旧工具记录及发布引用保留。

软件目录按十三种软件展示卡片，Java可选择8/17/21；分类包含服务软件、容器引擎、运行环境、构建工具、媒体工具和编程软件。首次打开优先选择随包材料已就绪的版本，手动选择后保持该选择。Java/Maven/Python/FFmpeg/Codex CLI/Claude Code为工具，安装后显示“已安装”和程序/环境路径，没有监听端口或启停按钮。Docker Java21工具沿用原JAR发布消费者；原生JDK/Maven不自动替换已有Compose发布环境。主机连接、登记和安装使用抽屉，页头和操作固定，窄屏单列。

## 端口管理

在“端口管理”选择主机并检测，分别查看监听端口、访问规则和操作记录。支持已有UFW的Ubuntu/systemd/linux amd64，TCP/UDP单端口或至多100个连续端口，来源可选IPv4/IPv6网段。新增放行/禁止、撤销Ops创建的规则，支持UFW启停；人工规则和默认策略只读。不安装UFW、不停止端口对应程序、不清空现有连接。

新变更需要当前受管执行器的`firewallVersion=2`；修订1须从主机安装准备升级，既有未决作业仍按原回执核对。无执行器时可查看可读的监听端口，防火墙变更提示安装准备；观察失败保留最近确认快照并报告最近错误。已有firewalld或独立nftables服务活动时拒绝变更，Docker发布端口单独展示，涉及相同协议发布端口的新增UFW规则拒绝执行，避免误报阻断；TCP/UDP同号端口分别判断。

规则变更先预检、冻结5分钟计划，再按原幂等标识提交Job。SSH实际来源与监听端口、Ops入口受保护，不假定SSH端口22；启用前先通过“保护SSH”创建管理放行，已有可能遮挡SSH的拒绝/限流或复杂规则需管理员先核对。全局启停需目标systemd独立恢复定时器就绪，并在120秒内以新管理连接“确认保持”。定时器保存绝对UTC期限及Persistent，目标重启不重新授予完整确认窗口。首次目标回执丢失时，核对原作业可恢复原确认入口及期限。逾期仅在规则/策略未变化时尝试恢复原启停状态；冲突保留UNKNOWN，不能重发或删除锁。本机全局启停只支持回环控制器入口。自定义包过滤、云防火墙与端到端可达须另行核对。

软件配置与防火墙共用主机作业锁，任一方未收尾时阻止另一方变更。软件修改监听/映射后保留旧访问规则，预检界面提示到端口管理重新检测并按需调整；不自动放行新端口。规则回执同时校验入站方向、来源/目标、协议、端口和精确标记，出站或转发规则不能冒充入站放行。

内部会话API：`GET /hosts/{id}/ports`、`POST /hosts/{id}/ports-observe`、`POST /hosts/{id}/firewall-plans`、`POST /firewall-plans/{id}/submit`、`POST /jobs/{id}/firewall-confirm`；沿用原Job日志/核对/排队取消、Origin/CSRF与审计。定位查看页面traceId、作业回执、控制器日志及目标`sfops-fw-<job>.service`日志。Windows隔离测试不替代真实Linux包过滤、SSH新连接或到期恢复验收。

## 软件安装与配置

材料库制作、启动导入及发行打包见[材料库说明](materials/README.md)。当前固定随包材料面向 Ubuntu24.04/amd64；其他容器离线包可将固定配方的 Docker-save 导入同一库。五个 Docker 包仍要求主机已有 iptables、nftables、libc6、libseccomp2、libsystemd0，不在线补装系统依赖。

“卸载软件”独立于“解除管理”：仅支持 Ops 安装且归属已确认的对象，默认保留配置、数据、日志与备份；勾选“同时删除数据”后显示精确实例路径，并要求输入软件名称。历史软件记录、安装计划、作业与审计仍保留。Docker有容器或受管依赖时拒绝卸载；停止包含 service/socket，已停止引擎需先启动核对依赖。`/var/lib/containerd` 始终保留。保留目录后的再次安装不会覆盖原数据，恢复复用需管理员另行评估。新增卸载要求目标执行器 `softwareUninstallVersion=1`；已接入主机可显式点击“更新接入组件”，打开页面不会自动升级。

入口为“软件管理 → 软件目录”，选择主机后点击“安装”。固定配方包括：

| 软件 | 原生材料 | Docker |
|---|---|---|
| SeaweedFS4.48 | 官方Linux二进制，单机S3 | 发布方固定镜像 |
| MySQL8.4.11 | 官方generic binary | 官方固定镜像 |
| Redis7.4.11 | 官方源码，固定make | 官方固定镜像 |
| Nginx1.30.6 | 官方源码，固定configure/make | 官方固定镜像 |
| MediaMTX1.21.2 | 官方Linux二进制 | 发布方固定镜像 |
| ZLMediaKit2026.10.21配方 | 固定commit和全部必要子模块源码 | 官方仓库固定digest，构建身份独立显示 |
| Java17/21 | Temurin官方JDK | 沿原Temurin JRE固定镜像 |
| Maven3.9.11 | 官方Maven与私有Temurin JDK21组合 | 沿原Maven+JDK21固定镜像 |
| Python3.12.15 | 官方源码，独立prefix，非root编译 | 官方Python固定镜像 |
| FFmpeg9.0.2 | 官方源码，非root编译，含ffprobe/libx264 | 不提供非官方镜像 |
| Codex CLI0.163.0 | 官方固定Linux musl程序包 | 不提供 |
| Claude Code2.1.304 | 官方固定Linux原始二进制 | 不提供 |
| Docker Engine29.9.2 | Docker官方CE/CLI/containerd/Buildx/Compose五个DEB | 主机引擎 |
| Redis7.0.15 / Nginx1.24.0 | Ubuntu24.04官方DEB，提取到独立实例目录 | 此版本仅原生 |
| Java8u504-b01 | Temurin官方JDK8 | 此版本仅原生 |

MinIO社区仓库已归档，本轮仅识别/登记已有systemd或Docker实例，明确接管后沿原启停/日志流程；不提供已撤回官方渠道的安装入口，不引入AIStor。已有对象、账号和配置不被登记操作修改。自定义unit名称可手动登记。

FFmpeg/Python原生构建只使用已有编译依赖，预检缺项会提示，绝不自动安装系统依赖或覆盖系统Python。FFmpeg验证libx264与ffprobe，Python验证SSL/SQLite/bz2/lzma/ctypes/venv/ensurepip。源码构建与新工具探针使用专用非root账号；工具的“已安装”不表示有业务任务运行。

编程软件只安装与检测版本，不收集供应商账号/API Key或代执行任务。安装后用展示的程序路径，在自己的SSH会话完成登录；版本探针使用隔离HOME和干净环境，关闭自动更新。Claude离线材料直接选择官方原始二进制，其他单部件使用官方原包，均按内置摘要校验。

来源：[FFmpeg](https://ffmpeg.org/download.html)、[Python](https://www.python.org/downloads/release/python-31215/)、[Codex CLI](https://developers.openai.com/codex/cli/)、[Claude Code](https://code.claude.com/docs/en/setup)、[MinIO社区仓库](https://github.com/minio/minio)。

原生安装面向Ubuntu24.04/systemd/amd64。Redis、Nginx、ZLM由专用非root账号构建，预检列出缺失工具/开发库，不自动安装系统依赖、创建兼容库软链接或临时下载源码。程序、配置、数据按实例隔离；原生服务采用受管systemd unit，工具不伪装常驻服务。

步骤为“安装来源 → 实例配置 → 网络端口 → 预检确认 → 执行结果”；Docker Engine和工具不显示无关端口/密码。默认从离线材料库选择已核验文件；可选择在线获取或上传入库，后续安装复用已有记录。材料显示软件、版本、发布方、原文件、依赖和直接来源链接，摘要进入折叠详情。Docker在线由目标拉固定digest；原生在线先由Ops下载并验证官方材料，再传给目标，目标无需外网。多部件原生材料可在有网机器准备：

```bash
./streamfusion-ops native-package --recipe zlm --out ./zlm-native.tar
./streamfusion-ops native-package --recipe maven --out ./maven-native.tar
```

单部件直接上传官方原包；Java可加`--recipe-version 17`。来源、架构、摘要及构建参数由内置配方约束，不接受自填下载URL、镜像或root脚本。单包最多2GiB、总计100项/16GiB、同时2路传输或校验；材料在state/installation-materials内，不进入Git。原生下载/材料传输最多15分钟，安装作业最多30分钟。确认失联时先查原记录；下载失败用原Key重试，已发布文件重新核对固定部件，不能覆盖不明文件。上传失败目前需重传整个文件，无分块续传。

实例配置可选择存储根目录；默认为主机项目目录/software/{软件ID}，自定义仅允许/opt、/srv、/var/lib、/data、/mnt下的专用规范子目录。预检展示实际程序、数据、配置、日志位置。已安装非空数据目录不能在配置页直接搬迁，需另行迁移；不会只改路径而丢失旧数据。

网络配置支持修改主端口和已实现协议的具名TCP/UDP端口。Docker显示宿主映射，原生显示实际监听；禁用项不发布。SeaweedFS的Docker仅发布S3 HTTP。原生S3 gRPC与HTTP共用监听地址，端口明确展示并预检，管理员须限制其来源；其他Master/Volume/Filer及gRPC绑定回环，可调整端口；MediaMTX/ZLM按需启用协议，管理API有凭据或只在回环发布。预检和启动前复检冲突，不杀占用进程、不自动改防火墙。软件中心的“服务配置”可修改地址、端口、内存及支持的凭据：先预检、冻结计划，再执行配置作业。成功更新精确绑定；失败恢复原绑定或保留UNKNOWN等待核对。原停止状态在修改后保留，不自动修改Platform连接。

MySQL管理员为root，业务库/账号可选，新空目录才初始化且默认不开放远程root；已有安装的初始账号/密码字段只读，配置变更不冒充数据库改密。Redis需要密码，SeaweedFS需Access Key/Secret Key，MediaMTX需用户/密码，ZLM需API secret，Nginx没有初始登录账号。可选择已有加密凭据，或在表单内输入/生成并保存，计划及作业只持久化修订引用。MySQL配方密码为8–128位可打印ASCII，排除引号/反斜线及控制字符；秘密不进入命令预览、普通日志或浏览器持久存储。

安装/配置计划有效5分钟，提交返回原持久Job。安装确认后进入软件中心，支持检测、启停及有界日志；超时和丢回执先核对原Job，不重复初始化。解除管理保留远端程序、配置和数据。Ops退出不停止既有服务；数据库和对象数据不随代码回退降级。

API以`/ops/api/v1`为前缀：目录`GET /software-recipes`；安装`POST /hosts/{id}/software-install-plans`、`POST /software-install-plans/{id}/submit`；材料`POST/GET /installation-materials`、`PUT /{id}/content`、`POST /{id}/confirm`、`DELETE /{id}`，原生在线准备`POST /installation-materials/official`；配置`POST /software-instances/{id}/configuration-plans`、`POST /software-configuration-plans/{id}/submit`。新安装要求执行器`serviceInstallVersion=2`且目录摘要相符。

### 主机安装准备

“主机管理 → 详情 → 安装准备”只保留两个入口：

1. **在线安装**：选择已核对指纹的 SSH 主机，点击“开始安装”。Ops 会检查 Linux amd64、上传材料、安装并验证结果。仅当服务器要求 sudo 密码时，页面才显示“当前账号的登录密码”；填写该 SSH 账号的系统密码，凭据只用于本次操作。连接中断后先点击“检查安装结果”；需要修复时使用页面的恢复按钮继续原作业。接入成功后自动刷新主机状态。
2. **离线安装**：点击“下载离线安装包”，把 `streamfusion-ops-target-linux-amd64-<主机ID>.tar.gz` 上传到目标服务器当前账号的主目录并解压，进入目录后只需执行：

   ```bash
   bash install.sh
   ```

   包内只提供 `streamfusion-ops`、`install.sh`、`SHA256SUMS` 和 `HOST-SETUP.md`；`install.sh` 已绑定当前控制器、主机、目录和连接账号，并把原有受限安装器逻辑内嵌其中。它会自动识别首次接入、同归属升级或授权修复，校验摘要后再修改文件。安装完成后回到 Ops 点击“检查安装结果”。目标服务器不需要访问外网，也不需要额外启动常驻服务。

主机 API 使用 `POST /hosts/{id}/preparation/install`、`POST /hosts/{id}/preparation/reconcile` 和 `POST /hosts/{id}/preparation/repair`；安装与修复请求使用现有敏感请求加密，统一作业页也可核对接入作业。单次安装最多执行3分钟，页面请求期限210秒，控制器写期限240秒；中断后先核对原作业。恢复操作核对主机归属、原作业与可信材料；不匹配时停止并保留原文件。

已有受管目标升级执行器时，在可信包目录执行准备页生成的命令，二进制必须为绝对路径，例如：

```bash
sudo ./streamfusion-ops target-upgrade --root /opt/streamfusion-ops-target \
  --binary "$(realpath ./streamfusion-ops)" --sha256 <可信摘要> \
  --controller-id <现有控制器ID> --target-id <现有主机ID>
```

保留executor.previous；不覆盖归属、UNKNOWN或未确认责任。独立Linux/systemd/Docker、编译依赖及跨机安装需实机验收，Windows本机测试不代表这些步骤已通过。

## I02 发布闭环

首次接入使用上面的远程安装或离线安装入口。诊断场景可手动执行底层命令：

```bash
sudo ./streamfusion-ops target-init \
  --root /opt/streamfusion-ops-target \
  --controller-id <控制器ID> --target-id <目标ID> \
  --docker-binary /usr/bin/docker --docker-socket /var/run/docker.sock
```

目标使用专用连接账号时，先审阅 `scripts/install-target.sh` 的计划，再显式 `--apply` 安装固定sudo策略。脚本不授予任意shell或docker命令。安装为systemd的本机控制器需显式提供 `--controller-unit`：这会为固定executor关闭NoNewPrivileges并增加该受管根写路径；默认控制器安装仍保持NoNewPrivileges=true。SSH目标无需此本机例外。

页面“主机管理”新增连接后执行“检查能力”；发布前仍会实时核对具体配方。“版本制品”可以导入固定 Docker save 归档，归档必须是单个 `sfops/` 镜像、`linux/amd64`，并填写可信 SHA-256；也可选择“可执行 JAR + 已安装 Java21”，上传最多128MiB的 JAR。导入完成后在“应用实例”保存 Java 配置草稿，再执行发布预检。已有依赖连接在“应用实例”的折叠区维护。每个发布作业都冻结制品、端口、配置、主机版本、依赖凭据修订、健康路径、数据兼容标识，以及 JAR 制品可选的运行环境身份。

依赖默认仍为外部服务；主机 TCP 检查不证明容器网络或密码可用。选定当前已发布的 Java 实例后，才从其真实容器网络命名空间执行有界 Redis AUTH/SELECT/PING 或 MySQL 握手/COM_PING，使用该 Release 冻结的凭据修订，不建库、不执行 SQL。探针不支持 TLS；MySQL `caching_sha2_password` full-auth 明确返回 `AUTH_TLS_REQUIRED`，不发送明文密码或信任未固定 RSA 公钥。Redis AUTH 仍使用绑定既有的普通 TCP 网络，不能当作加密传输能力。

作业页“取消排队”仅处理未运行、无待 ACK 且非上传中的作业。运行中、`UNKNOWN` 或待 ACK 保留原锁与责任，不提供强杀/强制解锁；取消沿兼容状态 `FAILED / CANCELLED / JOB_CANCELLED` 记录。导入取消确认提交后清理该作业上传的临时文件；清理失败可重试原取消请求，不删除其他制品或运行材料。

作业流程为“目标校验 → 归档校验/导入 → Compose 激活 → 实际容器身份 → 应用健康 → 控制器提交当前/上一成功版本 → 目标确认”。候选健康失败时自动停止候选并按冻结基线恢复；数据库内容不会随应用回滚。`UNKNOWN` 或目标确认未完成时，页面会保留锁并提供“核对/恢复”，不会因为超时自动重放。

应用源码构建、Jenkins、Node/Python安装和项目组合发布属于后续工作包；当前固定软件安装范围见上节。

Linux 隔离发布验收：

```bash
sudo python3 scripts/test-release.py \
  --binary /absolute/path/to/streamfusion-ops \
  --lab /opt/ops-i02-lab \
  --output /tmp/streamfusion-ops-i02-release.json
```

该脚本使用隔离 Docker socket、Java 健康夹具和临时状态，验证 A→B、C 失败恢复、A 回滚、重复提交幂等、日志、实际观察和控制器重启；结果只代表夹具闭环，不代表当前业务后端已上线。
