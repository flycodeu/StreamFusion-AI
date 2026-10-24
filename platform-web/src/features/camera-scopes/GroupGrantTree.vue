<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { ElButton, ElCheckbox, ElInput, ElTree, vLoading } from 'element-plus'
import type { CameraGroup } from '../../api/camera-groups/types'
import { getScopeCameras, getScopeGroupTree } from '../../api/camera-scopes/api'
import type { ScopeCamera } from '../../api/camera-scopes/types'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { groupTree, type GroupNode } from '../camera-groups/tree'

export interface ScopeTreeNode {
  key: string
  groupId: string
  cameraId?: string
  deviceId?: string | null
  groupIds?: string[]
  kind: 'group' | 'camera' | 'action'
  name: string
  path: string
  cameraCount: number | null
  lifecycle?: string
  children: ScopeTreeNode[]
}

const props = withDefaults(
  defineProps<{
    disabled: boolean
    allMode?: boolean
    initialCameras?: ScopeCamera[]
  }>(),
  {
    allMode: false,
    initialCameras: () => [],
  },
)
const selectedGroups = defineModel<string[]>({ required: true })
// Standalone cameras have no device record; identified cameras use stable device grants.
const selectedCameras = defineModel<string[]>('cameraIds', { default: () => [] })
const selectedDevices = defineModel<string[]>('deviceIds', { default: () => [] })

const groups = ref<CameraGroup[]>([]),
  cameraMap = ref<Map<string, ScopeCamera>>(new Map()),
  groupPages = ref<
    Record<string, { page: number; total: number; loading: boolean; error: unknown }>
  >({}),
  loading = ref(false),
  error = ref<unknown>(null),
  term = ref(''),
  expandedKeys = ref<string[]>([]),
  tree = ref<InstanceType<typeof ElTree>>()
const captureScope = usePageScope()
const selectionError = ref<unknown>(null)
const selectionIssue = ref('')
let selectionRevision = 0
watch(
  [selectedGroups, selectedCameras, selectedDevices, () => props.disabled, () => props.allMode],
  () => {
    ++selectionRevision
  },
  { flush: 'sync' },
)

const rawGroupTree = computed(() => groupTree(groups.value))

const groupById = computed(() => {
  const map = new Map<string, GroupNode>()
  const visit = (items: GroupNode[]) => {
    for (const item of items) {
      map.set(item.groupId, item)
      visit(item.children)
    }
  }
  visit(rawGroupTree.value)
  return map
})

const groupByPath = computed(() => {
  const map = new Map<string, GroupNode>()
  for (const node of groupById.value.values()) {
    map.set(node.path, node)
  }
  return map
})

function resolveCameraGroupId(camera: ScopeCamera): string | null {
  if (camera.groupId && groupById.value.has(camera.groupId)) return camera.groupId
  if (camera.path) {
    const matched = groupByPath.value.get(camera.path)
    if (matched) return matched.groupId
  }
  return camera.groupId ?? null
}

function cameraKey(camera: ScopeCamera) {
  return camera.deviceId ? `device:${camera.deviceId}` : `camera:${camera.cameraId}`
}

function mergeCameras(items: ScopeCamera[]) {
  const next = new Map(cameraMap.value)
  for (const item of items) {
    const key = cameraKey(item)
    const existing = next.get(key)
    next.set(key, {
      ...existing,
      ...item,
      groupId: item.groupId ?? existing?.groupId ?? null,
      path: item.path ?? existing?.path ?? null,
    })
  }
  cameraMap.value = next
}

watch(
  () => props.initialCameras,
  (items) => {
    if (items?.length) mergeCameras(items)
  },
  { immediate: true },
)

const camerasByGroup = computed(() => {
  const byGroup = new Map<string, ScopeCamera[]>()
  const ungrouped: ScopeCamera[] = []
  for (const cam of cameraMap.value.values()) {
    const groupIds = cam.deviceId && cam.groupIds ? cam.groupIds : [resolveCameraGroupId(cam)]
    const knownGroups = [...new Set(groupIds)].filter(
      (gid): gid is string => !!gid && groupById.value.has(gid),
    )
    for (const gid of knownGroups) {
      const list = byGroup.get(gid) ?? []
      list.push(cam)
      byGroup.set(gid, list)
    }
    if (!knownGroups.length) {
      ungrouped.push(cam)
    }
  }
  return { byGroup, ungrouped }
})

