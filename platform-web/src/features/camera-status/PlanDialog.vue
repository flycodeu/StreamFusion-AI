<script setup lang="ts">
import { computed, nextTick, onActivated, onDeactivated, onMounted, onScopeDispose, ref } from 'vue'
import {
  ElButton,
  ElDialog,
  ElForm,
  ElFormItem,
  ElOption,
  ElSelect,
  ElMessageBox,
  ElInput,
  ElTabs,
  ElTabPane,
  ElSwitch,
} from 'element-plus'
import { Check } from '@element-plus/icons-vue'
import {
  createStatusPlan,
  getStatusPlans,
  stopStatusPlan,
  resumeStatusPlan,
  deleteStatusPlan,
} from '../../api/camera-status/plans'
import { getGroupTree } from '../../api/camera-groups/api'
import { groupTree } from '../camera-groups/tree'
import type { GroupNode } from '../camera-groups/tree'
import type { StatusPlan, StatusPlanInput } from '../../api/camera-status/plans'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { ApiRequestError } from '../../lib/http/error'
import { reasonLabel } from './model'
import { inspectPlanTargets } from '../../api/camera-status/planTargets'
import type { PlanTarget, TargetSelection, TargetScope } from '../../api/camera-status/planTargets'
import PlanCameraPicker from './PlanCameraPicker.vue'
import PlanTargetSettings from './PlanTargetSettings.vue'
import PlanTaskList from './PlanTaskList.vue'
import './patrol-dialog.css'

