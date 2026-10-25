<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { ElDialog, ElDrawer, ElTabs, ElTabPane, ElMessage } from "element-plus";
import { request } from "./api";
import {
  hostTime,
  type Host,
  type HostFingerprint,
  type SSHCredential,
} from "./hosts";

const props = defineProps<{ open: boolean; host?: Host }>();
const emit = defineEmits<{
  close: [];
  saved: [Host];
  credentialSaved: [];
  error: [unknown];
}>();
const busy = ref(false);
const credentials = ref<SSHCredential[]>([]);
const fingerprint = ref<HostFingerprint>();
const confirmed = ref(false);
const credentialDialog = ref(false);
const credentialName = ref("");
const credentialKind = ref("SSH_KEY");
const credentialSecret = ref("");
const draft = ref(emptyDraft());
const formTab = ref("connection");
const hostForm = ref<HTMLFormElement>();
async function validateForm() {
  const field = hostForm.value?.querySelector<
    HTMLInputElement | HTMLSelectElement
  >("input:invalid, select:invalid, textarea:invalid");
  if (!field) return true;
  formTab.value =
    field.closest("[data-form-panel]")?.getAttribute("data-form-panel") ||
    "connection";
  await nextTick();
  field.reportValidity();
  return false;
}

function emptyDraft() {
  return {
    name: "",
    kind: "SSH" as "SSH" | "LOCAL",
    address: "",
    sshPort: 22,
    sshUser: "",
    credentialId: "",
    installRoot: "/opt/streamfusion-ops-target",
  };
}

const credentialOptions = computed(() =>
  credentials.value.filter(
    (candidate) =>
      ["SSH_KEY", "PASSWORD"].includes(candidate.kind) &&
      !candidate.revokedAt &&
      !credentials.value.some(
        (other) =>
          other.id === candidate.id &&
          !other.revokedAt &&
          Number(other.revision) > Number(candidate.revision),
      ),
  ),
);
const selectedCredential = computed(() =>
  credentialOptions.value.find((item) => item.id === draft.value.credentialId),
);
const unchangedConnection = computed(() => {
  const host = props.host;
  return (
    !!host &&
    host.kind === draft.value.kind &&
    host.address === draft.value.address.trim() &&
    host.sshPort === draft.value.sshPort &&
    host.sshUser === draft.value.sshUser.trim() &&
    host.credentialId === draft.value.credentialId &&
    host.credentialRevision === selectedCredential.value?.revision
  );
});
const trustedConnection = computed(
  () =>
    draft.value.kind === "LOCAL" ||
    unchangedConnection.value ||
    (!!fingerprint.value && confirmed.value),
);

watch(
  () => props.open,
  async (open) => {
    if (!open) return;
    formTab.value = "connection";
    draft.value = props.host
      ? {
          name: props.host.name,
          kind: props.host.kind,
          address: props.host.address,
          sshPort: props.host.sshPort || 22,
          sshUser: props.host.sshUser,
          credentialId: props.host.credentialId,
          installRoot: props.host.installRoot,
        }
      : emptyDraft();
    fingerprint.value = undefined;
    confirmed.value = false;
    try {
      credentials.value = await request<SSHCredential[]>("/credentials");
    } catch (error) {
      emit("error", error);
    }
  },
  { immediate: true },
);

watch(
  () => [
    draft.value.kind,
    draft.value.address,
    draft.value.sshPort,
    draft.value.sshUser,
    draft.value.credentialId,
  ],
  () => {
    fingerprint.value = undefined;
    confirmed.value = false;
  },
);

async function act(action: () => Promise<void>) {
  if (busy.value) return;
  busy.value = true;
  try {
    await action();
  } catch (error) {
    emit("error", error);
  } finally {
    busy.value = false;
  }
}

async function discoverFingerprint() {
  if (
    !draft.value.address.trim() ||
    !draft.value.sshUser.trim() ||
    !draft.value.credentialId ||
    !Number.isInteger(draft.value.sshPort) ||
    draft.value.sshPort < 1 ||
    draft.value.sshPort > 65535
  ) {
    ElMessage.warning("请先填写地址、SSH 端口、用户并选择凭据");
    return;
  }
  await act(async () => {
    fingerprint.value = undefined;
    confirmed.value = false;
    fingerprint.value = await request<HostFingerprint>(
      "/hosts/fingerprint",
      "POST",
      {
        address: draft.value.address.trim(),
        sshPort: draft.value.sshPort,
        sshUser: draft.value.sshUser.trim(),
        credentialId: draft.value.credentialId,
      },
    );
  });
}

