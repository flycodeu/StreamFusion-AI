<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { ApiError, request } from "./api";
import { hostTime, type Host, type SSHCredential } from "./hosts";
import HostPreparationPanel from "./HostPreparationPanel.vue";
import { automaticPreparationUpdate, type AutomaticPreparationUpdate, type PreparationUpdateCheck } from "./preparationUpdate";
import { softwareJobPhaseLabel, softwareJobStatusLabel } from "./software";
import { gitBindingStatus, gitJobPending, gitProfileError, gitSubmitRejected, newGitProfile, type GitBinding, type GitConnectionResult, type GitJob, type GitPlan, type GitProfile, type GitProfileDraft, type GitReceipt } from "./gitManagement";

const props = withDefaults(defineProps<{ host: Host; active?: boolean; section?: "accounts" | "repositories"; showTabs?: boolean }>(), { active: true, showTabs: true });
const emit = defineEmits<{ changed: []; closeState: [{ blocked: boolean; dirty: boolean }] }>();
const tab = ref<"accounts" | "repositories">(props.section || "repositories");
const profiles = ref<GitProfile[]>([]);
const bindings = ref<GitBinding[]>([]);
const credentials = ref<SSHCredential[]>([]);
const localHost = ref<Host>();
const busy = ref(false);
const error = ref<{ message: string; code: string; traceId: string; at: string }>();
const profileDraft = ref<GitProfileDraft>();
const editingProfile = ref<GitProfile>();
const deletingProfile = ref("");
const candidateFingerprint = ref("");
const repoDraft = ref<{ account: string; repoPath: string; profileId: string }>();
const editingBinding = ref<GitBinding>();
const plan = ref<GitPlan>();
const planProfile = ref<GitProfile>();
const receipt = ref<GitReceipt>();
const job = ref<GitJob>();
const attempted = ref(false);
const submitKey = ref("");
const connection = ref<Record<string, GitConnectionResult>>({});
const history = ref<Record<string, GitJob[]>>({});
const historyId = ref("");
const preparationOpen = ref(false);
const automaticUpdate = ref<AutomaticPreparationUpdate>();
const preflightAction = ref<"BIND" | "UNBIND">("BIND");
let generation = 0;
let abort: AbortController | undefined;
let timer: ReturnType<typeof setTimeout> | undefined;
let pollFailures = 0;
let stopped = false;
const host = computed(() => localHost.value || props.host);
const pending = computed(() => attempted.value && !receipt.value || gitJobPending(job.value));
const blocked = computed(() => busy.value || pending.value || preparationOpen.value);
const dirty = computed(() => !!profileDraft.value || !!repoDraft.value || !!plan.value || !!deletingProfile.value);
const writable = computed(() => !blocked.value && !host.value.maintenance);
const credentialChoice = computed({ get: () => profileDraft.value?.credentialId ? `${profileDraft.value.credentialId}:${profileDraft.value.credentialRevision}` : "", set: (value: string) => { if (profileDraft.value) [profileDraft.value.credentialId, profileDraft.value.credentialRevision] = value.split(":") as [string, string]; } });
const shownSection = computed(() => props.section || tab.value);
watch([blocked, dirty], () => emit("closeState", { blocked: blocked.value, dirty: dirty.value }), { immediate: true });
watch(() => [profileDraft.value?.sshHost, profileDraft.value?.sshPort, profileDraft.value?.hostPublicKey], () => { if (profileDraft.value) profileDraft.value.hostKeyConfirmed = false; });
const current = (revision: number) => !stopped && props.active && revision === generation;
function report(cause: unknown) {
  error.value = { message: cause instanceof Error && /[\u4e00-\u9fff]/.test(cause.message) ? cause.message : "Git 操作未完成，请查看诊断信息", code: cause instanceof ApiError ? cause.code : "REQUEST_NOT_CONFIRMED", traceId: cause instanceof ApiError ? cause.traceId : "", at: cause instanceof ApiError ? cause.timestamp : new Date().toISOString() };
}
async function act(action: (signal: AbortSignal, revision: number) => Promise<void>) {
  if (busy.value || !props.active) return;
  const revision = generation, controller = new AbortController();
  abort = controller; busy.value = true; error.value = undefined;
  try { await action(controller.signal, revision); }
  catch (cause) { if (current(revision) && !controller.signal.aborted) report(cause); }
  finally { if (abort === controller) abort = undefined; if (current(revision)) busy.value = false; }
}
async function readLists(signal: AbortSignal, revision: number) {
  const [accounts, repos, keys] = await Promise.all([
    request<GitProfile[]>("/git-accounts", "GET", undefined, signal),
    request<GitBinding[]>(`/hosts/${host.value.id}/git-repositories`, "GET", undefined, signal),
    request<SSHCredential[]>("/credentials", "GET", undefined, signal),
  ]);
  if (!current(revision)) return;
  profiles.value = accounts; bindings.value = repos; credentials.value = keys.filter(item => item.kind === "SSH_KEY" && !item.revokedAt);
}
async function refresh() {
  await act(async (signal, revision) => {
    await readLists(signal, revision);
    if (!current(revision)) return;
    const unresolved = bindings.value.find(item => item.lastJobId && (item.lastJobAckPending || ["QUEUED", "RUNNING", "UNKNOWN"].includes(item.lastJobStatus || "") || ["PENDING", "APPLYING", "UNKNOWN", "BINDING", "UNBINDING"].includes(item.status)));
    if (!receipt.value && unresolved) receipt.value = { bindingId: unresolved.id, jobId: unresolved.lastJobId };
    if (receipt.value) await readJob(signal, revision);
  });
  schedule();
}
function editProfile(profile?: GitProfile) {
  if (!writable.value) return;
  editingProfile.value = profile; profileDraft.value = newGitProfile();
  if (profile) for (const key of Object.keys(profileDraft.value) as (keyof GitProfileDraft)[]) {
    if (key !== "hostKeyConfirmed") Object.assign(profileDraft.value, { [key]: profile[key] });
  }
  candidateFingerprint.value = ""; deletingProfile.value = ""; error.value = undefined;
}
async function scanHostKey() {
  if (!profileDraft.value?.sshHost) return;
  const draft = profileDraft.value, address = draft.sshHost, port = draft.sshPort;
  await act(async (signal, revision) => {
    const candidate = await request<{ hostPublicKey: string; fingerprint: string }>("/git-host-key", "POST", { sshHost: address, sshPort: port }, signal);
    if (!current(revision) || profileDraft.value !== draft || draft.sshHost !== address || draft.sshPort !== port) return;
    draft.hostPublicKey = candidate.hostPublicKey; draft.hostKeyConfirmed = false; candidateFingerprint.value = candidate.fingerprint;
  });
}
async function saveProfile() {
  if (!profileDraft.value || !writable.value) return;
  const invalid = gitProfileError(profileDraft.value);
  if (invalid) { report(new Error(invalid)); return; }
  const body = { ...profileDraft.value, expectedVersion: editingProfile.value?.version };
  await act(async (signal, revision) => {
    await request(editingProfile.value ? `/git-accounts/${editingProfile.value.id}` : "/git-accounts", editingProfile.value ? "PATCH" : "POST", body, signal);
    if (!current(revision)) return;
    profileDraft.value = undefined; editingProfile.value = undefined;
    await readLists(signal, revision); emit("changed");
  });
}
async function removeProfile(profile: GitProfile) {
  if (!writable.value) return;
  await act(async (signal, revision) => {
    await request(`/git-accounts/${profile.id}`, "DELETE", { expectedVersion: profile.version }, signal);
    if (!current(revision)) return;
    deletingProfile.value = ""; await readLists(signal, revision); emit("changed");
  });
}
function editRepository(binding?: GitBinding) {
  if (!writable.value) return;
  editingBinding.value = binding;
  repoDraft.value = { account: binding?.account || (host.value.sshUser === "root" ? "" : host.value.sshUser || ""), repoPath: binding?.repoPath || "", profileId: binding?.profileId || profiles.value[0]?.id || "" };
  plan.value = undefined; receipt.value = undefined; job.value = undefined; attempted.value = false; error.value = undefined;
}
async function checkRepository(action: "BIND" | "UNBIND" = "BIND", automatic = true) {
  const draft = repoDraft.value;
  if (!draft || !writable.value) return;
  const profile = profiles.value.find(item => item.id === draft.profileId);
  if (!profile || !draft.account.trim() || !draft.repoPath.trim()) { report(new Error("请填写服务器账号、仓库目录并选择 Git 账号")); return; }
  preflightAction.value = action;
  const body = { expectedHostVersion: host.value.version, bindingId: editingBinding.value?.id, expectedBindingVersion: editingBinding.value?.version, action, account: draft.account.trim(), repoPath: draft.repoPath.trim(), profileId: profile.id, expectedProfileVersion: profile.version };
  await act(async (signal, revision) => {
    let checked: GitPlan;
    try { checked = await request<GitPlan>(`/hosts/${host.value.id}/git-repository-plans`, "POST", body, signal); }
    catch (cause) {
      if (!current(revision)) return;
      if (!automatic || host.value.kind !== "SSH" || !(cause instanceof ApiError) || !["EXECUTOR_GIT_UNSUPPORTED", "EXECUTOR_UPGRADE_REQUIRED"].includes(cause.code)) throw cause;
      const update = await request<PreparationUpdateCheck>(`/hosts/${host.value.id}/preparation?checkUpdate=1`, "GET", undefined, signal);
      if (!current(revision)) return;
      if (update.hostId !== host.value.id || update.expectedHostVersion !== host.value.version) throw new Error("主机配置已变化，请重新打开 Git 管理");
      automaticUpdate.value = automaticPreparationUpdate(update);
      if (!automaticUpdate.value) throw cause;
      preparationOpen.value = true;
      return;
    }
    if (!current(revision)) return;
    if (checked.hostId !== host.value.id || checked.action !== action) throw new Error("预检目标与当前操作不一致，请重新检查");
    plan.value = checked; planProfile.value = { ...profile }; submitKey.value = crypto.randomUUID();
  });
}
async function unbind(binding: GitBinding) { editRepository(binding); await checkRepository("UNBIND"); }
async function preparationUpdated() {
  let refreshed = false;
  await act(async (signal, revision) => {
    const hosts = await request<Host[]>("/hosts", "GET", undefined, signal);
    if (!current(revision)) return;
    const updated = hosts.find(item => item.id === props.host.id);
    if (!updated || updated.capabilities?.mutation.status !== "AVAILABLE") throw new Error("接入状态尚未确认，请重试检测");
    localHost.value = updated; preparationOpen.value = false; automaticUpdate.value = undefined; refreshed = true;
  });
  if (refreshed) await checkRepository(preflightAction.value, false);
}
async function submit() {
  if (!plan.value || busy.value || receipt.value || host.value.maintenance) return;
  const expiry = Date.parse(plan.value.expiresAt);
  if (!attempted.value && (!Number.isFinite(expiry) || expiry <= Date.now())) { report(new Error("预检已过期，请返回重新检查")); return; }
  await act(async (signal, revision) => {
    attempted.value = true;
    let result: GitReceipt;
    try { result = await request<GitReceipt>(`/git-repository-plans/${plan.value!.id}/submit`, "POST", { idempotencyKey: submitKey.value, confirmed: true }, signal); }
    catch (cause) {
      if (current(revision) && cause instanceof ApiError && gitSubmitRejected(cause.code, cause.status)) { attempted.value = false; plan.value = undefined; submitKey.value = ""; }
      throw cause;
    }
    if (!current(revision)) return;
    receipt.value = result; repoDraft.value = undefined; plan.value = undefined;
    await readJob(signal, revision); emit("changed");
  });
  schedule();
}
async function readJob(signal: AbortSignal, revision: number) {
  if (!receipt.value) return;
  const result = await request<GitJob>(`/jobs/${receipt.value.jobId}`, "GET", undefined, signal);
  if (!current(revision)) return;
  job.value = result; pollFailures = 0;
  if (!gitJobPending(result)) { attempted.value = false; await readLists(signal, revision); }
}
function schedule() {
  clearTimeout(timer);
  if (!props.active || stopped || !receipt.value || !job.value || !["QUEUED", "RUNNING"].includes(job.value.status) || pollFailures >= 3) return;
  timer = setTimeout(async () => {
    if (!busy.value) { const revision = generation, controller = new AbortController(); try { await readJob(controller.signal, revision); } catch (cause) { if (current(revision)) { pollFailures++; report(cause); } } }
    schedule();
  }, 3000);
}
async function jobAction(action: "reconcile" | "cancel") {
  if (!receipt.value) return;
  await act(async (signal, revision) => { await request(`/jobs/${receipt.value!.jobId}/${action}`, "POST", {}, signal); if (current(revision)) await readJob(signal, revision); });
  schedule();
}
async function inspect(binding: GitBinding, test = false) {
  if (!writable.value) return;
  await act(async (signal, revision) => {
    const result = await request<GitConnectionResult | GitBinding["observation"]>(`/git-repositories/${binding.id}/${test ? "test" : "observe"}`, "POST", { expectedVersion: binding.version }, signal);
    if (!current(revision)) return;
    if (test) connection.value[binding.id] = result as GitConnectionResult;
    await readLists(signal, revision);
  });
}
async function showHistory(binding: GitBinding) {
  if (historyId.value === binding.id) { historyId.value = ""; return; }
  await act(async (signal, revision) => {
    const result = await request<GitJob[]>(`/git-repositories/${binding.id}/jobs`, "GET", undefined, signal);
    if (!current(revision)) return;
    history.value[binding.id] = result; historyId.value = binding.id;
  });
}
function clearDraft() { if (blocked.value) return; repoDraft.value = undefined; plan.value = undefined; profileDraft.value = undefined; deletingProfile.value = ""; error.value = undefined; }
watch(() => [props.host.id, props.active], ([id], old) => {
  generation++; abort?.abort(); clearTimeout(timer); busy.value = false;
  if (!old || old[0] !== id) { localHost.value = undefined; profiles.value = []; bindings.value = []; profileDraft.value = undefined; repoDraft.value = undefined; plan.value = undefined; receipt.value = undefined; job.value = undefined; attempted.value = false; preparationOpen.value = false; automaticUpdate.value = undefined; connection.value = {}; }
  if (props.active) void refresh();
}, { immediate: true });
onUnmounted(() => { stopped = true; generation++; abort?.abort(); clearTimeout(timer); });
</script>

