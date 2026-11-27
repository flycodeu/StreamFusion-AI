<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  ElButton,
  ElFormItem,
  ElInput,
  ElTable,
  ElTableColumn,
  ElTag,
  vLoading,
} from 'element-plus'
import * as api from '../../api/storage/api'
import type { StorageInstance, StorageOptions } from '../../api/storage/types'
import RequestError from '../../components/feedback/RequestError.vue'
import TablePanel from '../../components/table/TablePanel.vue'
import TableActions from '../../components/table/TableActions.vue'
import StandardPagination from '../../components/table/StandardPagination.vue'
import FilterBar from '../../components/ui/FilterBar.vue'
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
function query() {
  search.value = keyword.value.trim()
  listPage.value = 1
  void loadInstances()
}
function resetSearch() {
  keyword.value = ''
  query()
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
  <div class="content-page storage-workspace">
    <template v-if="!selectedId">
      <FilterBar :loading="loading" @search="query" @reset="resetSearch">
        <ElFormItem label="存储">
          <ElInput
            v-model="keyword"
            class="search-field"
            clearable
            placeholder="名称或 S3 地址"
            aria-label="搜索图床"
          />
        </ElFormItem>
      </FilterBar>
      <RequestError v-if="error" :error="error" class="workspace-error" />
      <TablePanel :initial-loading="loading && !instances.length">
        <template #actions>
          <ElButton type="primary" @click="edit(null)">新建图床</ElButton>
          <ElButton @click="policyOpen = true">全局清理策略</ElButton>
        </template>
        <template #tools>
          <span class="sf-count-pill">共 {{ total }} 个实例</span>
          <ElButton :loading="loading" @click="loadInstances">刷新</ElButton>
        </template>
        <ElTable
          v-loading="loading"
          :data="instances"
          row-key="instanceId"
          border
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
              ><ElTag
                :type="row.accessStatus === 'ACCESSIBLE' ? 'success' : 'info'"
                effect="plain"
                >{{ label(row.accessStatus) }}</ElTag
              ></template
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
          <ElTableColumn
            label="操作"
            width="170"
            fixed="right"
            class-name="table-actions-column"
            :resizable="false"
            ><template #default="{ row }"
              ><TableActions compact
                ><ElButton link type="primary" @click="enter(row.instanceId)">进入文件库</ElButton
                ><ElButton link type="primary" @click="edit(asInstance(row))"
                  >编辑</ElButton
                ></TableActions
              ></template
            ></ElTableColumn
          >
        </ElTable>
        <template #footer>
          <StandardPagination
            v-model:page="listPage"
            v-model:size="listSize"
            :total="total"
            @change="loadInstances"
          />
        </template>
      </TablePanel>
    </template>
    <template v-else>
      <header class="workspace-heading">
        <ElButton text class="back-btn" @click="back">← 图床列表</ElButton>
        <span v-if="instance" class="instance-name">{{ instance.name }}</span>
        <nav class="workspace-navigation" aria-label="图床管理分区">
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
        <StorageLibrary
          v-if="instance && currentView === 'files'"
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
    </template>
    <StorageHistoryPolicy v-model="policyOpen" />
    <InstanceEditor v-model="editor" :instance="editing" :options="options" @saved="saved" />
  </div>
</template>

<style scoped>
.storage-workspace {
  min-width: 0;
}
.workspace-heading {
  display: flex;
  flex-wrap: wrap;
  gap: 14px;
  align-items: center;
  margin-bottom: 14px;
  padding: 12px var(--sf-card-padding-x);
  background: var(--sf-surface);
  border: 1px solid var(--sf-border);
  border-radius: var(--sf-radius-lg);
  box-shadow: var(--sf-shadow-xs);
}
.back-btn {
  font-weight: 500;
  color: var(--sf-text-regular);
}
.instance-name {
  color: var(--sf-text-primary);
  font-size: var(--sf-fs-section);
  font-weight: 600;
  overflow-wrap: anywhere;
}
.workspace-navigation {
  display: inline-flex;
  gap: 4px;
  margin-left: auto;
  padding: 3px;
  border: 1px solid var(--sf-border);
  border-radius: var(--sf-radius-md);
  background: var(--sf-surface-subtle);
  flex-wrap: wrap;
}
.workspace-navigation button {
  border: 0;
  background: transparent;
  padding: 6px 14px;
  border-radius: var(--sf-radius-sm);
  color: var(--sf-text-secondary);
  font: inherit;
  font-size: var(--sf-fs-sub);
  font-weight: 500;
  cursor: pointer;
  white-space: nowrap;
  transition:
    background-color 120ms ease,
    color 120ms ease,
    box-shadow 120ms ease;
}
.workspace-navigation button:hover:not(.active) {
  color: var(--sf-text-primary);
}
.workspace-navigation button.active {
  color: var(--sf-primary);
  background: var(--sf-surface);
  font-weight: 600;
  box-shadow: var(--sf-shadow-xs);
}
.workspace-content {
  min-height: 360px;
  min-width: 0;
  background: var(--sf-surface);
  border: 1px solid var(--sf-border);
  border-radius: var(--sf-radius-lg);
  box-shadow: var(--sf-shadow-xs);
  overflow: hidden;
}
.workspace-error {
  margin-bottom: 14px;
}
@media (max-width: 700px) {
  .workspace-heading {
    margin-bottom: 12px;
  }
  .workspace-navigation {
    margin-left: 0;
    width: 100%;
    gap: 2px;
  }
  .workspace-navigation button {
    flex: 1;
    padding: 7px 10px;
  }
}
</style>
