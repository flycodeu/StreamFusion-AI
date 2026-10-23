<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElAlert, ElButton, ElCard, ElTag } from 'element-plus'
import { ApiRequestError } from '../../lib/http/error'
import { getHealth } from '../../api/health/api'
import { getPreviewCapacity, getPreviewCatalog } from '../../api/preview/api'
import type { PreviewCapacity, PreviewChannel } from '../../api/preview/types'
import { formatDateTime } from '../../utils/dateTime'
import { sessionState } from '../../session/state'

defineOptions({ name: 'HomeView' })

const router = useRouter()

type HealthStatus = 'idle' | 'checking' | 'up' | 'failed'

const statusLabels: Record<HealthStatus, string> = {
  idle: '等待检查',
  checking: '检查中',
  up: '核心在线',
  failed: '连接异常',
}

const status = ref<HealthStatus>('idle')
const loading = computed(() => status.value === 'checking')
const detail = ref('')
const errorCode = ref('')
const traceId = ref('')
const checkedAt = ref('')
const capacity = ref<PreviewCapacity | null>(null)
const capacityLoading = ref(false)
const channels = ref<PreviewChannel[]>([])
const totalChannels = ref<number | null>(null)
const trendRange = ref<'24h' | '7d'>('24h')

interface ScrollElementLike {
  clientWidth: number
  clientHeight: number
  scrollTop: number
  scrollHeight: number
}

const scrollContainerRef = ref<ScrollElementLike | null>(null)
const gridCols = ref(6)
const baseRows = ref(2)
const extraRows = ref(0)
let resizeObserver: { disconnect: () => void } | null = null

const hasCameraModule = computed(() => sessionState.me?.modules?.includes('camera') ?? false)

const weekdayNames = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']

const trendSeries24h = [
  { label: '00:00', value: 24 },
  { label: '02:00', value: 18 },
  { label: '04:00', value: 15 },
  { label: '06:00', value: 32 },
  { label: '08:00', value: 58 },
  { label: '10:00', value: 74 },
  { label: '12:00', value: 62 },
  { label: '14:00', value: 82 },
  { label: '16:00', value: 76 },
  { label: '18:00', value: 64 },
  { label: '20:00', value: 46 },
  { label: '22:00', value: 30 },
]

const trendValues7d = [42, 56, 48, 68, 75, 60, 52]

const trendSeries7d = computed(() => {
  const now = new Date()
  return trendValues7d.map((value, idx) => {
    const offset = 6 - idx
    const date = new Date(now.getFullYear(), now.getMonth(), now.getDate() - offset)
    const month = String(date.getMonth() + 1).padStart(2, '0')
    const day = String(date.getDate()).padStart(2, '0')
    const week = weekdayNames[date.getDay()]
    return {
      label: `${month}/${day} ${week}`,
      value,
    }
  })
})

const activeTrendSeries = computed(() =>
  trendRange.value === '24h' ? trendSeries24h : trendSeries7d.value,
)

const categoryPlaceholders = [
  { id: 1, name: '区域入侵检测', width: '45%' },
  { id: 2, name: '人员聚集识别', width: '32%' },
  { id: 3, name: '安全帽/工装检测', width: '24%' },
  { id: 4, name: '车辆违停抓拍', width: '18%' },
]

function updateGridMetrics(): void {
  const el = scrollContainerRef.value
  const fallbackWidth = Math.max(
    320,
    (typeof globalThis.innerWidth === 'number' ? globalThis.innerWidth : 1440) - 260,
  )
  const fallbackHeight = Math.max(
    240,
    (typeof globalThis.innerHeight === 'number' ? globalThis.innerHeight : 900) - 430,
  )

  const containerWidth = el && el.clientWidth > 0 ? el.clientWidth : fallbackWidth
  const containerHeight = el && el.clientHeight > 0 ? el.clientHeight : fallbackHeight

  const gap = containerWidth >= 1800 ? 16 : 14
  const minCardWidth = containerWidth >= 2200 ? 250 : containerWidth >= 1400 ? 230 : 210

  const cols = Math.max(1, Math.floor((containerWidth + gap) / (minCardWidth + gap)))
  const cardWidth = (containerWidth - gap * (cols - 1)) / cols
  const cardHeight = (cardWidth * 9) / 16 + 60
  const rows = Math.max(2, Math.ceil((containerHeight + gap) / (cardHeight + gap)))

  gridCols.value = cols
  baseRows.value = rows
}

