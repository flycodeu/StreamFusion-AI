<script setup lang="ts">
import { ref } from 'vue'

defineProps<{ displayName?: string }>()
const collapsed = ref(false)
const brandIcon = `${import.meta.env.BASE_URL}streamfusion.svg`
</script>

<template>
  <div class="admin-layout" :class="{ 'is-collapsed': collapsed }">
    <a class="skip-link" href="#main-content">跳转到内容</a>
    <aside id="primary-sidebar" class="sidebar" aria-label="主导航">
      <a class="brand" href="#main-content" aria-label="StreamFusion AI 首页">
        <img class="brand-mark" :src="brandIcon" alt="" width="34" height="34" />
        <span class="brand-name">StreamFusion <span class="brand-ai">AI</span></span>
      </a>
      <nav class="navigation" aria-label="功能菜单">
        <a class="nav-item is-active" href="#main-content" aria-current="page" title="系统概览">
          <svg
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.6"
            aria-hidden="true"
          >
            <rect x="3" y="3" width="7" height="7" rx="1.5" />
            <rect x="14" y="3" width="7" height="7" rx="1.5" />
            <rect x="3" y="14" width="7" height="7" rx="1.5" />
            <rect x="14" y="14" width="7" height="7" rx="1.5" />
          </svg>
          <span class="nav-label">系统概览</span>
        </a>
      </nav>
    </aside>
    <div class="workspace">
      <header class="topbar">
        <button
          class="icon-button"
          type="button"
          :aria-label="collapsed ? '展开菜单' : '收起菜单'"
          :aria-expanded="!collapsed"
          aria-controls="primary-sidebar"
          @click="collapsed = !collapsed"
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
      <main id="main-content" class="main-content" tabindex="-1">
        <slot />
      </main>
    </div>
  </div>
</template>

<style scoped>
svg {
  width: 22px;
  height: 22px;
  flex-shrink: 0;
}
.skip-link {
  position: fixed;
  top: -64px;
  left: 16px;
  z-index: 10;
  padding: 12px;
  background: white;
}
.skip-link:focus {
  top: 8px;
}
.admin-layout {
  --sidebar-width: 224px;
  display: flex;
  min-height: 100dvh;
}
.sidebar {
  width: var(--sidebar-width);
  flex-shrink: 0;
  position: sticky;
  top: 0;
  height: 100dvh;
  display: flex;
  flex-direction: column;
  background: #edf3f2;
  color: #526b69;
  border-right: 1px solid #dce6e3;
}
.brand {
  height: 76px;
  padding: 0 18px;
  display: flex;
  align-items: center;
  gap: 10px;
  color: #254a47;
  white-space: nowrap;
}
.brand-mark {
  display: block;
  width: 34px;
  height: 34px;
  flex-shrink: 0;
}
.brand-name {
  font-size: 15px;
  font-weight: 600;
  letter-spacing: -0.3px;
}
.brand-ai {
  color: #177a78;
  font-size: 12px;
  font-weight: 600;
}
.navigation {
  padding: 22px 12px;
}
.nav-item {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 44px;
  padding: 0 15px;
  border-radius: 6px;
  font-size: 13px;
  white-space: nowrap;
}
.nav-item.is-active {
  background: #d8e9e4;
  color: #1f6259;
  box-shadow: inset 3px 0 #398a78;
}
.nav-item:hover {
  background: #cfe3dc;
}
.workspace {
  flex: 1;
  min-width: 0;
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
.main-content {
  padding: 36px;
}
.is-collapsed {
  --sidebar-width: 76px;
}
.is-collapsed .brand {
  padding: 0 21px;
}
.is-collapsed .brand-name,
.is-collapsed .nav-label {
  display: none;
}
.is-collapsed .nav-item {
  padding: 0 15px;
}
@media (max-width: 680px) {
  .admin-layout {
    --sidebar-width: 64px;
  }
  .brand,
  .is-collapsed .brand {
    padding: 0 15px;
  }
  .brand-name,
  .nav-label {
    display: none;
  }
  .navigation {
    padding: 22px 8px;
  }
  .nav-item {
    padding: 0 13px;
  }
  .is-collapsed .sidebar {
    display: none;
  }
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
  .main-content {
    padding: 26px 18px;
  }
}
</style>
