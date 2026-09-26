# 管理前端开发约定

本文说明当前代码的页面接入、权限和目录约定，供新增功能与交接使用。功能实现和测试结果不代表负责人已验收；生产部署链路仍需在目标环境验证。

## 新增一个业务页面

以相机管理为例，依次完成页面、接口和菜单配置：

1. 新建 `src/views/camera/manage.vue`，编写相机管理页面。
2. 在 `src/api/camera/` 内编写该领域的接口函数与响应类型，统一使用 `src/api/client.ts` 导出的 `request`。
3. 后端实际控制器或接口声明 `@ModuleAccess("camera")`。控制器需要由 Spring 注册为 MVC 接口。
4. 在菜单管理中新增 PAGE，页面路径填写 `/camera/manage`，唯一键填写 `camera`，选择所属目录。
5. 超级管理员在创建 PAGE 的同一事务中自动关联；普通角色在角色管理中分配，使用相应用户重新进入页面验证。

上述基本配置对应：

| 配置项         | 示例值                        | 用途                          |
| -------------- | ----------------------------- | ----------------------------- |
| 前端文件       | `src/views/camera/manage.vue` | 实际页面                      |
| `path`         | `/camera/manage`              | 浏览器路由地址                |
| `routeName`    | `camera`                      | 全局唯一的页面键              |
| `componentKey` | 缺省时为 `/camera/manage`     | 定位 `views` 下的页面文件     |
| `moduleKey`    | 缺省时为 `camera`             | 对应后端 `@ModuleAccess` 的值 |

页面由 `src/router/dynamic/views.ts` 中的 `import.meta.glob` 自动收集，不需要逐页添加 import 或注册表。扫描范围是 `src/views/<模块>/` 下的 Vue 文件；登录、首页等固定页面统一放在 `src/pages/`，不会进入业务页面集合。

自动发现发生在开发与构建阶段。生产包中必须已经包含该 Vue 文件；仅在菜单管理中填写一个新路径不会生成页面。增加页面后需要重新构建并发布前端，新增后端控制器也需要发布对应后端版本。

## 菜单表单与路径规则

菜单编辑仅区分目录和页面：目录可创建子目录或页面，页面为末级。页面填写路径与唯一标识，无高级配置、组件路径或权限键输入框。新建时组件直接采用页面路径，后端模块默认采用唯一标识，因此新建页面的唯一标识须与实际接口的 `@ModuleAccess` 一致。

页面路径相对 `src/views`，以 `/` 开头，不包含 `.vue`，并严格匹配文件大小写。例如 `src/views/system/Department.vue` 对应 `/system/Department`。当前前端未包含文件时，表单在页面路径处提示，不能保存无法加载的新页面。

| 字段           | 当前限制                                                                   |
| -------------- | -------------------------------------------------------------------------- |
| `routeName`    | 最长 64 字符；字母开头，后续可用字母、数字、下划线、冒号和连字符；全局唯一 |
| `path`         | 最长 200 字符；路径段使用字母、数字、下划线和连字符；全局唯一              |
| `componentKey` | 最长 64 字符，包含开头的 `/`；不能带 `.vue`                                |
| `moduleKey`    | 最长 64 字符；必须对应已注册接口的 `@ModuleAccess` 值；PAGE 创建后不能更换 |

新建页面的路径同时用于定位组件，最长 64 字符。表格中的 `componentKey`、`moduleKey` 是后端协议字段，由前端自动提交或由后端补齐，管理界面无需重复填写。

浏览器路由地址与组件文件路径可以不同，例如 `/system/departments` 使用 `/system/Department` 组件。只修改名称、图标、排序时保留组件路径；主动修改页面路径时，组件随新路径更新。页面的后端模块归属保持不变，更改唯一标识不会迁移接口权限。表单转换逻辑在 `src/features/menu/form.ts`。

页面键负责区分路由，模块键负责接口授权。多个 PAGE 可以共享同一模块；持有该模块任一有效 PAGE 可访问该模块接口，需要不同接口授权边界时由后端声明不同模块。

组件键统一使用以 `/` 开头的实际文件路径，解析器不维护别名。系统管理页面位于 `/system/User`、`/system/Role`、`/system/Menu`、`/system/Department`；监控页面位于 `/monitor/Audit`、`/monitor/Server`、`/monitor/ApiDocs`。