function pageActions(groupId: string, path: string): ScopeTreeNode[] {
  const page = groupPages.value[groupId]
  if (page && !page.loading && !page.error && page.page * 100 >= page.total) return []
  return [
    {
      key: `action:${groupId}`,
      groupId,
      kind: 'action',
      path,
      name: page?.error
        ? '读取失败，重试'
        : page?.loading
          ? '加载中…'
          : page
            ? '加载更多相机'
            : '加载相机',
      cameraCount: 0,
      children: [],
    },
  ]
}

const nodes = computed<ScopeTreeNode[]>(() => {
  const build = (items: GroupNode[]): ScopeTreeNode[] =>
    items.map((node) => {
      const childGroups = build(node.children)
      const directCameras = (camerasByGroup.value.byGroup.get(node.groupId) ?? []).map(
        (cam): ScopeTreeNode => ({
          key: cam.deviceId ? `device:${node.groupId}:${cam.deviceId}` : `camera:${cam.cameraId}`,
          groupId: node.groupId,
          cameraId: cam.cameraId,
          deviceId: cam.deviceId,
          groupIds: cam.groupIds,
          kind: 'camera',
          name: cam.name,
          path: `${node.path} / ${cam.name}`,
          cameraCount: 0,
          lifecycle: cam.lifecycle,
          children: [],
        }),
      )
      return {
        key: node.groupId,
        groupId: node.groupId,
        kind: 'group',
        name: node.name,
        path: node.path,
        cameraCount: node.visibleDeviceCount ?? null,
        children: [...childGroups, ...directCameras, ...pageActions(node.groupId, node.path)],
      }
    })
  const result = build(rawGroupTree.value)
  for (const cam of camerasByGroup.value.ungrouped) {
    result.push({
      key: cameraKey(cam),
      groupId: cam.groupId ?? '',
      cameraId: cam.cameraId,
      deviceId: cam.deviceId,
      groupIds: cam.groupIds,
      kind: 'camera',
      name: cam.name,
      path: cam.path ? `${cam.path} / ${cam.name}` : cam.name,
      cameraCount: 0,
      lifecycle: cam.lifecycle,
      children: [],
    })
  }
  if (groupPages.value['']) result.push(...pageActions('', ''))
  return result
})

const selectedGroupSet = computed(() => new Set(selectedGroups.value))
const selectedCameraSet = computed(() => new Set(selectedCameras.value))
const selectedDeviceSet = computed(() => new Set(selectedDevices.value))

function isGroupCovered(groupId: string | null | undefined): boolean {
  if (props.allMode) return true
  if (!groupId) return false
  let current = groupById.value.get(groupId)
  for (let depth = 0; current && depth < 16; depth++) {
    if (selectedGroupSet.value.has(current.groupId)) return true
    current = current.parentId ? groupById.value.get(current.parentId) : undefined
  }
  return false
}

function contains(parentId: string, childId: string | null | undefined) {
  let current = childId ? groupById.value.get(childId) : undefined
  for (let depth = 0; current && depth < 16; depth++) {
    if (current.groupId === parentId) return true
    current = current.parentId ? groupById.value.get(current.parentId) : undefined
  }
  return false
}

function isCameraChecked(node: ScopeTreeNode): boolean {
  if (props.allMode) return true
  if (node.deviceId && selectedDeviceSet.value.has(node.deviceId)) return true
  if (node.cameraId && selectedCameraSet.value.has(node.cameraId)) return true
  return isCameraCovered(node)
}

function isCameraCovered(node: ScopeTreeNode): boolean {
  return (node.groupIds?.length ? node.groupIds : [node.groupId]).some(isGroupCovered)
}

function coveringGroups(groupId: string) {
  return selectedGroups.value.filter((id) => id === groupId || contains(id, groupId))
}

