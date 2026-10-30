# P2 / OPS-INSTALL-SIMPLIFY-02 交付说明

日期：2026-10-29。承接 OPS-INSTALL-SIMPLIFY-01；多 Agent 复核与工程验证完成，负责人实机验收待进行。

## 范围与结果

本轮只修改 `operations/`，把主机安装准备收敛成两条操作路径：

- **远程安装**：保存并确认 SSH 连接后，在页面输入本次 sudo 密码或使用 root/免密 sudo，勾选确认，点击“连接并安装”。控制器自动传输材料、调用固定安装器并验证归属、协议与实际执行器摘要。目标服务器不需要访问公网。
- **离线安装**：下载对应主机的 Linux amd64 包，上传到当前账号主目录并解压，进入解压目录执行 `bash install.sh`，按提示确认即可。无需手工填写摘要、控制器ID、主机ID、账号和目录，也无需 `chmod`。

安装包只有 `streamfusion-ops`、`install.sh`、`SHA256SUMS`、`HOST-SETUP.md`。现有 `scripts/install-target.sh` 在生成包时内嵌进 `install.sh`，继续复用既有初始化、升级和受限授权逻辑。首次接入、同归属升级、授权修复共用入口；高级检查折叠展示。

接入组件由 SSH 按需调用，无需另行启动常驻服务。本包准备的是主机接入；MySQL 等软件继续使用现有软件目录与安装材料流程。