目录节点只组织菜单，不配置页面字段；目录可继续包含目录，PAGE 是末级节点，当前最多五层。角色授权树勾选目录会选中可分配的后代页面，取消目录会取消后代，部分选择显示半选；保存到后端的是 PAGE ID。隐藏页面或隐藏父目录只影响导航展示；有效 PAGE 的路由仍可直接访问。禁用状态和角色授权由后端决定。组件不存在时，已授权路由显示配置错误页面，入口见 `src/pages/error/RouteUnavailableView.vue`。

路径不能包含协议、点段、百分号编码、反斜杠、查询参数、锚点或重复分隔符。固定路由和基础设施路径受保留规则保护，规则入口为 `src/router/dynamic/routes.ts` 与后端 `MenuRules`。不要仅通过大小写区分两个页面键或路由地址。

## 权限与会话

后端 `ModuleRegistry` 从实际 MVC 接口发现 `@ModuleAccess`，不维护业务模块名称枚举。菜单保存时检查模块是否存在；请求到达后，由 `ModuleAuthorizationInterceptor` 按实际控制器方法检查当前用户的有效角色、PAGE 和启用祖先。前端隐藏按钮或菜单不代替接口鉴权。

`/auth/me` 返回当前身份、模块集合和菜单树，模块必须由当前运行后端实际注册。未授权、停用、祖先停用或对应后端模块未部署的 PAGE 不会进入授权导航，过滤后没有可用页面的目录也不显示；菜单管理的配置树仍保留这些配置供维护。前端在 `src/router/dynamic/routes.ts` 生成路由与导航，未授权模块不会注册。页面键或路径冲突时不会重复注册。

业务请求返回 `403/FORBIDDEN` 时，公共客户端合并同批拒绝，刷新一次身份与导航。当前页面确已失权时重新进入路由守卫；页面仍有权限的对象操作错误保留在原页，不自动重试业务请求。实现位于 `src/session/authorizationSync.ts`，认证接口和 CSRF 错误使用各自的处理链。

登录、首页、个人信息、改密和错误页使用固定路由，不需要后台 PAGE 配置。首页、个人信息和改密等受保护固定页面仍检查登录状态；会话过期后清理身份数据并进入登录页。登录页与服务不可用页作为公共或恢复入口，避免鉴权失败时循环跳转。

当前会话采用空闲 30 分钟、最长 8 小时的后端规则；同一账号的新登录会结束旧会话。前端每 15 秒检查会话状态，页面恢复可见时立即检查；状态检查不续期。旧登录收到失效原因后进入登录页并提示新登录的时间、IP、地区和浏览器环境。用户管理的强制登出同样触发会话失效提示。同源标签共享 Cookie，登录、退出或改密会通知其他标签重新恢复身份。相关实现位于 `src/session/` 和 `components/feedback/SessionEndedDialog.vue`。

浏览器缓存的菜单不是授权依据，身份和路由在加载或导航时通过后端刷新。强制改密状态在身份刷新后判断。

### 登录与记住密码

登录先获取一次性挑战，再用 Web Crypto 的 ECDH / HKDF / AES-GCM 加密账号密码后提交。生产环境仍须 HTTPS，以保护页面、公钥交换和会话 Cookie。

“记住账号和密码”默认关闭，仅在认证成功后保存，最长保留 30 天。账号和密码一起加密存入当前浏览器的 IndexedDB，密钥为不可导出的 CryptoKey；不写明文 localStorage。取消勾选会删除保存内容，修改密码后也会清除。临时密码要求首次改密时不保存；浏览器禁用存储时仍可正常登录。

此方式用于本机自动填充，同源脚本仍能使用保存的密钥解密，因此不提供 XSS 防护或操作系统级凭据保险箱能力。共享设备应保持不勾选，也可通过浏览器清除该站点数据删除保存内容。代码位于 `src/session/rememberedLogin.ts`；登录输入校验在 `src/utils/loginValidation.ts`，后端仍执行最终校验。

### 用户管理

新建和编辑用户的 `departmentIds` 可为空，最多 20 个；新建用户不会自动归属部门。管理端新建和密码重置成功响应携带一次性展示字段 `temporaryPassword`，界面在结果弹窗展示，关闭后清空前端结果。普通查询和个人信息不返回密码。开发环境默认临时密码与配置方法见根目录 README。

批量重置逐个调用现有重置接口，分别展示成功或失败，不承诺跨账号事务。启用开关关闭账号后，后端阻止新登录并使旧会话失效。本人、超级管理员和内置角色的保护继续由后端执行。

