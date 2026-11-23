<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { ElDialog, ElMessage } from "element-plus";
import { ApiError, request } from "./api";
import {
  canCopyJenkinsUnlockPassword,
  isJenkinsBootstrapPassword,
  isOpsUnlockConfirmationPassword,
  jenkinsUnlockFallbackCommand,
} from "./jenkinsUnlock";
import type { SoftwareInstance } from "./software";
import OpsDiagnosticBanner from "./OpsDiagnosticBanner.vue";
import TerminalCommandEntry from "./TerminalCommandEntry.vue";

const props = withDefaults(defineProps<{ instanceId: string; instance?: SoftwareInstance; active?: boolean }>(), { active: true });
const open = ref(false);
const busy = ref(false);
const currentPassword = ref("");
const error = ref("");
const diagnostic = ref<ApiError | null>(null);
const fallbackCommand = ref("");
const waitingForCopy = ref(false);
const setupInactive = ref(false);
const canOpen = computed(() => props.active && !setupInactive.value && !!props.instanceId && (!props.instance || canCopyJenkinsUnlockPassword(props.instance)));

let attempt: AbortController | undefined;
let pendingSecret = "";
let copyExpiry: ReturnType<typeof setTimeout> | undefined;

function clearPendingCopy() {
  if (copyExpiry !== undefined) clearTimeout(copyExpiry);
  copyExpiry = undefined;
  pendingSecret = "";
  waitingForCopy.value = false;
}

function clearDialog() {
  attempt?.abort();
  attempt = undefined;
  busy.value = false;
  currentPassword.value = "";
  error.value = "";
  diagnostic.value = null;
  fallbackCommand.value = "";
  clearPendingCopy();
}

function showDialog() {
  if (!canOpen.value) return;
  clearDialog();
  open.value = true;
}

watch(open, value => { if (!value) clearDialog(); }, { flush: "sync" });
watch(() => props.instanceId, () => { setupInactive.value = false; }, { flush: "sync" });
watch([() => props.instanceId, () => props.instance?.version, canOpen], () => { open.value = false; }, { flush: "sync" });
onUnmounted(clearDialog);

async function copyPassword() {
  if (!pendingSecret || !open.value) return;
  const currentAttempt = attempt;
  try {
    await navigator.clipboard.writeText(pendingSecret);
    if (!open.value || attempt !== currentAttempt) return;
    open.value = false;
    ElMessage.success("Jenkins 首次解锁密码已复制，请粘贴到 Jenkins 解锁页。");
  } catch {
    if (open.value && attempt === currentAttempt && waitingForCopy.value)
      error.value = "无法写入剪贴板，请允许此页面的剪贴板访问后重试复制。";
  }
}

function revealError(cause: unknown, instance?: SoftwareInstance) {
  const failure = cause instanceof ApiError ? cause : null;
  const code = failure?.code;
  switch (code) {
    case "JENKINS_UNLOCK_UNSUPPORTED":
      if (instance) fallbackCommand.value = jenkinsUnlockFallbackCommand(instance);
      error.value = fallbackCommand.value ? "更新接入组件后可直接复制。也可在软件所在主机执行下方命令。" : "更新这台主机的接入组件后可直接复制。";
      break;
    case "JENKINS_UNLOCK_SETUP_INACTIVE":
      setupInactive.value = true;
      open.value = false;
      ElMessage.info("该实例当前无需首次解锁，请在 Jenkins 登录页继续。");
      return;
    case "JENKINS_UNLOCK_SETUP_UNAVAILABLE":
      error.value = "暂时无法确认 Jenkins 是否等待首次解锁，请打开 Jenkins 检查后重试。";
      break;
    case "JENKINS_UNLOCK_SECRET_UNAVAILABLE":
      error.value = "暂时无法读取该实例的首次解锁密码，请检查 Jenkins 后重试。";
      break;
    case "JENKINS_UNLOCK_BINDING_UNVERIFIED":
      error.value = "无法核对当前 Jenkins 实例与主机的绑定，请检查软件状态后重试。";
      break;
    case "JENKINS_UNLOCK_REAUTH_FAILED":
      error.value = "Ops 管理员的登录密码不正确，请重新输入。";
      break;
    case "JENKINS_UNLOCK_PASSWORD_FORMAT":
      error.value = "请输入当前 Ops 管理员的登录密码，最多 64 字节。";
      break;
    case "JENKINS_UNLOCK_REAUTH_LOCKED":
    case "LOGIN_RATE_LIMIT":
    case "SECURE_REQUEST_LIMIT":
      error.value = "身份验证次数过多，请稍后重试。";
      break;
    case "JENKINS_UNLOCK_TLS_REQUIRED":
      error.value = "请通过 HTTPS 或本机回环地址访问 Ops 后重试。";
      break;
    case "AUTH_REQUIRED":
      error.value = "请重新登录 Ops 后重试。";
      break;
    case "CSRF_INVALID":
    case "SECURE_REQUEST_INVALID":
      error.value = "安全请求无效或已过期，请刷新 Ops 页面后重新验证。";
      break;
    case "ORIGIN_REJECTED":
      error.value = "请从当前 Ops 的访问地址打开软件详情后重试。";
      break;
    case "INVALID_INPUT":
    case "VERSION_CONFLICT":
    case "NOT_FOUND":
      error.value = "请刷新软件实例详情后重新验证。";
      break;
    case "LOGIN_BUSY":
    case "HOST_CHECK_BUSY":
    case "EXECUTOR_BUSY":
      error.value = "当前服务繁忙，请稍后重试。";
      break;
    case "OPERATION_LOCKED":
    case "TARGET_PENDING":
      error.value = "软件或主机有进行中的作业，请在作业结束后重试。";
      break;
    case "EXECUTOR_CHECK_TIMEOUT":
    case "SSH_TIMEOUT":
      error.value = "读取超时，请确认软件所在主机在线后重试。";
      break;
    case "SSH_AUTH_FAILED":
    case "CREDENTIAL_UNAVAILABLE":
    case "SUDO_AUTHORIZATION_REQUIRED":
    case "EXECUTOR_MISSING":
    case "EXECUTOR_CALL_FAILED":
    case "EXECUTOR_RESPONSE_INVALID":
      error.value = "无法调用主机接入组件，请检查主机的连接和安装准备后重试。";
      break;
    default:
      error.value = failure
        ? "本次未复制密码，请查看诊断信息后重试。"
        : "本次未复制密码，请检查 Ops 页面连接后重试。";
  }
  if (failure) {
    const safeCode = typeof code === "string" && /^[A-Z0-9_]{1,80}$/.test(code) ? code : "";
    const traceId = typeof failure.traceId === "string" && /^[0-9a-f]{32}$/.test(failure.traceId) ? failure.traceId : "";
    const timestamp = typeof failure.timestamp === "string" && Number.isFinite(Date.parse(failure.timestamp)) ? failure.timestamp : "";
    diagnostic.value = new ApiError(error.value, safeCode, traceId, timestamp);
  }
}

