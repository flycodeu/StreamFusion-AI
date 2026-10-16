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
  ElTree,
  ElSteps,
  ElStep,
  ElTabs,
  ElTabPane,
  ElRadioGroup,
  ElRadioButton,
  ElSwitch,
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
import PlanCameraPicker from './PlanCameraPicker.vue'

const props = defineProps<{ groupKeys: string[] }>()
const scope = ref<'PAGE' | 'ALL' | 'PLAN' | 'GROUPS'>('PAGE')
const selectedKeys = ref([...new Set(props.groupKeys)])
const tab = ref('create')
const step = ref(0)
const selectedGroups = ref<string[]>([])
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
let pageActive = true
let timer: ReturnType<typeof globalThis.setInterval> | undefined
function planActive(plan: StatusPlan) {
  return plan.state === 'ACTIVE' && Date.parse(plan.endAt) > now.value
}
const targets = ref<PlanTarget[]>([]),
  inspecting = ref(false),
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
  if (busy.value || pending.value) return
  reusedPlan.value = plan
  tab.value = 'create'
  step.value = 0
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
  if (busy.value || pending.value || inspecting.value) return
  if (step.value === 0) {
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
function checkGroups(_node: unknown, checked: { checkedKeys: (string | number)[] }) {
  if (busy.value || pending.value || inspecting.value) return
  selectedGroups.value = checked.checkedKeys.map(String)
  changeScope()
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
  busy.value = true
  error.value = null
  errorOperation.value = 'create'
  try {
    await createStatusPlan(pending.value)
    if (!active()) return
    pending.value = null
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
    <ElTabs v-model="tab"
      ><ElTabPane label="新建巡检" name="create" /><ElTabPane
        :label="`我的任务（${plans.length}）`"
        name="tasks"
    /></ElTabs>
    <section
      v-if="tab === 'create'"
      ref="createPanel"
      class="patrol-create"
      aria-label="下发巡检任务"
    >
      <ElSteps :active="step" finish-status="success" simple class="patrol-steps">
        <ElStep title="选择相机" /><ElStep title="连接检测" /><ElStep title="视频检测" /><ElStep
          title="检测周期"
        />
      </ElSteps>
      <ElForm
        label-position="top"
        :disabled="busy || !!pending || inspecting"
        @submit.prevent="step === 3 ? create() : nextStep()"
      >
        <div v-if="step === 0" class="wizard-stage">
          <ElFormItem label="选择范围">
            <ElRadioGroup v-model="scope" aria-label="选择范围" @change="changeScope">
              <ElRadioButton value="PAGE">选择相机</ElRadioButton>
              <ElRadioButton value="GROUPS">按分组</ElRadioButton>
              <ElRadioButton value="ALL">全部相机</ElRadioButton>
              <ElRadioButton v-if="reusedPlan" value="PLAN">复用范围</ElRadioButton>
            </ElRadioGroup>
          </ElFormItem>
          <PlanCameraPicker
            v-if="scope === 'PAGE'"
            v-model="selectedKeys"
            :disabled="busy || !!pending || inspecting"
            @update:model-value="changeScope"
          />
          <ElFormItem v-else-if="scope === 'GROUPS'" label="视频分组">
            <ElTree
              :data="groupNodes"
              :props="{
                label: 'name',
                children: 'children',
                disabled: () => busy || !!pending || inspecting,
              }"
              node-key="groupId"
              show-checkbox
              check-strictly
              :default-checked-keys="selectedGroups"
              @check="checkGroups"
            />
          </ElFormItem>
          <p v-else-if="scope === 'ALL'" class="patrol-note">巡检全部已授权相机，最多 2000 台。</p>
          <p v-else class="patrol-note">
            已复用任务 {{ reusedPlan?.planId }} 的范围，确认后启动新任务。
          </p>
        </div>
        <div v-else-if="step === 1" class="wizard-stage patrol-settings">
          <p class="patrol-note">
            {{
              layer === 'MEDIA'
                ? '沿用旧任务：仅检查视频，或切换为先检测连接。'
                : '先检查相机连接或平台在线状态。'
            }}
          </p>
          <ElFormItem v-if="layer === 'MEDIA'" label="复用旧任务">
            <ElButton @click="layer = 'CONTROL'">改为先检测连接</ElButton>
            <span>原任务仅检查视频</span>
          </ElFormItem>
          <ElFormItem label="调度方式"
            ><ElSelect v-model="dispatchMode" @change="parallelism = 1">
              <ElOption value="POLLING" label="逐个轮询" /><ElOption
                value="PARALLEL"
                label="有界并行"
              /> </ElSelect
          ></ElFormItem>
          <ElFormItem v-if="dispatchMode === 'PARALLEL'" label="并发请求数"
            ><ElSelect v-model="parallelism" :disabled="layer === 'MEDIA' || checkVideo">
              <ElOption
                v-for="count in layer === 'MEDIA' || checkVideo ? [1] : [1, 2, 3, 4]"
                :key="count"
                :value="count"
                :label="`${count} 个请求`"
              /> </ElSelect
          ></ElFormItem>
        </div>
        <div v-else-if="step === 2" class="wizard-stage">
          <ElFormItem v-if="layer === 'CONTROL'" label="连接通过后检查视频">
            <ElSwitch
              v-model="checkVideo"
              aria-label="连接通过后检查视频"
              @change="parallelism = 1"
            />
          </ElFormItem>
          <p class="patrol-note">
            {{
              layer === 'MEDIA'
                ? '短时采样检查是否持续收帧。'
                : checkVideo
                  ? '连接通过后短时采样，连接异常则跳过本轮视频检查。'
                  : '仅检查相机连接，不拉取视频。'
            }}
          </p>
          <p class="patrol-note">海康平台目标仅查询在线状态，视频检测适用于直连流。</p>
        </div>
        <div v-else class="wizard-stage patrol-settings">
          <ElFormItem label="检测间隔"
            ><ElSelect v-model="interval" aria-label="检测间隔">
              <ElOption
                v-for="minutes in [5, 10, 15, 30, 60, 120, 1440]"
                :key="minutes"
                :value="minutes"
                :label="minutes >= 60 ? `每 ${minutes / 60} 小时` : `每 ${minutes} 分钟`"
              /> </ElSelect
          ></ElFormItem>
          <ElFormItem label="持续时间"
            ><ElSelect v-model="hours" aria-label="持续时间">
              <ElOption
                v-for="value in [1, 6, 24, 72, 168]"
                :key="value"
                :value="value"
                :label="`${value} 小时`"
              /> </ElSelect
          ></ElFormItem>
          <p class="patrol-note">
            {{ layer === 'MEDIA' ? '视频收帧' : checkVideo ? '连接 → 视频' : '相机连接' }} ·
            {{ dispatchMode === 'POLLING' ? '逐个轮询' : '有界并行' }}
          </p>
        </div>
      </ElForm>
      <div
        v-if="inspecting || targets.length || limitExceeded"
        class="patrol-target-summary"
        role="status"
      >
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
      <p v-if="step === 3 && hours * 60 < interval" class="patrol-note" role="alert">
        持续时间不能小于检测间隔。
      </p>
    </section>
    <section v-else aria-label="我的巡检任务">
      <header class="patrol-heading">
        <h3>
          我的任务 <span>{{ plans.length }}</span>
        </h3>
        <ElButton :disabled="busy" :loading="loading" @click="load()">刷新任务</ElButton>
      </header>
      <p v-if="!plans.length && !loading && !error" class="patrol-note">
        暂无任务，请在“新建巡检”中选择相机。
      </p>
      <p v-if="pollPaused" class="patrol-note" role="status">
        读取连续失败，已暂停自动刷新，请点击“刷新任务”。
      </p>
      <article v-for="plan in plans" :key="plan.planId" class="patrol-row">
        <div>
          <div class="patrol-row-title">
            <strong
              >{{ plan.targetCount }} 台设备 ·
              {{
                plan.layer === 'MEDIA'
                  ? '视频收帧'
                  : plan.checkVideo
                    ? '连接 → 视频（直连）'
                    : '连接 / 平台状态'
              }}
              · 每 {{ plan.intervalMinutes }} 分钟</strong
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
    </section>
    <template #footer
      ><div class="patrol-footer">
        <div v-if="tab === 'create'" class="patrol-submit">
          <ElButton v-if="step > 0" :disabled="busy || !!pending" @click="step--">上一步</ElButton>
          <ElButton
            v-if="step < 3"
            type="primary"
            :loading="inspecting"
            :disabled="
              busy ||
              !!pending ||
              inspecting ||
              (step === 0 &&
                ((scope === 'PAGE' && !selectedKeys.length) ||
                  (scope === 'GROUPS' && !selectedGroups.length)))
            "
            @click="nextStep"
            >下一步</ElButton
          >
          <ElButton v-else type="primary" :disabled="!canCreate" :loading="busy" @click="create">{{
            pending ? '重试原计划' : '启动巡检'
          }}</ElButton>
        </div>
        <ElButton :disabled="busy" @click="close">关闭</ElButton>
      </div></template
    >
  </ElDialog>
</template>
<style scoped>
.patrol-create {
  padding: 18px;
  border: 1px solid var(--el-border-color-light);
  border-radius: 8px;
  background: var(--el-fill-color-extra-light);
}
.patrol-steps {
  margin-bottom: 24px;
}
.wizard-stage {
  min-height: 160px;
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
  margin-right: auto;
}
.patrol-footer {
  display: flex;
  justify-content: flex-end;
  gap: 12px;
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
  align-content: start;
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
  .patrol-steps {
    padding: 12px 8px;
  }
  .patrol-steps :deep(.el-step__head) {
    display: none;
  }
  .patrol-steps :deep(.el-step__title) {
    max-width: none;
    white-space: nowrap;
    font-size: 12px;
  }
  .patrol-steps :deep(.el-step__arrow) {
    margin: 0 4px;
  }
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
