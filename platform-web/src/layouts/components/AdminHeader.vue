<script setup lang="ts">
import { computed, inject, ref } from 'vue'
import { routeLocationKey, useRouter } from 'vue-router'
import {
  ElBreadcrumb,
  ElBreadcrumbItem,
  ElDropdown,
  ElDropdownItem,
  ElDropdownMenu,
  ElMessage,
} from 'element-plus'
import { signOut } from '../../session/session'
import { sessionState } from '../../session/state'
import UserAvatar from '../../components/avatar/UserAvatar.vue'

defineProps<{ collapsed: boolean; displayName?: string }>()
const emit = defineEmits<{ 'toggle-sidebar': [] }>()
const route = inject(routeLocationKey, null)
const router = useRouter()
const breadcrumbs = computed(() => {
  const items = route?.meta.breadcrumbs
  return Array.isArray(items) ? items.map(String) : [String(route?.meta.title || '首页')]
})
const busy = ref(false)
async function command(value: string): Promise<void> {
  if (value !== 'logout') {
    await router.push(value)
    return
  }
  if (busy.value) return
  busy.value = true
  try {
    await signOut()
    await router.replace('/login')
  } catch {
    ElMessage.error('退出未完成，请重试')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <header class="topbar">
    <button
      class="icon-button"
      type="button"
      :aria-label="collapsed ? '展开菜单' : '收起菜单'"
      :aria-expanded="!collapsed"
      aria-controls="primary-sidebar"
      @click="emit('toggle-sidebar')"
    >
      <svg
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="1.6"
        aria-hidden="true"
      >
        <rect x="3" y="4" width="18" height="16" rx="2" />
        <path d="M9 4v16M13 9h4M13 13h4" />
      </svg>
    </button>
    <ElBreadcrumb class="breadcrumbs" separator="/">
      <ElBreadcrumbItem v-for="(item, index) in breadcrumbs" :key="index">{{
        item
      }}</ElBreadcrumbItem>
    </ElBreadcrumb>
    <ElDropdown class="account" trigger="click" @command="command">
      <button class="account-trigger" type="button" aria-label="账户菜单">
        <UserAvatar
          :avatar-key="sessionState.me?.user.avatarKey"
          :name="displayName || '访'"
          :size="32"
        />
        <span class="account-name" :title="displayName || '未登录'">{{
          displayName || '未登录'
        }}</span>
        <span aria-hidden="true">⌄</span>
      </button>
      <template #dropdown
        ><ElDropdownMenu>
          <ElDropdownItem command="/profile">个人信息</ElDropdownItem>
          <ElDropdownItem command="/profile?tab=password">修改密码</ElDropdownItem>
          <ElDropdownItem command="logout" divided :disabled="busy">退出登录</ElDropdownItem>
        </ElDropdownMenu></template
      >
    </ElDropdown>
  </header>
</template>

<style scoped>
.topbar {
  height: var(--header-height);
  padding: 0 20px;
  background: #fff;
  border-bottom: 1px solid var(--border-subtle);
  display: flex;
  align-items: center;
  gap: 16px;
}
.icon-button {
  display: grid;
  place-items: center;
  width: 32px;
  height: 36px;
  flex-shrink: 0;
  padding: 4px;
  border: 0;
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
}
.icon-button:hover {
  background: var(--surface-muted);
}
.icon-button svg {
  width: 22px;
  height: 22px;
}
.breadcrumbs {
  flex: 1;
  min-width: 0;
  line-height: 1.5;
}
.account {
  margin-left: auto;
  max-width: 240px;
  min-width: 0;
}
.account-trigger {
  display: flex;
  align-items: center;
  gap: 10px;
  border: 0;
  padding: 0;
  background: transparent;
  cursor: pointer;
  color: var(--text-secondary);
  font-size: 14px;
  min-width: 0;
}
.account-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
@media (max-width: 680px) {
  .topbar {
    padding: 0 12px;
    gap: 8px;
  }
  .account-name {
    max-width: 80px;
  }
  .breadcrumbs {
    font-size: 12px;
  }
}
</style>
