<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  ElButton,
  ElCheckbox,
  ElDropdown,
  ElDropdownMenu,
  ElDropdownItem,
  ElTooltip,
  type DropdownInstance,
  ElDialog,
  ElEmpty,
  ElIcon,
  ElMessageBox,
  ElInput,
  ElMessage,
  ElSelect,
  ElOption,
  ElTable,
  ElTableColumn,
  vLoading,
} from 'element-plus'
import {
  Folder,
  Upload,
  FolderAdd,
  Grid,
  List,
  Refresh,
  MoreFilled,
  Delete,
  Download,
  Document,
  Link,
  CopyDocument,
} from '@element-plus/icons-vue'
import * as api from '../../api/storage/api'
import type {
  StorageInstance,
  StorageObject,
  StorageTask,
  ObjectPage,
} from '../../api/storage/types'
import StorageAccessState from './StorageAccessState.vue'
import RequestError from '../../components/feedback/RequestError.vue'
import StorageUpload from './StorageUpload.vue'
import StorageFolderDelete from './StorageFolderDelete.vue'
import StorageFileDelete from './StorageFileDelete.vue'
import StorageFileDetails from './StorageFileDetails.vue'
import { previewLocation } from './preview-link'
import StorageFolderTree from './StorageFolderTree.vue'
import StorageThumbnail from './StorageThumbnail.vue'
import { createThumbnailQueue, folderName, libraryFiles } from './file-browser'
import { bytes, time } from './display'

const route = useRoute(),
  router = useRouter()
const fileDeletion = ref<StorageObject[] | null>(null),
  selectedKeys = ref<string[]>([])
const folderDeletion = ref<{ scopeId: string; folder: string } | null>(null),
  copiedLink = ref(''),
  libraryLayout = ref<InstanceType<typeof globalThis.HTMLElement>>(),
  directoryWidth = ref(280)
try {
  const saved = Number(globalThis.localStorage.getItem('sf.storage.directoryWidth'))
  if (saved >= 180 && saved <= 560) directoryWidth.value = saved
} catch {
  /* Browser storage can be disabled. */
}
let resizePointer: number | null = null
function resizeWidth(value: number) {
  const max = Math.max(180, Math.min(560, (libraryLayout.value?.clientWidth ?? 900) - 320))
  directoryWidth.value = Math.round(Math.min(max, Math.max(180, value)))
  try {
    globalThis.localStorage.setItem('sf.storage.directoryWidth', String(directoryWidth.value))
  } catch {
    /* Width remains usable for this visit. */
  }
}
function resizeStart(event: InstanceType<typeof globalThis.PointerEvent>) {
  if (event.button !== 0) return
  resizePointer = event.pointerId
  ;(event.currentTarget as InstanceType<typeof globalThis.HTMLElement>).setPointerCapture(
    event.pointerId,
  )
}
function resizeMove(event: InstanceType<typeof globalThis.PointerEvent>) {
  if (resizePointer !== event.pointerId || !libraryLayout.value) return
  resizeWidth(event.clientX - libraryLayout.value.getBoundingClientRect().left)
}
function resizeEnd() {
  resizePointer = null
}
function resizeKey(event: InstanceType<typeof globalThis.KeyboardEvent>) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  resizeWidth(
    event.key === 'Home'
      ? 180
      : event.key === 'End'
        ? 560
        : directoryWidth.value + (event.key === 'ArrowLeft' ? -20 : 20),
  )
}
const props = defineProps<{ instance: StorageInstance }>()
const emit = defineEmits<{ updated: []; settings: [] }>()
const scopeId = ref(''),
  prefix = ref(''),
  layout = ref<'grid' | 'list'>('grid')
const scope = computed(() => props.instance.scopes.find((row) => row.scopeId === scopeId.value))
const folderDeletionScope = computed(() =>
  props.instance.scopes.find((row) => row.scopeId === folderDeletion.value?.scopeId),
)
const result = ref<ObjectPage | null>(null),
  error = ref<unknown>(null),
  treeError = ref<unknown>(null),
  busy = ref(false),
  mutating = ref(false)
