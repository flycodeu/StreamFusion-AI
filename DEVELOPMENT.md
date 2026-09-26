# 开发基础约定

本文说明工程配置、管理功能与验证方式。登录、系统管理前后端、操作记录和服务信息已有实现；视频推理、节点同步和生产部署仍待完成。

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

后端开发运行需要 MySQL 和 Redis。MyBatis-Plus 使用 Boot 3 专用 starter；
MySQL 驱动、Redis/Lettuce 和 Spring Security 版本由 Spring Boot 管理。
不重复引入 MyBatis starter；分页使用现有 MyBatis-Plus 插件，不创建无业务消费者的空 Mapper 或通用 RedisUtils。

| 环境 | 连接来源 |
|---|---|
| dev | MySQL URL 默认本地 streamfusion，账号密码从 DB_USERNAME / DB_PASSWORD 注入；Redis 默认本机 6379、数据库 10 |
| local | 被忽略的 platform-api/config/application-local.yml，适合个人连接配置 |
| prod | 显式设置 DB_URL、DB_USERNAME、DB_PASSWORD、REDIS_HOST、REDIS_DATABASE、REDIS_PASSWORD；REDIS_PORT 默认 6379 |
| test | 仅测试资源中的 H2 内存数据库；Redis 健康检查关闭，不连接个人 Redis |