function collectDescendantGroupIds(groupId: string): string[] {
  const result: string[] = []
  const visit = (node: GroupNode | undefined) => {
    if (!node) return
    result.push(node.groupId)
    for (const child of node.children) visit(child)
  }
  visit(groupById.value.get(groupId))
  return result
}

function collectGroupCameras(groupId: string): ScopeCamera[] {
  const gids = collectDescendantGroupIds(groupId)
  const list = new Map<string, ScopeCamera>()
  for (const gid of gids) {
    const cams = camerasByGroup.value.byGroup.get(gid)
    for (const cam of cams ?? []) list.set(cameraKey(cam), cam)
  }
  return [...list.values()]
}

function groupCheckState(node: ScopeTreeNode): { checked: boolean; indeterminate: boolean } {
  if (props.allMode || isGroupCovered(node.groupId)) {
    return { checked: true, indeterminate: false }
  }
  const gids = collectDescendantGroupIds(node.groupId)
  const cams = collectGroupCameras(node.groupId)
  const checkedCamCount = cams.filter((cam) =>
    cam.deviceId
      ? selectedDeviceSet.value.has(cam.deviceId)
      : selectedCameraSet.value.has(cam.cameraId),
  ).length
  const anySubgroupSelected = gids.some((gid) => selectedGroupSet.value.has(gid))
  return {
    checked: false,
    indeterminate: anySubgroupSelected || checkedCamCount > 0,
  }
}

let sequence = 0
async function load() {
  const current = ++sequence,
    active = captureScope()
  loading.value = true
  error.value = null
  try {
    const [groupResult, cameraResult] = await Promise.all([
      getScopeGroupTree(),
      getScopeCameras({ page: 1, size: 100 }),
    ])
    if (!active() || current !== sequence) return
    groups.value = groupResult
    mergeCameras([...props.initialCameras, ...cameraResult.items])
    groupPages.value = {
      ...groupPages.value,
      '': { page: 1, total: cameraResult.total, loading: false, error: null },
    }
  } catch (cause) {
    if (active() && current === sequence) error.value = cause
  } finally {
    if (active() && current === sequence) loading.value = false
  }
}

async function loadGroupPage(groupId: string, more = false) {
  const previous = groupPages.value[groupId]
  if (previous?.loading || (!more && previous && !previous.error)) return
  if (more && previous && !previous.error && previous.page * 100 >= previous.total) return
  const page = (previous?.page ?? 0) + 1
  groupPages.value = {
    ...groupPages.value,
    [groupId]: {
      page: previous?.page ?? 0,
      total: previous?.total ?? 0,
      loading: true,
      error: null,
    },
  }
  const active = captureScope()
  try {
    const result = await getScopeCameras({ page, size: 100, ...(groupId ? { groupId } : {}) })
    if (!active()) return
    mergeCameras(result.items)
    groupPages.value = {
      ...groupPages.value,
      [groupId]: { page, total: result.total, loading: false, error: null },
    }
  } catch (cause) {
    if (!active()) return
    groupPages.value = {
      ...groupPages.value,
      [groupId]: {
        page: previous?.page ?? 0,
        total: previous?.total ?? 0,
        loading: false,
        error: cause,
      },
    }
  }
}

function handleNodeExpand(data: ScopeTreeNode) {
  if (data.kind === 'group') {
    if (!expandedKeys.value.includes(data.key)) {
      expandedKeys.value = [...expandedKeys.value, data.key]
    }
    void loadGroupPage(data.groupId)
  }
}

function handleNodeCollapse(data: ScopeTreeNode) {
  expandedKeys.value = expandedKeys.value.filter((key) => key !== data.key)
}

async function loadAllGroupCameras(groupId: string, current: () => boolean) {
  const all: ScopeCamera[] = []
  let page = 1
  let total = 0
  while (page <= 20) {
    const result = await getScopeCameras({ page, size: 100, groupId, includeDescendants: true })
    if (!current()) return []
    if (result.total > 2000) {
      selectionIssue.value = '该分组超过 2000 台相机，请先取消上级分组，再按较小分组选择。'
      return null
    }
    total = result.total
    all.push(...result.items)
    if (all.length >= result.total || !result.items.length) break
    page += 1
  }
  if (all.length < total) {
    selectionIssue.value = '分组相机列表未完整读取，授权选择保持不变，请重新操作。'
    return null
  }
  return [...new Map(all.map((camera) => [cameraKey(camera), camera])).values()]
}

