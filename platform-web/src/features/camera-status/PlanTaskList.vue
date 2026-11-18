<script setup lang="ts">
import { computed } from 'vue'
import {
  ElButton,
  ElDropdown,
  ElDropdownItem,
  ElDropdownMenu,
  ElEmpty,
  ElInput,
  ElOption,
  ElSelect,
} from 'element-plus'
import { Refresh, Search } from '@element-plus/icons-vue'
import type { StatusPlan } from '../../api/camera-status/plans'
import { formatDateTime } from '../../utils/dateTime'
import { reasonLabel } from './model'
import { planStateLabel, planStateLabels, progressItems } from './presentation'

const props = defineProps<{
  plans: StatusPlan[]
  loading: boolean
  busy: boolean
  hasPending: boolean
  error: unknown
  pollPaused: boolean
  now: number
  taskState: string
  taskSearch: string
  reportPlanId: string | null
}>()

const emit = defineEmits<{
  'update:taskState': [value: string]
  'update:taskSearch': [value: string]
  'update:reportPlanId': [value: string | null]
  refresh: []
  stop: [plan: StatusPlan]
  resume: [plan: StatusPlan]
  reuse: [plan: StatusPlan]
  remove: [plan: StatusPlan]
}>()

const expiryFormatter = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hourCycle: 'h23',
})

function planActive(plan: StatusPlan) {
  return plan.state === 'ACTIVE' && Date.parse(plan.endAt) > props.now
}

function planTitle(plan: StatusPlan) {
  return plan.name?.trim() || `巡检任务 #${plan.planId.slice(-4)}`
}

function planState(plan: StatusPlan) {
  return planActive(plan)
    ? 'ACTIVE'
    : Date.parse(plan.endAt) <= props.now || plan.state === 'EXPIRED'
      ? 'EXPIRED'
      : 'STOPPED'
}

function checkLabel(plan: StatusPlan) {
  return plan.layer === 'MEDIA' ? '视频收帧' : plan.checkVideo ? '在线状态 / 视频' : '在线状态'
}

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
  props.plans.filter(
    (plan) =>
      (props.taskState === 'ALL' || planState(plan) === props.taskState) &&
      planTitle(plan).toLocaleLowerCase().includes(props.taskSearch.trim().toLocaleLowerCase()),
  ),
)

const reportPlan = computed(() => props.plans.find((plan) => plan.planId === props.reportPlanId))
</script>

<template>
  <section class="patrol-tasks" aria-label="我的巡检任务" :aria-busy="loading">
    <header class="patrol-toolbar">
      <div class="task-filters">
        <ElSelect
          :model-value="taskState"
          aria-label="任务状态"
          @update:model-value="emit('update:taskState', $event)"
          ><ElOption value="ALL" label="全部状态" /><ElOption
            v-for="(label, value) in planStateLabels"
            :key="value"
            :value="value"
            :label="label" /></ElSelect
        ><ElInput
          :model-value="taskSearch"
          :prefix-icon="Search"
          clearable
          placeholder="搜索任务名称"
          aria-label="搜索任务名称"
          @update:model-value="emit('update:taskSearch', $event)"
        />
      </div>
      <ElButton :icon="Refresh" :disabled="busy" :loading="loading" @click="emit('refresh')"
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
              <span class="task-state">{{ planStateLabel(planState(plan)) }}</span>
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
            :disabled="busy || hasPending"
            @click="emit('stop', plan)"
            >暂停</ElButton
          >
          <ElButton
            v-else-if="plan.state === 'STOPPED' && Date.parse(plan.endAt) > now"
            class="task-action-link"
            link
            size="small"
            :disabled="busy || hasPending"
            @click="emit('resume', plan)"
            >恢复执行</ElButton
          >
          <ElButton
            v-else
            class="task-action-link"
            link
            size="small"
            :disabled="busy || hasPending"
            @click="emit('reuse', plan)"
            >复用范围</ElButton
          >
          <ElButton
            class="task-report-button"
            size="small"
            :aria-expanded="reportPlanId === plan.planId"
            @click="emit('update:reportPlanId', reportPlanId === plan.planId ? null : plan.planId)"
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
                <ElDropdownItem :disabled="busy || hasPending" @click="emit('remove', plan)"
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
          <p v-if="planActive(plan) && plan.nextAt">下次检测 {{ formatDateTime(plan.nextAt) }}</p>
        </section>
      </article>
    </div>
  </section>
</template>
