# 数据库 SQL

八份 `业务/` 逐表 SQL 是唯一维护源；`汇总/streamfusion-mysql.sql` 由 `scripts/export-sql.ps1` 生成，不手工修改。应用不自动执行 SQL，无 Flyway；SQL 仅作为测试资源读取，不打包进应用 JAR。

## 当前结构

| 数据 | 表 | 实现 |
|---|---|---|
| 用户 | sys_user | PBKDF2 密码哈希、待改密/正常/封禁状态、登录冷却、会话失效版本；账号大小写无关唯一 |
| 角色与授权 | sys_role、sys_user_role、sys_role_menu | 用户关联角色，角色关联 PAGE；SUPER_ADMIN 获得全部 PAGE，目录不作为授权关系 |
| 菜单 | sys_menu | 目录与页面树、路由和模块键，模块鉴权与导航共用有效页面授权 |
| 部门 | sys_dept、sys_user_dept | 多根组织树、多部门且无主部门；组织归属不产生权限 |
| 审计 | sys_operation_log | 操作者、目标、动作、结果、来源及受控变更快照 |

用户、角色、菜单、部门均硬删除，数据库没有软删除字段；用户与角色删除后允许使用新 ID 重新创建同名对象。账号列明确使用 `utf8mb4_0900_as_ci`，以 `UNIQUE(username)` 保持大小写无关唯一。旧 `sys_permission/sys_role_permission` 不再维护，授权来源统一为角色菜单关系。

五张实体表主键是非自增 BIGINT，由 MyBatis-Plus `ASSIGN_ID` 生成；内置角色、菜单和示例部门使用固定种子 ID，账号由初始化命令生成 ID。`version` 用于防止旧表单覆盖，`session_version` 用于会话失效。

## 生成与初始化

修改逐表文件后，从仓库根目录执行：

```powershell
./scripts/export-sql.ps1
./scripts/export-sql.ps1 -Check
```

生成器按外键依赖倒序 DROP、正序 CREATE 与插入种子，保持外键检查开启。新增表必须在生成器登记依赖顺序。

要求 MySQL 8.0.16+、InnoDB、utf8mb4。只在专用空库执行汇总文件。初始化内容为 SUPER_ADMIN、四个管理 PAGE 及角色页面关联，还有以下可编辑的示例组织：

| ID | 名称 | 上级 |
|---|---|---|
| 2001 | 飞云科技公司 | 无 |
| 2002 | 研发部门 | 飞云科技公司 |
| 2003 | 运维部门 | 飞云科技公司 |

公司名称、部门名称和结构可在初始化前修改逐表SQL，运行后也可通过部门管理改名、移动或按引用规则删除；运行时代码不按这些名称或ID判断业务。重复执行独立示例部门 INSERT 不覆盖已经修改的名称，但整份初始化文件仍会重建表。

SQL不创建用户，也不包含管理员密码或固定哈希。按[快速开始](../../README.md#快速开始)运行 `--bootstrap-admin` 创建首个管理员并提供凭据；默认账号名为admin，可通过 `SF_BOOTSTRAP_USERNAME` 调整，昵称可通过 `SF_BOOTSTRAP_NICKNAME` 调整。`SF_BOOTSTRAP_DEPARTMENT_ID` 默认为2002，仅用于初始管理员归属；自定义组织时改成目标部门ID，空值表示不关联。已存在用户或成功引导记录时命令拒绝重复初始化，不重置密码或关系。

使用环境初始密码的初始化模式要求首次登录改密。运行期普通账号的新建/重置从 `SF_AUTH_INITIAL_PASSWORD` 读取受控初始密码，不自动分配示例部门；角色和部门由管理员明确分配。

**逐表与汇总文件含 DROP TABLE，会删除数据，不能用于已有库升级。** 已有库应备份、核对真实结构和数据，再使用定向增量SQL。MySQL DDL 不能依靠事务回滚，代码与数据库须匹配切换。添加示例组织只执行对应INSERT和明确的用户部门关联，不能重放初始化文件。

## 时区与验证

数据库 DATETIME 使用 Asia/Shanghai（UTC+8），脚本设置会话 `+08:00`；公共 API Instant 仍使用 UTC/Z。既有日期是否需要转换须单独核对，不能重复加 8 小时。

在 `platform-api` 执行 `./mvnw.cmd verify`。SchemaSqlTest 使用同一汇总文件在隔离 H2 中检查重建、种子幂等、硬删除后账号复用、大小写唯一及关系约束；仅把账号 MySQL 排序规则适配为 H2 的 `VARCHAR_IGNORECASE`。H2 不能证明 MySQL 字符集、DDL 或并发行为。

已有本机库可执行显式只读检查：

```powershell
./mvnw.cmd "-Dtest=LocalSchemaInspectionTest,LocalInfrastructureTest" "-Dsf.test.localSchema=true" "-Dsf.test.localInfrastructure=true" test
```

LocalSchemaInspectionTest 只接受 loopback:3306 的 streamfusion 库，检查八表、字段、用户名排序规则和唯一索引；旧结构未迁移时应失败。LocalInfrastructureTest 只检查 SELECT 1 与 Redis PING。两项默认跳过，不能将跳过计为通过，也不能将依赖连通当作业务验收。
