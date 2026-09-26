<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElButton, ElInput, ElPopover } from 'element-plus'
import { iconCatalog } from './catalog'
import AppIcon from './AppIcon.vue'
const model = defineModel<string | null>({ default: null })
const query = ref('')
const visible = ref(false)
const selected = computed(() => iconCatalog.find((icon) => icon.key === model.value))
const filtered = computed(() =>
  iconCatalog.filter((icon) =>
    `${icon.label} ${icon.key}`.toLowerCase().includes(query.value.trim().toLowerCase()),
  ),
)
function choose(key: string | null) {
  model.value = key
  visible.value = false
  query.value = ''
}
</script>

<template>
  <ElPopover v-model:visible="visible" placement="bottom-start" :width="340" trigger="click">
    <template #reference
      ><ElButton class="icon-picker-trigger"
        ><AppIcon v-if="model" :name="model" /><span>{{
          selected?.label || (model ? '当前图标' : '选择图标')
        }}</span></ElButton
      ></template
    >
    <ElInput v-model="query" placeholder="搜索图标" clearable aria-label="搜索图标" />
    <div class="icon-grid" role="group" aria-label="菜单图标">
      <button
        v-for="icon in filtered"
        :key="icon.key"
        type="button"
        :aria-label="icon.label"
        :aria-pressed="model === icon.key"
        @click="choose(icon.key)"
      >
        <AppIcon :name="icon.key" /><span>{{ icon.label }}</span>
      </button>
      <div v-if="!filtered.length" class="empty-icons">未找到图标</div>
    </div>
    <ElButton text @click="choose(null)">清除图标</ElButton>
  </ElPopover>
</template>

<style scoped>
.icon-picker-trigger :deep(.app-icon) {
  margin-right: 8px;
}
.icon-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 4px;
  max-height: 280px;
  overflow-y: auto;
  margin: 10px 0;
}
.icon-grid button {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 7px;
  padding: 10px 2px;
  background: transparent;
  color: var(--el-text-color-regular);
  border: 1px solid transparent;
  border-radius: 4px;
  cursor: pointer;
  font-size: 12px;
}
.icon-grid button:hover,
.icon-grid button[aria-pressed='true'] {
  color: var(--el-color-primary);
  background: var(--el-color-primary-light-9);
  border-color: var(--el-color-primary-light-5);
}
.empty-icons {
  grid-column: 1 / -1;
  text-align: center;
  padding: 24px;
}
</style>
