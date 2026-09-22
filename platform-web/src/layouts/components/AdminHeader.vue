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
    <span class="breadcrumb"
      >工作台 <span aria-hidden="true">/</span> <strong>系统概览</strong></span
    >
    <div class="account" aria-label="当前用户">
      <span class="avatar" aria-hidden="true">
        <template v-if="displayName">{{ Array.from(displayName)[0] }}</template>
        <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6">
          <circle cx="12" cy="8" r="3" />
          <path d="M5 20v-2a7 7 0 0 1 14 0v2" />
        </svg>
      </span>
      <span>{{ displayName || '未登录' }}</span>
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
  height: 64px;
  padding: 0 30px;
  border-bottom: 1px solid #e4e9ed;
  background: #fff;
  display: flex;
  align-items: center;
  gap: 22px;
}
.icon-button {
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  padding: 4px;
  border: 0;
  background: transparent;
  border-radius: 5px;
  color: #697888;
  cursor: pointer;
}
.icon-button:hover {
  background: #edf3f5;
  color: #177a78;
}
.breadcrumb {
  font-size: 12px;
  color: #8b96a1;
  display: flex;
  gap: 16px;
}
.breadcrumb strong {
  color: #4b5967;
  font-weight: 500;
}
.account {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 13px;
  color: #526170;
}
.avatar {
  width: 32px;
  height: 32px;
  display: grid;
  place-items: center;
  border-radius: 50%;
  background: #eaf1f2;
  color: #567980;
  font-size: 13px;
}
.avatar svg {
  width: 20px;
  height: 20px;
}
@media (max-width: 680px) {
  .topbar {
    padding: 0 16px;
    gap: 10px;
  }
  .breadcrumb {
    gap: 8px;
  }
  .account {
    gap: 6px;
    font-size: 12px;
  }
}
</style>
