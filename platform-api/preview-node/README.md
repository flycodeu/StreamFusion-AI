# 预览节点

需要独立部署媒体预览时，在每台目标服务器安装一个预览节点。Platform 负责鉴权、调度和资源登记，节点运行 ZLM、FFmpeg 和预览控制服务。平台数据库按源码中的 `platform-api/sql/README.md` 初始化，远程节点无需数据库连接。

## 构建离线包

使用 Java 21 在 `platform-api` 运行 `./mvnw verify`，取得 `*-preview-node.jar`。在 Linux Docker 构建机运行下列命令，将两个镜像摘要替换成实际值：

```bash
./build-package.sh /path/preview-node.jar /path/new-output \
  'zlmediakit/zlmediakit@sha256:<摘要>' 'eclipse-temurin@sha256:<摘要>'
```

离线包包含镜像、Compose 配置、初始化与预检脚本及 `SHA256SUMS`。目标机使用 Ubuntu 24.04/amd64、Docker Engine、Compose 和 Python 3。

## 部署与启用

使用 Ops 时，在「版本制品」导入离线包中的 `preview-node-image.tar`，发布预览节点实例，再从 Platform 的 Ops 目录引入。手工部署时，在每台目标机的独立包目录执行：

```bash
sha256sum -c SHA256SUMS
docker load -i preview-node-image.tar
sudo python3 configure.py --node-id preview-a --name '预览 A' \
  --media-ip 192.0.2.20 --control-host 192.0.2.20 --allow 192.0.2.10:554
```

替换示例地址。部署多台节点时，为各节点配置独立 ID 和地址，每个允许的相机端点传入一个 `--allow`。将 `.env` 中的 `PREVIEW_BIND_IP` 设为实际网卡地址，执行 `sudo ./preflight.sh` 和 `docker compose up -d`。Platform 要能访问节点控制口 9443/TCP，浏览器要能访问媒体端口 18000/TCP、UDP，节点要能访问获准的相机。ZLM 管理接口仅在容器回环可用。

手工部署后，将 `platform-registration` 文件复制到 Platform 的保护目录，修改 `connection.json` 的 `tokenFile` 为平台上的绝对路径，并配置 `SF_PREVIEW_NODE_CONNECTION_FILE`。在「监控面板 → 预览节点」核对节点、配置预算并启用，最后在预览中心试播。每节点观看和转码上限配置为 64，实际容量以目标码流压测为准。

维护前先停止接收新预览，确认在途资源清空后再停节点。保留 `config/`、`state/` 和原镜像，升级或回退后须在 Platform 复查并显式启用。排查时可沿 `traceId`、`leaseId` 查询节点诊断及 `docker compose logs --tail 100 preview`。
