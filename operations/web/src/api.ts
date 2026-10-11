export class ApiError extends Error {
  constructor(
    message: string,
    readonly code: string,
    readonly traceId: string,
    readonly timestamp: string,
  ) {
    super(message);
  }
}
let csrf = "";
export async function request<T>(
  path: string,
  method = "GET",
  data?: unknown,
): Promise<T> {
  const response = await send(path, method, data);
  const body = await response.json().catch(() => null);
  if (!body) throw new Error("控制器未返回有效响应，请检查连接");
  if (!response.ok)
    throw new ApiError(
      body.msg || "请求失败",
      body.code,
      body.traceId,
      body.timestamp,
    );
  return body.data as T;
}
export function send(path: string, method: string, data?: unknown) {
  return fetch("/ops/api/v1" + path, {
    method,
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
    body: data === undefined ? undefined : JSON.stringify(data),
    signal: AbortSignal.timeout(60000),
  });
}
export function setCSRF(token: string) {
  csrf = token;
}
export async function prepare() {
  const data = await request<{ csrfToken: string }>("/auth/csrf");
  csrf = data.csrfToken;
}