async function verifyAndCopy() {
  if (busy.value || !open.value || !canOpen.value || !currentPassword.value || waitingForCopy.value) return;
  error.value = "";
  diagnostic.value = null;
  fallbackCommand.value = "";
  if (!isOpsUnlockConfirmationPassword(currentPassword.value)) {
    error.value = "请输入当前 Ops 管理员的登录密码，最多 64 字节。";
    currentPassword.value = "";
    return;
  }
  if (!globalThis.crypto?.subtle) {
    error.value = "请通过 HTTPS 或本机回环地址访问 Ops 后重试。";
    currentPassword.value = "";
    return;
  }
  clearPendingCopy();
  attempt?.abort();
  const controller = new AbortController();
  attempt = controller;
  const id = props.instanceId;
  const payload = { password: currentPassword.value, expectedVersion: props.instance?.version || "" };
  currentPassword.value = "";
  busy.value = true;
  let instance = props.instance;
  const current = () => open.value && attempt === controller && !controller.signal.aborted;

  try {
    if (!instance) instance = await request<SoftwareInstance>(`/software-instances/${id}`, "GET", undefined, controller.signal);
    if (!current()) return;
    if (instance.id !== id || !canCopyJenkinsUnlockPassword(instance) || !instance.version) {
      error.value = "该实例当前无法读取首次解锁密码，请刷新软件详情后重试。";
      return;
    }
    payload.expectedVersion = instance.version;
    const received = await request<{ password: string }>(`/software-instances/${id}/jenkins-unlock`, "POST", payload, controller.signal);
    payload.password = "";
    const password = received?.password;
    if (received && typeof received === "object") received.password = "";
    if (!current()) return;
    if (!isJenkinsBootstrapPassword(password)) {
      error.value = "Ops 返回的 Jenkins 解锁密码格式异常，本次未复制。请重试。";
      return;
    }
    pendingSecret = password;
    waitingForCopy.value = true;
    copyExpiry = setTimeout(() => {
      clearPendingCopy();
      error.value = "本次读取已过期，请重新验证身份。";
    }, 30_000);
    await copyPassword();
  } catch (cause) {
    if (current()) revealError(cause, instance);
  } finally {
    payload.password = "";
    if (current()) busy.value = false;
  }
}
</script>

<template>
  <button v-if="canOpen" type="button" class="secondary" @click="showDialog">复制首次解锁密码</button>
  <ElDialog v-model="open" title="复制 Jenkins 首次解锁密码" width="min(440px, 94vw)" align-center append-to-body destroy-on-close :close-on-click-modal="false">
    <form class="dialog-form" @submit.prevent="verifyAndCopy">
      <p class="unlock-note">看到复制成功提示后，再把新复制的密码粘贴到 Jenkins 解锁页。</p>
      <label v-if="!waitingForCopy">Ops 管理员登录密码
        <input v-model="currentPassword" type="password" autocomplete="current-password" required :disabled="busy" />
      </label>
      <OpsDiagnosticBanner v-if="diagnostic" :diagnostic="diagnostic" @close="diagnostic = null; error = ''" />
      <p v-else-if="error" class="unlock-error" role="alert">{{ error }}</p>
      <TerminalCommandEntry v-if="fallbackCommand" label="读取首次解锁密码" :command="fallbackCommand" />
      <div class="dialog-footer">
        <button type="button" class="secondary" @click="open = false">取消</button>
        <button v-if="waitingForCopy" type="button" class="primary" :disabled="busy" @click="copyPassword">复制密码</button>
        <button v-else type="submit" class="primary" :disabled="busy || !currentPassword">{{ busy ? '正在读取…' : '验证并复制' }}</button>
      </div>
    </form>
  </ElDialog>
</template>

<style scoped>
.unlock-note, .unlock-error { margin: 0; font-size: 13px; line-height: 1.5; }
.unlock-note { color: var(--muted); }
.unlock-error { color: #b42318; }
</style>