const totalSnapshotCount = computed(() => gridCols.value * (baseRows.value + extraRows.value))

const snapshotCards = computed(() => {
  const list = channels.value
  return Array.from({ length: totalSnapshotCount.value }, (_, idx) => {
    const channel = list.length > 0 ? list[idx % list.length] : undefined
    const indexStr = String(idx + 1).padStart(2, '0')
    return {
      id: idx + 1,
      badge: `#${indexStr}`,
      pointName: channel?.name || `监控点位 #${indexStr}`,
      deviceName: channel?.deviceName || '待接入通道',
      time: '--:--:--',
    }
  })
})

function handleSnapshotScroll(event: { target: unknown }): void {
  const target = event.target as ScrollElementLike | null
  if (!target) return
  if (target.scrollTop + target.clientHeight >= target.scrollHeight - 48) {
    extraRows.value = Math.min(extraRows.value + 2, 12)
  }
}

async function checkHealth(): Promise<void> {
  if (loading.value) return
  status.value = 'checking'
  detail.value = ''
  errorCode.value = ''
  traceId.value = ''

  try {
    await getHealth()
    status.value = 'up'
  } catch (error: unknown) {
    status.value = 'failed'
    detail.value =
      error instanceof Error ? error.message : '服务健康检查未通过，请确认平台后端服务已启动。'
    if (error instanceof ApiRequestError) {
      errorCode.value = error.code
      traceId.value = error.traceId
    }
  } finally {
    checkedAt.value = formatDateTime(new Date())
  }

  if (hasCameraModule.value) {
    capacityLoading.value = true
    try {
      const [capResult, catResult] = await Promise.allSettled([
        getPreviewCapacity(),
        getPreviewCatalog({ page: 1, size: 24 }),
      ])
      if (capResult.status === 'fulfilled') {
        capacity.value = capResult.value
      }
      if (catResult.status === 'fulfilled') {
        channels.value = catResult.value.items
        totalChannels.value = catResult.value.total
      }
    } catch {
      capacity.value = null
      channels.value = []
      totalChannels.value = null
    } finally {
      capacityLoading.value = false
    }
  }
}

function navigateTo(path: string) {
  void router.push(path)
}

onMounted(() => {
  updateGridMetrics()
  if (typeof globalThis.ResizeObserver === 'function' && scrollContainerRef.value) {
    const observer = new globalThis.ResizeObserver(() => {
      updateGridMetrics()
    })
    observer.observe(scrollContainerRef.value as unknown as Parameters<typeof observer.observe>[0])
    resizeObserver = observer
  }
  if (typeof globalThis.addEventListener === 'function') {
    globalThis.addEventListener('resize', updateGridMetrics)
  }
  void checkHealth()
})

onBeforeUnmount(() => {
  resizeObserver?.disconnect()
  resizeObserver = null
  if (typeof globalThis.removeEventListener === 'function') {
    globalThis.removeEventListener('resize', updateGridMetrics)
  }
})
</script>

