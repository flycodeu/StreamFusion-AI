<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  ElButton,
  ElInput,
  ElPagination,
  ElTable,
  ElTableColumn,
  ElTag,
  vLoading,
} from 'element-plus'
import * as api from '../../api/storage/api'
import type { StorageInstance, StorageOptions } from '../../api/storage/types'
import RequestError from '../../components/feedback/RequestError.vue'
import InstanceEditor from '../../features/storage/InstanceEditor.vue'
import StorageLibrary from '../../features/storage/StorageLibrary.vue'
import StorageResources from '../../features/storage/StorageResources.vue'
import StorageChecks from '../../features/storage/StorageChecks.vue'
import StorageSettings from '../../features/storage/StorageSettings.vue'
import StorageHistoryPolicy from '../../features/storage/StorageHistoryPolicy.vue'
import { bytes, label, time } from '../../features/storage/display'

defineOptions({ name: 'StorageManage' })
const route = useRoute(),
  router = useRouter()
const views = [
  { key: 'files', label: '文件库' },
  { key: 'resources', label: '资源概览' },
  { key: 'checks', label: '连接检测' },
  { key: 'settings', label: '设置' },
] as const
type View = (typeof views)[number]['key']
const currentView = computed<View>(() =>
  views.some((v) => v.key === route.query.view) ? (route.query.view as View) : 'files',
)
const instances = ref<StorageInstance[]>([]),
  instance = ref<StorageInstance | null>(null),
  selectedId = ref('')
const listPage = ref(1),
  listSize = ref(20),
  total = ref(0),
  keyword = ref(''),
  search = ref('')
const policyOpen = ref(false)
const loading = ref(false),
  error = ref<unknown>(null),
  editor = ref(false),
  editing = ref<StorageInstance | null>(null)
const options = ref<StorageOptions>({
  networkPolicies: [],
  credentialProtectionReady: false,
  deleteReady: false,
  deleteReason: '',
})
let sequence = 0,
  listing = 0,
  editingSequence = 0,
  refreshing = 0,
  disposed = false
onBeforeUnmount(() => {
  disposed = true
  sequence++
  listing++
})
function navigate(view: View) {
  void router.replace({ query: { ...route.query, view: view === 'files' ? undefined : view } })
}
async function select(id: string) {
  selectedId.value = id
  const current = ++sequence
  instance.value = null
  error.value = null
  loading.value = !!id
  if (!id) return
  loading.value = true
  try {
    const value = await api.getInstance(id)
    if (!disposed && current === sequence) instance.value = value
  } catch (e) {
    if (!disposed && current === sequence) error.value = e
  } finally {
    if (!disposed && current === sequence) loading.value = false
  }
}
async function loadInstances() {
  const current = ++listing
  loading.value = true
  error.value = null
  try {
    const result = await api.listInstances(listPage.value, search.value, listSize.value)
    if (disposed || current !== listing) return
    if (listPage.value > 1 && !result.items.length) {
      listPage.value--
      await loadInstances()
      return
    }
    instances.value = result.items
    total.value = result.total
  } catch (e) {
    if (!disposed && current === listing) error.value = e
  } finally {
    if (!disposed && current === listing) loading.value = false
  }
}
function enter(id: string) {
  void router.push({ query: { instance: id } })
}
function back() {
  void router.push({ query: {} })
}
function sizeChanged() {
  listPage.value = 1
  void loadInstances()
}
function query() {
  search.value = keyword.value.trim()
  listPage.value = 1
  void loadInstances()
}
async function saved() {
  if (selectedId.value) await refresh()
  else await loadInstances()
}
async function refresh() {
  const revision = ++refreshing
  const current = sequence,
    id = selectedId.value
  if (!id) return
  try {
    const value = await api.getInstance(id)
    if (!disposed && current === sequence && revision === refreshing && id === selectedId.value) {
      instance.value = value
      error.value = null
    }
  } catch (e) {
    if (!disposed && current === sequence) error.value = e
  }
}
const asInstance = (row: unknown) => row as StorageInstance
async function edit(row: StorageInstance | null) {
  const current = ++editingSequence
  error.value = null
  try {
    const value = row ? await api.getInstance(row.instanceId) : null
    if (!disposed && current === editingSequence) {
      editing.value = value
      editor.value = true
    }
  } catch (e) {
    error.value = e
  }
}
watch(currentView, () => {
  if (instance.value) void refresh()
})
watch(
  () => route.query.instance,
  (value) => {
    const id = typeof value === 'string' && /^[1-9]\d*$/.test(value) ? value : ''
    void select(id)
    if (!id) void loadInstances()
  },
  { immediate: true },
)
onMounted(async () => {
  try {
    options.value = await api.getOptions()
  } catch (e) {
    if (!disposed) error.value = e
  }
})
</script>

