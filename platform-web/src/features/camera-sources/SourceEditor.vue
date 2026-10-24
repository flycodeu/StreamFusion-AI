<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import {
  ElButton,
  ElForm,
  ElFormItem,
  ElInput,
  ElInputNumber,
  ElMessage,
  ElMessageBox,
  ElOption,
  ElSelect,
  ElSwitch,
} from 'element-plus'
import * as api from '../../api/camera-sources/api'
import { getCameraOptions } from '../../api/camera/api'
import { useDeviceAdapters } from '../camera-access/useDeviceAdapters'
import { getPlatformConnection } from '../camera-access/platformConnection'
import type {
  Source,
  SourceEndpoint,
  SourceOptions,
  SourceUpdate,
  SourcePurpose,
} from '../../api/camera-sources/types'
import CameraDialogFrame from '../camera/CameraDialogFrame.vue'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { formError as validationError, secret, sourceBrandLabel } from '../camera/form'
import BrandSelect from '../camera/BrandSelect.vue'
import { sourceFieldLabels } from './presentation'
const props = defineProps<{ sourceId: string; embedded?: boolean; manufacturer?: string | null }>()
const adapters = useDeviceAdapters()
const emit = defineEmits<{
  close: []
  saved: [vendorHint?: string | null]
  vendorLoaded: [vendorHint: string | null]
}>()
const captureScope = usePageScope(),
  options = ref<SourceOptions | null>(null),
  editing = ref<Source | null>(null)
const saving = ref(false),
  loading = ref(true),
  formError = ref<unknown>(null),
  manualStorageReady = ref(true)
