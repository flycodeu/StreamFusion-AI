# 数据库 SQL

## 建表文件（直接查看这些文件）

当前目录每张表一份建表 SQL：部门表.sql、用户表.sql、角色表.sql、权限表.sql、菜单表.sql、
用户角色关联表.sql、角色权限关联表.sql、操作审计表.sql。
**每个字段在 CREATE TABLE 中直接包含中文 COMMENT，表本身也有 COMMENT。**

**这些文件是破坏性重建脚本：先 DROP TABLE IF EXISTS，再 CREATE TABLE，会清除目标表全部数据。**
仅供明确可丢弃的独立开发/测试库，执行前核对目标并备份；MySQL DDL 不能靠事务回滚恢复。

`汇总.sql` 先按逆依赖顺序删除8张业务表，再按依赖顺序汇总建表语句和内置角色种子；备注已经合并进 CREATE，
不在末尾再执行一批 ALTER TABLE 补备注。没有新增“备注表”，也没有默认账号或密码。

所有脚本显式保持 FOREIGN_KEY_CHECKS=1。单表脚本若有其他表引用会拒绝DROP，
请先处理依赖，或在获准清空全部8表的独立测试库使用汇总脚本；不要临时关闭外键检查绕过。
汇总也不会删除来自范围外的引用表；有额外依赖时应停止并评估。

这些文件是供阅读或独立开发/测试库重建的生成文件，**不能再次导入现有应用数据库**。
手工导入不产生 Flyway 历史，不把手工初始化库直接作为应用迁移目标。
脚本不删除数据库或 flyway_schema_history，也不伪造迁移记录；应用安装/升级始终使用下述迁移目录。

## 历史迁移（应用使用）

`migrations/` 保存已执行的 V001–V011 原始迁移，文件内容和校验和不变。
Maven 只将该目录的 V*__*.sql 打包为 db/migration；不会将上面的建表文件重复执行。
V009 仅建立 SUPER_ADMIN 角色；V010 是旧库备注升级记录，不是额外业务表。

新环境和升级环境均由 Flyway 按版本迁移。应用不建库、不开启自动 baseline、禁止 clean。
已执行文件不得修改，后续从 V012 起追加迁移；新增表和字段须直接带 COMMENT。
MySQL 8.0.16+、InnoDB、utf8mb4；默认北京时间 UTC+8。逐表和汇总脚本均设置 time_zone='+08:00'。
应用 JDBC connectionTimeZone=Asia/Shanghai，
连接池同步设置会话偏移；不修改数据库服务器全局时区。凭证只放个人配置或环境变量。

V011 只更新12个 DATETIME 字段的时区备注，不盲目给已有数据加8小时。
如果从旧 UTC 存储升级，必须停写、备份并核对旧值后单独转换已有 DATETIME；
不能把 TIMESTAMP 或已按北京时间写入的数据再次平移，新安装也不需要转换。
不得仅应用V011就宣称已有业务数据已转换。公共API的Instant仍使用UTC/Z，转换边界使用Asia/Shanghai。

## 生成与一致性检查

从仓库根目录执行：

```powershell
./scripts/export-sql.ps1
./scripts/export-sql.ps1 -Check
```

脚本把 V010 备注及 V011 北京时间备注合入 V001–V008 的 CREATE 定义，同时生成每表文件与汇总文件。
只转换这组已知初始表，不实现通用 SQL 解析器；后续迁移在汇总中按顺序追加。
表文件表达初始建表定义，不保证将未来所有 ALTER 折叠成最新结构快照。
不要单独手改生成结果；CI 同时检查逐表文件和汇总文件是否与源迁移一致。

## 验证

常规 `mvnw.cmd verify` 使用 H2，不能替代 MySQL 备注/锁/兼容性验证。
V010 的 MySQL 可执行注释在 H2 中跳过，真实备注由本地集成测试检查。

在 platform-api 目录显式运行下面的**写测试**，只用于本机新安装库：

```powershell
./mvnw.cmd "-Dtest=LocalSchemaMigrationTest" "-Dsf.test.localSchema=true" test
```

它仅接受 loopback:3306 的 streamfusion 库，拒绝已有用户的库，执行待应用迁移，
验证重复迁移、基本约束及备注。探针事务回滚，但自增计数可产生间隙；不创建/删除数据库。
不要在已有业务数据的数据库运行。LocalInfrastructureTest 则始终只读，且禁用迁移。
本地检查默认跳过，不进入普通 CI 的真实数据库。