const accessError = computed(() => (!result.value ? error.value || treeError.value : null))
const uploadDialog = ref(false),
  treeRevision = ref(0),
  page = ref(0),
  pageSize = ref(20),
  cursors = ref<(string | undefined)[]>([undefined])
const detailKey = ref(''),
  downloading = ref<string | null>(null)
const thumbnails = createThumbnailQueue()
let sequence = 0
const breadcrumbs = computed(() =>
  prefix.value
    .split('/')
    .filter(Boolean)
    .map((name, index, all) => ({ name, prefix: all.slice(0, index + 1).join('/') + '/' })),
)
const entries = computed(() => [
  ...(result.value?.commonPrefixes ?? []).map((full) => ({
    scopeId: scopeId.value,
    key: full,
    name: folderName(full),
    folder: true,
    full,
    file: null as StorageObject | null,
  })),
  ...libraryFiles(result.value?.items ?? []).map((file) => ({
    scopeId: scopeId.value,
    key: file.objectKey,
    name: file.name,
    folder: false,
    full: '',
    file,
  })),
])
type Entry = (typeof entries.value)[number]
const pageFiles = computed(() => libraryFiles(result.value?.items ?? []))
const selectedFiles = computed(() =>
  pageFiles.value.filter((file) => selectedKeys.value.includes(file.objectKey)),
)
const selectionAll = computed(
  () => pageFiles.value.length > 0 && selectedFiles.value.length === pageFiles.value.length,
)
const selectionSome = computed(() => selectedFiles.value.length > 0 && !selectionAll.value)
function toggleFile(key: string, checked: unknown) {
  if (checked) selectedKeys.value = [...new Set([...selectedKeys.value, key])]
  else selectedKeys.value = selectedKeys.value.filter((value) => value !== key)
}
function selectAll(checked: unknown) {
  selectedKeys.value = checked ? pageFiles.value.map((file) => file.objectKey) : []
}
const entryMenu = ref<DropdownInstance>(),
  menuEntry = ref<Entry | null>(null)
const menuAnchor = ref({ getBoundingClientRect: () => new globalThis.DOMRect(0, 0, 0, 0) })
let menuGeneration = 0
function closeMenu() {
  menuGeneration++
  entryMenu.value?.handleClose()
  menuEntry.value = null
}
type MenuEvent =
  InstanceType<typeof globalThis.MouseEvent> | InstanceType<typeof globalThis.KeyboardEvent>
