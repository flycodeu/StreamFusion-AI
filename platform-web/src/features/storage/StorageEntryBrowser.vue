<script setup lang="ts">
import { computed } from 'vue'
import {
  ElButton,
  ElCheckbox,
  ElEmpty,
  ElIcon,
  ElOption,
  ElSelect,
  ElTable,
  ElTableColumn,
  ElTooltip,
} from 'element-plus'
import { Delete, Document, Download, Folder, Link, MoreFilled } from '@element-plus/icons-vue'
import type { ObjectPage, StorageObject } from '../../api/storage/types'
import StorageThumbnail from './StorageThumbnail.vue'
import { createThumbnailQueue, libraryFiles } from './file-browser'
import { bytes, time } from './display'

export interface StorageBrowserEntry {
  scopeId: string
  key: string
  name: string
  folder: boolean
  full: string
  file: StorageObject | null
}

export type StorageMenuEvent =
  InstanceType<typeof globalThis.MouseEvent> | InstanceType<typeof globalThis.KeyboardEvent>

const props = defineProps<{
  layout: 'grid' | 'list'
  scopeId: string
  entries: StorageBrowserEntry[]
  result: ObjectPage | null
  busy: boolean
  mutating: boolean
  error: unknown
  downloading: string | null
  selectedKeys: string[]
  page: number
  pageSize: number
  thumbnails: ReturnType<typeof createThumbnailQueue>
}>()

const emit = defineEmits<{
  'update:selectedKeys': [value: string[]]
  'update:pageSize': [value: number]
  resizePage: []
  previous: []
  next: []
  open: [file: StorageObject]
  enter: [full: string]
  copyLink: [file: StorageObject]
  download: [file: StorageObject]
  removeSelection: []
  entryClick: [event: InstanceType<typeof globalThis.MouseEvent>, entry: StorageBrowserEntry]
  showMenu: [event: StorageMenuEvent, entry: StorageBrowserEntry]
}>()

const pageFiles = computed(() => libraryFiles(props.result?.items ?? []))
const selectedFiles = computed(() =>
  pageFiles.value.filter((file) => props.selectedKeys.includes(file.objectKey)),
)
const selectionAll = computed(
  () => pageFiles.value.length > 0 && selectedFiles.value.length === pageFiles.value.length,
)
const selectionSome = computed(() => selectedFiles.value.length > 0 && !selectionAll.value)
const firstSelected = computed(() => selectedFiles.value[0])

function toggleFile(key: string, checked: unknown) {
  if (checked) emit('update:selectedKeys', [...new Set([...props.selectedKeys, key])])
  else
    emit(
      'update:selectedKeys',
      props.selectedKeys.filter((value) => value !== key),
    )
}

function selectAll(checked: unknown) {
  emit('update:selectedKeys', checked ? pageFiles.value.map((file) => file.objectKey) : [])
}

function rowMenu(
  row: unknown,
  _column: unknown,
  event: InstanceType<typeof globalThis.MouseEvent>,
) {
  event.preventDefault()
  emit('showMenu', event, row as StorageBrowserEntry)
}
</script>

<template>
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
    <ElButton v-if="selectedFiles.length" text @click="emit('update:selectedKeys', [])"
      >取消选择</ElButton
    >
    <div class="spacer" />
    <template v-if="selectedFiles.length === 1 && firstSelected">
      <ElTooltip content="文件详情"
        ><ElButton
          :icon="Document"
          aria-label="查看选中文件详情"
          text
          @click="emit('open', firstSelected)"
      /></ElTooltip>
      <ElTooltip content="复制预览链接"
        ><ElButton
          :icon="Link"
          aria-label="复制选中文件链接"
          text
          @click="emit('copyLink', firstSelected)"
      /></ElTooltip>
      <ElTooltip content="下载"
        ><ElButton
          :icon="Download"
          aria-label="下载选中文件"
          text
          :disabled="!!downloading"
          @click="emit('download', firstSelected)"
      /></ElTooltip>
    </template>
    <ElButton
      v-if="selectedFiles.length"
      :icon="Delete"
      type="danger"
      plain
      :disabled="busy || mutating"
      @click="emit('removeSelection')"
      >删除选中</ElButton
    >
  </div>
  <div v-if="layout === 'grid'" class="file-grid">
    <article
      v-for="entry in entries"
      :key="entry.key"
      class="file-card"
      :class="{ 'folder-card': entry.folder, selected: selectedKeys.includes(entry.key) }"
      @contextmenu.prevent.stop="emit('showMenu', $event, entry)"
      @keydown.shift.f10.prevent="emit('showMenu', $event, entry)"
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
          @click.stop="emit('showMenu', $event, entry)"
        />
      </div>
      <button
        type="button"
        class="file-card-main"
        :title="entry.name"
        :disabled="busy || mutating"
        @click="emit('entryClick', $event, entry)"
      >
        <span v-if="entry.folder" class="folder-cover"
          ><ElIcon :size="48"><Folder /></ElIcon></span
        ><StorageThumbnail v-else :file="entry.file!" :scope-id="scopeId" :queue="thumbnails" />
        <span class="file-name">{{ entry.name }}</span
        ><span class="file-meta">{{ entry.folder ? '文件夹' : bytes(entry.file!.sizeBytes) }}</span>
      </button>
    </article>
  </div>
  <ElTable v-else :data="entries" row-key="key" empty-text="文件夹为空" @row-contextmenu="rowMenu"
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
          @click="row.folder ? emit('enter', row.full) : emit('open', row.file)"
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
          @click="emit('showMenu', $event, row as StorageBrowserEntry)" /></template
    ></ElTableColumn>
  </ElTable>
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
        :model-value="pageSize"
        aria-label="每页条数"
        :disabled="busy || mutating"
        @update:model-value="emit('update:pageSize', $event)"
        @change="emit('resizePage')"
        ><ElOption
          v-for="size in [20, 50, 100]"
          :key="size"
          :value="size"
          :label="`${size} 条/页`" /></ElSelect
      ><span>第 {{ page + 1 }} 页</span>
      <ElButton :disabled="!page || busy || mutating" @click="emit('previous')">上一页</ElButton
      ><ElButton :disabled="!result?.hasMore || busy || mutating" @click="emit('next')"
        >下一页</ElButton
      >
    </div>
  </div>
</template>
