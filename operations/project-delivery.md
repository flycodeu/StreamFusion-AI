# 用 Ops 构建和发布项目

在「项目管理」打开项目，进入「交付与运行」。在这里按模块和环境查看 QA、发布记录及运行状态。Jenkins 构建并归档制品，Ops 核对制品和部署配置，再由运行服务器上的受管执行器发布。当前这条路径支持 Java JAR（systemd）和 Nginx 静态站点。Python 模块可以做 QA，预览节点使用[预览节点部署流程](preview-nodes.md)。

## 准备 Jenkins 和运行服务器

1. 在项目详情点「设置 Jenkins」，选已登记的 Jenkins 实例，填写 Jenkins 用户名、个人 API Token 和构建节点标签。API Token 在 Jenkins 的「用户 → 安全 → API Token」生成。点「测试连接」，核对实际认证账号和在线节点，再保存。标签默认 `sfops-build`；至少一个匹配的 Linux Agent 须在线且有空闲执行器才能开始构建。Jenkins 需要 Pipeline、Git 插件；Ops 账号需要 `Overall/Read`、`Job/Create`、`Job/Read`、`Job/Build`，以及读取作业配置的 `Job/ExtendedRead` 或 `Job/Configure`。
2. 在「模块与配置」核对仓库、分支、模块目录和类型。构建 Agent 需要 Git 和访问仓库的权限；私有仓库填写 Jenkins 已保存的仓库凭据 ID。项目连接的 API Token 用于 Ops 调用 Jenkins，仓库凭据用于 Agent 拉代码。
3. 在「主机管理」接入运行服务器，检查 SSH、受管执行器和变更权限。运行服务器需要版本 2 的项目发布能力；页面提示能力版本过低时，先在「安装准备」更新接入组件。

| 模块 | Jenkins Agent | 运行服务器 |
| --- | --- | --- |
| Java 后端 | JDK 21，仓库的 Maven Wrapper 或系统 Maven。归档唯一的可执行 JAR 为 `app.jar`。 | systemd、`/usr/bin/java` 指向 Java 21；按环境准备数据库、Redis 等应用依赖。 |
| 前端 | Node.js 和锁文件对应的 pnpm 或 npm。构建 `dist/index.html`，归档为 `web-dist.tar.gz`。 | 已运行的 `nginx.service`，Nginx 主配置加载 `/etc/nginx/conf.d/*.conf`；无需 Node.js。 |
| Python | uv 和项目的 `uv.lock`，运行 pytest 并归档源码和报告。 | 本流程只执行 QA。 |

## 保存环境草稿

在「交付与运行」选环境，在模块行点「配置」或「草稿」，选择运行服务器和部署形态。配置只保存在 Ops；发布成功后才成为当前版本。

- **Java JAR（systemd）**：填写监听地址、端口、运行 Profile、内存和健康检查路径。同机 Nginx 访问 Java 时可用 `127.0.0.1`。在「应用配置文件」保存 YAML 或 properties；敏感值通过「凭据中心」引用，例如 `${SFOPS_CRED:凭据名称}`，也可为补充环境变量绑定凭据。Ops 在预检时锁定引用版本，服务启动时才使用实际凭据。关联的依赖软件用于检查目标机能否连上对应端口。
- **Nginx 静态站点**：填写域名、监听端口，按需开启「SPA 单页路由回退」。反向代理规则可关联同项目、同环境的后端模块，或填写上游 URL。跨主机时，后端要监听 Nginx 主机可达的网卡地址，并开放所需端口。

## 构建、预检和发布

1. 点模块行的「QA」，或在「发布与运行」点「构建」。首次运行会创建模块级 Jenkins 作业；后续查看同一作业的构建号和「QA 日志」。QA 成功不会自动发布。
2. 打开「发布与运行」，填成功的 QA 构建号并点「核对构建」，也可点「读取最近构建」。点「部署预检」，核对目标服务器、环境、构建号、Git 提交、端口和健康检查。Ops 根据 Jenkins 归档的 `sfops-release.txt` 核对提交、文件名、大小和 SHA-256。
3. 点「确认发布」。预检单有效期为 5 分钟；主机、草稿、当前发布版本或制品发生变化时，重新预检。目标健康检查通过后，Ops 更新「当前版本」。Java 检查实际 JAR 进程和 HTTP 健康接口；Nginx 检查首页及站点版本标记。
4. 只改 Nginx 配置时，可在「发布与运行」打开「编辑站点配置」。保存草稿后点「用当前构建预检」，再确认发布。Jenkins 仍保留当前版本的归档时，可以沿用该构建。目标生成 `/etc/nginx/conf.d/sfops-project-<部署ID>.conf`，通过 `nginx -t` 后重载。

## StreamFusion 站点路由

`platform-api` 的目标健康检查可填 `/actuator/health`，测试环境使用自己的数据库、Redis 和凭据。`platform-web` 当前的生产构建直接请求 `/auth`、`/cameras`、`/preview` 和 `/storage` 等路径。仅添加 `/api` 代理无法覆盖这些请求；把 `/preview` 或 `/storage` 整段交给后端也会拦截同前缀的 Vue 页面刷新。

首次部署 StreamFusion 整站前，需要确定网关对页面和 API 的路由规则。若前端后续统一使用 `/api`，可在 Ops 打开 SPA 回退，添加 `/api` 反代并开启「去前缀」，将请求转到同环境的 `platform-api`，Java 路由保持原样。浏览器还须能访问预览节点公布的媒体地址及端口。

Ops 生成的站点只监听 HTTP。正式登录站点应由现有网关或独立 Nginx 配置终止 TLS，保留 Platform 默认的安全 Cookie 设置。发布后从实际访问域名核对健康接口返回 JSON 和 `UP`，再验证登录、页面刷新和预览首帧。

## 日志、核对和回退

| 需要定位的问题 | 入口 |
| --- | --- |
| 拉代码、编译、测试或归档失败 | 「QA 日志」或对应构建的 Jenkins 控制台，按构建号定位。 |
| 预检未通过 | 先看预检错误；按提示核对 Jenkins 制品、目标工具和依赖。预检通过并确认发布后才生成作业。 |
| 作业下载制品或在目标执行失败 | 「最近作业 → 执行日志」，记录阶段、错误码和请求标识。Java 首次启动失败时也可在这里查看已脱敏的启动摘要。 |
| 当前版本的应用状态 | 「检查服务」刷新实际状态；「服务日志」读取当前 Java 的 systemd 日志或该站点的 Nginx 日志。 |
| 作业结果待核对 | 先点「检查服务」核对原作业，再决定是否重发或回退。 |

需要恢复时，在「发布与运行」点「回退上一版」，核对构建号并确认。目标使用已保存的上一成功版本及其发布时配置，回退不需要重新构建或从 Jenkins 下载。数据库和业务数据按各自的备份与恢复流程处理。
