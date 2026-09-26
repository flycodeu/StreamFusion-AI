<script setup lang="ts">
import { computed } from 'vue'
import { UserFilled } from '@element-plus/icons-vue'
import { resolveAvatar } from './catalog'

const props = withDefaults(
  defineProps<{ avatarKey?: string | null; name?: string; size?: number }>(),
  { size: 40 },
)
const avatar = computed(() => resolveAvatar(props.avatarKey))
const initial = computed(() => Array.from(props.name || '用户')[0])
</script>

<template>
  <span
    class="user-avatar"
    aria-hidden="true"
    :style="{
      width: `${size}px`,
      height: `${size}px`,
      fontSize: `${Math.round(size * 0.4)}px`,
      background: avatar?.background,
      color: avatar?.color,
    }"
  >
    <UserFilled v-if="avatar" /><span v-else>{{ initial }}</span>
  </span>
</template>

<style scoped>
.user-avatar {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: 0 0 auto;
  overflow: hidden;
  border-radius: 50%;
  background: var(--el-color-primary-light-9);
  color: var(--el-color-primary);
}
.user-avatar svg {
  width: 62%;
  height: 62%;
}
</style>
