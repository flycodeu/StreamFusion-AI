<script setup lang="ts">
import PreviewNodePanel from '../../features/preview/PreviewNodePanel.vue'

defineOptions({ name: 'PreviewServiceNodes' })
</script>

<template>
  <div class="content-page preview-nodes-page">
    <PreviewNodePanel management />
  </div>
</template>

<style scoped>
.preview-nodes-page {
  background: #f8fafc;
}
</style>
