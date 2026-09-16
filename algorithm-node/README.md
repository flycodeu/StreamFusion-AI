# Algorithm Node

这是第三个顶层项目，包含两个独立 Python 3.12 环境和进程：

- `node-agent/`：未来负责平台同步和 Worker 监督，默认本地端口 8100。
- `runtime/`：未来负责视频与算法执行，默认本地端口 8101。

两者目前仅提供 `/health` 和 FastAPI `/docs`。`mode=skeleton` 仅代表空壳进程可用，不代表推理就绪。
各自维护 `pyproject.toml`、`uv.lock` 和 `.venv`，不互相导入，也不共用依赖环境。
没有注册、心跳、任务监督、GPU、视频或模型逻辑。Windows 只验证空壳；正式媒体运行目标为 Ubuntu 24.04。

在各自目录运行 `uv sync --locked` 安装，再用以下命令启动：

```powershell
# node-agent 目录
uv run --locked python -m app
# runtime 目录（另一个终端）
uv run --locked python -m app
```

各目录执行 `uv run --locked python -m pytest` 可测试健康接口、配置校验、错误响应与日志脱敏。Ctrl+C 停止服务。

复制各目录的 `.env.example` 为 `.env.local` 后修改本地配置（不要覆盖已有文件）。
Agent 使用 SF_AGENT_*，Runtime 使用 SF_RUNTIME_*；启动时校验配置。
完整配置、日志、质量检查说明见 [开发指南](../DEVELOPMENT.md)。
