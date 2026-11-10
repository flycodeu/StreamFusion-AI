# 安装命令维护

此目录是软件安装定义的命令源，`command_sources.go` 将文件嵌入执行程序。修改后需重新构建；平台中已发布的配方和已提交任务继续使用其保存的命令快照。

| 目录 | 内容 |
| --- | --- |
| `builtin/` | 官方软件包安装、卸载和数据清理；`official-plan.json` 保存预检展示的固定 argv |
| `docker/` | 镜像拉取、离线加载、容器创建、启动和卸载 |
| `native/` | 原生文件安装、编译、包装脚本、服务启动和卸载 |
| `verify/` | 软件版本与动态库检查；`programs/` 定义各软件的验证命令 |
| `node/` | Node/npm/pnpm/nvm 账号环境操作；`commands.sh` 为完整操作源 |
| `examples/` | 命令编辑器的 Java、Nginx 示例草稿 |

`.sh` 为原样使用的命令或可组合片段，`.sh.tmpl` 使用 Go `fmt` 占位符填入已验证的目录、来源或固定参数。模板中的 Shell `printf` 百分号需要写成 `%%`；普通 `.sh` 保持 `%`。

片段的开头和结尾换行也属于命令内容，部分文件故意不以换行结束。读取时只统一 CRLF 为 LF，不裁剪空白，以保留现有命令哈希和历史定义。

新增软件时，在相应目录维护命令；`builtin_commands.go` 负责选择步骤、运行方式和权限，`command_flow.go` 负责保护步骤的顺序。`TestInstallationCommandsStayInTemplateSources` 检查生产 Go 源码中的安装 Shell、多行片段和固定命令 argv，排除测试样例与依赖工具元数据。现有 CI 的 `go test ./...` 会执行此门禁；生成后的命令语法与定义由既有回归测试验证。
