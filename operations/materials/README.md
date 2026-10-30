# 可复用安装材料库

此目录说明发布材料的结构。实际二进制材料放在发布程序旁的 `materials` 目录，包含 `library.json`；不将安装包嵌入 Go 二进制，不改运行数据库结构。

## 制作发布材料

使用本次构建的程序输出固定配方。打包脚本只读取原始文件、核对大小和 SHA-256，并生成确定性组合包；不会安装软件或修改输入文件。

```powershell
python .\scripts\package-material-library.py --ops .\streamfusion-ops.exe --source "$env:USERPROFILE\Downloads" --out D:\packages\sfops-materials --download
```

默认包含 Ubuntu 24.04 Redis 7.0.15、Nginx 1.24.0、Eclipse Temurin JDK 8u504-b01 和 Docker Engine 29.9.2 的固定配方。`--download` 只补下载目录内缺少的固定原始包；已有但校验不符的文件会被拒绝。若完全离线，准备全部原始包并省略该参数。输出目录必须不存在。

指定子集或补充固定配方的 Docker-save 镜像：

```powershell
python .\scripts\package-material-library.py --ops .\streamfusion-ops.exe --source "$env:USERPROFILE\Downloads" --recipe java@8 --docker-archive redis@7.4.11=D:\packages\redis.tar --out D:\release\materials
```

Docker-save 包的文件 SHA-256 只记录传输完整性。导入时仍核对配方固定镜像 ID、架构和各层摘要；任意 tar 文件不能因此获得可信镜像身份。

## 随控制器发行包分发

准备好材料后，构建脚本会把 `library.json` 及其列出的文件放到发行程序相邻的 `materials`，并逐项写入发行包 `SHA256SUMS`。暂存只核对清单、路径、文件大小和摘要；固定配方及包内内容仍由运行程序导入时核验。

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\build-linux.ps1 -MaterialLibrary D:\packages\sfops-materials
```

Linux 开发机使用：

```bash
bash scripts/build-linux.sh --material-library /srv/packages/sfops-materials
```

省略目录时，若源码 `materials/library.json` 存在则携带；只有本说明文件时仍生成最小发行包。显式指定目录缺失、清单损坏、链接或摘要不符会中止打包。构建过程不会自动下载或执行材料包。暂存目录内旧文件不被递归清理，发行 tar 只收入本轮清单列出的材料，避免夹带过期包。

Linux 安装器会先运行纯只读的 `material-library-check --material-library <发行materials目录>`，核对当前内置配方、路径、大小和 SHA-256，并要求发行 `SHA256SUMS` 覆盖每个成员。检查通过后，将清单及其声明文件放到 `<prefix>/materials`，目录为 root 持有的 0755、文件为 0644，服务账号可读取；不依赖 Python 或 jq，也不执行材料。已有同名文件内容或权限不同会在修改账号、状态前拒绝；重复安装可补回缺失的声明文件，额外旧文件保留。该命令不打开状态目录，固定厂商包的内部内容仍由首次 `serve` 导入核验。

## 导入与复用

程序初始化管理员后可调用 `import-materials --state <状态目录> --material-library <材料目录>`。启动时可指定同一目录；默认发现程序旁的材料库后，导入既有 `installation_materials` 记录和私有 `installation-materials/<id>.tar`。

每项必须通过当前内置配方、文件大小、SHA-256 和内容校验才进入 `AVAILABLE`。重复启动复用已核验记录；源文件不被改动；显式删除的预置记录不会在下次启动自动恢复。缺少原文件、非普通文件、符号链接、配方不匹配或容量超限会给出错误并保留此前已经完成的条目。单包 2GiB，合计 100 项/16GiB，沿用现有限额。

材料列表显示实际来源、软件名、版本、发布方和主件/依赖。来源信息保存在同目录 `<id>.origin.json`，历史未记录来源的条目显示未知。`RECIPE_SHA256` 仅说明字节与内置固定配方一致；没有宣称完成发布者签名校验或软件安全验收。

Redis/Nginx 的 `.deb` 原始文件组成一个材料包，安装时提取到受管目录；不替换系统已有的 Redis/Nginx 软件包。Docker Engine 使用系统软件包安装，目标 Ubuntu 24.04 amd64 主机必须已有兼容版本的 `iptables`、`nftables`、`libseccomp2`、`libc6`、`libsystemd0`。这五个固定 Docker 包不是完整操作系统离线镜像；不会联网补装这些基础依赖。它们的预检、执行和回滚限制以安装计划为准。

## 定位与回滚

导入错误以 `MATERIAL_LIBRARY_`、`INSTALL_MATERIAL_QUOTA` 或 `INSTALL_TRANSFER_BUSY` 开头。材料状态与引用关系仍在现有材料列表；来源文件位于状态目录的 `installation-materials`。

停用自动导入可移走发布程序旁材料目录或取消显式目录参数；已导入材料仍可按既有删除入口清理，受计划、作业与受管实例引用保护。不要直接删除状态数据库或手动改写已有包。
