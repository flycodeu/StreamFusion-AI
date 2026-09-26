<script setup lang="ts">
import { computed, inject } from 'vue'
import { routeLocationKey } from 'vue-router'
import { ElMenu, ElMenuItem } from 'element-plus'
import { sessionState } from '../../session/state'
import { buildMenuRoutes } from '../../router/dynamic/routes'
import AppIcon from '../../components/icons/AppIcon.vue'
import SidebarMenu from './SidebarMenu.vue'

defineProps<{ collapsed: boolean }>()
const brandIcon = `${import.meta.env.BASE_URL}streamfusion.svg`
const route = inject(routeLocationKey, null)
const navigation = computed(
  () => buildMenuRoutes(sessionState.me?.routes ?? [], sessionState.me?.modules ?? []).navigation,
)
</script>

<template>
  <aside
    id="primary-sidebar"
    class="sidebar"
    :class="{ 'is-collapsed': collapsed }"
    aria-label="主导航"
  >
    <RouterLink class="brand" to="/home" aria-label="StreamFusion AI 首页">
      <img :src="brandIcon" alt="" width="30" height="30" />
      <span class="brand-name">StreamFusion AI</span>
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
  flex-shrink: 0;
  position: sticky;
  top: 0;
  height: 100dvh;
  display: flex;
  flex-direction: column;
  background: #fff;
  border-right: 1px solid var(--border-subtle);
}
.brand {
  height: var(--header-height);
  flex-shrink: 0;
  border-bottom: 1px solid var(--border-subtle);
  padding: 0 18px;
  display: flex;
  align-items: center;
  gap: 10px;
  overflow: hidden;
  white-space: nowrap;
}
.brand-name {
  color: #263445;
  font-size: 15px;
  font-weight: 600;
}
.navigation {
  padding: 12px 0;
  overflow-y: auto;
  overflow-x: hidden;
}
.navigation :deep(.el-menu) {
  border-right: 0;
  --el-menu-item-height: 46px;
  --el-menu-sub-item-height: 42px;
}
.navigation :deep(.app-icon) {
  margin-right: 12px;
}
.navigation :deep(.el-menu-item.is-active) {
  background: var(--el-color-primary-light-9);
  border-right: 3px solid var(--el-color-primary);
}
.navigation :deep(.el-sub-menu .el-menu-item) {
  font-size: 13px;
}
.is-collapsed {
  width: 64px;
}
.is-collapsed .brand {
  padding: 0 17px;
}
.is-collapsed .brand-name {
  display: none;
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