async function save() {
  if (!(await validateForm())) return;
  if (!trustedConnection.value) {
    ElMessage.warning("请获取并确认主机指纹");
    return;
  }
  if (
    fingerprint.value &&
    Date.parse(fingerprint.value.expiresAt) <= Date.now()
  ) {
    fingerprint.value = undefined;
    confirmed.value = false;
    ElMessage.warning("指纹确认已过期，请重新获取");
    return;
  }
  await act(async () => {
    const host = props.host;
    const saved = await request<Host>(
      host ? `/hosts/${host.id}` : "/hosts",
      host ? "PATCH" : "POST",
      {
        name: draft.value.name.trim(),
        kind: draft.value.kind,
        installRoot: draft.value.installRoot.trim(),
        ...(host ? { expectedVersion: host.version } : {}),
        ...(draft.value.kind === "SSH"
          ? {
              address: draft.value.address.trim(),
              sshPort: draft.value.sshPort,
              sshUser: draft.value.sshUser.trim(),
              credentialId: draft.value.credentialId,
              ...(fingerprint.value
                ? { fingerprintToken: fingerprint.value.fingerprintToken }
                : {}),
            }
          : {}),
      },
    );
    emit("saved", saved);
    emit("close");
    ElMessage.success(host ? "主机已更新" : "主机已添加");
  });
}

async function saveCredential() {
  await act(async () => {
    const name = credentialName.value.trim();
    const kind = credentialKind.value;
    const created = await request<{ id: string; revision: string }>(
      "/credentials",
      "POST",
      {
        name,
        kind,
        secret: credentialSecret.value,
      },
    );
    credentialSecret.value = "";
    credentialDialog.value = false;
    credentials.value = [
      {
        id: created.id,
        revision: created.revision,
        name,
        kind,
        revokedAt: "",
      },
      ...credentials.value.filter(
        (item) =>
          !(item.id === created.id && item.revision === created.revision),
      ),
    ];
    try {
      credentials.value = await request<SSHCredential[]>("/credentials");
    } catch {
      // Keep optimistic entry when refresh fails
    }
    draft.value.credentialId = created.id;
    emit("credentialSaved");
    ElMessage.success("SSH 凭据已保存");
  });
}

function close() {
  if (!busy.value) emit("close");
}
</script>

<template>
  <ElDrawer
    :model-value="open"
    :title="host ? '维护主机连接' : '新增主机'"
    size="min(640px, 100vw)"
    destroy-on-close
    :close-on-click-modal="false"
    :close-on-press-escape="!busy"
    :show-close="!busy"
    @close="close"
  >
    <form
      id="host-connection-form"
      class="host-form"
      @submit.prevent="save"
      ref="hostForm"
      novalidate
    >
      <ElTabs v-model="formTab">
        <ElTabPane label="连接设置" name="connection">
          <fieldset data-form-panel="connection" :disabled="busy">
            <label>
              主机名称
              <input
                v-model="draft.name"
                required
                maxlength="60"
                autocomplete="off"
                placeholder="例如：分析服务器 A"
              />
            </label>
            <template v-if="draft.kind === 'SSH'">
              <div class="connection-grid">
                <label>
                  IP / 域名
                  <input
                    v-model="draft.address"
                    required
                    maxlength="253"
                    autocomplete="off"
                    placeholder="192.168.1.10"
                  />
                </label>
                <label>
                  SSH 端口
                  <input
                    v-model.number="draft.sshPort"
                    required
                    type="number"
                    min="1"
                    max="65535"
                  />
                </label>
              </div>
              <label>
                SSH 用户
                <input
                  v-model="draft.sshUser"
                  required
                  maxlength="64"
                  autocomplete="off"
                  placeholder="登录服务器的账号"
                />
              </label>
              <div class="credential-row">
                <label>
                  SSH 凭据
                  <select
                    v-model="draft.credentialId"
                    aria-label="SSH 凭据"
                    required
                  >
                    <option value="">选择已保存的私钥或密码</option>
                    <option
                      v-for="item in credentialOptions"
                      :key="item.id"
                      :value="item.id"
                    >
                      {{ item.name }} ·
                      {{ item.kind === "SSH_KEY" ? "私钥" : "密码" }}
                    </option>
                  </select>
                </label>
                <button
                  type="button"
                  class="secondary"
                  @click="
                    credentialName = '';
                    credentialSecret = '';
                    credentialKind = 'SSH_KEY';
                    credentialDialog = true;
                  "
                >
                  新增凭据
                </button>
              </div>
              <div class="fingerprint-panel">
                <div class="fingerprint-head">
                  <span>主机身份</span>
                  <button
                    type="button"
                    class="quiet"
                    @click="discoverFingerprint"
                  >
                    {{
                      busy
                        ? "获取中…"
                        : fingerprint || unchangedConnection
                          ? "重新获取指纹"
                          : "获取主机指纹"
                    }}
                  </button>
                </div>
                <template v-if="fingerprint">
                  <code>{{ fingerprint.hostFingerprint }}</code>
                  <label class="fingerprint-confirm">
                    <input v-model="confirmed" type="checkbox" />
                    <span>已通过服务器控制台或管理员核对该指纹</span>
                  </label>
                  <small>有效至 {{ hostTime(fingerprint.expiresAt) }}</small>
                </template>
                <template v-else-if="unchangedConnection">
                  <code>{{ host?.hostFingerprint }}</code>
                  <small>已确认</small>
                </template>
              </div>
            </template>
            <details class="advanced-fields">
              <summary>高级配置</summary>
              <label>
                连接方式
                <select
                  v-model="draft.kind"
                  aria-label="连接方式"
                  :disabled="!!host"
                >
                  <option value="SSH">SSH 远程主机</option>
                  <option value="LOCAL">控制器本机</option>
                </select>
              </label>
            </details>
          </fieldset>
        </ElTabPane>
        <ElTabPane label="项目目录" name="directory">
          <fieldset data-form-panel="directory" :disabled="busy">
            <label>
              项目目录
              <input
                v-model="draft.installRoot"
                required
                maxlength="240"
                autocomplete="off"
                placeholder="/opt/streamfusion-ops-target"
                aria-label="项目目录"
              />
            </label>
          </fieldset>
        </ElTabPane>
      </ElTabs>
    </form>
    <template #footer>
      <footer class="dialog-actions">
        <button type="button" class="secondary" :disabled="busy" @click="close">
          取消
        </button>
        <button
          class="primary"
          form="host-connection-form"
          :disabled="busy || !trustedConnection"
        >
          {{ busy ? "处理中…" : host ? "保存连接" : "添加主机" }}
        </button>
      </footer>
    </template>
  </ElDrawer>
  <ElDialog
    v-model="credentialDialog"
    title="新增 SSH 凭据"
    width="min(520px, 94vw)"
    align-center
    destroy-on-close
    append-to-body
    :close-on-click-modal="false"
    :close-on-press-escape="!busy"
    :show-close="!busy"
    @closed="credentialSecret = ''"
  >
    <form class="host-form" @submit.prevent="saveCredential">
      <fieldset :disabled="busy">
        <label>
          凭据名称
          <input
            v-model="credentialName"
            required
            maxlength="60"
            autocomplete="off"
          />
        </label>
        <label>
          凭据类型
          <select
            v-model="credentialKind"
            aria-label="凭据类型"
            @change="credentialSecret = ''"
          >
            <option value="SSH_KEY">SSH 私钥</option>
            <option value="PASSWORD">SSH 密码</option>
          </select>
        </label>
        <label v-if="credentialKind === 'SSH_KEY'">
          SSH 私钥
          <textarea
            v-model="credentialSecret"
            required
            maxlength="32768"
            spellcheck="false"
            autocomplete="off"
            placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
          ></textarea>
        </label>
        <label v-else>
          SSH 密码
          <input
            v-model="credentialSecret"
            type="password"
            required
            maxlength="32768"
            autocomplete="new-password"
          />
        </label>
      </fieldset>
      <footer class="dialog-actions">
        <button
          type="button"
          class="secondary"
          :disabled="busy"
          @click="credentialDialog = false"
        >
          取消
        </button>
        <button class="primary" :disabled="busy">保存凭据</button>
      </footer>
    </form>
  </ElDialog>
