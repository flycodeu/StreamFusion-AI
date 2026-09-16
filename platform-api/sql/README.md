# 数据库 SQL

## 一个维护入口

业务/ 下八份逐表 SQL 是唯一维护源：部门表.sql、用户表.sql、角色表.sql、权限表.sql、菜单表.sql、
用户角色关联表.sql、角色权限关联表.sql、操作审计表.sql。字段和表直接包含中文 COMMENT。

[汇总/streamfusion-mysql.sql](汇总/streamfusion-mysql.sql) 由业务/ 下逐表文件自动生成，不手工编辑。
[业务/角色表.sql](业务/角色表.sql) 同时保存 SUPER_ADMIN 内置角色种子；
没有默认账号或密码。修改表结构、索引、备注或种子时，只改所属逐表文件，再从仓库根目录执行：

```powershell
./scripts/export-sql.ps1
./scripts/export-sql.ps1 -Check
```

生成器仅维护文件依赖顺序，将 DROP 逆序集中在前、CREATE/索引/种子正序放在后。
新增表需在脚本的文件列表登记依赖顺序；CI 检查汇总是否与源文件一致，不再维护 migrations 目录。

## 初始化与安全边界

应用不自动创建数据库或表、不执行 SQL；无 Flyway 依赖，spring.sql.init.mode=never。
SQL 仅作为测试资源读取，不打包进应用 JAR。

首次安装：创建专用空库并选择该库，使用 MySQL 客户端执行汇总/streamfusion-mysql.sql，检查八张业务表和内置角色，
然后启动应用。要求 MySQL 8.0.16+、InnoDB、utf8mb4；脚本设置会话 UTC+8，字段日期使用北京时间。

**逐表和汇总脚本都先 DROP 再 CREATE，会删除目标表全部数据。它们不是已有数据库的升级脚本。**
执行前确认目标和备份；MySQL DDL 不能靠事务回滚恢复，失败可能留下部分重建状态。
单表受其他表外键引用时不能单独重建；保持 FOREIGN_KEY_CHECKS=1，不以关闭外键检查绕过依赖。
应用健康检查仅证明依赖连通，不保证业务表已初始化。

已经初始化的本地数据库保持原样，不要重新导入。旧 flyway_schema_history 不再被应用读取，
可保留作为历史记录；本次文件整理不删除它。旧迁移文件可从 Git 提交 e3c86aa 恢复，但不再作为当前入口。

后续已有数据环境需要结构升级时，必须先评估备份与增量 ALTER 方案，并同步逐表定义后重新生成汇总。
修改源文件不会自动改变数据库；不要为了省维护步骤而重建有数据的表。

## 时区

默认 Asia/Shanghai（UTC+8），JDBC connectionTimeZone 与连接池会话时区保持一致，
不修改数据库服务器全局时区。公共 API 的 Instant 仍使用 UTC/Z，映射时显式转换。
旧 UTC DATETIME 数据需核对后单独转换，不能把已按北京时间写入的数据重复加8小时。

## 验证

在 platform-api 执行 mvnw.cmd verify。SchemaSqlTest 只在隔离的 H2 内存库读取同一份汇总 SQL，
验证建表、再次重建、初始角色、无默认账号和唯一/FK/CHECK 约束；仅适配 MySQL 会话设置。
它不能替代 MySQL 语法、字符集与并发行为的实库验收。

现有本机库可显式运行只读检查：

```powershell
./mvnw.cmd "-Dtest=LocalSchemaInspectionTest,LocalInfrastructureTest" "-Dsf.test.localSchema=true" "-Dsf.test.localInfrastructure=true" test
```

LocalSchemaInspectionTest 只接受 loopback:3306 的 streamfusion 库，检查8张表、79字段备注、
12个日期字段备注及会话时区；不执行 DDL/DML。LocalInfrastructureTest 仅 SELECT 1 与 Redis PING。
两者默认跳过；本次不自动执行真实 MySQL 重建。
