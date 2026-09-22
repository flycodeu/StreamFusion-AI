<script setup lang="ts">
import { ref } from 'vue'
import { ElTabs, ElTabPane } from 'element-plus'
import 'element-plus/es/components/tabs/style/css'
import 'element-plus/es/components/tab-pane/style/css'
import AdminLayout from './layouts/AdminLayout.vue'
import HomeView from './views/HomeView.vue'

const activePage = ref('home')
</script>

<template>
  <AdminLayout>
    <ElTabs v-model="activePage" class="page-tabs" aria-label="页面标签">
      <ElTabPane label="首页" name="home">
        <HomeView />
      </ElTabPane>
    </ElTabs>
  </AdminLayout>
</template>

<style scoped>
.page-tabs :deep(.el-tabs__header) {
  margin: 0;
  padding: 0 var(--content-spacing);
  background: #fff;
  border-bottom: 1px solid var(--border-subtle);
}
.page-tabs :deep(.el-tabs__nav-wrap::after) {
  display: none;
}
.page-tabs :deep(.el-tabs__item) {
  min-width: 64px;
  height: 44px;
  font-size: 14px;
}
.page-tabs :deep(.el-tabs__content) {
  padding: var(--content-spacing);
}
</style>
