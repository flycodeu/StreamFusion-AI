export interface HostCapability {
  status: "AVAILABLE" | "UNAVAILABLE" | "UNKNOWN";
  code: string;
  message: string;
  observedAt: string;
  source: string;
}

export type HostCapabilityKey =
  "reachability" | "readOnly" | "mutation" | "docker";

export interface Host {
  id: string;
  name: string;
  kind: "LOCAL" | "SSH";
  address: string;
  sshPort: number;
  sshUser: string;
  hostFingerprint: string;
  credentialId: string;
  credentialRevision: string;
  installRoot: string;
  version: string;
  maintenance: boolean;
  observedAt: string;
  capabilities?: Record<HostCapabilityKey, HostCapability> & {
    dockerCompose?: HostCapability;
    expiresAt: string;
    platform?: string;
  };
}

export interface SSHCredential {
  id: string;
  revision: string;
  name: string;
  kind: string;
  revokedAt: string;
}

export interface HostFingerprint {
  fingerprintToken: string;
  hostFingerprint: string;
  address: string;
  sshPort: number;
  expiresAt: string;
}

export const hostCapabilityLabels: Record<HostCapabilityKey, string> = {
  reachability: "SSH 连通",
  readOnly: "只读观察",
  mutation: "变更权限",
  docker: "Docker 能力",
};

export function hostTime(value?: string): string {
  return value
    ? new Date(value).toLocaleString("zh-CN", {
        timeZone: "Asia/Shanghai",
        hour12: false,
      })
    : "—";
}

export function hostObservationExpired(host: Host, now: number): boolean {
  const expiry = Date.parse(host.capabilities?.expiresAt || "");
  return !Number.isFinite(expiry) || expiry <= now;
}
