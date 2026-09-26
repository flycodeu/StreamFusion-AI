<script setup lang="ts">
import { ref } from 'vue'
import AdminHeader from './components/AdminHeader.vue'
import AdminSidebar from './components/AdminSidebar.vue'
import AdminContent from './components/AdminContent.vue'

defineProps<{ displayName?: string }>()
const collapsed = ref(typeof globalThis.innerWidth === 'number' && globalThis.innerWidth <= 680)
</script>

<template>
  <div class="admin-layout">
    <a class="skip-link" href="#main-content">跳转到内容</a>
    <AdminSidebar :collapsed="collapsed" />
    <button
      v-if="!collapsed"
      class="sidebar-backdrop"
      aria-label="关闭侧栏"
      @click="collapsed = true"
    />
    <div class="workspace">
      <AdminHeader
        :collapsed="collapsed"
        :display-name="displayName"
        @toggle-sidebar="collapsed = !collapsed"
      />
      <AdminContent>
        <slot />
      </AdminContent>
    </div>
  </div>
</template>

<style scoped>
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
  --header-height: 56px;
  display: flex;
  min-height: 100dvh;
}
.workspace {
  flex: 1;
  min-width: 0;
  background: #f4f6f8;
}
.sidebar-backdrop {
  display: none;
}
@media (max-width: 680px) {
  .sidebar-backdrop {
    display: block;
    position: fixed;
    inset: 0 0 0 216px;
    z-index: 19;
    border: 0;
    background: #00000030;
  }
}
</style>
