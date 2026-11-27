<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { ElButton, ElCard } from 'element-plus'
import { getHealth } from '../../api/health/api'
import { getPreviewCapacity, getPreviewCatalog } from '../../api/preview/api'
import type { PreviewCapacity } from '../../api/preview/types'
import { getStatusPage } from '../../api/camera-status/api'
import RequestError from '../../components/feedback/RequestError.vue'
import PageHeader from '../../components/ui/PageHeader.vue'
import { usePageScope } from '../../composables/usePageScope'
import { findMenuPagePath } from '../../router/menuPath'
import { formatDateTime } from '../../utils/dateTime'
import { sessionState } from '../../session/state'

defineOptions({ name: 'HomeView' })

const page = (component: string) =>
  computed(() => findMenuPagePath(sessionState.me?.routes ?? [], component))
const cameras = page('/camera/Manage')
const preview = page('/preview/Center')
const cameraStatus = page('/camera/Status')
const captureScope = usePageScope()
const loading = ref(false)
const checkedAt = ref('')
const platformUp = ref<boolean | null>(null)
const healthError = ref<unknown>(null)
const channels = ref<number | null>(null)
const capacity = ref<PreviewCapacity | null>(null)
const failures = ref<number | null>(null)
let revision = 0

async function refresh() {
  const active = captureScope()
  const current = ++revision
  loading.value = true
  healthError.value = null
  platformUp.value = null
  checkedAt.value = ''
  channels.value = null
  capacity.value = null
  failures.value = null

  const update = <T,>(
    task: Promise<T>,
    success: (value: T) => void,
    fail?: (error: unknown) => void,
  ) =>
    task.then(
      (value) => {
        if (active() && current === revision) success(value)
      },
      (error: unknown) => {
        if (active() && current === revision) fail?.(error)
      },
    )
  const tasks: Promise<void>[] = [
    update(
      getHealth(),
      () => {
        platformUp.value = true
        checkedAt.value = formatDateTime(new Date())
      },
      (error) => {
        platformUp.value = false
        healthError.value = error
        checkedAt.value = formatDateTime(new Date())
      },
    ),
  ]
  if (cameras.value || preview.value)
    tasks.push(
      update(getPreviewCatalog({ page: 1, size: 1 }), (result) => {
        channels.value = result.total
      }),
    )
  if (preview.value)
    tasks.push(
      update(getPreviewCapacity(), (result) => {
        capacity.value = result
      }),
    )
  if (cameraStatus.value)
    tasks.push(
      update(
        getStatusPage({ layer: 'CONTROL', page: 1, size: 1, observation: 'FAILED' }),
        (result) => {
          failures.value = result.total
        },
      ),
    )
  await Promise.all(tasks)
  if (active() && current === revision) loading.value = false
}

onMounted(refresh)
watch([cameras, preview, cameraStatus, () => sessionState.epoch], () => void refresh())
</script>

<template>
  <main class="content-page home-dashboard">
    <PageHeader title="视频工作台" :card="false" heading-tag="h1">
      <template #actions>
        <ElButton :loading="loading" @click="refresh">刷新</ElButton>
      </template>
    </PageHeader>
    <RequestError :error="healthError" advice="请检查平台服务连接，或将请求标识提供给运维。" />
    <section class="home-metrics" aria-label="视频运行概况">
      <ElCard v-if="cameras || preview" shadow="never" class="home-metric">
        <h2>可见视频通道</h2>
        <p class="home-value">{{ channels ?? (loading ? '…' : '未读取') }}</p>
        <RouterLink v-if="cameras" :to="cameras">管理相机 →</RouterLink>
      </ElCard>
      <ElCard v-if="preview" shadow="never" class="home-metric">
        <h2>预览会话占用</h2>
        <p class="home-value">
          {{
            capacity
              ? `${capacity.occupiedSessions} / ${capacity.maxSessions}`
              : loading
                ? '…'
                : '未读取'
          }}
        </p>
        <RouterLink :to="preview">打开预览中心 →</RouterLink>
      </ElCard>
      <ElCard v-if="cameraStatus" shadow="never" class="home-metric">
        <h2>连接检查异常设备</h2>
        <p class="home-value" :class="{ 'is-danger': (failures ?? 0) > 0 }">
          {{ failures ?? (loading ? '…' : '未读取') }}
        </p>
        <RouterLink :to="{ path: cameraStatus, query: { observation: 'FAILED' } }">
          查看异常设备 →
        </RouterLink>
      </ElCard>
      <ElCard shadow="never" class="home-metric">
        <h2>平台服务连接</h2>
        <p
          class="home-value"
          :class="{ 'is-success': platformUp === true, 'is-danger': platformUp === false }"
        >
          {{ platformUp === null ? '检查中' : platformUp ? '正常' : '异常' }}
        </p>
        <p v-if="checkedAt" class="check-time">{{ checkedAt }}</p>
      </ElCard>
    </section>
  </main>
</template>

<style scoped>
.home-dashboard {
  display: grid;
  gap: var(--sf-space-md);
  align-content: start;
}
.home-metrics {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr));
  gap: var(--sf-space-md);
}
.home-metric {
  border: 1px solid var(--sf-border);
  border-radius: var(--sf-radius-lg);
  box-shadow: var(--sf-shadow-xs);
  transition:
    border-color 0.15s ease,
    box-shadow 0.15s ease;
}
.home-metric:hover {
  border-color: var(--sf-border-strong);
  box-shadow: var(--sf-shadow-sm);
}
.home-metric :deep(.el-card__body) {
  padding: 18px 20px;
}
.home-metric h2 {
  margin: 0;
  color: var(--sf-text-secondary);
  font-size: var(--sf-fs-sub);
  font-weight: 500;
}
.home-value {
  margin: 10px 0 12px;
  color: var(--sf-text-primary);
  font-size: var(--sf-fs-kpi);
  font-weight: 700;
  line-height: 1.25;
  font-variant-numeric: tabular-nums;
}
.home-value.is-success {
  color: var(--sf-success);
}
.home-value.is-danger {
  color: var(--sf-danger);
}
.home-metric a {
  color: var(--sf-primary);
  font-size: var(--sf-fs-sub);
  font-weight: 500;
}
.home-metric a:hover {
  color: var(--sf-primary-active);
  text-decoration: underline;
}
.check-time {
  color: var(--sf-text-secondary);
  margin: 0;
  font-size: var(--sf-fs-caption);
  font-variant-numeric: tabular-nums;
}
</style>
