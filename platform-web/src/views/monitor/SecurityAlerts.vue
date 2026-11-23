<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import {
  ElAlert,
  ElButton,
  ElDescriptions,
  ElDescriptionsItem,
  ElDrawer,
  ElEmpty,
  ElPagination,
  ElTable,
  ElTableColumn,
  ElTag,
  vLoading,
} from 'element-plus'
import { getSecurityAlerts, securityDegraded } from '../../api/security/alerts'
import type { SecurityAlert, SecurityFeed } from '../../api/security/alerts'
import RequestError from '../../components/feedback/RequestError.vue'
import TablePanel from '../../components/table/TablePanel.vue'
import { usePageScope } from '../../composables/usePageScope'
import DiagnosticLink from '../../features/diagnostics/DiagnosticLink.vue'
import { securityCategories, securityRules } from '../../features/security/presentation'
import { sessionState } from '../../session/state'
import { formatDateTime } from '../../utils/dateTime'

defineOptions({ name: 'MonitorSecurityAlerts' })

const pageSize = 20
const allowed = computed(
  () => !!sessionState.me?.isSuperAdmin && sessionState.me.modules?.includes('audit'),
)
const rows = ref<SecurityAlert[]>([])
const monitor = ref<SecurityFeed | null>(null)
const page = ref(1)
const total = ref(0)
const loading = ref(false)
const error = ref<unknown>(null)
const selected = ref<SecurityAlert | null>(null)
const detailOpen = ref(false)
const degraded = computed(() => monitor.value !== null && securityDegraded(monitor.value))
const emptyText = computed(() =>
  monitor.value?.enabled && !degraded.value ? '最近 7 天暂无安全告警' : '暂无留存告警',
)
const captureScope = usePageScope()
let sequence = 0
let pending: InstanceType<typeof globalThis.AbortController> | null = null
async function load(targetPage = page.value) {
  if (!allowed.value) return
  const inScope = captureScope()
  const current = ++sequence
  pending?.abort()
  const controller = new globalThis.AbortController()
  pending = controller
  loading.value = true
  error.value = null
  const currentRequest = () => inScope() && allowed.value && sequence === current
  try {
    let result = await getSecurityAlerts({ page: targetPage, size: pageSize }, controller.signal)
    if (!currentRequest()) return
    const lastPage = Math.max(1, Math.ceil(result.total / pageSize))
    if (targetPage > lastPage) {
      result = await getSecurityAlerts({ page: lastPage, size: pageSize }, controller.signal)
      if (!currentRequest()) return
    }
    rows.value = result.items
    page.value = result.page
    total.value = result.total
    monitor.value = result.monitor
  } catch (cause) {
    if (currentRequest()) {
      clearEvidence()
      error.value = cause
    }
  } finally {
    if (currentRequest()) {
      pending = null
      loading.value = false
    }
  }
}
function details(row: SecurityAlert) {
  selected.value = row
  detailOpen.value = true
}
function clearEvidence() {
  rows.value = []
  page.value = 1
  total.value = 0
  monitor.value = null
  selected.value = null
  detailOpen.value = false
}
function clear() {
  sequence++
  pending?.abort()
  pending = null
  loading.value = false
  error.value = null
  clearEvidence()
}
watch(
  [allowed, () => sessionState.me?.user.id, () => sessionState.epoch],
  ([permitted]) => {
    clear()
    if (permitted) void load()
  },
  { immediate: true },
)
watch(detailOpen, (open) => {
  if (!open) selected.value = null
})
onBeforeUnmount(clear)
</script>

