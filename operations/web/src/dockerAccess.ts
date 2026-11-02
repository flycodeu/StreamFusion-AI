import type { SoftwareInstance, SoftwareJob } from "./software";

export type DockerAccessAction = "GRANT" | "REVOKE";
// These controller responses occur before a permission job is accepted.
export function dockerAccessRejected(code: string, status?: number) {
  return (
    !!status &&
    status >= 400 &&
    status < 500 &&
    [
      "VERSION_CONFLICT",
      "HOST_CHANGED",
      "HOST_MAINTENANCE",
      "OPERATION_LOCKED",
      "QUEUE_FULL",
      "DOCKER_ACCESS_NOT_MANAGED",
      "DOCKER_ACCESS_CONFIRMATION_REQUIRED",
      "DOCKER_ACCESS_STATE_CHANGED",
      "DOCKER_ACCESS_INPUT_INVALID",
      "DOCKER_ACCESS_ACCOUNT_INELIGIBLE",
      "DOCKER_ACCESS_PRIMARY_GROUP",
      "DOCKER_ACCESS_SOCKET_NOT_READY",
      "DOCKER_ACCESS_ALREADY_SET",
      "EXECUTOR_DOCKER_ACCESS_UNSUPPORTED",
    ].includes(code)
  );
}
export interface DockerAccount {
  name: string;
  uid: string;
  gid: string;
  shell: string;
  eligible: boolean;
  reason?: string;
  member: boolean;
  primaryGroup: boolean;
  groups: { name: string; gid: string }[];
}
export interface DockerAccessSnapshot {
  softwareId: string;
  expectedVersion: string;
  expectedHostVersion: string;
  sshAccount: string;
  lastJobId?: string;
  observation: {
    softwareId: string;
    engineIdentityHash: string;
    dockerGid: string;
    uidMin: number;
    uidMax: number;
    fingerprint: string;
    observedAt: string;
    socket: {
      path: string;
      uid: string;
      gid: string;
      mode: string;
      ready: boolean;
      errorCode?: string;
    };
    accounts: DockerAccount[];
  };
}
export interface DockerAccessJob extends SoftwareJob {
  kind?: string;
  result?: {
    dockerAccess?: {
      account: string;
      action: DockerAccessAction;
      member: boolean;
    };
  };
}
export function managedDockerAccess(item: SoftwareInstance) {
  return (
    item.managementMode === "MANAGED" &&
    item.dataOwnership === "OPS" &&
    item.runtimeKind === "SYSTEMD" &&
    item.locator === "docker.service" &&
    item.installationStatus === "READY" &&
    !!item.installation &&
    item.softwareType === "DOCKER" &&
    item.installation.definition?.key === "docker"
  );
}
export function dockerAccountAction(
  account: DockerAccount | undefined,
  action: DockerAccessAction,
  socketReady = true,
) {
  return (
    !!account?.eligible &&
    (action === "GRANT"
      ? !account.member && socketReady
      : account.member && !account.primaryGroup)
  );
}
