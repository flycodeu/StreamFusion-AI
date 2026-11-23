<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from "vue";
import { ElDialog, ElMessage } from "element-plus";
import { ApiError, request } from "./api";

interface LoginAccount {
  id: string;
  source: "MANUAL" | "INSTALL";
  label: string;
  version: string;
  username: string;
  fullName: string;
  email: string;
  hasPassword: boolean;
}

const props = defineProps<{ instanceId: string }>();
const emit = defineEmits<{ "close-state": [state: { blocked: boolean; dirty: boolean }] }>();
const path = `/software-instances/${props.instanceId}/login-accounts`;
const lifetime = new AbortController();
const accounts = ref<LoginAccount[]>();
const editing = ref<LoginAccount>();
const form = reactive({ username: "", password: "", fullName: "", email: "" });
const loading = ref(false);
const saving = ref(false);
const conflict = ref(false);
const error = ref("");
const dirty = computed(() => !!editing.value && (!!form.password || form.username !== editing.value.username ||
  form.fullName !== editing.value.fullName || form.email !== editing.value.email));

const copyOpen = ref(false);
const copyTarget = ref<LoginAccount>();
const copyBusy = ref(false);
const opsPassword = ref("");
const copyError = ref("");
const waitingForCopy = ref(false);
let copyAttempt: AbortController | undefined;
let clipboardPassword = "";
let clipboardExpires: ReturnType<typeof setTimeout> | undefined;

watch([saving, dirty], ([blocked, changed]) => emit("close-state", { blocked, dirty: changed }), { immediate: true });

function editAccount(account?: LoginAccount) {
  if (account?.source === "INSTALL") return;
  const row = account ?? { id: "", source: "MANUAL", label: "", version: "0", hasPassword: false, username: "", fullName: "", email: "" };
  editing.value = { ...row };
  Object.assign(form, { username: row.username, fullName: row.fullName, email: row.email, password: "" });
  error.value = "";
}

function finishEditing() {
  editing.value = undefined;
  form.password = "";
  error.value = "";
}

async function load() {
  if (loading.value || saving.value) return;
  loading.value = true;
  error.value = "";
  try {
    const data = await request<{ accounts: LoginAccount[] }>(path, "GET", undefined, lifetime.signal);
    if (lifetime.signal.aborted) return;
    accounts.value = data.accounts;
    finishEditing();
    conflict.value = false;
  } catch (cause) {
    if (!lifetime.signal.aborted) error.value = cause instanceof ApiError ? cause.message : "无法读取登录账号，请重试。";
  } finally {
    loading.value = false;
  }
}

async function save() {
  const row = editing.value;
  if (!row || !accounts.value || !dirty.value || saving.value || loading.value || conflict.value) return;
  if (!form.username.trim() || !row.hasPassword && !form.password) {
    error.value = "请填写用户名和密码。";
    return;
  }
  const creating = row.version === "0";
  const payload = { ...form, expectedVersion: row.version, id: undefined as string | undefined };
  saving.value = true;
  error.value = "";
  try {
    if (creating) {
      row.id ||= crypto.randomUUID();
      payload.id = row.id;
    }
    const data = await request<LoginAccount>(creating ? path : `${path}/${row.id}`, "POST", payload, lifetime.signal);
    if (lifetime.signal.aborted) return;
    if (creating) accounts.value.push(data);
    else accounts.value = accounts.value.map(item => item.id === data.id ? data : item);
    finishEditing();
    ElMessage.success("登录账号已保存");
  } catch (cause) {
    if (lifetime.signal.aborted) return;
    conflict.value = cause instanceof ApiError && ["VERSION_CONFLICT", "NOT_FOUND"].includes(cause.code);
    error.value = conflict.value ? "账号已在其他页面更新，请重新读取后再修改。"
      : cause instanceof ApiError ? cause.message : "无法保存登录账号，请检查 Ops 连接后重试。";
  } finally {
    payload.password = "";
    saving.value = false;
  }
}

function clearClipboardPassword() {
  if (clipboardExpires !== undefined) clearTimeout(clipboardExpires);
  clipboardExpires = undefined;
  clipboardPassword = "";
  waitingForCopy.value = false;
}

function resetCopy() {
  copyAttempt?.abort();
  copyAttempt = undefined;
  opsPassword.value = "";
  copyTarget.value = undefined;
  copyError.value = "";
  copyBusy.value = false;
  clearClipboardPassword();
}

