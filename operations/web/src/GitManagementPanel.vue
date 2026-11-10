<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { ApiError, request } from "./api";
import OpsPagination from "./OpsPagination.vue";
import { usePagination } from "./pagination";
import { hostTime, type Host } from "./hosts";
import HostPreparationPanel from "./HostPreparationPanel.vue";
import BlockingJobAction, { type BlockingJob } from "./BlockingJobAction.vue";
import { gitAccountRemote, gitProfileError, newGitProfile, type GitKeyEvidence, type GitProfile, type GitProfileDraft } from "./gitManagement";

const props = withDefaults(defineProps<{ host: Host; active?: boolean }>(), { active: true });
const emit = defineEmits<{ changed: []; closeState: [{ blocked: boolean; dirty: boolean }] }>();
const profiles = ref<GitProfile[]>([]);
const { page, pageSize, total, pageItems: pagedProfiles } = usePagination(
  () => profiles.value, [() => props.host.id],
);
const profileDraft = ref<GitProfileDraft>();
const editingProfile = ref<GitProfile>();
const generatedKey = ref<GitKeyEvidence>();
const candidateFingerprint = ref("");
const deletingProfile = ref("");
const busy = ref(false);
const preparationOpen = ref(false);
const localHost = ref<Host>();
const copied = ref(false);
const token = ref("");
const resumeProfile = ref<{id:string;remove:boolean}>();
const usageProfile = ref("");
const repository = ref("");
const error = ref<{ message: string; code: string; traceId: string; at: string; blockingJob?: BlockingJob }>();
const host = computed(() => localHost.value || props.host);
const blocked = computed(() => busy.value || preparationOpen.value);
const writable = computed(() => !blocked.value && !host.value.maintenance);
const dirty = computed(() => !!profileDraft.value || !!deletingProfile.value);
const repositoryCommand = computed(() => {
  const profile = profiles.value.find(item => item.id === usageProfile.value);
  if (!profile || !repository.value.trim()) return {};
  try {
    return { command: `git clone -- ${gitAccountRemote(profile, repository.value)}` };
  } catch {
    return { error: "请粘贴属于此账号的 HTTPS 或 SSH 仓库地址" };
  }
});
function showUsage(profile: GitProfile) {
  usageProfile.value = usageProfile.value === profile.id ? "" : profile.id;
  repository.value = "";
}
let generation = 0;
let stopped = false;
let abort: AbortController | undefined;
let resume: "KEY" | "HOST_KEY" = "KEY";
watch([blocked, dirty], () => emit("closeState", { blocked: blocked.value, dirty: dirty.value }), { immediate: true });
watch(() => [profileDraft.value?.sshHost, profileDraft.value?.sshPort, profileDraft.value?.hostPublicKey], () => {
  const d = profileDraft.value, saved = editingProfile.value;
  if (d) d.hostKeyConfirmed = !!saved?.hostPublicKey && d.sshHost === saved.sshHost && d.sshPort === saved.sshPort && d.hostPublicKey === saved.hostPublicKey;
});
const current = (revision: number) => !stopped && props.active && revision === generation;
function report(cause: unknown) {
  error.value = { message: cause instanceof Error && /[\u4e00-\u9fff]/.test(cause.message) ? cause.message : "操作未完成，请重试或查看诊断信息", code: cause instanceof ApiError ? cause.code : "REQUEST_NOT_CONFIRMED", traceId: cause instanceof ApiError ? cause.traceId : "", at: cause instanceof ApiError ? cause.timestamp : new Date().toISOString(), blockingJob: cause instanceof ApiError && cause.code === "OPERATION_LOCKED" ? cause.data as BlockingJob | undefined : undefined };
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
  const accounts = await request<GitProfile[]>("/git-accounts", "GET", undefined, signal);
  if (current(revision)) profiles.value = accounts.filter(item => item.hostId === host.value.id);
}
async function refresh() { await act(readLists); }
function editProfile(profile?: GitProfile) {
  if (!writable.value) return;
  editingProfile.value = profile;
  const draft = newGitProfile();
  if (profile?.hostId) Object.assign(draft, profile);
  else if (profile) Object.assign(draft, { name: profile.name, sshHost: profile.sshHost, sshPort: profile.sshPort, authorName: profile.authorName, authorEmail: profile.authorEmail });
  draft.systemAccount ||= host.value.sshUser === "root" ? "" : host.value.sshUser || "";
  profileDraft.value = draft;
  token.value="";
  generatedKey.value = draft.publicKey ? { id: draft.keyId!, account: draft.systemAccount!, accountUid: draft.accountUid!, keyPath: draft.keyPath!, publicKey: draft.publicKey, fingerprint: profile?.keyFingerprint || "" } : undefined;
  candidateFingerprint.value = ""; deletingProfile.value = ""; copied.value = false; error.value = undefined;
}
async function generateTargetKey() {
  const draft = profileDraft.value;
  if (!draft || !draft.systemAccount?.trim() || draft.systemAccount === "root" || !writable.value) return;
  resume = "KEY";
  draft.keyId ||= crypto.randomUUID();
  const account = draft.systemAccount.trim();
  await act(async (signal, revision) => {
    const result = await request<GitKeyEvidence>(`/hosts/${host.value.id}/git-keys`, "POST", { id: draft.keyId, account, expectedHostVersion: host.value.version, operation: "GENERATE" }, signal);
    if (!current(revision) || profileDraft.value !== draft || draft.systemAccount?.trim() !== account) return;
    if (!result.publicKey || !result.keyPath || !result.fingerprint) throw new Error("没有取得账号公钥，请重新读取");
    generatedKey.value = result;
    Object.assign(draft, { keySource: "TARGET", hostId: host.value.id, accountUid: result.accountUid, keyId: result.id, keyPath: result.keyPath, publicKey: result.publicKey, keyFingerprint: result.fingerprint });
  });
}
function resetTargetKey() {
  if (profileDraft.value) Object.assign(profileDraft.value, { keyId: "", keyPath: "", publicKey: "", accountUid: "", keyFingerprint: "" });
  generatedKey.value = undefined; copied.value = false;
}
async function copyPublicKey() {
  try { if (!generatedKey.value?.publicKey) return; await navigator.clipboard.writeText(generatedKey.value.publicKey); copied.value = true; }
  catch { report(new Error("复制未完成，请选中公钥手动复制")); }
}
async function scanHostKey() {
  const draft = profileDraft.value;
  if (!draft?.sshHost || !draft.systemAccount) return;
  resume = "HOST_KEY";
  const address = draft.sshHost, port = draft.sshPort;
  await act(async (signal, revision) => {
    const candidate = await request<GitKeyEvidence>(`/hosts/${host.value.id}/git-keys`, "POST", { id: draft.keyId || crypto.randomUUID(), account: draft.systemAccount, sshHost: address, sshPort: port, operation: "HOST_KEY", expectedHostVersion: host.value.version }, signal);
    if (!current(revision) || profileDraft.value !== draft || draft.sshHost !== address || draft.sshPort !== port) return;
    if (!candidate.hostPublicKey || !candidate.hostFingerprint) throw new Error("没有读取到 Git 服务主机公钥，请检查地址、SSH 端口和目标服务器网络；可先留空保存账号");
    draft.hostPublicKey = candidate.hostPublicKey; candidateFingerprint.value = candidate.hostFingerprint;
  });
}
async function saveProfile() {
  const draft = profileDraft.value;
  if (!draft || !writable.value) return;
  const invalid = gitProfileError(draft);
  if (invalid) { report(new Error(invalid)); return; }
  if (draft.authMethod==="HTTPS" && !token.value && !draft.credentialId) { report(new Error("请填写访问 Token"));return; }
  await act(async (signal, revision) => {
    if(draft.authMethod==="HTTPS" && token.value) {
      const credential=await request<{id:string;revision:string}>("/credentials","POST",{name:draft.name.slice(0,50)+" Token",kind:"TOKEN",secret:token.value},signal);
      if(!current(revision) || profileDraft.value!==draft) return;
      draft.credentialId=credential.id;draft.credentialRevision=credential.revision;token.value="";
    }
    await request(editingProfile.value ? `/git-accounts/${editingProfile.value.id}` : "/git-accounts", editingProfile.value ? "PATCH" : "POST", { ...draft, keySource: draft.authMethod==="HTTPS" ? "OPS" : "TARGET", credentialId:draft.authMethod==="HTTPS" ? draft.credentialId : "",credentialRevision:draft.authMethod==="HTTPS" ? draft.credentialRevision : "",hostId: host.value.id, expectedHostVersion: host.value.version, expectedVersion: editingProfile.value?.version }, signal);
    if (!current(revision)) return;
    profileDraft.value = undefined; editingProfile.value = undefined;
    await readLists(signal, revision); emit("changed");
  });
}
function changeMethod() { if(profileDraft.value) { profileDraft.value.sshPort=profileDraft.value.authMethod==="HTTPS" ? 443 : 22;resetTargetKey();token.value=""; } }
async function configureProfile(profile:GitProfile,remove=false) {
  resumeProfile.value={id:profile.id,remove};
  await act(async(signal,revision)=>{
    await request(`/git-accounts/${profile.id}/${remove ? 'remove-configuration' : 'configure'}`,"POST",{expectedVersion:profile.version,expectedHostVersion:host.value.version},signal);
    if(!current(revision)) return;
    resumeProfile.value=undefined;await readLists(signal,revision);emit("changed");
  });
}
async function removeProfile(profile: GitProfile) {
  if (!writable.value) return;
  await act(async (signal, revision) => {
    if(profile.configuredVersion && profile.configuredVersion!=="0") {
      await request(`/git-accounts/${profile.id}/remove-configuration`,"POST",{expectedVersion:profile.version,expectedHostVersion:host.value.version},signal);
      if(!current(revision)) return;
    }
    await request(`/git-accounts/${profile.id}`, "DELETE", { expectedVersion: profile.version }, signal);
    if (!current(revision)) return;
    deletingProfile.value = ""; await readLists(signal, revision); emit("changed");
  });
}
async function preparationUpdated() {
  await act(async (signal, revision) => {
    const updated = (await request<Host[]>("/hosts", "GET", undefined, signal)).find(item => item.id === props.host.id);
    if (!current(revision)) return;
    if (!updated || updated.capabilities?.mutation.status !== "AVAILABLE") throw new Error("接入状态尚未确认，请重试检测");
    localHost.value = updated; preparationOpen.value = false;
  });
  if (!preparationOpen.value) {
    const pending=resumeProfile.value;
    if(pending) { const profile=profiles.value.find(item=>item.id===pending.id);if(profile) await configureProfile(profile,pending.remove); }
    else if (resume === "KEY") await generateTargetKey(); else await scanHostKey();
  }
}
function clearDraft() { if (!blocked.value) { profileDraft.value = undefined; generatedKey.value = undefined; token.value="";deletingProfile.value = ""; error.value = undefined; } }
watch([() => props.host.id, () => props.active], ([id], old) => {
  generation++; abort?.abort(); busy.value = false;
  token.value="";
  if (!old || old[0] !== id) { localHost.value = undefined; profiles.value = []; profileDraft.value = undefined; generatedKey.value = undefined; preparationOpen.value = false; resumeProfile.value=undefined;deletingProfile.value = "";usageProfile.value = "";repository.value = ""; }
  if (props.active) void refresh();
}, { immediate: true });
onUnmounted(() => { stopped = true; generation++; abort?.abort();token.value=""; });
</script>