<template>
  <div class="content-page home-dashboard">
    <!-- 异常告警条（仅在健康检查失败时出现） -->
    <ElAlert
      v-if="status === 'failed'"
      class="error-banner"
      :title="detail"
      type="error"
      :closable="false"
      show-icon
    >
      <div class="error-meta">
        <span v-if="errorCode">错误码：{{ errorCode }}</span>
        <span v-if="traceId">TraceId：{{ traceId }}</span>
      </div>
    </ElAlert>

    <!-- 第一层：顶部 4 项核心指标 -->
    <section class="kpi-row" aria-label="核心运行指标">
      <ElCard shadow="never" class="kpi-card">
        <div class="kpi-head">
          <span class="kpi-title">接入视频源</span>
          <ElButton link type="primary" @click="navigateTo('/cameras/manage')">点位管理</ElButton>
        </div>
        <div class="kpi-body">
          <template v-if="totalChannels !== null">
            <span class="kpi-number">{{ totalChannels }}</span>
            <span class="kpi-unit">路</span>
          </template>
          <template v-else>
            <span class="kpi-placeholder">{{ capacityLoading ? '...' : '—' }}</span>
          </template>
        </div>
      </ElCard>

      <ElCard shadow="never" class="kpi-card">
        <div class="kpi-head">
          <span class="kpi-title">实时推理流</span>
          <ElButton link type="primary" @click="navigateTo('/preview/center')">实时预览</ElButton>
        </div>
        <div class="kpi-body">
          <template v-if="capacity">
            <span class="kpi-number">{{ capacity.occupiedSessions }}</span>
            <span class="kpi-unit">/ {{ capacity.maxSessions }} 路</span>
          </template>
          <template v-else>
            <span class="kpi-placeholder">{{ capacityLoading ? '...' : '—' }}</span>
          </template>
        </div>
      </ElCard>

      <ElCard shadow="never" class="kpi-card">
        <div class="kpi-head">
          <span class="kpi-title">今日推送统计</span>
          <ElTag size="small" type="info" effect="plain">待接入</ElTag>
        </div>
        <div class="kpi-body">
          <span class="kpi-number is-muted">—</span>
          <span class="kpi-unit">条</span>
        </div>
      </ElCard>

      <ElCard shadow="never" class="kpi-card">
        <div class="kpi-head">
          <span class="kpi-title">服务运行状态</span>
          <ElButton link type="primary" :loading="loading" @click="checkHealth">刷新</ElButton>
        </div>
        <div class="kpi-body kpi-status-body">
          <div class="status-indicator">
            <span
              class="status-dot"
              :class="{
                'is-up': status === 'up',
                'is-failed': status === 'failed',
              }"
            />
            <span class="kpi-status-text">{{ statusLabels[status] }}</span>
          </div>
          <span v-if="checkedAt" class="sync-time">{{ checkedAt }}</span>
        </div>
      </ElCard>
    </section>

    <!-- 第二层：统计趋势与分类占比 -->
    <section class="analytics-row" aria-label="推理推送统计">
      <ElCard shadow="never" class="panel-card chart-panel">
        <template #header>
          <div class="card-header">
            <div class="card-header-left">
              <h2 class="card-title">推送统计趋势</h2>
              <ElTag size="small" type="info" effect="plain">待接入</ElTag>
            </div>
            <div class="range-switch">
              <button
                type="button"
                class="switch-btn"
                :class="{ active: trendRange === '24h' }"
                @click="trendRange = '24h'"
              >
                24小时
              </button>
              <button
                type="button"
                class="switch-btn"
                :class="{ active: trendRange === '7d' }"
                @click="trendRange = '7d'"
              >
                近7天
              </button>
            </div>
          </div>
        </template>

        <div class="chart-placeholder-box">
          <div class="y-axis">
            <span>100</span>
            <span>75</span>
            <span>50</span>
            <span>25</span>
            <span>0</span>
          </div>
          <div class="chart-canvas">
            <div class="grid-lines">
              <div v-for="n in 5" :key="n" class="grid-line" />
            </div>
            <div
              class="bar-columns"
              :style="{ gridTemplateColumns: `repeat(${activeTrendSeries.length}, 1fr)` }"
            >
              <div v-for="(item, idx) in activeTrendSeries" :key="idx" class="bar-col">
                <div class="bar-track">
                  <div class="bar-fill" :style="{ height: `${item.value}%` }" />
                </div>
                <span class="x-label">
                  {{ item.label }}
                </span>
              </div>
            </div>
          </div>
        </div>
      </ElCard>

      <ElCard shadow="never" class="panel-card dist-panel">
        <template #header>
          <div class="card-header">
            <h2 class="card-title">推理触发分类占比</h2>
            <ElTag size="small" type="info" effect="plain">待接入</ElTag>
          </div>
        </template>

        <div class="dist-placeholder-box">
          <div class="ring-skeleton">
            <div class="ring-inner">
              <span class="ring-total">—</span>
              <span class="ring-label">总触发</span>
            </div>
          </div>
          <div class="dist-list">
            <div v-for="item in categoryPlaceholders" :key="item.id" class="dist-row">
              <div class="dist-row-head">
                <span class="dist-name">{{ item.name }}</span>
                <span class="dist-val">-- %</span>
              </div>
              <div class="dist-bar-bg">
                <div class="dist-bar-fill" :style="{ width: item.width }" />
              </div>
            </div>
          </div>
        </div>
      </ElCard>
    </section>

    <!-- 第三层：最新推理截图（按屏幕分辨率动态计算行列数量，大屏多展示、小屏少展示，支持滚动加载） -->
    <ElCard shadow="never" class="panel-card snapshot-wall-panel">
      <template #header>
        <div class="card-header">
          <div class="card-header-left">
            <h2 class="card-title">最新推理截图</h2>
            <ElTag size="small" type="info" effect="plain">待接入</ElTag>
          </div>
          <div class="card-header-right">
            <ElButton link type="primary" @click="navigateTo('/storage/manage')">
              存储管理
            </ElButton>
          </div>
        </div>
      </template>

      <div
        ref="scrollContainerRef"
        class="snapshot-scroll-container"
        @scroll="handleSnapshotScroll"
      >
        <div
          class="snapshot-grid"
          :style="{ gridTemplateColumns: `repeat(${gridCols}, minmax(0, 1fr))` }"
        >
          <article v-for="item in snapshotCards" :key="item.id" class="snapshot-card">
            <div class="snapshot-thumb">
              <span class="thumb-tag">{{ item.badge }}</span>
              <span class="thumb-status">待接入</span>
              <div class="thumb-placeholder">
                <div class="viewfinder-corner tl" />
                <div class="viewfinder-corner tr" />
                <div class="viewfinder-corner bl" />
                <div class="viewfinder-corner br" />
                <span class="thumb-label">暂无抓拍</span>
              </div>
            </div>
            <div class="snapshot-info">
              <div class="info-primary">
                <span class="point-name" :title="item.pointName">{{ item.pointName }}</span>
                <span class="capture-time">{{ item.time }}</span>
              </div>
              <div class="info-secondary">
                <span class="device-label" :title="item.deviceName">{{ item.deviceName }}</span>
                <span class="event-tag">待触发</span>
              </div>
            </div>
          </article>
        </div>
      </div>
    </ElCard>
  </div>
