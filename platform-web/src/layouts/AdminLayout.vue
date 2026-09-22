<script setup lang="ts">
import { ref } from 'vue'
import AdminHeader from './components/AdminHeader.vue'
import AdminSidebar from './components/AdminSidebar.vue'
import AdminContent from './components/AdminContent.vue'

defineProps<{ displayName?: string }>()
const collapsed = ref(false)
</script>

<template>
  <div class="admin-layout">
    <a class="skip-link" href="#main-content">跳转到内容</a>
    <AdminSidebar :collapsed="collapsed" />
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
  background: #fafbf9;
}
</style>
