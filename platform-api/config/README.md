# 后端外部配置

复制 [application-local.yml.example](application-local.yml.example) 为同目录的 `application-local.yml`，填写 MySQL 与 Redis 后使用 `local` profile 启动。配置文件以 API 工作目录为准，也可用 `LOCAL_CONFIG_PATH` 指定绝对路径。首次安装使用 [快速开始](../../README.md#快速开始) 和 [完整初始化 SQL](../sql/streamfusion-mysql.sql)。

## 在本机切换数据库

在 IDEA 的 Spring Boot 运行配置中，把工作目录设为 `platform-api`，在 **Active profiles** 中填 `local-dev` 或 `local-test`。使用仓库根目录的 `scripts/start.ps1` 时，脚本会自动切换工作目录。未选 profile 时使用共享的 `test` 配置。

| Profile | 本目录中的私有文件 | 连接目标 |
| --- | --- | --- |
| `local-dev` | `application-local-dev.yml` | 本机的 MySQL 和 Redis，地址固定为 `127.0.0.1` |
| `local-test` | `application-local-test.yml` | 个人指定的测试服务器。MySQL、Redis 的地址、端口和账号在文件中维护 |

两份文件都由 Git 忽略，并存放在 JAR 外。测试服务器的密码可填写在私有文件中，也可通过 `DB_PASSWORD` 和 `REDIS_PASSWORD` 提供。

## 必需部署配置

相机档案需要持久 AES 密钥。设备探测、平台导入和预览取流还需要明确批准的目标网段或域名。将配置放在受保护的外部文件中。

```yaml
platform:
  camera:
    active-key-id: camera-v1
    keys:
      camera-v1: "<32 字节随机值的 Base64 编码>"
    network-policies:
      factory:
        name: 工厂摄像机网段
        cidrs: ["10.20.0.0/16"]
        hosts: []
```

将示例网段替换为实际批准范围。`hosts` 仅填写完整域名。服务会校验出站地址，包括设备返回的 RTSP 地址及域名解析结果。密钥轮换时保留旧 keyId，直到所引用的密文已经迁移且原创建回执到期。

## 预览配额与重启清理

本机 ZLM 从 [媒体环境示例](../src/main/resources/media-runtime.example.yml) 配置搜索目录、可执行文件和配置文件。可用 `SF_MEDIA_RUNTIME_ROOTS` 设置搜索根。启动 ZLM 后在管理端「服务信息」检测并选择对应运行时，确认浏览器可访问 WebRTC 媒体端口。

| 本机配置 | 默认 | 范围 |
| --- | --- | --- |
| `SF_PREVIEW_MAX_SESSIONS` | 64 | 1～64 个观看会话 |
| `SF_PREVIEW_MAX_TRANSCODES` | 64 | 0～64 个独立转码输出，0 禁止新转码 |

同 Profile 的观看者可共享摄像机上游，同配置的转码也可复用。配额需要按服务器、媒体和网络实测设置。重启后旧流由平台按工作目录中的记录清理，用户需重新打开预览。清理未确认时，保留记录并检查原 ZLM 可达性和诊断码。

## H.265 预览转码配置

本机预览可按需将 H.265 转成 H.264。软件编码使用 [libx264 片段](zlm-preview.ini.example)，NVIDIA 硬件编码使用 [NVENC 片段](zlm-preview-nvenc.ini.example)。选择适用片段合并到 ZLM 实际加载的 `config.ini`，保留端口、密钥和网络配置。`ffmpeg.bin` 必须与平台选中的 FFmpeg 一致，当前网关要求路径不含空格。

网关允许 `libx264`、`h264_nvenc`、`h264_qsv`、`h264_amf`。QSV 与 AMF 按实际驱动配置。应用配置后检查 FFmpeg 日志、浏览器解码和播放关闭后的资源回收。

## 按需增加预览节点

默认使用本机。独立 ZLM 直连首期用于 H.264 无转码流，安装及权限要求见 [直连 ZLM 说明](../../operations/zlm-direct/README.md)。需要远程转码时使用 [独立预览节点包](../preview-node/README.md)。

预览节点可从 Ops 已发布的实例引入。Ops 启动时配置 `--platform-token-file`，Platform 配置 `SF_PREVIEW_OPS_BASE_URI`、`SF_PREVIEW_OPS_TOKEN_FILE` 和 `SF_PREVIEW_NODE_CREDENTIAL_DIRECTORY`。非回环连接使用可信 HTTPS。引入后显式启用调度。高级登记入口可用 `SF_PREVIEW_NODE_CONNECTION_FILE` 读取受保护的连接 JSON。部署和引入步骤见 [Ops 节点说明](../../operations/preview-nodes.md)。

节点凭据目录应提前创建并授权给 Platform。独立 ZLM 的默认持久密钥目录为 `~/.streamfusion/preview-zlm-secrets`，设定 `SF_PREVIEW_NODE_CREDENTIAL_DIRECTORY` 时使用指定目录。调整节点连接、证书或码率前先禁止接收新预览并确认资源清空。

## 备份与恢复

数据库、相机加密密钥和节点凭据应配套备份。恢复时先停止应用写入，在隔离环境核对结构、数据和旧密文可读性，再切换应用。真实配置与备份存放在仓库之外。
