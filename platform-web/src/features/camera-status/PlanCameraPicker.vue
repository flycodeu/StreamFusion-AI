<script setup lang="ts">
import { computed, nextTick, onScopeDispose, ref, watch } from 'vue'
import { ElButton, ElCheckbox, ElInput, ElTree, vLoading } from 'element-plus'
import { Folder, Search, VideoCamera } from '@element-plus/icons-vue'
import { getCameraDevices, getDeviceChannels } from '../../api/camera/api'
import type { CameraDeviceGroup } from '../../api/camera/types'
import { inspectPlanTargets } from '../../api/camera-status/planTargets'
import type { PlanTarget } from '../../api/camera-status/planTargets'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { addressText } from '../camera/address'
import type { GroupNode } from '../camera-groups/tree'
import { deviceStatus } from './deviceStatus'

const props = withDefaults(
  defineProps<{
    disabled: boolean
    mode?: 'PAGE' | 'ALL' | 'PLAN' | 'GROUPS'
    targets?: PlanTarget[]
    totalCount?: number | null
    inspecting?: boolean
    rangeReady?: boolean
    groups?: GroupNode[]
    checkedGroups?: string[]
    groupError?: unknown
  }>(),
  { mode: 'PAGE', rangeReady: true, targets: () => [], groups: () => [], checkedGroups: () => [] },
)
const emit = defineEmits<{
  manual: [keys: string[]]
  groups: [keys: string[]]
  all: []
  resolving: [value: boolean]
  reloadGroups: []
}>()
const selected = defineModel<string[]>({ required: true })
interface DevicePage {
  items: CameraDeviceGroup[]
  page: number
  total: number
  loading: boolean
  error: unknown
}
interface PickerNode {
  key: string
  kind: 'all' | 'group' | 'camera' | 'action'
  label: string
  groupId?: string
  camera?: CameraDeviceGroup
  children: PickerNode[]
}
const tree = ref<InstanceType<typeof ElTree>>()
const name = ref('')
const pages = ref(new Map<string, DevicePage>())
const searchPage = ref<DevicePage>(emptyPage())
const searchGroups = ref(new Map<string, string | null>())
const groupRanges = ref(new Map<string, string[]>())
const resolving = ref(false)
const loadError = ref<unknown>(null)
const selectionNotice = ref('')
const expanded = new Set(['all'])
const capture = usePageScope()
const searching = computed(() => !!name.value.trim())
const activeKeys = computed(() => {
  if (props.mode === 'PAGE') return selected.value
  // Once all group selections are removed, an old preview must not keep
  // camera rows checked while the parent is settling the empty range.
  if (props.mode === 'GROUPS' && !props.checkedGroups.length) return []
  return props.targets.map((target) => target.groupKey)
})
const activeSet = computed(() => new Set(activeKeys.value))
// Resolving a range is a background refresh. Keep the tree interactive so a quick
// select/cancel sequence can supersede the previous request without a disabled flash.
const locked = computed(() => props.disabled)
const groupIndex = computed(() => {
  const index = new Map<string, GroupNode>()
  const visit = (groups: GroupNode[]) => {
    for (const group of groups) {
      index.set(group.groupId, group)
      visit(group.children)
    }
  }
  visit(props.groups)
  return index
})
// Count each device once per ancestor, even when it belongs to sibling groups.
const selectedGroupCounts = computed(() => {
  const counts = new Map<string, number>()
  for (const target of props.targets) {
    if (!activeSet.value.has(target.groupKey)) continue
    const ancestors = new Set<string>()
    for (const id of target.groupIds ?? []) {
      let group = groupIndex.value.get(id)
      for (let depth = 0; group && depth < 16; depth++) {
        ancestors.add(group.groupId)
        group = group.parentId ? groupIndex.value.get(group.parentId) : undefined
      }
    }
    for (const id of ancestors) counts.set(id, (counts.get(id) ?? 0) + 1)
  }
  return counts
})
const allChecked = computed(
  () =>
    props.mode === 'ALL' ||
    (props.rangeReady &&
      props.totalCount != null &&
      props.totalCount > 0 &&
      activeSet.value.size === props.totalCount &&
      props.targets.length === props.totalCount &&
      props.targets.every(
        (target) =>
          activeSet.value.has(target.groupKey) && target.reasonCode !== 'TARGET_UNAVAILABLE',
      )),
)
const groupStates = computed(
  () => new Map([...groupIndex.value.keys()].map((id) => [id, groupState(id)])),
)
let searchRevision = 0
let selectionRevision = 0
let searchTimer: ReturnType<typeof globalThis.setTimeout> | undefined
let synchronizing = false

