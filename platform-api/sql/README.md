# 数据库 SQL

新安装只执行本目录的 [`streamfusion-mysql.sql`](streamfusion-mysql.sql)：它包含全部30张应用表、索引、外键、约束和初始化种子，无需拼接逐表文件或执行历史升级脚本。应用不自动执行 SQL，无 Flyway；初始化 SQL 随源码交付，不打包进应用 JAR。

`业务/` 的30份逐表文件仅供开发维护，是唯一结构定义来源；`scripts/export-sql.ps1` 生成完整安装文件，禁止手工修改生成结果。

## 当前结构

完整安装文件已包含相机本地名称 `camera_device.local_name` 和备注 `remark`，均可空；名称为空时沿用来源名称。设备、通道和码流保持独立层级。已有数据库的处理见[已有库与菜单配置](#已有库与菜单配置)。

| 数据 | 表 | 实现 |
|---|---|---|
| 用户 | sys_user | PBKDF2 密码哈希、待改密/正常/停用状态、登录冷却、会话失效版本；账号大小写无关唯一 |
| 角色与授权 | sys_role、sys_user_role、sys_role_menu | 用户关联角色，角色关联 PAGE；SUPER_ADMIN 获得全部 PAGE，目录不作为授权关系 |
| 菜单 | sys_menu | 目录与页面树、路由和模块键；根导航BUSINESS在前、SYSTEM及MONITOR固定最后，鉴权仍沿有效页面关系 |
| 部门 | sys_dept、sys_user_dept | 多根组织树、多部门且无主部门；组织归属不产生权限 |
| 审计 | sys_operation_log | 操作者、目标、动作、结果、来源及受控变更快照 |
| 审计维护 | sys_audit_retention_policy、sys_audit_export | 默认关闭的保留策略、受保护的初始化记录、有界导出任务和文件生命周期 |
| IP 防护 | sys_ip_block | 规范化来源地址、封禁与人工解除状态、失败次数、时间及并发版本；无自动解封 |
| 登录记录 | sys_login_record | 成功登录时的账号与来源快照、活动及到期时间、结束原因；不保存 Session ID 或凭据 |
| 相机资产 | camera_device、camera_channel、camera_stream_profile、camera_create_request | 可选设备、稳定通道、多码流、受会话约束的幂等创建回执 |
| 接入配置 | camera_source、camera_source_endpoint、camera_source_credential、camera_profile_locator | 设备/平台/RTSP连接、可空驱动、品牌提示、加密凭据；档案可没有码流与定位 |
| 发现与导入 | camera_access_job、camera_access_import_item | 有界发现/整批导入、短期加密连接、后台分页计数与逐项去重结果 |
| 相机状态 | camera_status_check、camera_status_snapshot | 有界手动检查回执及每 Profile 的 CONTROL / RTSP_DESCRIBE 事实；不代表媒体收流 |
| 按需巡检 | camera_status_plan、camera_status_plan_target | 有限期固定选集、代表流、错峰时间槽；登出继续，执行时复核账号/权限/连接配置 |
| 视频分组与范围 | camera_group、camera_user_scope、camera_user_group_grant、camera_user_channel_grant | 独立分组与账户组/点位授权，不复用部门权限 |

用户、角色、菜单、部门均硬删除，数据库没有软删除字段；用户与角色删除后允许使用新 ID 重新创建同名对象。账号列明确使用 `utf8mb4_0900_as_ci`，以 `UNIQUE(username)` 保持大小写无关唯一。授权来源统一为角色菜单关系。

原身份模块七张实体表主键是非自增 BIGINT，由 MyBatis-Plus 生成；三张关联表使用联合主键。相机资源继续使用雪花 ID，定位与账户范围以所属资源 ID 为主键，授权关系使用联合主键。内置角色、菜单和示例部门使用固定种子 ID，账号由初始化命令生成 ID。`version` 用于防止旧表单覆盖，`session_version` 用于会话失效。IP 封禁表的 version 同时隔离解除前尚未结束的登录尝试；unblocked_by 保留历史操作人 ID，不建立用户外键。审计和登录记录同样保留历史身份，用户删除不会级联删除这些记录。

## 生成与初始化

修改逐表文件后，从仓库根目录执行：

```powershell
./scripts/export-sql.ps1
./scripts/export-sql.ps1 -Check
```

生成器按外键依赖倒序 DROP、正序 CREATE 与插入种子，保持外键检查开启。默认码流的循环复合外键通过 DEFERRED CONSTRAINT 在两表存在后添加，重建前先解除此边。新增表必须在生成器登记依赖顺序。

要求 MySQL 8.0.17+、InnoDB、utf8mb4；已有独立验证使用 8.0.36。相机外部身份键采用 `utf8mb4_0900_bin`（自 [MySQL 8.0.17](https://dev.mysql.com/doc/relnotes/mysql/8.0/en/news-8-0-17.html) 提供）。只在专用空库执行汇总文件。初始化为 SUPER_ADMIN、三个目录、十三个 PAGE 及角色页面关联，预览中心排在相机管理前；视频分组旧入口复用相机管理，相机状态和诊断日志为独立页面。示例组织如下：

| ID | 名称 | 上级 |
|---|---|---|
| 2001 | 飞云科技公司 | 无 |
| 2002 | 研发部门 | 飞云科技公司 |
| 2003 | 运维部门 | 飞云科技公司 |

| 菜单目录 | 页面 | 模块键 |
|---|---|---|
| 系统管理（1001） | 用户管理（1002） | user |
| 系统管理（1001） | 角色管理（1003） | role |
| 系统管理（1001） | 菜单管理（1004） | menu |
| 系统管理（1001） | 部门管理（1005） | department |
| 监控面板（1008） | 操作记录（1006） | audit |
| 监控面板（1008） | 服务信息（1007） | server |
| 监控面板（1008） | 接口文档（1009） | api-docs |
| 视频管理（1100） | 预览中心（1105） | camera |
| 视频管理（1100） | 相机管理（1101）、视频分组（1103） | camera |
| 视频管理（1100） | 相机授权（1104） | camera_scope |
| 视频管理（1100） | 相机状态（1106） | camera |

从仓库根目录启动 MySQL 客户端；完整 SQL 的相对路径仅含 ASCII，可直接导入。替换 `YOUR_DB_USER` 为有建库及建表权限的安装账号；`-p` 会交互询问密码，密码不要写入命令：

```powershell
mysql --default-character-set=utf8mb4 -h 127.0.0.1 -P 3306 -u YOUR_DB_USER -p
```

仅当 `streamfusion` 尚不存在时，在该客户端中执行：

```sql
CREATE DATABASE streamfusion CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_ci;
USE streamfusion;
SOURCE platform-api/sql/streamfusion-mysql.sql;
```

`SOURCE` 路径相对于启动 MySQL 客户端时的目录。若该库已存在，先检查是否为空并确认用途；有任何业务表时停止，不继续导入。数据库名自定义时，同步修改后端 JDBC URL；应用账号至少需要该库的查询、新增、更新和删除权限。

首次导入后、管理员引导前，执行以下只读核对。应分别得到30个表名，以及 `users=0`、`roles=1`、`directories=3`、`pages=13`、`super_admin_pages=13`、`departments=3`：

```sql
SHOW TABLES;
SELECT
    (SELECT COUNT(*) FROM sys_user) AS users,
    (SELECT COUNT(*) FROM sys_role) AS roles,
    (SELECT COUNT(*) FROM sys_menu WHERE type='DIRECTORY') AS directories,
    (SELECT COUNT(*) FROM sys_menu WHERE type='PAGE') AS pages,
    (SELECT COUNT(*) FROM sys_role_menu rm JOIN sys_role r ON r.id=rm.role_id
        WHERE r.code='SUPER_ADMIN') AS super_admin_pages,
    (SELECT COUNT(*) FROM sys_dept) AS departments;
```

这是全新初始化的预期值，不是已运行数据库的固定限制；业务管理可以合法修改部门、菜单和普通角色。

公司名称、部门名称和结构可在初始化前修改逐表SQL，运行后也可通过部门管理改名、移动或按引用规则删除；运行时代码不按这些名称或ID判断业务。重复执行独立示例部门 INSERT 不覆盖已经修改的名称，但整份初始化文件仍会重建表。

SQL不创建用户，也不包含管理员密码或固定哈希。按[快速开始](../../README.md#快速开始)运行 `--bootstrap-admin` 创建首个管理员并提供凭据；默认账号名为admin，可通过 `SF_BOOTSTRAP_USERNAME` 调整，昵称可通过 `SF_BOOTSTRAP_NICKNAME` 调整。`SF_BOOTSTRAP_DEPARTMENT_ID` 默认为2002，仅用于初始管理员归属；自定义组织时改成目标部门ID，空值表示不关联。已存在用户或成功引导记录时命令拒绝重复初始化，不重置密码或关系。

使用环境初始密码的初始化模式要求首次登录改密。运行期普通账号的新建/重置从 `SF_AUTH_INITIAL_PASSWORD` 读取受控初始密码，不自动分配示例部门；角色和部门由管理员明确分配。

**逐表与汇总文件含 DROP TABLE，会删除数据，不能用于已有库升级。** 已有库应备份、核对真实结构和数据，再使用定向增量SQL。MySQL DDL 不能依靠事务回滚，代码与数据库须匹配切换。添加示例组织只执行对应INSERT和明确的用户部门关联，不能重放初始化文件。

## 已有库与菜单配置

已有库先停止写入，备份数据库与相机密钥，再比较真实表、列、约束和数据与当前维护源。在隔离库恢复并预演经过核对的定向变更，确认旧数据和授权关系保留后才执行实际升级。MySQL DDL 隐式提交，失败后须按实际执行结果处理。交付目录不提供可叠加试跑的历史增量脚本，禁止用完整初始化文件覆盖已有数据。

如果当前库仅缺少 `camera_device.local_name` 与 `remark`，所需变化是添加两列，不回填旧行、不改外键；回退应用版本可保留这两列。其他旧版本必须单独评估差异。相机密钥与网络配置见[外部配置说明](../config/README.md)。

如果已核对当前库与维护源一致、仅缺少 `camera_status_check` 和 `camera_status_snapshot`，可在仓库根目录运行 `./scripts/export-camera-status-upgrade.ps1`。脚本从两份逐表源生成仅含 CREATE TABLE 的候选升级文件，默认位于 `.run/camera-status-a2-02/camera-status-upgrade.sql`；它不连接数据库、不执行升级，已有不同内容的输出不会被覆盖。实际升级仍须备份、隔离预演及负责人授权；状态表已经存在或其他结构有差异时另行评估，禁止重放初始化文件。

检查回执不建立用户或资产外键，保留期内不会阻止现有硬删除。快照通过通道/Profile复合外键随 Profile 删除，回执清理不删除快照。新代码启动检查会拒绝缺少状态表的数据库；回退应用可保留两张新增表，不必删除数据。

有限期巡检新增两张计划/目标表及 `camera_status_check.plan_id`。已有当前两张状态表、没有plan_id且没有巡检表的库，可运行 `./scripts/export-camera-patrol-upgrade.ps1` 从逐表源生成定向候选DDL。脚本不连接或升级数据库；须先核对实际结构、备份并隔离预演，再取得实际升级授权。禁止重放初始化。手动检查仍受会话约束；计划检查通过plan_id分支使用持久账号/模块/相机范围授权，原session_digest字段保存目的隔离的计划HMAC而非Session。回退旧代码可保留新增表/列，不需删除状态或相机数据。

独立相机状态页面不增加表或列。已有库不会在启动时自动补菜单；由负责人授权后通过现有 `POST /menus` 配置 PAGE，`parentId` 使用实际视频管理目录ID，`routeName=CameraStatus`、`path=/camera/status`、`componentKey=/camera/Status`、`moduleKey=camera`。创建页面会事务内补齐 SUPER_ADMIN 关系，普通角色仍由管理员明确授权。不要直接写菜单关系或重放初始化SQL。相机状态页沿用 `camera` 模块权限，不新增只读权限模型；媒体首帧检测尚未实现。

`component_key` 直接采用 `views` 下的页面路径，不再接受 `SYSTEM_*` 等旧组件别名。操作记录和服务信息的组件与默认 URL 统一为 `/monitor/Audit`、`/monitor/Server`。以下页面已包含在完整初始化中；运行期新增菜单通过管理页面完成：

| 页面 | 上级目录 | 页面唯一标识 | 页面路径 | 自动采用的模块键 |
|---|---|---|---|---|
| 操作记录 | 监控面板 | audit | /monitor/Audit | audit |
| 服务信息 | 监控面板 | server | /monitor/Server | server |
| 接口文档 | 监控面板 | api-docs | /monitor/ApiDocs | api-docs |
| 诊断日志 | 监控面板 | diagnostics | /monitor/Diagnostics | diagnostics |

监控面板为 DIRECTORY，建议图标 `monitor`；根目录按导航分区排序，BUSINESS在前，SYSTEM、MONITOR固定最后，同分区再按sort/id排序；操作记录、服务信息、接口文档排序分别为 0、1、2；接口文档为 PAGE，建议图标 `menu`。创建 PAGE 会在同一事务自动关联 SUPER_ADMIN；普通角色需在角色管理中明确授权。当前菜单界面不单独输入模块键，新建时由页面唯一标识推导，所以接口文档填写 `api-docs`。初始化 SQL 的路由名称 `MonitorApiDocs` 已显式配置 `module_key='api-docs'`，不需要修改；通过 API 创建时才可分别传入 `routeName` 与 `moduleKey`。

### 诊断日志菜单升级

`LOG-IMPLEMENT-01` 不新增表或列。新库初始化包含“诊断日志”PAGE，模块键 `diagnostics` 独立于 `audit`、`camera`。已有库优先通过菜单管理新增：监控面板下，页面唯一标识 `diagnostics`，路径和组件 `/monitor/Diagnostics`，排序3；创建事务自动补齐超级管理员关系，普通角色需明确授权。

如经授权采用 SQL，在项目根目录运行 `./scripts/export-diagnostics-menu-upgrade.ps1` 生成候选增量脚本；监控目录不是初始化ID1008时，传入 `-MonitorDirectoryId 实际目录ID` 和新的 `-OutputPath`。须先备份、核对实际监控目录存在且菜单1010、路由名 `MonitorDiagnostics`、路径 `/monitor/Diagnostics` 未占用，再隔离预演执行。该脚本只生成菜单和 SUPER_ADMIN 关系 INSERT，不连接数据库、不覆盖旧菜单；禁止使用 `mysql --force` 忽略错误，禁止重放初始化。已有同类菜单时不要执行候选，应检查其组件及授权。回退代码时可在菜单管理禁用该页面，日志文件可保留。

诊断数据写入 `streamfusion.work-dir/logs/diagnostics*.jsonl`，当前文件10 MB触发滚动，归档最多7天、100 MB（按滚动时清理，当前文件另计）。仅查询本机实例，不导入旧文本日志；保留期、容量或查询扫描上限可能导致旧记录不可查。

### 相机状态时间信息升级

`CAMERA-STATUS-10` 在快照表增加 `state_since`、`last_accessible_at`、`last_failed_at` 三个可空时间字段，不新增表或外键。仅当当前状态/巡检表已存在且这三列均缺失时，运行 `./scripts/export-camera-status-time-upgrade.ps1` 生成定向 ALTER 候选。先备份并在隔离库预演，再经授权执行；脚本不连接数据库、不回填历史、不自动执行。部分列已存在时应另行评估，不能重放候选或初始化 SQL。

旧事实只回退到已有 `observed_at`，不能还原更早的异常发生时间。时间表示同一连接配置下的检查采样，不是设备真实上下线；成功/失败转换更新本轮首次时间，配置改变重新起算，跳过/取消不推进事实。新版本启动需要这三列；回退代码时保留可空列即可，不删除旧数据。

### 操作日志保留与导出升级

`LOG-IMPLEMENT-02` 新增 `sys_audit_retention_policy` 和 `sys_audit_export` 两表，无外键，不改变已有审计记录。初始保留策略关闭，成功 BOOTSTRAP 记录始终排除清理。已有库必须先备份、核对两表均缺失，再执行 `./scripts/export-audit-maintenance-upgrade.ps1` 生成候选增量 SQL，隔离预演后按授权执行。脚本不连接数据库、不删除日志、不自动升级；部分表已存在时应核对实际结构，不得重放初始化或忽略错误。

应用普通启动会检查这两张新增表；升级应先完成结构变更。回退旧应用可以保留两表，禁用策略后再回退；不要删除已有审计数据。导出文件存于 `streamfusion.work-dir/exports/audit`，最多5个未清理文件、单文件100 MiB、4个活跃任务；过期24小时清理，任务元数据保留。导出工作线程与清理共用专用后台线程，空闲每分钟检查一次，显式创建/立即执行会唤醒。

临时详细诊断不新增表：本机内存状态，默认关闭，重启停止，最多60分钟或1000次匹配请求。参数和响应先按字段投影、脱敏再保存，各8 KiB；读取器兼容旧JSONL行，单行上限64 KiB，整体16 MiB扫描预算不变。启用新能力前应检查 `audit`/`diagnostics` 页面授权，清理和采集启停还要求超级管理员。

## 时区与验证

数据库 DATETIME 使用 Asia/Shanghai（UTC+8），脚本设置会话 `+08:00`；公共 API Instant 仍使用 UTC/Z。既有日期是否需要转换须单独核对，不能重复加 8 小时。

普通启动与非 Web 管理员引导都会只读检查业务表及必要列，缺项时停止并报告对应表/列和本 SQL 指南。启动检查不创建或修改结构，也不强制菜单和种子数量；索引、外键、列类型及业务规则仍需由初始化源和相应测试验证。

在 `platform-api` 执行 `./mvnw.cmd verify`。SchemaSqlTest 使用同一汇总文件在隔离 H2 中检查重建、种子幂等、硬删除后账号复用、大小写唯一及关系约束；仅把账号 MySQL 排序规则适配为 H2 的 `VARCHAR_IGNORECASE`。H2 不能证明 MySQL 字符集、DDL 或并发行为。

已有本机库可执行显式只读检查：

```powershell
./mvnw.cmd "-Dtest=LocalSchemaInspectionTest,LocalInfrastructureTest" "-Dsf.test.localSchema=true" "-Dsf.test.localInfrastructure=true" test
```

LocalSchemaInspectionTest 只接受 loopback:3306 的 streamfusion 库，启动时检查全部30张应用表的必要列；测试进一步核对十二张系统表的138列、字段注释、时区、用户名排序规则和唯一索引；旧结构未迁移时应失败。LocalInfrastructureTest 只检查 SELECT 1 与 Redis PING。两项默认跳过，不启动相机/日志维护任务；不能将跳过计为通过，也不能将依赖连通当作业务验收。