async function materializeInheritedSelection(
  groupIds: string[],
  excludedGroupId?: string,
  excludedCameraKey?: string,
) {
  const ancestors = [...new Set(groupIds.flatMap(coveringGroups))]
  if (!ancestors.length) return false
  const revision = selectionRevision,
    active = captureScope()
  const current = () =>
    active() && revision === selectionRevision && !props.disabled && !props.allMode
  const loaded = new Map<string, ScopeCamera>()
  try {
    for (const ancestor of ancestors) {
      const cameras = await loadAllGroupCameras(ancestor, current)
      if (!current() || !cameras) return false
      for (const camera of cameras) loaded.set(cameraKey(camera), camera)
    }
  } catch (cause) {
    if (current()) selectionError.value = cause
    return false
  }
  const excluded = (camera: ScopeCamera) => {
    if (excludedCameraKey && cameraKey(camera) === excludedCameraKey) return true
    if (!excludedGroupId) return false
    const memberships = camera.groupIds?.length ? camera.groupIds : [camera.groupId]
    return (
      memberships.length > 0 &&
      memberships.every((id) => id === excludedGroupId || contains(excludedGroupId, id))
    )
  }
  const remainingGroups = new Set(selectedGroups.value.filter((id) => !ancestors.includes(id)))
  if (excludedGroupId) {
    // Keep siblings at every level of the excluded branch; preserving its parent
    // would silently grant the excluded descendants again.
    const keepSiblings = (node: GroupNode | undefined) => {
      for (const child of node?.children ?? []) {
        if (contains(excludedGroupId, child.groupId)) continue
        if (contains(child.groupId, excludedGroupId)) keepSiblings(child)
        else remainingGroups.add(child.groupId)
      }
    }
    for (const ancestor of ancestors) {
      keepSiblings(groupById.value.get(ancestor))
    }
  }
  const explicitDevices = new Set(selectedDevices.value)
  const explicitCameras = new Set(selectedCameras.value)
  if (excludedCameraKey?.startsWith('device:')) explicitDevices.delete(excludedCameraKey.slice(7))
  if (excludedCameraKey?.startsWith('camera:')) explicitCameras.delete(excludedCameraKey.slice(7))
  for (const camera of loaded.values()) {
    if (excluded(camera)) continue
    const memberships = camera.groupIds?.length ? camera.groupIds : [camera.groupId]
    const covered = memberships.some(
      (id) => id && [...remainingGroups].some((grant) => contains(grant, id) || grant === id),
    )
    if (covered) continue
    if (camera.deviceId) explicitDevices.add(camera.deviceId)
    else explicitCameras.add(camera.cameraId)
  }
  if (remainingGroups.size > 200 || explicitDevices.size + explicitCameras.size > 2000) {
    selectionIssue.value = '调整后的范围超过 200 个分组或 2000 台直接授权相机，请先缩小选择范围。'
    return false
  }
  mergeCameras([...loaded.values()])
  selectedGroups.value = [...remainingGroups]
  selectedDevices.value = [...explicitDevices]
  selectedCameras.value = [...explicitCameras]
  return true
}

async function applyGroupToggle(groupId: string, shouldCheck: boolean) {
  if (props.disabled || props.allMode || loading.value) return
  ++selectionRevision
  selectionError.value = null
  selectionIssue.value = ''
  if (!shouldCheck && isGroupCovered(groupById.value.get(groupId)?.parentId)) {
    await materializeInheritedSelection([groupId], groupId)
    return
  }
  if (shouldCheck && isGroupCovered(groupId)) return
  const gids = new Set(selectedGroups.value)

  if (shouldCheck) {
    if (gids.size >= 200 && !gids.has(groupId)) {
      tree.value?.setCheckedKeys(selectedGroups.value)
      return
    }
    gids.add(groupId)
  } else {
    gids.delete(groupId)
  }

  selectedGroups.value = [...gids]
}