function emptyPage(): DevicePage {
  return { items: [], page: 0, total: 0, loading: false, error: null }
}
function contains(parentId: string, groupId: string) {
  let group = groupIndex.value.get(groupId)
  for (let depth = 0; group && depth < 16; depth++) {
    if (group.groupId === parentId) return true
    group = group.parentId ? groupIndex.value.get(group.parentId) : undefined
  }
  return false
}
function covered(groupId: string) {
  return (
    props.mode === 'ALL' ||
    (props.mode === 'GROUPS' && props.checkedGroups.some((id) => contains(id, groupId)))
  )
}
function groupState(groupId: string) {
  if (covered(groupId)) return { checked: true, partial: false }
  if (props.mode === 'GROUPS') {
    return {
      checked: false,
      partial: props.checkedGroups.some((id) => contains(groupId, id)),
    }
  }
  const range = groupRanges.value.get(groupId)
  if (range?.length) {
    const count = range.filter((key) => activeSet.value.has(key)).length
    if (count === range.length) return { checked: true, partial: false }
    if (count) return { checked: false, partial: true }
  }
  const selectedCount = selectedGroupCounts.value.get(groupId) ?? 0
  const total = groupIndex.value.get(groupId)?.visibleDeviceCount
  if (selectedCount && total != null && selectedCount === total)
    return { checked: true, partial: false }
  const hasSelectedChild =
    selectedCount > 0 ||
    [...pages.value].some(
      ([id, page]) =>
        contains(groupId, id) && page.items.some((camera) => activeSet.value.has(camera.groupKey)),
    ) ||
    searchPage.value.items.some((camera) => {
      const id = searchGroups.value.get(camera.groupKey)
      return !!id && contains(groupId, id) && activeSet.value.has(camera.groupKey)
    })
  return { checked: false, partial: hasSelectedChild }
}
function cameraNode(camera: CameraDeviceGroup, parent: string): PickerNode {
  return {
    key: `${parent}:${camera.groupKey}`,
    kind: 'camera',
    label: camera.name,
    camera,
    children: [],
  }
}
function actionNode(groupId: string, label: string, actionable = true): PickerNode {
  return {
    key: `action:${groupId}`,
    kind: 'action',
    label,
    groupId: actionable ? groupId : undefined,
    children: [],
  }
}
const nodes = computed<PickerNode[]>(() => {
  const build = (groups: GroupNode[]): PickerNode[] =>
    groups.flatMap((group): PickerNode[] => {
      const page = pages.value.get(group.groupId)
      const cameras = searching.value
        ? searchPage.value.items.filter(
            (camera) => searchGroups.value.get(camera.groupKey) === group.groupId,
          )
        : (page?.items ?? [])
      const children = [
        ...build(group.children),
        ...cameras.map((camera) => cameraNode(camera, group.groupId)),
      ]
      if (searching.value && !children.length) return []
      if (!searching.value) {
        if (!page?.page || page.error || page.loading || page.page * 50 < page.total) {
          const label = page?.loading
            ? '正在加载…'
            : page?.error
              ? '加载失败，点击重试'
              : page?.page
                ? `加载更多（${page.items.length} / ${page.total}）`
                : '展开加载相机'
          children.push(actionNode(group.groupId, label))
        } else if (!children.length) {
          children.push(actionNode(group.groupId, '暂无可选相机', false))
        }
      }
      return [
        {
          key: `group:${group.groupId}`,
          kind: 'group',
          label: group.name,
          groupId: group.groupId,
          children,
        },
      ]
    })
  const children = build(props.groups)
  // 搜索以首个可见启用通道定位设备；跨组设备只显示一次，不调用仅超管可读的移组接口。
  if (searching.value) {
    children.push(
      ...searchPage.value.items
        .filter((camera) => !groupIndex.value.has(searchGroups.value.get(camera.groupKey) ?? ''))
        .map((camera) => cameraNode(camera, 'search')),
    )
    const page = searchPage.value
    if (page.error || page.loading || page.page * 20 < page.total) {
      const label = page.loading
        ? '正在搜索…'
        : page.error
          ? '搜索失败，点击重试'
          : `加载更多（${page.items.length} / ${page.total}）`
      children.push(actionNode('search', label))
    } else if (!children.length) {
      children.push(actionNode('search', '没有匹配的相机', false))
    }
  } else if (!children.length && !props.groupError) {
    children.push(actionNode('empty', '暂无可选分组', false))
  }
  return [{ key: 'all', kind: 'all', label: '全部相机', children }]
})

