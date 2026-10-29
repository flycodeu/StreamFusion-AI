# Camera Inspector

独立相机巡检项目，包含 **P0-F01 控制与数据库契约**、**P0-F02 媒体实验执行器**和 **P0-F03 单对象证据上传**。平台通过 HTTP 通知 START/STOP；节点核对数据库中的控制代次、启动身份和期望状态，再评估资源。任务领取、租约、逐项结果及预算在 MySQL 短事务内处理。

媒体执行器和存储上传器目前由实验脚本调用，尚未接入生产自动领取与检测判定。平台已提供单对象 `authorize/complete` 和按当前相机权限读取证据的接口；节点上传器仍需由后续执行流程调用。状态接口继续返回 `P0_CONTRACT / NOT_VERIFIED`，检测能力为空，正常启动请求得到 `WAITING_RESOURCE / EXECUTOR_NOT_IMPLEMENTED`。隔离测试不会生成真实相机“正常”结论。物理可写容量为 `LIMITED`，自动选仓、多图床切换和画面检测尚未接入。

## 安装与启动

使用Python 3.12。项目独立环境，不导入或安装现有algorithm-node。安装与启动分开，启动不建表、不迁移数据库、不安装依赖。`.env.local`、相对配置路径和默认`.run`均以启动目录为准。

Windows PowerShell，在本目录执行：

```powershell
py -3.12 -m venv .venv
.venv\Scripts\python.exe -m pip install --require-hashes -r requirements.txt
.venv\Scripts\python.exe -m pip install --no-deps .
Copy-Item .env.example .env.local
# 编辑 .env.local，填写节点身份、受限数据库账号和TLS配置。
.venv\Scripts\python.exe -m camera_inspector
```

Linux，在本目录执行：

```bash
python3.12 -m venv .venv
.venv/bin/python -m pip install --require-hashes -r requirements.txt
.venv/bin/python -m pip install --no-deps .
cp .env.example .env.local
# 编辑 .env.local。
.venv/bin/python -m camera_inspector
```

这些是启动方式；Linux实机支持仍须单独验证。默认监听 `0.0.0.0:8120`，需要TLS证书或显式受信反向代理配置；不提供匿名控制。临时本机实验可设置HOST为字面地址127.0.0.1，并使用 `python -m camera_inspector --allow-loopback-http`；数据库明文连接也只允许显式开启的字面loopback地址。

机器令牌与数据库密码仅通过受限环境/本地配置提供，不放请求参数、日志或源码。接口使用 `Authorization: Bearer <节点机器令牌>`。数据库只授予巡检交换表所需权限；普通MySQL授权不提供按nodeId的行隔离，节点须属于同一受信运维域。

## 控制接口

| 接口 | 行为 |
|---|---|
| GET /inspection/v1/status | 返回契约/服务版本、nodeId、当前bootId及契约验证模式，不表示具备检测能力 |
| POST /inspection/v1/control | 验证身份、请求大小/时限及版本，再核对DB中的有效控制记录 |

```json
{
  "contractVersion": "1.0",
  "requestId": "request-1",
  "controlId": "control-1",
  "controlSeq": 1,
  "expectedBootId": "boot-1",
  "action": "START",
  "traceId": "trace-1"
}
```

示例中的expectedBootId须替换为状态接口返回的当前bootId，其他ID对应实际DB控制记录。平台必须先保存控制，再发送HTTP；该接口不创建计划或接受任务列表。202只代表控制已接纳，实际状态以响应/数据库为准。STOP先在DB更新期望状态并递增代次，HTTP用于加快响应；旧START、旧boot以及迟到资源评估不能覆盖新状态。重启需要平台针对新bootId发送新代次START。

同代次START在PREPARING/WAITING_RESOURCE时可重新评估，READY重放不重置槽位。全节点最多保留一个START流程，相同控制共享处理，其他START返回CONTROL_BUSY；客户端取消不撤销已受理流程。STOP使用独立调用通道，仍遵守数据库短事务锁。

## 数据库边界

七份逐表草案集中在 `platform-api/sql/业务/巡检P0/`：控制、预算、作业、运行、结果、领取回执及证据位置。回执也记录空领取，避免同一请求在重试时领取另一任务。证据位置引用现有图床实例与范围表。此目录只供P0隔离试验，不进入现行新库汇总，也不是业务库升级脚本。

交换表使用UTC DATETIME(6)，连接显式使用UTC；平台现有业务时间不受影响。Python运行时不执行DDL。七表的主键、外键和受限列权限已通过隔离验证；正式Java发布/消费、旧计划迁移和生产账号部署随后续工作卡完成。

- 领取先锁控制，再按持久游标有界读取候选，随后锁排序后的预算和作业；锁后重新校验范围、快照和时间。受限来源不会长期挡住后续候选，末页回绕，重放不推进游标。
- 同一领取请求返回原运行或原空结果；结果按运行/代次/检测项幂等，相同摘要重放保留首次数据库提交时间。
- 租约不超过控制期限、作业硬期限和单次上限。已观察到的控制期限过期后必须新代次START；过期不等于资源退出，未确认释放保持CLEANUP_PENDING。
- 旧运行清理只释放自己的预约；正常、疑似、未知与执行/证据状态分开。
- WAITING_RESOURCE只暂停新增领取，已占预算的运行可继续启动、续租及回写；STOP、授权代次或期限失效仍撤销运行授权。释放不自动增加准入槽位，需重新实测资源。

