# 数据库 SQL

十份 `业务/` 逐表 SQL 是唯一维护源；`汇总/streamfusion-mysql.sql` 由 `scripts/export-sql.ps1` 生成，不手工修改。应用不自动执行 SQL，无 Flyway；初始化 SQL 不打包进应用 JAR，使用源码仓库中的汇总文件初始化数据库。

## 当前结构

| 数据 | 表 | 实现 |
|---|---|---|
| 用户 | sys_user | PBKDF2 密码哈希、待改密/正常/停用状态、登录冷却、会话失效版本；账号大小写无关唯一 |
| 角色与授权 | sys_role、sys_user_role、sys_role_menu | 用户关联角色，角色关联 PAGE；SUPER_ADMIN 获得全部 PAGE，目录不作为授权关系 |
| 菜单 | sys_menu | 目录与页面树、路由和模块键，模块鉴权与导航共用有效页面授权 |
| 部门 | sys_dept、sys_user_dept | 多根组织树、多部门且无主部门；组织归属不产生权限 |
| 审计 | sys_operation_log | 操作者、目标、动作、结果、来源及受控变更快照 |
| IP 防护 | sys_ip_block | 规范化来源地址、封禁与人工解除状态、失败次数、时间及并发版本；无自动解封 |
| 登录记录 | sys_login_record | 成功登录时的账号与来源快照、活动及到期时间、结束原因；不保存 Session ID 或凭据 |

用户、角色、菜单、部门均硬删除，数据库没有软删除字段；用户与角色删除后允许使用新 ID 重新创建同名对象。账号列明确使用 `utf8mb4_0900_as_ci`，以 `UNIQUE(username)` 保持大小写无关唯一。旧 `sys_permission/sys_role_permission` 不再维护，授权来源统一为角色菜单关系。

七张实体表主键是非自增 BIGINT，由 MyBatis-Plus 生成；三张关联表使用联合主键。内置角色、菜单和示例部门使用固定种子 ID，账号由初始化命令生成 ID。`version` 用于防止旧表单覆盖，`session_version` 用于会话失效。IP 封禁表的 version 同时隔离解除前尚未结束的登录尝试；unblocked_by 保留历史操作人 ID，不建立用户外键。审计和登录记录同样保留历史身份，用户删除不会级联删除这些记录。

## 生成与初始化

修改逐表文件后，从仓库根目录执行：

```powershell
./scripts/export-sql.ps1
./scripts/export-sql.ps1 -Check
```

生成器按外键依赖倒序 DROP、正序 CREATE 与插入种子，保持外键检查开启。新增表必须在生成器登记依赖顺序。

要求 MySQL 8.0.16+、InnoDB、utf8mb4。只在专用空库执行汇总文件。初始化内容为 SUPER_ADMIN、两个目录、七个 PAGE 及角色页面关联，还有以下可编辑的示例组织：

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
| 系统管理（1001） | 操作记录（1006） | audit |
| 系统管理（1001） | 服务信息（1007） | server |
| 监控面板（1008） | 接口文档（1009） | api-docs |

Windows MySQL 客户端的 `SOURCE` 对中文文件路径存在兼容问题。先在仓库根目录将汇总文件复制为仅含 ASCII 的临时相对路径，再从同一目录启动客户端。该副本只是导入文件，不作为维护源；每次导入前重新复制最新汇总。替换 `YOUR_DB_USER` 为可创建专用开发数据库的账号；`-p` 会交互询问密码，密码不要写入命令：

```powershell
New-Item -ItemType Directory -Path .run -Force | Out-Null
Copy-Item -LiteralPath 'platform-api/sql/汇总/streamfusion-mysql.sql' -Destination '.run/streamfusion-mysql.sql'
mysql --default-character-set=utf8mb4 -h 127.0.0.1 -P 3306 -u YOUR_DB_USER -p
```

仅当 `streamfusion` 尚不存在时，在该客户端中执行：

```sql
CREATE DATABASE streamfusion CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_ci;
USE streamfusion;
SOURCE .run/streamfusion-mysql.sql;
```

`SOURCE` 路径相对于启动 MySQL 客户端时的目录。若该库已存在，先检查是否为空并确认用途；有任何业务表时停止，不继续导入。数据库名自定义时，同步修改后端 JDBC URL；应用账号至少需要该库的查询、新增、更新和删除权限。

首次导入后、管理员引导前，执行以下只读核对。应分别得到十个表名，以及 `users=0`、`roles=1`、`directories=2`、`pages=7`、`super_admin_pages=7`、`departments=3`：

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

