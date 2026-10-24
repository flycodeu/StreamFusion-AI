<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElButton, ElInput, ElPopover } from 'element-plus'
import { Star } from '@element-plus/icons-vue'
import { sessionState } from '../../session/state'
import {
  MAX_SAVED_VIEWS,
  readSavedViews,
  savedViewsKey,
  viewName,
  writeSavedViews,
  type SavedView,
} from './saved-views'

const props = defineProps<{
  busy: boolean
  current: () => Pick<SavedView, 'layout' | 'slots'>
}>()
const emit = defineEmits<{ open: [view: SavedView] }>()
const visible = ref(false),
  views = ref<SavedView[]>([]),
  name = ref(''),
  failure = ref(''),
  note = ref(''),
  renamed = ref<Record<string, string>>({})
const userId = computed(() => sessionState.me?.user.id)
function report(cause: unknown) {
  failure.value = cause instanceof Error ? cause.message : '本地存储不可用，未保存'
  note.value = ''
}
function reload() {
  failure.value = ''
  note.value = ''
  try {
    views.value = userId.value ? readSavedViews(userId.value) : []
    renamed.value = Object.fromEntries(views.value.map((view) => [view.id, view.name]))
  } catch (cause) {
    views.value = []
    report(cause)
  }
}
function save(change: (existing: SavedView[]) => SavedView[], message: string) {
  if (!userId.value || props.busy) return
  try {
    // Re-read before writing so another tab's latest saved list is not silently replaced.
    views.value = writeSavedViews(userId.value, change(readSavedViews(userId.value)))
    renamed.value = Object.fromEntries(views.value.map((view) => [view.id, view.name]))
    failure.value = ''
    note.value = message
  } catch (cause) {
    report(cause)
  }
}
function create() {
  save((existing) => {
    if (existing.length >= MAX_SAVED_VIEWS)
      throw new Error(`最多保存 ${MAX_SAVED_VIEWS} 个收藏视图`)
    return [
      ...existing,
      {
        id: crypto.randomUUID(),
        name: viewName(name.value),
        ...props.current(),
        updatedAt: new Date().toISOString(),
      },
    ]
  }, '已保存到当前账号的本地收藏')
}
function update(id: string, rename: boolean) {
  save(
    (existing) => {
      if (!existing.some((view) => view.id === id)) throw new Error('该收藏已删除，请刷新列表')
      return existing.map((view) =>
        view.id === id
          ? {
              ...view,
              ...(rename ? { name: viewName(renamed.value[id] || '') } : props.current()),
              updatedAt: new Date().toISOString(),
            }
          : view,
      )
    },
    rename ? '已重命名' : '已用当前画面明确更新收藏',
  )
}
function remove(id: string) {
  save((existing) => existing.filter((view) => view.id !== id), '已删除本地收藏')
}
function open(view: SavedView) {
  if (props.busy) return
  visible.value = false
  emit('open', view)
}
function stored(event: globalThis.StorageEvent) {
  if (userId.value && (event.key === null || event.key === savedViewsKey(userId.value))) reload()
}
watch(
  () => [userId.value, sessionState.epoch],
  () => {
    visible.value = false
    name.value = ''
    reload()
  },
  { immediate: true },
)
onMounted(() => globalThis.addEventListener?.('storage', stored))
onBeforeUnmount(() => globalThis.removeEventListener?.('storage', stored))
</script>

<template>
  <ElPopover
    v-model:visible="visible"
    placement="top-end"
    :width="400"
    :popper-style="{ maxWidth: 'calc(100vw - 24px)' }"
    trigger="click"
    @show="reload"
  >
    <template #reference>
      <ElButton size="small" :icon="Star" :disabled="!userId || busy">收藏视图</ElButton>
    </template>
    <section class="saved-views" aria-label="收藏视图">
      <header>
        <strong>收藏视图</strong>
        <span>{{ views.length }} / {{ MAX_SAVED_VIEWS }}</span>
      </header>
      <form class="save-current" @submit.prevent="create">
        <ElInput
          v-model="name"
          maxlength="80"
          aria-label="新收藏名称"
          placeholder="例如：日常巡看 6×6"
          size="small"
        />
        <ElButton native-type="submit" size="small" type="primary" plain :disabled="busy"
          >保存当前</ElButton
        >
      </form>
      <p v-if="failure" class="saved-error" role="alert">{{ failure }}；现有预览不受影响。</p>
      <p v-else-if="note" class="saved-note" role="status">{{ note }}</p>
      <div class="saved-list">
        <article v-for="view in views" :key="view.id">
          <div class="saved-title">
            <ElInput
              v-model="renamed[view.id]"
              :aria-label="`${view.name} 的名称`"
              maxlength="80"
              size="small"
            />
            <span>{{ view.slots.length }} 路</span>
          </div>
          <div class="saved-actions">
            <ElButton size="small" type="primary" :disabled="busy" @click="open(view)"
              >打开</ElButton
            >
            <ElButton size="small" :disabled="busy" @click="update(view.id, false)"
              >更新画面</ElButton
            >
            <ElButton size="small" :disabled="busy" @click="update(view.id, true)">重命名</ElButton>
            <ElButton size="small" :disabled="busy" @click="remove(view.id)">删除</ElButton>
          </div>
        </article>
        <p v-if="!views.length && !failure" class="saved-empty">还没有收藏，先配置画面后保存。</p>
      </div>
    </section>
  </ElPopover>
</template>

<style scoped>
.saved-views {
  display: grid;
  gap: 10px;
  color: var(--el-text-color-primary);
}
header,
.save-current,
.saved-title,
.saved-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
header {
  justify-content: space-between;
  font-size: 13px;
}
header span,
.saved-title span,
p {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
header span,
.saved-title span {
  display: inline-flex;
  align-items: center;
  padding: 1px 8px;
  border-radius: 999px;
  background: #f1f5f9;
  color: #475569;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.save-current :deep(.el-input),
.saved-title :deep(.el-input) {
  min-width: 0;
  width: 100%;
}
.saved-list {
  display: grid;
  gap: 8px;
  max-height: min(380px, 56dvh);
  overflow: auto;
}
article {
  display: grid;
  gap: 8px;
  padding: 10px 12px;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  background: #f8fafc;
}
.saved-actions {
  flex-wrap: wrap;
}
.saved-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}
p {
  margin: 0;
  overflow-wrap: anywhere;
}
.saved-note {
  padding: 6px 10px;
  border-radius: 6px;
  background: #eff6ff;
  color: #1d4ed8;
}
.saved-empty {
  padding: 16px 12px;
  border: 1px dashed #cbd5e1;
  border-radius: 8px;
  background: #f8fafc;
  text-align: center;
}
.saved-error {
  padding: 6px 10px;
  border-radius: 6px;
  background: #fef2f2;
  color: var(--el-color-danger);
}
</style>