const error = formError
const platform = computed(() => getPlatformConnection(editing.value?.adapterType))
const brandPlaceholder = computed(() => {
  const brand = sourceBrandLabel({
    sourceType: editing.value?.adapterType ?? null,
    manufacturer: props.manufacturer,
    sourceDisplayName: '',
  })
  return brand === '未识别品牌' ? '选择或输入品牌' : `${brand}（设备识别，可修改）`
})
const initialDraft = ref<string | null>(null)
const dirty = computed(
  () => initialDraft.value !== null && JSON.stringify(form) !== initialDraft.value,
)
let detailSequence = 0
const form = reactive({
  adapterType: '',
  vendorHint: '',
  name: '',
  remark: '',
  networkPolicyKey: '',
  enabled: true,
  host: '',
  port: 554,
  rtspPort: 554,
  scheme: 'rtsp' as SourceEndpoint['scheme'],
  authenticated: false,
  username: '',
  password: '',
})
async function openEdit() {
  const sequence = ++detailSequence,
    active = captureScope()
  error.value = null
  try {
    const current = await api.getSource(props.sourceId)
    if (!active() || sequence !== detailSequence) return
    const endpoint = current.endpoints[0]
    if (!endpoint) throw validationError('来源缺少连接端点，请联系管理员核对。')
    editing.value = current
    formError.value = null
    Object.assign(form, {
      name: current.name,
      remark: current.remark ?? '',
      networkPolicyKey: current.networkPolicyKey ?? '',
      adapterType: current.adapterType ?? '',
      vendorHint: current.vendorHint ?? '',
      enabled: current.enabled,
      host: endpoint.host,
      port: endpoint.port,
      rtspPort: current.rtspPort ?? 554,
      scheme: endpoint.scheme,
      authenticated: endpoint.authMode !== 'NONE',
      username: '',
      password: '',
    })
    initialDraft.value = JSON.stringify(form)
    if (current.vendorHint) emit('vendorLoaded', current.vendorHint)
  } catch (cause) {
    if (active() && sequence === detailSequence) error.value = cause
  }
}
async function confirmLeave(): Promise<boolean> {
  if (saving.value) return false
  if (!dirty.value) return true
  try {
    await ElMessageBox.confirm('连接资料尚未保存，离开会放弃这些修改。', '放弃未保存的修改？', {
      type: 'warning',
      confirmButtonText: '放弃修改',
      cancelButtonText: '继续编辑',
    })
    return true
  } catch {
    return false
  }
}
async function close() {
  const active = captureScope()
  if ((await confirmLeave()) && active()) emit('close')
}
defineExpose({ confirmLeave, saving })
async function save() {
  if (saving.value || !options.value || !editing.value) return
  const active = captureScope()
  formError.value = null
  if ([...form.vendorHint.trim()].length > 128) {
    formError.value = validationError('品牌最多填写128个字符。')
    return
  }
  const hasSavedCredentials = Boolean(editing.value.credentials.length)
  const usernameAction: 'KEEP' | 'REPLACE' = hasSavedCredentials
    ? form.username
      ? 'REPLACE'
      : 'KEEP'
    : 'REPLACE'
  const passwordAction: 'KEEP' | 'REPLACE' = hasSavedCredentials
    ? form.password
      ? 'REPLACE'
      : 'KEEP'
    : 'REPLACE'
  if (
    !form.name.trim() ||
    !form.host.trim() ||
    (form.authenticated &&
      ((usernameAction === 'REPLACE' && !form.username) ||
        (passwordAction === 'REPLACE' && !form.password)))
  ) {
    formError.value = validationError('请填写连接名称、主机和需要替换的账号密码。')
    return
  }
  saving.value = true
  try {
    const purpose: SourcePurpose = editing.value?.endpoints[0]?.purpose ?? 'RTSP'
    const endpoint: SourceEndpoint = {
      purpose,
      scheme: form.scheme,
      host: form.host.trim(),
      port: form.port,
      basePath: editing.value?.endpoints[0]?.basePath ?? '',
      authMode: form.authenticated ? 'DRIVER_NEGOTIATED' : 'NONE',
      credentialPurpose: form.authenticated ? purpose : null,
      tlsPolicy: 'SYSTEM_CA',
    }
    const credentials = form.authenticated
      ? [
          {
            purpose,
            username: secret(usernameAction, form.username),
            password: secret(passwordAction, form.password),
          },
        ]
      : []
    if (
      form.authenticated &&
      !manualStorageReady.value &&
      (usernameAction === 'REPLACE' || passwordAction === 'REPLACE')
    )
      throw validationError('凭据加密尚未配置，请联系系统管理员完成配置后保存账号密码。')
    const common = {
      name: form.name.trim(),
      remark: form.remark.trim() || null,
      enabled: form.enabled,
      networkPolicyKey: form.networkPolicyKey,
    }
    {
      const original = editing.value
      const input: SourceUpdate = { version: original.version }
      if (common.name !== original.name) input.name = common.name
      if (common.remark !== original.remark) input.remark = common.remark
      if (common.enabled !== original.enabled) input.enabled = common.enabled
      if (common.networkPolicyKey && common.networkPolicyKey !== original.networkPolicyKey)
        input.networkPolicyKey = common.networkPolicyKey
      if (original.rtspPort != null && form.rtspPort !== original.rtspPort)
        input.rtspPort = form.rtspPort
      if (original.connectionCategory === 'DEVICE' && original.endpointEditable) {
        if ((form.adapterType || null) !== original.adapterType)
          input.adapterType = form.adapterType || null
      }
      if ((form.vendorHint.trim() || null) !== (original.vendorHint ?? null))
        input.vendorHint = form.vendorHint.trim() || null
      const previous = original.endpoints[0]!
      if (
        endpoint.host !== previous.host ||
        endpoint.port !== previous.port ||
        endpoint.scheme !== previous.scheme ||
        endpoint.authMode !== previous.authMode
      )
        input.endpointsUpsert = [endpoint]
      if (form.authenticated && (usernameAction === 'REPLACE' || passwordAction === 'REPLACE'))
        input.credentialsUpsert = credentials
      if (!form.authenticated && original.credentials.length) input.credentialsRemove = [purpose]
      if (Object.keys(input).length === 1) {
        emit('saved', form.vendorHint.trim() || null)
        return
      }
      await api.updateSource(original.sourceId, input)
    }
    if (!active()) return
    form.username = ''
    form.password = ''
    initialDraft.value = JSON.stringify(form)
    emit('saved', form.vendorHint.trim() || null)
    ElMessage.success('连接资料已保存')
  } catch (cause) {
    if (active()) {
      formError.value = cause
    }
  } finally {
    saving.value = false
  }
}