const props = withDefaults(defineProps<{ groupKeys?: string[] }>(), { groupKeys: () => [] })
const scope = ref<'PAGE' | 'ALL' | 'PLAN' | 'GROUPS'>(props.groupKeys.length ? 'PAGE' : 'GROUPS')
const selectedKeys = ref([...new Set(props.groupKeys)])
const targetSelections = ref<TargetSelection[]>([])
const tab = ref('create')
const step = ref(0)
const taskName = ref(defaultTaskName())
const nameTouched = ref(false)
const taskState = ref('ALL')
const taskSearch = ref('')
const reportPlanId = ref<string | null>(null)
const steps = ['选择相机', '在线状态检测', '视频检测', '检测周期']
const selectedGroups = ref<string[]>([])
const pickerBusy = ref(false)
const navigationBusy = ref(false)
const groupNodes = ref<GroupNode[]>([])
const layer = ref<'CONTROL' | 'MEDIA'>('CONTROL')
const checkVideo = ref(false)
const dispatchMode = ref<'POLLING' | 'PARALLEL'>('POLLING')
const parallelism = ref(1)
const reusedPlan = ref<StatusPlan | null>(null)
const createPanel = ref<globalThis.HTMLElement | null>(null)
const limitExceeded = ref(false)
const totalCount = ref<number | null>(null)
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
const errorOperation = ref<'load' | 'create' | 'stop' | 'resume' | 'delete'>('load')
const groupError = ref<unknown>(null)
const now = ref(Date.now())
const pollPaused = ref(false)
let clockOffset = 0
let nextRefresh = 0
let failures = 0
let readRevision = 0
let pageActive = true
let timer: ReturnType<typeof globalThis.setInterval> | undefined

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
  return plan.name?.trim() || `巡检任务 #${plan.planId.slice(-4)}`
}

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
      : inspectionError.value || limitExceeded.value
        ? null
        : targets.value.length,
)
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
  const active = capture()
  const requestId = ++inspectionId
  inspecting.value = true
  inspectionError.value = null
  limitExceeded.value = false
  if (scope.value === 'GROUPS' && !selectedGroups.value.length) {
    inspecting.value = false
    return
  }
  try {
    const query: TargetScope =
      scope.value === 'GROUPS'
        ? { scope: 'GROUPS', groupIds: [...selectedGroups.value] }
        : scope.value === 'ALL'
          ? { scope: 'ALL' }
          : scope.value === 'PLAN' && reusedPlan.value
            ? { scope: 'PLAN', planId: reusedPlan.value.planId }
            : { scope: 'PAGE', groupKeys: [...selectedKeys.value] }
    const result = await inspectPlanTargets({
      ...query,
      ...(targetSelections.value.length ? { targetSelections: [...targetSelections.value] } : {}),
    })
    if (active() && requestId === inspectionId) {
      totalCount.value = result.totalCount ?? null
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
  targetSelections.value = []
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
  if (busy.value || pending.value || navigationBusy.value || pickerBusy.value) return
  const requiresInspection = step.value === 0
  if (requiresInspection) navigationBusy.value = true
  try {
    if (requiresInspection) {
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
  } finally {
    if (requiresInspection) navigationBusy.value = false
  }
}

function changeScope() {
  targetSelections.value = []
  ++inspectionId
  inspectionError.value = null
  inspecting.value = false
  limitExceeded.value = false
  if (
    (scope.value === 'PAGE' && !selectedKeys.value.length) ||
    (scope.value === 'GROUPS' && !selectedGroups.value.length)
  )
    targets.value = []
}

async function selectedChanged() {
  changeScope()
  await nextTick()
  if (scope.value === 'PAGE' && selectedKeys.value.length) await inspectTargets()
}

async function setTargetSelection(selection: TargetSelection) {
  if (busy.value || pending.value || inspecting.value) return
  targetSelections.value = [
    ...targetSelections.value.filter((item) => item.groupKey !== selection.groupKey),
    selection,
  ]
  await inspectTargets()
}

async function selectGroups(keys: string[]) {
  if (busy.value || pending.value) return
  selectedGroups.value = keys
  selectedKeys.value = []
  scope.value = 'GROUPS'
  changeScope()
  if (keys.length) await inspectTargets()
}

async function selectAll() {
  if (busy.value || pending.value) return
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
  if (errorOperation.value === 'delete') return '任务状态已变化，请刷新后重试删除。'
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
    name: taskName.value.trim(),
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
    ...(targetSelections.value.length
      ? {
          targetSelections: targetSelections.value.filter((item) =>
            eligibleKeys.value.includes(item.groupKey),
          ),
        }
      : {}),
    ...(scope.value === 'GROUPS' ? { groupIds: [...selectedGroups.value] } : {}),
    ...(scope.value === 'PLAN' ? { planId: reusedPlan.value!.planId } : {}),
  }
  invalidateRead()
  busy.value = true
  error.value = null
  errorOperation.value = 'create'
  try {
    await createStatusPlan(pending.value)
    if (!active()) return
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

async function remove(plan: StatusPlan) {
  if (busy.value || pending.value) return
  const active = capture()
  try {
    await ElMessageBox.confirm(
      `删除“${planTitle(plan)}”？删除后停止后续巡检，已产生的检测记录保留。`,
      '删除巡检任务',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  if (!active() || busy.value || pending.value) return
  invalidateRead()
  busy.value = true
  error.value = null
  errorOperation.value = 'delete'
  try {
    await deleteStatusPlan(plan)
    if (!active()) return
    if (reportPlanId.value === plan.planId) reportPlanId.value = null
    emit('changed')
    await load()
  } catch (cause) {
    if (active()) {
      error.value = cause
      if (cause instanceof ApiRequestError && [404, 409].includes(cause.status ?? 0))
        await load(true)
    }
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
  if (scope.value === 'PAGE' && selectedKeys.value.length) void inspectTargets()
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
    align-center
    append-to-body
    :before-close="close"
    :close-on-click-modal="!busy && !pending"
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
          :disabled="busy || !!pending || navigationBusy"
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
              :total-count="totalCount"
              :inspecting="inspecting"
              :range-ready="!inspectionError && !limitExceeded"
              :disabled="busy || !!pending || navigationBusy"
              @update:model-value="selectedChanged"
              @groups="selectGroups"
              @all="selectAll"
              @resolving="pickerBusy = $event"
              @reload-groups="loadGroups"
              @manual="
                (keys) => {
                  selectedKeys = keys
                  selectedGroups = []
                  scope = 'PAGE'
                  selectedChanged()
                }
              "
            />
          </div>
          <div v-if="step === 1" class="wizard-stage settings-stage">
            <header class="stage-heading">
              <h3>配置在线状态检测</h3>
            </header>
            <ElFormItem v-if="layer === 'MEDIA'" label="复用旧任务">
              <ElButton @click="layer = 'CONTROL'">改为先检测在线状态</ElButton>
            </ElFormItem>
            <div class="setting-list" aria-label="在线状态检测调度设置">
              <ElFormItem label="调度方式" class="setting-row">
                <div class="setting-row-copy">
                  <strong>调度方式</strong>
                  <span>控制设备检查请求的发送节奏</span>
                </div>
                <ElSelect v-model="dispatchMode" aria-label="调度方式" @change="parallelism = 1">
                  <ElOption value="POLLING" label="逐个轮询" />
                  <ElOption value="PARALLEL" label="有界并行" />
                </ElSelect>
              </ElFormItem>
              <ElFormItem v-if="dispatchMode === 'PARALLEL'" label="并发请求数" class="setting-row">
                <div class="setting-row-copy">
                  <strong>并发请求数</strong>
                  <span>同时发起的在线状态检查数量</span>
                </div>
                <ElSelect
                  v-model="parallelism"
                  aria-label="并发请求数"
                  :disabled="layer === 'MEDIA' || checkVideo"
                >
                  <ElOption
                    v-for="count in layer === 'MEDIA' || checkVideo ? [1] : [1, 2, 3, 4]"
                    :key="count"
                    :value="count"
                    :label="`${count} 个请求`"
                  />
                </ElSelect>
              </ElFormItem>
            </div>
          </div>
          <div v-else-if="step === 2" class="wizard-stage settings-stage">
            <header class="stage-heading">
              <h3>按需检查视频收帧</h3>
            </header>
            <div class="video-setting">
              <div>
                <strong>{{ layer === 'MEDIA' ? '视频收帧检查' : '在线状态通过后检查视频' }}</strong>
              </div>
              <ElFormItem
                v-if="layer === 'CONTROL'"
                label="在线状态通过后检查视频"
                class="video-check-toggle"
                ><ElSwitch
                  v-model="checkVideo"
                  aria-label="在线状态通过后检查视频"
                  @change="parallelism = 1"
              /></ElFormItem>
              <span v-else class="config-chip">沿用视频检测</span>
            </div>
            <PlanTargetSettings
              v-if="checkVideo || layer === 'MEDIA'"
              :targets="targets"
              :disabled="busy || !!pending || inspecting"
              @change="setTargetSelection"
            />
          </div>
          <div v-else-if="step === 3" class="wizard-stage settings-stage">
            <header class="stage-heading">
              <h3>设置执行频率与有效期</h3>
            </header>
            <div class="setting-list period-settings" aria-label="检测周期设置">
              <ElFormItem label="检测间隔" class="setting-row">
                <div class="setting-row-copy">
                  <strong>检测间隔</strong>
                  <span>每台设备再次执行在线状态检查的时间</span>
                </div>
                <ElSelect v-model="interval" aria-label="检测间隔">
                  <ElOption
                    v-for="minutes in [5, 10, 15, 30, 60, 120, 1440]"
                    :key="minutes"
                    :value="minutes"
                    :label="minutes >= 60 ? `每 ${minutes / 60} 小时` : `每 ${minutes} 分钟`"
                  />
                </ElSelect>
              </ElFormItem>
              <ElFormItem label="持续时间" class="setting-row">
                <div class="setting-row-copy">
                  <strong>有效时长</strong>
                  <span>任务持续运行后自动结束</span>
                </div>
                <ElSelect v-model="hours" aria-label="持续时间">
                  <ElOption
                    v-for="value in [1, 6, 24, 72, 168]"
                    :key="value"
                    :value="value"
                    :label="`${value} 小时`"
                  />
                </ElSelect>
              </ElFormItem>
            </div>
            <div class="configuration-summary">
              <span class="config-chip">{{
                layer === 'MEDIA' ? '视频收帧' : checkVideo ? '在线状态 → 视频' : '在线状态'
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
              !inspecting &&
              (step > 0 || blockedTargets.length || limitExceeded) &&
              (targets.length || limitExceeded)
            "
            class="patrol-target-summary"
            role="status"
          >
            <span v-if="limitExceeded">授权范围超过 2000 台，请缩小范围；未截取部分设备下发。</span>
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
      <PlanTaskList
        v-else
        v-model:task-state="taskState"
        v-model:task-search="taskSearch"
        v-model:report-plan-id="reportPlanId"
        :plans="plans"
        :loading="loading"
        :busy="busy"
        :has-pending="!!pending"
        :error="error"
        :poll-paused="pollPaused"
        :now="now"
        @refresh="load()"
        @stop="stop"
        @resume="resume"
        @reuse="reuse"
        @remove="remove"
      />
    </div>
    <template #footer>
      <div class="patrol-footer">
        <p v-if="tab === 'create'" class="selection-count" role="status">
          <template v-if="selectedCount !== null"
            >已选择 <strong>{{ selectedCount }}</strong> 台相机设备</template
          ><template v-else
            >已选择{{ scope === 'GROUPS' ? ` ${selectedGroups.length} 个分组` : '范围' }}</template
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
              class="patrol-next-button"
              :loading="navigationBusy"
              :disabled="
                busy ||
                !!pending ||
                navigationBusy ||
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
