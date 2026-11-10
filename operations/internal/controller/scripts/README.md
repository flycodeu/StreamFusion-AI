# Ops 命令维护

`preparation/` 保存首次接入、升级、权限确认、上传及清理命令；`observe/` 保存主机和执行器探测脚本。`target_installer.go` 从这些文件生成安装包，底层受限授权脚本继续维护在 `operations/scripts/install-target.sh`。

`controllerScript` 原样读取，`controllerTemplate` 使用 Go `fmt` 占位符；接入安装脚本中的 `@ROOT@` 等标记由原来的 `strings.NewReplacer` 填充。替换前仍按原入口校验和引用参数。

文件通过 `go:embed` 随控制器发布，修改后需重新构建。保留首尾换行及 Shell 引用方式，运行 `go test -buildvcs=false ./internal/controller` 检查接入、升级、授权和恢复路径。

`service-configuration/` 保存 Nginx、MySQL、Redis 的安装后配置脚本：`common.py` 处理备份、状态保持与恢复，`docker.py` 保留原容器设置，各软件文件只处理白名单配置项。`additional-software/manage-config-*` 保存 Jenkins、GitLab、Prometheus、Nacos、PostgreSQL 的配置步骤；`annotation/annotation-*` 保存标注平台配置与数据迁移。

模板工厂声明字段、资源和读取/应用/验证/恢复步骤，界面使用相同模板生成表单。新增配置时同步声明实际修改文件与端口；测试场景放 `internal/controller/testdata/`，运行时目录只放被模板使用的脚本。
