import {
  encryptSensitiveRequest,
  isSensitiveRequest,
  requireRequestCrypto,
  type RequestChallenge,
} from "./requestCipher";

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
  signal?: AbortSignal,
): Promise<T> {
  const response = await send(path, method, data, signal);
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
export async function send(
  path: string,
  method: string,
  data?: unknown,
  signal?: AbortSignal,
) {
  method = method.toUpperCase();
  const timeout = AbortSignal.timeout(
    /\/preparation\/(install|repair)$/.test(path)
      ? 210000
      : /^\/installation-materials\/[^/]+\/confirm$/.test(path) ||
      path === "/installation-materials/official"
      ? 900000
      : path.endsWith("/recover")
        ? 300000
        : 60000,
  );
  const requestSignal = signal ? AbortSignal.any([signal, timeout]) : timeout;
  if (isSensitiveRequest(path, method)) {
    requireRequestCrypto();
    const challenge = await request<RequestChallenge>(
      "/auth/secure-challenge",
      "POST",
      { path },
      requestSignal,
    );
    data = await encryptSensitiveRequest(challenge, path, data);
  }
  requestSignal.throwIfAborted();
  return fetch("/ops/api/v1" + path, {
    method,
    cache: "no-store",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
    body: data === undefined ? undefined : JSON.stringify(data),
    signal: requestSignal,
  });
}
export async function upload(path: string, file: File, signal?: AbortSignal) {
  const timeout = AbortSignal.timeout(
    path.startsWith("/installation-materials/") ? 900000 : 180000,
  );
  const response = await fetch("/ops/api/v1" + path, {
    method: "PUT",
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/octet-stream",
      "X-CSRF-Token": csrf,
    },
    body: file,
    signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
  });
  const body = await response.json();
  if (!response.ok)
    throw new ApiError(body.msg, body.code, body.traceId, body.timestamp);
}
export function setCSRF(token: string) {
  csrf = token;
}
export async function prepare() {
  const data = await request<{ csrfToken: string }>("/auth/csrf");
  csrf = data.csrfToken;
}
