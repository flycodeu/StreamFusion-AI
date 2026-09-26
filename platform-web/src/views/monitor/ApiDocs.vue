<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { ElButton, ElEmpty, vLoading } from 'element-plus'
import { apiDocsFrameUrl, getApiDocsStatus, getOpenApiDocument } from '../../api/api-docs/api'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'

defineOptions({ name: 'MonitorApiDocs' })
const enabled = ref(false)
const loading = ref(false)
const exporting = ref(false)
const error = ref<unknown>(null)
const revision = ref(0)
const frameUrl = apiDocsFrameUrl()
const captureScope = usePageScope()
let controller: InstanceType<typeof globalThis.AbortController> | null = null

onBeforeUnmount(() => controller?.abort())

async function load(): Promise<void> {
  if (loading.value || exporting.value) return
  const current = captureScope()
  controller = new globalThis.AbortController()
  loading.value = true
  error.value = null
  enabled.value = false
  try {
    const result = await getApiDocsStatus(controller.signal)
    if (!current()) return
    enabled.value = result.enabled
    revision.value++
  } catch (cause) {
    if (current()) error.value = cause
  } finally {
    if (current()) loading.value = false
  }
}

async function exportDocument(): Promise<void> {
  if (!enabled.value || loading.value || exporting.value) return
  const current = captureScope()
  controller = new globalThis.AbortController()
  exporting.value = true
  error.value = null
  try {
    const document = await getOpenApiDocument(controller.signal)
    if (!current()) return
    const url = globalThis.URL.createObjectURL(
      new globalThis.Blob([document], { type: 'application/json;charset=utf-8' }),
    )
    const link = globalThis.document.createElement('a')
    try {
      link.href = url
      link.download = 'streamfusion-openapi.json'
      globalThis.document.body.append(link)
      link.click()
    } finally {
      link.remove()
      globalThis.setTimeout(() => globalThis.URL.revokeObjectURL(url), 0)
    }
  } catch (cause) {
    if (current()) error.value = cause
  } finally {
    if (current()) exporting.value = false
  }
}

onMounted(load)
</script>

<template>
  <section v-loading="loading" class="content-page api-docs-page" aria-label="接口文档">
    <div class="api-docs-toolbar">
      <ElButton :disabled="loading || !enabled" :loading="exporting" @click="exportDocument">
        导出 OpenAPI JSON
      </ElButton>
      <ElButton :disabled="exporting" :loading="loading" @click="load">刷新</ElButton>
    </div>
    <RequestError :error="error" />
    <iframe
      v-if="enabled"
      :key="revision"
      class="api-docs-frame"
      :src="frameUrl"
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
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 10px;
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
