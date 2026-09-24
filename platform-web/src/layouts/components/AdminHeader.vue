<script setup lang="ts">
defineProps<{ collapsed: boolean; displayName?: string }>()
const emit = defineEmits<{ 'toggle-sidebar': [] }>()
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
    <span class="topbar-title">平台管理</span>
    <div class="account" aria-label="当前用户">
      <span class="avatar" aria-hidden="true">
        <template v-if="displayName">{{ Array.from(displayName)[0] }}</template>
        <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6">
          <circle cx="12" cy="8" r="3" />
          <path d="M5 20v-2a7 7 0 0 1 14 0v2" />
        </svg>
      </span>
      <span class="account-name" :title="displayName || '未登录'">{{
        displayName || '未登录'
      }}</span>
    </div>
  </header>
</template>

<style scoped>
svg {
  width: 22px;
  height: 22px;
  flex-shrink: 0;
}
.topbar {
  height: var(--header-height);
  padding: 6px 20px;
  background: #fff;
  border-bottom: 1px solid var(--border-subtle);
  display: flex;
  align-items: center;
  gap: 16px;
}
.topbar-title {
  color: var(--text-primary);
  font-size: 14px;
  font-weight: 600;
}
.icon-button {
  display: grid;
  place-items: center;
  width: 40px;
  height: 40px;
  flex-shrink: 0;
  padding: 8px;
  border: 0;
  background: transparent;
  border-radius: 6px;
  color: var(--text-secondary);
  cursor: pointer;
}
.icon-button:hover {
  background: var(--surface-muted);
  color: var(--text-primary);
}
.account {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 13px;
  color: var(--text-secondary);
  min-width: 0;
  max-width: min(50%, 240px);
}
.account-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.avatar {
  width: 32px;
  height: 32px;
  flex-shrink: 0;
  display: grid;
  place-items: center;
  border-radius: 6px;
  background: #e9efec;
  color: #48665e;
  font-size: 13px;
}
.avatar svg {
  width: 20px;
  height: 20px;
}
@media (max-width: 680px) {
  .topbar {
    padding: 0 12px;
    gap: 10px;
  }
  .account {
    gap: 6px;
    font-size: 12px;
  }
}
</style>
