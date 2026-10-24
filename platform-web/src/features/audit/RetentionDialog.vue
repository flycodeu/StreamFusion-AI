<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import {
  ElButton,
  ElDialog,
  ElForm,
  ElFormItem,
  ElInputNumber,
  ElRadio,
  ElRadioGroup,
  ElSwitch,
  ElDescriptions,
  ElDescriptionsItem,
  ElTag,
  ElMessage,
} from 'element-plus'
import {
  getRetention,
  saveRetention,
  previewRetention,
  runRetention,
  type RetentionPolicy,
  type RetentionPreview,
} from '../../api/audit/maintenance'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { formatDateTime } from '../../utils/dateTime'
import { labelOf } from '../../utils/display'
import { retentionResults, retentionModeLabels } from './maintenancePresentation'
defineEmits<{ close: [] }>()
const form = reactive({
  enabled: false,
  mode: 'AGE',
  keepMonths: 6,
  maxRecords: 50000,
  version: '0',
})
const policy = ref<RetentionPolicy | null>(null),
  preview = ref<RetentionPreview | null>(null),
  loading = ref(false),
  error = ref<unknown>(null)
const scope = usePageScope()
const unchanged = computed(
  () =>
    policy.value &&
    form.enabled === policy.value.enabled &&
    form.mode === policy.value.mode &&
    form.keepMonths === policy.value.keepMonths &&
    form.maxRecords === policy.value.maxRecords &&
    form.version === policy.value.version,
)
let timer: ReturnType<typeof globalThis.setTimeout> | undefined
let alive = true
let revision = 0
function applyPolicy(value: RetentionPolicy) {
  const { enabled, mode, keepMonths, maxRecords, version } = value
  Object.assign(form, { enabled, mode, keepMonths, maxRecords, version })
}
watch(
  () => [form.enabled, form.mode, form.keepMonths, form.maxRecords],
  () => {
    preview.value = null
  },
)
async function load(initial = false) {
  if (loading.value) return
  const active = scope(),
    current = ++revision
  try {
    const value = await getRetention()
    if (!active() || current !== revision) return
    policy.value = value
    if (initial) applyPolicy(value)
  } catch (e) {
    if (active() && current === revision) error.value = e
  }
}
async function act(action: 'preview' | 'save' | 'run') {
  if (loading.value || !policy.value) return
  const active = scope(),
    current = ++revision
  loading.value = true
  error.value = null
  try {
    if (action === 'preview') {
      const input = { ...form }
      const value = await previewRetention(input)
      if (active() && current === revision && JSON.stringify(input) === JSON.stringify(form))
        preview.value = value
    } else {
      const value =
        action === 'run' ? await runRetention(form.version) : await saveRetention({ ...form })
      if (active() && current === revision) {
        policy.value = value
        applyPolicy(value)
        preview.value = null
        ElMessage.success(action === 'run' ? '已提交后台执行' : '保留策略已保存')
      }
    }
  } catch (e) {
    if (active() && current === revision) error.value = e
  } finally {
    if (active()) loading.value = false
  }
}
onMounted(async () => {
  await load(true)
  if (!alive) return
  const poll = async () => {
    if (!alive) return
    if (policy.value?.enabled) await load()
    if (alive) timer = globalThis.setTimeout(poll, 5000)
  }
  timer = globalThis.setTimeout(poll, 5000)
})
onBeforeUnmount(() => {
  alive = false
  globalThis.clearTimeout(timer)
})
</script>
<template>
  <ElDialog
    :model-value="true"
    title="日志保留策略"
    width="680px"
    class="management-dialog"
    align-center
    @close="$emit('close')"
  >
    <RequestError :error="error" />
    <ElForm label-width="100px" :disabled="loading || !policy" @submit.prevent>
      <ElFormItem label="自动清理"
        ><ElSwitch v-model="form.enabled" :disabled="!policy"
      /></ElFormItem>
      <ElFormItem label="保留方式"
        ><ElRadioGroup v-model="form.mode"
          ><ElRadio v-for="(label, value) in retentionModeLabels" :key="value" :value="value">{{
            label
          }}</ElRadio></ElRadioGroup
        ></ElFormItem
      >
      <ElFormItem v-if="form.mode === 'AGE'" label="保留时间"
        ><ElInputNumber v-model="form.keepMonths" :min="1" :max="120" /><span class="unit"
          >个月</span
        ></ElFormItem
      >
      <ElFormItem v-else label="保留条数"
        ><ElInputNumber v-model="form.maxRecords" :min="1000" :max="1000000" :step="1000"
      /></ElFormItem>
    </ElForm>
    <div v-if="preview" class="retention-preview">
      <strong
        >预计清理 {{ preview.limited ? '超过 ' : ''
        }}{{ preview.candidates.toLocaleString() }} 条</strong
      >
      <span
        >当前 {{ preview.total.toLocaleString() }} 条 · 保留
        {{ preview.protectedCount }} 条初始化记录</span
      >
      <span v-if="preview.cutoff">截止边界 {{ formatDateTime(preview.cutoff) }}</span>
      <ElTag v-if="preview.exportInProgress" type="warning">有导出任务，清理将等待</ElTag>
    </div>
    <ElDescriptions v-if="policy" :column="1" border>
      <ElDescriptionsItem label="下次执行">{{
        policy.enabled ? formatDateTime(policy.nextAt) : '未开启'
      }}</ElDescriptionsItem>
      <ElDescriptionsItem label="最近执行">{{
        formatDateTime(policy.lastFinishedAt || policy.lastStartedAt)
      }}</ElDescriptionsItem>
      <ElDescriptionsItem label="执行结果"
        >{{ labelOf(retentionResults, policy.lastResult, '尚未执行') }} ·
        {{ policy.lastDeleted }} 条</ElDescriptionsItem
      >
    </ElDescriptions>
    <template #footer
      ><div class="retention-actions">
        <ElButton @click="$emit('close')">关闭</ElButton
        ><ElButton :loading="loading" :disabled="!policy" @click="act('preview')">预览范围</ElButton
        ><ElButton
          :disabled="loading || !policy?.enabled || !unchanged || policy.lastResult === 'RUNNING'"
          @click="act('run')"
          >立即执行</ElButton
        ><ElButton
          type="primary"
          :loading="loading"
          :disabled="!policy || (form.enabled && !preview)"
          @click="act('save')"
          >{{ form.enabled ? '保存并启用' : '保存并停用' }}</ElButton
        >
      </div></template
    >
  </ElDialog>
</template>
<style scoped>
.unit {
  margin-left: 10px;
  color: var(--el-text-color-secondary);
}
.retention-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}
.retention-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}
@media (max-width: 560px) {
  .retention-actions {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
.retention-preview {
  display: grid;
  gap: 8px;
  padding: 12px 14px;
  margin: 10px 0 18px;
  border: 1px solid #bfdbfe;
  background: #eff6ff;
  color: #1e3a8a;
  font-size: 13px;
  border-radius: 8px;
}
</style>
