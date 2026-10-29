<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { ElDialog, ElMessage, ElMessageBox } from "element-plus";
import { ApiError, prepare, request, send, setCSRF } from "./api";
import OpsWorkspace from "./OpsWorkspace.vue";
import SoftwareManagement from "./SoftwareManagement.vue";
import HostManagement from "./HostManagement.vue";
import PortManagement from "./PortManagement.vue";
import HostOverview from "./HostOverview.vue";

interface Status {
  status: string;
  release: string;
  os: string;
  arch: string;
  listen: string;
  stateDirectory: string;
  startedAt: string;
  uptimeSeconds: number;
  heapBytes: number;
}
interface Settings {
  name: string;
  controllerId: string;
  version: string;
  createdAt: string;
}
interface Credential {
  id: string;
  revision: string;
  name: string;
  kind: string;
  createdAt: string;
  revokedAt: string;
}
interface Audit {
  id: string;
  actor: string;
  action: string;
  result: string;
  createdAt: string;
  traceId: string;
  objectId: string;
}
const nav = [
  {
    id: "overview",
    label: "运行总览",
    icon: "M3 3h7v7H3z M14 3h7v7h-7z M3 14h7v7H3z M14 14h7v7h-7z",
  },
  {
    id: "hosts",
    label: "主机管理",
    icon: "M4 5h16v14H4z M8 9h2 M14 9h2 M8 14h8",
  },
  {
    id: "ports",
    label: "端口管理",
    icon: "M4 6h16v12H4z M8 9v6 M12 9v6 M16 9v6",
  },
  {
    id: "software",
    label: "软件管理",
    icon: "M4 4h6v6H4z M14 4h6v6h-6z M4 14h6v6H4z M14 14h6v6h-6z",
  },
  { id: "instances", label: "应用实例", icon: "M5 5h14v14H5z M9 9h6v6H9z" },
  { id: "artifacts", label: "版本制品", icon: "M4 7h16v13H4z M8 4h8v3H8z" },
  { id: "jobs", label: "发布作业", icon: "M6 3h12v4H6z M6 11h12 M6 16h8" },

  {
    id: "credentials",
    label: "连接凭据",
    icon: "M14 3a7 7 0 0 0-6.7 9L3 16v5h5v-3h3l2-2a7 7 0 1 0 1-13z M16 7h.01",
  },
  {
    id: "audit",
    label: "操作记录",
    icon: "M7 3h10v3H7z M7 5H4v16h16V5h-3 M8 11h8 M8 16h5",
  },
  {
    id: "settings",
    label: "系统设置",
    icon: "M4 7h16 M4 17h16 M8 4v6 M16 14v6",
  },
];
const title = computed(
  () => nav.find((item) => item.id === active.value)?.label,
);
const active = ref("overview");
const preparationHostId = ref("");
function showHostPreparation(hostId: string) {
  preparationHostId.value = hostId;
  active.value = "hosts";
}
const navigationOpen = ref(false);
const navigationGroups = [
  { label: "工作空间", items: nav.slice(0, 4) },
  { label: "应用交付", items: nav.slice(4, 7) },
  { label: "控制器", items: nav.slice(7) },
];
const loggedIn = ref(false);
const busy = ref(false);
const connected = ref(false);
const username = ref("");
const password = ref("");
const loginError = ref("");
const diagnostic = ref<ApiError | null>(null);
const status = ref<Status>();
const settings = ref<Settings>();
const nameDraft = ref("");
const audits = ref<Audit[]>([]);
const nextCursor = ref("");
const credentials = ref<Credential[]>([]);
const credentialDialog = ref(false);
const selected = ref<Credential | null>(null);
const credentialName = ref("");
const credentialKind = ref("TOKEN");
const secret = ref("");
const passwordDialog = ref(false);
const oldPassword = ref("");
const newPassword = ref("");
const confirmPassword = ref("");
const auditDetail = ref<Audit | null>(null);
const showLoginPassword = ref(false);
const loginCapsLock = ref(false);
const opsHighlights = [
  "主机与环境纳管",
  "软件与服务编排",
  "端口与安全基线",
] as const;

