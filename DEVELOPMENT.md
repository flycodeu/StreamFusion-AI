# 开发指南

## 本地环境

按 [快速开始](README.md#快速开始) 初始化并启动管理端，从根目录运行 `.\scripts\doctor.ps1` 检查工具链、依赖和默认端口。后端使用 JDK 21。Agent 和 Runtime 使用各自目录内的 Python 3.12 虚拟环境，安装与启动见 [算法节点说明](algorithm-node/README.md)。

## 配置入口

| 场景 | 配置入口 |
| --- | --- |
| 本地 API | 复制 `platform-api/config/application-local.yml.example` 为同目录的 `application-local.yml`，使用 `local` profile。可用 `LOCAL_CONFIG_PATH` 指定绝对路径 |
| 共享开发 API | 使用 `dev` profile，设置 `DB_USERNAME`、`DB_PASSWORD`。Redis 默认回环地址、6379、库号 10 |
| 默认测试环境 | 运行 `.\scripts\start.ps1 api`，默认使用 `test`。通用连接参数见 [application-test.yml](platform-api/src/main/resources/application-test.yml) |
| 本机开发数据库 | 运行 `.\scripts\start.ps1 api -Profile local-dev`，连接 `127.0.0.1` 上的 MySQL 和 Redis，使用本机保存的连接资料 |
| 本机连接测试服务器 | 运行 `.\scripts\start.ps1 api -Profile local-test`。服务器地址与账号保存在本机，配置方式见 [后端外部配置](platform-api/config/README.md) |
| 部署 API | 使用 `prod` profile，显式设置 `DB_URL`、`DB_USERNAME`、`DB_PASSWORD`、`REDIS_HOST`、`REDIS_DATABASE`、`REDIS_PASSWORD`。默认 Redis 端口为 6379 |
| 本地 Web | 需要修改代理目标时，将 [环境示例](platform-web/.env.example) 复制为 `platform-web/.env.local`，设置 `API_TARGET` 后重启 Vite |

`SERVER_ADDRESS`、`SERVER_PORT` 默认是 `127.0.0.1:8080`。部署环境应使用已验证的数据库 TLS、最小权限数据库账号和认证 Redis，经 HTTPS 提供 API 与静态页面。prod 默认启用 `SF_AUTH_SECURE_COOKIE` 并关闭 `API_DOCS_ENABLED`。日志工作目录、会话时限和其他参数见 [公共配置](platform-api/src/main/resources/application.yml) 与 [prod 配置](platform-api/src/main/resources/application-prod.yml)。

新安装执行 [完整 SQL](platform-api/sql/streamfusion-mysql.sql)。开发者修改 [逐表 SQL](platform-api/sql/业务/) 后，从仓库根目录运行 `./scripts/export-sql.ps1`，由脚本重新生成汇总。启动时只读检查必需表和字段。MySQL 业务时间使用 Asia/Shanghai，JDBC 配置保持同一时区。见 [SQL 指南](platform-api/sql/README.md)。

相机密钥、出站网络、ZLM 预览和备份见 [后端外部配置](platform-api/config/README.md)。图床端点使用独立白名单 `SF_STORAGE_ALLOWED_ENDPOINTS`，默认允许回环 8333，服务端访问地址须由运维批准。

### 首位管理员初始化

使用已初始化的数据库和构建完成的 API JAR。以下在 `platform-api` 的交互式终端执行。

```powershell
java -jar target/platform-api-0.1.0-SNAPSHOT.jar --spring.profiles.active=local --bootstrap-admin
```

引导执行一次并退出，用户表非空或已成功引导时会拒绝。用 `--platform.bootstrap.department-id=` 留空初始部门。非交互引导使用 `--platform.bootstrap.default-admin=true`，由 `SF_BOOTSTRAP_USERNAME`、`SF_BOOTSTRAP_NICKNAME`、`SF_BOOTSTRAP_DEPARTMENT_ID`、`SF_BOOTSTRAP_PASSWORD` 控制，以这种方式创建的账号首次登录需改密。普通用户新建、重置使用 `SF_AUTH_INITIAL_PASSWORD`。部署环境显式提供引导密码和普通用户初始密码。

### 登录与模块授权

浏览器使用 Redis 会话、Cookie 和 CSRF。集成时先取 `GET /auth/csrf`，以同一 Cookie 调用 `POST /auth/captcha` 和 `GET /auth/login/challenge`，按现有实现加密后调用 `POST /auth/login/secure`。成功后重新获取 CSRF，写请求携带新 CSRF 和 Cookie。查看接口文档需登录并拥有 `api-docs` 页面权限。见 [前端加密实现](platform-web/src/api/auth/cipher.ts)。

管理权限按用户、有效角色和启用的页面关系判断。并发编辑沿用 `version` 或 `If-Match`，错误通过统一响应及 `traceId` 定位。动态页面和前端接口约定见 [前端开发说明](platform-web/DEVELOPMENT.md)。

### IP 登录防护

`SF_IP_GUARD_ENABLED` 默认启用。默认同一来源 IP 十分钟内二十次失败后封禁，由未封禁地址的超级管理员从「监控面板 → 操作记录 → IP 封禁」解除。阈值通过 `SF_IP_FAILURE_THRESHOLD` 和 `SF_IP_FAILURE_WINDOW` 配置，登录资源限频通过 `SF_IP_RESOURCE_LIMIT` 和 `SF_IP_RESOURCE_WINDOW` 配置。

反向代理须清理客户端自行提交的来源头，再传入真实来源链。后端的 `SF_TRUSTED_PROXIES` 仅填写实际代理 IP，使用逗号分隔。登录地域数据可选，见 [离线地域数据库](scripts/IP-REGION.md)。

### 前端生产访问路径

开发和生产前端都通过同源 `/api/` 访问后端。开发代理会去掉此前缀；发布 `platform-web/dist` 时，网关也要把 `/api/` 请求去前缀后转发给 Java，例如 `/api/auth/me` 转到 `/auth/me`。Vue 的 `/preview/center`、`/storage/manage` 等页面仍走静态站点和 SPA 回退。保留 Cookie、CSRF、`X-Trace-Id`、`If-Match` 和后端原响应状态。使用 Ops 发布时见[整站发布步骤](operations/project-delivery.md#streamfusion-整站发布)。

在静态服务器设置下列响应头。开发与本地预览已设置相同头部。

```nginx
add_header Content-Security-Policy "frame-ancestors 'self'" always;
add_header X-Frame-Options "SAMEORIGIN" always;
add_header X-Content-Type-Options "nosniff" always;
add_header Referrer-Policy "strict-origin-when-cross-origin" always;
```

`VITE_*` 会编译入浏览器包。登录、改密和路由刷新应在实际发布代理上验证。

## 按业务查找代码

| 要改的行为 | 首先看 |
| --- | --- |
| 相机来源列表、详情、端点和凭据配置 | `platform-api` 的 `camera/service/CameraSourceQueries.java` 负责读取；`CameraSourceService.java` 负责写入和事务。 |
| 相机取流地址、预览接入与租约 | `camera/locator/CameraStreamLocator.java` 管理取址和连接身份；`media/preview/source/PreviewLocatorService.java` 校验媒体目标；`media/preview/` 管理节点、媒体与租约。 |
| 网页预览目录、收藏、播放器或节点状态 | [预览维护入口](platform-web/src/features/preview/README.md)按行为列出页面和状态归属。 |
| Jenkins 构建、项目环境、发布、服务检查与日志 | [Ops 维护入口](operations/project-delivery.md#维护入口)区分仓库识别、项目规则、控制端作业和目标端执行。 |

## 验证命令

| 工作目录 | 命令 |
| --- | --- |
| `platform-api` | `mvnw.cmd verify` 或 `sh mvnw verify` |
| `platform-web` | `pnpm lint`、`pnpm format:check`、`pnpm test`、`pnpm build` |
| `algorithm-node/node-agent`、`algorithm-node/runtime` | 各运行 `uv run --locked ruff check app tests`、`uv run --locked mypy`、`uv run --locked python -m pytest -q` |
| 仓库根目录 | `./scripts/export-sql.ps1 -Check`、`uv run --project algorithm-node/node-agent --locked python -m pytest contracts/tests -q` |
| `operations` | `go test ./...`、`pnpm --dir web test`，构建入口见 [Ops 说明](operations/README.md) |

平台 API 的自动化集成测试使用 `it` profile 和 H2 内存库。要执行本机 MySQL 结构和依赖连通性只读检查，应先确认目标配置，再在 `platform-api` 运行：

```powershell
.\mvnw.cmd "-Dtest=LocalSchemaInspectionTest,LocalInfrastructureTest" "-Dsf.test.localSchema=true" "-Dsf.test.localInfrastructure=true" test
```

真实 HTTP 会话测试需专用 Redis `127.0.0.1:16379`，使用 `-Dtest=RedisSessionIntegrationTest -Dsf.test.redisSession=true`。运行中的 API 与 Web 可用 `./scripts/check-health.ps1 -Services api,web` 复查。

## 排查与提交

API 与 Python 默认运行日志在各服务的 `.run/logs/`。沿响应 `X-Trace-Id` 查日志，检查任务可用 `checkId`、`planId`。确定只读健康检查、外部设备和真实媒体的验证范围后再报告结果。

改动应沿用现有配置、日志、错误和权限边界。公共接口变更先核对 [契约](contracts/README.md)。提交应包含源代码、测试及公开配置示例。本地配置、密钥、凭据、构建物和现场数据留在本地。