<template>
  <section class="git-management" aria-label="Git 账号与仓库">
    <section v-if="preparationOpen" class="git-preparation">
      <h3>正在准备 Git 配置环境</h3>
      <HostPreparationPanel :host="host" :automatic-update="automaticUpdate" update-only @updated="preparationUpdated" />
    </section>
    <template v-else>
      <nav v-if="showTabs" class="git-tabs" aria-label="Git 管理">
        <button :class="{ active: shownSection === 'accounts' }" :disabled="blocked" @click="tab = 'accounts'">Git 账号 <span>{{ profiles.length }}</span></button>
        <button :class="{ active: shownSection === 'repositories' }" :disabled="blocked" @click="tab = 'repositories'">仓库 <span>{{ bindings.length }}</span></button>
      </nav>
      <div v-if="error" class="git-error" role="alert">
        <strong>{{ error.message }}</strong>
        <button v-if="['EXECUTOR_GIT_UNSUPPORTED', 'EXECUTOR_UPGRADE_REQUIRED'].includes(error.code)" class="secondary" :disabled="busy" @click="automaticUpdate = undefined; preparationOpen = true">更新接入组件</button>
        <details><summary>诊断信息</summary><code>{{ error.code }}</code><time>{{ hostTime(error.at) }}</time><code v-if="error.traceId">traceId={{ error.traceId }}</code></details>
      </div>
      <template v-if="shownSection === 'accounts'">
        <form v-if="profileDraft" class="git-form" aria-label="Git 账号表单" @submit.prevent="saveProfile">
          <header><h3>{{ editingProfile ? '编辑 Git 账号' : '新增 Git 账号' }}</h3><button type="button" class="quiet" :disabled="blocked" @click="clearDraft">返回列表</button></header>
          <fieldset :disabled="busy"><div class="git-grid">
            <label>账号名称<input v-model="profileDraft.name" aria-label="Git 账号名称" maxlength="120" required /></label>
            <label>Git 服务器<input v-model="profileDraft.sshHost" aria-label="Git 服务器地址" placeholder="github.com" required /></label>
            <label>SSH 端口<input v-model.number="profileDraft.sshPort" aria-label="Git SSH 端口" type="number" min="1" max="65535" /></label>
            <label>SSH 用户<input v-model="profileDraft.sshUser" aria-label="Git SSH 用户" required /></label>
            <label>提交姓名<input v-model="profileDraft.authorName" aria-label="Git 提交姓名" maxlength="200" required /></label>
            <label>提交邮箱<input v-model="profileDraft.authorEmail" aria-label="Git 提交邮箱" type="email" maxlength="254" required /></label>
            <label class="wide">SSH 密钥凭据<select v-model="credentialChoice" aria-label="Git SSH 密钥凭据" required><option value="">选择已保存的 SSH 密钥</option><option v-for="credential in credentials" :key="credential.id" :value="`${credential.id}:${credential.revision}`">{{ credential.name }}</option></select><small v-if="!credentials.length">请先在“连接凭据”添加 SSH 密钥。</small></label>
          </div>
          <section class="git-host-key"><header><h4>服务器主机公钥</h4><button type="button" class="secondary" :disabled="!profileDraft.sshHost" @click="scanHostKey">读取候选公钥</button></header>
            <textarea v-model="profileDraft.hostPublicKey" aria-label="Git 服务器主机公钥" rows="3" placeholder="ssh-ed25519 AAAA…" required />
            <code v-if="candidateFingerprint">{{ candidateFingerprint }}</code>
            <label class="git-check"><input v-model="profileDraft.hostKeyConfirmed" type="checkbox" aria-label="已与可信来源核对主机公钥" />已与可信来源核对主机公钥</label>
          </section></fieldset>
          <footer><button type="button" class="secondary" :disabled="blocked" @click="clearDraft">取消</button><button class="primary" :disabled="!writable || !profileDraft.hostKeyConfirmed">{{ busy ? '保存中…' : '保存账号' }}</button></footer>
        </form>
        <template v-else>
          <header class="git-toolbar"><h3>Git 账号</h3><button class="secondary" :disabled="busy" @click="refresh">刷新</button><button class="primary" :disabled="!writable" @click="editProfile()">新增账号</button></header>
          <div v-if="!profiles.length" class="empty">暂无 Git 账号</div>
          <article v-for="profile in profiles" :key="profile.id" class="git-row">
            <div class="git-row-main"><strong>{{ profile.name }}</strong><span>{{ profile.sshUser }}@{{ profile.sshHost }}:{{ profile.sshPort }}</span><small>{{ profile.authorName }} · {{ profile.authorEmail }}</small></div>
            <div v-if="deletingProfile === profile.id" class="git-actions"><span>删除此账号？</span><button class="danger" :disabled="!writable" @click="removeProfile(profile)">确认删除</button><button class="quiet" :disabled="busy" @click="deletingProfile = ''">取消</button></div>
            <div v-else class="git-actions"><button class="quiet" :disabled="!writable" @click="editProfile(profile)">编辑</button><button class="quiet danger" :disabled="!writable" @click="deletingProfile = profile.id">删除</button></div>
          </article>
        </template>
      </template>
      <template v-else>
        <section v-if="plan" class="git-plan" aria-label="Git 配置确认">
          <header><h3>{{ plan.action === 'UNBIND' ? '确认解除绑定' : '确认仓库配置' }}</h3><span>有效至 {{ hostTime(plan.expiresAt) }}</span></header>
          <dl><dt>服务器</dt><dd>{{ host.name }}</dd><dt>系统账号</dt><dd>{{ plan.observation.account }}</dd><dt>仓库目录</dt><dd><code>{{ plan.observation.repoPath }}</code></dd><dt>Git 账号</dt><dd>{{ planProfile?.name }}</dd><dt>提交身份</dt><dd>{{ plan.change.authorName || planProfile?.authorName }} · {{ plan.change.authorEmail || planProfile?.authorEmail }}</dd><dt>密钥指纹</dt><dd><code>{{ plan.change.keyFingerprint || planProfile?.keyFingerprint }}</code></dd><dt>主机指纹</dt><dd><code>{{ plan.change.hostKeyFingerprint || plan.observation.hostKeyFingerprint || '已按保存公钥核验' }}</code></dd></dl>
          <p v-if="plan.action === 'UNBIND'">解除账号绑定，保留仓库源码和提交历史。</p>
          <details><summary>配置范围</summary><p>{{ plan.action === 'UNBIND' ? '移除本绑定维护的配置与密钥文件。' : '写入此仓库的 user.name、user.email、core.sshCommand，并为所选系统账号保存专用 SSH 密钥。' }}</p><code v-if="plan.change.keyPath || plan.observation.keyPath">{{ plan.change.keyPath || plan.observation.keyPath }}</code></details>
          <footer><button class="secondary" :disabled="blocked" @click="plan = undefined">返回修改</button><button class="primary" :disabled="busy || !!receipt || host.maintenance" @click="submit">{{ busy ? '提交中…' : attempted ? '重试并核对原请求' : plan.action === 'UNBIND' ? '确认解除绑定' : '确认绑定' }}</button></footer>
        </section>
        <form v-else-if="repoDraft" class="git-form" aria-label="仓库绑定表单" @submit.prevent="checkRepository()">
          <header><h3>{{ editingBinding ? '修改仓库绑定' : '绑定已有仓库' }}</h3><button type="button" class="quiet" :disabled="blocked" @click="clearDraft">返回列表</button></header>
          <fieldset :disabled="busy"><div class="git-grid"><label>服务器账号<input v-model="repoDraft.account" aria-label="仓库系统账号" placeholder="flycode" required /></label><label>Git 账号<select v-model="repoDraft.profileId" aria-label="仓库 Git 账号" required><option value="">选择账号</option><option v-for="profile in profiles" :key="profile.id" :value="profile.id">{{ profile.name }} · {{ profile.authorEmail }}</option></select></label><label class="wide">仓库目录<input v-model="repoDraft.repoPath" aria-label="仓库目录" placeholder="/home/flycode/projects/app" required /></label></div></fieldset>
          <footer><button type="button" class="secondary" :disabled="blocked" @click="clearDraft">取消</button><button class="primary" :disabled="!writable">{{ busy ? '检查中…' : '检查配置' }}</button></footer>
        </form>
        <template v-else>
          <header class="git-toolbar"><h3>仓库绑定</h3><button class="secondary" :disabled="busy" @click="refresh">刷新</button><button class="primary" :disabled="!writable || !profiles.length" @click="editRepository()">绑定仓库</button></header>
          <div v-if="!bindings.length" class="empty">{{ profiles.length ? '暂无仓库绑定' : '先添加 Git 账号，再绑定服务器上的仓库。' }}</div>
          <article v-for="binding in bindings" :key="binding.id" class="git-repository">
            <div class="git-row"><div class="git-row-main"><strong>{{ binding.repoPath }}</strong><span>{{ binding.account }} · {{ binding.profileName || '账号已不可用' }}</span><small>{{ binding.observation?.branch || '分支待检查' }}<template v-if="binding.observation?.remoteUrl"> · {{ binding.observation.remoteUrl }}</template></small></div><span class="state-tag">{{ gitBindingStatus(binding.status) }}</span></div>
            <div class="git-actions"><button class="quiet" :disabled="!writable" @click="inspect(binding)">检查状态</button><button class="quiet" :disabled="!writable || ['RELEASED', 'UNBOUND', 'REMOVED'].includes(binding.status)" @click="inspect(binding, true)">测试读取</button><button class="quiet" :disabled="!writable" @click="editRepository(binding)">修改绑定</button><button class="quiet danger" :disabled="!writable || ['RELEASED', 'UNBOUND', 'REMOVED'].includes(binding.status)" @click="unbind(binding)">解除绑定</button><button class="quiet" :disabled="busy" @click="showHistory(binding)">操作记录</button></div>
            <section v-if="historyId === binding.id" class="git-history" aria-label="仓库操作记录">
              <p v-if="!history[binding.id]?.length">暂无操作记录</p>
              <article v-for="entry in history[binding.id]" :key="entry.id"><strong>{{ softwareJobStatusLabel(entry.status) }}</strong><span>{{ softwareJobPhaseLabel(entry.phase) }}</span><time>{{ hostTime(entry.updatedAt) }}</time><details><summary>诊断信息</summary><code v-if="entry.errorCode">{{ entry.errorCode }}</code><code>作业 {{ entry.id }}</code><code>traceId={{ entry.traceId }}</code></details></article>
            </section>
            <p v-if="connection[binding.id]" class="git-test-result">{{ connection[binding.id]?.ready ? '仓库读取正常' : connection[binding.id]?.message || '读取未确认' }}</p>
            <details v-if="binding.errorCode || connection[binding.id] && !connection[binding.id]?.ready"><summary>诊断信息</summary><code>{{ binding.errorCode || connection[binding.id]?.code }}</code><time>{{ hostTime(binding.observation?.observedAt) }}</time><code>仓库 {{ binding.id }}</code></details>
          </article>
        </template>
      </template>
      <section v-if="job" class="git-job" aria-label="Git 作业结果"><header><strong>{{ softwareJobStatusLabel(job.status) }}</strong><span>{{ softwareJobPhaseLabel(job.phase) }}</span></header><div class="git-actions"><button v-if="job.status === 'UNKNOWN' || job.targetAckPending" class="secondary" :disabled="busy" @click="jobAction('reconcile')">核对操作结果</button><button v-if="job.status === 'QUEUED'" class="quiet" :disabled="busy" @click="jobAction('cancel')">取消排队</button><button v-if="pollFailures >= 3" class="secondary" :disabled="busy" @click="pollFailures = 0; refresh()">刷新结果</button></div><details><summary>诊断信息</summary><code v-if="job.errorCode">{{ job.errorCode }}</code><time>{{ hostTime(job.updatedAt) }}</time><code>作业 {{ job.id }}</code><code>traceId={{ job.traceId }}</code></details></section>
    </template>
  </section>