async function loadGroup(groupId: string) {
  if (locked.value || searching.value) return
  let page = pages.value.get(groupId)
  if (!page) {
    pages.value.set(groupId, emptyPage())
    page = pages.value.get(groupId)!
  }
  if (page.loading || (page.page && page.page * 50 >= page.total)) return
  const active = capture()
  page.loading = true
  page.error = null
  loadError.value = null
  try {
    const result = await getCameraDevices({
      page: page.page + 1,
      size: 50,
      groupId,
      includeDescendants: false,
      lifecycle: 'ENABLED',
    })
    if (!active() || pages.value.get(groupId) !== page) return
    page.items = [
      ...new Map(
        [...page.items, ...result.items].map((camera) => [camera.groupKey, camera]),
      ).values(),
    ]
    page.page++
    page.total = result.total
  } catch (cause) {
    if (active() && pages.value.get(groupId) === page) {
      page.error = cause
      loadError.value = cause
    }
  } finally {
    if (active() && pages.value.get(groupId) === page) page.loading = false
  }
}
async function searchMore() {
  const page = searchPage.value
  if (
    locked.value ||
    !searching.value ||
    page.loading ||
    (page.page && page.page * 20 >= page.total)
  )
    return
  const revision = searchRevision,
    keyword = name.value.trim(),
    active = capture()
  const current = () => active() && revision === searchRevision && name.value.trim() === keyword
  page.loading = true
  page.error = null
  try {
    const result = await getCameraDevices({
      page: page.page + 1,
      size: 20,
      name: keyword,
      lifecycle: 'ENABLED',
    })
    if (!current()) return
    const placements = new Map<string, string | null>()
    // 每页20台、最多4个并发；只读一条已授权通道，不扫描整台设备的所有通道。
    for (let offset = 0; offset < result.items.length; offset += 4) {
      const entries = await Promise.all(
        result.items.slice(offset, offset + 4).map(async (camera) => {
          const channels = await getDeviceChannels(camera.groupKey, {
            page: 1,
            size: 1,
            lifecycle: 'ENABLED',
          })
          return [camera.groupKey, channels.items[0]?.groupId ?? null] as const
        }),
      )
      if (!current()) return
      for (const [key, id] of entries) placements.set(key, id)
    }
    for (const [key, id] of placements) searchGroups.value.set(key, id)
    page.items = [
      ...new Map(
        [...page.items, ...result.items].map((camera) => [camera.groupKey, camera]),
      ).values(),
    ]
    page.page++
    page.total = result.total
  } catch (cause) {
    if (current()) page.error = cause
  } finally {
    if (current()) page.loading = false
  }
}
watch(name, () => {
  globalThis.clearTimeout(searchTimer)
  ++searchRevision
  searchPage.value = emptyPage()
  searchGroups.value.clear()
  loadError.value = null
  const active = capture()
  if (searching.value)
    searchTimer = globalThis.setTimeout(() => {
      if (active()) void searchMore()
    }, 200)
})
watch(locked, (value) => {
  if (!value && searching.value && !searchPage.value.page && !searchPage.value.error)
    void searchMore()
})
watch(
  [() => props.targets, () => props.checkedGroups, () => props.mode, () => props.inspecting],
  () => {
    if (
      !props.inspecting &&
      props.mode === 'GROUPS' &&
      props.checkedGroups.length === 1 &&
      props.targets.length
    ) {
      groupRanges.value.set(
        props.checkedGroups[0]!,
        props.targets.map((target) => target.groupKey),
      )
    }
  },
)
interface ExpandableNode {
  expanded: boolean
  expand(): void
  collapse(): void
}
function expand(data: PickerNode) {
  if (synchronizing || searching.value) return
  expanded.add(data.key)
  if (data.kind === 'group' && !pages.value.get(data.groupId!)?.page) void loadGroup(data.groupId!)
}
function collapse(data: PickerNode) {
  if (!synchronizing && !searching.value) expanded.delete(data.key)
}
function clickNode(data: PickerNode, node?: ExpandableNode) {
  if (data.kind === 'action') {
    if (data.groupId === 'search') void searchMore()
    else if (data.groupId) void loadGroup(data.groupId)
  } else if (data.kind !== 'camera' && node) {
    if (node.expanded) {
      node.collapse()
      collapse(data)
    } else {
      node.expand()
      expand(data)
    }
  }
}
function toggleTreeNode(data: PickerNode) {
  if (data.kind === 'camera') {
    toggleCamera(data.camera!.groupKey, !activeSet.value.has(data.camera!.groupKey))
    return
  }
  if (data.kind === 'action') {
    clickNode(data)
    return
  }
  const node = tree.value?.getNode(data.key) as ExpandableNode | undefined
  if (node) clickNode(data, node)
}
watch(
  nodes,
  async () => {
    await nextTick()
    synchronizing = true
    const sync = (items: PickerNode[]) => {
      for (const item of items) {
        if (!item.children.length) continue
        const node = tree.value?.getNode(item.key)
        if (searching.value || expanded.has(item.key)) node?.expand()
        else node?.collapse()
        sync(item.children)
      }
    }
    sync(nodes.value)
    synchronizing = false
  },
  { immediate: true },
)

