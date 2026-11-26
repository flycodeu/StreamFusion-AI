# 用 Ops 构建和发布项目

在「项目管理」打开 StreamFusion 项目，进入「交付与运行」。在一个环境里分别为 `platform-api` 和 `platform-web` 选择运行主机，算法模块可以按主机逐条登记。Jenkins 负责构建和归档，Ops 负责核对制品、发布、检查和下线。预览节点使用独立的[预览节点部署流程](preview-nodes.md)。

## 准备 Jenkins 和运行服务器

1. 在项目详情点「设置 Jenkins」，选已登记的 Jenkins 实例，填写 Jenkins 用户名、个人 API Token 和构建节点标签。API Token 在 Jenkins 的「用户 → 安全 → API Token」生成。点「测试连接」，核对实际认证账号和在线节点，再保存。标签默认 `sfops-build`；至少一个匹配的 Linux Agent 须在线且有空闲执行器才能开始构建。Jenkins 需要 Pipeline、Git 插件；Ops 账号需要 `Overall/Read`、`Job/Create`、`Job/Read`、`Job/Build`，以及读取作业配置的 `Job/ExtendedRead` 或 `Job/Configure`。
2. 在「模块与配置」核对仓库、分支、模块目录和类型。构建 Agent 需要 Git 和访问仓库的权限；私有仓库填写 Jenkins 已保存的仓库凭据 ID。项目连接的 API Token 用于 Ops 调用 Jenkins，仓库凭据用于 Agent 拉代码。
3. 在「主机管理」接入运行服务器，检查 SSH、受管执行器和变更权限。新版现场检查、恢复和下线要求目标机项目发布能力为版本 3；在「安装准备」更新接入组件后再操作已有实例。

| 模块 | Jenkins Agent | 运行服务器 |
| --- | --- | --- |
| Java 后端 | JDK 21，仓库的 Maven Wrapper 或系统 Maven。归档唯一的可执行 JAR 为 `app.jar`。 | systemd、`/usr/bin/java` 指向 Java 21；按环境准备数据库、Redis 等应用依赖。 |
| 前端 | Node.js 和锁文件对应的 pnpm 或 npm。构建 `dist/index.html`，归档为 `web-dist.tar.gz`。 | 已运行的 `nginx.service`，Nginx 主配置加载 `/etc/nginx/conf.d/*.conf`；无需 Node.js。 |
| `node-agent`、`runtime` | uv 和各模块的 `uv.lock`，运行 pytest 并归档源码和报告。 | 在交付清单中登记各台主机和已有运行服务。当前模块的 Jenkins 作业用于 QA；Ops 尚未提供算法制品的一键发布。 |

## 保存环境草稿

在「交付与运行」选环境，在模块行点「配置」或「草稿」，选择运行服务器和部署形态。配置只保存在 Ops；发布成功后才成为当前版本。

- **Java JAR（systemd）**：填写监听地址、端口、运行 Profile、内存和健康检查路径。同机 Nginx 访问 Java 时可用 `127.0.0.1`。在「应用配置文件」保存 YAML 或 properties；敏感值通过「凭据中心」引用，例如 `${SFOPS_CRED:凭据名称}`，也可为补充环境变量绑定凭据。Ops 在预检时锁定引用版本，服务启动时才使用实际凭据。关联的依赖软件用于检查目标机能否连上对应端口。
- **Nginx 静态站点**：填写用户实际访问的域名或 IP 地址及 Nginx 监听端口。StreamFusion 使用 SPA 回退和同环境 `/api` 规则；在已有草稿点「应用 StreamFusion 路由」可替换重叠规则。跨主机时，后端要监听 Nginx 主机可达的内网地址，防火墙允许这台 Nginx 主机访问 Java 端口。
- **算法节点**：为算法模块保存第一台主机后，在同环境的「交付与运行」点「添加服务器」，继续登记其他服务器。新登记可选原生进程或 Docker。要关联已运行的算法服务，先在「软件管理」登记相应服务或容器并完成只读身份检查，再回到该算法服务器的记录选择它。关联后可查看最近的运行观察记录及日志。

## 构建、预检和发布

1. 点模块行的「QA」，或在「发布与运行」点「构建」。首次运行会创建模块级 Jenkins 作业；后续查看同一作业的构建号和「QA 日志」。QA 成功不会自动发布。
2. 打开「发布与运行」，填成功的 QA 构建号并点「核对构建」，也可点「读取最近构建」。点「部署预检」，核对目标服务器、环境、构建号、Git 提交、端口和健康检查。Ops 根据 Jenkins 归档的 `sfops-release.txt` 核对提交、文件名、大小和 SHA-256。
3. 点「确认发布」。预检单有效期为 5 分钟；主机、草稿、当前发布版本或制品发生变化时，重新预检。Java 检查实际 JAR 进程和健康接口；StreamFusion 站点还通过目标机 Nginx 检查 API。目标机检查成功后更新「线上版本」。
4. 只改 Nginx 配置时，可在「发布与运行」打开「编辑站点配置」。保存草稿后点「用当前构建预检」，再确认发布。Jenkins 仍保留当前版本的归档时，可以沿用该构建。目标生成 `/etc/nginx/conf.d/sfops-project-<部署ID>.conf`，通过 `nginx -t` 后重载。

## StreamFusion 整站发布