function focusLoginInput() {
  void nextTick(() => {
    if (loggedIn.value) return;
    const id = username.value.trim()
      ? "ops-login-password"
      : "ops-login-username";
    const el = document.getElementById(id) as HTMLInputElement | null;
    if (el && !el.disabled) el.focus();
  });
}

function checkLoginCapsLock(event: Event) {
  if (
    typeof KeyboardEvent !== "undefined" &&
    event instanceof KeyboardEvent &&
    typeof event.getModifierState === "function"
  ) {
    loginCapsLock.value = event.getModifierState("CapsLock");
  }
}
let timer: ReturnType<typeof setTimeout> | undefined;
const displayTime = (value?: string) =>
  value
    ? new Date(value).toLocaleString("zh-CN", {
        timeZone: "Asia/Shanghai",
        hour12: false,
      })
    : "—";
const uptime = computed(() => {
  const seconds = status.value?.uptimeSeconds || 0;
  return (
    Math.floor(seconds / 3600) + "h " + Math.floor((seconds % 3600) / 60) + "m"
  );
});
const credentialKindLabels: Record<string, string> = {
  TOKEN: "Token 密钥",
  PASSWORD: "密码凭据",
  SSH_KEY: "SSH 私钥",
};
const actionLabels: Record<string, string> = {
  ADMIN_INITIALIZED: "初始化管理员",
  LOGIN: "登录",
  LOGIN_FAILED: "登录失败",
  LOGOUT: "退出",
  PASSWORD_CHANGED: "修改密码",
  SETTINGS_UPDATED: "修改设置",
  CREDENTIAL_SAVED: "保存凭据",
  CREDENTIAL_REVOKED: "撤销凭据",
  BACKUP_CREATED: "创建备份",
  STATE_RESTORED: "恢复状态",
  HOST_PORTS_OBSERVED: "检测主机端口",
  FIREWALL_CHANGE_ACCEPTED: "提交防火墙变更",
  FIREWALL_CHANGE_RESULT: "防火墙变更结果",
  FIREWALL_JOB_CANCELLED: "取消防火墙作业",
  HOST_SAVED: "保存主机连接",
  HOST_CHECKED: "检查主机能力",
  HOST_SOFTWARE_DISCOVERED: "识别主机软件",
  HOST_MAINTENANCE_CHANGED: "修改主机维护状态",
  HOST_DELETED: "移除主机登记",
  HOST_PREPARATION_ACCEPTED: "开始主机接入",
  HOST_PREPARATION_RESULT: "主机接入结果",
  HOST_PREPARED: "准备主机运行依赖",
  SOFTWARE_REGISTERED: "登记已有软件",
  SOFTWARE_OBSERVED: "检查软件状态",
  SOFTWARE_OBSERVE_BATCH_ACCEPTED: "提交批量状态检测",
  SOFTWARE_OBSERVE_BATCH_RESULT: "批量状态检测结果",
  SOFTWARE_INSTALL_ACCEPTED: "提交服务安装",
  SOFTWARE_INSTALL_RESULT: "服务安装结果",
  SOFTWARE_ADOPT_ACCEPTED: "提交软件接管",
  SOFTWARE_START_ACCEPTED: "提交软件启动",
  SOFTWARE_STOP_ACCEPTED: "提交软件停止",
  SOFTWARE_UNMANAGE_ACCEPTED: "提交解除软件管理",
  SOFTWARE_ADOPT_RESULT: "软件接管结果",
  SOFTWARE_START_RESULT: "软件启动结果",
  SOFTWARE_STOP_RESULT: "软件停止结果",
  SOFTWARE_UNMANAGE_RESULT: "解除软件管理结果",
  SOFTWARE_UNMANAGED: "解除软件管理",
  ARTIFACT_UPLOADED: "上传版本制品",
  INSTANCE_SAVED: "保存应用实例",
  BINDING_SAVED: "保存外部依赖",
  DEPENDENCY_CHECKED: "检测依赖连通",
  DEPLOYMENT_CREATED: "创建发布作业",
  JOB_CANCELLED: "取消发布作业",
};
function report(e: unknown) {
  if (e instanceof ApiError) {
    diagnostic.value = e;
    if (e.code === "AUTH_REQUIRED") {
      loggedIn.value = false;
      password.value = "";
      void prepare();
    }
  }
  ElMessage.error(e instanceof Error ? e.message : "操作失败");
}
async function execute(action: () => Promise<void>) {
  if (busy.value) return;
  busy.value = true;
  try {
    await action();
  } catch (e) {
    report(e);
  } finally {
    busy.value = false;
  }
}
async function refreshStatus() {
  try {
    status.value = await request<Status>("/status");
    connected.value = true;
  } catch (e) {
    connected.value = false;
    throw e;
  }
}
async function loadAudit(cursor = "") {
  const data = await request<{ items: Audit[]; nextCursor: string }>(
    "/audit?limit=20&before=" + encodeURIComponent(cursor),
  );
  audits.value = data.items;
  nextCursor.value = data.nextCursor;
}
async function load() {
  const [config, items] = await Promise.all([
    request<Settings>("/settings"),
    request<Credential[]>("/credentials"),
    refreshStatus(),
    loadAudit(),
  ]);
  settings.value = config;
  nameDraft.value = config.name;
  credentials.value = items;
}
async function login() {
  loginError.value = "";
  await execute(async () => {
    await prepare();
    try {
      const data = await request<{ username: string; csrfToken: string }>(
        "/auth/login",
        "POST",
        { username: username.value, password: password.value },
      );
      setCSRF(data.csrfToken);
      username.value = data.username;
      password.value = "";
      loggedIn.value = true;
      diagnostic.value = null;
      await load();
    } catch (e) {
      loginError.value = e instanceof Error ? e.message : "登录失败";
    }
  });
}
async function logout() {
  await execute(async () => {
    await request("/auth/logout", "POST");
    loggedIn.value = false;
    diagnostic.value = null;
    await prepare();
  });
}
async function saveName() {
  await execute(async () => {
    await request("/settings", "PUT", {
      name: nameDraft.value,
      version: settings.value?.version,
    });
    await load();
    ElMessage.success("名称已保存");
  });
}
function openCredential(item: Credential | null = null) {
  selected.value = item;
  credentialName.value = item?.name || "";
  credentialKind.value = item?.kind || "TOKEN";
  secret.value = "";
  credentialDialog.value = true;
}
async function saveCredential() {
  await execute(async () => {
    const item = selected.value;
    await request(
      item ? "/credentials/" + item.id + "/revisions" : "/credentials",
      "POST",
      {
        name: credentialName.value,
        kind: credentialKind.value,
        secret: secret.value,
        ...(item ? { expectedRevision: item.revision } : {}),
      },
    );
    secret.value = "";
    credentialDialog.value = false;
    await load();
    ElMessage.success("凭据已保存");
  });
}
async function revoke(item: Credential) {
  try {
    await ElMessageBox.confirm(
      "撤销后该修订将不可使用，确认继续？",
      "撤销凭据",
      { confirmButtonText: "撤销", cancelButtonText: "取消", type: "warning" },
    );
  } catch {
    return;
  }
  await execute(async () => {
    await request(
      "/credentials/" + item.id + "/revisions/" + item.revision + "/revoke",
      "POST",
    );
    await load();
  });
}
function latest(item: Credential) {
  return !credentials.value.some(
    (other) =>
      other.id === item.id && Number(other.revision) > Number(item.revision),
  );
}
async function changePassword() {
  if (newPassword.value !== confirmPassword.value) {
    ElMessage.error("两次新密码不一致");
    return;
  }
  await execute(async () => {
    await request("/auth/password", "POST", {
      currentPassword: oldPassword.value,
      newPassword: newPassword.value,
    });
    oldPassword.value = "";
    newPassword.value = "";
    confirmPassword.value = "";
    passwordDialog.value = false;
    loggedIn.value = false;
    await prepare();
    ElMessage.success("密码已修改，请重新登录");
  });
}
async function backup() {
  await execute(async () => {
    const response = await send("/backups", "POST");
    if (!response.ok) {
      const body = await response.json();
      throw new ApiError(body.msg, body.code, body.traceId, body.timestamp);
    }
    const objectURL = URL.createObjectURL(await response.blob());
    const link = document.createElement("a");
    link.href = objectURL;
    link.download = "streamfusion-ops-backup.tar.gz";
    link.click();
    setTimeout(() => URL.revokeObjectURL(objectURL), 10000);
    await loadAudit();
  });
}
function poll() {
  timer = setTimeout(async () => {
    if (loggedIn.value && !document.hidden) {
      try {
        if (["credentials", "audit", "settings"].includes(active.value)) {
          await load();
        } else {
          await refreshStatus();
        }
      } catch (e) {
        report(e);
      }
    }
    poll();
  }, 15000);
}
watch(
  [title, loggedIn, () => settings.value?.name],
  () => {
    const siteName = settings.value?.name || "StreamFusion Ops";
    document.title = loggedIn.value
      ? `${title.value} · ${siteName}`
      : `控制器登录 · ${siteName}`;
  },
  { immediate: true },
);
watch(loggedIn, (value) => {
  if (!value) {
    showLoginPassword.value = false;
    loginCapsLock.value = false;
    focusLoginInput();
  }
});
watch(active, (page) => {
  if (loggedIn.value && ["credentials", "audit", "settings"].includes(page)) {
    void load().catch(report);
  }
});
onMounted(async () => {
  busy.value = true;
  try {
    await prepare();
    const me = await request<{ username: string }>("/auth/me");
    username.value = me.username;
    loggedIn.value = true;
    await load();
  } catch (e) {
    if (!(e instanceof ApiError && e.code === "AUTH_REQUIRED")) report(e);
  } finally {
    busy.value = false;
    if (!loggedIn.value) focusLoginInput();
    poll();
  }
});
onUnmounted(() => {
  if (timer) clearTimeout(timer);
});
</script>