参考交互来自 [1Panel 官方离线安装说明](https://docs.fit2cloud.com/1panel/installation/package-installation/)与[宝塔官方在线安装入口](https://www.bt.cn/new/download.html)。复用项目已有 SSH、作业、锁、安装器与请求加密，没有新增生产依赖或通用远程命令接口。

## 多 Agent 复核修复

- 前端：旧主机请求不再覆盖新主机状态或清除新请求的等待状态；下载使用发起时的主机与文件名；未确认提交保留同次安装幂等键，终态后才允许创建新意图。
- 进度：暂时读取失败最多重试3次，成功后恢复显示；持续失败明确提示手动刷新。离线检查展示执行器和授权的实际结果，不将 SSH 连通当作已完成接入。
- 后端：核对结束时的会话授权、主机版本/连接身份、作业归属和状态、结果/审计写入与解锁合并同事务；核对期间配置变化、会话撤销和写入失败均保留原 UNKNOWN 作业及锁。
- 临时材料：请求取消后通过固定指纹的独立 SSH 连接尝试清理当前作业上传，最多5秒；不重放安装。服务器不可达时仍可能遗留临时文件。
- 离线包：非 Linux amd64 的控制器本机禁用下载；未知已有目录的拒绝说明与实际安装逻辑一致。

## 主调用链

`HostPreparationPanel.vue` → 敏感请求加密 → `installHostPreparation` → 既有 SSH 指纹验证 → Linux amd64 探测 → 私有临时目录上传 → root 或临时 sudo 执行固定安装器 → `CAPABILITIES` 与执行器摘要核对 → 持久化 Job 结果。

离线路径由 `createTargetPreparationArchive` 生成绑定当前主机的包；`install.sh` 自动校验摘要、提示确认和申请 sudo，并按目标目录现状选择初始化、升级或受限授权修复。归属不一致或未知已有目录保留原文件并拒绝覆盖。

在线作业使用现有 `jobs` / `operation_locks`。同一账号和幂等键复用原作业；主机维护、版本冲突和已有责任阻止新操作。sudo 密码只经密文请求和 SSH 标准输入传递，不写入作业、审计或日志。运行最多3分钟，前端请求期限210秒，服务端默认写期限240秒。

安装开始后的错误、断线、超时与控制器重启保留 `UNKNOWN` 和主机锁；安装页及统一作业页可核对已有结果，不自动重放安装。核对要求原连接身份、目录、归属和执行器摘要匹配；维护状态变化不改变原连接身份。

## 文件清单

新增：

- `internal/controller/target_installer.go`：单脚本与包内指引。
- `internal/controller/host_remote_preparation.go`：在线接入、作业准入、状态与核对。
- `internal/controller/schema_v13.go`：持久化接入作业种类。
- `internal/controller/host_remote_preparation_test.go`：敏感请求、SSH、权限失败、架构拒绝、摘要、幂等、重启和核对测试。
- `scripts/test-host-preparation.py`：独立控制器内的安装页、包下载、脚本语法与窄屏验收。
- `web/tests/hostPreparation.test.mjs`：请求竞态、轮询恢复、幂等意图、下载快照和离线检查反馈。
- 本交付说明。

修改：

- `internal/controller/host_preparation.go`、`host_preparation_test.go`：四文件包和新指引。
- `internal/controller/hosts.go`、`operations.go`、`operations_jobs.go`、`operations_releases.go`：路由、统一作业核对与禁止 worker 重放。
- `internal/controller/store.go`、`firewall_test.go`、`schema_v11_test.go`：增量迁移与现行版本断言。
- `internal/controller/hosts_test.go`：可返回标准输出、错误和退出码的 SSH 测试夹具。
- `internal/controller/request_cipher.go`、`web/src/requestCipher.ts`、`scripts/secure_request.py`、`web/tests/requestCipher.test.mjs`：新敏感路径。
- `web/src/HostPreparationPanel.vue`：两个主要入口、单命令离线指引、进度及核对。
- `web/src/App.vue`、`web/src/OpsWorkspace.vue`、`web/src/api.ts`：作业标签、审计与请求期限。
- `cmd/streamfusion-ops/main.go`：与安装执行期限匹配的 HTTP 写期限。
- `internal/deploy/executor.go`：更新初始化函数注释，无执行器公共协议变化。
- `README.md`：现行安装流程、API 和迁移说明。

## 数据与协议

- SQLite 从 Schema 12 增量到13，仅扩展 `jobs.kind` 的 `HOST_PREPARE`，不新增业务数据关系。升级前自动保存 `before-schema-13-*.tar.gz`，事务升级后核对外键。
- 当前本地状态库已升级；`quick_check=ok`、外键错误0，原有1个管理员、3台主机保留。
- 新增内部会话 API：`POST /hosts/{id}/preparation/install`、`POST /hosts/{id}/preparation/reconcile`。前者携带 `expectedVersion`、`idempotencyKey`、`confirmed` 和可选临时 `sudoPassword`，必须使用现有密文协议。
- `GET /hosts/{id}/preparation` 增加最近接入作业；下载包增加单一 `install.sh`，包名明确 `linux-amd64`。原 commands 字段保留，首次/修复指向统一脚本。
- Platform、TaskSpec、Event、Agent Sync 与目标 `target-exec` 协议语义无变化。

## 已执行验证

在 `operations/` 执行：

| 命令 / 检查 | 真实结果 |
| --- | --- |
| `go test -json ./... -count=1` | 353项通过，15项条件跳过，0失败；记录在 `.run/ops-install-simplify-final/go-test-reviewed.jsonl` |
| `go vet ./...` | 通过 |
| `pnpm --dir web typecheck`、`pnpm --dir web build` | 通过 |
| `pnpm --dir web test` | 18项通过 |
| `node scripts/embed-web.mjs`、Windows构建、Linux amd64交叉构建 | 通过 |
| `scripts/test-browser.py` | 61项通过，0页面脚本错误；独立状态库 |
| `scripts/test-host-preparation.py` | 19项通过，0页面脚本错误；包含真实四文件包下载、全部摘要、实际生成脚本 `bash -n`、离线检查反馈和轮询失败恢复 |
| HTTP首页及 `/ops/api/v1/health` | 均为200，健康状态UP |
| `git diff --check` | 通过 |

新接入浏览器验收使用独立状态库；SSH主机身份、在线作业和主机能力检查响应是明确的测试夹具。认证、包生成和下载使用真实控制器。Go测试通过隔离 SSH 服务覆盖 root、sudo 密码、无管理员权限、非 amd64、上传后主机变化、摘要不符、重复提交、中断保锁、重启不重放、核对时版本/授权变化、事务回滚和取消后的清理。

15项条件跳过涉及 Linux/root 文件归属、软件生命周期、执行器升级/校验和本机Linux资源探针。未执行真实目标服务器、真实 sudo/systemd/Docker 安装，不把这些测试计为负责人实机验收。

## 人工验收步骤

1. 打开本地 Ops 的“主机管理 → 详情 → 安装准备”，确认默认只有远程安装和离线安装两个主要入口。
2. 对一台已核对指纹的 Linux amd64 测试主机，在远程安装中核对目标、账号和目录，填写本次 sudo 密码，勾选后安装。应依次显示连接、上传、安装、验证，完成后检查主机能力。
3. 离线路径重新下载当前主机专用包，上传到该账号主目录，通过文件管理器解压后执行 `bash install.sh`。确认只需 sudo 密码和安装确认，无需填写标识或摘要。
4. 使用同一包再次安装，验证同归属识别、升级或授权修复。其他归属或未知目录必须拒绝覆盖；原软件和数据保留。
5. 在测试环境模拟安装阶段断线，回到页面核对原作业；新安装按钮应被阻止。实际结果和摘要未确认时继续保锁，不重复安装。
6. 接入就绪后进入软件目录选择 MySQL，继续既有安装计划流程。主机接入包不等于数据库安装包。

## 故障入口与限制

- SSH失败：核对主机IP/端口、连接凭据及指纹；普通账号没有管理员权限时页面返回 `PREPARATION_SUDO_REQUIRED`。
- 包不可用：检查控制器程序旁 `release/0.2.0` 的Linux二进制、原安装器和 `SHA256SUMS`；文件发生变化时拒绝生成包。
- 安装中断：按 jobId、traceId、错误码和时间定位，查看 `state/logs/controller.log`、安装页及统一作业页。远端原始 stderr 不进入页面或作业，避免泄露目标输出中的秘密。
- 临时目录：安装在普通账号 `/tmp/sfops-upload-<job>` 上传，提权后转入私有 `/var/tmp/sfops-setup-*` 验证。取消时仅清理当前作业上传文件；若网络不可达或远端进程中断，仍可能留下本作业临时文件，须确认原操作结束后由管理员按精确路径清理。
- 当前只提供 Linux amd64；其他架构明确拒绝。需具备 Bash、tar、sha256sum 等基础工具，普通账号需 sudo/visudo；不自动安装Docker或更改现有数据归属。
- 离线安装不依赖公网；回到控制器检查及管理仍要求控制器到目标SSH可达。页面关闭导致在线请求中断时先核对原作业。

## 回滚

停止本地控制器后，使用原密钥和 `before-schema-13` 备份恢复到**新的状态目录**，再配套启动支持Schema12的旧程序；不得直接用旧程序打开Schema13，也不得删锁绕过 `UNKNOWN`。目标执行器同归属升级保留 `executor.previous`；目标程序、授权与软件数据由管理员按现有归属规则核对，不随控制器代码回退删除。

负责人已授权工程验证完成后正常提交并推送 GitHub，提交范围仅 `operations/` 源码、测试与说明。未在已登记服务器执行安装，未修改业务数据库；负责人验收结论待实机测试后登记。

## 2026-10-29 后续：目录权限受控修复

针对旧安装目录已存在但未通过安全检查的情况，安装准备页现提供“修复权限并继续安装”和“只重新检查”。普通运维界面不再展示 `SSH_CONNECTED` 等内部码；主机能力页只显示中文结论、来源和检查时间，原码继续保留在接口、日志和审计诊断链中。

修复不是通用远程终端，也不执行目录删除或递归 `chown`。`POST /hosts/{id}/preparation/repair` 只接受原 `HOST_PREPARE` 作业仍为 `UNKNOWN` 且原 `target` 锁仍存在的请求，并复用敏感请求密文协议传输本次 sudo 密码。服务器端安装包将原作业保存的执行器 SHA、控制器 ID 和主机 ID 绑定到 `target-repair`；Linux 修复器拒绝符号链接、不安全父目录、归属不匹配和摘要不匹配，只修改安装根、`owner.json`、`executor`、`executor.lock` 的 root 归属与固定权限。

修复后继续执行既有目标验证、可信执行器升级和受限 sudo 授权；只有最终验证成功才把原作业标记成功并释放锁。请求中断或结果无法确认时原 `UNKNOWN` 作业和锁继续保留，不重复安装。sudo 密码提交后立即清空，不进入作业、审计、数据库或日志。

本后续没有数据库迁移、新生产依赖或公共 TaskSpec/Event/Agent Sync 变化。最终验证为前端21项、Go全量、go vet、Linux amd64构建通过；隔离 Edge 接入28项、端口15项、全站61项通过且页面错误为0。Linux修复器测试已完成Linux目标编译；当前Windows环境缺少可用WSL与Docker，未执行真实Linux文件归属测试，也未替负责人点击现有服务器的修复按钮。

人工验收时进入“主机管理 → 详情 → 安装准备”，先点击“检查安装结果”。若显示目录权限问题，核对目录和连接账号后填写当前账号的 sudo 密码并点击“修复权限并继续安装”；应完成权限修复、执行器升级、授权和最终核对。若提示身份或摘要不匹配，应停止并检查目录归属，不删除原目录。控制器回滚使用本轮保留的旧程序并继续使用同一状态目录；目标服务器已成功完成的权限收紧和执行器升级不会因控制器回滚自动撤销。
