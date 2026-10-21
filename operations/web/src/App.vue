<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
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
  SOFTWARE_REGISTERED: "登记已有软件",
  SOFTWARE_OBSERVED: "检查软件状态",
  SOFTWARE_ADOPT_ACCEPTED: "提交软件接管",
  SOFTWARE_START_ACCEPTED: "提交软件启动",
  SOFTWARE_STOP_ACCEPTED: "提交软件停止",
  SOFTWARE_UNMANAGE_ACCEPTED: "提交解除软件管理",
  SOFTWARE_ADOPT_RESULT: "软件接管结果",
  SOFTWARE_START_RESULT: "软件启动结果",
  SOFTWARE_STOP_RESULT: "软件停止结果",
  SOFTWARE_UNMANAGE_RESULT: "解除软件管理结果",
  SOFTWARE_UNMANAGED: "解除软件管理",
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
        expectedRevision: item?.revision || "",
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
        await refreshStatus();
      } catch (e) {
        report(e);
      }
    }
    poll();
  }, 15000);
}
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
        <span class="mark">S</span
        ><span>StreamFusion<small>运维控制台</small></span>
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
      <section v-if="!loggedIn" class="login-card">
        <div class="login-intro">
          <div class="brand">
            <span class="mark">S</span><span>StreamFusion Ops</span>
          </div>
          <h1>控制器登录</h1>
        </div>
        <form @submit.prevent="login">
          <label
            >账号<input
              v-model="username"
              autocomplete="username"
              required
              maxlength="32"
          /></label>
          <label
            >密码<input
              v-model="password"
              type="password"
              autocomplete="current-password"
              required
              maxlength="64"
          /></label>
          <p v-if="loginError" class="error" role="alert">{{ loginError }}</p>
          <button class="primary" :disabled="busy">
            {{ busy ? "请稍候" : "登录" }}
          </button>
        </form>
      </section>
      <template v-else>
        <details v-if="diagnostic" class="error diagnostic">
          <summary>{{ diagnostic.message }}</summary>
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
        <section v-else-if="active === 'credentials'" class="content">
          <div class="section-head">
            <h2>凭据修订</h2>
            <button
              class="secondary"
              :disabled="busy"
              @click="openCredential()"
            >
              新增凭据
            </button>
          </div>
          <div v-if="credentials.length" class="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>名称</th>
                  <th>类型</th>
                  <th>修订</th>
                  <th>状态</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="item in credentials" :key="item.id + item.revision">
                  <td>{{ item.name }}</td>
                  <td>{{ item.kind }}</td>
                  <td>r{{ item.revision }}</td>
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
        <section v-else-if="active === 'audit'" class="content">
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
          <div class="table-scroll">
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
              <p>下载数据库与加密凭据；恢复密钥需在本机单独导出。</p>
            </div>
            <button class="secondary" :disabled="busy" @click="backup">
              下载备份
            </button>
          </div>
          <div class="setting-action">
            <div><strong>管理员密码</strong></div>
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
        ><button class="primary" :disabled="busy">保存</button>
      </form>
    </ElDialog>
    <ElDialog
      v-model="passwordDialog"
      title="修改密码"
      width="min(440px, 94vw)"
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
            required /></label
        ><button class="primary" :disabled="busy">修改并重新登录</button>
      </form>
    </ElDialog>
    <ElDialog
      :model-value="!!auditDetail"
      title="记录详情"
      width="min(540px, 94vw)"
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
