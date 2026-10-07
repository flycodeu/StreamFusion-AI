<script setup lang="ts">
import { onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import {
  ElButton,
  ElForm,
  ElFormItem,
  ElInput,
  ElInputNumber,
  ElMessage,
  ElOption,
  ElSelect,
  ElSwitch,
} from 'element-plus'
import * as api from '../../api/camera-sources/api'
import { getCameraOptions } from '../../api/camera/api'
import { useDeviceAdapters } from '../camera-access/useDeviceAdapters'
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
import { formError as validationError, secret } from '../camera/form'
const props = defineProps<{ sourceId: string; embedded?: boolean }>()
const adapters = useDeviceAdapters()
const emit = defineEmits<{ close: []; saved: [] }>()
const captureScope = usePageScope(),
  options = ref<SourceOptions | null>(null),
  editing = ref<Source | null>(null)
const saving = ref(false),
  loading = ref(true),
  formError = ref<unknown>(null),
  manualStorageReady = ref(true)
const error = formError
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
  usernameAction: 'REPLACE' as 'KEEP' | 'REPLACE',
  username: '',
  passwordAction: 'REPLACE' as 'KEEP' | 'REPLACE',
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
      usernameAction: current.credentials.length ? 'KEEP' : 'REPLACE',
      username: '',
      passwordAction: current.credentials.length ? 'KEEP' : 'REPLACE',
      password: '',
    })
  } catch (cause) {
    if (active() && sequence === detailSequence) error.value = cause
  }
}
async function save() {
  if (saving.value || !options.value || !editing.value) return
  const active = captureScope()
  formError.value = null
  if (
    !form.name.trim() ||
    !form.host.trim() ||
    (form.authenticated &&
      ((form.usernameAction === 'REPLACE' && !form.username) ||
        (form.passwordAction === 'REPLACE' && !form.password)))
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
            username: secret(form.usernameAction, form.username),
            password: secret(form.passwordAction, form.password),
          },
        ]
      : []
    if (
      form.authenticated &&
      !manualStorageReady.value &&
      (form.usernameAction === 'REPLACE' || form.passwordAction === 'REPLACE')
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
        if ((form.vendorHint.trim() || null) !== (original.vendorHint ?? null))
          input.vendorHint = form.vendorHint.trim() || null
      }
      const previous = original.endpoints[0]!
      if (
        endpoint.host !== previous.host ||
        endpoint.port !== previous.port ||
        endpoint.scheme !== previous.scheme ||
        endpoint.authMode !== previous.authMode
      )
        input.endpointsUpsert = [endpoint]
      if (
        form.authenticated &&
        (form.usernameAction === 'REPLACE' || form.passwordAction === 'REPLACE')
      )
        input.credentialsUpsert = credentials
      if (!form.authenticated && original.credentials.length) input.credentialsRemove = [purpose]
      if (Object.keys(input).length === 1) {
        emit('saved')
        return
      }
      await api.updateSource(original.sourceId, input)
    }
    if (!active()) return
    emit('saved')
    form.username = ''
    form.password = ''
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
    @update:model-value="emit('close')"
  >
    <RequestError :error="formError" />
    <p class="connection-note">连接配置对共用此连接的相机生效。</p>
    <ElForm label-width="110px" :disabled="saving || loading" @submit.prevent="save">
      <template v-if="editing?.connectionCategory === 'DEVICE' && editing.endpointEditable">
        <ElFormItem label="连接方式"
          ><ElSelect v-model="form.adapterType"
            ><ElOption label="暂不指定" value="" /><ElOption
              v-for="adapter in adapters"
              :key="adapter.type"
              :value="adapter.type"
              :label="adapter.label" /><ElOption
              v-if="form.adapterType && !adapters.some((item) => item.type === form.adapterType)"
              :value="form.adapterType"
              :label="form.adapterType" /></ElSelect
        ></ElFormItem>
        <ElFormItem label="品牌备注"
          ><ElInput v-model="form.vendorHint" maxlength="100" placeholder="人工填写的品牌"
        /></ElFormItem>
      </template>
      <ElFormItem label="连接名称" required
        ><ElInput v-model="form.name" maxlength="100"
      /></ElFormItem>
      <ElFormItem v-if="options?.networkPolicies.length" label="接入网络"
        ><ElSelect v-model="form.networkPolicyKey" placeholder="可暂不设置"
          ><ElOption
            v-for="item in options?.networkPolicies"
            :key="item.key"
            :value="item.key"
            :label="item.name" /></ElSelect
      ></ElFormItem>
      <ElFormItem label="连接主机" required
        ><ElInput
          v-model="form.host"
          :disabled="editing?.endpointEditable === false"
          maxlength="253"
          placeholder="IP 或批准的域名，不含协议、端口或路径"
      /></ElFormItem>
      <ElFormItem v-if="editing && editing.adapterType !== 'RTSP'" label="连接协议"
        ><ElSelect v-model="form.scheme" :disabled="editing?.endpointEditable === false"
          ><ElOption label="HTTP" value="http" /><ElOption
            label="HTTPS（验证证书）"
            value="https" /></ElSelect
      ></ElFormItem>
      <ElFormItem label="服务端口" required
        ><ElInputNumber
          v-model="form.port"
          :min="1"
          :max="65535"
          :disabled="editing?.endpointEditable === false"
      /></ElFormItem>
      <p v-if="editing?.endpointEditable === false">已有通道的设备地址不可修改。</p>
      <ElFormItem v-if="editing?.rtspPort != null" label="RTSP 端口"
        ><ElInputNumber v-model="form.rtspPort" :min="1" :max="65535"
      /></ElFormItem>
      <ElFormItem label="需要认证"
        ><ElSwitch v-model="form.authenticated" :disabled="editing?.adapterType === 'HIK_PLATFORM'"
      /></ElFormItem>
      <template v-if="form.authenticated">
        <ElFormItem
          :label="editing?.adapterType === 'HIK_PLATFORM' ? 'AppKey' : '设备账号'"
          required
          ><ElSelect v-if="editing?.credentials.length" v-model="form.usernameAction"
            ><ElOption value="KEEP" label="保留已保存账号" /><ElOption
              value="REPLACE"
              label="替换账号" /></ElSelect
          ><ElInput
            v-if="form.usernameAction === 'REPLACE'"
            v-model="form.username"
            autocomplete="off"
            maxlength="128"
        /></ElFormItem>
        <ElFormItem
          :label="editing?.adapterType === 'HIK_PLATFORM' ? 'AppSecret' : '设备密码'"
          required
          ><ElSelect v-if="editing?.credentials.length" v-model="form.passwordAction"
            ><ElOption value="KEEP" label="保留已保存密码" /><ElOption
              value="REPLACE"
              label="替换密码" /></ElSelect
          ><ElInput
            v-if="form.passwordAction === 'REPLACE'"
            v-model="form.password"
            type="password"
            show-password
            autocomplete="new-password"
            maxlength="512"
        /></ElFormItem>
      </template>
      <ElFormItem label="允许新使用"><ElSwitch v-model="form.enabled" /></ElFormItem>
      <ElFormItem label="备注"
        ><ElInput v-model="form.remark" type="textarea" maxlength="500"
      /></ElFormItem>
    </ElForm>
    <template #footer
      ><ElButton :disabled="saving" @click="emit('close')">关闭</ElButton
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
</style>
