# StreamFusion AI

StreamFusion AI 是视频接入与运维平台。当前管理端支持用户与权限、相机档案、设备目录导入、状态巡检、ZLMediaKit WebRTC 预览和 S3 图床。Node Agent 和 Algorithm Runtime 目前提供独立的开发骨架，模型推理与自动告警尚未接入。

[快速开始](#快速开始) · [开发指南](DEVELOPMENT.md) · [StreamFusion Ops](operations/README.md) · [问题反馈](https://github.com/flycodeu/StreamFusion-AI/issues)

## 快速开始

以下以 Windows PowerShell 为例。运行管理端需要 Git、JDK 21、MySQL 8.0.17+、Redis、Node.js 24.21.0 和 pnpm 10.34.5。Maven 由仓库自带的 Wrapper 提供。设置 `JAVA_HOME` 指向 JDK 21。

```powershell
git clone https://github.com/flycodeu/StreamFusion-AI.git
cd StreamFusion-AI
```

### 1. 初始化数据库

创建专用空库，按 [SQL 指南](platform-api/sql/README.md) 导入一次 [完整初始化 SQL](platform-api/sql/streamfusion-mysql.sql)。**初始化文件包含 `DROP TABLE`，仅用于空库。**

复制本地配置，从示例填写 MySQL 账号、密码和 Redis 连接。已有本地配置时跳过复制。

```powershell
Copy-Item platform-api/config/application-local.yml.example platform-api/config/application-local.yml
```

### 2. 创建管理员

从仓库根目录执行。在终端按提示输入首位管理员的账号和密码。

```powershell
Push-Location platform-api
.\mvnw.cmd -DskipTests package
java -jar target/platform-api-0.1.0-SNAPSHOT.jar --spring.profiles.active=local --bootstrap-admin
Pop-Location
```

如需指定初始部门或使用环境变量引导，见 [管理员初始化](DEVELOPMENT.md#首位管理员初始化)。

### 3. 启动管理端

安装前端依赖。

```powershell
pnpm --dir platform-web install --frozen-lockfile
```

在两个终端中分别从仓库根目录运行。

```powershell
.\scripts\start.ps1 api -Profile local
.\scripts\start.ps1 web
```

打开 [管理界面](http://127.0.0.1:8090) 并使用刚创建的账号登录。API 默认监听 `127.0.0.1:8080`。查看 API 与 Web 的连通状态可从根目录执行 `.\scripts\check-health.ps1 -Services api,web`。

Linux 或 macOS 可在 `platform-api` 执行 `sh mvnw -DskipTests package`，沿用上面的 Java 引导命令，再执行 `sh mvnw spring-boot:run -Dspring-boot.run.profiles=local`。在 `platform-web` 执行 `pnpm dev`。

## 相机与预览

相机接入需配置持久加密密钥，并为设备探测、平台导入和取流配置获准的目标网段。预览还需部署 ZLMediaKit，确保它能访问相机 RTSP，浏览器能访问 WebRTC 媒体端口。见 [后端外部配置](platform-api/config/README.md)。

## 工程入口

| 目录 | 用途 |
| --- | --- |
| `platform-api`、`platform-web` | Java/Spring Boot 管理 API 与 Vue 管理界面。见 [开发指南](DEVELOPMENT.md) |
| `operations` | 独立的 Linux 主机、软件安装和发布控制台。见 [Ops 启动与使用](operations/README.md) |
| `algorithm-node` | 独立的 Node Agent 和 Algorithm Runtime。见 [算法节点运行方式](algorithm-node/README.md) |
| `camera-inspector` | 相机巡检控制服务及媒体、证据上传的实验工具。见 [Camera Inspector](camera-inspector/README.md) |
| `contracts` | 跨组件契约、样例与校验。见 [协议说明](contracts/README.md) |

提交变更前运行涉及模块的 [检查命令](DEVELOPMENT.md#验证命令)。报告问题时请附复现步骤、环境和脱敏日志。
