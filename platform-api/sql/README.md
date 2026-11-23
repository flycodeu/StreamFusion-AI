# 数据库初始化

平台使用 MySQL 8.0.17+。新部署只需导入 [streamfusion-mysql.sql](streamfusion-mysql.sql)，一次创建 51 张表及必要的索引、约束和初始数据。

**初始化脚本包含 `DROP TABLE`，只可在专用空库执行。** 已有库请先备份数据和加密密钥，按实际结构制定并验证迁移方案。应用启动时只读校验数据库结构。

## 安装

从仓库根目录打开 MySQL 客户端：

```sh
mysql --host=127.0.0.1 --port=3306 --user=<数据库账号> --password --default-character-set=utf8mb4
```

```sql
CREATE DATABASE streamfusion CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_ci;
USE streamfusion;
SOURCE platform-api/sql/streamfusion-mysql.sql;
```

如果已创建专用空库，从 `USE` 开始。自定义库名时同步配置 [JDBC URL](../config/README.md)。初始化预置 `SUPER_ADMIN`、三个菜单目录、十六个页面和示例部门。首个管理员按[快速开始](../../README.md#快速开始)创建。

## 维护

只在 `业务/` 修改逐表 SQL。新增表时，在生成器中登记依赖顺序，然后从仓库根目录执行：

```powershell
./scripts/export-sql.ps1
./scripts/export-sql.ps1 -Check
```
