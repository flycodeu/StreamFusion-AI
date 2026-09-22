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
      <a class="nav-item is-active" href="#main-content" aria-current="page" title="首页">
        <svg
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.6"
          aria-hidden="true"
        >
          <path d="m3 10 9-7 9 7v10a1 1 0 0 1-1 1h-5v-7H9v7H4a1 1 0 0 1-1-1Z" />
        </svg>
        <span class="nav-label">首页</span>
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
  --sidebar-width: 232px;
  width: var(--sidebar-width);
  flex-shrink: 0;
  position: sticky;
  top: 0;
  height: 100dvh;
  display: flex;
  flex-direction: column;
  background: #eef1ed;
  border-right: 1px solid #dce1d9;
  color: var(--text-secondary);
}
.brand {
  height: var(--header-height);
  flex-shrink: 0;
  border-bottom: 1px solid #dce1d9;
  padding: 0 22px;
  display: flex;
  align-items: center;
  gap: 10px;
  color: var(--text-primary);
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
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: 600;
}
.navigation {
  padding: 20px 14px;
}
.nav-item {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 44px;
  padding: 0 15px;
  border-radius: 24px;
  font-size: 13px;
  white-space: nowrap;
}
.nav-item.is-active {
  background: #e5e9e1;
  color: #344a39;
  font-weight: 500;
}
.nav-item:hover {
  background: #dce3d7;
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
  padding: 0;
  justify-content: center;
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
