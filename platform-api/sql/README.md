# 数据库

要求 MySQL 8.0.17+。新安装只需执行 [streamfusion-mysql.sql](streamfusion-mysql.sql)，包含30张应用表、索引、约束和初始数据。应用启动只检查结构，不自动建表或迁移。

**初始化文件包含 DROP TABLE，只能用于专用空库；已有数据的库禁止重复导入。**

## 初始化

在仓库根目录打开 MySQL 客户端：

```powershell
mysql --host=127.0.0.1 --port=3306 --user=<数据库账号> --password --default-character-set=utf8mb4
```

确认 `streamfusion` 尚不存在后执行：

```sql
CREATE DATABASE streamfusion CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_ci;
USE streamfusion;
SOURCE platform-api/sql/streamfusion-mysql.sql;
SHOW TABLES;
```

已有空库可以直接选择该库执行 `SOURCE`；自定义库名须同步修改后端 JDBC URL。运行账号需要查询、新增、更新和删除权限；建表账号还需要对应的结构管理权限。

初始数据包括 SUPER_ADMIN、三个菜单目录、十三个页面及示例部门，不创建用户或固定密码。按[快速开始](../../README.md#快速开始)运行 `--bootstrap-admin` 创建首个管理员。示例部门可通过部门管理调整，普通新用户不会自动关联部门。

连接参数、相机密钥和网络配置见[配置说明](../config/README.md)。数据库业务时间使用 Asia/Shanghai（UTC+8），初始化文件设置会话时区 `+08:00`，不修改 MySQL 全局时区。

## 结构维护

`业务/` 下的逐表文件是唯一结构维护源。修改后在仓库根目录执行：

```powershell
./scripts/export-sql.ps1
./scripts/export-sql.ps1 -Check
```

不要手工修改生成的 `streamfusion-mysql.sql`。新增表需在生成器中登记依赖顺序；汇总文件按依赖处理外键，保持外键检查开启。SQL 随源码交付，不打包进应用 JAR。

| 数据 | 主要表 |
|---|---|
| 身份与授权 | sys_user、sys_role、sys_menu、sys_dept 及关联表 |
| 审计与登录 | sys_operation_log、sys_audit_retention_policy、sys_audit_export、sys_login_record、sys_ip_block |
| 相机与接入 | camera_source、camera_device、camera_channel、camera_stream_profile 及凭据、端点、定位和导入表 |
| 状态与巡检 | camera_status_check、camera_status_snapshot、camera_status_plan、camera_status_plan_target |
| 视频分组与范围 | camera_group、camera_user_scope、camera_user_group_grant、camera_user_channel_grant |

设备、通道、码流分别保存。状态快照以 `(profile_id, layer)` 为主键，区分控制连接、媒体收帧和平台状态；巡检计划保存固定范围、执行间隔、期限和并发方式。检查时间是采样时间，不是设备真实上下线时间。

## 已有数据库

保留业务数据时，先备份数据库及相机加密密钥，核对实际表、列、索引和约束，再制定针对当前结构的变更。在隔离副本验证数据保留和应用启动后，停写执行并保留恢复点。MySQL DDL 隐式提交，失败后应核对已执行内容，不能盲目重试。

本目录只交付当前安装结构，不要求新用户依次执行开发阶段的历史 SQL。已有菜单通过菜单管理维护，新增页面会自动关联超级管理员，普通角色须明确授权。

## 验证

在 `platform-api` 目录执行：

```powershell
./mvnw.cmd "-Dtest=SchemaSqlTest,CameraStatusSchemaTest" test
```

上述测试使用隔离 H2，验证汇总 SQL、初始数据和状态约束，不替代 MySQL 验证。核对本机开发库时显式运行：

```powershell
./mvnw.cmd "-Dtest=LocalSchemaInspectionTest,LocalInfrastructureTest" "-Dsf.test.localSchema=true" "-Dsf.test.localInfrastructure=true" test
```

本机检查读取 `local` 配置，结构检查只允许本机3306端口的 `streamfusion` 库；连通检查还需要 Redis。两项默认跳过，不修改业务数据。健康接口正常不代表相机、预览或厂商平台已完成实机验收。