onMounted(async () => {
  const active = captureScope()
  try {
    const [capabilities, permission] = await Promise.all([
      api.getSourceOptions(),
      getCameraOptions(),
    ])
    if (!active()) return
    options.value = capabilities
    manualStorageReady.value = permission.manualStorageReady
    await openEdit()
  } catch (cause) {
    if (active()) formError.value = cause
  } finally {
    if (active()) loading.value = false
  }
})
onBeforeUnmount(() => {
  detailSequence++
  form.username = ''
  form.password = ''
})
</script>
<template>
  <CameraDialogFrame
    :embedded="embedded"
    :model-value="true"
    title="连接设置"
    width="660px"
    destroy-on-close
    :close-on-click-modal="!saving"
    :close-on-press-escape="!saving"
    :show-close="!saving"
    :before-close="close"
    @update:model-value="
      (visible: boolean) => {
        if (!visible) void close()
      }
    "
  >
    <RequestError :error="formError" :field-labels="sourceFieldLabels" />
    <ElForm label-position="top" :disabled="saving || loading" @submit.prevent="save">
      <div class="connection-fields">
        <ElFormItem label="连接名称" required
          ><ElInput v-model="form.name" maxlength="100" placeholder="请输入连接名称"
        /></ElFormItem>
        <ElFormItem label="品牌">
          <BrandSelect
            v-model="form.vendorHint"
            :adapters="adapters"
            :placeholder="brandPlaceholder"
          />
        </ElFormItem>
      </div>
      <div class="connection-fields address-fields">
        <ElFormItem label="连接主机" required
          ><ElInput
            v-model="form.host"
            :disabled="editing?.endpointEditable === false"
            maxlength="253"
            placeholder="IP 或批准的域名，不含协议、端口或路径"
        /></ElFormItem>
        <ElFormItem label="服务端口" required
          ><ElInputNumber
            v-model="form.port"
            :min="1"
            :max="65535"
            :disabled="editing?.endpointEditable === false"
        /></ElFormItem>
      </div>
      <ElFormItem label="需要认证"
        ><ElSwitch v-model="form.authenticated" :disabled="!!platform"
      /></ElFormItem>
      <div v-if="form.authenticated" class="connection-fields credential-fields">
        <ElFormItem
          :label="platform?.usernameLabel ?? '设备账号'"
          :required="!editing?.credentials.length"
          ><ElInput
            v-model="form.username"
            autocomplete="off"
            maxlength="128"
            clearable
            :placeholder="
              editing?.credentials.length ? '留空表示保持原账号不变' : '请输入设备账号'
            "
        /></ElFormItem>
        <ElFormItem
          :label="platform?.passwordLabel ?? '设备密码'"
          :required="!editing?.credentials.length"
          ><ElInput
            v-model="form.password"
            type="password"
            show-password
            autocomplete="new-password"
            maxlength="512"
            clearable
            :placeholder="
              editing?.credentials.length ? '留空表示保持原密码不变' : '请输入设备密码'
            "
        /></ElFormItem>
      </div>
      <details class="connection-advanced">
        <summary>更多连接设置</summary>
        <template v-if="editing?.connectionCategory === 'DEVICE' && editing.endpointEditable">
          <ElFormItem label="连接方式">
            <ElSelect v-model="form.adapterType">
              <ElOption label="暂不指定" value="" />
              <ElOption
                v-for="adapter in adapters"
                :key="adapter.type"
                :value="adapter.type"
                :label="adapter.label"
              />
              <ElOption
                v-if="form.adapterType && !adapters.some((item) => item.type === form.adapterType)"
                :value="form.adapterType"
                :label="form.adapterType"
              />
            </ElSelect>
          </ElFormItem>
        </template>
        <ElFormItem v-if="options?.networkPolicies.length" label="接入网络">
          <ElSelect v-model="form.networkPolicyKey" placeholder="可暂不设置">
            <ElOption
              v-for="item in options?.networkPolicies"
              :key="item.key"
              :value="item.key"
              :label="item.name"
            />
          </ElSelect>
        </ElFormItem>
        <div class="connection-fields">
          <ElFormItem v-if="editing && editing.adapterType !== 'RTSP'" label="连接协议">
            <ElSelect v-model="form.scheme" :disabled="editing?.endpointEditable === false">
              <ElOption label="HTTP" value="http" /><ElOption
                label="HTTPS（验证证书）"
                value="https"
              />
            </ElSelect>
          </ElFormItem>
          <ElFormItem v-if="editing?.rtspPort != null" label="RTSP 端口"
            ><ElInputNumber v-model="form.rtspPort" :min="1" :max="65535"
          /></ElFormItem>
        </div>
        <ElFormItem label="备注"
          ><ElInput v-model="form.remark" type="textarea" maxlength="500"
        /></ElFormItem>
      </details>
    </ElForm>
    <template #footer
      ><ElButton :disabled="saving" @click="close">关闭</ElButton
      ><ElButton type="primary" :loading="saving" :disabled="loading || !options" @click="save">{{
        '保存配置'
      }}</ElButton></template
    >
  </CameraDialogFrame>
</template>
<style scoped>
.connection-note {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.connection-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  column-gap: 20px;
}
.address-fields {
  grid-template-columns: minmax(0, 1fr) 160px;
}
.credential-fields :deep(.el-select + .el-input) {
  margin-top: 8px;
}
.connection-fields :deep(.el-input-number) {
  width: 100%;
}
.connection-advanced summary {
  cursor: pointer;
  color: var(--el-color-primary);
  padding: 4px 0 16px;
}
@media (max-width: 600px) {
  .connection-fields {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