<template>
  <div class="shell">
    <button
      v-if="loggedIn && navigationOpen"
      class="nav-backdrop"
      aria-label="收起导航"
      @click="navigationOpen = false"
    ></button>
    <aside
      v-if="loggedIn"
      id="ops-navigation"
      class="sidebar"
      :class="{ 'is-open': navigationOpen }"
    >
      <div class="brand">
        <img
          class="brand-logo"
          src="/streamfusion.svg"
          alt=""
          width="34"
          height="34"
        /><span>StreamFusion<small>运维控制台</small></span>
      </div>
      <nav aria-label="导航">
        <div
          v-for="group in navigationGroups"
          :key="group.label"
          class="nav-group"
        >
          <span class="nav-group-label">{{ group.label }}</span>
          <button
            v-for="item in group.items"
            :key="item.id"
            :class="{ active: active === item.id }"
            :aria-current="active === item.id ? 'page' : undefined"
            @click="
              active = item.id;
              navigationOpen = false;
            "
          >
            <svg class="nav-icon" viewBox="0 0 24 24" aria-hidden="true">
              <path :d="item.icon" />
            </svg>
            {{ item.label }}
          </button>
        </div>
      </nav>
      <div class="side-foot">
        {{ status?.os || "—" }} · {{ status?.arch || "—" }}<br /><span
          >v{{ status?.release || "—" }}</span
        >
      </div>
    </aside>
    <main class="main" :class="{ 'login-main': !loggedIn }">
      <header v-if="loggedIn">
        <button
          class="navigation-toggle"
          aria-label="展开导航"
          aria-controls="ops-navigation"
          :aria-expanded="navigationOpen"
          @click="navigationOpen = !navigationOpen"
        >
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M4 6h16M4 12h16M4 18h16" />
          </svg>
        </button>
        <h1>{{ title }}</h1>
        <div class="account">
          <svg class="account-icon" viewBox="0 0 24 24" aria-hidden="true">
            <circle cx="12" cy="8" r="3" />
            <path d="M5 21v-3a7 7 0 0 1 14 0v3" />
          </svg>
          <span>{{ username }}</span>
          <button class="quiet" :disabled="busy" @click="logout">退出</button>
        </div>
      </header>
      <div v-if="!loggedIn" class="ops-login-layout">
        <section class="ops-login-visual" aria-label="运维控制台概览">
          <div class="ops-visual-brand">
            <img
              class="brand-logo"
              src="/streamfusion.svg"
              alt=""
              width="38"
              height="38"
            />
            <span>StreamFusion Ops</span>
          </div>
          <div class="ops-visual-body">
            <div class="ops-illustration" aria-hidden="true">
              <svg viewBox="0 0 560 330" fill="none">
                <path
                  d="M142 92H232Q260 92 260 124V162M418 92H328Q300 92 300 124V162M142 248H232Q260 248 260 216V182M418 248H328Q300 248 300 216V182"
                  stroke="#93C5FD"
                  stroke-width="3"
                  stroke-dasharray="6 5"
                />
                <rect
                  x="32"
                  y="36"
                  width="150"
                  height="102"
                  rx="12"
                  fill="#FFFFFF"
                  stroke="#BFDBFE"
                  stroke-width="2"
                />
                <rect
                  x="48"
                  y="52"
                  width="118"
                  height="26"
                  rx="6"
                  fill="#EFF6FF"
                />
                <circle cx="62" cy="65" r="5" fill="#2563EB" />
                <path
                  d="M76 65h66M50 96h88M50 114h62"
                  stroke="#93C5FD"
                  stroke-width="4"
                  stroke-linecap="round"
                />
                <rect
                  x="378"
                  y="36"
                  width="150"
                  height="102"
                  rx="12"
                  fill="#FFFFFF"
                  stroke="#BFDBFE"
                  stroke-width="2"
                />
                <rect
                  x="396"
                  y="54"
                  width="32"
                  height="32"
                  rx="7"
                  fill="#DBEAFE"
                />
                <rect
                  x="438"
                  y="54"
                  width="32"
                  height="32"
                  rx="7"
                  fill="#DBEAFE"
                />
                <rect
                  x="480"
                  y="54"
                  width="32"
                  height="32"
                  rx="7"
                  fill="#EFF6FF"
                />
                <path
                  d="M398 108h104"
                  stroke="#93C5FD"
                  stroke-width="4"
                  stroke-linecap="round"
                />
                <rect
                  x="46"
                  y="206"
                  width="150"
                  height="92"
                  rx="12"
                  fill="#FFFFFF"
                  stroke="#BFDBFE"
                  stroke-width="2"
                />
                <path
                  d="M72 272v-24m28 24v-40m28 40v-18m28 18v-32"
                  stroke="#3B82F6"
                  stroke-width="11"
                  stroke-linecap="round"
                />
                <rect
                  x="364"
                  y="206"
                  width="150"
                  height="92"
                  rx="12"
                  fill="#FFFFFF"
                  stroke="#BFDBFE"
                  stroke-width="2"
                />
                <path
                  d="M388 232h20m12 0h68M388 252h20m12 0h52M388 272h20m12 0h68"
                  stroke="#93C5FD"
                  stroke-width="5"
                  stroke-linecap="round"
                />
                <rect
                  x="225"
                  y="117"
                  width="110"
                  height="110"
                  rx="26"
                  fill="#FFFFFF"
                  stroke="#93C5FD"
                  stroke-width="2.5"
                />
                <image
                  href="/streamfusion.svg"
                  x="246"
                  y="138"
                  width="68"
                  height="68"
                />
              </svg>
            </div>
            <h2>连接基础设施与服务编排</h2>
            <p class="ops-visual-subtitle">
              StreamFusion Ops · 运维与部署控制台
            </p>
            <div class="ops-visual-pills">
              <span v-for="item in opsHighlights" :key="item" class="ops-pill">{{
                item
              }}</span>
            </div>
          </div>
        </section>
        <section class="ops-login-region">
          <div class="login-card">
            <div class="login-intro">
              <div class="brand mobile-login-brand">
                <img
                  class="brand-logo"
                  src="/streamfusion.svg"
                  alt=""
                  width="34"
                  height="34"
                /><span>StreamFusion Ops</span>
              </div>
              <h1>控制器登录</h1>
            </div>
            <form @submit.prevent="login">
              <div class="login-field">
                <label for="ops-login-username">账号</label>
                <input
                  id="ops-login-username"
                  v-model="username"
                  autocomplete="username"
                  placeholder="请输入账号"
                  required
                  maxlength="32"
                  :disabled="busy"
                  @input="loginError = ''"
                />
              </div>
              <div class="login-field">
                <label for="ops-login-password">密码</label>
                <div class="password-input-wrap">
                  <input
                    id="ops-login-password"
                    v-model="password"
                    :type="showLoginPassword ? 'text' : 'password'"
                    autocomplete="current-password"
                    placeholder="请输入密码"
                    required
                    maxlength="64"
                    :disabled="busy"
                    @input="loginError = ''"
                    @keydown="checkLoginCapsLock"
                    @keyup="checkLoginCapsLock"
                    @blur="loginCapsLock = false"
                  />
                  <button
                    type="button"
                    class="password-eye-btn"
                    :aria-label="showLoginPassword ? '隐藏密码' : '显示密码'"
                    :aria-pressed="showLoginPassword"
                    @click="showLoginPassword = !showLoginPassword"
                  >
                    <svg
                      v-if="showLoginPassword"
                      viewBox="0 0 24 24"
                      aria-hidden="true"
                    >
                      <path
                        d="M3 3l18 18M10.6 10.6a2 2 0 0 0 2.8 2.8M9.9 5.1A10.4 10.4 0 0 1 12 5c5 0 9.3 3.1 11 7-0.7 1.6-1.8 3.1-3.2 4.2M6.2 6.2C4.3 7.5 2.8 9.5 2 12c1.7 3.9 6 7 10 7 1.6 0 3.1-.4 4.5-1"
                      />
                    </svg>
                    <svg v-else viewBox="0 0 24 24" aria-hidden="true">
                      <path
                        d="M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7-10-7-10-7Z"
                      />
                      <circle cx="12" cy="12" r="3" />
                    </svg>
                  </button>
                </div>
                <div
                  v-if="loginCapsLock"
                  class="caps-lock-notice"
                  role="status"
                >
                  已开启大写锁定 (Caps Lock)
                </div>
              </div>
              <p v-if="loginError" class="error login-alert" role="alert">
                {{ loginError }}
              </p>
              <button class="primary login-submit-btn" :disabled="busy">
                {{ busy ? "请稍候" : "登录" }}
              </button>
            </form>
          </div>
        </section>
      </div>
      <template v-else>
        <details v-if="diagnostic" class="error diagnostic">
          <summary>
            <span>{{ diagnostic.message }}</span>
            <button
              type="button"
              class="diagnostic-close"
              aria-label="关闭错误提示"
              @click.stop="diagnostic = null"
            >
              ×
            </button>
          </summary>
          <div>
            {{ diagnostic.code }} · {{ displayTime(diagnostic.timestamp) }}
          </div>
          <code>traceId: {{ diagnostic.traceId }}</code>
        </details>
        <HostManagement
          v-if="active === 'hosts'"
          :preparation-host-id="preparationHostId"
          @error="report"
          @preparation-opened="preparationHostId = ''"
          @credential-saved="load"
        />
        <PortManagement
          v-else-if="active === 'ports'"
          @error="report"
          @preparation="showHostPreparation"
        />
        <SoftwareManagement
          v-else-if="active === 'software'"
          @error="report"
          @preparation="showHostPreparation"
        />
        <OpsWorkspace
          v-else-if="['instances', 'artifacts', 'jobs'].includes(active)"
          :page="active"
          @error="report"
        />
        <HostOverview
          v-else-if="active === 'overview'"
          :controller="status"
          :connected="connected"
          :uptime="uptime"
          @error="report"
        />
        <section
          v-else-if="active === 'credentials'"
          class="content panel-card"
        >
          <div class="section-head">
            <h2>凭据修订</h2>
            <div class="section-actions">
              <button
                class="secondary"
                :disabled="busy"
                @click="execute(() => load())"
              >
                刷新
              </button>
              <button
                class="primary"
                :disabled="busy"
                @click="openCredential()"
              >
                新增凭据
              </button>
            </div>
          </div>
          <div v-if="credentials.length" class="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>名称</th>
                  <th>类型</th>
                  <th>修订</th>
                  <th>创建时间</th>
                  <th>状态</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="item in credentials" :key="item.id + item.revision">
                  <td>{{ item.name }}</td>
                  <td>{{ credentialKindLabels[item.kind] || item.kind }}</td>
                  <td>r{{ item.revision }}</td>
                  <td>{{ displayTime(item.createdAt) }}</td>
                  <td>
                    <span
                      class="state-tag"
                      :class="item.revokedAt ? 'state-muted' : 'state-success'"
                    >
                      {{ item.revokedAt ? "已撤销" : "可用" }}
                    </span>
                  </td>
                  <td>
                    <button
                      v-if="!item.revokedAt && latest(item)"
                      class="quiet"
                      :disabled="busy"
                      @click="openCredential(item)"
                    >
                      新修订</button
                    ><button
                      v-if="!item.revokedAt"
                      class="quiet danger"
                      :disabled="busy"
                      @click="revoke(item)"
                    >
                      撤销
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <div v-else class="empty">尚未保存凭据</div>
        </section>
        <section v-else-if="active === 'audit'" class="content panel-card">
          <div class="section-head">
            <h2>操作记录</h2>
            <button
              class="secondary"
              :disabled="busy"
              @click="execute(() => loadAudit())"
            >
              最新记录
            </button>
          </div>
          <div v-if="audits.length" class="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>时间</th>
                  <th>操作者</th>
                  <th>动作</th>
                  <th>结果</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="item in audits" :key="item.id">
                  <td>{{ displayTime(item.createdAt) }}</td>
                  <td>{{ item.actor }}</td>
                  <td>{{ actionLabels[item.action] || item.action }}</td>
                  <td>
                    <span
                      class="state-tag"
                      :class="
                        item.result === 'SUCCESS'
                          ? 'state-success'
                          : 'state-error'
                      "
                    >
                      {{ item.result === "SUCCESS" ? "成功" : "失败" }}
                    </span>
                  </td>
                  <td>
                    <button class="quiet" @click="auditDetail = item">
                      详情
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <div v-else class="empty">暂无操作记录</div>
          <button
            v-if="nextCursor"
            class="secondary pagination"
            :disabled="busy"
            @click="execute(() => loadAudit(nextCursor))"
          >
            更早记录
          </button>
        </section>
        <section v-else class="content settings">
          <div class="settings-meta">
            <div>
              <span>控制器 ID</span>
              <code>{{ settings?.controllerId || "—" }}</code>
            </div>
            <div>
              <span>监听地址</span>
              <code>{{ status?.listen || "—" }}</code>
            </div>
            <div>
              <span>服务版本</span>
              <code>v{{ status?.release || "—" }}</code>
            </div>
          </div>
          <form class="settings-form" @submit.prevent="saveName">
            <label
              >控制器名称<input
                v-model="nameDraft"
                maxlength="60"
                required /></label
            ><button class="secondary" :disabled="busy">保存名称</button>
          </form>
          <div class="section-head"><h2>备份与账户</h2></div>
          <div class="setting-action">
            <div>
              <strong>状态备份</strong>
            </div>
            <button class="secondary" :disabled="busy" @click="backup">
              下载备份
            </button>
          </div>
          <div class="setting-action">
            <div>
              <strong>管理员密码</strong>
            </div>
            <button
              class="secondary"
              :disabled="busy"
              @click="passwordDialog = true"
            >
              修改密码
            </button>
          </div>
        </section>
      </template>
    </main>
    <ElDialog
      v-model="credentialDialog"
      :title="selected ? '创建新修订' : '新增凭据'"
      width="min(480px, 94vw)"
      align-center
      @closed="secret = ''"
    >
      <form class="dialog-form" @submit.prevent="saveCredential">
        <label
          >名称<input v-model="credentialName" required maxlength="60" /></label
        ><label
          >类型<select v-model="credentialKind" :disabled="!!selected">
            <option value="TOKEN">Token</option>
            <option value="PASSWORD">密码</option>
            <option value="SSH_KEY">SSH 私钥</option>
          </select></label
        ><label
          >凭据内容<textarea
            v-model="secret"
            required
            maxlength="32768"
            spellcheck="false"
            autocomplete="off"
          ></textarea></label
        >
        <div class="dialog-footer">
          <button
            type="button"
            class="secondary"
            :disabled="busy"
            @click="credentialDialog = false"
          >
            取消
          </button>
          <button class="primary" :disabled="busy">保存</button>
        </div>
      </form>
    </ElDialog>
    <ElDialog
      v-model="passwordDialog"
      title="修改密码"
      width="min(440px, 94vw)"
      align-center
      @closed="
        oldPassword = '';
        newPassword = '';
        confirmPassword = '';
      "
    >
      <form class="dialog-form" @submit.prevent="changePassword">
        <label
          >当前密码<input
            v-model="oldPassword"
            type="password"
            autocomplete="current-password"
            required /></label
        ><label
          >新密码<input
            v-model="newPassword"
            type="password"
            autocomplete="new-password"
            required /></label
        ><small>6–64字节，包含英文字母和数字</small
        ><label
          >确认新密码<input
            v-model="confirmPassword"
            type="password"
            autocomplete="new-password"
            required
        /></label>
        <div class="dialog-footer">
          <button
            type="button"
            class="secondary"
            :disabled="busy"
            @click="passwordDialog = false"
          >
            取消
          </button>
          <button class="primary" :disabled="busy">修改并重新登录</button>
        </div>
      </form>
    </ElDialog>
    <ElDialog
      :model-value="!!auditDetail"
      title="记录详情"
      width="min(540px, 94vw)"
      align-center
      @close="auditDetail = null"
      ><dl v-if="auditDetail" class="audit-detail">
        <dt>时间</dt>
        <dd>{{ displayTime(auditDetail.createdAt) }}</dd>
        <dt>动作</dt>
        <dd>{{ actionLabels[auditDetail.action] || auditDetail.action }}</dd>
        <dt>对象</dt>
        <dd>{{ auditDetail.objectId || "—" }}</dd>
        <dt>请求标识</dt>
        <dd>{{ auditDetail.traceId || "本机操作" }}</dd>
      </dl></ElDialog
    >
  </div>
</template>
