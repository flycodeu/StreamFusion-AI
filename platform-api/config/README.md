# 后端外部配置

复制 `application-local.yml.example` 为同目录的 `application-local.yml`，填写 MySQL、Redis及需要启用的相机配置，再使用 local profile 启动。实际文件被 Git 忽略，公开示例不包含可用凭据。完整安装步骤见[快速开始](../../README.md#快速开始)，数据库只需导入[完整初始化 SQL](../sql/streamfusion-mysql.sql)。

## 必需部署配置

通过既有外部 `application-local.yml` 或环境配置提供，真实密钥不入 Git：

```yaml
platform:
  camera:
    active-key-id: camera-v1
    keys:
      camera-v1: "<安全生成的32字节随机值，Base64编码>"
    network-policies:
      factory:
        name: 工厂摄像机网段
        cidrs: ["10.20.0.0/16"]
        hosts: []
```

示例网段必须换成实际批准目标。`hosts` 仅允许显式批准的完整域名。保存配置不执行设备探测；发现任务实际外呼时解析并校验全部DNS结果，以已验IP建立连接，ONVIF返回地址同样受出站策略约束。当前没有媒体链路。

使用 JDK AES-256-GCM 保护账号密码及 RTSP path/query；AAD 绑定来源/凭据或 Profile，随机 nonce 每次独立。主密钥通过用途标签派生存储加密、会话绑定、创建指纹和确认签名用密钥。持久密钥是档案加密及幂等回执的前提；网络策略只限制读取/扫描/导入外呼，手工DEVICE建档不要求联网策略。不自动生成临时业务密钥。

轮换先加入新 keyId 并切换 active-key-id；旧密钥必须保留到所有旧密文已迁移且旧创建回执已过期。当前未提供自动批量重加密工具，因此不能删除仍有数据引用的旧密钥。数据库备份必须与密钥备份配套，只有数据库无法恢复凭据。

## 备份与恢复

数据库与持久密钥须配套备份，备份和真实配置不进入仓库。恢复前停止应用写入，在隔离环境验证结构、数据与旧密文可读性，再切换应用。回退应用版本时可保留新增的可空相机本地名称和备注列；删除字段会丢失用户编辑的资料。
