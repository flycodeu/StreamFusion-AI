<script setup lang="ts">
import { ref, watch } from 'vue'
import { ElButton, ElDialog } from 'element-plus'
import type {
  DirectZlmInput,
  DirectZlmInstance,
  PreviewMaintenance,
  PreviewNodeConnectionInput,
  PreviewNodeStatus,
} from '../../api/preview-node/api'
import RequestError from '../../components/feedback/RequestError.vue'
import { formatDateTime } from '../../utils/dateTime'
import { labelOf } from '../../utils/display'
import {
  directBudgetFields as directFields,
  nodeBudgetFields as fields,
  previewMaintenanceLabels as maintenanceNames,
  previewNodeLabels as names,
} from './nodePresentation'

export interface DirectEditingDraft {
  instance: DirectZlmInstance
  priority: number
  budget: DirectZlmInstance['budget']
  installPath: string
}

export interface DirectCredentialDraft {
  instance: DirectZlmInstance
  secret: string
}

const props = defineProps<{
  busy: boolean
  error: unknown
  directForm: DirectZlmInput | null
  directEditing: DirectEditingDraft | null
  directCredential: DirectCredentialDraft | null
  directRemoving: DirectZlmInstance | null
  connection: PreviewNodeConnectionInput | null
  maintaining: PreviewNodeStatus | null
  maintenance: PreviewMaintenance | null
  diagnostic: PreviewNodeStatus | null
  editing: PreviewNodeStatus | null
}>()

const emit = defineEmits<{
  closeDirectForm: []
  saveDirectForm: [draft: DirectZlmInput]
  closeDirectEditing: []
  saveDirectEditing: [draft: DirectEditingDraft]
  closeDirectCredential: []
  saveDirectCredential: [draft: DirectCredentialDraft]
  closeDirectRemoving: []
  confirmRemoveDirect: []
  closeConnection: []
  saveConnection: [draft: PreviewNodeConnectionInput]
  closeMaintaining: []
  maintain: [action?: 'PREPARE' | 'RELEASE']
  closeDiagnostic: []
  closeEditing: []
  saveEditing: [draft: PreviewNodeStatus]
}>()

const directDraft = ref<DirectZlmInput | null>(null)
const directEditingDraft = ref<DirectEditingDraft | null>(null)
const directCredentialDraft = ref<DirectCredentialDraft | null>(null)
const connectionDraft = ref<PreviewNodeConnectionInput | null>(null)
const editingDraft = ref<PreviewNodeStatus | null>(null)

function syncDraft<T>(source: () => T | null, target: { value: T | null }, clone: (value: T) => T) {
  watch(
    source,
    (value) => {
      target.value = value ? clone(value) : null
    },
    { immediate: true },
  )
}

syncDraft(
  () => props.directForm,
  directDraft,
  (value) => ({ ...value, budget: { ...value.budget } }),
)
syncDraft(
  () => props.directEditing,
  directEditingDraft,
  (value) => ({ ...value, budget: { ...value.budget } }),
)
syncDraft(
  () => props.directCredential,
  directCredentialDraft,
  (value) => ({ ...value }),
)
syncDraft(
  () => props.connection,
  connectionDraft,
  (value) => ({ ...value }),
)
syncDraft(
  () => props.editing,
  editingDraft,
  (value) => ({ ...value, budget: { ...value.budget } }),
)

function submitDirectForm() {
  if (props.busy || !directDraft.value) return
  const payload: DirectZlmInput = {
    ...directDraft.value,
    budget: { ...directDraft.value.budget },
  }
  directDraft.value.secret = ''
  emit('saveDirectForm', payload)
}

function submitDirectEditing() {
  if (!directEditingDraft.value) return
  emit('saveDirectEditing', {
    ...directEditingDraft.value,
    budget: { ...directEditingDraft.value.budget },
  })
}

function submitDirectCredential() {
  if (props.busy || !directCredentialDraft.value?.secret) return
  const payload: DirectCredentialDraft = { ...directCredentialDraft.value }
  directCredentialDraft.value.secret = ''
  emit('saveDirectCredential', payload)
}

function submitConnection() {
  if (!connectionDraft.value) return
  emit('saveConnection', { ...connectionDraft.value })
}

function submitEditing() {
  if (!editingDraft.value) return
  emit('saveEditing', { ...editingDraft.value, budget: { ...editingDraft.value.budget } })
}
</script>