async function toggle(data: ScopeTreeNode, state?: { checkedKeys?: (string | number)[] }) {
  if (props.disabled || props.allMode || loading.value) return
  if (data.kind === 'action') return
  if (data.kind === 'camera') {
    await toggleCamera(data, !isCameraChecked(data))
    return
  }
  const currentChecked = groupCheckState(data).checked
  const checkedByState =
    state?.checkedKeys && !currentChecked
      ? state.checkedKeys.map(String).includes(data.groupId)
      : !currentChecked
  await applyGroupToggle(data.groupId, checkedByState)
  await nextTick()
  syncTreeCheckStates()
}

async function toggleCamera(node: ScopeTreeNode, checked: unknown) {
  if (props.disabled || props.allMode || loading.value || !node.cameraId) return
  ++selectionRevision
  selectionError.value = null
  selectionIssue.value = ''
  if (!checked && isCameraCovered(node)) {
    const memberships = node.groupIds?.length ? node.groupIds : [node.groupId]
    await materializeInheritedSelection(
      memberships,
      undefined,
      node.deviceId ? `device:${node.deviceId}` : `camera:${node.cameraId}`,
    )
    await nextTick()
    syncTreeCheckStates()
    return
  }
  const selection = node.deviceId ? selectedDevices : selectedCameras
  const id = node.deviceId ?? node.cameraId
  const ids = new Set(selection.value)

  if (checked) {
    if (selectedDevices.value.length + selectedCameras.value.length >= 2000 && !ids.has(id)) return
    ids.add(id)
  } else {
    ids.delete(id)
  }
  selection.value = [...ids]
}

function setAllExpanded(open: boolean) {
  const keys = [...groupById.value.keys()]
  expandedKeys.value = open ? keys : []
  for (const key of keys) {
    const node = tree.value?.getNode(key)
    if (open) node?.expand()
    else node?.collapse()
  }
}

function syncTreeCheckStates(syncSelectedGroups = true) {
  if (syncSelectedGroups) tree.value?.setCheckedKeys(selectedGroups.value)
  const visit = (items: ScopeTreeNode[]) => {
    for (const item of items) {
      if (item.kind === 'group') {
        const state = groupCheckState(item)
        const treeNode = tree.value?.getNode?.(item.key) as
          { checked?: boolean; indeterminate?: boolean } | undefined
        if (treeNode) {
          treeNode.checked = state.checked
          treeNode.indeterminate = state.indeterminate
        }
        visit(item.children)
      }
    }
  }
  visit(nodes.value)
}

watch([selectedGroups, nodes], async () => {
  await nextTick()
  syncTreeCheckStates()
  tree.value?.filter(term.value.trim())
})
watch([selectedCameras, selectedDevices], async () => {
  await nextTick()
  syncTreeCheckStates(false)
})
watch(term, (value) => {
  const keyword = value.trim()
  tree.value?.filter(keyword)
  if (!keyword) {
    for (const key of groupById.value.keys()) {
      const node = tree.value?.getNode(key)
      if (expandedKeys.value.includes(key)) node?.expand()
      else node?.collapse()
    }
  }
})
onMounted(load)
</script>

