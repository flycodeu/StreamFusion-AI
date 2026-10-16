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
  ElTreeSelect,
} from 'element-plus'
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

const props = defineProps<{ groupKeys: string[] }>()
const scope = ref<'PAGE' | 'ALL' | 'PLAN' | 'GROUPS'>('GROUPS')
const selectedGroups = ref<string[]>([])
const groupNodes = ref<GroupNode[]>([])
const layer = ref<'CONTROL' | 'MEDIA'>('MEDIA')
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
let pageActive = true
let timer: ReturnType<typeof globalThis.setInterval> | undefined
function planActive(plan: StatusPlan) {
  return plan.state === 'ACTIVE' && Date.parse(plan.endAt) > now.value
}
const targets = ref<PlanTarget[]>([]),
  inspecting = ref(true),
  inspectionError = ref<unknown>(null)
const eligibleKeys = computed(() =>
  targets.value.filter((target) => !target.reasonCode).map((target) => target.groupKey),
)
const blockedTargets = computed(() => targets.value.filter((target) => target.reasonCode))
const canCreate = computed(
  () =>
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
            : { scope: 'PAGE', groupKeys: [...new Set(props.groupKeys)] },
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
  if (busy.value || pending.value) return
  reusedPlan.value = plan
  scope.value = 'PLAN'
  interval.value = plan.intervalMinutes
  hours.value = Math.max(
    1,
    Math.min(168, Math.round((Date.parse(plan.endAt) - Date.parse(plan.startAt)) / 3_600_000)),
  )
  layer.value = plan.layer
  dispatchMode.value = plan.dispatchMode
  parallelism.value = plan.parallelism
  await inspectTargets()
  createPanel.value?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
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
  const active = capture()
  loading.value = true
  if (!silent) {
    error.value = null
    failures = 0
    pollPaused.value = false
  }
  try {
    const result = await getStatusPlans()
    if (active()) {
      plans.value = result
      const serverNow = result.find((plan) => plan.serverNow)?.serverNow
      clockOffset = serverNow ? Date.parse(serverNow) - Date.now() : 0
      now.value = Date.now() + clockOffset
      failures = 0
      if (errorOperation.value === 'load') error.value = null
    }
  } catch (cause) {
    if (active()) {
      if (!error.value || errorOperation.value === 'load') {
        error.value = cause
        errorOperation.value = 'load'
      }
      if (++failures >= 3) pollPaused.value = true
    }
  } finally {
    if (active()) {
      loading.value = false
      nextRefresh = Date.now() + 30_000
    }
  }
}
async function create() {
  if (busy.value || !canCreate.value) return
  const active = capture()
  pending.value ??= {
    clientRequestId: `${Date.now()}-${crypto.randomUUID()}`,
    groupKeys: [...eligibleKeys.value],
    intervalMinutes: interval.value,
    durationHours: hours.value,
    layer: layer.value,
    dispatchMode: dispatchMode.value,
    parallelism:
      layer.value === 'MEDIA' || dispatchMode.value === 'POLLING' ? 1 : parallelism.value,
    ...(scope.value === 'GROUPS' ? { groupIds: [...selectedGroups.value] } : {}),
    ...(scope.value === 'PLAN' ? { planId: reusedPlan.value!.planId } : {}),
  }
  busy.value = true
  error.value = null
  errorOperation.value = 'create'
  try {
    await createStatusPlan(pending.value)
    if (!active()) return
    pending.value = null
    emit('changed')
    await load()
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
  inspecting.value = false
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
    width="1080px"
    class="management-dialog patrol-dialog"
    append-to-body
    :before-close="close"
    :close-on-click-modal="false"
  >
    <RequestError :error="error" :advice="planErrorAdvice" />
    <RequestError :error="inspectionError" />
    <RequestError :error="groupError" />
    <ElButton v-if="groupError" link @click="loadGroups">重新加载分组</ElButton>
    <ElButton
      v-if="inspectionError"
      link
      :disabled="busy || !!pending || inspecting"
      @click="inspectTargets"
      >重新核对目标</ElButton
    >
    <section ref="createPanel" class="patrol-create" aria-label="下发巡检任务">
      <h3>下发巡检任务</h3>
      <ElForm label-position="top" :disabled="busy || !!pending" @submit.prevent="create">
        <ElFormItem label="1. 检测范围">
          <ElSelect v-model="scope" aria-label="检测范围" @change="inspectTargets">
            <ElOption value="GROUPS" label="选择视频分组" />
            <ElOption value="PAGE" :label="`当前页设备（${new Set(groupKeys).size} 台）`" />
            <ElOption value="ALL" label="全量巡检 · 全部已授权设备" />
            <ElOption v-if="reusedPlan" value="PLAN" label="复用所选任务范围" />
          </ElSelect>
        </ElFormItem>
        <ElFormItem v-if="scope === 'GROUPS'" label="视频分组">
          <ElTreeSelect
            v-model="selectedGroups"
            :data="groupNodes"
            :props="{ label: 'name', children: 'children' }"
            node-key="groupId"
            multiple
            show-checkbox
            check-strictly
            :default-expand-all="false"
            placeholder="选择分组（包含子分组）"
            collapse-tags
            collapse-tags-tooltip
            @change="inspectTargets"
          />
        </ElFormItem>
        <div class="patrol-settings">
          <ElFormItem label="检查内容"
            ><ElSelect v-model="layer" @change="parallelism = 1">
              <ElOption value="MEDIA" label="视频收帧 / 平台状态" /><ElOption
                value="CONTROL"
                label="RTSP连接 / 平台状态"
              /> </ElSelect
          ></ElFormItem>
          <ElFormItem label="调度方式"
            ><ElSelect v-model="dispatchMode" @change="parallelism = 1">
              <ElOption value="POLLING" label="逐个轮询" /><ElOption
                value="PARALLEL"
                label="有界并行"
              /> </ElSelect
          ></ElFormItem>
          <ElFormItem v-if="dispatchMode === 'PARALLEL'" label="最大并发">
            <ElSelect v-model="parallelism" :disabled="layer === 'MEDIA'">
              <ElOption
                v-for="count in layer === 'MEDIA' ? [1] : [1, 2, 3, 4]"
                :key="count"
                :value="count"
                :label="`${count} 路`"
              />
            </ElSelect>
          </ElFormItem>
          <ElFormItem label="2. 检测间隔"
            ><ElSelect v-model="interval" aria-label="检测间隔">
              <ElOption
                v-for="minutes in [15, 30, 60, 120, 1440]"
                :key="minutes"
                :value="minutes"
                :label="minutes >= 60 ? `每 ${minutes / 60} 小时` : `每 ${minutes} 分钟`"
              /> </ElSelect
          ></ElFormItem>
          <ElFormItem label="3. 持续时间"
            ><ElSelect v-model="hours" aria-label="持续时间">
              <ElOption
                v-for="value in [1, 6, 24, 72, 168]"
                :key="value"
                :value="value"
                :label="`${value} 小时`"
              /> </ElSelect
          ></ElFormItem>
        </div>
      </ElForm>
      <div class="patrol-target-summary" role="status">
        <span v-if="inspecting">正在核对代表流配置…</span>
        <span v-else-if="limitExceeded"
          >授权范围超过 2000 台，请缩小范围；未截取部分设备下发。</span
        >
        <span v-else-if="!inspectionError"
          ><strong>{{ eligibleKeys.length }}</strong> 台可检测
          <span> / {{ targets.length }} 台 · {{ blockedTargets.length }} 台跳过</span></span
        >
        <ElButton link :disabled="busy || !!pending || inspecting" @click="inspectTargets"
          >重新核对</ElButton
        >
      </div>
      <div v-if="blockedTargets.length" class="patrol-blocked">
        <details>
          <summary>查看 {{ blockedTargets.length }} 台跳过原因（不计为离线）</summary>
          <ul>
            <li v-for="target in blockedTargets" :key="target.groupKey">
              {{ target.name }}：{{ reasonLabel(target.reasonCode) }}
            </li>
          </ul>
        </details>
      </div>
      <p v-if="pending" role="status">响应未确认，重试将使用原计划请求。</p>
      <p v-if="hours * 60 < interval" class="patrol-note" role="alert">
        持续时间不能小于检测间隔。
      </p>
      <div class="patrol-submit">
        <ElButton
          type="primary"
          size="large"
          :disabled="!canCreate"
          :loading="busy"
          @click="create"
          >{{ pending ? '重试原计划' : '启动巡检' }}</ElButton
        ><span>下发 {{ eligibleKeys.length }} 台 · {{ hours }} 小时后结束</span>
      </div>
      <p v-if="scope === 'PLAN'" class="patrol-note">
        已复用范围，请确认后启动新任务；旧任务记录保留。
      </p>
    </section>
    <header class="patrol-heading">
      <h3>
        我的任务 <span>{{ plans.length }}</span>
      </h3>
      <ElButton :disabled="busy" :loading="loading" @click="load()">刷新任务</ElButton>
    </header>
    <p v-if="!plans.length && !loading && !error" class="patrol-note">
      暂无任务，选择范围后点击“启动巡检”。
    </p>
    <p v-if="pollPaused" class="patrol-note" role="status">
      读取连续失败，已暂停自动刷新，请点击“刷新任务”。
    </p>
    <article v-for="plan in plans" :key="plan.planId" class="patrol-row">
      <div>
        <div class="patrol-row-title">
          <strong
            >{{ plan.targetCount }} 台设备 ·
            {{ plan.layer === 'MEDIA' ? '收帧 / 平台状态' : '连接 / 平台状态' }} · 每
            {{ plan.intervalMinutes }} 分钟</strong
          >
          <span class="patrol-state" :class="{ 'is-active': planActive(plan) }">{{
            planActive(plan) ? '执行中' : plan.state === 'STOPPED' ? '已停止' : '已到期'
          }}</span>
        </div>
        <p>任务 {{ plan.planId }}</p>
        <p>{{ formatDateTime(plan.startAt) }} 开始 · {{ formatDateTime(plan.endAt) }} 截止</p>
        <p v-if="planActive(plan) && plan.nextAt">下次检测 {{ formatDateTime(plan.nextAt) }}</p>
        <p v-if="plan.blockedCount">{{ plan.blockedCount }} 个目标无法继续检测</p>
        <p v-if="plan.lastReasonCode">{{ reasonLabel(plan.lastReasonCode) }}</p>
        <p v-if="plan.progress" class="patrol-progress">
          最近回执：成功 {{ plan.progress.success }} · 异常 {{ plan.progress.failed }} · 进行中
          {{ plan.progress.pending }} · 未执行 {{ plan.progress.skipped }} · 未确认
          {{ plan.progress.unknown }}
        </p>
      </div>
      <ElButton
        v-if="planActive(plan)"
        type="danger"
        plain
        :disabled="busy || !!pending"
        @click="stop(plan)"
        >停止</ElButton
      >
      <ElButton
        v-else-if="plan.state === 'STOPPED' && Date.parse(plan.endAt) > now"
        :disabled="busy || !!pending"
        type="primary"
        @click="resume(plan)"
        >恢复执行</ElButton
      >
      <ElButton v-else :disabled="busy || !!pending" @click="reuse(plan)">复用范围</ElButton>
    </article>
    <template #footer><ElButton :disabled="busy" @click="close">关闭</ElButton></template>
  </ElDialog>
</template>
<style scoped>
.patrol-create {
  padding: 18px;
  border: 1px solid var(--el-border-color-light);
  border-radius: 8px;
  background: var(--el-fill-color-extra-light);
}
.patrol-create h3 {
  margin: 0 0 18px;
  font-size: 16px;
}
.patrol-create :deep(.el-select) {
  width: 100%;
}
.patrol-target-summary,
.patrol-submit,
.patrol-row-title {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.patrol-target-summary {
  justify-content: space-between;
}
.patrol-target-summary strong {
  font-size: 22px;
  color: var(--el-color-primary);
}
.patrol-submit {
  margin-top: 16px;
}
.patrol-submit > span {
  color: var(--text-secondary);
}
.patrol-state {
  padding: 3px 8px;
  border-radius: 4px;
  background: var(--el-fill-color);
  font-size: 13px;
}
.patrol-state.is-active {
  color: var(--el-color-success);
  background: var(--el-color-success-light-9);
}
.patrol-blocked summary {
  cursor: pointer;
  padding: 10px 0;
}
.patrol-settings {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}
.patrol-settings :deep(.el-select) {
  width: 100%;
}
.patrol-blocked {
  max-height: 180px;
  overflow-y: auto;
  overflow-wrap: anywhere;
}
.patrol-blocked ul {
  margin: 0 0 16px;
  padding-left: 20px;
  color: var(--text-secondary);
  font-size: 13px;
  line-height: 1.7;
}
.patrol-note,
.patrol-row p {
  color: var(--text-secondary);
  line-height: 1.6;
}
.patrol-heading,
.patrol-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 16px;
}
.patrol-heading {
  margin-top: 24px;
}
.patrol-heading h3 {
  font-size: 16px;
}
.patrol-heading h3 span {
  color: var(--text-secondary);
  font-weight: 400;
}
.patrol-row {
  padding: 12px 0;
  border-top: 1px solid var(--border-subtle);
}
.patrol-row p {
  margin: 6px 0 0;
  font-size: 13px;
}
.patrol-row > div {
  min-width: 0;
  overflow-wrap: anywhere;
}
@media (max-width: 480px) {
  .patrol-create {
    padding: 12px;
  }
  .patrol-row {
    align-items: flex-start;
    flex-direction: column;
    gap: 10px;
  }
  .patrol-settings {
    grid-template-columns: 1fr;
    gap: 0;
  }
}
</style>