<template>
  <div class="storage-workspace">
    <header class="workspace-heading" :class="{ 'list-heading': !selectedId }">
      <template v-if="selectedId"
        ><ElButton text @click="back">← 图床列表</ElButton
        ><span v-if="instance" class="instance-name">{{ instance.name }}</span></template
      >
      <div v-else class="list-actions">
        <ElButton @click="policyOpen = true">全局清理策略</ElButton
        ><ElButton type="primary" @click="edit(null)">新增图床</ElButton>
      </div>
      <nav v-if="selectedId" class="workspace-navigation" aria-label="图床管理分区">
        <button
          v-for="view in views"
          :key="view.key"
          type="button"
          :aria-current="currentView === view.key ? 'page' : undefined"
          :class="{ active: currentView === view.key }"
          @click="navigate(view.key)"
        >
          {{ view.label }}
        </button>
      </nav>
    </header>
    <RequestError v-if="error" :error="error" class="workspace-error" />
    <main v-loading="loading" class="workspace-content">
      <section v-if="!selectedId" class="server-list" aria-label="图床服务器列表">
        <form class="server-search" @submit.prevent="query">
          <ElInput
            v-model="keyword"
            clearable
            placeholder="名称或 S3 地址"
            aria-label="搜索图床"
          /><ElButton native-type="submit" type="primary">查询</ElButton
          ><ElButton @click="loadInstances">刷新</ElButton>
        </form>
        <ElTable
          :data="instances"
          row-key="instanceId"
          height="clamp(320px, calc(100vh - 340px), 640px)"
          empty-text="尚未配置图床服务器"
        >
          <ElTableColumn label="图床名称" min-width="170"
            ><template #default="{ row }"
              ><ElButton link type="primary" @click="enter(row.instanceId)">{{
                row.name
              }}</ElButton></template
            ></ElTableColumn
          >
          <ElTableColumn prop="endpoint" label="S3 地址" min-width="230" show-overflow-tooltip />
          <ElTableColumn label="连接状态" width="130"
            ><template #default="{ row }"
              ><ElTag :type="row.accessStatus === 'ACCESSIBLE' ? 'success' : 'info'">{{
                label(row.accessStatus)
              }}</ElTag></template
            ></ElTableColumn
          >
          <ElTableColumn label="文件 / 逻辑大小" min-width="180"
            ><template #default="{ row }"
              >{{ row.usage.visibleObjectCount ?? '—' }} /
              {{ bytes(row.usage.logicalBytes) }}</template
            ></ElTableColumn
          >
          <ElTableColumn label="检查时间" min-width="180"
            ><template #default="{ row }">{{ time(row.checkedAt) }}</template></ElTableColumn
          >
          <ElTableColumn label="操作" width="170"
            ><template #default="{ row }"
              ><ElButton link type="primary" @click="enter(row.instanceId)">进入文件库</ElButton
              ><ElButton link @click="edit(asInstance(row))">编辑</ElButton></template
            ></ElTableColumn
          >
        </ElTable>
        <ElPagination
          v-model:current-page="listPage"
          v-model:page-size="listSize"
          :page-sizes="[10, 20, 50]"
          :total="total"
          :pager-count="5"
          layout="total, sizes, prev, pager, next"
          @size-change="sizeChanged"
          @current-change="loadInstances"
        />
      </section>
      <StorageLibrary
        v-else-if="instance && currentView === 'files'"
        :key="instance.instanceId"
        :instance="instance"
        @updated="refresh"
        @settings="navigate('settings')"
      />
      <StorageResources
        v-else-if="instance && currentView === 'resources'"
        :key="instance.instanceId"
        :instance="instance"
        @updated="refresh"
      />
      <StorageChecks
        v-else-if="instance && currentView === 'checks'"
        :key="instance.instanceId"
        :instance="instance"
        @updated="refresh"
      />
      <StorageSettings
        v-else-if="currentView === 'settings'"
        :instances="instance ? [instance] : []"
        :instance="instance"
        @edit="edit"
        @select="enter"
        @updated="saved"
      />
    </main>
    <StorageHistoryPolicy v-model="policyOpen" />
    <InstanceEditor v-model="editor" :instance="editing" :options="options" @saved="saved" />
  </div>
</template>

<style scoped>
.storage-workspace {
  padding: 20px 24px;
  min-width: 0;
}
.workspace-heading {
  display: flex;
  flex-wrap: wrap;
  gap: 18px;
  align-items: center;
  margin-bottom: 16px;
  padding: 14px 20px;
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
}
.instance-name {
  color: #0f172a;
  font-size: 18px;
  font-weight: 600;
  overflow-wrap: anywhere;
}
.list-heading {
  justify-content: flex-end;
}
.list-actions {
  display: flex;
  gap: 10px;
}
.list-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}
.server-list {
  padding: 20px 24px;
  min-width: 0;
}
.server-list :deep(.el-table__body .el-table__cell) {
  padding-top: 18px;
  padding-bottom: 18px;
}
.server-list :deep(.el-table__header .el-table__cell) {
  padding-top: 14px;
  padding-bottom: 14px;
}
.server-search {
  display: flex;
  gap: 12px;
  margin-bottom: 22px;
}
.server-search .el-input {
  width: 340px;
  max-width: 55%;
}
.server-list .el-pagination {
  margin-top: 22px;
  justify-content: flex-end;
  flex-wrap: wrap;
  row-gap: 10px;
  overflow-x: auto;
  padding-bottom: 6px;
}
.workspace-navigation {
  display: flex;
  gap: 6px;
  margin-left: auto;
  flex-wrap: wrap;
}
.workspace-navigation button {
  border: 0;
  border-bottom: 2px solid transparent;
  background: none;
  padding: 9px 18px;
  border-radius: 7px;
  color: var(--el-text-color-regular);
  font: inherit;
  font-size: 14px;
  cursor: pointer;
  white-space: nowrap;
}
.workspace-navigation button.active {
  color: var(--el-color-primary);
  background: #eff6ff;
}
.workspace-content {
  min-height: 360px;
  min-width: 0;
  background: var(--el-bg-color);
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 12px;
}
.workspace-error {
  margin-bottom: 16px;
}
@media (max-width: 700px) {
  .storage-workspace {
    padding: 12px;
  }
  .workspace-heading {
    margin-bottom: 18px;
  }
  .workspace-navigation {
    margin-left: 0;
    width: 100%;
    gap: 2px;
  }
  .workspace-navigation button {
    flex: 1;
    padding: 9px 10px;
  }
}
</style>
