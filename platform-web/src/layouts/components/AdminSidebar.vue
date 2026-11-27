<script setup lang="ts">
import { computed, inject } from 'vue'
import { routeLocationKey } from 'vue-router'
import { ElMenu, ElMenuItem } from 'element-plus'
import { sessionState } from '../../session/state'
import { buildMenuRoutes } from '../../router/dynamic/routes'
import AppIcon from '../../components/icons/AppIcon.vue'
import SidebarMenu from './SidebarMenu.vue'
import { systemConfig } from '../../config/system'

defineProps<{ collapsed: boolean }>()
const route = inject(routeLocationKey, null)
const navigation = computed(
  () =>
    buildMenuRoutes(sessionState.me?.routes ?? [], sessionState.me?.modules ?? [], {
      isSuperAdmin: sessionState.me?.isSuperAdmin ?? false,
    }).navigation,
)
</script>

<template>
  <aside
    id="primary-sidebar"
    class="sidebar"
    :class="{ 'is-collapsed': collapsed }"
    aria-label="主导航"
  >
    <RouterLink class="brand" to="/home" :aria-label="`${systemConfig.name} 首页`">
      <img :src="systemConfig.logo" alt="" width="30" height="30" />
      <span class="brand-name">{{ systemConfig.name }}</span>
    </RouterLink>
    <nav aria-label="功能菜单" class="navigation">
      <ElMenu
        :default-active="route?.path"
        :collapse="collapsed"
        :collapse-transition="false"
        router
        unique-opened
      >
        <ElMenuItem index="/home"
          ><AppIcon name="home" /><template #title><span>首页</span></template></ElMenuItem
        >
        <SidebarMenu :nodes="navigation" />
      </ElMenu>
    </nav>
  </aside>
</template>

<style scoped>
.sidebar {
  width: 216px;
  transition: width 0.2s ease;
  flex-shrink: 0;
  position: sticky;
  top: 0;
  height: 100dvh;
  display: flex;
  flex-direction: column;
  background: var(--sf-surface);
  border-right: 1px solid var(--sf-border);
}
.brand {
  height: var(--header-height);
  flex-shrink: 0;
  border-bottom: 1px solid var(--sf-border);
  padding: 0 18px;
  display: flex;
  align-items: center;
  gap: 10px;
  overflow: hidden;
  white-space: nowrap;
}
.brand-name {
  transition: opacity 0.15s ease;
  color: var(--sf-text-primary);
  font-size: var(--sf-fs-section);
  font-weight: 600;
  letter-spacing: -0.01em;
}
.navigation {
  padding: 10px 0;
  overflow-y: auto;
  overflow-x: hidden;
}
.navigation :deep(.el-menu) {
  border-right: 0;
  --el-menu-item-height: 44px;
  --el-menu-sub-item-height: 40px;
  --el-menu-text-color: var(--sf-text-regular);
  --el-menu-hover-bg-color: var(--sf-surface-subtle);
  --el-menu-active-color: var(--sf-primary);
}
.navigation :deep(.app-icon) {
  margin-right: 12px;
}
.navigation :deep(.el-menu-item.is-active) {
  background: var(--sf-primary-soft);
  border-right: 3px solid var(--sf-primary);
  font-weight: 600;
}
.navigation :deep(.el-sub-menu .el-menu-item) {
  font-size: var(--sf-fs-sub);
}
.is-collapsed {
  width: 64px;
}
.is-collapsed .brand {
  padding: 0 17px;
}
.is-collapsed .brand-name {
  opacity: 0;
  visibility: hidden;
}
.brand img {
  flex-shrink: 0;
}
@media (prefers-reduced-motion: reduce) {
  .sidebar,
  .brand-name {
    transition: none;
  }
}
.is-collapsed .navigation :deep(.app-icon) {
  margin-right: 0;
}
@media (max-width: 680px) {
  .sidebar {
    position: fixed;
    z-index: 20;
    box-shadow: 3px 0 12px #00000012;
  }
  .is-collapsed {
    display: none;
  }
}
</style>
