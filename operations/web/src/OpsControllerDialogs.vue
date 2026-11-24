<script setup lang="ts">
import { ref } from "vue";
import { ElDialog } from "element-plus";

export interface ControllerCredential {
  id: string;
  revision: string;
  name: string;
  kind: string;
  createdAt: string;
  revokedAt: string;
}

export interface ControllerAudit {
  id: string;
  actor: string;
  action: string;
  result: string;
  createdAt: string;
  traceId: string;
  objectId: string;
}

defineProps<{
  credentialDialog: boolean;
  selected: ControllerCredential | null;
  credentialName: string;
  credentialKind: string;
  secret: string;
  passwordDialog: boolean;
  oldPassword: string;
  newPassword: string;
  confirmPassword: string;
  auditDetail: ControllerAudit | null;
  busy: boolean;
  actionLabels: Record<string, string>;
  auditResult: (action: string, result: string) => { label: string; tone: string };
}>();

const emit = defineEmits<{
  "update:credentialDialog": [value: boolean];
  "update:credentialName": [value: string];
  "update:credentialKind": [value: string];
  "update:secret": [value: string];
  "update:passwordDialog": [value: boolean];
  "update:oldPassword": [value: string];
  "update:newPassword": [value: string];
  "update:confirmPassword": [value: string];
  "update:auditDetail": [value: ControllerAudit | null];
  saveCredential: [];
  changePassword: [];
}>();

const revealCredentialSecret = ref(false);

function generateRandomSecret() {
  const alphabet =
    "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#$%^&*-_";
  const bytes = new Uint8Array(24);
  crypto.getRandomValues(bytes);
  let next = "";
  for (const b of bytes) next += alphabet[b % alphabet.length];
  emit("update:secret", next);
  revealCredentialSecret.value = true;
}

const displayTime = (value?: string) =>
  value
    ? new Date(value).toLocaleString("zh-CN", {
        timeZone: "Asia/Shanghai",
        hour12: false,
      })
    : "—";
</script>

<template>
  <ElDialog
    :model-value="credentialDialog"
    :title="selected ? '创建新修订' : '新增凭据'"
    width="min(480px, 94vw)"
    align-center
    @update:model-value="emit('update:credentialDialog', $event)"
    @closed="
      revealCredentialSecret = false;
      emit('update:secret', '');
    "
  >
    <form class="dialog-form" @submit.prevent="emit('saveCredential')">
      <label
        >名称<input
          :value="credentialName"
          required
          maxlength="60"
          @input="
            emit(
              'update:credentialName',
              ($event.target as HTMLInputElement).value,
            )
          " /></label
      ><label
        >类型<select
          :value="credentialKind"
          :disabled="!!selected"
          @change="
            revealCredentialSecret = false;
            emit(
              'update:credentialKind',
              ($event.target as HTMLSelectElement).value,
            );
          "
        >
          <option value="TOKEN">Token</option>
          <option value="PASSWORD">密码</option>
          <option value="SSH_KEY">SSH 私钥</option>
        </select></label
      ><label v-if="credentialKind === 'SSH_KEY'"
        >SSH 私钥内容<textarea
          :value="secret"
          required
          maxlength="32768"
          spellcheck="false"
          autocomplete="off"
          placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
          @input="
            emit('update:secret', ($event.target as HTMLTextAreaElement).value)
          "
        ></textarea>
      </label>
      <div v-else class="credential-secret-block">
        <div class="secret-label-row">
          <span>{{ credentialKind === "TOKEN" ? "Token 密钥" : "密码内容" }}</span>
          <button
            type="button"
            class="quiet"
            :disabled="busy"
            @click="generateRandomSecret"
          >
            随机生成
          </button>
        </div>
        <div class="secret-input-row">
          <input
            :value="secret"
            :type="revealCredentialSecret ? 'text' : 'password'"
            required
            maxlength="32768"
            spellcheck="false"
            autocomplete="new-password"
            @input="
              emit('update:secret', ($event.target as HTMLInputElement).value)
            "
          />
          <button
            type="button"
            class="secondary"
            :disabled="busy"
            @click="revealCredentialSecret = !revealCredentialSecret"
          >
            {{ revealCredentialSecret ? "隐藏" : "显示" }}
          </button>
        </div>
      </div>
      <div class="dialog-footer">
        <button
          type="button"
          class="secondary"
          :disabled="busy"
          @click="emit('update:credentialDialog', false)"
        >
          取消
        </button>
        <button class="primary" :disabled="busy">保存</button>
      </div>
    </form>
  </ElDialog>

  <ElDialog
    :model-value="passwordDialog"
    title="修改密码"
    width="min(440px, 94vw)"
    align-center
    @update:model-value="emit('update:passwordDialog', $event)"
    @closed="
      emit('update:oldPassword', '');
      emit('update:newPassword', '');
      emit('update:confirmPassword', '');
    "
  >
    <form class="dialog-form" @submit.prevent="emit('changePassword')">
      <label
        >当前密码<input
          :value="oldPassword"
          type="password"
          autocomplete="current-password"
          required
          @input="
            emit('update:oldPassword', ($event.target as HTMLInputElement).value)
          " /></label
      ><label
        >新密码<input
          :value="newPassword"
          type="password"
          autocomplete="new-password"
          required
          @input="
            emit('update:newPassword', ($event.target as HTMLInputElement).value)
          " /></label
      ><small>6–64字节，包含英文字母和数字</small
      ><label
        >确认新密码<input
          :value="confirmPassword"
          type="password"
          autocomplete="new-password"
          required
          @input="
            emit(
              'update:confirmPassword',
              ($event.target as HTMLInputElement).value,
            )
          "
      /></label>
      <div class="dialog-footer">
        <button
          type="button"
          class="secondary"
          :disabled="busy"
          @click="emit('update:passwordDialog', false)"
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
    @close="emit('update:auditDetail', null)"
    ><dl v-if="auditDetail" class="audit-detail">
      <dt>时间</dt>
      <dd>{{ displayTime(auditDetail.createdAt) }}</dd>
      <dt>动作</dt>
      <dd>{{ actionLabels[auditDetail.action] || "其他操作" }}</dd>
      <dt>结果</dt>
      <dd>{{ auditResult(auditDetail.action, auditDetail.result).label }}</dd>
      <dt>对象</dt>
      <dd>{{ auditDetail.objectId || "—" }}</dd>
      <dt>请求标识</dt>
      <dd>{{ auditDetail.traceId || "本机操作" }}</dd>
      <dt>诊断信息</dt>
      <dd>
        <details>
          <summary>原始标识</summary>
          <code>{{ auditDetail.action }}</code
          ><br /><code>{{ auditDetail.result || "—" }}</code>
        </details>
      </dd>
    </dl></ElDialog
  >
</template>

<style scoped>
.credential-secret-block {
  display: grid;
  gap: 8px;
}
.secret-label-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 13px;
  font-weight: 500;
  color: #334155;
}
.secret-input-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.secret-input-row input {
  flex: 1;
  margin-top: 0;
}
.secret-input-row button {
  flex-shrink: 0;
  min-height: 38px;
}
</style>
