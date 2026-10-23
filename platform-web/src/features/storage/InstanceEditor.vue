<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import {
  ElDialog,
  ElForm,
  ElFormItem,
  ElInput,
  ElButton,
  ElTabs,
  ElTabPane,
  ElCheckbox,
  ElAlert,
} from 'element-plus'
import * as api from '../../api/storage/api'
import type { InstanceInput, StorageInstance, StorageOptions } from '../../api/storage/types'
import RequestError from '../../components/feedback/RequestError.vue'
import { label } from './display'

const visible = defineModel<boolean>({ required: true })
const props = defineProps<{ instance: StorageInstance | null; options: StorageOptions }>()
const emit = defineEmits<{ saved: [] }>()
const busy = ref(false),
  error = ref<unknown>(null),
  check = ref<Record<string, unknown> | null>(null)
const replace = ref(true)
const activeTab = ref('connection')
const form = reactive({
  name: '',
  endpoint: '',
  region: 'us-east-1',
  managementUrl: '',
  metricsEndpoint: '',
  remark: '',
  access: '',
  secret: '',
  bucket: '',
  prefix: '',
  probePrefix: '',
})
watch(visible, (open) => {
  error.value = null
  check.value = null
  form.access = ''
  form.secret = ''
  if (!open) return
  activeTab.value = 'connection'
  const row = props.instance
  Object.assign(form, {
    name: row?.name ?? '',
    endpoint: row?.endpoint ?? '',
    region: row?.region ?? 'us-east-1',
    managementUrl: row?.managementUrl ?? '',
    metricsEndpoint: row?.metricsEndpoint ?? '',
    remark: row?.remark ?? '',
    bucket: row?.scopes[0]?.bucket ?? '',
    prefix: row?.scopes[0]?.prefix ?? '',
    probePrefix: row?.scopes[0]?.probePrefix ?? '',
  })
  replace.value = !row
})
watch([form, replace], () => {
  check.value = null
})
function command(): InstanceInput {
  return {
    name: form.name,
    endpoint: form.endpoint,
    region: form.region,
    managementUrl: form.managementUrl,
    metricsEndpoint: form.metricsEndpoint,
    remark: form.remark,
    accessKey: replace.value ? { action: 'REPLACE', value: form.access } : { action: 'KEEP' },
    secretKey: replace.value ? { action: 'REPLACE', value: form.secret } : { action: 'KEEP' },
    version: props.instance?.version,
    scopes: [
      { bucket: form.bucket, prefix: form.prefix, displayName: '', probePrefix: form.probePrefix },
    ],
    idempotencyKey: requestKey.value,
  }
}
const requestKey = ref(crypto.randomUUID())
watch(visible, (open) => {
  if (open) requestKey.value = crypto.randomUUID()
})
async function submit(test = false) {
  if (busy.value) return
  busy.value = true
  error.value = null
  try {
    const input = command()
    if (test) check.value = await api.draftCheck(input, props.instance?.instanceId)
    else {
      await api.saveInstance(input, props.instance?.instanceId)
      visible.value = false
      emit('saved')
    }
  } catch (e) {
    error.value = e
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <ElDialog
    v-model="visible"
    :title="instance ? '编辑存储接入' : '新增存储接入'"
    class="management-dialog storage-instance-editor"
    width="680px"
    align-center
    :close-on-click-modal="false"
    :close-on-press-escape="!busy"
    :show-close="!busy"
    destroy-on-close
  >
    <RequestError v-if="error" :error="error" />
    <ElAlert
      v-if="!options.credentialProtectionReady"
      title="凭据保护尚未配置，请先配置平台密钥。"
      type="warning"
      :closable="false"
    />
    <ElForm label-position="top" :disabled="busy" @submit.prevent="submit()">
      <ElTabs v-model="activeTab" class="editor-tabs">
        <ElTabPane label="连接信息" name="connection">
          <div class="tab-panel">
            <ElFormItem label="名称" required>
              <ElInput v-model="form.name" maxlength="64" />
            </ElFormItem>
            <ElFormItem label="S3 地址" required>
              <ElInput
                v-model="form.endpoint"
                placeholder="http://127.0.0.1:8333"
                maxlength="2048"
              />
            </ElFormItem>
            <ElFormItem label="SeaweedFS 管理地址">
              <ElInput
                v-model="form.managementUrl"
                placeholder="http://127.0.0.1:23646"
                maxlength="2048"
              />
            </ElFormItem>
            <ElCheckbox v-if="instance" v-model="replace">替换访问密钥</ElCheckbox>
            <div v-if="replace" class="field-grid">
              <ElFormItem label="Access Key" required>
                <ElInput
                  v-model="form.access"
                  type="password"
                  show-password
                  autocomplete="off"
                  maxlength="256"
                />
              </ElFormItem>
              <ElFormItem label="Secret Key" required>
                <ElInput
                  v-model="form.secret"
                  type="password"
                  show-password
                  autocomplete="new-password"
                  maxlength="2048"
                />
              </ElFormItem>
            </div>
          </div>
        </ElTabPane>
        <ElTabPane label="存储目录" name="scope">
          <div class="tab-panel">
            <ElFormItem label="已存在的 Bucket" required>
              <ElInput v-model="form.bucket" :disabled="!!instance" maxlength="63" />
            </ElFormItem>
            <ElFormItem label="根目录">
              <ElInput
                v-model="form.prefix"
                :disabled="!!instance"
                placeholder="留空表示整个 Bucket；目录以 / 结尾"
                maxlength="1024"
              />
            </ElFormItem>
          </div>
        </ElTabPane>
        <ElTabPane label="高级配置" name="advanced">
          <div class="tab-panel">
            <ElFormItem label="签名 Region">
              <ElInput v-model="form.region" maxlength="64" />
            </ElFormItem>
            <ElFormItem label="只读指标地址">
              <ElInput
                v-model="form.metricsEndpoint"
                placeholder="可选，需在部署配置中允许此地址"
                maxlength="2048"
              />
            </ElFormItem>
            <ElFormItem label="备注">
              <ElInput v-model="form.remark" type="textarea" :rows="2" maxlength="500" />
            </ElFormItem>
          </div>
        </ElTabPane>
      </ElTabs>
    </ElForm>
    <ElAlert
      v-if="check"
      :title="`只读测试：${label(String(check.status))}`"
      type="info"
      :closable="false"
      class="check-feedback"
    >
      <details>
        <summary>查看测试详情</summary>
        <pre class="check-result">{{ JSON.stringify(check, null, 2) }}</pre>
      </details>
    </ElAlert>
    <template #footer>
      <div class="editor-footer">
        <ElButton :disabled="busy" @click="visible = false">取消</ElButton>
        <ElButton :loading="busy" :disabled="!form.bucket" @click="submit(true)">只读测试</ElButton>
        <ElButton
          type="primary"
          :loading="busy"
          :disabled="!options.credentialProtectionReady"
          @click="submit()"
          >保存接入</ElButton
        >
      </div>
    </template>
  </ElDialog>
</template>

<style scoped>
:global(.el-dialog.storage-instance-editor) {
  margin: auto;
}
.field-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0 20px;
}
.tab-panel {
  padding-top: 4px;
}
.field-hint {
  margin: 0 0 16px;
  color: var(--text-secondary);
  font-size: 13px;
  line-height: 1.6;
}
.editor-footer {
  display: flex;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 8px;
}
.editor-footer :deep(.el-button + .el-button) {
  margin-left: 0;
}
.check-feedback {
  margin-top: 12px;
}
.check-feedback summary {
  cursor: pointer;
}
.check-result {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  max-height: 120px;
  overflow: auto;
}
@media (max-width: 600px) {
  .field-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
