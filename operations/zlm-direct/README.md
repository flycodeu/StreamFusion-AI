# 独立 ZLM 部署配方

Ops 负责安装、发布和回退 ZLM；Platform 负责连接、预览和分配流。此配方与 `preview-node-v1` 独立，首期只提供 ZLM 直连预览，不执行转码。单实例源和观看预算各不超过 64。

在有 Docker 的 Linux 构建机生成归档，必须传入已核对的不可变上游镜像摘要：

```bash
bash build-package.sh /tmp/zlm-direct-package 'zlmediakit/zlmediakit@sha256:<64位摘要>'
```

在 Ops 中先登记目标和 64 位十六进制 TOKEN 凭据，再以 `zlm-direct-v1` 导入 `zlm-direct-image.tar` 和 `SHA256SUMS` 中的可信摘要。创建独立 ZLM 实例时填写目标绑定 IPv4、服务地址（首期应相同）、API/WebRTC 端口、凭据修订和 Platform 实际出口 IPv4。源/观看预算、CPU 和内存可在容量配置中调整。完成目标核对、预检、发布后，Platform 才会在受限实例目录看到它。Platform 后端用服务 Bearer 和发布版本 ID 交接 API secret；浏览器只看到非敏感目录。

发布和回退沿用 Ops 的预检、作业、健康及既有回滚流程。升级期间已有预览是否中断取决于 ZLM 容器重建，需在业务低峰排空或接受短时中断；此配方没有预览节点维护许可机制。Platform 应在引入时从真实出口执行 API 连通检查；来源 IP 不匹配时调整 Ops 实例草稿中的允许来源后重新发布。未在独立 Ubuntu 24.04/amd64 目标完成实机验收前，不将本配方标为生产验证通过。