## 已有库升级

先备份并检查目标库。早期八表库缺少 IP 封禁和登录记录；已补 IP 封禁的九表库仍可能缺少登录记录。按实际缺项升级，不按总表数猜测，因为数据库可能保留旧历史表。

### 增加 IP 封禁表

缺少 `sys_ip_block` 时，从 `业务/IP封禁表.sql` 单独提取完整 `CREATE TABLE sys_ip_block (...)` 与后面的 `CREATE INDEX`，在已确认的数据库执行；保留 `SET NAMES utf8mb4` 与 `SET time_zone = '+08:00'`，**不得执行文件中的 DROP TABLE**。已有同名表时先检查结构，不重复执行或覆盖。

核对唯一键 `uk_ip_block_source`、索引 `ix_ip_block_status_time`、两个 CHECK 及空表状态，再启动新版后端。应用默认启用 IP 防护，缺表会导致业务请求失败。此表无需账号或菜单种子，也不改变已有角色关系。DDL 在 MySQL 中自动提交，操作前后的备份与行数、哈希应留存。回滚代码时保留新增表及封禁历史，不运行重建脚本。

### 增加登录记录表

缺少 `sys_login_record` 时，从 `业务/登录记录表.sql` 提取完整 `CREATE TABLE sys_login_record (...)` 与两条 `CREATE INDEX`，在备份后的目标库定向执行，不执行 DROP。核对十七列、主键 `id`、约束 `ck_login_record_limits`，以及 `ix_login_record_user_time(user_id, login_at, id)` 和 `ix_login_record_user_end(user_id, end_reason)` 两个索引。

登录成功会写入该表，因此此表是当前后端的必需结构。新建表不回填此前的成功登录，也不修改已有用户或会话。升级后用正常登录验证新增记录；回滚代码时保留新增表和历史记录。

### 补齐菜单

当前初始化源已包含操作记录、服务信息和监控面板接口文档。已有库的菜单属于可编辑业务数据，不自动重放种子。缺少页面时，使用已有超级管理员从菜单管理创建，避免固定种子 ID 与现有数据冲突：

| 页面 | 上级目录 | 页面唯一标识 | 页面路径 | 自动采用的模块键 |
|---|---|---|---|---|
| 操作记录 | 系统管理 | audit | /system/Audit | audit |
| 服务信息 | 系统管理 | server | /system/Server | server |
| 接口文档 | 监控面板 | api-docs | /monitor/ApiDocs | api-docs |

监控面板为 DIRECTORY，建议图标 `monitor`、排序 1；接口文档为 PAGE，建议图标 `menu`、排序 0。创建 PAGE 会在同一事务自动关联 SUPER_ADMIN；普通角色需在角色管理中明确授权。当前菜单界面不单独输入模块键，新建时由页面唯一标识推导，所以接口文档填写 `api-docs`。初始化 SQL 的路由名称 `MonitorApiDocs` 已显式配置 `module_key='api-docs'`，不需要修改；通过 API 创建时才可分别传入 `routeName` 与 `moduleKey`。

## 时区与验证

数据库 DATETIME 使用 Asia/Shanghai（UTC+8），脚本设置会话 `+08:00`；公共 API Instant 仍使用 UTC/Z。既有日期是否需要转换须单独核对，不能重复加 8 小时。

普通启动与非 Web 管理员引导都会只读检查业务表及必要列，缺项时停止并报告对应表/列和本 SQL 指南。启动检查不创建或修改结构，也不强制菜单和种子数量；索引、外键、列类型及业务规则仍需由初始化源和相应测试验证。

在 `platform-api` 执行 `./mvnw.cmd verify`。SchemaSqlTest 使用同一汇总文件在隔离 H2 中检查重建、种子幂等、硬删除后账号复用、大小写唯一及关系约束；仅把账号 MySQL 排序规则适配为 H2 的 `VARCHAR_IGNORECASE`。H2 不能证明 MySQL 字符集、DDL 或并发行为。

已有本机库可执行显式只读检查：

```powershell
./mvnw.cmd "-Dtest=LocalSchemaInspectionTest,LocalInfrastructureTest" "-Dsf.test.localSchema=true" "-Dsf.test.localInfrastructure=true" test
```

LocalSchemaInspectionTest 只接受 loopback:3306 的 streamfusion 库，检查十表、字段、用户名排序规则和唯一索引；旧结构未迁移时应失败。LocalInfrastructureTest 只检查 SELECT 1 与 Redis PING。两项默认跳过，不能将跳过计为通过，也不能将依赖连通当作业务验收。