1. **先发布后端。** `platform-api` 的目标健康检查填 `/actuator/health`，按环境配置数据库、Redis 和凭据。Ops 同时检查对应的 Java 进程和 Actuator JSON `UP`。单实例发布会重启 Java，应安排窗口并在发布后确认预览能重新打开。
2. **再发布前端。** 开启 SPA 回退，添加 `/api` 反代并去前缀，目标选同环境的 `platform-api`。Ops 从已发布版本解析地址，后端草稿里的新端口不会生效。预检从站点服务器访问后端 `/actuator/health`；切换站点后再通过 Nginx 请求 `/api/actuator/health`，只接受返回 JSON `UP` 的结果。Vue 页面走静态站点；生成的配置只允许同源页面嵌入，保留站内接口文档。
3. **验收实际 HTTPS 入口。** Ops 生成 HTTP 站点，由现有网关终止 TLS 并转发站点，保留 `Host` 和 Platform 安全 Cookie 设置。入口网关覆盖外部传入的 `X-Forwarded-For`；在 Java 的 `SF_TRUSTED_PROXIES` 中只配置实际受信网关和站点 Nginx 的 IP，确认登录审计显示客户端地址。使用对象上传时，外层网关也须允许 21 MiB 的请求体。访问真实域名的 `/api/actuator/health` 核对 JSON `UP`，使用测试账号登录，刷新 `/preview/center`，打开已授权视频确认首帧。浏览器应能访问预览节点公布的媒体地址和端口。

已有自建网关先加 `/api/` 转发，再用包含本次改动的前端源码重新构建。旧前端归档缺少新版 `/api` 标记，不能用来发布新版站点；旧 Jenkins 作业和记录会保留。发布面板显示站点名称和 Nginx 监听端口，实际入口以 TLS 网关配置为准。

## 日志、检查、下线和回退

| 需要定位的问题 | 入口 |
| --- | --- |
| 拉代码、编译、测试或归档失败 | 「QA 日志」或对应构建的 Jenkins 控制台，按构建号定位。 |
| 预检未通过 | 先看预检错误；按提示核对 Jenkins 制品、目标工具和依赖。预检通过并确认发布后才生成作业。 |
| 作业下载制品或在目标执行失败 | 「最近作业 → 执行日志」，记录阶段、错误码和请求标识。Java 首次启动失败时也可在这里查看已脱敏的启动摘要。 |
| 当前版本的应用状态 | 「读取发布记录」读取控制端记录。「立即检查」在目标机做只读检查，显示时间；五分钟后的历史健康结果显示过期。 |
| 运行输出 | 「服务日志」查看当前 Java 的 systemd 日志或该站点的 Nginx 日志。下线后 Ops 仍可查看作业日志；历史运行日志在原服务器查看。 |
| 作业结果待核对 | 「立即检查」看现场，「原作业日志」看过程。「恢复未完成操作」需要另行确认，可能恢复旧配置、重启 Java 或重载 Nginx。 |

需要回退时，在「发布与运行」点「回退上一版」，核对构建号并确认。目标复用上一版本及其发布时配置，无需 Jenkins 重新构建。StreamFusion 站点的上一版必须支持 `/api` 检查且指向当前同环境后端；首次从旧方案升级后，先形成一个符合新规则的上一版，再从 Ops 回退。

变更部署主机前，点「下线实例」，核对主机和构建号并确认。下线只撤除 Ops 管理的 systemd 单元或 Nginx 站点配置，保留业务数据和作业历史。当前端仍使用该 Java 地址时，先下线前端或切换其反代。目标确认下线后再编辑绑定，按「后端 → 前端」的顺序发布到新主机。

发布或回退成功后，目标机分批清理超出当前版、上一版的旧受管制品。出现 `PROJECT_RELEASE_RETENTION_PENDING` 时，从作业日志核对目标机还有哪些目录待处理。

## 维护入口

| 修改场景 | 代码入口 | 归属 |
| --- | --- | --- |
| 识别仓库和模块 | `internal/controller/application_project_inspect.go` → `internal/projectinspect` | 控制器处理来源、授权和 Git；识别包读取受限源码目录并生成建议。 |
| 仓库识别弹窗 | `web/src/features/project-delivery/useProjectInspection.ts` | 读取来源、取消扫描、确认建议和选择登记仓库。 |
| 编辑或识别应用配置 | `web/src/features/project-delivery/environmentPatch.ts`；`internal/projectconfig` | 前端区分保留原值与显式清除；后端共享配置文件元数据与基础校验。 |
| 识别 StreamFusion 前后端 | `internal/controller/application_project_streamfusion.go` | 项目域定义同一仓库内的模块配对；Jenkins 作业与站点发布共用。 |
| 查询线上版本和待处理作业 | `web/src/features/project-delivery/deploymentStatusReader.ts` → `internal/controller/project_deployment.go` | 页面按项目与登记共享状态；控制端读取发布记录，目标端的现场检查另行执行。 |
| 预检、执行、确认结果 | `internal/controller/project_deployment.go` → `project_deployment_jobs.go` → `internal/deploy/project_deployment.go` | 控制端冻结计划、核对制品和提交作业结果；`project_deployment_transition.go` 集中判断结果是否允许更新线上版本。目标端执行 systemd/Nginx、检查和恢复。 |
| Java 和站点的发布依赖 | `internal/controller/project_deployment_dependencies.go` | 按当前发布与待执行作业保护站点依赖的 Java 地址和端口。 |

在 `operations` 运行 `pnpm --dir web test` 和 `go test -timeout 15m ./...`。重点覆盖同环境前后端分主机、同模块多算法登记、草稿与线上版本隔离、目标断联后的检查与恢复，以及上版回退和下线。
