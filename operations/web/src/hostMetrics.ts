import type { ObservationAttempt } from "./observation";

export interface HostMetrics {
  code: string;
  observedAt: string;
  hostname?: string;
  os?: string;
  kernel?: string;
  arch?: string;
  bootTime?: string;
  cpuModel?: string;
  cpuCores?: number;
  cpuPercent?: number;
  uptimeSeconds?: number;
  memoryTotalBytes?: number;
  memoryAvailableBytes?: number;
  swapTotalBytes?: number;
  swapFreeBytes?: number;
  rootTotalBytes?: number;
  rootUsedBytes?: number;
  load?: number[];
  primaryInterface?: string;
  network?: {
    name: string;
    kind: string;
    addresses?: string[];
    receivedBytes: number;
    sentBytes: number;
    receiveBytesPerSecond?: number;
    sendBytesPerSecond?: number;
  }[];
  processCpuBasis?: string;
  processes?: {
    pid: number;
    user: string;
    cpuPercent: number;
    memoryBytes: number;
    name: string;
  }[];
}

export interface HostObservation {
  hostId: string;
  lastConfirmed?: HostMetrics;
  latestAttempt: ObservationAttempt;
  freshness: string;
  enabled: boolean;
}

export interface HostMetricSnapshot extends HostMetrics {
  hostId: string;
  latestAttempt: ObservationAttempt;
  freshness: string;
  enabled: boolean;
}

export const metricSnapshot = (
  observation: HostObservation,
): HostMetricSnapshot => ({
  ...(observation.lastConfirmed || {
    code: observation.latestAttempt.code || "NOT_CHECKED",
    observedAt: "",
  }),
  hostId: observation.hostId,
  latestAttempt: observation.latestAttempt,
  freshness: observation.freshness,
  enabled: observation.enabled,
});

export const metricsAvailable = (metrics?: HostMetrics) =>
  !!metrics && ["READY", "PARTIAL"].includes(metrics.code);
export const metricsLive = (metrics?: HostMetricSnapshot) =>
  metricsAvailable(metrics) &&
  metrics?.freshness === "FRESH" &&
  ["READY", "PARTIAL"].includes(metrics.latestAttempt.code || "") &&
  metrics.enabled !== false;

export function metricBytes(value?: number): string {
  if (value === undefined || !Number.isFinite(value) || value < 0) return "—";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${value.toFixed(unit ? 1 : 0)} ${units[unit]}`;
}

export function metricDuration(seconds?: number): string {
  if (seconds === undefined || !Number.isFinite(seconds) || seconds < 0)
    return "—";
  const hours = Math.floor((seconds % 86400) / 3600),
    minutes = Math.floor((seconds % 3600) / 60);
  return seconds >= 86400
    ? `${Math.floor(seconds / 86400)} 天 ${hours} 小时`
    : `${hours} 小时 ${minutes} 分钟`;
}

export const metricPercent = (used?: number, total?: number) =>
  used !== undefined && total !== undefined && used >= 0 && total > 0
    ? Math.max(0, Math.min(100, (used / total) * 100))
    : undefined;

const metricMessages: Record<string, string> = {
  PLATFORM_UNSUPPORTED: "当前系统暂不支持资源采集",
  PROBE_FAILED: "系统信息读取失败，请检查连接账号的读取权限",
  RESPONSE_INVALID: "系统信息读取不完整，请重新检测",
  SSH_TIMEOUT: "连接超时，请检查主机网络",
  SSH_UNAVAILABLE: "无法连接主机，请检查地址与 SSH 服务",
  SSH_AUTH_FAILED: "SSH 登录失败，请检查连接账号与凭据",
  SSH_HOST_KEY_CHANGED: "主机指纹已变化，请在连接信息中核对",
  CREDENTIAL_UNAVAILABLE: "连接凭据不可用，请检查连接配置",
};

export function metricNotice(metrics?: HostMetricSnapshot): string {
  if (metricsLive(metrics)) return "";
  if (metrics?.enabled === false) return "监测已暂停";
  const code = metrics?.latestAttempt.code;
  if (code && !["READY", "PARTIAL", "NOT_CHECKED"].includes(code))
    return metricMessages[code] || "当前无法读取主机信息，请检查连接与读取权限";
  return metricsAvailable(metrics)
    ? "上次采样 · 等待后台更新"
    : "尚无采样，等待后台检测";
}

export interface CpuSample {
  at: string;
  value: number;
}
export function appendCpuSample(
  samples: CpuSample[],
  metrics?: HostMetrics,
): CpuSample[] {
  const at = Date.parse(metrics?.observedAt || ""),
    value = metrics?.cpuPercent;
  if (
    !metricsAvailable(metrics) ||
    !Number.isFinite(at) ||
    value === undefined ||
    !Number.isFinite(value) ||
    value < 0 ||
    value > 100
  )
    return samples;
  if (samples.length && Date.parse(samples[samples.length - 1]!.at) >= at)
    return samples;
  return [...samples, { at: metrics!.observedAt, value }].slice(-60);
}
