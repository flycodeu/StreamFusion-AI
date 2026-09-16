# 开发基础约定

本文描述工程基础，不代表视频推理、节点同步或生产部署已实现。

## 本地环境检查

从仓库根目录运行：

```powershell
.\scripts\doctor.ps1
# 多版本环境可显式传入 -JavaHome <JDK目录> -NodePath <node.exe路径>
```

脚本只检查，不安装依赖、不修改全局环境、不停止进程。失败返回退出码 1。
检查 JDK 21、Node 24.21.0、pnpm 10.34.5、Maven Wrapper、uv、
前端依赖目录、两个 Python 3.12 解释器及 FastAPI 导入、Python 配置与默认端口占用。
pnpm 检查禁用 Corepack 网络下载。端口占用为警告，可能是你已经启动的服务。
它不是依赖完整性审计；安装锁文件、构建和启动测试仍需单独执行。

IDEA 的项目 SDK、Maven 导入和运行 JDK 均选择 21。
PyCharm 分别使用 `node-agent/.venv`、`runtime/.venv`，不要选择父目录或全局解释器。
修改工具链后重新导入项目；终端检查通过不代表 IDE 自动选择了相同解释器。

## 配置入口

### MySQL、MyBatis-Plus、Redis

后端开发运行现在需要 MySQL 和 Redis。MyBatis-Plus 使用 Boot 3 专用 starter；
MySQL 驱动、Redis/Lettuce 和 Spring Security 版本由 Spring Boot 管理。
不重复引入 MyBatis starter，不创建空 Mapper、通用 RedisUtils 或尚无业务消费者的分页插件。

| 环境 | 连接来源 |
|---|---|
| dev | MySQL URL 默认本地 streamfusion，账号密码从 DB_USERNAME / DB_PASSWORD 注入；Redis 默认本机 6379、数据库 10 |
| local | 被忽略的 platform-api/config/application-local.yml，适合个人连接配置 |
| prod | 显式设置 DB_URL、DB_USERNAME、DB_PASSWORD、REDIS_HOST、REDIS_DATABASE、REDIS_PASSWORD；REDIS_PORT 默认 6379 |
| test | 仅测试资源中的 H2 内存数据库；Redis 健康检查关闭，不连接个人 Redis |

标准覆盖入口为 Spring 配置；不使用 REDIS_URL，避免 URL 中数据库号覆盖单独的 database 配置。
普通开发变量为上述 DB_* / REDIS_*；local 文件中可直接修改连接参数。
本轮不自动建库、建用户表或初始化数据；没有迁移脚本，因为没有业务结构变更。
首次运行前由开发者确认项目数据库已存在。不要使用系统业务库代替缺失的项目库。

从示例复制 local 文件并填写连接信息后，使用：

```powershell
.\scripts\start.ps1 api -Profile local
```

实际 local 文件不提交、不进入 JAR。公共示例不放真实 MySQL 账号密码。
示例 JDBC 的关闭 TLS、公钥获取参数只供本机开发；生产使用最小权限数据库账号及经验证的 TLS。
Redis 无密码仅适用于受控本机开发；不要将无认证 Redis 暴露到公网。

数据库连接池上限 10，连接等待 3 秒；Redis 连接/命令超时均 3 秒，
MyBatis 默认语句超时 5 秒。无 SQL 自动初始化、无 Redis 启动写入。
数据源连接按需建立，因此“进程启动”不能代替连接成功；/actuator/health 会检查数据库与 Redis，
失败返回 DOWN/503，且不公开连接细节。

普通 `mvnw.cmd verify` 无需本机数据库，H2 只证明组件装配与基础查询，不证明 MySQL 完全兼容。
真实本地连接检查需明确启用，只有 SELECT 1 与 Redis PING，不写数据：

```powershell
# 在 platform-api 目录执行，先确认 local 文件中的目标
.\mvnw.cmd "-Dtest=LocalInfrastructureTest" "-Dsf.test.localInfrastructure=true" test
```

### Spring Security 基础

当前仅引入安全边界，尚无登录接口、用户认证提供者或用户表：

- GET /actuator/health 公开；接口文档开启时 GET /v3/api-docs 公开，prod 默认关闭文档。
- 其余请求默认拒绝；匿名安全请求返回 JSON 401，不跳登录页面、不发 Basic 挑战。
- 保留 CSRF；缺少 CSRF 的写请求返回 JSON 403。
- 不生成默认 user 或随机密码，不启用 formLogin、HTTP Basic、默认 logout。
- 复用现有错误体与 traceId，不建立第二套错误模型。
- 未选定 JWT、Session JDBC 或 Redis 会话；引入 Redis 不等于已经决定使用它保存登录状态。

后续登录功能要显式开放对应入口并实现认证，不可临时改为全局 permitAll。

### 应用配置

后端继续保留 dev / local / prod / test 隔离。公共配置对象在
[PlatformProperties.java](platform-api/src/main/java/com/streamfusion/platform/common/config/PlatformProperties.java)，
环境专属端口和日志级别仍在对应 profile 中；真实 local 文件不提交。

| Java 配置 / 环境变量 | 默认值 | 校验或边界 |
|---|---|---|
| server.address / SERVER_ADDRESS | 127.0.0.1 | Spring 地址绑定解析 |
| server.port / SERVER_PORT | 8080 | 1–65535；仅 test 允许 0 |
| streamfusion.work-dir / SF_API_WORK_DIR | .run | 可写目录或可写的现有祖先目录 |
| streamfusion.shutdown-grace / SF_API_SHUTDOWN_GRACE | 15s | 1–120s，停机阶段等待 |
| API_DOCS_ENABLED | dev/local 为 true，prod 为 false | 控制 /v3/api-docs |