async function showMenu(event: MenuEvent, entry: Entry) {
  if (busy.value || mutating.value) return
  entryMenu.value?.handleClose()
  const current = ++menuGeneration
  const rect = (
    event.currentTarget as InstanceType<typeof globalThis.HTMLElement>
  ).getBoundingClientRect()
  const x = ('clientX' in event && event.clientX) || rect.right,
    y = ('clientY' in event && event.clientY) || rect.bottom
  menuAnchor.value = { getBoundingClientRect: () => new globalThis.DOMRect(x, y, 0, 0) }
  menuEntry.value = entry
  await nextTick()
  if (current === menuGeneration) entryMenu.value?.handleOpen()
}
function rowMenu(
  row: unknown,
  _column: unknown,
  event: InstanceType<typeof globalThis.MouseEvent>,
) {
  event.preventDefault()
  void showMenu(event, row as Entry)
}
function treeMenu(event: MenuEvent, id: string, directory: string) {
  closeMenu()
  const target = props.instance.scopes.find((row) => row.scopeId === id)
  if (!target || !directory) return
  const full = target.prefix + directory
  void showMenu(event, {
    scopeId: id,
    key: `${id}:${full}`,
    name: folderName(full),
    folder: true,
    full,
    file: null,
  })
}
function menuCommand(action: string) {
  const entry = menuEntry.value
  closeMenu()
  if (!entry || busy.value || mutating.value) return
  if (action === 'open') {
    if (entry.folder) {
      const target = props.instance.scopes.find((row) => row.scopeId === entry.scopeId)
      if (target) navigate(target.scopeId, entry.full.slice(target.prefix.length))
    } else void open(entry.file!)
  } else if (action === 'delete') {
    if (entry.folder) folderDeletion.value = { scopeId: entry.scopeId, folder: entry.full }
    else removeFile(entry.file!)
  } else if (entry.file && action === 'download') void download(entry.file)
  else if (entry.file && action === 'copy') void copyLink(entry.file)
  else if (entry.file && action === 'link') copiedLink.value = fileLink(entry.file)
}
watch(
  () =>
    `${props.instance.instanceId}:${props.instance.version}|${props.instance.scopes.map((s) => `${s.scopeId}:${s.version}`).join(',')}`,
  () => {
    sequence++
    thumbnails.clear()
    closeDetails()
    uploadDialog.value = false
    const location = previewLocation(route.query, props.instance.scopes)
    scopeId.value = location?.scopeId ?? props.instance.scopes[0]?.scopeId ?? ''
    prefix.value = location?.directory ?? ''
    treeRevision.value++
    void load(true).then(() => openLinkedFile())
  },
  { immediate: true },
)
onBeforeUnmount(() => {
  sequence++
  thumbnails.clear()
  closeDetails()
})
async function load(reset = false) {
  const current = ++sequence
  selectedKeys.value = []
  closeMenu()
  thumbnails.clear()
  result.value = null
  error.value = null
  if (reset) {
    page.value = 0
    cursors.value = [undefined]
  }
  if (!scope.value) {
    busy.value = false
    return
  }
  busy.value = true
  try {
    const value = await api.listObjects(
      scopeId.value,
      prefix.value,
      cursors.value[page.value],
      pageSize.value,
    )
    if (current === sequence) result.value = value
  } catch (e) {
    if (current === sequence) error.value = e
  } finally {
    if (current === sequence) busy.value = false
  }
}
function navigate(id: string, directory: string) {
  if (mutating.value) return
  scopeId.value = id
  prefix.value = directory
  closeDetails()
  void router.replace({
    query: { instance: props.instance.instanceId, scope: id, directory: directory || undefined },
  })
  void load(true)
}
function entryClick(event: InstanceType<typeof globalThis.MouseEvent>, entry: Entry) {
  if (entry.folder) {
    enter(entry.full)
    return
  }
  if (selectedKeys.value.length || event.ctrlKey || event.metaKey || event.shiftKey)
    toggleFile(entry.key, !selectedKeys.value.includes(entry.key))
  else void open(entry.file!)
}
function enter(full: string) {
  if (scope.value) navigate(scopeId.value, full.slice(scope.value.prefix.length))
}
function next() {
  if (result.value?.nextCursor) {
    cursors.value[page.value + 1] = result.value.nextCursor
    page.value++
    void load()
  }
}
function previous() {
  if (page.value > 0) {
    page.value--
    void load()
  }
}
function closeDetails() {
  detailKey.value = ''
}
function open(file: StorageObject) {
  detailKey.value = file.objectKey
}
function openLinkedFile() {
  const location = previewLocation(route.query, props.instance.scopes)
  if (location?.file) detailKey.value = location.file
}