标准覆盖入口为 Spring 配置；不使用 REDIS_URL，避免 URL 中数据库号覆盖单独的 database 配置。
普通开发变量为上述 DB_* / REDIS_*；local 文件中可直接修改连接参数。
表结构只维护 platform-api/sql/业务 中的九份逐表文件，sql/汇总/streamfusion-mysql.sql 由 scripts/export-sql.ps1 自动生成。旧版八表库须先增加 sys_ip_block，再启动启用 IP 防护的新版后端，步骤见 [SQL 指南](platform-api/sql/README.md#已有库增加-ip-封禁表)。
首次运行前，按 [SQL 指南](platform-api/sql/README.md)手动初始化专用空库；应用不自动建库、建表或升级。
脚本先 DROP 再 CREATE，会清空目标表；已有数据的库禁止用它升级。本地已经初始化的数据库无需重导。
SQL 仅初始化内置角色、管理 PAGE 及可调整的示例组织，不包含默认用户或固定密码哈希。首次账号由下面的非 Web 引导创建。

从示例复制 local 文件并填写连接信息后，使用：

```powershell
.\scripts\start.ps1 api -Profile local
```

实际 local 文件不提交、不进入 JAR。公共示例不放真实 MySQL 账号密码。
示例 JDBC 的关闭 TLS、公钥获取参数只供本机开发；生产使用最小权限数据库账号及经验证的 TLS。
Redis 无密码仅适用于受控本机开发；不要将无认证 Redis 暴露到公网。

数据库连接池上限 10，连接等待 3 秒；Redis 连接/命令超时均 3 秒，
MyBatis 默认语句超时 5 秒。spring.sql.init.mode=never，SQL 不打包到应用 JAR，无 Redis 启动写入。
平台默认时区为 Asia/Shanghai（UTC+8）。Java 启动入口和 Jackson 默认时区使用 Asia/Shanghai，
MySQL 连接池执行 SET time_zone='+08:00'，H2 测试使用等价命令。
JDBC URL 的 connectionTimeZone 也应为 Asia/Shanghai（含生产 DB_URL），不能再覆盖为 UTC。
这只影响本项目连接，不修改 MySQL 全局或操作系统时区。DATETIME 业务字段按北京时间存储；
历史 UTC 数据不能只改配置，需要核查后停写转换；新库不得重复加8小时。详见 SQL 说明。
/actuator/health 检查数据库与 Redis 连通性，失败返回 DOWN/503，且不公开连接细节。
健康检查不校验业务表是否存在；初始化是否成功需单独核对。

普通 `mvnw.cmd verify` 无需本机数据库，H2 检查管理业务、组件装配、汇总 SQL 建表/重建与约束，不证明 MySQL 排序规则、JSON 类型或并发行为完全兼容。
真实本地连接检查需明确启用，只有 SELECT 1 与 Redis PING，不写数据：

```powershell
# 在 platform-api 目录执行，先确认 local 文件中的目标
.\mvnw.cmd "-Dtest=LocalInfrastructureTest" "-Dsf.test.localInfrastructure=true" test
```

汇总生成、重建限制和真实 MySQL 元数据检查见 [SQL 指南](platform-api/sql/README.md)。
其中 LocalSchemaInspectionTest 只读检查本机现有表、字段、账号唯一约束和排序规则等断言，不执行初始化或任何写操作；并非完整结构差异工具。

### 首位管理员初始化

使用已构建的 JAR 和完成初始化的数据库。`--bootstrap-admin` 强制使用非 Web 模式，结束后退出；普通 API 启动不会创建账号。用户表非空或已经存在成功引导记录时拒绝再次引导。

默认账号方式见 [README 的初始化命令](README.md#4-初始化数据库与首位管理员)：使用 `--platform.bootstrap.default-admin=true`。dev/local 默认账号为 `admin`、临时密码为 `StreamFusion@123`；可通过 `SF_AUTH_INITIAL_PASSWORD` 覆盖开发默认值，或用 `SF_BOOTSTRAP_PASSWORD` 单独覆盖本次引导。prod 须显式提供引导密码。创建后必须首次改密；已有账号不受默认值变更影响。真实密码不要写入 SQL、启动参数或仓库。

需要自行输入账号、昵称和强密码时，在 `platform-api` 目录的交互终端执行：

```powershell
java -jar target/platform-api-0.1.0-SNAPSHOT.jar --spring.profiles.active=local --bootstrap-admin
```

交互入口创建正常状态账号，密码须满足运行期规则：默认 8～64 位，同时包含大写、小写字母、数字和特殊字符，禁止空白及控制字符。它要求可读取密码的控制台，不适合无交互的管道输入。

| 配置 / 环境变量 | 默认值 | 用途 |
|---|---|---|
| platform.bootstrap.username / SF_BOOTSTRAP_USERNAME | admin | 默认账号方式的登录名 |
| platform.bootstrap.nickname / SF_BOOTSTRAP_NICKNAME | 超级管理员 | 默认账号方式的显示名 |
| platform.bootstrap.department-id / SF_BOOTSTRAP_DEPARTMENT_ID | 2002 | 两种引导方式共用的初始部门 ID；默认关联示例研发部门 |
| SF_BOOTSTRAP_PASSWORD | dev/local 缺省沿用初始密码；prod 必填 | 本次默认管理员引导密码 |
| platform.auth.initial-password / SF_AUTH_INITIAL_PASSWORD | dev/local 为 StreamFusion@123；prod 必填 | 后续新建/重置普通用户使用，成功响应仅当次返回临时密码；首次登录须改密 |

不需要初始部门时，在任一引导命令后加 `--platform.bootstrap.department-id=`，或在私有配置中将该项设为空。指定的部门不存在时引导整体失败，不留下半个账号。账号、SUPER_ADMIN 关联、全部 PAGE 关联、可选部门关联和引导审计在同一事务中保存。

示例组织“飞云科技公司 / 研发部门 / 运维部门”只是 SQL 初始数据，可通过部门 API 改名、移动，解除成员和子部门引用后删除。超级管理员可以调整本人及其他超管的部门归属；普通新建用户不自动关联部门。部门仅表示组织归属，不参与授权，也没有主部门。

### 登录与模块授权

认证采用 Spring Security、Spring Session Redis 与 `SF_SESSION` Cookie；Cookie 带 HttpOnly、SameSite=Lax。dev/local 允许本机 HTTP，prod 默认启用 Secure Cookie，应通过 HTTPS 访问。空闲超时默认 30 分钟、绝对时长默认 8 小时；连续第 5 次登录失败锁定 15 分钟。改密、重置或封禁后，数据库会话版本使旧会话失效。

API 客户端需按以下顺序对接：

1. `GET /auth/csrf`，保存 Cookie 及响应 `data.headerName`、`data.token`。
2. `GET /auth/login/challenge` 获取一次性挑战，按前端 `src/api/auth/cipher.ts` 使用 ECDH P-256、HKDF-SHA256、AES-GCM 加密账号和密码，再 `POST /auth/login/secure` 提交密文、携带 CSRF 与 Cookie。`/auth/login` 旧入口默认关闭。
3. 登录会轮换会话和 CSRF；重新请求 `/auth/csrf`，再读取 `GET /auth/me`。待改密账号只允许本人信息、改密和退出等入口。
4. 写请求继续携带最新 CSRF 令牌和 Cookie。`PUT /auth/password` 改密成功后重新登录；`POST /auth/logout` 撤销当前会话。

`GET /actuator/health`、CSRF 和登录入口公开；开启文档时 `/v3/api-docs`、`/swagger-ui.html` 可访问，prod 默认关闭。Swagger UI 不代替会话登录或 CSRF 获取。未知业务入口默认拒绝；未登录返回统一 JSON 401，缺少 CSRF 的写请求返回 403，不启用表单登录、HTTP Basic 或默认随机密码用户。

管理模块使用“用户 → 有效角色 → 有效 PAGE 及祖先 → module_key”授权。普通管理者可通过其管理 PAGE 管理普通对象，授予自己尚未持有的页面；超级管理员身份授予/移除及本人敏感操作仍有对象保护。SUPER_ADMIN 默认拥有全部 PAGE 关系，新建 PAGE 在同一事务补齐它的关联；没有绕过菜单状态的角色名放行逻辑。

管理 API 编辑使用单个 `version` 检查旧表单冲突，删除需要 `If-Match: "版本值"`。这是并发保护，不是业务版本历史。返回和错误沿用统一 `R` 与 `traceId`；管理审计保存对象及关系摘要，不记录密码或完整请求。管理前端与动态路由已接入，新增领域页面参见 [前端开发约定](platform-web/DEVELOPMENT.md)。

### IP 登录防护

同一规范化 IP 在十分钟窗口累计二十次登录失败后写入 `sys_ip_block`。封禁无到期时间，应用或 Redis 重启不会解除。成功登录不清除同一 IP 的失败累计；原账号五次连续失败锁十五分钟的保护继续生效。封禁后拒绝登录与业务接口，已有会话同样受限；健康检查、退出及用于退出的 CSRF 获取保留。

超级管理员须从未封禁的地址进入“系统管理 → 操作记录 → IP 封禁”，查询并确认解除。查询和解除接口同时检查 `audit` PAGE 与 SUPER_ADMIN 身份，普通操作日志查看者没有解封权限。解除使用 `If-Match` 防止旧记录覆盖，新世代使解除前的未完成登录尝试不能再次封禁。封禁与解除写入操作记录。

| 环境变量 | 默认值 | 用途 |
|---|---|---|
| SF_IP_GUARD_ENABLED | true | 启用来源防护；普通测试显式关闭，专项测试显式开启 |
| SF_IP_FAILURE_THRESHOLD | 20 | 同一 IP 的失败阈值 |
| SF_IP_FAILURE_WINDOW | 10m | 失败计数窗口，修改不解除已封禁地址 |
| SF_IP_RESOURCE_LIMIT | 120 | 同一 IP 的 CSRF、登录挑战及提交共享请求上限 |
| SF_IP_RESOURCE_WINDOW | 1m | 请求计数窗口；超过返回 429 与 Retry-After，不单独生成永久封禁 |
| SF_TRUSTED_PROXIES | prod 为空；dev/local 为 127.0.0.1,::1 | 逗号分隔的可信代理精确 IP，最多 32 个，不使用任意来源通配 |

失败计数、资源限频及挑战一次性消费由 Redis 原子操作完成；故障返回依赖不可用，不静默放行。失败窗口为首次失败起计的固定窗口。每个挑战只允许成功领取一次，凭据从日志与审计中排除。前端已登录的密码表单从 `GET /auth/password-policy` 获取实际规则，按 Unicode 码点计数，服务器仍作最终校验。

**代理配置：** 默认使用 TCP 对端 IP，只在对端命中可信代理列表时解释 `X-Forwarded-For`，从右向左寻找首个不可信来源。Vite 开发代理将客户端传入的来源链覆盖为实际 socket 地址，并删除 `Forwarded` 和 `X-Real-IP`。生产边缘代理也必须清理外部来源头，后端仅信任真实代理地址；不要将客户端网段加入代理白名单。否则会将多人错误合并到代理 IP，或允许伪造来源。IP 封禁与账号锁定只是登录防护，不表示已实现恶意软件检测或流量攻击防御。参见 [Spring Boot 代理说明](https://docs.spring.io/spring-boot/3.5/how-to/webserver.html)及 [OWASP 认证防护](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)。

**应急恢复：** 优先由另一未封禁地址的超级管理员在页面解除。所有管理员共用被封禁出口时，由有数据库权限的运维备份目标记录，核实 `source_ip`、`id` 与 `version` 后，在维护事务内定向更新该行 `status='RELEASED'`、`unblocked_at=当前北京时间`、`unblocked_by=NULL`、`version=version+1`，更新条件必须同时匹配原 id、version、BLOCKED，并确认仅一行。保留工单及前后记录；直接数据库恢复不会自动写应用审计。不删除整表、不重置其他 IP、不将关闭防护作为常规解封操作。Redis 旧计数无需清除，新版本已隔离旧世代。

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
| API_DOCS_ENABLED | dev/local 为 true，prod 为 false | 同时控制 /v3/api-docs 与 Swagger UI |
| SF_AUTH_IDLE_TIMEOUT / SF_AUTH_ABSOLUTE_TIMEOUT | 30m / 8h | 会话空闲与绝对超时；绝对时长不得小于空闲超时且不超过 1 天 |
| SF_AUTH_SECURE_COOKIE | dev/local 为 false，prod 为 true | 是否仅通过 HTTPS 发送会话 Cookie |

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

Java 输出带 +08:00 的北京时间文本日志；Python 的既有 UTC JSON 日志和跨服务协议暂不改动，共用字段语义：
时间、level、service、traceId；请求结束记录 method、路由模板、status、durationMs。
公共 ApiError/协议中的 Instant 仍按 UTC/Z 序列化以保持兼容；数据库本地日期时间转成 Instant 时必须显式指定 Asia/Shanghai。
前端当前状态页的检查时间固定按 Asia/Shanghai 展示，不依赖浏览器所在时区。
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
| 仓库根目录 | `./scripts/export-sql.ps1 -Check` |

默认后端测试使用 H2 与模拟会话，真实基础设施用例默认跳过。以下命令在 `platform-api` 目录执行，并先确认目标和配置：

```powershell
# 本机 MySQL 结构检查 + MySQL/Redis 连通性检查，仅执行只读查询和 PING
.\mvnw.cmd "-Dtest=LocalSchemaInspectionTest,LocalInfrastructureTest" "-Dsf.test.localSchema=true" "-Dsf.test.localInfrastructure=true" test

# 真实 HTTP + Redis 会话：使用测试 H2；需要专用本机 Redis 127.0.0.1:16379
.\mvnw.cmd "-Dtest=RedisSessionIntegrationTest" "-Dsf.test.redisSession=true" test
```

Redis HTTP 用例会在测试命名空间写入和删除会话，不能指向生产 Redis；它不连接业务 MySQL。MySQL 结构检查不会迁移数据库，旧结构需要先按独立增量方案升级。测试数量和运行结果以本次 Maven 报告为准，不把跳过项当作通过，也不把 H2/连通性通过当作真实数据库并发或完整业务验收。

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
