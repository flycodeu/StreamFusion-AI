<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElButton, ElEmpty, vLoading } from 'element-plus'
import { getApiDocsStatus } from '../../api/api-docs/api'
import RequestError from '../../components/feedback/RequestError.vue'

defineOptions({ name: 'MonitorApiDocs' })
const enabled = ref(false)
const loading = ref(false)
const error = ref<unknown>(null)
const revision = ref(0)

async function load(): Promise<void> {
  if (loading.value) return
  loading.value = true
  error.value = null
  enabled.value = false
  try {
    enabled.value = (await getApiDocsStatus()).enabled
    revision.value++
  } catch (cause) {
    error.value = cause
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<template>
  <section v-loading="loading" class="content-page api-docs-page" aria-label="接口文档">
    <div class="api-docs-toolbar">
      <h1>接口文档</h1>
      <ElButton :loading="loading" @click="load">刷新</ElButton>
    </div>
    <RequestError :error="error" />
    <iframe
      v-if="enabled"
      :key="revision"
      class="api-docs-frame"
      src="/api-docs.html"
      title="Springdoc 接口文档"
      referrerpolicy="same-origin"
    />
    <ElEmpty v-else-if="!loading && !error" description="当前环境未启用接口文档" />
  </section>
</template>

<style scoped>
.api-docs-page {
  display: flex;
  flex-direction: column;
  min-height: calc(100dvh - var(--header-height) - 2 * var(--content-spacing));
}
.api-docs-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 14px;
}
.api-docs-toolbar h1 {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
}
.api-docs-frame {
  flex: 1;
  width: 100%;
  min-height: 640px;
  border: 1px solid var(--el-border-color-light);
  border-radius: 6px;
  background: white;
}
</style>
