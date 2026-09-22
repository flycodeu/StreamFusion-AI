<script setup lang="ts">
defineProps<{ collapsed: boolean }>()
const brandIcon = `${import.meta.env.BASE_URL}streamfusion.svg`
</script>

<template>
  <aside
    id="primary-sidebar"
    class="sidebar"
    :class="{ 'is-collapsed': collapsed }"
    aria-label="主导航"
  >
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
</template>

<style scoped>
svg {
  width: 22px;
  height: 22px;
  flex-shrink: 0;
}
.sidebar {
  --sidebar-width: 224px;
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
  .sidebar {
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
  .sidebar.is-collapsed {
    display: none;
  }
}
</style>