<template>
  <section class="git-management" aria-label="Git 账号配置">
    <section v-if="preparationOpen" class="git-preparation">
      <h3>正在准备 Git 配置环境</h3>
      <HostPreparationPanel :host="host" update-only @updated="preparationUpdated" />
    </section>
    <template v-else>
      <div v-if="error" class="git-error" role="alert">
        <strong>{{ error.message }}</strong>
        <BlockingJobAction v-if="error.blockingJob?.id" :job="error.blockingJob" :disabled="busy" />
        <button v-if="['EXECUTOR_GIT_UNSUPPORTED', 'EXECUTOR_UPGRADE_REQUIRED'].includes(error.code)" class="secondary" :disabled="busy" @click="preparationOpen = true">更新接入组件</button>
        <details><summary>诊断信息</summary><code>{{ error.code }}</code><time>{{ hostTime(error.at) }}</time><code v-if="error.traceId">traceId={{ error.traceId }}</code></details>
      </div>
        <form v-if="profileDraft" class="git-form" aria-label="Git 账号表单" @submit.prevent="saveProfile">
          <header><h3>{{ editingProfile ? '编辑 Git 账号' : '新增 Git 账号' }}</h3><button type="button" class="quiet" :disabled="blocked" @click="clearDraft">返回列表</button></header>
          <fieldset :disabled="busy"><div class="git-grid">
            <label>账号名称<input v-model="profileDraft.name" aria-label="Git 账号名称" maxlength="120" required /></label>
            <label>认证方式<select v-model="profileDraft.authMethod" aria-label="Git 认证方式" :disabled="!!editingProfile" @change="changeMethod"><option value="HTTPS">HTTPS · 用户名与 Token</option><option value="SSH">SSH · 账号密钥</option></select></label>
            <label>Git 服务地址<input v-model="profileDraft.sshHost" aria-label="Git 服务器地址" placeholder="gitlab.example.com 或 github.com" :disabled="!!editingProfile" required /></label>
            <label>端口<input v-model.number="profileDraft.sshPort" aria-label="Git 服务端口" type="number" min="1" max="65535" :disabled="!!editingProfile" /></label>
            <label>服务器系统账号<input v-model="profileDraft.systemAccount" aria-label="目标系统账号" placeholder="flycode" :disabled="!!editingProfile" @input="resetTargetKey" required /></label>
            <template v-if="profileDraft.authMethod === 'HTTPS'"><label>Git 用户名<input v-model="profileDraft.username" aria-label="Git 登录用户名" placeholder="flycodeu" :disabled="!!editingProfile" required /></label><label class="wide">访问 Token<input v-model="token" type="password" aria-label="Git 访问 Token" autocomplete="new-password" :placeholder="editingProfile ? '留空保留已保存的 Token' : '粘贴 Git 服务的访问 Token'" /></label><p v-if="profileDraft.sshHost === 'github.com'" class="wide"><a href="https://github.com/settings/personal-access-tokens" target="_blank" rel="noopener noreferrer">创建 GitHub Token</a> · 用于 Git 拉取和推送</p><details class="wide"><summary>凭据存放</summary><p>Ops 加密保存 Token；配置到服务器后，Git 使用该系统账号的私有凭据文件（文件权限 600，内容未加密）。</p></details></template>
            <label v-else>SSH 用户<input v-model="profileDraft.sshUser" aria-label="Git SSH 用户" placeholder="git" :disabled="!!editingProfile" required /></label>
            <details class="wide"><summary>默认提交身份（选填）</summary><div class="git-grid"><label>提交姓名<input v-model="profileDraft.authorName" aria-label="Git 提交姓名" maxlength="200" /></label><label>提交邮箱<input v-model="profileDraft.authorEmail" aria-label="Git 提交邮箱" type="email" maxlength="254" /></label></div></details>
            <section v-if="profileDraft.authMethod === 'SSH'" class="wide git-key-steps" aria-label="目标服务器密钥">
              <h4>1. 在目标服务器生成账号密钥</h4>
              <p>{{ host.name }} · 私钥留在服务器，仅返回公钥。</p>
              <div class="git-key-row"><button type="button" class="secondary" :disabled="!profileDraft.systemAccount || profileDraft.systemAccount === 'root'" @click="generateTargetKey">{{ generatedKey ? '读取现有公钥' : '生成密钥' }}</button></div>
              <p v-if="!generatedKey" role="status">点击“生成密钥”后显示可复制的账号公钥，无需手工查找 SSH 文件。</p>
              <template v-if="generatedKey">
                <h4>2. 添加公钥到 Git 服务</h4>
                <div v-if="profileDraft.sshHost === 'github.com'" class="github-key-fields"><a href="https://github.com/settings/ssh/new" target="_blank" rel="noopener noreferrer">打开 GitHub 添加 SSH key</a><dl><dt>Title</dt><dd>{{ host.name }} · {{ profileDraft.systemAccount }}</dd><dt>Key type</dt><dd>Authentication Key</dd><dt>Key</dt><dd>复制下方完整公钥</dd></dl></div>
                <p v-else>自建 GitLab：打开你填写的 Git 服务，进入个人设置 → SSH Keys，把下面完整公钥粘贴到 Key 后保存。其他 Git 服务在个人设置中添加 SSH 公钥。</p>
                <textarea :value="generatedKey.publicKey" readonly rows="3" aria-label="复制到 Git 服务的账号公钥" />
                <div class="git-actions"><button type="button" class="secondary" @click="copyPublicKey">{{ copied ? '已复制' : '复制账号公钥' }}</button></div>
                <details><summary>密钥位置与指纹</summary><code>{{ generatedKey.keyPath }}</code><code>{{ generatedKey.fingerprint }}</code></details>
              </template>
            </section>

          </div>
          <details v-if="profileDraft.authMethod === 'SSH'" class="git-host-key"><summary>连接校验（可稍后配置）</summary><header><h4>Git 服务主机身份</h4><button type="button" class="secondary" :disabled="!profileDraft.sshHost || !profileDraft.systemAccount" @click="scanHostKey">从目标服务器读取</button></header>
            <p>用于连接时核验 Git 服务，不是账号公钥。保存账号时可留空；实际连接前必须核验。<a v-if="profileDraft.sshHost === 'github.com'" href="https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/githubs-ssh-key-fingerprints" target="_blank" rel="noopener noreferrer">核对 GitHub 官方指纹</a></p>
            <p v-if="!profileDraft.hostPublicKey" role="status">尚未读取。可点击上方按钮获取；自建 GitLab 的可信指纹由管理员提供。</p>
            <textarea v-model="profileDraft.hostPublicKey" aria-label="Git 服务器主机公钥" rows="3" placeholder="选填：读取后显示主机公钥，也可粘贴管理员提供的可信公钥" />
            <code v-if="candidateFingerprint">{{ candidateFingerprint }}</code>
            <label class="git-check"><input v-model="profileDraft.hostKeyConfirmed" type="checkbox" aria-label="已与可信来源核对主机公钥" />已与可信来源核对主机公钥</label>
          </details></fieldset>
          <footer><button type="button" class="secondary" :disabled="blocked" @click="clearDraft">取消</button><button class="primary" :disabled="!writable">{{ busy ? '保存中…' : '保存账号' }}</button></footer>
        </form>
        <template v-else>
          <header class="git-toolbar"><h3>Git 账号</h3><button class="secondary" :disabled="busy" @click="refresh">刷新</button><button class="primary" :disabled="!writable" @click="editProfile()">新增账号</button></header>
          <div v-if="!profiles.length" class="empty">暂无 Git 账号</div>
          <article v-for="profile in pagedProfiles" :key="profile.id" class="git-row">
            <div class="git-row-main">
              <strong>{{ profile.name }}</strong>
              <span>{{ profile.authMethod === 'HTTPS' ? profile.username : profile.sshUser }}@{{ profile.sshHost }} · {{ profile.authMethod }}</span>
              <small>{{ profile.systemAccount }} · {{ profile.configuredVersion === profile.version ? '已配置到服务器' : '待配置到服务器' }}</small>
            </div>
            <div v-if="deletingProfile === profile.id" class="git-actions"><span>删除此账号？</span><button class="danger" :disabled="!writable" @click="removeProfile(profile)">确认删除</button><button class="quiet" :disabled="busy" @click="deletingProfile = ''">取消</button></div>
            <div v-else class="git-actions"><button v-if="profile.configuredVersion !== profile.version" class="secondary" :disabled="!writable" @click="configureProfile(profile)">配置到服务器</button><button v-else class="secondary" :aria-expanded="usageProfile === profile.id" @click="showUsage(profile)">使用账号</button><button class="quiet" :disabled="!writable" @click="editProfile(profile)">编辑</button><button class="quiet danger" :disabled="!writable" @click="deletingProfile = profile.id">删除</button></div>
            <div v-if="usageProfile === profile.id && profile.configuredVersion === profile.version" class="git-usage">
              <label>仓库地址<input v-model="repository" placeholder="https://gitlab.example.com/team/project.git" autocomplete="off" /></label>
              <small v-if="repositoryCommand.error" role="status">{{ repositoryCommand.error }}</small>
              <template v-if="repositoryCommand.command">
                <code>{{ repositoryCommand.command }}</code>
                <small>在 {{ profile.systemAccount }} 的终端执行。进入项目目录后用 git pull 或 git push。</small>
              </template>
            </div>
          </article>
          <OpsPagination v-model:page="page" v-model:page-size="pageSize" :total="total" label="Git 账号分页" />
        </template>
    </template>
  </section>