function startCopy(account: LoginAccount) {
  copyTarget.value = account;
  copyOpen.value = true;
}

watch(copyOpen, open => { if (!open) resetCopy(); }, { flush: "sync" });
onMounted(load);
onUnmounted(() => {
  lifetime.abort();
  resetCopy();
  form.password = "";
  emit("close-state", { blocked: false, dirty: false });
});

async function copySavedPassword() {
  if (!clipboardPassword || !copyOpen.value) return;
  const attempt = copyAttempt;
  try {
    await navigator.clipboard.writeText(clipboardPassword);
    if (!copyOpen.value || copyAttempt !== attempt) return;
    copyOpen.value = false;
    ElMessage.success("软件登录密码已复制");
  } catch {
    if (copyOpen.value && copyAttempt === attempt && waitingForCopy.value)
      copyError.value = "无法写入剪贴板，请允许访问后重试复制。";
  }
}

async function verifyAndCopy() {
  const account = copyTarget.value;
  if (!account?.hasPassword || !copyOpen.value || copyBusy.value || waitingForCopy.value || !opsPassword.value) return;
  const attempt = new AbortController();
  copyAttempt = attempt;
  const payload = { expectedVersion: account.version, password: opsPassword.value };
  opsPassword.value = "";
  copyError.value = "";
  copyBusy.value = true;
  const current = () => copyOpen.value && copyAttempt === attempt && !attempt.signal.aborted;
  try {
    const result = await request<{ password: string }>(`${path}/${account.id}/copy-password`, "POST", payload, attempt.signal);
    const password = result?.password;
    if (result && typeof result === "object") result.password = "";
    if (!current()) return;
    if (typeof password !== "string" || !password) {
      copyError.value = "读取密码失败，请重试。";
      return;
    }
    clipboardPassword = password;
    waitingForCopy.value = true;
    clipboardExpires = setTimeout(() => {
      clearClipboardPassword();
      copyError.value = "本次读取已过期，请重新验证。";
    }, 30_000);
    await copySavedPassword();
  } catch (cause) {
    if (!current()) return;
    if (cause instanceof ApiError && ["VERSION_CONFLICT", "NOT_FOUND"].includes(cause.code)) {
      copyOpen.value = false;
      conflict.value = true;
      error.value = "账号或关联凭据已更新，请重新读取后再复制。";
    } else {
      copyError.value = cause instanceof ApiError ? cause.message : "无法读取登录密码，请检查 Ops 连接后重试。";
    }
  } finally {
    payload.password = "";
    if (current()) copyBusy.value = false;
  }
}
</script>