## 验证

开发环境安装：`uv sync --locked --python 3.12`。

```powershell
uv run --locked python -m pytest -q
uv run --locked ruff check src tests
uv run --locked mypy
./scripts/test-isolated-mysql.ps1
```

默认pytest没有显式隔离MySQL配置时跳过数据库测试。最后一条脚本使用独立datadir、端口和测试库运行真实MySQL测试，拒绝把默认业务库当测试库；完成后仅关闭本次启动实例，保留日志/JUnit证据。不以Mock或跳过结果代替数据库并发验证。

F02 媒体测试另需 FFmpeg/ffprobe。Windows 媒体进程使用原子绑定的 Job Object，协调进程异常退出也能由 OS 回收后代；不安装新的 Python 媒体依赖。完整媒体实验还需已有 ZLMediaKit 可执行文件：

```powershell
uv run --locked python scripts/verify-media.py --zlm '<MediaServer.exe绝对路径>'
```

脚本启动独立端口和配置的 ZLM、合成视频源，验证完成后关闭本次创建的进程；证据保存在工作区 `.run/p0-f02/media-*`。`--smoke` 只验证源的建立与读取，不代表完整矩阵通过。合成画面通过实际 RTSP 链路执行，不代表现场摄像机或千路容量验收。

F03 图床实验使用本机已有 SeaweedFS 4.48，但每次创建新的临时 HTTPS 实例、测试账号、TLS CA、端口和受管前缀，不读取既有图床凭据。上传器只消费单对象短期授权；URL 和签名头通过受监督子进程的有界输入传递，不跟随重定向、不自动重试。请求开始后的结果不明保留 `UNKNOWN`，本地 journal 不保存 URL 或密钥，远端和平台索引确认前不删除本地文件。

```powershell
$env:SF_INSPECTOR_STORAGE_EVIDENCE = '<verify-storage.py 输出的绝对证据目录>'
uv run --locked pytest -q -m storage
```

跨语言预签名与平台写入探针可用 `scripts/verify-platform-presign.py` 在新建的隔离 SeaweedFS HTTPS 实例验证。运行前先用 Java 21 编译 `platform-api`；脚本调用 Maven Wrapper 执行平台受控写探针测试，需要本机 SeaweedFS 可执行文件，不读取生产图床配置。物理容量和多图床自动切换仍未验证。

媒体内部入口为 `MediaSession(MediaSpec(...))`，在独立线程运行 `run(stop_event)`，消费者通过 `poll_sample()` 取有界采样，`trigger()` 固定原异常样本及目标 30/60 秒片段。一次会话只有一个 FFmpeg 输入，原流段与采样共享读取；截图只编码已经捕获的 RGB。`COMPLETED` 只表示执行窗口结束，不能解释为画面正常。

执行期限使用本机单调时钟，调用方必须从数据库授权余量扣除 `grace_seconds + kill_seconds + reader_join_seconds` 后设置 `monotonic_deadline`；断开 HTTP/DB 不得自行延长期限。只有 `cleanup_confirmed=true` 才能确认本地进程及管道退出；真实平台媒体租约仍须由后续消费者独立确认，不能仅凭 FFmpeg 结束释放共享上游。回写失败继续保留数据库预算，恢复后幂等补写。

缓存字节是停止水位，报告实际峰值和收尾超量；精确硬盘限额需部署配额。采样队列满会丢弃新样本并记录本地过载，不能据此判相机故障。报告包括执行层帧数、PTS、观察时间、退出码、脱敏 stderr、实际片段范围和缺口；不支持的 RTP/源端丢包指标为 null。Linux 媒体运行需要显式外部 systemd/cgroup 监督，不能用进程组单独承诺父进程被杀后的回收；本轮 Linux 实机验证未完成。

完整HTTP/DB链使用只获交换表必要列权限的临时Worker账号；发布、建表和授权由隔离测试管理员完成。测试覆盖禁止修改平台控制、预算上限、作业快照以及删除/DDL/非交换表访问。测试中的接纳判断只说明逐项契约，正式平台结果消费者仍待后续接入。

若mysqld不在PATH，脚本传 `-MysqldPath '<mysqld.exe绝对路径>'`。独立测试脚本仅验证Windows MySQL 8；不读取机器MySQL配置或复用现有实例。

更新依赖先修改pyproject.toml，再运行 `uv lock`；服务器清单由 `uv export --locked --no-dev --no-emit-project --format requirements-txt --output-file requirements.txt` 生成，不手工维护第二套版本。

定位顺序：traceId/controlId/seq → nodeId/bootId → jobId/runId/epoch。启动问题先核对必填配置与TLS，控制冲突核对当前DB代次，资源等待核对能力状态，预算未释放核对运行清理事实。日志默认在本项目.run中滚动保存。

P0 尚未完成画面检测与 G0 验收。回退时停止独立服务与本次实验进程；本项目未迁移业务库，不删除业务数据、结果或未知资源责任来“恢复空闲”。