</template>

<style scoped>
.home-dashboard {
  --border-color: #e5e8ef;
  --text-main: #1d2129;
  --text-secondary: #4e5969;
  --text-muted: #86909c;
  --surface-subtle: #f7f8fa;

  height: calc(100dvh - var(--header-height, 56px));
  min-height: 640px;
  display: flex;
  flex-direction: column;
  gap: clamp(12px, 1.2dvh, 18px);
  padding-top: clamp(12px, 1.4dvh, 20px);
  padding-bottom: clamp(12px, 1.4dvh, 20px);
  box-sizing: border-box;
  overflow: hidden;
}

.error-banner {
  flex-shrink: 0;
  width: 100%;
}

.error-meta {
  display: flex;
  gap: 16px;
  font-size: 12px;
  margin-top: 4px;
}

/* 第一层：顶部 4 列 KPI */
.kpi-row {
  flex-shrink: 0;
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: clamp(12px, 1vw, 18px);
}

.kpi-card {
  border: 1px solid var(--border-color);
  border-radius: 6px;
}

.kpi-card :deep(.el-card__body) {
  padding: clamp(12px, 1.2dvh, 16px) 18px;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  gap: 6px;
}

.kpi-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.kpi-title {
  font-size: 13px;
  font-weight: 500;
  color: var(--text-secondary);
}

.kpi-body {
  display: flex;
  align-items: baseline;
  gap: 6px;
  font-variant-numeric: tabular-nums;
  min-height: 28px;
}

.kpi-status-body {
  align-items: center;
  justify-content: space-between;
}

.status-indicator {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #c9cdd4;
}