目前没有异步控制器或出站 HTTP 调用，不预留通用请求超时配置；新增真实调用时在调用边界定义超时。
Java 日志系统在配置对象绑定前初始化，启动失败也可能生成日志目录。

Python 各自在 `app/settings.py` 定义配置，在 `app/__main__.py` 启动进程。
复制各目录 `.env.example` 为 `.env.local`（已有文件勿覆盖），然后在各目录运行：

```powershell
uv sync --locked
uv run --locked python -m app
```

环境变量优先于该工程的 `.env.local`，再使用代码默认值。
Agent 前缀为 `SF_AGENT_`，Runtime 为 `SF_RUNTIME_`。不互相加载配置或导入代码。

| 配置后缀 | 默认值 | 校验 |
|---|---|---|
| HOST | 127.0.0.1 | IP 地址或 localhost |
| PORT | Agent 8100 / Runtime 8101 | 1–65535 |
| WORK_DIR | 工程目录/.run | 可写目录或可写祖先；相对路径基于所属工程目录 |
| HTTP_KEEPALIVE_SECONDS | 5 | 1–120，连接空闲等待，不是推理执行时限 |
| SHUTDOWN_GRACE_SECONDS | 15 | 1–120 |
| LOG_LEVEL | INFO | DEBUG / INFO / WARNING / ERROR |
| LOG_MAX_BYTES | 10485760 | 1024–104857600 |
| LOG_BACKUPS | 5 | 1–10 |
| DOCS_ENABLED | true | 控制 /docs 与 /openapi.json |

配置错误会阻止正常启动。不认识的 dotenv 配置键也会报错。
统一使用 `python -m app`，让启动参数与应用使用同一份配置。
`app.main` 仅提供应用工厂，不再导出全局 `app`；旧的 `uvicorn app.main:app` 命令不再适用。
部署时关闭不需要的 Python 文档；当前不存在认证或生产加固。

前端仍使用被忽略的 `platform-web/.env.local` 中的 `API_TARGET`。
不要把私密信息放进 `VITE_*` 变量，这类变量会进入浏览器包。

## 日志与请求关联

Java 输出 UTC 文本日志，Python 输出 UTC JSON 日志，共用字段语义：
时间、level、service、traceId；请求结束记录 method、路由模板、status、durationMs。
请求头 `X-Trace-Id` 只接收 32 位小写十六进制，不合法则重新生成并在响应头返回。
前端自动生成请求标识，出错时显示错误码及标识，默认请求超时 5 秒，不自动重试。
启动和停机等非请求日志的 traceId 为 `-`。
这只是请求关联基础；还未实现跨服务调用、异步线程 MDC 传播或分布式追踪。

日志位置：各服务工作目录的 `logs/`。

- Java：单文件滚动阈值 10MB，归档保留 7 天，归档总量上限 100MB（不含当前文件）。
- Python：默认 10MiB 当前文件和 5 个备份；单条日志可能让阈值有少量超出。
- 消息与异常文本分别最多 16KiB；文件、缓存、个人配置不进入 Git。
- 不记录请求正文、原始查询串；访问日志使用路由模板。
- 抑制 Python HTTP 客户端的 DEBUG/INFO 原始 URL 日志及 Uvicorn 默认 access log。
- 对已知 password/token/secret/key、Bearer 和 URL 用户凭证进行脱敏。

脱敏是补充保护，不能保证识别任意格式的秘密。业务代码仍不得主动记录凭证；
新增第三方组件及 DEBUG 日志必须检查是否泄露信息。日志保留在本地不等于可以公开上传。

## 验证命令

| 工作目录 | 命令 |
|---|---|
| platform-api | `mvnw.cmd verify`（Linux/macOS：`sh mvnw verify`） |
| platform-api，主动格式化 | `mvnw.cmd spotless:apply` |
| platform-web | `pnpm lint`、`pnpm format:check`、`pnpm test`、`pnpm build` |
| platform-web，主动格式化 | `pnpm format` |
| 两个 Python 工程分别执行 | `uv run --locked ruff check app tests`、`uv run --locked ruff format --check app tests`、`uv run --locked mypy`、`uv run --locked python -m pytest -q` |
| 仓库根目录 | `uv run --project algorithm-node/node-agent --locked python -m pytest contracts/tests -q` |

GitHub Actions 在 push、PR 或手动触发时检查 Windows/Linux 构建、格式、类型和测试；
只读权限、固定 action 提交、无部署步骤、不使用生产密钥。远端运行结果以实际 Actions 为准。
CI 不执行视频、GPU、真实节点同步或生产集成验收。

公共 API 约定和协议维护方式见 [contracts](contracts/README.md)。
新增模块先复用当前配置、日志和错误入口，不要复制控制器内的临时异常处理。

## 扩展原则

- 新增配置、依赖和公共类时说明当前调用者，不为尚未实现的模块预留空抽象。
- 先写清协议与边界，再在第一个实际接口中落实约束；分页等约定见 contracts。
- 不为减少行数合并不同职责。Agent 与 Runtime 保持独立；提取公共包前评估维护、安装和发布成本。
- 公共日志和错误规则变更时检查两个 Python 工程，避免一端更新、另一端遗漏。