</template>

<style scoped>
.git-management { display: grid; gap: 20px; min-width: 0; }
.git-tabs { display: flex; gap: 8px; border-bottom: 1px solid var(--border, #e0e7ef); }
.git-tabs button { border: 0; border-radius: 0; background: transparent; padding: 12px 18px; color: var(--muted); }
.git-tabs button.active { color: #245cb0; box-shadow: inset 0 -2px #245cb0; }
.git-tabs span { margin-left: 6px; font-size: 12px; }
.git-toolbar, .git-form header, .git-plan header, .git-host-key header, .git-job header { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
h3, h4, p { margin: 0; } h3 { font-size: 16px; } h4 { font-size: 14px; }
.git-toolbar h3, .git-form header h3, .git-host-key h4 { flex: 1; }
.git-form, .git-plan, .git-host-key { display: grid; gap: 18px; }
fieldset { border: 0; padding: 0; margin: 0; min-width: 0; display: grid; gap: 20px; }
.git-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 18px 20px; }
label { display: grid; gap: 8px; font-size: 13px; }
.wide { grid-column: 1 / -1; }
input:not([type=checkbox]), select, textarea { box-sizing: border-box; width: 100%; min-width: 0; border: 1px solid #d4deeb; border-radius: 6px; padding: 10px 12px; background: white; color: inherit; }
textarea, code { font-family: var(--mono, Consolas, monospace); overflow-wrap: anywhere; white-space: pre-wrap; }
textarea { resize: vertical; }
.git-host-key { border-top: 1px solid #e0e7ef; padding-top: 18px; }
.git-check { display: flex; gap: 8px; align-items: center; }.git-check input { width: 16px; height: 16px; }
.git-row { display: flex; align-items: center; gap: 16px; padding: 16px 0; border-bottom: 1px solid #e6ebf2; }
.git-row-main { flex: 1; min-width: 0; display: grid; gap: 7px; overflow-wrap: anywhere; }
.git-row-main span, .git-row-main small, small, time { color: var(--muted, #64748b); font-size: 12px; }
.git-actions { display: flex; gap: 12px; flex-wrap: wrap; align-items: center; }
.git-repository { display: grid; gap: 12px; border: 1px solid #e0e7ef; border-radius: 8px; padding: 0 16px 16px; }
.git-repository .git-row { border-bottom: 0; padding-bottom: 4px; }
footer { display: flex; justify-content: flex-end; gap: 10px; border-top: 1px solid #e6ebf2; padding-top: 16px; }
.git-plan dl { display: grid; grid-template-columns: 88px minmax(0, 1fr); gap: 14px; margin: 0; }.git-plan dt { color: var(--muted); }.git-plan dd { margin: 0; overflow-wrap: anywhere; }
.git-error { border-left: 3px solid #c55749; background: #fff7f4; padding: 14px; display: grid; gap: 10px; }
.git-job { border-top: 1px solid #dce5f0; padding-top: 16px; display: grid; gap: 12px; }
details { color: var(--muted, #64748b); font-size: 12px; } summary { cursor: pointer; margin-bottom: 8px; } details code, details time { display: block; margin: 6px 0; }
.git-test-result { color: #26745a; font-size: 13px; }
.git-history { display: grid; gap: 12px; padding: 14px; background: #f5f8fc; border-radius: 6px; }.git-history article { display: grid; grid-template-columns: auto minmax(0, 1fr); gap: 8px 14px; }.git-history details { grid-column: 1 / -1; }
@media(max-width: 620px) { .git-grid { grid-template-columns: minmax(0, 1fr); }.wide { grid-column: auto; }.git-row { flex-wrap: wrap; }.git-row-main { flex-basis: 100%; }.git-toolbar { gap: 8px; }.git-toolbar h3 { flex-basis: 100%; }.git-plan dl { grid-template-columns: 76px minmax(0, 1fr); } }
</style>