<template>
  <ElDialog
    :model-value="!!directDraft"
    class="management-dialog"
    title="接入 ZLM"
    width="min(620px, calc(100vw - 32px))"
    align-center
    append-to-body
    destroy-on-close
    @close="emit('closeDirectForm')"
  >
    <form v-if="directDraft" class="budget-form" @submit.prevent="submitDirectForm">
      <p class="connection-form-intro">填写内网 HTTP API；安装位置仅用于登记。</p>
      <label>实例名称<input v-model.trim="directDraft.name" maxlength="80" required /></label>
      <label
        >内网 IP<input v-model.trim="directDraft.apiHost" placeholder="10.0.0.10" required
      /></label>
      <label
        >HTTP 端口<input
          v-model.number="directDraft.apiPort"
          type="number"
          min="1"
          max="65535"
          required
      /></label>
      <label
        >安装位置（选填）<input
          v-model.trim="directDraft.installPath"
          maxlength="512"
          placeholder="例如 /opt/zlm"
        />
      </label>
      <label
        >ZLM secret<input
          v-model="directDraft.secret"
          type="password"
          autocomplete="new-password"
          required
      /></label>
      <details class="form-advanced">
        <summary>高级设置：媒体地址与预算</summary>
        <div class="budget-form form-advanced-fields">
          <label
            >浏览器可达媒体地址<input
              v-model.trim="directDraft.mediaAddress"
              :placeholder="directDraft.apiHost || '默认同内网 IP'"
          /></label>
          <label
            >优先级<input
              v-model.number="directDraft.priority"
              type="number"
              min="0"
              max="1000"
              required
          /></label>
          <label v-for="field in directFields" :key="field.key">
            {{ field.label
            }}<input
              v-model.number="directDraft.budget[field.key]"
              type="number"
              :min="field.min"
              :max="field.max"
              required
            />
          </label>
        </div>
      </details>
      <ElButton native-type="submit" type="primary" :loading="busy">检查并接入</ElButton>
    </form>
  </ElDialog>

  <ElDialog
    :model-value="!!directEditingDraft"
    class="management-dialog"
    title="ZLM 配置与预算"
    width="min(620px, calc(100vw - 32px))"
    align-center
    append-to-body
    destroy-on-close
    @close="emit('closeDirectEditing')"
  >
    <form v-if="directEditingDraft" class="budget-form" @submit.prevent="submitDirectEditing">
      <label
        >安装位置（选填）<input
          v-model.trim="directEditingDraft.installPath"
          maxlength="512"
          placeholder="例如 /opt/zlm"
        />
      </label>
      <label
        >优先级<input
          v-model.number="directEditingDraft.priority"
          type="number"
          min="0"
          max="1000"
          required
      /></label>
      <label v-for="field in directFields" :key="field.key">
        {{ field.label
        }}<input
          v-model.number="directEditingDraft.budget[field.key]"
          type="number"
          :min="field.min"
          :max="field.max"
          required
        />
      </label>
      <p>直连 ZLM 首期不支持转码，转码上限固定为 0。降低预算只影响新申请。</p>
      <ElButton native-type="submit" type="primary" :loading="busy">保存</ElButton>
    </form>
  </ElDialog>

  <ElDialog
    :model-value="!!directCredentialDraft"
    class="management-dialog"
    title="更新 ZLM secret"
    width="min(520px, calc(100vw - 32px))"
    align-center
    append-to-body
    destroy-on-close
    @close="emit('closeDirectCredential')"
  >
    <form v-if="directCredentialDraft" class="budget-form" @submit.prevent="submitDirectCredential">
      <p class="connection-form-intro">
        {{ directCredentialDraft.instance.name }}：排空并确认资源归零后才能更新。
      </p>
      <label class="field-full"
        >新 secret<input
          v-model="directCredentialDraft.secret"
          type="password"
          autocomplete="new-password"
          required
      /></label>
      <ElButton native-type="submit" type="primary" :loading="busy">更新密钥</ElButton>
    </form>
  </ElDialog>

  <ElDialog
    :model-value="!!directRemoving"
    class="management-dialog"
    title="移除 ZLM 实例"
    width="min(480px, calc(100vw - 32px))"
    align-center
    append-to-body
    destroy-on-close
    @close="emit('closeDirectRemoving')"
  >
    <div v-if="directRemoving" class="remove-confirm">
      <p class="maintenance-body">
        确认移除 <strong>{{ directRemoving.name }}</strong
        >？仅排空且无资源归属时允许移除。
      </p>
      <div class="node-actions">
        <ElButton type="danger" :loading="busy" @click="emit('confirmRemoveDirect')"
          >确认移除</ElButton
        >
      </div>
    </div>
  </ElDialog>

  <ElDialog
    :model-value="!!connectionDraft"
    class="management-dialog"
    :title="connectionDraft?.version == null ? '新增远程预览节点' : '修改节点连接'"
    width="min(680px, calc(100vw - 32px))"
    align-center
    append-to-body
    destroy-on-close
    @close="emit('closeConnection')"
  >
    <form v-if="connectionDraft" class="budget-form" @submit.prevent="submitConnection">
      <p class="connection-form-intro">
        节点控制地址不是 ZLM HTTP API；可用内网自签名 HTTPS 控制口，无需公网证书。
      </p>
      <label
        >节点 ID<input
          v-model.trim="connectionDraft.nodeId"
          :readonly="connectionDraft.version != null"
          maxlength="64"
          required
      /></label>
      <label>名称<input v-model.trim="connectionDraft.name" maxlength="80" required /></label>
      <label
        >节点控制地址（高级）<input
          v-model.trim="connectionDraft.controlUri"
          type="url"
          placeholder="https://10.0.0.10:9443"
          required
      /></label>
      <label
        >浏览器可达媒体地址<input
          v-model.trim="connectionDraft.mediaAddress"
          placeholder="10.0.0.10"
          required
      /></label>
      <label class="field-full"
        >证书 SHA-256 指纹<input
          v-model.trim="connectionDraft.certificateSha256"
          minlength="64"
          maxlength="64"
          required
      /></label>
      <p>凭据文件须预先存放在 Platform 受保护目录，文件名为“节点 ID.token”。</p>
      <ElButton native-type="submit" type="primary" :loading="busy">检查并登记</ElButton>
    </form>
  </ElDialog>

  <ElDialog
    :model-value="!!maintaining"
    class="management-dialog"
    title="预览节点维护"
    width="min(680px, calc(100vw - 32px))"
    align-center
    append-to-body
    destroy-on-close
    @close="emit('closeMaintaining')"
  >
    <div v-if="maintaining" class="maintenance-body">
      <p class="maintenance-target">
        <strong>{{ maintaining.name }}</strong> · {{ labelOf(names, maintaining.mode) }}
      </p>
      <ol>
        <li>停止接新预览，等待已有观看关闭及资源释放。</li>
        <li>获取维护许可，在 Ops 对应预览实例的升级或回退计划中填写许可编号。</li>
        <li>Ops 作业完成后检查节点；配置发生变化时同步 Ops 配置，解除维护，再明确参与调度。</li>
      </ol>
      <RequestError
        :error="error"
        advice="未决 Ops 作业须先核对或恢复。许可过期不会自动解除维护。"
      />
      <p v-if="maintenance" class="maintenance-state">
        维护状态：<strong>{{ labelOf(maintenanceNames, maintenance.state) }}</strong>
      </p>
      <template v-if="maintenance?.permitId">
        <label
          >维护许可编号<input :value="maintenance.permitId" readonly aria-label="维护许可编号"
        /></label>
        <p>领取期限：{{ formatDateTime(new Date(maintenance.expiresAt).toISOString()) }}</p>
        <p v-if="maintenance.jobId">
          Ops 作业：<code>{{ maintenance.jobId }}</code>
        </p>
      </template>
      <div class="node-actions maintenance-actions">
        <ElButton :disabled="busy" @click="emit('maintain')">刷新维护状态</ElButton>
        <ElButton
          type="primary"
          plain
          :disabled="busy || maintaining.mode !== 'DRAINING' || maintenance?.state === 'CLAIMED'"
          @click="emit('maintain', 'PREPARE')"
          >获取维护许可</ElButton
        >
        <ElButton
          type="primary"
          :disabled="
            busy ||
            maintaining.mode !== 'DRAINING' ||
            !maintenance?.permitId ||
            maintenance.state === 'CLAIMED'
          "
          @click="emit('maintain', 'RELEASE')"
          >解除维护</ElButton
        >
      </div>
    </div>
  </ElDialog>

  <ElDialog
    :model-value="!!diagnostic"
    class="management-dialog"
    title="预览节点诊断"
    width="min(740px, calc(100vw - 32px))"
    align-center
    append-to-body
    destroy-on-close
    @close="emit('closeDiagnostic')"
  >
    <pre class="node-json">{{ JSON.stringify(diagnostic, null, 2) }}</pre>
  </ElDialog>

  <ElDialog
    :model-value="!!editingDraft"
    class="management-dialog"
    title="节点预算与优先级"
    width="min(620px, calc(100vw - 32px))"
    align-center
    append-to-body
    destroy-on-close
    @close="emit('closeEditing')"
  >
    <form v-if="editingDraft" class="budget-form" @submit.prevent="submitEditing">
      <label class="field-full"
        >优先级<input
          v-model.number="editingDraft.priority"
          type="number"
          min="0"
          max="10000"
          required
      /></label>
      <label v-for="field in fields" :key="field.key"
        >{{ field.label
        }}<input
          v-model.number="editingDraft.budget[field.key]"
          type="number"
          :min="field.min"
          :max="field.max"
          required
      /></label>
      <p>降低预算只影响新申请。修改每路码率上界须先排空；远程预算不得超过节点已应用配置。</p>
      <ElButton native-type="submit" type="primary" :loading="busy">保存</ElButton>
    </form>
  </ElDialog>
</template>