function toggleCamera(key: string, value: unknown) {
  if (locked.value || !props.rangeReady) return
  invalidateSelection()
  const keys = new Set(activeKeys.value)
  if (value) {
    if (keys.size >= 2000 && !keys.has(key)) return
    keys.add(key)
  } else keys.delete(key)
  // 逐台调整保留完整已核对范围，沿用PAGE语义，避免提交残留groupIds。
  selectionNotice.value = ''
  if (props.mode === 'PAGE') selected.value = [...keys]
  else emit('manual', [...keys])
}
async function toggleGroup(groupId: string, value: unknown) {
  if (locked.value) return
  invalidateSelection()
  selectionNotice.value = ''
  if (value && (props.mode === 'GROUPS' || (props.mode === 'PAGE' && !activeKeys.value.length))) {
    const ids = props.checkedGroups.filter((id) => !contains(groupId, id))
    if (ids.length >= 100) {
      selectionNotice.value = '单次最多选择 100 个分组'
      return
    }
    emit('groups', [...ids, groupId])
    return
  }
  if (!value && props.mode === 'GROUPS' && props.checkedGroups.includes(groupId)) {
    emit(
      'groups',
      props.checkedGroups.filter((id) => !contains(groupId, id)),
    )
    return
  }
  if (!props.rangeReady) {
    selectionNotice.value = '当前范围未核对成功，请重新核对或清空后选择'
    return
  }
  const revision = ++selectionRevision,
    active = capture()
  resolving.value = true
  emit('resolving', true)
  loadError.value = null
  try {
    // 混合增选或从整组排除子组时，先解析完整分组，再合并为固定设备范围。
    const result = await inspectPlanTargets({ scope: 'GROUPS', groupIds: [groupId] })
    if (!active() || revision !== selectionRevision) return
    if (result.limitExceeded) {
      selectionNotice.value = '该分组超过 2000 台，请缩小选择范围'
      return
    }
    const range = result.targets.map((target) => target.groupKey)
    const keys = new Set(activeKeys.value)
    for (const key of range) {
      if (value) keys.add(key)
      else keys.delete(key)
    }
    if (keys.size > 2000) {
      selectionNotice.value = '单个任务最多选择 2000 台相机'
      return
    }
    groupRanges.value.set(groupId, range)
    emit('manual', [...keys])
  } catch (cause) {
    if (active() && revision === selectionRevision) loadError.value = cause
  } finally {
    if (active() && revision === selectionRevision) {
      resolving.value = false
      emit('resolving', false)
    }
  }
}
function invalidateSelection() {
  ++selectionRevision
  if (resolving.value) {
    resolving.value = false
    emit('resolving', false)
  }
}
function toggleAll(value: unknown) {
  if (locked.value) return
  invalidateSelection()
  groupRanges.value.clear()
  selectionNotice.value = ''
  if (value) emit('all')
  else emit('manual', [])
}
function clear() {
  if (locked.value) return
  invalidateSelection()
  groupRanges.value.clear()
  selectionNotice.value = ''
  if (props.mode === 'PAGE') selected.value = []
  else emit('manual', [])
}
onScopeDispose(() => {
  globalThis.clearTimeout(searchTimer)
  ++searchRevision
  ++selectionRevision
})
</script>