<template>
  <section class="content-page" aria-label="安全告警">
    <TablePanel v-if="allowed" :initial-loading="loading && !monitor">
      <template #actions>
        <div class="security-heading">
          <h1>安全告警</h1>
          <span>本机最近 7 天，最多 1000 条</span>
        </div>
      </template>
      <template #tools>
        <ElTag v-if="monitor?.enabled && !degraded && !loading" type="success" effect="plain"
          >监测正常</ElTag
        >
        <ElButton :loading="loading" @click="load()">刷新</ElButton>
      </template>
      <RequestError v-if="error" :error="error" advice="安全告警读取失败，请刷新重试。" />
      <div v-else-if="monitor" class="security-results">
        <ElAlert v-if="!monitor.enabled" type="warning" title="安全监测已关闭" :closable="false" />
        <ElAlert v-if="degraded" type="warning" title="安全监测异常" :closable="false">
          事件丢弃 {{ monitor.droppedEvents }} · 来源淘汰 {{ monitor.evictedSources }} · 事件截断
          {{ monitor.truncatedWindows }} · 处理失败 {{ monitor.processingFailures }}
          <span v-if="!monitor.persistenceHealthy"> · 告警保存失败</span>
        </ElAlert>
        <ElTable
          v-loading="loading"
          :data="rows"
          :empty-text="emptyText"
          border
          @row-dblclick="details"
        >
          <ElTableColumn label="异常类型" min-width="170"
            ><template #default="{ row }"
              ><ElTag :type="row.rule === 'MIXED_PROBING' ? 'danger' : 'warning'">{{
                securityRules[(row as SecurityAlert).rule]
              }}</ElTag></template
            ></ElTableColumn
          >
          <ElTableColumn prop="sourceIp" label="来源 IP" min-width="150" />
          <ElTableColumn prop="eventCount" label="窗口事件数" width="110" />
          <ElTableColumn label="最近出现" min-width="180"
            ><template #default="{ row }">{{
              formatDateTime(row.lastSeen)
            }}</template></ElTableColumn
          >
          <ElTableColumn label="操作" width="90" fixed="right"
            ><template #default="{ row }"
              ><ElButton link type="primary" @click="details(row as SecurityAlert)"
                >详情</ElButton
              ></template
            ></ElTableColumn
          >
        </ElTable>
      </div>
      <template v-if="total > 0" #footer>
        <ElPagination
          class="security-pagination"
          :current-page="page"
          :page-size="pageSize"
          :total="total"
          :disabled="loading"
          :pager-count="5"
          layout="total, prev, pager, next"
          @update:current-page="load"
        />
      </template>
    </TablePanel>
    <ElEmpty v-else description="当前账号无权查看安全告警" />
    <ElDrawer
      v-if="allowed"
      v-model="detailOpen"
      class="camera-detail-drawer"
      title="安全告警详情"
      size="min(680px, 100vw)"
      append-to-body
      destroy-on-close
    >
      <template v-if="selected">
        <ElDescriptions :column="1" border>
          <ElDescriptionsItem label="判断"
            >{{ securityRules[selected.rule] }} · 待核查</ElDescriptionsItem
          >
          <ElDescriptionsItem label="来源 IP">{{ selected.sourceIp }}</ElDescriptionsItem>
          <ElDescriptionsItem label="时间范围"
            >{{ formatDateTime(selected.firstSeen) }} —
            {{ formatDateTime(selected.lastSeen) }}</ElDescriptionsItem
          >
          <ElDescriptionsItem label="窗口摘要"
            >{{ selected.eventCount }} 次异常，{{
              selected.distinctPaths
            }}
            个不同路径</ElDescriptionsItem
          >
          <ElDescriptionsItem label="异常构成"
            ><div v-for="(count, category) in selected.categories" :key="category">
              {{ securityCategories[category] }}：{{ count }}
            </div></ElDescriptionsItem
          >
          <ElDescriptionsItem label="已认证用户 ID">{{
            selected.actorIds.join('、') || '—'
          }}</ElDescriptionsItem>
        </ElDescriptions>
        <div v-for="trace in selected.traceIds" :key="trace" class="security-trace">
          <code>{{ trace }}</code
          ><DiagnosticLink :trace-id="trace" :time="selected.lastSeen" />
        </div>
        <details>
          <summary>查看告警 JSON</summary>
          <pre>{{ JSON.stringify(selected, null, 2) }}</pre>
        </details>
      </template>
    </ElDrawer>
  </section>
</template>

<style scoped>
.security-results {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 12px;
  min-width: 0;
}
.security-heading {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 6px 12px;
}
.security-heading h1 {
  margin: 0;
  color: var(--el-text-color-primary);
  font-size: 18px;
}
.security-heading span {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.security-pagination {
  justify-content: flex-end;
  flex-wrap: wrap;
  row-gap: 10px;
}
.security-trace {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin: 10px 0;
  padding: 8px 12px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
}
code,
pre {
  overflow-wrap: anywhere;
  white-space: pre-wrap;
  font-size: 12px;
}
pre {
  max-height: 360px;
  overflow: auto;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
  padding: 14px;
}
summary {
  cursor: pointer;
  color: var(--el-color-primary);
  margin-top: 20px;
}
</style>