watch(
  () => [route.query.scope, route.query.directory, route.query.file],
  () => {
    const location = previewLocation(route.query, props.instance.scopes)
    if (!location) return
    if (scopeId.value !== location.scopeId || prefix.value !== location.directory) {
      scopeId.value = location.scopeId
      prefix.value = location.directory
      closeDetails()
      void load(true).then(() => openLinkedFile())
    } else if (location.file && detailKey.value !== location.file) void openLinkedFile()
    else if (!location.file) closeDetails()
  },
)
function fileLink(file: StorageObject) {
  const href = router.resolve({
    path: route.path,
    query: { instance: props.instance.instanceId, scope: scopeId.value, file: file.objectKey },
  }).href
  return new globalThis.URL(href, globalThis.location.origin).href
}
async function copyLink(file: StorageObject) {
  const url = fileLink(file)
  try {
    await globalThis.navigator.clipboard.writeText(url)
    ElMessage.success('预览链接已复制，打开时需要平台访问权限')
  } catch {
    copiedLink.value = url
  }
}
function resizePage() {
  void load(true)
}
async function download(file: StorageObject) {
  if (downloading.value) return
  const target = globalThis.open('about:blank', '_blank')
  if (target) target.opener = null
  const current = sequence,
    id = scopeId.value
  downloading.value = file.objectKey
  error.value = null
  try {
    const value = await api.getObject(id, file.objectKey)
    if (current !== sequence) {
      target?.close()
      return
    }
    if (target)
      target.location.replace(
        api.objectContentUrl(id, value.objectKey, value.objectToken!, 'attachment'),
      )
    else {
      globalThis.location.assign(
        api.objectContentUrl(id, value.objectKey, value.objectToken!, 'attachment'),
      )
    }
  } catch (e) {
    target?.close()
    if (current === sequence) error.value = e
  } finally {
    downloading.value = null
  }
}
async function filesUpdated() {
  await load(true)
  treeRevision.value++
  emit('updated')
}
function refreshFiles() {
  treeError.value = null
  treeRevision.value++
  void load(true)
}
async function folderUpdated(task: StorageTask) {
  const selected = folderDeletion.value,
    target = folderDeletionScope.value
  if (selected && target && task.status === 'SUCCEEDED' && scopeId.value === selected.scopeId) {
    const relative = selected.folder.slice(target.prefix.length)
    if (prefix.value.startsWith(relative)) {
      const withoutSlash = relative.slice(0, -1)
      prefix.value = withoutSlash.slice(0, withoutSlash.lastIndexOf('/') + 1)
      closeDetails()
      await router.replace({
        query: {
          instance: props.instance.instanceId,
          scope: scopeId.value,
          directory: prefix.value || undefined,
        },
      })
    }
  }
  await filesUpdated()
}
async function createFolder() {
  if (busy.value || mutating.value || !scope.value) return
  const current = sequence,
    selected = scope.value,
    directory = prefix.value
  try {
    const input = await ElMessageBox.prompt('文件夹名称', '新建文件夹', {
      inputPlaceholder: '请输入名称',
      inputPattern: /^(?!\.{1,2}$)[^/\\]{1,255}$/,
      inputErrorMessage: '请输入单层文件夹名称',
      confirmButtonText: '创建',
      cancelButtonText: '取消',
    })
    if (current !== sequence) return
    mutating.value = true
    error.value = null
    await api.createFolder(selected.scopeId, {
      name: input.value,
      relativePrefix: directory,
      instanceVersion: props.instance.version,
      scopeVersion: selected.version,
      idempotencyKey: crypto.randomUUID(),
    })
    await filesUpdated()
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') error.value = e
  } finally {
    mutating.value = false
  }
}
function removeFile(file: StorageObject) {
  if (!busy.value && !mutating.value) fileDeletion.value = [{ ...file }]
}
function removeSelection() {
  if (!busy.value && !mutating.value && selectedFiles.value.length)
    fileDeletion.value = selectedFiles.value.map((file) => ({ ...file }))
}
const firstSelected = computed(() => selectedFiles.value[0])
</script>