<template>
  <section class="login-account" aria-label="软件登录账号">
    <div class="account-heading">
      <p v-if="accounts?.some(account => account.source === 'INSTALL')" class="account-note">安装账号保留安装时的密码。若已在软件中改密，可添加登录账号保存新密码。</p>
      <p v-else class="account-note">可以在这里保存软件中已有账号的登录资料。</p>
      <button v-if="accounts && !editing" type="button" class="secondary" :disabled="loading || conflict" @click="editAccount()">添加登录账号</button>
    </div>
    <p v-if="loading" class="account-note" role="status">正在读取登录账号…</p>
    <p v-if="error" class="account-error" role="alert">{{ error }}</p>
    <button v-if="!loading && (conflict || !accounts)" type="button" class="secondary account-retry" @click="load">重新读取</button>
    <ul v-if="accounts?.length" class="account-list">
      <li v-for="account in accounts" :key="account.id" class="account-row">
        <div class="account-identity">
          <strong>{{ account.username }}</strong>
          <span>{{ account.source === 'INSTALL' ? ['安装时保存', account.label].filter(Boolean).join(' · ') : '手工保存' }}</span>
          <span v-if="account.fullName || account.email">{{ [account.fullName, account.email].filter(Boolean).join(' · ') }}</span>
        </div>
        <div class="account-password">
          <span>{{ account.hasPassword ? '密码已保存' : '密码不可用' }}</span>
          <small v-if="!account.hasPassword">{{ account.source === 'INSTALL' ? '请检查此实例绑定的连接凭据。' : '编辑账号可补录密码。' }}</small>
        </div>
        <div class="account-actions">
          <button v-if="account.source === 'MANUAL'" type="button" class="quiet" :disabled="!!editing || loading || conflict" @click="editAccount(account)">编辑</button>
          <button type="button" class="secondary" :disabled="!account.hasPassword || !!editing || loading || conflict" @click="startCopy(account)">复制密码</button>
        </div>
      </li>
    </ul>
    <p v-else-if="accounts && !editing" class="account-empty">暂无登录账号</p>
    <form v-if="editing" class="account-form" autocomplete="off" @submit.prevent="save">
      <h3>{{ editing.version === '0' ? '添加登录账号' : '编辑登录账号' }}</h3>
      <p class="account-note">登录资料保存在 Ops。创建账号或修改实际密码需在软件中操作。</p>
      <fieldset :disabled="saving || loading || conflict">
        <label>用户名
          <input v-model="form.username" maxlength="100" required />
        </label>
        <label>{{ editing.hasPassword ? '新密码（选填）' : '密码' }}
          <input v-model="form.password" type="password" autocomplete="new-password" maxlength="1024"
            :required="!editing.hasPassword" :placeholder="editing.hasPassword ? '留空保留已保存的密码' : ''" />
        </label>
        <label>全名
          <input v-model="form.fullName" maxlength="100" />
        </label>
        <label>邮箱
          <input v-model="form.email" type="email" maxlength="254" />
        </label>
      </fieldset>
      <div class="account-actions">
        <button type="submit" class="primary" :disabled="!dirty || saving || loading || conflict">{{ saving ? '正在保存…' : '保存登录账号' }}</button>
        <button type="button" class="secondary" :disabled="saving || loading" @click="finishEditing">取消</button>
      </div>
    </form>
  </section>
  <ElDialog v-model="copyOpen" title="复制软件登录密码" width="min(440px, 94vw)" align-center append-to-body destroy-on-close :close-on-click-modal="false">
    <form class="dialog-form" @submit.prevent="verifyAndCopy">
      <p v-if="copyTarget" class="account-note">复制 <strong>{{ copyTarget.username }}</strong> 的{{ copyTarget.source === 'INSTALL' ? '安装密码' : '登录密码' }}。</p>
      <label v-if="!waitingForCopy">Ops 管理员登录密码
        <input v-model="opsPassword" type="password" autocomplete="current-password" required :disabled="copyBusy" />
      </label>
      <p v-if="copyError" class="account-error" role="alert">{{ copyError }}</p>
      <div class="dialog-footer">
        <button type="button" class="secondary" @click="copyOpen = false">取消</button>
        <button v-if="waitingForCopy" type="button" class="primary" :disabled="copyBusy" @click="copySavedPassword">复制密码</button>
        <button v-else type="submit" class="primary" :disabled="copyBusy || !opsPassword">{{ copyBusy ? '正在验证…' : '验证并复制' }}</button>
      </div>
    </form>
  </ElDialog>
</template>

<style scoped>
.login-account, .account-form { display: grid; gap: 16px; min-width: 0; }
.account-heading, .account-row { display: flex; align-items: center; justify-content: space-between; gap: 12px 20px; }
.account-heading button { flex: none; }
.account-list { list-style: none; margin: 0; padding: 0; border-block: 1px solid var(--border); }
.account-row { padding: 12px 0; }
.account-row + .account-row { border-top: 1px solid var(--border); }
.account-identity, .account-password { display: grid; gap: 3px; overflow-wrap: anywhere; min-width: 0; }
.account-identity { flex: 1; }
.account-identity span, .account-password, .account-empty { color: var(--muted); font-size: 13px; }
.account-empty { margin: 0; padding: 16px 0; }
.account-form h3 { margin: 0; font-size: 15px; }
.account-note, .account-error { margin: 0; font-size: 13px; line-height: 1.6; }
.account-note { color: var(--muted); }
.account-error { color: var(--error); }
.account-form fieldset { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 14px 16px; margin: 0; padding: 0; border: 0; min-width: 0; }
.account-form label { display: grid; gap: 6px; min-width: 0; font-size: 13px; }
.account-form input { width: 100%; min-width: 0; padding: 8px 10px; border: 1px solid var(--border); border-radius: var(--control-radius); font: inherit; }
.account-form input:focus-visible { outline: 2px solid var(--accent); outline-offset: 1px; }
.account-actions { display: flex; flex-wrap: wrap; gap: 10px; }
.account-actions .primary { width: auto; margin: 0; }
.account-retry { justify-self: start; }
@media (max-width: 600px) {
  .account-heading, .account-row { flex-wrap: wrap; }
  .account-row .account-actions { width: 100%; }
  .account-form fieldset { grid-template-columns: minmax(0, 1fr); }
}
</style>