</template>

<style scoped>
.git-management { display: grid; gap: 20px; min-width: 0; }
.git-toolbar, .git-form header, .git-host-key header { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
h3, h4, p { margin: 0; } h3 { font-size: 16px; } h4 { font-size: 14px; }
.git-toolbar h3, .git-form header h3, .git-host-key h4 { flex: 1; }
.git-form, .git-host-key { display: grid; gap: 18px; }
fieldset { border: 0; padding: 0; margin: 0; min-width: 0; display: grid; gap: 20px; }
.git-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 18px 20px; }
.git-key-steps { display: grid; gap: 14px; padding: 18px; border: 1px solid #dbe5f1; border-radius: 8px; background: #f8fbff; }
.git-key-steps p, .git-host-key p { font-size: 13px; line-height: 1.6; color: var(--muted, #64748b); }
.git-key-row { display: flex; gap: 12px; align-items: end; flex-wrap: wrap; }.git-key-row label { flex: 1; min-width: 180px; }
label { display: grid; gap: 8px; font-size: 13px; }
.wide { grid-column: 1 / -1; }
input:not([type=checkbox]), select, textarea { box-sizing: border-box; width: 100%; min-width: 0; border: 1px solid #d4deeb; border-radius: 6px; padding: 10px 12px; background: white; color: inherit; }
textarea, code { font-family: var(--mono, Consolas, monospace); overflow-wrap: anywhere; white-space: pre-wrap; }
textarea { resize: vertical; }
.git-host-key { border-top: 1px solid #e0e7ef; padding-top: 18px; }
.git-check { display: flex; gap: 8px; align-items: center; }.git-check input { width: 16px; height: 16px; }
.git-row { display: flex; align-items: center; flex-wrap: wrap; gap: 16px; padding: 16px 0; border-bottom: 1px solid #e6ebf2; }
.git-usage { flex-basis: 100%; display: grid; gap: 8px; }
.git-usage code { padding: 10px 12px; border-radius: 6px; background: #f1f5f9; }
.git-row-main { flex: 1; min-width: 0; display: grid; gap: 7px; overflow-wrap: anywhere; }
.git-row-main span, .git-row-main small, small, time { color: var(--muted, #64748b); font-size: 12px; }
.git-actions { display: flex; gap: 12px; flex-wrap: wrap; align-items: center; }
footer { display: flex; justify-content: flex-end; gap: 10px; border-top: 1px solid #e6ebf2; padding-top: 16px; }
.git-error { border-left: 3px solid #c55749; background: #fff7f4; padding: 14px; display: grid; gap: 10px; }
details { color: var(--muted, #64748b); font-size: 12px; } summary { cursor: pointer; margin-bottom: 8px; } details code, details time { display: block; margin: 6px 0; }
@media(max-width: 620px) { .git-grid { grid-template-columns: minmax(0, 1fr); }.wide { grid-column: auto; }.git-row { flex-wrap: wrap; }.git-row-main { flex-basis: 100%; }.git-toolbar { gap: 8px; }.git-toolbar h3 { flex-basis: 100%; } }
.github-key-fields dl { display:grid; grid-template-columns:80px minmax(0,1fr); gap:8px; margin:12px 0 0; font-size:13px; }.github-key-fields dd { margin:0; overflow-wrap:anywhere; }
</style>