</template>

<style scoped>
.host-form,
fieldset {
  display: grid;
  gap: 24px;
  margin: 0;
}
fieldset {
  min-width: 0;
  padding: 0;
  border: 0;
}
.host-form label {
  min-width: 0;
}
.root-help {
  color: var(--muted);
  font-size: 12px;
  line-height: 1.6;
  margin-top: 6px;
}
.host-form input:not([type="checkbox"]),
.host-form select {
  height: 40px;
}
.connection-grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 130px;
  gap: 16px;
}
.credential-row {
  display: flex;
  align-items: end;
  gap: 10px;
}
.credential-row label {
  flex: 1;
}
.credential-row button {
  height: 40px;
  flex-shrink: 0;
}
.fingerprint-panel {
  display: grid;
  gap: 12px;
  padding: 14px 16px;
  background: #f5f8fc;
  border: 1px solid var(--border);
  border-radius: 4px;
}
.fingerprint-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  font-size: 13px;
}
.fingerprint-head .quiet {
  margin: -5px -8px -5px 0;
}
.fingerprint-panel code {
  font: 12px/1.7 var(--mono);
  overflow-wrap: anywhere;
}
.fingerprint-confirm {
  display: flex;
  align-items: start;
  gap: 8px;
  font-size: 12px;
  line-height: 1.6;
}
.fingerprint-confirm input {
  width: 15px;
  height: 15px;
  min-height: 0;
  margin: 2px 0 0;
  padding: 0;
  flex-shrink: 0;
  accent-color: var(--accent);
}
.advanced-fields {
  border-top: 1px solid var(--border);
  padding-top: 16px;
}
.advanced-fields summary {
  color: var(--muted);
  cursor: pointer;
  font-size: 13px;
}
.advanced-fields label {
  margin-top: 16px;
}
.dialog-actions {
  display: flex;
  justify-content: end;
  gap: 10px;
  padding-top: 4px;
}
.dialog-actions .primary {
  width: auto;
  margin: 0;
}
textarea {
  min-height: 180px;
  resize: vertical;
  font: 12px/1.6 var(--mono);
}
@media (max-width: 450px) {
  .connection-grid {
    grid-template-columns: minmax(0, 1fr) 105px;
    gap: 12px;
  }
  .credential-row {
    flex-wrap: wrap;
  }
  .credential-row label {
    flex-basis: 100%;
  }
  .fingerprint-head {
    flex-wrap: wrap;
  }
}
</style>
