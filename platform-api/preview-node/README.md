# 预览节点包：构建与部署

本机足够时无需部署节点包。扩容时可在 A/B 各安装一份：Platform 负责鉴权、连接、源归属、预算和预览下发；本包运行预览控制、ZLM 和 FFmpeg，不接业务数据库、Redis 或算法模块。已有观看不迁移，每节点观看/转码配置上限为 64，实际承载须按目标码流压测。专用 ZLM 也可单独安装，再由 Platform 直连接入，不要求安装本节点包。

## Ops部署与维护

已有Ops可使用固定预览配方，详见[Ops预览节点操作流程](../../operations/preview-nodes.md)。导入解包后的`preview-node-image.tar`，由目标executor生成身份与冻结配置；不会自动接管既有手工节点。升级/回退必须先在平台排空并取得维护许可，节点持久封闭，Ops作业确认后仍须平台显式解除、启用。S4不新增平台数据库迁移；S3现有表保持。旧S2/S3镜像缺少维护约束，不能作为Ops回退版本。

下方保留独立手工部署方式，两种方式不应同时管理同一运行实例。

## 构建与节点安装

Java21下运行`./mvnw verify`，得到平台包和`*-preview-node.jar`。Linux构建机使用Docker和固定摘要的ZLM、Temurin21镜像：

```bash
./build-package.sh /path/platform-api-0.1.0-SNAPSHOT-preview-node.jar /path/new-output \
  zlmediakit/zlmediakit@sha256:实际摘要 eclipse-temurin@sha256:实际摘要
```

输出离线镜像、镜像 ID、上游摘要、Compose、配置/预检脚本、平台升级 SQL 和 SHA256SUMS。目标机不编译或在线拉镜像。A/B 分别准备 Ubuntu 24.04/amd64、Docker Engine、Compose 插件、Python 3，并校准时钟。平台须能访问节点控制地址，节点须能访问准许的 RTSP 目标，浏览器须能访问节点公布的媒体 IPv4 和 18000/TCP、UDP；控制口连通不能证明首帧。

在每台节点的独立包目录执行：

```bash
sha256sum -c SHA256SUMS
docker load -i preview-node-image.tar
sudo python3 configure.py --node-id preview-a --name '预览 A' \
  --media-ip 192.0.2.20 --control-host 192.0.2.20 --allow 192.0.2.10:554
# B使用不同node-id/IP；多个相机端点重复--allow。
```

示例地址须换成实际节点和相机 IP。把 `.env` 的 `PREVIEW_BIND_IP` 设为实际接口，并按节点能力配置容器 CPU/内存。执行 `sudo ./preflight.sh` 后 `docker compose up -d`。仅公布 9443/TCP 控制、18000/TCP+UDP 媒体；ZLM 管理 API 仅允许容器回环访问，且不映射到宿主机。`configure.py` 遇到已存在的 `config` 会拒绝运行，不会重写身份文件；升级应走维护流程，不要重跑生成器。不要改 host 网络、挂 Docker socket、特权运行或额外发布管理端口。

## 预算怎样设置

生成器支持`--max-sources`、`--max-sessions`、`--max-transcodes`、`--max-starting`，默认64/64/64/4；转码0禁止新转码。另有`--source-upper-kbps`、`--output-upper-kbps`（默认8192）、`--ingress-kbps`、`--egress-kbps`（默认655360）。上界×1.25用于预占；这些默认值只是估值，须按码流和机器校准，不能当作实测能力。当前一个共享转码输出计1单位，节点只使用其现有固定编码配置，未提供多GPU资源池或预设成本目录。

平台可将预算调低，不能超过远程节点已应用配置，码率上界须两侧一致。持续CPU/内存≥95%各3次或实测码率连续超上界会暂停新预览；低于85%恢复阈值各3次后恢复。媒体轮询有界且不拉新流，诊断JSON显示观测值/原因。保护不终止或迁移旧会话；未知观测不冒充空闲。

## 平台数据库与手工登记

节点自身不需要平台数据库。已有 Platform 库升级前须先备份、排空预览并停服务：没有预览表时使用 `platform-upgrade.sql`；S2 三表升 S3 时使用 `platform-s2-to-s3.sql`；从 S3/S4 加入独立 ZLM 类型时使用[定向增量脚本](upgrade-zlm-direct.sql)。每份脚本只适用于对应起点，先核对表结构，失败后检查再继续，不能重放完整初始化或盲目重试。新库按[SQL 指南](../sql/README.md)安装。

Ops 发布的实例从 Platform 的 Ops 目录直接引入；以下仅适用于上述手工安装。安全复制各节点的 `platform-registration` 文件到平台保护目录，分别修改 `connection.json` 的 `tokenFile` 为平台绝对路径。`SF_PREVIEW_NODE_CONNECTION_FILE` 可指向单个连接对象或 A/B 对象数组，最多 16 个远程节点；`local` 是本机保留身份。凭据不放页面、数据库明文或交付材料。

在“监控面板 → 预览节点”登记各节点，设置优先级和预算后启用。默认本机 0、远程 100，数字小的先选；同级优先已有观看的节点，再按 nodeId 排序。新源在候选容量不足时检查下一节点；同源已有归属只复用原节点，观看或出口满时返回原因，不跨机重复拉。ZLM 网络线程负载仅展示，不参与调度。

检查节点只读取控制/配置，不代替首帧。原预览中心完成试播；浏览器媒体直达所分配节点，平台代理控制和SDP。S2包缺少S3预算字段，须升级后才能启用S3调度。

## 维护、排查与回退

- 停止该节点接收新预览后，旧观看继续续期；确认源/输出/观看归零后再停节点或修改地址、证书、运行时、允许端点。更新保护文件和配置，重新登记，仍保持排空状态，检查后显式启用。
- traceId/leaseId → 节点诊断JSON → resourceId/operationId → 节点控制回执与`docker compose logs --tail 100 preview`。不打开ZLM apiDebug，不记录完整RTSP凭据、SDP或机器Token。
- 启动响应丢失查询原操作，不换机器重拉；结果不明和关闭失败保留原节点占用。90秒节点租约由平台鉴权后续期，未确认创建为30秒；有界重试失败后人工修复再“重新核对资源”。
- 平台/节点重启按持久责任精确清理，旧bootId不能继续写。浏览器重新申请；不删SQL记录或state/ownership来“清零”。不可读的责任日志拒绝继续准入。
- 维护前确认排空，再`docker compose down`；保留config/state、数据库备份及原镜像。S3内版本回退使用原镜像和兼容配置；退回S2需所有节点清空并单独评估数据库/配置兼容，不能直接替换运行中程序。

本机隔离验证不能替代独立Linux服务器、真实跨网、64路和长期负载验收。S4 Ops单节点可重复部署、排空、升级与回退已实现；批量并行部署、自动接管/卸载、热点级联和已有会话迁移未实施。
