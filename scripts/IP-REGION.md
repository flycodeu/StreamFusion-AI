# 离线 IP 地域数据

在仓库根目录手动执行：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\install-ip-region.ps1
```

脚本将两个 XDB 文件及原始 `LICENSE.md` 下载到 `platform-api/.run/ip-region/`。该目录只在本地保留，不提交二进制数据。应用启动和查询时不会调用下载脚本，也不会向外部服务发送用户 IP。需要其他路径时传入 `-OutputDirectory`，并在后端配置对应的本地数据库文件路径。

## 固定来源与许可

- 官方仓库：[lionsoul2014/ip2region](https://github.com/lionsoul2014/ip2region)。
- 固定版本：[`v3.18.0`](https://github.com/lionsoul2014/ip2region/tree/v3.18.0)，解析到提交 `c1a1fc7d5941760db3f8431dc05c48cf7f0e30a1`。下载使用提交路径，避免标签后续变化影响结果。
- 原始数据说明：[data/README_zh.md](https://github.com/lionsoul2014/ip2region/blob/c1a1fc7d5941760db3f8431dc05c48cf7f0e30a1/data/README_zh.md)。
- 官方 [`LICENSE.md`](https://github.com/lionsoul2014/ip2region/blob/c1a1fc7d5941760db3f8431dc05c48cf7f0e30a1/LICENSE.md) 声明 `Apache-2.0 OR MIT`；完整原文随数据库一并下载保留。分发这些文件时保留许可与原作者声明。

固定长度及 SHA256 由 [`ip-region.manifest.json`](ip-region.manifest.json) 维护：

| 文件               |     字节数 | SHA256                                                             |
| ------------------ | ---------: | ------------------------------------------------------------------ |
| `ip2region_v4.xdb` | 11,122,036 | `8e31bbdccb5bf21028af10592d4312ec975da0bffa108c0c5d862a12190f9ad3` |
| `ip2region_v6.xdb` | 37,277,813 | `939f6b46bd2b8bec3cf7c5ceb8ba782266ae9b1f35b5ba7916700dec0b7506ed` |
| `LICENSE.md`       |     12,976 | `fe01f2f8fcaafac539154e6aa80b0b7f8af54e01dc4d52322f72971991c6280e` |

两个数据库合计 48,399,849 字节，约 46.16 MiB，低于 128 MiB 的数据库总大小限制。此数值表示数据库内容大小，不是 JVM 进程总内存上限。地域查询结果取决于固定数据版本，不能作为用户实时位置的证明。

## 下载与文件保护

- 每个文件默认最多下载 90 秒，可用 `-TimeoutSeconds` 调整为 5～300 秒；不自动无限重试。
- 流式下载使用 64 KiB 缓冲区，超出固定文件大小立即终止。所有文件在临时路径校验长度、SHA256，XDB 另核对版本和 IP 家族后才移动到最终文件名。
- 已有文件与清单完全匹配时跳过下载。任意已有同名文件不匹配时直接终止并保留，不提供强制覆盖参数；保留自有数据库时为官方样例选择另一个目录。
- 超时、哈希不符或不完整下载只清理本次生成的临时文件。不会启动、停止或重启任何应用。

如需升级，先核对官方版本、许可、两个 XDB 文件和后端读取兼容性，再更新清单的提交、文件大小与 SHA256。重新下载到新目录核验后调整部署配置，保留旧目录即可回退。
