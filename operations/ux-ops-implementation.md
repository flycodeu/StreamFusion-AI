# P2 / UX-OPS-DESIGN-01：Ops 本批实施交付

本批为实现及工程验证，不是负责人验收或生产发布。范围仅 `operations/`；不部署、不安装 Docker、不操作现有业务数据库。Platform 来源同步、权限查询、预览收藏与浏览器音频由同轮其他模块交付，本说明只记录 Ops 一侧。

## 实际实现

- LINK-01：预览节点及独立 ZLM 目录、交接返回 controller/target/instance/release 稳定身份。ZLM 另返回 `credentialRevision=credentialId:revision`，取冻结 Release 的精确修订；不是密码摘要。交接前后拒绝未决作业、待 ACK 和目标/实例锁。目标短锁及 pending 检查覆盖真实身份/健康读取，不将短交接锁说成持续 Platform 维护许可。
- EXT-01：可执行 JAR + 已安装就绪的内置 Java21 JRE → 只读导出基础镜像 → Go 离线追加固定 app.jar 层 → 原 Artifact/不可变 Release/发布回退链。封装不执行 JAR、Dockerfile 或 Maven。摘要、ZIP 展开量、累计压缩读取预算、主清单、外层普通 class 头版本做有界检查；这不是完整 Java 字节码或嵌套依赖兼容性证明，真实健康仍由发布链验证。拒绝 Java17/Maven、自定义环境快照及完整镜像覆盖 JRE。
- EXT-02：保持旧 host-only 输入兼容；额外选择当前已发布 Java 实例后，验证依赖与冻结 Release 完整相等，用其凭据修订检查控制器 TCP、目标宿主 TCP、真实实例网络 TCP 和最小认证。短命探针容器只挂只读静态 executor、复用实际容器网络，固定入口、非 root、只读、禁 healthcheck、无特权/宿主 Docker socket，并有资源/时间/日志限制与按名清理。
- EXT-03：排队 IMPORT/PUBLISH/ROLLBACK 及原插件作业可安全取消；上传中、运行中、UNKNOWN、待 ACK 拒绝。worker 用条件认领隔离取消竞态，不杀目标进程、不强制解锁。状态兼容使用 `FAILED + CANCELLED + JOB_CANCELLED`。已确认取消提交后只删除该 IMPORT 的上传临时文件；清理失败明确报错，重复取消可重试，不删除其他上传、制品或运行材料。
- 独立 ZLM 固定配方保留来源音轨（enable_audio=1）；FFmpeg 转码仍保留 `-an`。是否出口音频由 Platform 实际音轨判定，本修改不承诺 AAC 转码或全格式音频。

## 未实施与限制

EXT-04 Docker 宿主无引擎登记/固定安装计划/执行、EXT-05 Maven 可信源码包隔离构建 **本批未实施**，没有空壳按钮或伪成功。当前 target-init/PROBE 仍要求已有 Docker/Compose。前者需明确固定包源/版本、冲突与主机中断恢复；后者需单独验明信任源码、网络/磁盘/输出配额和隔离后执行边界。JAR 被动封装不等于 Maven 源码构建。

Git/Jenkins、Node/pnpm、Python/uv、Nginx、受管 MySQL/Redis和项目组合发布仍不在本批。现有 MySQL/Redis 保持 external，不因端口可达自动托管。

MySQL 仅支持 mysql_native_password、caching_sha2 fast-auth 及认证切换，full-auth 返回 `AUTH_TLS_REQUIRED`；不发送 cleartext 或取得未固定 RSA key。MySQL/Redis TLS 尚未实现；Redis AUTH 使用原绑定的普通 TCP。未执行/不支持不等于密码错误，更不等于认证通过。

## 数据与接口兼容

Ops SQLite 5→6 只给 artifacts 增加可空 `runtime_environment_json`，以及作业环境引用表达式索引；旧制品/Release 空值兼容，旧迁移摘要不改。升级前一致性备份 `before-schema-6-*.tar.gz`，同事务登记迁移摘要；测试仅使用临时状态库，未打开升级真实控制器状态。

引用数从真实历史制品和 QUEUED/RUNNING/UNKNOWN JAR Job 计算，并保守叠加旧 reference_count；取消后释放排队引用，成功制品继续持有引用。没有自动清理历史制品/环境按钮。

管理 API 兼容扩展：`POST /artifact-imports` 可选 sourceKind/pluginInstallationId；runtimeEnvironment 仅服务端冻结，客户端提交拒绝。`POST /bindings/{id}/probes` 可选 instanceId/releaseId，必须成对；旧 targetId-only 仍宿主 TCP。原 `POST /jobs/{id}/cancel` 增加安全作业种类。目录/交接只增加来源和可选凭据修订字段，旧磁盘连接文件不变。新 JAR 配方需目标 PROBE 返回 javaJarRecipe；新实例认证探针若遇旧 executor，明确提示升级，不降级冒充完成。未修改 TaskSpec/Event/Agent Sync。

## 变更文件清单

- CLI：`cmd/streamfusion-ops/main.go`。
- 控制器：`internal/controller/bindings.go`、`operations.go`、`operations_artifacts.go`、`operations_jobs.go`、`operations_releases.go`、`platform_preview.go`、`platform_zlm.go`、`plugins_jobs.go`、`plugins_store.go`、`jar_import.go`、`runtime_export.go`、`schema_v6.go`、`store.go`。
- 目标与封装：`internal/deploy/archive.go`、`executor.go`、`model.go`、`binding_probe.go`、`handoff.go`、`jar_package.go`、`runtime_export.go`、`zlm.go`。
- 前端：`web/src/OpsWorkspace.vue`；沿既有布局增加真实操作、环境摘要和渐进诊断，不改主题，不添加生产依赖。
- 测试：`internal/controller/binding_probe_test.go`、`controller_test.go`、`jar_import_test.go`、`queued_cancel_test.go`、`platform_preview_test.go`、`platform_zlm_test.go`、`plugins_test.go`、`preview_test.go`；`internal/deploy/binding_probe_test.go`、`extension_linux_test.go`、`jar_package_test.go`、`zlm_test.go`；`scripts/test-browser.py`。
- 说明：本文件、`README.md`。生成网页/测试状态/二进制仅忽略目录或临时目录，不加入 Git。

