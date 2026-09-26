<script setup lang="ts">
import { ref } from 'vue'
import { ElButton, ElPopover } from 'element-plus'
import UserAvatar from '../../components/avatar/UserAvatar.vue'
import { avatarCatalog } from '../../components/avatar/catalog'
defineProps<{ name: string }>()
const model = defineModel<string | null>({ required: true })
const visible = ref(false)
function choose(key: string | null) {
  model.value = key
  visible.value = false
}
</script>

<template>
  <div class="avatar-field">
    <UserAvatar :avatar-key="model" :name="name" :size="64" />
    <ElPopover v-model:visible="visible" trigger="click" placement="bottom-start" :width="280">
      <template #reference><ElButton>选择头像</ElButton></template>
      <div class="avatar-options" role="group" aria-label="预置头像">
        <button
          v-for="avatar in avatarCatalog"
          :key="avatar.key"
          type="button"
          :aria-label="avatar.label"
          :aria-pressed="model === avatar.key"
          @click="choose(avatar.key)"
        >
          <UserAvatar :avatar-key="avatar.key" :size="48" />
        </button>
      </div>
      <ElButton class="default-avatar" @click="choose('default')">使用姓名头像</ElButton>
    </ElPopover>
  </div>
</template>

<style scoped>
.avatar-field {
  display: flex;
  align-items: center;
  gap: 18px;
}
.avatar-options {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 12px;
  padding: 4px;
}
.avatar-options button {
  display: grid;
  place-items: center;
  padding: 8px;
  border: 1px solid transparent;
  border-radius: 6px;
  background: var(--el-bg-color);
  cursor: pointer;
}
.avatar-options button:hover,
.avatar-options button[aria-pressed='true'] {
  border-color: var(--el-color-primary);
  background: var(--el-color-primary-light-9);
}
.default-avatar {
  width: 100%;
  margin-top: 12px;
}
</style>
