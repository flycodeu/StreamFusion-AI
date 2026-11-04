import type { SoftwareJob } from "./software";

export interface GitProfile {
  id: string;
  name: string;
  sshHost: string;
  sshPort: number;
  sshUser: string;
  authorName: string;
  authorEmail: string;
  credentialId: string;
  credentialRevision: string;
  hostPublicKey: string;
  keyFingerprint: string;
  version: string;
  createdAt: string;
  updatedAt: string;
}
export type GitProfileDraft = Pick<GitProfile, "name" | "sshHost" | "sshPort" | "sshUser" | "authorName" | "authorEmail" | "credentialId" | "credentialRevision" | "hostPublicKey"> & { hostKeyConfirmed: boolean };
export interface GitObservation {
  account: string;
  accountUid: string;
  repoPath: string;
  remoteUrl: string;
  branch: string;
  gitVersion: string;
  keyFingerprint: string;
  hostKeyFingerprint?: string;
  keyPath?: string;
  authorName?: string;
  authorEmail?: string;
  status: string;
  observedAt: string;
}
export interface GitBinding {
  id: string;
  hostId: string;
  account: string;
  accountUid: string;
  repoPath: string;
  profileId: string;
  profileName: string;
  version: string;
  status: string;
  lastJobId: string;
  lastJobStatus?: string;
  lastJobAckPending?: boolean;
  observation?: GitObservation;
  errorCode: string;
}
export interface GitPlan {
  id: string;
  hostId: string;
  action: "BIND" | "UNBIND";
  change: { account: string; repoPath: string; authorName: string; authorEmail: string; keyFingerprint: string; hostKeyFingerprint?: string; keyPath?: string };
  observation: GitObservation;
  expiresAt: string;
}
export interface GitReceipt { jobId: string; bindingId: string }
export interface GitConnectionResult { ready: boolean; code: string; message: string; observation?: GitObservation }
export type GitJob = SoftwareJob;
export function newGitProfile(): GitProfileDraft {
  return { name: "", sshHost: "", sshPort: 22, sshUser: "git", authorName: "", authorEmail: "", credentialId: "", credentialRevision: "", hostPublicKey: "", hostKeyConfirmed: false };
}
export function gitProfileError(draft: GitProfileDraft): string {
  if (!draft.name.trim() || !draft.sshHost.trim() || !draft.sshUser.trim()) return "请填写账号名称和 Git 服务器地址";
  if (!Number.isInteger(draft.sshPort) || draft.sshPort < 1 || draft.sshPort > 65535) return "SSH 端口须为 1–65535";
  if (!draft.authorName.trim() || !/^[^\s@]+@[^\s@]+$/.test(draft.authorEmail.trim())) return "请填写提交姓名和有效邮箱";
  if (!draft.credentialId || !draft.credentialRevision) return "请选择 SSH 密钥凭据";
  if (!draft.hostPublicKey.trim() || !draft.hostKeyConfirmed) return "请核对并确认 Git 服务器主机公钥";
  return "";
}
export function gitBindingStatus(status: string): string {
  return ({ BOUND: "已绑定", READY: "已绑定", ACTIVE: "已绑定", RELEASED: "已解除", UNBOUND: "已解除", REMOVED: "已解除", PENDING: "等待执行", APPLYING: "正在配置", UNKNOWN: "结果待核对", FAILED: "配置未完成" } as Record<string, string>)[status] || "待检查";
}
export function gitJobPending(job?: GitJob): boolean {
  return !!job && (["QUEUED", "RUNNING", "UNKNOWN"].includes(job.status) || job.targetAckPending);
}
export function gitSubmitRejected(code: string, status?: number): boolean {
  return !!status && status >= 400 && status < 500 && ["GIT_PLAN_STALE", "GIT_CONFIRMATION_REQUIRED", "GIT_PROFILE_CHANGED", "GIT_BINDING_CHANGED", "GIT_CREDENTIAL_UNAVAILABLE", "HOST_CHANGED", "OPERATION_LOCKED", "QUEUE_FULL", "IDEMPOTENCY_CONFLICT", "INPUT_INVALID", "REVISION_CONFLICT"].includes(code);
}
