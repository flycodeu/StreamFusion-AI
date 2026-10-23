<script setup lang="ts">
import { computed, onActivated, onDeactivated, onMounted, onScopeDispose, ref } from 'vue'
import {
  ElButton,
  ElDialog,
  ElForm,
  ElFormItem,
  ElOption,
  ElSelect,
  ElMessageBox,
  ElInput,
  ElEmpty,
  ElDropdown,
  ElDropdownMenu,
  ElDropdownItem,
  ElTabs,
  ElTabPane,
  ElSwitch,
} from 'element-plus'
import { Check, Refresh, Search } from '@element-plus/icons-vue'
import {
  createStatusPlan,
  getStatusPlans,
  stopStatusPlan,
  resumeStatusPlan,
} from '../../api/camera-status/plans'
import { getGroupTree } from '../../api/camera-groups/api'
import { groupTree } from '../camera-groups/tree'
import type { GroupNode } from '../camera-groups/tree'
import type { StatusPlan, StatusPlanInput } from '../../api/camera-status/plans'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { formatDateTime } from '../../utils/dateTime'
import { ApiRequestError } from '../../lib/http/error'
import { reasonLabel } from './model'
import { inspectPlanTargets } from '../../api/camera-status/planTargets'
import type { PlanTarget } from '../../api/camera-status/planTargets'
import PlanCameraPicker from './PlanCameraPicker.vue'

const props = defineProps<{ groupKeys: string[] }>()
const scope = ref<'PAGE' | 'ALL' | 'PLAN' | 'GROUPS'>(props.groupKeys.length ? 'PAGE' : 'GROUPS')
const selectedKeys = ref([...new Set(props.groupKeys)])
const tab = ref('create')
const step = ref(0)
// 新增前端字段：现有 API 没有名称字段；备注只保留在当前弹窗会话，不写入请求。
const taskName = ref(defaultTaskName())
const taskNames = ref<Record<string, string>>({})
const nameTouched = ref(false)
const taskState = ref('ALL')
const taskSearch = ref('')
const reportPlanId = ref<string | null>(null)
const steps = ['选择相机', '连接检测', '视频检测', '检测周期']
const progressItems = [
  { key: 'success', label: '成功' },
  { key: 'failed', label: '异常' },
  { key: 'pending', label: '进行中' },
  { key: 'unknown', label: '未确认' },
  { key: 'skipped', label: '未执行' },
] as const
const selectedGroups = ref<string[]>([])
const pickerBusy = ref(false)
const groupNodes = ref<GroupNode[]>([])
const layer = ref<'CONTROL' | 'MEDIA'>('CONTROL')
const checkVideo = ref(false)
const dispatchMode = ref<'POLLING' | 'PARALLEL'>('POLLING')
const parallelism = ref(1)
const reusedPlan = ref<StatusPlan | null>(null)
const createPanel = ref<globalThis.HTMLElement | null>(null)
const limitExceeded = ref(false)
let inspectionId = 0
const emit = defineEmits<{ close: []; changed: [] }>()
const plans = ref<StatusPlan[]>([]),
  interval = ref(60),
  hours = ref(24)
const busy = ref(false),
  loading = ref(false),
  error = ref<unknown>(null),
  pending = ref<StatusPlanInput | null>(null)
const capture = usePageScope()
const errorOperation = ref<'load' | 'create' | 'stop' | 'resume'>('load')
const groupError = ref<unknown>(null)
const now = ref(Date.now())
const pollPaused = ref(false)
let clockOffset = 0
let nextRefresh = 0
let failures = 0
let readRevision = 0
let pageActive = true
let timer: ReturnType<typeof globalThis.setInterval> | undefined
function planActive(plan: StatusPlan) {
  return plan.state === 'ACTIVE' && Date.parse(plan.endAt) > now.value
}
function defaultTaskName() {
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Shanghai',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).formatToParts(new Date())
  return `巡检任务_${['year', 'month', 'day'].map((key) => parts.find((part) => part.type === key)?.value).join('')}`
}
function planTitle(plan: StatusPlan) {
  return taskNames.value[plan.planId] || `巡检任务 #${plan.planId.slice(-4)}`
}
function planState(plan: StatusPlan) {
  return planActive(plan)
    ? 'ACTIVE'
    : Date.parse(plan.endAt) <= now.value || plan.state === 'EXPIRED'
      ? 'EXPIRED'
      : 'STOPPED'
}
function checkLabel(plan: StatusPlan) {
  return plan.layer === 'MEDIA' ? '视频收帧' : plan.checkVideo ? '连接 / 视频' : '连接 / 平台状态'
}
const expiryFormatter = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hourCycle: 'h23',
})
function planSummary(plan: StatusPlan) {
  const endAt = new Date(plan.endAt)
  const expiry = Number.isFinite(endAt.getTime()) ? expiryFormatter.format(endAt) : '—'
  return `${plan.targetCount} 台设备 · ${checkLabel(plan)} · 每 ${plan.intervalMinutes} 分钟 · 有效期至 ${expiry}`
}
function receiptItems(plan: StatusPlan) {
  return progressItems.filter(
    (item) => !['pending', 'skipped'].includes(item.key) || plan.progress?.[item.key] !== 0,
  )
}
const filteredPlans = computed(() =>
  plans.value.filter(
    (plan) =>
      (taskState.value === 'ALL' || planState(plan) === taskState.value) &&
      planTitle(plan).toLocaleLowerCase().includes(taskSearch.value.trim().toLocaleLowerCase()),
  ),
)
const nameError = computed(() =>
  !taskName.value.trim()
    ? '请输入任务名称'
    : [...taskName.value.trim()].length > 80
      ? '任务名称不能超过 80 个字符'
      : '',
)
const selectedCount = computed(() =>
  scope.value === 'PAGE'
    ? selectedKeys.value.length
    : scope.value === 'GROUPS' && !selectedGroups.value.length
      ? 0
      : inspecting.value || inspectionError.value || limitExceeded.value
        ? null
        : targets.value.length,
)
const reportPlan = computed(() => plans.value.find((plan) => plan.planId === reportPlanId.value))
const targets = ref<PlanTarget[]>([]),
  inspecting = ref(false),
  inspectionError = ref<unknown>(null)
