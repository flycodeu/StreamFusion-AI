export interface RuntimePolicy {
  restart: "no" | "on-failure" | "always" | "unless-stopped";
  autoStart: boolean;
  restartDelaySeconds?: number;
  stopTimeoutSeconds?: number;
  maxRetries?: number;
}

export interface RuntimePolicyState {
  configured?: RuntimePolicy;
  actual?: RuntimePolicy;
  held: boolean;
  observedAt: string;
  editable: boolean;
  reason?: string;
}

export type PolicyRuntime = "SYSTEMD" | "DOCKER";

export function runtimePolicyStopConfirmed(state: RuntimePolicyState | undefined, stopped: boolean, kind: PolicyRuntime) {
  return !!(state?.held && stopped && state.actual && !state.actual.autoStart &&
    (kind !== "DOCKER" || state.actual.restart === "no"));
}

export function defaultRuntimePolicy(kind: PolicyRuntime): RuntimePolicy {
  return kind === "DOCKER"
    ? { restart: "unless-stopped", autoStart: true, stopTimeoutSeconds: 10 }
    : { restart: "on-failure", autoStart: true, restartDelaySeconds: 5, stopTimeoutSeconds: 30 };
}

export function normalizeRuntimePolicy(value: RuntimePolicy, kind: PolicyRuntime): RuntimePolicy {
  if (kind === "DOCKER") return {
    restart: value.restart,
    autoStart: ["always", "unless-stopped"].includes(value.restart),
    ...(value.stopTimeoutSeconds ? { stopTimeoutSeconds: value.stopTimeoutSeconds } : {}),
    ...(value.restart === "on-failure" && value.maxRetries ? { maxRetries: value.maxRetries } : {}),
  };
  return {
    restart: value.restart === "unless-stopped" ? "on-failure" : value.restart,
    autoStart: value.autoStart,
    ...(value.restartDelaySeconds ? { restartDelaySeconds: value.restartDelaySeconds } : {}),
    ...(value.stopTimeoutSeconds ? { stopTimeoutSeconds: value.stopTimeoutSeconds } : {}),
  };
}

export function runtimePolicySummary(value?: RuntimePolicy, kind: PolicyRuntime = "SYSTEMD") {
  if (!value) return "保持当前配置";
  const restart = { no: "退出后不重启", "on-failure": "异常退出后重启", always: "退出后自动重启", "unless-stopped": "退出后自动重启" }[value.restart];
  const autoStart = kind === "DOCKER" ? ["always", "unless-stopped"].includes(value.restart) : value.autoStart;
  return `${autoStart ? "开机启动" : "不开机启动"} · ${restart}`;
}

export function runtimePolicyError(value: RuntimePolicy, kind: PolicyRuntime) {
  if (kind === "SYSTEMD" && value.restart === "unless-stopped") return "原生服务不支持所选恢复方式";
  if (!Number.isInteger(value.stopTimeoutSeconds) || !value.stopTimeoutSeconds || value.stopTimeoutSeconds < 1 || value.stopTimeoutSeconds > 600) return "停止等待时间须为 1 至 600 秒";
  if (kind === "SYSTEMD" && value.restartDelaySeconds !== undefined && (!Number.isFinite(value.restartDelaySeconds) || value.restartDelaySeconds < 0 || value.restartDelaySeconds > 300)) return "重启间隔须为 0 至 300 秒";
  if (kind === "DOCKER" && value.maxRetries !== undefined && (!Number.isInteger(value.maxRetries) || value.maxRetries < 0 || value.maxRetries > 100)) return "重试次数须为 0 至 100 次";
  return "";
}
