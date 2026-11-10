export interface GitProfile {
  id: string;
  name: string;
  authMethod?: "HTTPS" | "SSH";
  username?: string;
  configuredVersion?: string;
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
  keySource?: "OPS" | "TARGET";
  hostId?: string;
  systemAccount?: string;
  accountUid?: string;
  keyId?: string;
  keyPath?: string;
  publicKey?: string;
}
export type GitProfileDraft = Pick<GitProfile, "name" | "sshHost" | "sshPort" | "sshUser" | "authorName" | "authorEmail" | "credentialId" | "credentialRevision" | "hostPublicKey" | "keySource" | "hostId" | "systemAccount" | "accountUid" | "keyId" | "keyPath" | "publicKey" | "authMethod" | "username"> & { hostKeyConfirmed: boolean; keyFingerprint?: string };
export interface GitKeyEvidence {
  id: string;
  account: string;
  accountUid: string;
  keyPath: string;
  publicKey: string;
  fingerprint: string;
  hostPublicKey?: string;
  hostFingerprint?: string;
}
export function newGitProfile(): GitProfileDraft {
  return { name: "", authMethod:"HTTPS",username:"",sshHost: "", sshPort: 443, sshUser: "git", authorName: "", authorEmail: "", credentialId: "", credentialRevision: "", hostPublicKey: "", hostKeyConfirmed: false, keySource: "TARGET", hostId: "", systemAccount: "", accountUid: "", keyId: "", keyPath: "", publicKey: "" };
}
export function gitProfileError(draft: GitProfileDraft): string {
  if (!draft.name.trim() || !draft.sshHost.trim() || !draft.sshUser.trim()) return "请填写账号名称和 Git 服务器地址";
  if (!Number.isInteger(draft.sshPort) || draft.sshPort < 1 || draft.sshPort > 65535) return "端口须为 1–65535";
  if (draft.authorEmail.trim() && !/^[^\s@]+@[^\s@]+$/.test(draft.authorEmail.trim())) return "请填写有效邮箱，或留空";
  if (!draft.systemAccount || draft.systemAccount === "root") return "请选择目标服务器的普通系统账号";
  if (draft.authMethod === "HTTPS") {
    if (!draft.username?.trim()) return "请填写 Git 登录用户名";
  } else if (draft.keySource === "TARGET") {
    if (!draft.systemAccount || draft.systemAccount === "root") return "请选择目标服务器的普通系统账号";
    if (!draft.publicKey || !draft.keyPath || !draft.keyId) return "请先在目标服务器生成密钥";
  } else return "请在目标服务器生成账号密钥";
  if (draft.hostPublicKey.trim() && !draft.hostKeyConfirmed) return "请核对并确认 Git 服务器主机公钥，或留空稍后核验";
  return "";
}

export function gitAccountRemote(profile: GitProfile, repository: string): string {
  const input = repository.trim();
  if (!input) return "";
  // Accept the usual GitHub/GitLab clone links, including nested namespaces.
  const scp = /^([\w.-]+)@([\w.-]+):([\w./-]+)$/.exec(input);
  const source = new URL(scp ? `ssh://${scp[1]}@${scp[2]}/${scp[3]}` : input);
  const protocol = profile.authMethod === "HTTPS" ? "https:" : "ssh:";
  if (
    !["https:", "ssh:"].includes(source.protocol) ||
    source.hostname.toLowerCase() !== profile.sshHost.toLowerCase() ||
    source.password || source.search || source.hash ||
    !/^\/[\w.-]+(?:\/[\w.-]+)+$/.test(source.pathname) ||
    /(?:^|\/)(?:\.|\.\.)(?:\/|$)/.test(input) ||
    (source.protocol === protocol && source.port && Number(source.port) !== profile.sshPort)
  ) throw new Error("仓库地址须属于当前账号的 Git 服务，请核对地址与端口");
  if (protocol === "ssh:") return `${profile.sshUser}@ops-git-${profile.id}:${source.pathname.slice(1)}`;
  const port = profile.sshPort === 443 ? "" : `:${profile.sshPort}`;
  return `https://${profile.username}@${profile.sshHost}${port}${source.pathname}`;
}