<template>
  <section class="camera-picker" aria-label="选择巡检相机">
    <div class="tree-shell">
      <header class="picker-toolbar">
        <ElInput
          v-model="name"
          class="camera-search"
          :prefix-icon="Search"
          clearable
          :disabled="locked"
          maxlength="128"
          placeholder="搜索相机名称 / IP"
          aria-label="搜索巡检相机"
        />
        <div class="selection-summary" aria-live="polite">
          <span class="selection-pill">
            <template v-if="!rangeReady">待核对设备数</template>
            <template v-else
              >已选 <strong>{{ activeKeys.length }}</strong> 台</template
            >
          </span>
          <ElButton
            link
            :disabled="locked || (!activeKeys.length && !checkedGroups.length && mode !== 'ALL')"
            @click="clear"
            >清空</ElButton
          >
        </div>
      </header>
      <div class="tree-grid">
        <div class="tree-header" aria-hidden="true">
          <span class="tree-heading">分组 / 相机名称</span>
          <div class="tree-meta">
            <span class="address-heading">IP / 主机地址</span>
            <span class="status-heading">当前状态</span>
          </div>
        </div>
        <div
          v-loading="searchPage.loading && !searchPage.items.length"
          class="tree-scroll"
          :aria-busy="resolving || searchPage.loading"
        >
          <div
            v-if="groupError || searchPage.error || loadError || selectionNotice"
            class="tree-feedback"
          >
            <RequestError :error="groupError || searchPage.error || loadError" />
            <ElButton v-if="groupError" link :disabled="locked" @click="emit('reloadGroups')"
              >重新加载分组</ElButton
            >
            <p v-if="selectionNotice" class="selection-notice" role="alert">
              {{ selectionNotice }}
            </p>
          </div>
          <ElTree
            ref="tree"
            class="picker-tree"
            :data="nodes"
            node-key="key"
            :indent="22"
            :props="{ label: 'label', children: 'children' }"
            :default-expanded-keys="['all']"
            :expand-on-click-node="false"
            empty-text="暂无可选相机"
            aria-label="分组相机选择树"
            @node-click="clickNode"
            @node-expand="expand"
            @node-collapse="collapse"
          >
            <template #default="{ data }">
              <span v-if="data.kind === 'action'" class="tree-action">
                <ElButton
                  link
                  :disabled="
                    locked ||
                    !data.groupId ||
                    (data.groupId === 'search'
                      ? searchPage.loading
                      : pages.get(data.groupId)?.loading)
                  "
                  @click.stop="clickNode(data)"
                  >{{ data.label }}</ElButton
                >
              </span>
              <div
                v-else
                class="tree-row"
                :class="{ 'is-selected': data.camera && activeSet.has(data.camera.groupKey) }"
                @click.stop="toggleTreeNode(data)"
              >
                <div class="tree-main">
                  <ElCheckbox
                    v-if="data.kind === 'all'"
                    :model-value="allChecked"
                    :indeterminate="
                      !allChecked && (activeKeys.length > 0 || checkedGroups.length > 0)
                    "
                    :disabled="locked"
                    aria-label="选择全部已授权相机"
                    @click.stop
                    @update:model-value="toggleAll"
                  />
                  <ElCheckbox
                    v-else-if="data.kind === 'group'"
                    :model-value="groupStates.get(data.groupId)?.checked"
                    :indeterminate="groupStates.get(data.groupId)?.partial"
                    :disabled="locked"
                    :aria-label="`选择 ${data.label} 分组及子分组全部相机`"
                    @click.stop
                    @update:model-value="toggleGroup(data.groupId, $event)"
                  />
                  <ElCheckbox
                    v-else
                    :model-value="activeSet.has(data.camera.groupKey)"
                    :disabled="
                      locked ||
                      !rangeReady ||
                      (!activeSet.has(data.camera.groupKey) && activeKeys.length >= 2000)
                    "
                    :aria-label="`选择 ${data.label}`"
                    @click.stop
                    @update:model-value="toggleCamera(data.camera.groupKey, $event)"
                  />
                  <component
                    :is="data.kind === 'camera' ? VideoCamera : Folder"
                    class="tree-icon"
                    aria-hidden="true"
                  />
                  <span
                    class="tree-label"
                    :class="{ 'group-label': data.kind !== 'camera' }"
                    :title="data.label"
                    >{{ data.label }}</span
                  >
                </div>
                <div v-if="data.camera" class="tree-meta">
                  <span class="camera-address" :title="addressText(data.camera.accessAddress)">{{
                    addressText(data.camera.accessAddress)
                  }}</span>
                  <span
                    class="camera-status"
                    :class="{ 'is-online': deviceStatus(data.camera).tone === 'success' }"
                    :title="deviceStatus(data.camera).label"
                  >
                    <span class="status-dot" aria-hidden="true" />
                    <span class="camera-status-label">{{ deviceStatus(data.camera).label }}</span>
                  </span>
                </div>
              </div>
            </template>
          </ElTree>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.camera-picker {
  display: flex;
  flex-direction: column;
  box-sizing: border-box;
  flex: 1;
  min-height: 0;
  min-width: 0;
  overflow: hidden;
  background: #fff;
}
.picker-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 10px 16px;
  border-bottom: 1px solid #e2e8f0;
  background: #f8fafc;
  flex-shrink: 0;
}
.camera-search {
  flex: 0 1 280px;
  width: 280px;
  min-width: 0;
}
.selection-summary {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-left: auto;
  flex-shrink: 0;
  color: #64748b;
  font-size: 13px;
  white-space: nowrap;
}
.selection-pill {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 10px;
  border: 1px solid #e2e8f0;
  border-radius: 999px;
  background: #fff;
  font-size: 12px;
  line-height: 20px;
}
.selection-summary strong {
  color: #1e293b;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.selection-summary :deep(.el-button) {
  color: #64748b;
  font-size: 12px;
}
.tree-shell {
  display: flex;
  flex-direction: column;
  min-height: 0;
  flex: 1;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  overflow: hidden;
}
.tree-grid {
  display: flex;
  flex-direction: column;
  min-height: 0;
  flex: 1;
  overflow-x: auto;
  overflow-y: hidden;
  overscroll-behavior: contain;
  scrollbar-width: thin;
  scrollbar-color: #cbd5e1 transparent;
}
.tree-scroll {
  box-sizing: border-box;
  flex: 1;
  height: 100%;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
  overscroll-behavior: contain;
}
/* 表头预留与列表一致的滚动条宽度，保持右侧列对齐。 */
.tree-header,
.tree-scroll {
  min-width: 680px;
  scrollbar-gutter: stable;
  scrollbar-width: thin;
  scrollbar-color: #cbd5e1 transparent;
}
.tree-grid::-webkit-scrollbar,
.tree-header::-webkit-scrollbar,
.tree-scroll::-webkit-scrollbar {
  width: 5px;
  height: 5px;
}
.tree-grid::-webkit-scrollbar-thumb,
.tree-scroll::-webkit-scrollbar-thumb {
  border-radius: 8px;
  background: #cbd5e1;
}
.tree-header {
  display: flex;
  align-items: center;
  flex-shrink: 0;
  box-sizing: border-box;
  padding: 6px 16px;
  overflow: hidden;
  border-bottom: 1px solid #f1f5f9;
  background: #fcfcfd;
  color: #94a3b8;
  font-size: 12px;
  line-height: 20px;
}
.tree-heading {
  flex: 1;
  min-width: 0;
}
.tree-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex: 0 0 320px;
  width: 320px;
  margin-left: auto;
}
.address-heading,
.camera-address {
  flex: 0 0 220px;
  width: 220px;
  text-align: left;
}
.status-heading,
.camera-status {
  flex: 0 0 100px;
  width: 100px;
  text-align: right;
}
.tree-feedback {
  padding: 0 16px 8px;
}
.picker-tree {
  --el-tree-node-content-height: 40px;
  --el-tree-node-hover-bg-color: #f8fafc;
  --el-tree-text-color: #334155;
  background: transparent;
}
.picker-tree :deep(.el-tree-node__content) {
  box-sizing: border-box;
  padding-right: 0;
  border-bottom: 1px solid #f1f5f9;
  transition: background 150ms ease;
}
.picker-tree :deep(.el-tree-node__expand-icon) {
  color: #94a3b8;
}
.picker-tree :deep(.el-tree-node__expand-icon.is-leaf) {
  color: transparent;
}
.tree-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex: 1;
  box-sizing: border-box;
  min-width: 0;
  height: 100%;
  padding: 8px 16px;
  gap: 16px;
  font-size: 13px;
}
.tree-main {
  display: flex;
  align-items: center;
  flex: 1;
  min-width: 0;
  height: 100%;
  gap: 10px;
}
.tree-row.is-selected {
  background: #f8fafc;
}
.tree-row :deep(.el-checkbox) {
  height: 100%;
  margin: 0;
  flex-shrink: 0;
}
.tree-icon {
  width: 15px;
  height: 15px;
  color: #94a3b8;
  flex-shrink: 0;
}
.tree-label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: #1e293b;
}
.group-label {
  font-weight: 600;
}
.camera-address {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font:
    12px ui-monospace,
    Consolas,
    monospace;
  color: #64748b;
}
.camera-status {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
  color: #94a3b8;
  font-size: 12px;
}
.status-dot {
  flex: 0 0 6px;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}
.camera-status-label {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.camera-status.is-online {
  color: #10b981;
}
.tree-action :deep(.el-button) {
  color: #94a3b8;
  font-size: 12px;
  font-weight: 400;
}
.selection-notice {
  margin: 0 0 8px;
  color: #b45309;
  font-size: 12px;
}
@media (max-width: 600px) {
  .picker-toolbar {
    flex-wrap: wrap;
    gap: 12px;
  }
  .camera-search {
    flex-grow: 1;
  }
  .selection-summary {
    gap: 8px;
  }
}
@media (prefers-reduced-motion: reduce) {
  .picker-tree :deep(.el-tree-node__content) {
    transition: none;
  }
}
</style>
