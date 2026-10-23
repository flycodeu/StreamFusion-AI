<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
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
import {
  getSecurityAlerts,
  securityRules,
  securityCategories,
  securityDegraded,
} from '../../api/security/alerts'
import type { SecurityAlert, SecurityFeed } from '../../api/security/alerts'
import RequestError from '../../components/feedback/RequestError.vue'
import DiagnosticLink from '../diagnostics/DiagnosticLink.vue'
import { usePageScope } from '../../composables/usePageScope'
import { sessionState } from '../../session/state'
import { formatDateTime } from '../../utils/dateTime'

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
const captureScope = usePageScope()
let sequence = 0
let pending: InstanceType<typeof globalThis.AbortController> | null = null
async function load() {
  if (!allowed.value) return
  const inScope = captureScope()
  const current = ++sequence
  pending?.abort()
  pending = new globalThis.AbortController()
  loading.value = true
  error.value = null
  try {
    const result = await getSecurityAlerts({ page: page.value, size: 20 }, pending.signal)
    if (!inScope() || !allowed.value || sequence !== current) return
    rows.value = result.items
    total.value = result.total
    monitor.value = result.monitor
  } catch (cause) {
    if (inScope() && allowed.value && sequence === current) error.value = cause
  } finally {
    if (inScope() && sequence === current) loading.value = false
  }
}
function details(row: SecurityAlert) {
  selected.value = row
  detailOpen.value = true
}
function clear() {
  sequence++
  pending?.abort()
  rows.value = []
  monitor.value = null
  selected.value = null
  detailOpen.value = false
}
watch(() => [allowed.value, sessionState.me?.user.id, sessionState.epoch], clear)
watch(detailOpen, (open) => {
  if (!open) selected.value = null
})
onMounted(() => void load())
onBeforeUnmount(clear)
</script>

<template>
  <section v-if="allowed" class="security-alerts">
    <div class="security-toolbar">
      <h3>安全告警</h3>
      <ElButton :loading="loading" @click="load">刷新</ElButton>
    </div>
    <RequestError v-if="error" :error="error" advice="请检查会话和审计权限后重试。" />
    <ElAlert
      v-if="monitor && !monitor.enabled"
      type="warning"
      title="安全监测已关闭"
      :closable="false"
    />
    <ElAlert
      v-if="monitor && securityDegraded(monitor)"
      type="warning"
      title="安全监测异常"
      :closable="false"
    >
      事件丢弃 {{ monitor.droppedEvents }} · 来源淘汰 {{ monitor.evictedSources }} · 事件截断
      {{ monitor.truncatedWindows }} · 处理失败 {{ monitor.processingFailures }}
      <span v-if="!monitor.persistenceHealthy"> · 告警保存失败</span>
    </ElAlert>
    <ElTable v-loading="loading" :data="rows" border @row-dblclick="details">
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
        ><template #default="{ row }">{{ formatDateTime(row.lastSeen) }}</template></ElTableColumn
      >
      <ElTableColumn label="操作" width="90" fixed="right"
        ><template #default="{ row }"
          ><ElButton link type="primary" @click="details(row as SecurityAlert)"
            >详情</ElButton
          ></template
        ></ElTableColumn
      >
      <template #empty><ElEmpty description="暂无告警" :image-size="60" /></template>
    </ElTable>
    <ElPagination
      v-model:current-page="page"
      :page-size="20"
      :total="total"
      layout="prev, pager, next, total"
      @current-change="load"
    />
    <ElDrawer
      v-model="detailOpen"
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
          ><DiagnosticLink :trace-id="trace" />
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
.security-alerts {
  display: grid;
  gap: 16px;
}
.security-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
h3 {
  margin: 0;
}
.security-toolbar .el-button {
  flex-shrink: 0;
}
.security-trace {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  margin: 12px 0;
}
code,
pre {
  overflow-wrap: anywhere;
  white-space: pre-wrap;
  font-size: 12px;
}
pre {
  background: var(--el-fill-color-light);
  padding: 14px;
}
summary {
  cursor: pointer;
  color: var(--el-color-primary);
  margin-top: 20px;
}
</style>