<template>
  <div class="group-grant-tree">
    <div class="group-grant-tree__toolbar">
      <ElInput
        v-model="term"
        class="group-grant-tree__search"
        clearable
        placeholder="搜索分组或相机名称"
        aria-label="搜索授权分组"
      />
      <div class="group-grant-tree__actions">
        <ElButton link type="primary" :disabled="!groups.length" @click="setAllExpanded(true)"
          >展开全部</ElButton
        >
        <ElButton link :disabled="!groups.length" @click="setAllExpanded(false)">收起全部</ElButton>
      </div>
    </div>
    <RequestError :error="error" />
    <RequestError :error="selectionError" advice="授权选择保持不变，请重试刚才的取消操作。" />
    <p v-if="selectionIssue" class="selection-issue" role="alert">{{ selectionIssue }}</p>
    <ElButton v-if="error" link :loading="loading" @click="load">重新读取分组</ElButton>
    <div v-loading="loading" class="group-grant-tree__body">
      <ElTree
        ref="tree"
        :data="nodes"
        node-key="key"
        show-checkbox
        check-strictly
        :indent="18"
        :default-expanded-keys="expandedKeys"
        :auto-expand-parent="false"
        :expand-on-click-node="true"
        :default-checked-keys="selectedGroups"
        :props="{
          label: 'name',
          children: 'children',
          disabled: (data) =>
            disabled ||
            allMode ||
            loading ||
            data.kind === 'action' ||
            (data.kind === 'group' &&
              selectedGroups.length >= 200 &&
              !selectedGroupSet.has(String(data.groupId))),
        }"
        :filter-node-method="
          (value: string, data) =>
            !value ||
            (data.kind === 'action' && !data.groupId) ||
            String(data.path).toLocaleLowerCase().includes(value.toLocaleLowerCase()) ||
            String(data.name).toLocaleLowerCase().includes(value.toLocaleLowerCase())
        "
        empty-text="暂无可授权相机分组"
        aria-label="分组授权树"
        @check="toggle"
        @node-expand="handleNodeExpand"
        @node-collapse="handleNodeCollapse"
      >
        <template #default="{ data }">
          <span v-if="data.kind === 'group'" class="group-grant-node" :title="data.path">
            <span class="group-grant-node__name">{{ data.name }}</span>
            <small class="group-grant-node__count">{{ data.cameraCount ?? '—' }} 台相机</small>
          </span>
          <span v-else-if="data.kind === 'action'" class="group-grant-action">
            <ElButton
              link
              type="primary"
              :loading="groupPages[data.groupId]?.loading"
              @click.stop="loadGroupPage(data.groupId, true)"
            >
              {{ data.name }}
            </ElButton>
          </span>
          <span
            v-else
            class="camera-grant-node"
            :title="data.path"
            @click.stop="toggleCamera(data, !isCameraChecked(data))"
          >
            <ElCheckbox
              :model-value="isCameraChecked(data)"
              :disabled="
                disabled ||
                allMode ||
                loading ||
                (!isCameraChecked(data) && selectedDevices.length + selectedCameras.length >= 2000)
              "
              :aria-label="`选择 ${data.name}`"
              @click.stop
              @update:model-value="toggleCamera(data, $event)"
            />
            <span class="camera-grant-node__name">
              {{ data.name }}{{ data.lifecycle === 'DISABLED' ? '（已停用，保留授权）' : '' }}
            </span>
          </span>
        </template>
      </ElTree>
    </div>
  </div>
</template>

<style scoped>
.selection-issue {
  color: var(--el-color-danger);
  font-size: 13px;
  margin: 8px 0;
}
.group-grant-tree__toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
.group-grant-tree__search {
  flex: 1 1 240px;
}
.group-grant-tree__actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}
.group-grant-tree__body {
  min-height: 320px;
  max-height: min(56dvh, 460px);
  margin-top: 12px;
  padding: 6px;
  border: 1px solid var(--border-subtle);
  border-radius: 6px;
  background: #fff;
  overflow: auto;
}
.group-grant-tree :deep(.el-tree) {
  background: transparent;
  --el-tree-node-content-height: 38px;
}
.group-grant-tree :deep(.el-tree-node__content) {
  padding-right: 10px;
  border-radius: 4px;
}
.group-grant-tree :deep(.el-tree-node__content:has(.camera-grant-node) > .el-checkbox),
.group-grant-tree :deep(.el-tree-node__content:has(.group-grant-action) > .el-checkbox) {
  display: none;
}
.group-grant-node,
.camera-grant-node {
  display: flex;
  align-items: center;
  min-width: 0;
  flex: 1;
  gap: 8px;
}
.group-grant-node__name {
  flex: 1;
  font-weight: 500;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-primary);
}
.camera-grant-node__name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-primary);
}
.group-grant-node__count {
  flex-shrink: 0;
  padding: 1px 8px;
  border-radius: 10px;
  background: var(--surface-muted);
  color: var(--text-secondary);
  font-size: 12px;
}
</style>
