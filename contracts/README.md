# 公共协议与 API 约定

这里维护可公开的 JSON Schema 和合成样例，与实现语言无关。
当前四份 schemaVersion=1.0 的协议是设计基线，不代表对应接口已实现。
本次仅纳入版本控制及自动校验，未改变协议语义。

| Schema / 同名 example | 边界 |
|---|---|
| task-spec | 任务运行描述 |
| runtime-event | Runtime 产生的事件 |
| agent-sync-request | Agent 发往平台的同步请求 |
| agent-sync-response | 平台返回 Agent 的期望状态 |

`schemas/` 存 Draft 2020-12 Schema，`examples/` 存可校验的合成 JSON。
Schema 的 example.invalid 标识不是运行时地址；测试不从网络加载 schema。
不要放入真实相机地址、密钥、个人路径或现场数据。

根目录执行：

```powershell
uv run --project algorithm-node/node-agent --locked python -m pytest contracts/tests -q
```

测试校验样例、版本、必填字段、未知字段及顶层日期格式；
不是完整协议互操作测试，也不证明任务同步已运行。
公共字段的删除、改名、类型或状态语义变化必须先讨论版本与兼容策略，再同步 schema、样例和消费者测试。
不要只改某一端；禁止为了通过测试放宽协议约束。

## HTTP 基础约定

Platform管理端 `/api/v1` 的普通JSON成功与失败统一使用 `R<T>`：

```json
{
  "code": "SUCCESS",
  "msg": "操作成功",
  "data": null,
  "traceId": "0123456789abcdef0123456789abcdef",
  "timestamp": "2026-09-16T00:00:00Z"
}
```

成功业务数据放data；无数据也保留null。错误保留真实4xx/5xx，使用字符串错误码与安全msg，不能HTTP200包装失败。创建使用201并保留Location，其他普通业务成功使用200。当前只实现公共封装与拒绝出口，登录及CRUD尚未开放。

业务异常通过 `throw BusinessException.error(ErrorCode.CONFLICT)` 交统一异常处理；Security/CSRF和Servlet错误分派采用相同外壳，CSRF拒绝为403/CSRF_INVALID。业务响应设置no-store，响应头X-Trace-Id与正文一致。

Actuator健康与OpenAPI成功响应保持原样。非业务底座错误及Python接口仍使用以下既有结构；TaskSpec/Event/Agent Sync不改为R。

```json
{
  "code": "NOT_FOUND",
  "message": "Request rejected",
  "data": null,
  "traceId": "0123456789abcdef0123456789abcdef",
  "timestamp": "2026-09-16T00:00:00Z"
}
```

错误保持真实 HTTP 状态，不用 HTTP 200 表达失败。
客户端依赖 code 和 HTTP status，不解析 message/msg 文本；两类消息字段均不暴露原始输入、堆栈和内部异常。
响应头 X-Trace-Id 与错误体一致。UTC 时间使用带 Z 的 ISO 8601 / RFC 3339，保留可变小数精度。

| HTTP | code | 说明 |
|---|---|---|
| 400 / 422 | VALIDATION_ERROR | Java 请求绑定常用 400，FastAPI 参数验证为 422 |
| 404 | NOT_FOUND | 路由不存在 |
| 405 | METHOD_NOT_ALLOWED | 方法不支持，保留 Allow 头 |
| 500 | INTERNAL_ERROR | 未处理异常 |

Java 还预留 401 UNAUTHORIZED、403 FORBIDDEN、409 CONFLICT、415 UNSUPPORTED_MEDIA_TYPE、
429 RATE_LIMITED 等状态映射；这不表示已实现登录、权限或限流。
Python 非 404/405 的显式 HTTPException 当前返回 HTTP_ERROR，
后续新增这些业务错误时需要同步映射与测试。
容器或代理在应用之前拒绝的请求不保证返回这个错误外壳。

未来分页查询统一 page 从 1 开始、size 默认 20 / 最大 100；
尚无列表接口，暂不创建分页基类或响应对象；第一个列表接口落地时实施校验与默认值。
引入游标分页时另行定义。

## 在线接口描述

- Java：/v3/api-docs，prod 默认关闭；只提供 OpenAPI JSON，不包含 Swagger UI。
- Agent / Runtime：/openapi.json 与 /docs，DOCS_ENABLED=false 可关闭。
- 尚无业务端点；协议 Schema 不是已经上线的 HTTP 路由。