const eligibleKeys = computed(() =>
  targets.value.filter((target) => !target.reasonCode).map((target) => target.groupKey),
)
const blockedTargets = computed(() => targets.value.filter((target) => target.reasonCode))
const canCreate = computed(
  () =>
    !nameError.value &&
    !pickerBusy.value &&
    !inspecting.value &&
    !inspectionError.value &&
    !limitExceeded.value &&
    eligibleKeys.value.length > 0 &&
    hours.value * 60 >= interval.value,
)
async function inspectTargets() {
  if (pickerBusy.value) return
  const active = capture()
  const requestId = ++inspectionId
  inspecting.value = true
  inspectionError.value = null
  targets.value = []
  limitExceeded.value = false
  if (scope.value === 'GROUPS' && !selectedGroups.value.length) {
    inspecting.value = false
    return
  }
  try {
    const result = await inspectPlanTargets(
      scope.value === 'GROUPS'
        ? { scope: 'GROUPS', groupIds: [...selectedGroups.value] }
        : scope.value === 'ALL'
          ? { scope: 'ALL' }
          : scope.value === 'PLAN' && reusedPlan.value
            ? { scope: 'PLAN', planId: reusedPlan.value.planId }
            : { scope: 'PAGE', groupKeys: [...selectedKeys.value] },
    )
    if (active() && requestId === inspectionId) {
      targets.value = result.targets
      limitExceeded.value = result.limitExceeded
    }
  } catch (cause) {
    if (active() && requestId === inspectionId) inspectionError.value = cause
  } finally {
    if (active() && requestId === inspectionId) inspecting.value = false
  }
}
async function reuse(plan: StatusPlan) {
  if (busy.value || pending.value || pickerBusy.value) return
  reusedPlan.value = plan
  taskName.value = defaultTaskName()
  nameTouched.value = false
  tab.value = 'create'
  step.value = 0
  selectedGroups.value = []
  scope.value = 'PLAN'
  interval.value = plan.intervalMinutes
  hours.value = Math.max(
    1,
    Math.min(168, Math.round((Date.parse(plan.endAt) - Date.parse(plan.startAt)) / 3_600_000)),
  )
  layer.value = plan.layer
  checkVideo.value = plan.checkVideo === true
  dispatchMode.value = plan.dispatchMode
  parallelism.value = plan.parallelism
  await inspectTargets()
  createPanel.value?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
}
async function nextStep() {
  if (busy.value || pending.value || inspecting.value || pickerBusy.value) return
  if (step.value === 0) {
    nameTouched.value = true
    if (nameError.value) return
    if (scope.value === 'PAGE' && !selectedKeys.value.length) return
    if (scope.value === 'GROUPS' && !selectedGroups.value.length) return
    const requestId = inspectionId + 1
    await inspectTargets()
    if (
      requestId !== inspectionId ||
      inspectionError.value ||
      limitExceeded.value ||
      !eligibleKeys.value.length
    )
      return
  }
  step.value = Math.min(3, step.value + 1)
}
function changeScope() {
  ++inspectionId
  targets.value = []
  inspectionError.value = null
  inspecting.value = false
  limitExceeded.value = false
}
async function selectGroups(keys: string[]) {
  if (busy.value || pending.value || inspecting.value) return
  selectedGroups.value = keys
  selectedKeys.value = []
  scope.value = 'GROUPS'
  changeScope()
  if (keys.length) await inspectTargets()
}
async function selectAll() {
  if (busy.value || pending.value || inspecting.value || pickerBusy.value) return
  selectedKeys.value = []
  selectedGroups.value = []
  scope.value = 'ALL'
  changeScope()
  await inspectTargets()
}
const planErrorAdvice = computed(() => {
  if (!(error.value instanceof ApiRequestError)) return undefined
  if (error.value.status === 429 && ['create', 'resume'].includes(errorOperation.value))
    return '巡检容量已满，请减少目标、增大检测间隔，或停止不需要的任务。'
  if (error.value.status !== 409) return undefined
  if (errorOperation.value === 'stop') return '任务状态已变化，请刷新任务后确认是否仍需停止。'
  if (errorOperation.value === 'resume') return '任务已到期或配置已变化，请刷新任务并核对目标。'
  if (errorOperation.value !== 'create') return undefined
  return '检测配置或授权已变化，请重新核对目标后启动。'
})
async function load(silent = false) {
  if (loading.value) return
  const active = capture(),
    current = ++readRevision
  loading.value = true
  if (!silent) {
    error.value = null
    failures = 0
    pollPaused.value = false
  }
  try {
    const result = await getStatusPlans()
    if (active() && current === readRevision) {
      plans.value = result
      const serverNow = result.find((plan) => plan.serverNow)?.serverNow
      clockOffset = serverNow ? Date.parse(serverNow) - Date.now() : 0
      now.value = Date.now() + clockOffset
      failures = 0
      if (errorOperation.value === 'load') error.value = null
    }
  } catch (cause) {
    if (active() && current === readRevision) {
      if (!error.value || errorOperation.value === 'load') {
        error.value = cause
        errorOperation.value = 'load'
      }
      if (++failures >= 3) pollPaused.value = true
    }
  } finally {
    if (active() && current === readRevision) {
      loading.value = false
      nextRefresh = Date.now() + 30_000
    }
  }
}
function invalidateRead() {
  ++readRevision
  loading.value = false
}
async function create() {
  if (step.value !== 3 || busy.value || !canCreate.value) return
  const active = capture()
  pending.value ??= {
    clientRequestId: `${Date.now()}-${crypto.randomUUID()}`,
    groupKeys: [...eligibleKeys.value],
    intervalMinutes: interval.value,
    durationHours: hours.value,
    layer: layer.value,
    checkVideo: checkVideo.value,
    dispatchMode: dispatchMode.value,
    parallelism:
      layer.value === 'MEDIA' || checkVideo.value || dispatchMode.value === 'POLLING'
        ? 1
        : parallelism.value,
    ...(scope.value === 'GROUPS' ? { groupIds: [...selectedGroups.value] } : {}),
    ...(scope.value === 'PLAN' ? { planId: reusedPlan.value!.planId } : {}),
  }
  invalidateRead()
  busy.value = true
  error.value = null
  errorOperation.value = 'create'
  try {
    const created = await createStatusPlan(pending.value)
    if (!active()) return
    taskNames.value[created.planId] = taskName.value.trim()
    pending.value = null
    taskState.value = 'ALL'
    taskSearch.value = ''
    emit('changed')
    await load()
    tab.value = 'tasks'
    step.value = 0
  } catch (cause) {
    if (active()) {
      error.value = cause
      if (
        cause instanceof ApiRequestError &&
        cause.status &&
        cause.status >= 400 &&
        cause.status < 500 &&
        cause.code !== 'INVALID_RESPONSE'
      )
        pending.value = null
      if (cause instanceof ApiRequestError && cause.status === 409) await inspectTargets()
    }
  } finally {
    if (active()) busy.value = false
  }
}
async function stop(plan: StatusPlan) {
  if (busy.value) return
  const active = capture()
  invalidateRead()
  busy.value = true
  error.value = null
  errorOperation.value = 'stop'
  try {
    await stopStatusPlan(plan)
    if (active()) {
      emit('changed')
      await load()
    }
  } catch (cause) {
    if (active()) {
      error.value = cause
      if (cause instanceof ApiRequestError && cause.status === 409) await load(true)
    }
  } finally {
    if (active()) busy.value = false
  }
}
async function resume(plan: StatusPlan) {
  if (busy.value) return
  const active = capture()
  invalidateRead()
  busy.value = true
  error.value = null
  errorOperation.value = 'resume'
  try {
    await resumeStatusPlan(plan)
    if (active()) {
      emit('changed')
      await load()
    }
  } catch (cause) {
    if (active()) error.value = cause
  } finally {
    if (active()) busy.value = false
  }
}
async function close() {
  if (busy.value) return
  if (pending.value) {
    try {
      await ElMessageBox.confirm(
        '任务响应未确认。关闭后请先查看已有任务，避免重复启动。',
        '关闭巡检任务',
      )
    } catch {
      return
    }
  }
  emit('close')
}
async function loadGroups() {
  const active = capture()
  groupError.value = null
  try {
    const groups = await getGroupTree()
    if (active()) groupNodes.value = groupTree(groups)
  } catch (cause) {
    if (active()) groupError.value = cause
  }
}
onMounted(() => {
  void load()
  const active = capture()
  void loadGroups()
  timer = globalThis.setInterval(() => {
    if (!pageActive || !active() || globalThis.document?.hidden) return
    now.value = Date.now() + clockOffset
    if (
      !busy.value &&
      !pending.value &&
      !pollPaused.value &&
      Date.now() >= nextRefresh &&
      plans.value.some((plan) => plan.state === 'ACTIVE')
    )
      void load(true)
  }, 1000)
})
onScopeDispose(() => globalThis.clearInterval(timer))
onActivated(() => {
  pageActive = true
})
onDeactivated(() => {
  pageActive = false
})
</script>
<template>
  <ElDialog
    :model-value="true"
    title="巡检任务"
    width="1180px"
    class="management-dialog patrol-dialog"
    append-to-body
    :before-close="close"
    :close-on-click-modal="false"
  >
    <RequestError class="patrol-request-error" :error="error" :advice="planErrorAdvice" />
    <ElTabs v-model="tab" class="patrol-tabs">
      <ElTabPane label="新建巡检" name="create" />
      <ElTabPane :label="`我的任务（${plans.length}）`" name="tasks" />
    </ElTabs>
    <div class="patrol-content" :class="{ 'is-picking': tab === 'create' && step === 0 }">
      <section
        v-if="tab === 'create'"
        ref="createPanel"
        class="patrol-create"
        aria-label="下发巡检任务"
      >
        <div class="steps-banner">
          <ol class="patrol-steps" aria-label="创建任务步骤">
            <li
              v-for="(title, index) in steps"
              :key="title"
              :class="{ 'is-current': step === index, 'is-complete': step > index }"
              :aria-current="step === index ? 'step' : undefined"
            >
              <span class="step-number"
                ><Check v-if="step > index" aria-hidden="true" /><template v-else>{{
                  index + 1
                }}</template></span
              >
              <span>{{ title }}</span>
            </li>
          </ol>
        </div>
        <ElForm
          label-position="top"
          :disabled="busy || !!pending || inspecting || pickerBusy"
          @submit.prevent="step === 3 ? create() : nextStep()"
        >
          <div :hidden="step !== 0" class="wizard-stage">
            <ElFormItem
              label="任务名称"
              class="task-name-field"
              required
              :error="nameTouched ? nameError : undefined"
            >
              <ElInput
                v-model="taskName"
                maxlength="80"
                show-word-limit
                placeholder="请输入任务名称"
                aria-label="任务名称"
                @blur="nameTouched = true"
              />
            </ElFormItem>
            <PlanCameraPicker
              v-model="selectedKeys"
              :mode="scope"
              :groups="groupNodes"
              :checked-groups="selectedGroups"
              :group-error="groupError"
              :targets="targets"
              :inspecting="inspecting"
              :range-ready="!inspectionError && !limitExceeded"
              :disabled="busy || !!pending || inspecting || pickerBusy"
              @update:model-value="changeScope"
              @groups="selectGroups"
              @all="selectAll"
              @resolving="pickerBusy = $event"
              @reload-groups="loadGroups"
              @manual="
                (keys) => {
                  selectedKeys = keys
                  selectedGroups = []
                  scope = 'PAGE'
                  changeScope()
                }
              "
            />
          </div>
          <div v-if="step === 1" class="wizard-stage settings-stage">
            <header class="stage-heading">
              <h3>配置相机连接检查</h3>
            </header>
            <ElFormItem v-if="layer === 'MEDIA'" label="复用旧任务">
              <ElButton @click="layer = 'CONTROL'">改为先检测连接</ElButton>
            </ElFormItem>
            <div class="patrol-settings">
              <ElFormItem label="调度方式"
                ><ElSelect v-model="dispatchMode" @change="parallelism = 1"
                  ><ElOption value="POLLING" label="逐个轮询" /><ElOption
                    value="PARALLEL"
                    label="有界并行" /></ElSelect
              ></ElFormItem>
              <ElFormItem v-if="dispatchMode === 'PARALLEL'" label="并发请求数"
                ><ElSelect v-model="parallelism" :disabled="layer === 'MEDIA' || checkVideo"
                  ><ElOption
                    v-for="count in layer === 'MEDIA' || checkVideo ? [1] : [1, 2, 3, 4]"
                    :key="count"
                    :value="count"
                    :label="`${count} 个请求`" /></ElSelect
              ></ElFormItem>
            </div>
          </div>
          <div v-else-if="step === 2" class="wizard-stage settings-stage">
            <header class="stage-heading">
              <h3>按需检查视频收帧</h3>
            </header>
            <div class="video-setting">
              <div>
                <strong>{{ layer === 'MEDIA' ? '视频收帧检查' : '连接通过后检查视频' }}</strong>
              </div>
              <ElFormItem v-if="layer === 'CONTROL'" label="连接通过后检查视频"
                ><ElSwitch
                  v-model="checkVideo"
                  aria-label="连接通过后检查视频"
                  @change="parallelism = 1"
              /></ElFormItem>
              <span v-else class="config-chip">沿用视频检测</span>
            </div>
          </div>
          <div v-else-if="step === 3" class="wizard-stage settings-stage">
            <header class="stage-heading">
              <h3>设置执行频率与有效期</h3>
            </header>
            <div class="patrol-settings">
              <ElFormItem label="检测间隔"
                ><ElSelect v-model="interval" aria-label="检测间隔"
                  ><ElOption
                    v-for="minutes in [5, 10, 15, 30, 60, 120, 1440]"
                    :key="minutes"
                    :value="minutes"
                    :label="
                      minutes >= 60 ? `每 ${minutes / 60} 小时` : `每 ${minutes} 分钟`
                    " /></ElSelect
              ></ElFormItem>
              <ElFormItem label="持续时间"
                ><ElSelect v-model="hours" aria-label="持续时间"
                  ><ElOption
                    v-for="value in [1, 6, 24, 72, 168]"
                    :key="value"
                    :value="value"
                    :label="`${value} 小时`" /></ElSelect
              ></ElFormItem>
            </div>
            <div class="configuration-summary">
              <span class="config-chip">{{
                layer === 'MEDIA' ? '视频收帧' : checkVideo ? '连接 → 视频' : '相机连接'
              }}</span>
              <span class="config-chip">{{
                dispatchMode === 'POLLING' ? '逐个轮询' : `有界并行 · ${parallelism} 个请求`
              }}</span>
              <span class="config-chip">{{ hours }} 小时后结束</span>
            </div>
            <p v-if="hours * 60 < interval" class="patrol-warning" role="alert">
              持续时间不能小于检测间隔。
            </p>
          </div>
        </ElForm>
        <div class="patrol-feedback">
          <RequestError :error="inspectionError" />
          <ElButton
            v-if="inspectionError"
            link
            :disabled="busy || !!pending || inspecting || pickerBusy"
            @click="inspectTargets"
            >重新核对目标</ElButton
          >
          <div
            v-if="
              (step > 0 || blockedTargets.length || limitExceeded || inspecting) &&
              (inspecting || targets.length || limitExceeded)
            "
            class="patrol-target-summary"
            role="status"
          >
            <span v-if="inspecting">正在核对代表流配置…</span>
            <span v-else-if="limitExceeded"
              >授权范围超过 2000 台，请缩小范围；未截取部分设备下发。</span
            >
            <span v-else-if="!inspectionError"
              ><strong>{{ eligibleKeys.length }}</strong> 台可检测<span>
                / {{ targets.length }} 台 · {{ blockedTargets.length }} 台跳过</span
              ></span
            >
            <ElButton
              link
              :disabled="busy || !!pending || inspecting || pickerBusy"
              @click="inspectTargets"
              >重新核对</ElButton
            >
          </div>
          <details v-if="blockedTargets.length" class="patrol-blocked">
            <summary>查看 {{ blockedTargets.length }} 台跳过原因（不计为离线）</summary>
            <ul>
              <li v-for="target in blockedTargets" :key="target.groupKey">
                {{ target.name }}：{{ reasonLabel(target.reasonCode) }}
              </li>
            </ul>
          </details>
          <p v-if="pending" class="patrol-warning" role="status">
            响应未确认，重试将使用原计划请求。
          </p>
        </div>
      </section>
      <section v-else class="patrol-tasks" aria-label="我的巡检任务" :aria-busy="loading">
        <header class="patrol-toolbar">
          <div class="task-filters">
            <ElSelect v-model="taskState" aria-label="任务状态"
              ><ElOption value="ALL" label="全部状态" /><ElOption
                value="ACTIVE"
                label="运行中" /><ElOption value="STOPPED" label="已停止" /><ElOption
                value="EXPIRED"
                label="已到期" /></ElSelect
            ><ElInput
              v-model="taskSearch"
              :prefix-icon="Search"
              clearable
              placeholder="搜索任务名称"
              aria-label="搜索任务名称"
            />
          </div>
          <ElButton :icon="Refresh" :disabled="busy" :loading="loading" @click="load()"
            >刷新列表</ElButton
          >
        </header>
        <p v-if="pollPaused" class="patrol-warning" role="status">
          读取连续失败，已暂停自动刷新，请点击“刷新列表”。
        </p>
        <div class="task-list">
          <ElEmpty
            v-if="!filteredPlans.length && !loading && !error"
            :image-size="64"
            :description="plans.length ? '没有符合条件的任务' : '暂无任务，请先新建巡检'"
          />
          <p v-if="loading && !plans.length" class="patrol-note" role="status">正在读取巡检任务…</p>
          <article v-for="plan in filteredPlans" :key="plan.planId" class="patrol-card">
            <div class="task-content">
              <div class="task-main">
                <div class="patrol-row-title">
                  <h3 :title="planTitle(plan)">{{ planTitle(plan) }}</h3>
                  <span class="task-state">{{
                    planActive(plan)
                      ? '运行中'
                      : planState(plan) === 'EXPIRED'
                        ? '已到期'
                        : '已停止'
                  }}</span>
                </div>
                <span
                  class="task-summary"
                  :title="`${planSummary(plan)}；有效期 ${formatDateTime(plan.startAt)} — ${formatDateTime(plan.endAt)}`"
                  >{{ planSummary(plan) }}</span
                >
              </div>
              <div
                class="receipt-bar"
                :aria-label="plan.progress ? '最近回执' : '暂无最近回执'"
                :title="plan.progress ? undefined : '暂无最近回执'"
              >
                <span class="receipt-label">最近巡检：</span>
                <span v-for="item in receiptItems(plan)" :key="item.key" class="receipt-item">
                  <span>{{ item.label }}</span>
                  <strong
                    :class="{
                      'has-anomaly': item.key === 'failed' && (plan.progress?.failed ?? 0) > 0,
                    }"
                    >{{ plan.progress?.[item.key] ?? '—' }}</strong
                  >
                </span>
              </div>
            </div>
            <div class="task-actions">
              <ElButton
                v-if="planActive(plan)"
                class="task-action-link"
                link
                size="small"
                :disabled="busy || !!pending"
                @click="stop(plan)"
                >暂停</ElButton
              >
              <ElButton
                v-else-if="plan.state === 'STOPPED' && Date.parse(plan.endAt) > now"
                class="task-action-link"
                link
                size="small"
                :disabled="busy || !!pending"
                @click="resume(plan)"
                >恢复执行</ElButton
              >
              <ElButton
                v-else
                class="task-action-link"
                link
                size="small"
                :disabled="busy || !!pending"
                @click="reuse(plan)"
                >复用范围</ElButton
              >
              <ElButton
                class="task-report-button"
                size="small"
                :aria-expanded="reportPlanId === plan.planId"
                @click="reportPlanId = reportPlanId === plan.planId ? null : plan.planId"
                >巡检报告</ElButton
              >
              <ElDropdown placement="bottom-end" trigger="click">
                <ElButton class="task-more-button" text size="small" aria-label="更多操作">
                  <span aria-hidden="true">···</span>
                </ElButton>
                <template #dropdown>
                  <ElDropdownMenu>
                    <ElDropdownItem disabled title="当前服务尚未提供按任务立即巡检接口"
                      >立即巡检</ElDropdownItem
                    >
                    <ElDropdownItem disabled title="当前服务尚未提供删除巡检任务接口"
                      >删除</ElDropdownItem
                    >
                  </ElDropdownMenu>
                </template>
              </ElDropdown>
            </div>
            <p v-if="plan.blockedCount || plan.lastReasonCode" class="patrol-warning">
              {{ plan.blockedCount ? `${plan.blockedCount} 个目标无法继续检测` : ''
              }}{{ plan.blockedCount && plan.lastReasonCode ? ' · ' : ''
              }}{{ plan.lastReasonCode ? reasonLabel(plan.lastReasonCode) : '' }}
            </p>
            <section
              v-if="reportPlan?.planId === plan.planId"
              class="plan-report"
              aria-label="巡检报告"
            >
              <strong>最近回执汇总</strong>
              <p v-if="!plan.progress">暂无巡检回执</p>
              <p>
                {{ checkLabel(plan) }} ·
                {{ plan.dispatchMode === 'POLLING' ? '逐个轮询' : '有界并行' }}
              </p>
              <p>有效期 {{ formatDateTime(plan.startAt) }} — {{ formatDateTime(plan.endAt) }}</p>
              <p v-if="planActive(plan) && plan.nextAt">
                下次检测 {{ formatDateTime(plan.nextAt) }}
              </p>
            </section>
          </article>
        </div>
      </section>
    </div>
    <template #footer>
      <div class="patrol-footer">
        <p v-if="tab === 'create'" class="selection-count" role="status">
          <template v-if="selectedCount !== null"
            >已选择 <strong>{{ selectedCount }}</strong> 台相机设备</template
          ><template v-else
            >已选择{{
              scope === 'GROUPS' ? ` ${selectedGroups.length} 个分组` : '范围'
            }}，待核对设备数</template
          >
        </p>
        <div class="footer-actions">
          <ElButton :disabled="busy" @click="close">{{
            tab === 'create' ? '取消' : '关闭'
          }}</ElButton>
          <template v-if="tab === 'create'">
            <ElButton
              v-if="step > 0"
              :disabled="busy || !!pending || inspecting || pickerBusy"
              @click="step--"
              >上一步</ElButton
            >
            <ElButton
              v-if="step < 3"
              type="primary"
              :loading="inspecting || pickerBusy"
              :disabled="
                busy ||
                !!pending ||
                inspecting ||
                pickerBusy ||
                (step === 0 &&
                  (!!nameError ||
                    (scope === 'PAGE' && !selectedKeys.length) ||
                    (scope === 'GROUPS' && !selectedGroups.length)))
              "
              @click="nextStep"
              >下一步</ElButton
            >
            <ElButton
              v-else
              type="primary"
              :disabled="!canCreate"
              :loading="busy"
              @click="create"
              >{{ pending ? '重试原计划' : '提交任务' }}</ElButton
            >
          </template>
        </div>
      </div>
    </template>
  </ElDialog>
