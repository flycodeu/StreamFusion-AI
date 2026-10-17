<script setup lang="ts">
import { onMounted, onBeforeUnmount, reactive, ref } from 'vue'
import {
  ElButton,
  ElDialog,
  ElForm,
  ElFormItem,
  ElSelect,
  ElOption,
  ElInput,
  ElInputNumber,
  ElSwitch,
  ElTag,
} from 'element-plus'
import {
  getCapture,
  captureRoutes,
  startCapture,
  stopCapture,
  type CaptureWindow,
} from '../../api/diagnostics/api'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { formatDateTime } from '../../utils/dateTime'
defineEmits<{ close: [] }>()
const form = reactive({ routes: [] as string[], actorId: '', minutes: 15, errorsOnly: true })
const status = ref<CaptureWindow | null>(null),
  routes = ref<{ key: string; label: string }[]>([]),
  busy = ref(false),
  error = ref<unknown>(null),
  validation = ref('')
const scope = usePageScope()
let timer: ReturnType<typeof globalThis.setTimeout> | undefined
let alive = true
let revision = 0
function updateStatus(value: CaptureWindow) {
  status.value = value
  if (value.enabled) {
    form.routes = [...value.routes]
    form.actorId = value.actorId || ''
    form.errorsOnly = value.errorsOnly
  }
}
async function load() {
  if (busy.value) return
  const active = scope(),
    current = ++revision
  try {
    const value = await getCapture()
    if (active() && current === revision) {
      updateStatus(value)
      error.value = null
    }
  } catch (e) {
    if (active() && current === revision) error.value = e
  }
}
async function change(stop = false) {
  if (busy.value) return
  validation.value = ''
  if (!stop && !form.routes.length) validation.value = '请选择至少一个接口'
  if (!stop && form.actorId && !/^[1-9][0-9]{0,18}$/.test(form.actorId))
    validation.value = '账号编号必须为正整数'
  if (validation.value) return
  const active = scope(),
    current = ++revision
  busy.value = true
  error.value = null
  try {
    const value = stop
      ? await stopCapture()
      : await startCapture({ ...form, actorId: form.actorId || null })
    if (active() && current === revision) updateStatus(value)
  } catch (e) {
    if (active() && current === revision) error.value = e
  } finally {
    if (active()) busy.value = false
  }
}
onMounted(async () => {
  const active = scope(),
    current = ++revision
  try {
    const values = await Promise.all([getCapture(), captureRoutes()])
    if (active() && current === revision) {
      updateStatus(values[0])
      routes.value = values[1]
    }
  } catch (e) {
    if (active() && current === revision) error.value = e
  }
  if (!alive) return
  const poll = async () => {
    if (!alive) return
    if (status.value?.enabled) await load()
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
    title="临时详细记录"
    width="720px"
    class="management-dialog"
    append-to-body
    @close="$emit('close')"
  >
    <RequestError :error="error" />
    <div v-if="status" class="capture-status">
      <ElTag :type="status.enabled ? 'success' : 'info'">{{
        status.enabled ? '采集中' : '未开启'
      }}</ElTag
      ><span>已匹配 {{ status.matched }} / {{ status.limit }} · 已记录 {{ status.recorded }}</span
      ><span v-if="status.enabled">截止 {{ formatDateTime(status.expiresAt) }}</span>
    </div>
    <ElForm label-width="105px" :disabled="busy || status?.enabled" @submit.prevent="change(false)">
      <ElFormItem label="采集接口"
        ><ElSelect
          v-model="form.routes"
          multiple
          filterable
          placeholder="选择接口"
          :multiple-limit="32"
          ><ElOption
            v-for="route in routes"
            :key="route.key"
            :value="route.key"
            :label="`${route.label} · ${route.key}`" /></ElSelect
      ></ElFormItem>
      <ElFormItem label="账号范围"
        ><ElInput v-model="form.actorId" placeholder="账号编号，留空匹配全部账号" clearable
      /></ElFormItem>
      <ElFormItem label="执行时间"
        ><ElInputNumber v-model="form.minutes" :min="1" :max="60" /><span class="unit"
          >分钟</span
        ></ElFormItem
      >
      <ElFormItem label="仅记录失败"><ElSwitch v-model="form.errorsOnly" /></ElFormItem>
    </ElForm>
    <p v-if="validation" class="capture-validation" role="alert">{{ validation }}</p>
    <template #footer
      ><ElButton @click="$emit('close')">关闭</ElButton
      ><ElButton v-if="status?.enabled" :loading="busy" @click="change(true)">停止记录</ElButton
      ><ElButton
        type="primary"
        :loading="busy"
        :disabled="!status || status.enabled"
        @click="change(false)"
        >开始记录</ElButton
      ></template
    >
  </ElDialog>
</template>
<style scoped>
.capture-status {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  align-items: center;
  margin-bottom: 20px;
  font-size: 13px;
  color: var(--el-text-color-secondary);
}
.unit {
  margin-left: 10px;
}
.capture-validation {
  color: var(--el-color-danger);
  font-size: 13px;
}
</style>
