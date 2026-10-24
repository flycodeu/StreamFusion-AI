<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import {
  ElButton,
  ElDialog,
  ElForm,
  ElFormItem,
  ElInputNumber,
  ElSwitch,
  ElTag,
  ElMessage,
} from 'element-plus'
import * as api from '../../api/storage/api'
import type { HistoryPolicy } from '../../api/storage/api'
import RequestError from '../../components/feedback/RequestError.vue'

const props = defineProps<{ modelValue: boolean; instanceId?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; saved: [] }>()
const policy = ref<HistoryPolicy | null>(null),
  globalPolicy = ref<HistoryPolicy | null>(null)
const form = reactive({
  inheritGlobal: false,
  enabled: false,
  retentionDays: undefined as number | undefined,
})
const loading = ref(false),
  saving = ref(false),
  error = ref<unknown>(null)
let sequence = 0
const valid = computed(
  () =>
    form.inheritGlobal ||
    !form.enabled ||
    (Number.isInteger(form.retentionDays) &&
      form.retentionDays! >= 1 &&
      form.retentionDays! <= 3650),
)
watch(
  () => [props.modelValue, props.instanceId],
  async () => {
    const current = ++sequence
    policy.value = null
    error.value = null
    if (!props.modelValue) return
    loading.value = true
    try {
      const [value, globalValue] = await Promise.all([
        api.getHistoryPolicy(props.instanceId),
        props.instanceId ? api.getHistoryPolicy() : Promise.resolve(null),
      ])
      if (current !== sequence) return
      policy.value = value
      globalPolicy.value = globalValue
      Object.assign(form, {
        inheritGlobal: value.inheritGlobal,
        enabled: value.enabled,
        retentionDays: value.retentionDays ?? undefined,
      })
    } catch (e) {
      if (current === sequence) error.value = e
    } finally {
      if (current === sequence) loading.value = false
    }
  },
  { immediate: true },
)
onBeforeUnmount(() => {
  sequence++
})
async function save() {
  if (!policy.value || !valid.value || saving.value) return
  const current = sequence
  saving.value = true
  error.value = null
  try {
    await api.saveHistoryPolicy(
      {
        version: policy.value.version,
        inheritGlobal: form.inheritGlobal,
        enabled: form.enabled,
        retentionDays: form.retentionDays ?? null,
      },
      props.instanceId,
    )
    if (current !== sequence) return
    ElMessage.success('清理策略已保存')
    emit('saved')
    emit('update:modelValue', false)
  } catch (e) {
    if (current === sequence) error.value = e
  } finally {
    saving.value = false
  }
}
</script>
<template>
  <ElDialog
    :model-value="modelValue"
    class="management-dialog"
    :title="instanceId ? '当前图床 · 记录清理策略' : '全局 · 记录清理策略'"
    width="min(500px, 94vw)"
    align-center
    :close-on-click-modal="false"
    :show-close="!saving"
    :close-on-press-escape="!saving"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <RequestError v-if="error" :error="error" />
    <div v-if="loading" class="policy-loading">加载策略…</div>
    <ElForm v-if="policy" label-position="top" :disabled="saving" @submit.prevent="save">
      <div class="policy-summary">
        <span>操作日志 / 文件操作记录 / 清理候选</span><ElTag type="info">数据库记录</ElTag>
      </div>
      <ElFormItem v-if="instanceId" label="策略来源"
        ><ElSwitch v-model="form.inheritGlobal" active-text="继承全局" inactive-text="单独设置"
      /></ElFormItem>
      <div v-if="form.inheritGlobal" class="inherited-state">
        <span>全局策略</span
        ><strong>{{
          globalPolicy?.effective.enabled
            ? `自动清理 · 保留 ${globalPolicy.effective.retentionDays} 天`
            : '未启用自动清理'
        }}</strong>
      </div>
      <template v-else>
        <ElFormItem label="自动清理"
          ><ElSwitch v-model="form.enabled" active-text="启用" inactive-text="关闭"
        /></ElFormItem>
        <ElFormItem label="保留天数" :required="form.enabled"
          ><div class="days-input">
            <ElInputNumber
              v-model="form.retentionDays"
              :disabled="!form.enabled"
              :min="1"
              :max="3650"
              :precision="0"
              :value-on-clear="null"
              placeholder="请输入天数"
              aria-label="保留天数"
            /><span>天</span>
          </div></ElFormItem
        >
        <div v-if="form.enabled" class="day-presets">
          <ElButton
            v-for="days in [30, 90, 180]"
            :key="days"
            size="small"
            @click="form.retentionDays = days"
            >{{ days }} 天</ElButton
          >
        </div>
      </template>
    </ElForm>
    <template #footer
      ><ElButton :disabled="saving" @click="emit('update:modelValue', false)">取消</ElButton
      ><ElButton type="primary" :disabled="!policy || !valid" :loading="saving" @click="save"
        >保存策略</ElButton
      ></template
    >
  </ElDialog>
</template>
<style scoped>
.policy-summary {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
  align-items: center;
  padding: 12px 14px;
  background: #f8fafc;
  border-radius: 8px;
  color: #64748b;
  font-size: 13px;
  margin-bottom: 24px;
}
.inherited-state {
  display: flex;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
  padding: 20px 0;
  color: #64748b;
}
.inherited-state strong {
  font-weight: 500;
  color: #334155;
}
.days-input {
  display: flex;
  gap: 12px;
  align-items: center;
}
.day-presets {
  display: flex;
  gap: 8px;
  margin-top: -8px;
  margin-bottom: 20px;
}
.day-presets .el-button {
  margin-left: 0;
}
.policy-loading {
  padding: 24px;
  text-align: center;
  color: #64748b;
}
</style>