## 已执行的工程验证（2026-10-18）

- `go test ./... -count=1`：两项独立安全复核修复后的最终全量重跑通过；controller 24.392s、cmd 1.352s、deploy 0.857s、plugin 0.768s，webui 无测试。首跑发现旧测试将未来 schema 硬编码为6，改为 SchemaVersion+1 后全量重跑通过。
- `go vet ./...`：在 operations 目录执行通过；一次误在仓库根执行提示无 Go module，该次不计通过证据。
- `pnpm --dir web typecheck`、`pnpm --dir web build`：通过；随后 `node scripts/embed-web.mjs` 和 Windows 临时二进制构建通过。
- 定向 JAR、schema5→6、三层探针、取消、目录/交接及 Redis/MySQL 协议测试：通过。包含离线层/摘要一致、不可覆盖输出、Java17拒绝、引用阻断、失败无可发布 artifact、旧字段兼容、版本/ACK竞态和密码不外泄。
- 独立复核补充两项资源边界后，定向和全量回归均通过：IMPORT 取消后精确清理该作业的 `.partial`，UPLOADED/VERIFY 可重试清理且其他材料/运行状态保持；微型 ZIP 中两个中央目录项共享压缩区可由 Go 真实读取，但累计压缩读取预算拒绝该扫描放大样本。另一 agent 独立复读并执行这两项定向回归通过，不等同生产/负责人验收。
- `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./internal/deploy` 与 Linux 静态二进制交叉编译：通过；不等于执行 Linux tests。构建环境变量执行后恢复。
- `git diff --check -- operations`：通过。

浏览器验收使用两项独立安全复核修复后的最终 Windows 临时二进制及独立状态目录，未操作已有 19090 服务。`python scripts/test-browser.py` 的本轮扩展验收 24 项通过、`pageErrors=[]`；最终结果保存在 `operations/.run/ux-ops-browser-20261018-04/result.json`。包含 JAR 固定配方/只选 READY Java21、完整镜像不覆盖 JRE、真实排队 IMPORT 取消、host-only 不冒充认证、九页 390px 窄屏无溢出。已实际查看 JAR 弹窗及窄屏截图；这是隔离环境工程验证，`productionAcceptance=false`。

本说明不引用已删除的旧 .run 原始材料。

## 尚未执行

Linux-only root 安全路径/真实容器网络/清理/export 测试已经编写并交叉编译，但本机 Debian、kali-linux 均因登记的 ext4.vhdx 不存在而无法启动（WSL ERROR_FILE_NOT_FOUND）；没有安装/修复 WSL 或新系统。真实 Docker、Java21 镜像导出/加载、A→B→坏版本 C 恢复→回退、MySQL/Redis 服务、第二台 SSH Linux 与生产验收均未执行。Windows SSH/protocol/离线归档夹具不能替代这些现场结果。

## 可复现人工验收

1. 用独立 Ubuntu24.04/amd64 目标及测试状态准备 Docker/Compose 与受保护静态 executor，登记并核对；在插件中心安装固定 Java21，等原 Job 终态且 ACK 收尾。
2. 上传可信 Spring Boot JAR，核对其可信 SHA-256，选该环境。成功后制品显示冻结 Java21/digest；排队时及成功制品存在时卸载应拒绝。改选 Java17或将完整镜像强绑环境，应拒绝。
3. 用同一 Java 实例完成 A、B发布，坏 C健康失败恢复，回退至 A；每次核对冻结环境/配置/实际容器身份，失败候选不占成功槽。停机/断线沿原 Job UNKNOWN/ACK 核对，不另建作业绕过。
4. 绑定只读测试 MySQL/Redis，先查 host TCP，再从实际已发布实例检查；分别制造密码错误、容器网络不通、full-auth需求、旧执行器、预检期间Release变化和待ACK，逐层结果不得伪成功，日志/响应不能出现密码。
5. 在等上传/VERIFY排队状态取消，重复取消和直接重调worker不执行；RUNNING、UNKNOWN、待ACK、上传中不可取消，锁仍保留。
6. 来源交接分别制造未决Job/锁/ACK和Release变化；返回冲突且无secret。成功返回三元组和精确冻结修订；由平台继续排空→同步→显式启用→浏览器首帧，不能仅凭Ops健康认定媒体通过。

定位入口：控制器 traceId、jobId、targetId、artifactId 与 `logs/controller.log`；目标受保护 jobs/pending/instances 责任文件；前端作业详情及依赖分层结果。遇 `TARGET_JAR_RECIPE_REQUIRED`/`TARGET_PROBE_UPGRADE_REQUIRED` 升级可信 executor；`AUTH_TLS_REQUIRED` 保持未验证，不更换成不安全明文/RSA降级。

回滚：先停止新控制器，保护当前状态、密钥和归档；以对应旧程序和升级前备份恢复到**新目录**，保留本轮Job证据，不让旧程序打开schema6。应用按既有不可变Release恢复，不回滚业务库。未执行系统安装/真实目标变更，无Docker系统回滚操作；不删除UNKNOWN锁或责任文件制造空闲。