.status-dot.is-up {
  background: #00b42a;
  box-shadow: 0 0 0 3px rgba(0, 180, 42, 0.15);
}

.status-dot.is-failed {
  background: #f53f3f;
  box-shadow: 0 0 0 3px rgba(245, 63, 63, 0.15);
}

.kpi-status-text {
  font-size: 18px;
  font-weight: 600;
  color: var(--text-main);
}

.sync-time {
  font-size: 12px;
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
}

.kpi-number {
  font-size: clamp(22px, 2.2dvh, 28px);
  font-weight: 600;
  color: var(--text-main);
  line-height: 1.1;
}

.kpi-number.is-muted {
  color: var(--text-muted);
}

.kpi-unit {
  font-size: 13px;
  color: var(--text-muted);
}

.kpi-placeholder {
  font-size: 22px;
  font-weight: 600;
  color: var(--text-muted);
}

/* 第二层：统计趋势与分类占比（随屏幕纵向高度自适应缩放） */
.analytics-row {
  flex-shrink: 0;
  display: grid;
  grid-template-columns: 2fr 1fr;
  gap: clamp(12px, 1vw, 18px);
  height: clamp(200px, 24dvh, 300px);
}

.panel-card {
  border: 1px solid var(--border-color);
  border-radius: 6px;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.panel-card :deep(.el-card__header) {
  flex-shrink: 0;
  padding: 10px 18px;
  border-bottom: 1px solid #f0f2f5;
}

.panel-card :deep(.el-card__body) {
  flex: 1;
  min-height: 0;
  padding: 14px 18px;
  display: flex;
  flex-direction: column;
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.card-header-left,
.card-header-right {
  display: flex;
  align-items: center;
  gap: 10px;
}

.card-title {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  color: var(--text-main);
}

/* 趋势图占位 */
.range-switch {
  display: inline-flex;
  background: var(--surface-subtle);
  border: 1px solid var(--border-color);
  border-radius: 4px;
  padding: 2px;
}

.switch-btn {
  border: 0;
  background: transparent;
  padding: 2px 10px;
  font-size: 12px;
  color: var(--text-secondary);
  border-radius: 3px;
  cursor: pointer;
}

.switch-btn.active {
  background: #ffffff;
  color: var(--text-main);
  font-weight: 600;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.06);
}

.chart-placeholder-box {
  flex: 1;
  min-height: 0;
  display: flex;
  gap: 12px;
}

.y-axis {
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  padding-bottom: 20px;
  font-size: 11px;
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
  text-align: right;
  width: 26px;
}

.chart-canvas {
  flex: 1;
  position: relative;
  display: flex;
  flex-direction: column;
}

.grid-lines {
  position: absolute;
  inset: 0 0 20px 0;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  pointer-events: none;
}

.grid-line {
  border-bottom: 1px dashed #ebedf0;
  width: 100%;
}

.bar-columns {
  flex: 1;
  display: grid;
  grid-template-columns: repeat(12, 1fr);
  gap: 10px;
  align-items: stretch;
  z-index: 1;
}

.bar-col {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 5px;
}

.bar-track {
  flex: 1;
  width: 100%;
  max-width: 34px;
  display: flex;
  align-items: flex-end;
  background: rgba(242, 243, 245, 0.45);
  border-radius: 3px 3px 0 0;
}

.bar-fill {
  width: 100%;
  background: linear-gradient(180deg, #dbe6f6 0%, #eef3fb 100%);
  border-top: 2px solid #94b4eb;
  border-radius: 3px 3px 0 0;
}

.x-label {
  font-size: 11px;
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

/* 分类占比占位 */
.dist-placeholder-box {
  flex: 1;
  min-height: 0;
  display: flex;
  align-items: center;
  gap: 20px;
}

.ring-skeleton {
  width: clamp(92px, 11dvh, 120px);
  height: clamp(92px, 11dvh, 120px);
  border-radius: 50%;
  border: 10px solid #edf1f7;
  border-top-color: #b8cdf0;
  border-right-color: #d3e1f8;
  display: grid;
  place-items: center;
  flex-shrink: 0;
}

.ring-inner {
  display: flex;
  flex-direction: column;
  align-items: center;
}

.ring-total {
  font-size: 18px;
  font-weight: 600;
  color: var(--text-muted);
}

.ring-label {
  font-size: 11px;
  color: var(--text-muted);
}

.dist-list {
  flex: 1;
  display: flex;
  flex-direction: column;
  justify-content: center;
  gap: 10px;
}

.dist-row {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.dist-row-head {
  display: flex;
  justify-content: space-between;
  font-size: 12px;
}

.dist-name {
  color: var(--text-secondary);
}

.dist-val {
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
}

.dist-bar-bg {
  height: 5px;
  background: #f2f3f5;
  border-radius: 3px;
  overflow: hidden;
}

.dist-bar-fill {
  height: 100%;
  background: #c9d8f0;
  border-radius: 3px;
}

/* 第三层：底部全宽「最新推理截图」自适应网格 */
.snapshot-wall-panel {
  flex: 1;
  min-height: 0;
}

.snapshot-scroll-container {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding-right: 2px;
}

.snapshot-grid {
  display: grid;
  gap: clamp(12px, 0.9vw, 16px);
  align-content: start;
}

.snapshot-card {
  border: 1px solid var(--border-color);
  border-radius: 6px;
  overflow: hidden;
  background: #ffffff;
  display: flex;
  flex-direction: column;
  transition: border-color 0.15s ease;
}

.snapshot-card:hover {
  border-color: #c9cdd4;
}

/* 锁定 16:9 宽高比 */
.snapshot-thumb {
  width: 100%;
  aspect-ratio: 16 / 9;
  background: #181d26;
  position: relative;
  overflow: hidden;
}

.thumb-tag {
  position: absolute;
  top: 8px;
  left: 8px;
  font-size: 11px;
  color: #e2e8f0;
  background: rgba(15, 23, 42, 0.72);
  padding: 2px 7px;
  border-radius: 3px;
  z-index: 2;
  font-variant-numeric: tabular-nums;
}

.thumb-status {
  position: absolute;
  top: 8px;
  right: 8px;
  font-size: 11px;
  color: #94a3b8;
  background: rgba(15, 23, 42, 0.72);
  padding: 2px 7px;
  border-radius: 3px;
  z-index: 2;
}

.thumb-placeholder {
  position: absolute;
  inset: 14px;
  display: grid;
  place-items: center;
}

.viewfinder-corner {
  position: absolute;
  width: 10px;
  height: 10px;
  border-color: rgba(148, 163, 184, 0.32);
  border-style: solid;
}

.viewfinder-corner.tl {
  top: 0;
  left: 0;
  border-width: 1px 0 0 1px;
}

.viewfinder-corner.tr {
  top: 0;
  right: 0;
  border-width: 1px 1px 0 0;
}

.viewfinder-corner.bl {
  bottom: 0;
  left: 0;
  border-width: 0 0 1px 1px;
}

.viewfinder-corner.br {
  bottom: 0;
  right: 0;
  border-width: 0 1px 1px 0;
}

.thumb-label {
  font-size: 12px;
  color: #64748b;
}

.snapshot-info {
  padding: 9px 12px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  background: #ffffff;
}

.info-primary,
.info-secondary {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.point-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-main);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.capture-time {
  font-size: 12px;
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
  flex-shrink: 0;
}

.device-label {
  font-size: 12px;
  color: var(--text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.event-tag {
  font-size: 11px;
  color: var(--text-muted);
  background: var(--surface-subtle);
  padding: 1px 6px;
  border-radius: 3px;
  flex-shrink: 0;
}

/* 中小屏幕响应式回退 */
@media (max-width: 1280px) {
  .home-dashboard {
    height: auto;
    overflow: visible;
  }

  .analytics-row {
    grid-template-columns: 1fr;
    height: auto;
  }

  .chart-panel,
  .dist-panel {
    height: 230px;
  }

  .snapshot-wall-panel {
    height: 500px;
  }
}

@media (max-width: 960px) {
  .kpi-row {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 640px) {
  .kpi-row {
    grid-template-columns: 1fr;
  }
}
</style>