<template>
  <div
    v-if="scope"
    ref="libraryLayout"
    class="library-layout"
    :style="{ '--directory-width': directoryWidth + 'px' }"
  >
    <StorageFolderTree
      :key="treeRevision"
      :instance="instance"
      :scope-id="scopeId"
      :prefix="prefix"
      :disabled="busy || mutating"
      @navigate="navigate"
      @context-menu="treeMenu"
      @access-error="treeError = $event"
      @retry="refreshFiles"
    />
    <div
      class="directory-resizer"
      role="separator"
      tabindex="0"
      aria-label="调整目录栏宽度"
      aria-orientation="vertical"
      :aria-valuenow="directoryWidth"
      :aria-valuemin="180"
      :aria-valuemax="560"
      @pointerdown="resizeStart"
      @pointermove="resizeMove"
      @pointerup="resizeEnd"
      @pointercancel="resizeEnd"
      @lostpointercapture="resizeEnd"
      @keydown="resizeKey"
    />
    <section v-loading="busy" class="file-browser" aria-label="文件库">
      <div class="file-toolbar">
        <ElButton
          type="primary"
          :icon="Upload"
          :disabled="busy || mutating || !!accessError"
          @click="uploadDialog = true"
          >上传文件</ElButton
        ><ElButton
          :icon="FolderAdd"
          :disabled="busy || mutating || !!accessError"
          @click="createFolder"
          >新建文件夹</ElButton
        >
        <div class="spacer" />
        <ElButton
          :icon="Refresh"
          :disabled="busy || mutating"
          aria-label="刷新文件"
          @click="refreshFiles"
        />
        <div class="view-toggle">
          <ElButton
            :icon="Grid"
            :type="layout === 'grid' ? 'primary' : undefined"
            :plain="layout === 'grid'"
            aria-label="网格视图"
            :aria-pressed="layout === 'grid'"
            @click="layout = 'grid'"
          /><ElButton
            :icon="List"
            :type="layout === 'list' ? 'primary' : undefined"
            :plain="layout === 'list'"
            aria-label="列表视图"
            :aria-pressed="layout === 'list'"
            @click="layout = 'list'"
          />
        </div>
      </div>
      <nav class="breadcrumbs" aria-label="文件路径">
        <button type="button" :disabled="mutating" @click="navigate(scopeId, '')">
          {{ scope.displayName || scope.bucket }}</button
        ><template v-for="crumb in breadcrumbs" :key="crumb.prefix"
          ><span>/</span
          ><button type="button" :disabled="mutating" @click="navigate(scopeId, crumb.prefix)">
            {{ crumb.name }}
          </button></template
        >
      </nav>
      <RequestError v-if="error && result" :error="error" />
      <StorageAccessState
        v-if="accessError"
        :error="accessError"
        :busy="busy"
        @retry="refreshFiles"
        @settings="emit('settings')"
      />
      <template v-else>
        <div v-if="pageFiles.length" class="selection-toolbar" aria-label="文件选择操作">
          <ElCheckbox
            :model-value="selectionAll"
            :indeterminate="selectionSome"
            :disabled="busy || mutating"
            aria-label="全选本页文件"
            @change="selectAll"
            >全选本页</ElCheckbox
          >
          <span v-if="selectedFiles.length" class="selection-count"
            >已选 {{ selectedFiles.length }} 个文件</span
          >
          <ElButton v-if="selectedFiles.length" text @click="selectedKeys = []">取消选择</ElButton>
          <div class="spacer" />
          <template v-if="selectedFiles.length === 1 && firstSelected">
            <ElTooltip content="文件详情"
              ><ElButton
                :icon="Document"
                aria-label="查看选中文件详情"
                text
                @click="open(firstSelected)"
            /></ElTooltip>
            <ElTooltip content="复制预览链接"
              ><ElButton
                :icon="Link"
                aria-label="复制选中文件链接"
                text
                @click="copyLink(firstSelected)"
            /></ElTooltip>
            <ElTooltip content="下载"
              ><ElButton
                :icon="Download"
                aria-label="下载选中文件"
                text
                :disabled="!!downloading"
                @click="download(firstSelected)"
            /></ElTooltip>
          </template>
          <ElButton
            v-if="selectedFiles.length"
            :icon="Delete"
            type="danger"
            plain
            :disabled="busy || mutating"
            @click="removeSelection"
            >删除选中</ElButton
          >
        </div>
        <div v-if="layout === 'grid'" class="file-grid">
          <article
            v-for="entry in entries"
            :key="entry.key"
            class="file-card"
            :class="{ 'folder-card': entry.folder, selected: selectedKeys.includes(entry.key) }"
            @contextmenu.prevent.stop="showMenu($event, entry)"
            @keydown.shift.f10.prevent="showMenu($event, entry)"
          >
            <div class="card-controls">
              <ElCheckbox
                v-if="entry.file"
                class="card-select"
                :model-value="selectedKeys.includes(entry.key)"
                :aria-label="`选择 ${entry.name}`"
                :disabled="busy || mutating"
                @change="toggleFile(entry.key, $event)"
                @click.stop
              />
              <ElButton
                class="entry-more"
                text
                :icon="MoreFilled"
                :aria-label="`${entry.name} 更多操作`"
                :disabled="busy || mutating"
                @click.stop="showMenu($event, entry)"
              />
            </div>
            <button
              type="button"
              class="file-card-main"
              :title="entry.name"
              :disabled="busy || mutating"
              @click="entryClick($event, entry)"
            >
              <span v-if="entry.folder" class="folder-cover"
                ><ElIcon :size="48"><Folder /></ElIcon></span
              ><StorageThumbnail
                v-else
                :file="entry.file!"
                :scope-id="scopeId"
                :queue="thumbnails"
              />
              <span class="file-name">{{ entry.name }}</span
              ><span class="file-meta">{{
                entry.folder ? '文件夹' : bytes(entry.file!.sizeBytes)
              }}</span>
            </button>
          </article>
        </div>
        <ElTable
          v-else
          :data="entries"
          row-key="key"
          empty-text="文件夹为空"
          @row-contextmenu="rowMenu"
          ><ElTableColumn width="48"
            ><template #default="{ row }"
              ><ElCheckbox
                v-if="row.file"
                :model-value="selectedKeys.includes(row.key)"
                :aria-label="`选择 ${row.name}`"
                :disabled="busy || mutating"
                @change="toggleFile(row.key, $event)" /></template></ElTableColumn
          ><ElTableColumn label="名称" min-width="190"
            ><template #default="{ row }"
              ><button
                class="list-name"
                :disabled="busy || mutating"
                @click="row.folder ? enter(row.full) : open(row.file)"
              >
                <ElIcon v-if="row.folder"><Folder /></ElIcon>{{ row.name }}
              </button></template
            ></ElTableColumn
          ><ElTableColumn label="大小" width="115"
            ><template #default="{ row }">{{
              row.folder ? '—' : bytes(row.file.sizeBytes)
            }}</template></ElTableColumn
          ><ElTableColumn label="修改时间" min-width="180"
            ><template #default="{ row }">{{
              row.folder ? '—' : time(row.file.lastModified)
            }}</template></ElTableColumn
          ><ElTableColumn label="" width="56" align="center"
            ><template #default="{ row }"
              ><ElButton
                text
                :icon="MoreFilled"
                :aria-label="`${row.name} 更多操作`"
                :disabled="busy || mutating"
                @click="showMenu($event, row as Entry)" /></template
          ></ElTableColumn>
          ></ElTable
        >
        <ElEmpty
          v-if="layout === 'grid' && !entries.length && !busy && !error"
          description="文件夹为空"
        />
        <div class="file-footer">
          <span
            >本页 {{ result?.commonPrefixes.length ?? 0 }} 个文件夹 ·
            {{ libraryFiles(result?.items ?? []).length }} 个文件</span
          >
          <div class="file-pagination">
            <ElSelect
              v-model="pageSize"
              aria-label="每页条数"
              :disabled="busy || mutating"
              @change="resizePage"
              ><ElOption
                v-for="size in [20, 50, 100]"
                :key="size"
                :value="size"
                :label="`${size} 条/页`" /></ElSelect
            ><span>第 {{ page + 1 }} 页</span>
            <ElButton :disabled="!page || busy || mutating" @click="previous">上一页</ElButton
            ><ElButton :disabled="!result?.hasMore || busy || mutating" @click="next"
              >下一页</ElButton
            >
          </div>
        </div>
      </template>
    </section>
    <ElDropdown
      ref="entryMenu"
      virtual-triggering
      :virtual-ref="menuAnchor"
      trigger="click"
      placement="bottom-start"
      @command="menuCommand"
    >
      <template #dropdown
        ><ElDropdownMenu v-if="menuEntry" class="file-context-menu">
          <ElDropdownItem command="open" :icon="menuEntry.folder ? Folder : Document">{{
            menuEntry.folder ? '打开文件夹' : '文件详情'
          }}</ElDropdownItem>
          <ElDropdownItem
            v-if="menuEntry.file"
            command="download"
            :icon="Download"
            :disabled="!!downloading"
            >下载</ElDropdownItem
          >
          <ElDropdownItem v-if="menuEntry.file" command="copy" :icon="CopyDocument"
            >复制预览链接</ElDropdownItem
          >
          <ElDropdownItem v-if="menuEntry.file" command="link" :icon="Link"
            >查看链接</ElDropdownItem
          >
          <ElDropdownItem command="delete" :icon="Delete" divided class="delete-menu-item">{{
            menuEntry.folder ? '删除文件夹及内容' : '删除文件'
          }}</ElDropdownItem>
        </ElDropdownMenu></template
      >
    </ElDropdown>
    <StorageFileDelete
      v-if="fileDeletion"
      :instance="instance"
      :scope="scope"
      :files="fileDeletion"
      @close="fileDeletion = null"
      @updated="filesUpdated"
    />
    <StorageFolderDelete
      v-if="folderDeletion && folderDeletionScope"
      :key="`${folderDeletion.scopeId}:${folderDeletion.folder}`"
      :instance="instance"
      :scope="folderDeletionScope"
      :folder="folderDeletion.folder"
      @close="folderDeletion = null"
      @updated="folderUpdated"
    />
    <ElDialog
      :model-value="!!copiedLink"
      class="management-dialog"
      title="预览链接"
      width="min(560px,94vw)"
      align-center
      @update:model-value="copiedLink = ''"
      ><ElInput
        :model-value="copiedLink"
        readonly
        aria-label="预览链接"
        @focus="($event.target as HTMLInputElement).select()"
      />
      <p class="link-dialog-note">打开链接时需要登录平台并具有图床访问权限。</p></ElDialog
    >
    <StorageUpload
      v-model="uploadDialog"
      :instance="instance"
      :scope="scope"
      :prefix="prefix"
      @updated="filesUpdated"
    />
    <StorageFileDetails
      v-if="detailKey"
      :key="`${scopeId}:${detailKey}`"
      :instance="instance"
      :scope="scope"
      :object-key="detailKey"
      :downloading="!!downloading"
      @close="closeDetails"
      @download="download"
      @copy-link="copyLink"
      @show-link="copiedLink = fileLink($event)"
    />
  </div>
  <ElEmpty v-else description="尚未配置存储目录"
    ><ElButton type="primary" @click="emit('settings')">前往设置</ElButton></ElEmpty
  >