</template>

<style scoped>
:global(.el-dialog.patrol-dialog > .el-dialog__body) {
  display: flex;
  flex-direction: column;
  padding: 12px 24px;
  overflow: hidden;
}
.patrol-request-error {
  flex-shrink: 0;
  max-height: 160px;
  overflow: auto;
}
.patrol-tabs {
  flex-shrink: 0;
}
.patrol-tabs :deep(.el-tabs__header) {
  margin-bottom: 12px;
}
.patrol-tabs :deep(.el-tabs__item) {
  font-weight: 600;
}
.patrol-content {
  flex: 0 1 600px;
  height: 600px;
  min-height: 0;
  overflow-y: auto;
  scrollbar-width: thin;
  scrollbar-color: var(--el-border-color-darker) transparent;
}
.patrol-content.is-picking {
  overflow: hidden;
}
.is-picking .patrol-create {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}
.is-picking .patrol-create > .el-form {
  display: flex;
  flex-direction: column;
  flex: 0 1 auto;
  min-height: 0;
}
.is-picking .wizard-stage {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
}
.is-picking .patrol-feedback {
  flex: 0 1 auto;
  min-height: 0;
  max-height: 35%;
  overflow: auto;
}
.steps-banner {
  flex-shrink: 0;
  padding: 12px 32px;
  margin: 14px 0 18px;
  border: 1px solid #f1f5f9;
  border-radius: 8px;
  background: #f8fafc;
}
.patrol-steps {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  padding: 0;
  margin: 0;
  list-style: none;
}
.patrol-steps li {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
  white-space: nowrap;
}
.patrol-steps li:not(:last-child)::after {
  content: '';
  position: absolute;
  left: calc(50% + 58px);
  right: calc(-50% + 58px);
  height: 1px;
  background: var(--el-border-color);
}
.step-number {
  display: grid;
  place-items: center;
  width: 22px;
  height: 22px;
  flex-shrink: 0;
  border: 1px solid var(--el-border-color);
  border-radius: 50%;
  font-size: 12px;
  font-weight: 600;
}
.step-number svg {
  width: 14px;
}
.patrol-steps .is-current {
  color: var(--el-color-primary);
  font-weight: 600;
}
.is-current .step-number {
  border-color: #bfdbfe;
  color: #2563eb;
  background: #eff6ff;
}
.is-complete .step-number {
  color: var(--el-color-success);
  background: var(--el-color-success-light-9);
  border-color: var(--el-color-success-light-5);
}
.patrol-steps .is-complete::after {
  background: var(--el-color-success-light-5);
}
.task-name-field {
  display: flex;
  flex-direction: row;
  align-items: flex-start;
  flex: 0 0 auto;
  width: 438px;
  max-width: 100%;
  min-width: 0;
  margin: 0 0 18px;
}
.task-name-field :deep(.el-form-item__label) {
  box-sizing: border-box;
  flex: 0 0 78px;
  height: 32px;
  padding: 0 12px 0 0;
  margin: 0;
  line-height: 32px;
}
.task-name-field :deep(.el-form-item__content) {
  flex: 1;
  min-width: 0;
}
.task-name-field :deep(.el-input) {
  width: 360px;
  max-width: 100%;
}
.patrol-note {
  font-size: 13px;
  line-height: 1.7;
  color: var(--el-text-color-secondary);
}
.settings-stage {
  padding: 8px 0;
}
.stage-heading {
  margin-bottom: 24px;
}
.stage-heading h3 {
  font-size: 19px;
  color: var(--el-text-color-primary);
  margin: 8px 0;
}
.patrol-settings {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}
.patrol-settings :deep(.el-select) {
  width: 100%;
}
.video-setting {
  display: flex;
  gap: 24px;
  justify-content: space-between;
  align-items: center;
  padding: 20px;
  border: 1px solid var(--el-border-color-light);
  background: var(--el-fill-color-extra-light);
  border-radius: 8px;
}
.video-setting :deep(.el-form-item) {
  margin: 0;
  flex-shrink: 0;
}
.video-setting :deep(.el-form-item__label) {
  display: none;
}
.patrol-target-summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: 14px;
  padding: 8px 12px;
  border-radius: 6px;
  background: var(--el-color-primary-light-9);
  font-size: 12px;
}
.patrol-target-summary strong {
  font-size: 16px;
  color: var(--el-color-primary);
}
.patrol-blocked {
  margin-top: 10px;
  font-size: 12px;
  max-height: 130px;
  overflow: auto;
}
.patrol-blocked summary {
  cursor: pointer;
  color: var(--el-text-color-secondary);
}
.patrol-blocked ul {
  padding-left: 20px;
  line-height: 1.8;
  overflow-wrap: anywhere;
}
.patrol-toolbar,
.task-filters,
.task-main,
.patrol-row-title,
.task-actions,
.configuration-summary,
.patrol-footer,
.footer-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
.patrol-toolbar {
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 16px;
}
.task-filters {
  min-width: 0;
}
.task-filters :deep(.el-select) {
  width: 120px;
  flex-shrink: 0;
}
.task-filters :deep(.el-input) {
  width: 200px;
}
.task-list {
  padding: 2px 2px 6px;
}
.patrol-card {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px 24px;
  padding: 16px 20px;
  margin-bottom: 10px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: var(--el-bg-color);
  transition:
    border-color 180ms ease,
    box-shadow 180ms ease;
}
.patrol-card:last-child {
  margin-bottom: 0;
}
.patrol-card:hover {
  border-color: #cbd5e1;
  box-shadow: 0 2px 10px rgb(18 35 64 / 4%);
}
.task-content {
  min-width: 0;
}
.task-main {
  min-width: 0;
  gap: 12px;
}
.patrol-row-title {
  min-width: 0;
  flex-shrink: 0;
  max-width: 260px;
}
.patrol-row-title h3 {
  margin: 0;
  min-width: 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--el-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.task-summary {
  flex: 1;
  min-width: 0;
  color: #64748b;
  font-size: 12px;
  line-height: 20px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.task-state {
  display: inline-flex;
  align-items: center;
  flex-shrink: 0;
  padding: 2px 8px;
  border: 1px solid #e5e7eb;
  border-radius: 4px;
  background: #f8fafc;
  color: #4b5563;
  font-size: 12px;
  font-weight: 500;
  line-height: 18px;
  white-space: nowrap;
}
.task-actions {
  flex-shrink: 0;
  gap: 12px;
}
.task-action-link:not(:disabled) {
  color: #2563eb;
}
.task-action-link:not(:disabled):hover {
  color: #1d4ed8;
}
.task-report-button {
  border-color: #e2e8f0;
  background: #fff;
  color: #475569;
}
.task-report-button:hover,
.task-report-button:focus-visible {
  border-color: #cbd5e1;
  background: #f8fafc;
  color: #1e293b;
}
.task-more-button {
  width: 24px;
  padding: 0;
  color: #64748b;
  font-size: 20px;
  letter-spacing: 1px;
}
.task-more-button:hover {
  background: #f1f5f9;
  color: #1e293b;
}
.task-actions :deep(.el-button + .el-button),
.footer-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}
.configuration-summary {
  flex-wrap: wrap;
}
.config-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 100%;
  padding: 3px 10px;
  border-radius: 4px;
  background: #f1f5f9;
  color: #334155;
  font-size: 12px;
  font-weight: 500;
  line-height: 18px;
  overflow-wrap: anywhere;
}
.receipt-bar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 0;
  margin-top: 8px;
  color: #64748b;
  font-size: 13px;
  line-height: 20px;
}
.receipt-label {
  flex-shrink: 0;
}
.receipt-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}
.receipt-item + .receipt-item::before {
  content: '|';
  margin: 0 10px;
  color: #cbd5e1;
}
.receipt-item strong {
  color: #1e293b;
  font-size: 13px;
  font-weight: 500;
  line-height: 20px;
  font-variant-numeric: tabular-nums;
}
.receipt-item strong.has-anomaly {
  color: #dc2626;
  font-weight: 700;
}
.patrol-warning {
  font-size: 12px;
  line-height: 1.6;
  color: var(--el-color-warning-dark-2);
  margin: 10px 0 0;
  overflow-wrap: anywhere;
}
.plan-report {
  grid-column: 1 / -1;
  margin-top: 4px;
  padding: 8px 0 0;
  color: #475569;
  font-size: 12px;
}
.patrol-card > .patrol-warning {
  grid-column: 1 / -1;
  color: #64748b;
}
.plan-report p {
  margin: 6px 0 0;
  line-height: 1.7;
  color: var(--el-text-color-secondary);
}
.patrol-footer {
  justify-content: space-between;
  gap: 12px;
}
.footer-actions {
  margin-left: auto;
  flex-shrink: 0;
}
.selection-count {
  display: flex;
  align-items: center;
  gap: 5px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
  margin: 0;
  text-align: left;
}
.selection-count strong {
  color: var(--el-color-primary);
  font-size: 16px;
  font-variant-numeric: tabular-nums;
}
.patrol-content,
.patrol-blocked {
  scrollbar-width: thin;
  scrollbar-color: var(--el-border-color-darker) transparent;
}
.patrol-content::-webkit-scrollbar,
.patrol-blocked::-webkit-scrollbar {
  width: 5px;
}
.patrol-content::-webkit-scrollbar-thumb,
.patrol-blocked::-webkit-scrollbar-thumb {
  background: var(--el-border-color);
  border-radius: 8px;
}
@media (prefers-reduced-motion: reduce) {
  .patrol-card {
    transition: none;
  }
}
@media (max-width: 680px) {
  :global(.el-dialog.patrol-dialog > .el-dialog__body) {
    padding: 12px 16px;
  }
  .task-name-field {
    width: 100%;
  }
  .task-name-field :deep(.el-input) {
    width: 100%;
  }
  .steps-banner {
    padding: 12px;
  }
  .patrol-steps li {
    gap: 4px;
    font-size: 12px;
  }
  .patrol-steps li::after {
    display: none;
  }
  .task-filters {
    flex: 1;
  }
  .task-filters :deep(.el-input) {
    width: 100%;
  }
  .patrol-toolbar {
    flex-wrap: wrap;
  }
  .patrol-settings {
    grid-template-columns: 1fr;
    gap: 0;
  }
  .video-setting {
    padding: 16px;
    gap: 12px;
  }
  .task-main {
    flex-wrap: wrap;
    gap: 10px;
  }
  .patrol-card {
    grid-template-columns: minmax(0, 1fr);
    gap: 12px;
  }
  .task-summary {
    flex-basis: 100%;
    white-space: normal;
  }
  .task-actions {
    flex-wrap: wrap;
    margin-left: auto;
  }
  .receipt-bar {
    gap: 4px 0;
  }
  .patrol-footer {
    flex-wrap: wrap;
  }
  .selection-count {
    width: 100%;
    font-size: 12px;
  }
}
</style>
