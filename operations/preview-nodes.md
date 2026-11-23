# 用 Ops 部署和维护预览节点

本机预览不需要 Ops。增加远程预览节点时，由 Ops 部署节点，Platform 引入节点并决定调度。自行安装的专用 ZLMediaKit 可使用[专用 ZLM 说明](zlm-direct/README.md)。

## 首次部署

1. 准备 Ubuntu 24.04 amd64、Docker Engine 和 Compose。Ops 主机的接入组件须支持 `preview-node-v1`。
2. 按节点包说明构建同版本离线包，核对可信 SHA-256。解包后在 Ops「版本制品」选择「预览节点」，导入 `preview-node-image.tar`。镜像须为 `streamfusion/preview-node:*`，具备 `com.streamfusion.preview.maintenance=1`。[节点包构建说明](../platform-api/preview-node/README.md)
3. 在「应用实例」新建预览节点，填写唯一的 `nodeId`、绑定 IPv4、控制地址、浏览器可达的媒体 IPv4 及允许访问的相机 RTSP 地址。核对配置后发布。默认端口为控制 9443/TCP、媒体 18000/TCP 和 UDP。ZLM 管理端口留在内部。
4. 给 Ops 和 Platform 分别配置内容相同的服务身份文件。Ops 使用 `--platform-token-file`，Linux 安装脚本要求源文件为 0600，所在目录为 0700。Platform 设置 `SF_PREVIEW_OPS_BASE_URI`、`SF_PREVIEW_OPS_TOKEN_FILE` 和 `SF_PREVIEW_NODE_CREDENTIAL_DIRECTORY`。Ops 的非回环地址须使用 Platform 信任的 HTTPS。[Platform 配置](../platform-api/config/README.md)
5. 在 Platform「监控面板 → 预览节点」引入已发布实例，核对身份、容量和连接状态。启用调度后到预览中心检查浏览器首帧。

每台主机使用相同的默认端口时部署一个节点。已有手工节点需要先排空，再显式设置来源。

## 升级和回退

1. 在 Platform 停止该节点接新预览，等平台责任记录和节点资源均归零，再申请维护许可。
2. 在 Ops 提交新版本或回退计划，填写许可编号。Ops 需要在 10 分钟内领取。超时后节点继续保持封闭。
3. 等原作业完成并收到目标确认。升级健康检查失败时，Ops 尝试恢复上一成功版本。回退使用该版本的完整配置，要求镜像支持相同的维护和资源格式。
4. 在原 Platform 节点执行「同步 Ops 配置」，核对当前发布版本和配置，再解除维护并显式启用调度。回到预览中心验证首帧。

同一 Ops 实例内保持 `nodeId` 和控制、媒体地址稳定。更换节点身份需排空旧节点，再登记新节点。程序回退应保留原身份、责任目录和成功版本材料。

## 常见故障

| 现象 | 处理 |
| --- | --- |
| 许可无效或过期 | 核对节点版本与排空状态。确认旧许可尚未领取后，按平台提示解除并重新申请。 |
| 作业为 `UNKNOWN` 或等待目标确认 | 在 Ops 核对原作业，保留锁、作业编号和责任记录。 |
| Ops 已完成，平台配置版本不一致 | 保持排空，在原节点同步配置，再解除维护。 |
| 平台控制检查通过，浏览器无首帧 | 检查节点到相机的 RTSP 白名单、浏览器到媒体地址的 TCP/UDP 及实际码流。 |

故障可按 Platform 的节点 ID 和维护许可、Ops 作业 ID、目标 `jobs/<jobId>` 与容器日志定位。