## 图标、表格与系统信息

侧栏与菜单选择器使用 `@element-plus/icons-vue`，图标键和中文名称统一维护在 `src/components/icons/catalog.ts`，后端保存稳定的图标键。未知图标键使用兜底图标。品牌与 favicon 共用 `public/streamfusion.svg`。

管理表格使用 Element Plus 的带边框表格，拖动表头边缘可调整列宽，列显隐由公共 ColumnPicker 管理；列宽在当前页面生效。业务界面的正文不再重复显示侧栏和顶部已有的标题。

操作记录和服务信息位于监控面板，页面分别为 `src/views/monitor/Audit.vue`、`src/views/monitor/Server.vue`，浏览器地址与组件路径统一为 `/monitor/Audit`、`/monitor/Server`。接口集中在 `src/api/audit/`、`src/api/server/`，使用 `/audit/page`、`/audit/{id}` 和 `/server/status`，分别需要 `audit`、`server` 模块授权。菜单初始化 SQL 包含监控目录及 PAGE；现有数据库的菜单更新使用 `platform-api/sql/升级/` 定向脚本，不能重跑初始化建表脚本。

接口文档页面为 `src/views/monitor/ApiDocs.vue`，嵌入同源固定的 `public/api-docs.html`，可刷新和导出 OpenAPI JSON。文档入口检查 `api-docs` 模块授权；嵌入页只从当前站点读取 `/v3/api-docs`，不接受外部 URL。导出接口位于 `src/api/api-docs/`。页面错误统一使用 `PageState` 与 `RequestError`，先展示原因和恢复操作，再按需展开错误码、时间及请求标识。

服务信息只读取当前后端运行环境、JVM、NVIDIA GPU 和项目配置的服务端口，不扫描全机所有服务。使用有界单采集线程、请求合并、默认 30 秒缓存和采集超时；页面只在进入和手动刷新时请求，不轮询。端口可达只表示 TCP 连接成功，页面不会将其显示为服务业务健康；未能读取的指标显示不可用。

采集实现独立维护在后端 `server/adapter/`：`HostMetricsAdapter` 定义环境采集入口，`JdkHostMetricsAdapter` 复用 JDK 的 Windows/Linux 实现；JVM、NVIDIA 命令及端口/DNS 探测分别管理。环境 CPU 在预读后间隔 1 秒采样，进程 CPU 按同类窗口的 CPU 时间差与 JDK 可用逻辑处理器数量归一化；两者均返回 0～100 或 null。容器中采用 JDK 所见资源范围，不能直接当作宿主机物理资源。30 秒缓存值也不等于另一个工具此刻的瞬时读数。适配器支持不等于已完成所有发行版实机验收，部署环境须具备兼容的 Java 21；GPU 还依赖相应 NVIDIA 驱动及工具。

后端配置前缀为 `platform.server-monitor`：`cache-ttl` 默认 `30s`，`probe-timeout` 默认 `500ms`，`collection-timeout` 默认 `6s`，`gpu-enabled` 默认 `true`；`agent-host/agent-port` 与 `runtime-host/runtime-port` 按部署填写。项目服务包含 `Platform Web`，`web-host/web-port` 默认 `127.0.0.1:8090`，对应本地开发端口；生产部署应配置后端可访问的实际 Web 监听地址，或将 `web-port` 设为 `0` 禁用。以上端口探测均支持用端口 `0` 或空主机关闭，并显示“未配置”。Web 探测只建立 TCP 连接，与其他服务共享缓存及采集时限，不启动前端进程、不请求登录接口，也不验证页面或业务健康。GPU 读取使用固定 `nvidia-smi` 查询，超时或无驱动时降级，不创建推理任务或重启服务。

## 通用代码与业务代码

