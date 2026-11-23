# Camera Inspector

独立相机巡检服务提供 HTTP 控制、MySQL 作业交换及证据上传协议。媒体读取与上传目前通过隔离实验调用，自动领取和画面检测尚未接入。状态返回 `P0_CONTRACT / NOT_VERIFIED`，默认 START 等待执行资源。

## 安装与启动

需要 Python 3.12。先按 [SQL 指南](../platform-api/sql/README.md) 用完整初始化文件准备专用空库。让服务连接平台使用的巡检交换表，数据库账号只授予交换所需权限。

Windows PowerShell 在本目录执行。复制配置前可用 `Test-Path .env.local` 确认尚未创建。

```powershell
py -3.12 -m venv .venv
.venv\Scripts\python.exe -m pip install --require-hashes -r requirements.txt
.venv\Scripts\python.exe -m pip install --no-deps .
Copy-Item .env.example .env.local
```

编辑 [.env.example](.env.example) 对应的 `.env.local`，填写节点身份、机器令牌、数据库连接及 TLS 配置。随后使用 `.venv\Scripts\python.exe -m camera_inspector` 启动。Linux 可用 `python3.12 -m venv .venv` 创建环境，用 `.venv/bin/python` 执行同样的 pip 与启动命令。

默认监听 `0.0.0.0:8120`，需要 TLS 或明确配置的可信反向代理。仅本机实验可使用字面地址 `127.0.0.1` 和 `--allow-loopback-http`。数据库明文连接同样只允许显式开启的字面回环地址。

## 控制接口

带上 `Authorization: Bearer <机器令牌>` 调用。

| 接口 | 用途 |
| --- | --- |
| `GET /inspection/v1/status` | 获取节点身份、`bootId` 和能力状态 |
| `POST /inspection/v1/control` | 通知 START 或 STOP。服务先核对数据库中的控制代次和启动身份 |

平台先持久化控制记录，再发 HTTP。请求包含 `contractVersion`、`requestId`、`controlId`、`controlSeq`、`expectedBootId`、`action`、`traceId`。HTTP 202 表示接纳控制，实际执行以状态和数据库记录为准。节点重启后，平台按新的 `bootId` 发起新代次。

七张交换表使用 UTC `DATETIME(6)`。领取及结果回写可按原请求幂等重试，释放预算需确认资源已清理。MySQL 表与列授权没有按节点划分行权限，节点须处于同一受信运维域。

## 验证

在本目录运行：

```powershell
uv sync --locked --python 3.12
uv run --locked pytest -q
uv run --locked ruff check src tests
uv run --locked mypy
```

- [隔离 MySQL](scripts/test-isolated-mysql.ps1) 使用独立目录和端口运行。可用 `-MysqldPath` 指定 `mysqld.exe`。普通 pytest 缺少隔离配置时跳过真实数据库用例。
- [媒体实验](scripts/verify-media.py) 需本机 FFmpeg、ffprobe 和 ZLMediaKit。运行 `uv run --locked python scripts/verify-media.py --zlm "<MediaServer 路径>"`。
- [图床实验](scripts/verify-storage.py) 使用 `--weed "<SeaweedFS 路径>"` 启动隔离实例。Java 平台预签名集成入口为 [verify-platform-presign.py](scripts/verify-platform-presign.py)。

实验日志和证据保存在本工程的 `.run/`。用 `traceId`、`controlId`、`nodeId` 和 `runId` 对齐控制、媒体和证据记录。