</template>

<style scoped>
.library-layout {
  display: grid;
  grid-template-columns: minmax(180px, min(var(--directory-width), 45%)) 8px minmax(0, 1fr);
  min-height: 520px;
}
.directory-resizer {
  cursor: col-resize;
  touch-action: none;
  background: #f8fafc;
  border-inline: 2px solid #ffffff;
  transition: background-color 120ms ease;
}
.directory-resizer:hover,
.directory-resizer:focus-visible {
  background: #93c5fd;
  outline: none;
}
.file-pagination {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  justify-content: flex-end;
  margin-left: auto;
}
.file-pagination .el-select {
  width: 110px;
}
.file-browser {
  min-width: 0;
  padding: 18px 22px;
}
.file-toolbar,
.breadcrumbs,
.file-footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}
.file-toolbar {
  margin-bottom: 14px;
}
.spacer {
  flex: 1;
}
.view-toggle {
  display: flex;
  gap: 0;
}
.view-toggle :deep(.el-button + .el-button) {
  margin-left: 4px;
}
.breadcrumbs {
  margin-bottom: 14px;
  padding: 6px 12px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
  color: #94a3b8;
  font-size: 13px;
}
.breadcrumbs button {
  background: none;
  border: 0;
  padding: 2px 4px;
  border-radius: 4px;
  color: #475569;
  font: inherit;
  font-weight: 500;
  cursor: pointer;
  overflow-wrap: anywhere;
  text-align: left;
}
.breadcrumbs button:hover:not(:disabled) {
  background: #e2e8f0;
  color: #0f172a;
}
.breadcrumbs button:last-child {
  color: #0f172a;
  font-weight: 600;
}
.file-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(176px, 1fr));
  gap: 14px;
  align-items: start;
}
.file-card {
  position: relative;
  min-width: 0;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  overflow: hidden;
  background: #ffffff;
  transition:
    border-color 0.15s ease,
    box-shadow 0.15s ease;
}
.file-card:hover {
  border-color: #93c5fd;
  box-shadow: 0 6px 16px -6px rgba(15, 23, 42, 0.08);
}
.file-card-main {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  width: 100%;
  min-width: 0;
  border: 0;
  padding: 0;
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
}
.file-card-main:disabled {
  cursor: default;
}
.folder-cover {
  height: 136px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #f59e0b;
  background: #f8fafc;
}
.file-name {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  overflow-wrap: anywhere;
  margin: 10px 12px 4px;
  font-size: 13px;
  font-weight: 500;
  color: #0f172a;
  min-height: 20px;
}
.file-meta {
  margin: 0 12px 10px;
  color: #64748b;
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
.file-card.selected {
  border-color: #2563eb;
  box-shadow: 0 0 0 2px rgba(37, 99, 235, 0.16);
}
.card-controls {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 36px;
  padding: 4px 8px;
  border-bottom: 1px solid #f1f5f9;
  background: #ffffff;
}
.card-select {
  background: #ffffff;
  padding: 0 6px;
  border-radius: 4px;
  height: 26px;
}
.card-controls .entry-more {
  margin-left: auto;
  width: 26px;
  height: 26px;
  padding: 4px;
  background: #ffffff;
  border: 1px solid #e2e8f0;
  border-radius: 6px;
}
.selection-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  min-height: 36px;
  padding: 4px 12px;
  margin: 0 0 14px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
}
.selection-count {
  padding: 1px 8px;
  border-radius: 999px;
  background: #eff6ff;
  font-size: 12px;
  font-weight: 600;
  color: #1d4ed8;
}
.selection-toolbar :deep(.el-button + .el-button) {
  margin-left: 0;
}
:global(.file-context-menu) {
  min-width: 168px;
}
:global(.file-context-menu .delete-menu-item) {
  color: var(--el-color-danger);
}
.list-name {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  background: none;
  border: 0;
  font: inherit;
  font-weight: 500;
  color: #0f172a;
  cursor: pointer;
  text-align: left;
  overflow-wrap: anywhere;
}
.list-name:hover:not(:disabled) {
  color: #2563eb;
}
.list-name .el-icon {
  color: #f59e0b;
  flex-shrink: 0;
}
.file-footer {
  margin-top: 20px;
  padding-top: 12px;
  border-top: 1px solid #f1f5f9;
  justify-content: flex-end;
  font-size: 12px;
  color: #64748b;
  font-variant-numeric: tabular-nums;
}
.link-dialog-note {
  margin: 10px 0 0;
  color: #64748b;
  font-size: 12.5px;
}
@media (max-width: 1000px) {
  .file-browser {
    padding: 16px;
  }
  .file-grid {
    gap: 12px;
  }
}
@media (max-width: 700px) {
  .directory-resizer {
    display: none;
  }
  .library-layout {
    grid-template-columns: minmax(0, 1fr);
  }
  .file-browser {
    padding: 14px;
  }
  .file-toolbar {
    gap: 8px;
  }
  .file-toolbar :deep(.el-button + .el-button) {
    margin-left: 0;
  }
  .file-grid {
    grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
  }
}
</style>