| 目录                       | 职责                                                                                                 |
| -------------------------- | ---------------------------------------------------------------------------------------------------- |
| `src/pages/auth/`          | 固定登录页与改密页，URL 仍为 `/login`、`/change-password`                                            |
| `src/pages/home/`          | 固定首页，URL 为 `/home`                                                                             |
| `src/pages/account/`       | 固定个人信息页，URL 为 `/profile`                                                                    |
| `src/pages/error/`         | 无权访问、服务不可用与菜单组件缺失的完整错误页面                                                     |
| `src/views/<模块>/`        | 业务页面；例如 `system/User.vue`、`system/Department.vue`                                            |
| `src/api/<领域>/`          | 该领域的接口函数、请求参数和响应解析；现有 `auth`、`user`、`roles`、`departments`、`menus`、`health` |
| `src/api/client.ts`        | 将身份与 CSRF 处理接入通用 HTTP 客户端                                                               |
| `src/lib/http/`            | 传输、路径校验、统一响应、错误、超时与取消处理                                                       |
| `src/components/table/`    | 公共表格区域、列设置等组件                                                                           |
| `src/components/feedback/` | 公共请求错误反馈                                                                                     |
| `src/components/icons/`    | 公共图标                                                                                             |
| `src/composables/table/`   | 列显隐、行选择等可复用状态逻辑                                                                       |
| `src/features/<功能>/`     | 从页面提取的业务逻辑；例如菜单表单转换 `menu/form.ts`                                                |
| `src/layouts/`             | 管理端布局和导航组件                                                                                 |
| `src/router/dynamic/`      | 页面发现、菜单转换和路径规则                                                                         |
| `src/session/`             | 身份恢复、登录、退出和会话状态                                                                       |
| `src/utils/`               | 通用工具，例如树处理                                                                                 |

固定页面通过 `src/router/fixed.ts` 显式注册；菜单组件缺失页由动态路由生成器引用。`pages` 和 `views` 都保存完整页面，公共组件放在 `components`，复用状态放在 `composables`，业务转换放在 `features`。不要在 `views` 中放固定页、错误页、公共组件或测试文件，以免被当作业务页面发现。跨页面交互测试位于 `tests/views/`；其他单元测试与对应模块同目录。

表格行操作统一使用 `components/table/TableActions.vue`，按钮采用 `plain`，由组件维护字号、点击尺寸和间距。操作列设置足够的 `min-width`、`class-name="table-actions-column"` 和 `:resizable="false"`，避免拖窄后按钮遮挡；数据列仍可调整宽度。目录/页面等不同行类型应保持主要操作的位置稳定，尾部危险操作可使用 `table-action-end`。宽度不超过1024px时解除右固定列，使用表格内部横向滚动；触摸设备增大按钮高度。图标选择器在格子内显示名称，不叠加悬停提示。

新增固定页面时，在 `src/pages` 的相应目录创建页面，并仅在 `src/router/fixed.ts` 的 `fixedRoutes` 增加 URL、名称和懒加载函数；路由注册和 `fixedPaths` 自动使用这份配置，守卫与前端菜单保留路径无需维护第二份列表。后端 `MenuRules` 的保留路径仍须按公共协议核对。将固定页移动目录不改变其 URL、登录校验或菜单权限规则。

新增领域接口使用已有 HTTP 客户端，保留 CSRF、版本字段或 `If-Match`、响应解析和身份代次保护，不直接另写 `fetch`。列表查询字段应有实际后端接口支持；行选择、查询条件和列显隐分别承担不同交互。

## 本地开发与生产请求路径

### 用户与个人信息组件

用户新建/编辑弹窗采用两列表单，窄屏降为单列。角色分配抽离到 `features/user/RoleAssignmentDialog.vue`：按名称/编码搜索、表格勾选、显示选中数量，搜索隐藏的选择会保留；缺少当前可分配信息但已绑定的角色仍保留，当前用户自身超级身份不允许在此移除。角色接口不返回启用状态时，界面不推测状态。

个人信息固定页 `pages/account/ProfileView.vue` 左侧显示身份，右侧在基本资料/修改密码之间切换。`components/avatar/` 统一头像目录及渲染，`features/account/AvatarPicker.vue` 提供已有六个头像键和姓名头像，随资料保存到后端，不上传文件。`features/account/PasswordForm.vue` 供个人中心及强制改密页面复用；规则由 `api/auth/passwordPolicy.ts` 读取，校验逻辑位于 `features/account/password.ts`。强制改密账号继续使用独立 `/change-password`，普通账号从 `/profile?tab=password` 进入。

操作记录中的 IP 封禁页签使用 `features/security/IpBlockPanel.vue`，接口与解析放在 `api/security/`。`GET /audit/ip-blocks/page` 支持分页、精确 IP 及状态查询；`PUT /audit/ip-blocks/{id}/unblock` 不带请求体，携带最新 `If-Match` 与 CSRF。只有超级管理员显示此页签，后端同时检查身份与 `audit` 模块。身份变化、弹窗取消和组件销毁均不能继续旧解除请求。

