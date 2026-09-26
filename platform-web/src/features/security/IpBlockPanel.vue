<script setup lang="ts">
import { onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import {
  ElButton,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessage,
  ElMessageBox,
  ElOption,
  ElPagination,
  ElSelect,
  ElTable,
  ElTableColumn,
  ElTag,
  vLoading,
} from 'element-plus'
import { getIpBlocks, unblockIp } from '../../api/security/api'
import type { IpBlock, IpBlockStatus } from '../../api/security/types'
import RequestError from '../../components/feedback/RequestError.vue'
import ColumnPicker from '../../components/table/ColumnPicker.vue'
import TableActions from '../../components/table/TableActions.vue'
import TablePanel from '../../components/table/TablePanel.vue'
import { useColumns } from '../../composables/table/useColumns'
import { sessionState } from '../../session/state'

const query = reactive({ page: 1, size: 20, sourceIp: '', status: 'BLOCKED' as IpBlockStatus | '' })
const rows = ref<IpBlock[]>([])
const total = ref(0)
const loading = ref(false)
const unblocking = ref<string | null>(null)
const error = ref<unknown>(null)
const columns = useColumns('ip-blocks', [
  'ip',
  'status',
  'reason',
  'attempts',
  'blockedAt',
  'unblockedAt',
  'unblockedBy',
])
const columnOptions = [
  { key: 'ip', label: '来源IP' },
  { key: 'status', label: '状态' },
  { key: 'reason', label: '封禁原因' },
  { key: 'attempts', label: '登录失败次数' },
  { key: 'blockedAt', label: '封禁时间' },
  { key: 'unblockedAt', label: '解封时间' },
  { key: 'unblockedBy', label: '解封人ID' },
]
const reasonNames: Record<string, string> = { LOGIN_FAILURE_THRESHOLD: '登录失败次数超限' }
const asBlock = (value: unknown): IpBlock => value as IpBlock
let sequence = 0
let active = true
let pendingRead: InstanceType<typeof globalThis.AbortController> | undefined

function formatTime(value: string | null): string {
  return value
    ? new Intl.DateTimeFormat('zh-CN', {
        timeZone: 'Asia/Shanghai',
        dateStyle: 'short',
        timeStyle: 'medium',
        hour12: false,
      }).format(new Date(value))
    : '—'
}

async function load(): Promise<void> {
  if (!active || !sessionState.me?.isSuperAdmin) return
  const current = ++sequence
  pendingRead?.abort()
  pendingRead = new globalThis.AbortController()
  loading.value = true
  error.value = null
  try {
    const result = await getIpBlocks(
      {
        page: query.page,
        size: query.size,
        sourceIp: query.sourceIp.trim() || undefined,
        status: query.status || undefined,
      },
      pendingRead.signal,
    )
    if (!active || current !== sequence) return
    rows.value = result.items
    total.value = result.total
  } catch (cause) {
    if (active && current === sequence) error.value = cause
  } finally {
    if (active && current === sequence) loading.value = false
  }
}

function search(): void {
  query.page = 1
  void load()
}

function reset(): void {
  query.sourceIp = ''
  query.status = 'BLOCKED'
  search()
}

async function release(row: IpBlock): Promise<void> {
  if (!active || unblocking.value || row.status !== 'BLOCKED' || !sessionState.me?.isSuperAdmin)
    return
  const epoch = sessionState.epoch
  const userId = sessionState.me.user.id
  unblocking.value = row.id
  try {
    await ElMessageBox.confirm(`确认解除 IP ${row.sourceIp} 的封禁？`, '解除IP封禁', {
      confirmButtonText: '解除封禁',
      cancelButtonText: '取消',
      type: 'warning',
    })
    if (
      !active ||
      sessionState.epoch !== epoch ||
      sessionState.me?.user.id !== userId ||
      !sessionState.me.isSuperAdmin
    )
      return
    error.value = null
    await unblockIp(row)
    if (!active) return
    ElMessage.success('已解除封禁')
    if (rows.value.length === 1 && query.page > 1 && query.status === 'BLOCKED') query.page--
    await load()
  } catch (cause) {
    if (active && cause !== 'cancel' && cause !== 'close') error.value = cause
  } finally {
    if (active) unblocking.value = null
  }
}

onMounted(load)
onBeforeUnmount(() => {
  active = false
  sequence++
  pendingRead?.abort()
})
</script>

<template>
  <div class="ip-block-panel">
    <ElForm class="search-form" inline @submit.prevent="search">
      <ElFormItem label="来源IP">
        <ElInput
          v-model="query.sourceIp"
          class="search-field"
          placeholder="请输入完整IP地址"
          :maxlength="64"
          clearable
        />
      </ElFormItem>
      <ElFormItem label="状态">
        <ElSelect v-model="query.status" class="status-field" placeholder="全部状态" clearable>
          <ElOption label="封禁中" value="BLOCKED" /><ElOption label="已解除" value="RELEASED" />
        </ElSelect>
      </ElFormItem>
      <ElFormItem>
        <ElButton type="primary" native-type="submit" :loading="loading">查询</ElButton>
        <ElButton @click="reset">重置</ElButton>
      </ElFormItem>
    </ElForm>
    <RequestError :error="error" />
    <TablePanel>
      <template #tools>
        <ElButton :loading="loading" @click="load">刷新</ElButton>
        <ColumnPicker v-model="columns" :options="columnOptions" />
      </template>
      <ElTable v-loading="loading" :data="rows" row-key="id" border empty-text="暂无IP封禁记录">
        <ElTableColumn
          v-if="columns.includes('ip')"
          prop="sourceIp"
          label="来源IP"
          min-width="210"
          show-overflow-tooltip
        />
        <ElTableColumn v-if="columns.includes('status')" label="状态" width="100">
          <template #default="{ row }"
            ><ElTag :type="row.status === 'BLOCKED' ? 'danger' : 'success'" effect="plain">{{
              row.status === 'BLOCKED' ? '封禁中' : '已解除'
            }}</ElTag></template
          >
        </ElTableColumn>
        <ElTableColumn
          v-if="columns.includes('reason')"
          label="封禁原因"
          min-width="175"
          show-overflow-tooltip
        >
          <template #default="{ row }">{{
            reasonNames[row.reasonCode] || row.reasonCode
          }}</template>
        </ElTableColumn>
        <ElTableColumn v-if="columns.includes('attempts')" label="登录失败次数" min-width="180">
          <template #default="{ row }"
            >{{ row.failedAttempts }} 次 / {{ row.windowSeconds }} 秒</template
          >
        </ElTableColumn>
        <ElTableColumn v-if="columns.includes('blockedAt')" label="封禁时间" min-width="175">
          <template #default="{ row }">{{ formatTime(row.blockedAt) }}</template>
        </ElTableColumn>
        <ElTableColumn v-if="columns.includes('unblockedAt')" label="解封时间" min-width="175">
          <template #default="{ row }">{{ formatTime(row.unblockedAt) }}</template>
        </ElTableColumn>
        <ElTableColumn
          v-if="columns.includes('unblockedBy')"
          label="解封人ID"
          min-width="180"
          show-overflow-tooltip
        >
          <template #default="{ row }">{{ row.unblockedBy || '—' }}</template>
        </ElTableColumn>
        <ElTableColumn
          label="操作"
          min-width="140"
          fixed="right"
          class-name="table-actions-column"
          :resizable="false"
        >
          <template #default="{ row }">
            <TableActions>
              <ElButton
                plain
                type="primary"
                :disabled="row.status !== 'BLOCKED' || loading || !!unblocking"
                :loading="unblocking === row.id"
                @click="release(asBlock(row))"
                >解除封禁</ElButton
              >
            </TableActions>
          </template>
        </ElTableColumn>
      </ElTable>
      <template #footer>
        <ElPagination
          v-model:current-page="query.page"
          v-model:page-size="query.size"
          :total="total"
          :page-sizes="[20, 50, 100]"
          layout="total, sizes, prev, pager, next"
          @change="load"
        />
      </template>
    </TablePanel>
  </div>
</template>

<style scoped>
.ip-block-panel {
  min-width: 0;
}
</style>