收到 `IP_BLOCKED` 时统一清除本地身份并返回登录页，显示需管理员解除的提示；它不触发普通 PAGE 失权刷新。`429` 展示稍后重试及服务端提供的时间。完整配置和解除流程见[后端开发指南](../DEVELOPMENT.md#ip-登录防护)。

### 请求代理

在本目录执行：

```sh
pnpm install --frozen-lockfile
pnpm dev
```

工具版本以 `package.json` 的 `engines` 和 `packageManager` 为准。开发服务默认使用 `127.0.0.1:8090`，后端代理目标默认 `http://127.0.0.1:8080`，可通过 Vite 环境配置 `API_TARGET` 调整。

开发环境的 HTTP 传输层自动添加 `/api`，Vite 去掉该前缀后转发到后端。API 函数中始终填写后端原始路径，不手工添加 `/api`。

| 调用                                     | 开发浏览器请求              | 后端接收 / 生产浏览器请求 |
| ---------------------------------------- | --------------------------- | ------------------------- |
| `request({ path: '/camera/page', ... })` | `/api/camera/page`          | `/camera/page`            |
| 登录挑战                                 | `/api/auth/login/challenge` | `/auth/login/challenge`   |
| 健康检查                                 | `/api/actuator/health`      | `/actuator/health`        |

这使开发环境的 `/camera/manage` 页面导航与 API 代理互不干扰。生产构建保留现有后端根路径 API 协议；Vite 的开发代理不会包含在构建产物中。

部署方需要同时配置 SPA history fallback 和后端 API 转发，并区分页面 URL 与实际 API URL。例如页面 `/camera/manage` 与接口 `/camera/page` 应分别落到前端与后端，不要把整个 `/camera/**` 无差别转发。不要给一个 GET URL 同时安排页面 HTML 和业务 JSON。生产部署行为尚未在本轮目标环境验证。

## 检查与排错

在 `platform-web` 目录执行：

```sh
pnpm lint
pnpm format:check
pnpm typecheck
pnpm test
pnpm build
```

针对页面发现、路由和会话守卫：

```sh
pnpm exec vitest run src/router/dynamic/views.test.ts src/router/dynamic/routes.test.ts src/router/guard.test.ts
```

针对 HTTP、代理路径与健康响应：

```sh
pnpm exec vitest run src/lib/http/client.test.ts src/lib/http/transport.test.ts src/api/health/api.test.ts
```

| 现象                        | 检查入口                                                                                                                               |
| --------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- |
| 菜单没有出现                | 浏览器 `/auth/me` 响应（开发为 `/api/auth/me`）中的 routes/modules；角色 PAGE 授权、启用祖先和 visible；`src/router/dynamic/routes.ts` |
| 提示页面配置不可用          | `componentKey` 对应文件是否存在、大小写是否一致、是否带了 `.vue`、当前部署是否包含该文件；`src/router/dynamic/views.ts`                |
| 菜单保存提示 moduleKey 无效 | 后端控制器是否已启动并声明匹配的 `@ModuleAccess`；`ModuleRegistry`、`MenuRules`                                                        |
| 401 或自动回登录页          | 会话 Cookie、会话有效期、后端用户状态；`src/session/`、`src/api/client.ts`                                                             |
| 403 或 CSRF_INVALID         | 实际模块授权、写请求 CSRF；`ModuleAuthorizationInterceptor`、`src/lib/http/client.ts`                                                  |
| 新菜单出现但接口报 403      | 核对实际监听进程的启动时间及 `/v3/api-docs` 是否包含新接口；新增后端控制器后需重新编译并重启后端，数据库菜单变更不会热加载 Java 类     |
| 409 或版本冲突              | 当前记录是否被其他用户修改，重新加载最新 version；对应领域 `src/api/`                                                                  |
| 开发 API 返回 HTML 或 404   | 请求是否经过 `/api`、Vite `API_TARGET`、后端实际接口路径；`vite.config.ts`、`src/lib/http/transport.ts`                                |
| 页面刷新出现服务器 404      | 部署的 SPA fallback 与 API 转发规则                                                                                                    |

排查请求失败时保留错误码、时间和 `traceId`，用其关联后端日志。不要将密码、会话 Cookie、完整凭据写入日志或文档。
