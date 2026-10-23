<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElButton, ElDialog, ElIcon, ElSkeleton } from 'element-plus'
import { Document, Download, Link, Refresh } from '@element-plus/icons-vue'
import * as api from '../../api/storage/api'
import type { StorageInstance, StorageObject, StorageScope } from '../../api/storage/types'
import RequestError from '../../components/feedback/RequestError.vue'
import { ApiRequestError } from '../../lib/http/error'
import { bytes, time } from './display'

const props = defineProps<{
  instance: StorageInstance
  scope: StorageScope
  objectKey: string
  downloading: boolean
}>()
const emit = defineEmits<{
  close: []
  download: [file: StorageObject]
  copyLink: [file: StorageObject]
  showLink: [file: StorageObject]
}>()
const file = ref<StorageObject | null>(null),
  error = ref<unknown>(null),
  busy = ref(false),
  mediaFailed = ref(false)
let controller: InstanceType<typeof globalThis.AbortController> | undefined
const errorAdvice = computed(() =>
  error.value instanceof ApiRequestError && error.value.status === 404
    ? '文件可能已被删除或路径已变化。请关闭详情并刷新文件库确认。'
    : undefined,
)
const name = computed(() => file.value?.name || props.objectKey.split('/').at(-1) || '文件详情')
// Match the existing content endpoint's inline allowlist; never infer active content from a suffix.
const previewKind = computed(() => {
  const type = file.value?.contentType?.split(';', 1)[0]?.toLowerCase()
  if (['image/jpeg', 'image/png', 'image/webp', 'image/gif'].includes(type ?? '')) return 'image'
  if (['video/mp4', 'video/webm'].includes(type ?? '')) return 'video'
  return null
})
const contentUrl = computed(() => {
  const value = file.value
  return value?.objectToken && previewKind.value
    ? api.objectContentUrl(props.scope.scopeId, value.objectKey, value.objectToken)
    : ''
})
const exactBytes = computed(() => {
  const size = file.value?.sizeBytes
  return size && /^\d+$/.test(size) ? `${BigInt(size).toLocaleString('zh-CN')} 字节` : '不可获取'
})
async function load() {
  controller?.abort()
  const request = new globalThis.AbortController()
  controller = request
  file.value = null
  error.value = null
  mediaFailed.value = false
  busy.value = true
  try {
    const value = await api.getObject(props.scope.scopeId, props.objectKey, request.signal)
    if (!request.signal.aborted) file.value = value
  } catch (e) {
    if (!request.signal.aborted) error.value = e
  } finally {
    if (!request.signal.aborted) busy.value = false
  }
}
watch(
  () =>
    `${props.instance.instanceId}:${props.instance.version}:${props.scope.scopeId}:${props.scope.version}:${props.objectKey}`,
  () => void load(),
  { immediate: true },
)
onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <ElDialog
    :model-value="true"
    title="文件详情"
    width="min(1160px,96vw)"
    top="5vh"
    class="file-details-dialog"
    :close-on-click-modal="false"
    @update:model-value="emit('close')"
  >
    <template #header="{ titleId }">
      <div class="detail-heading">
        <ElIcon :size="22"><Document /></ElIcon>
        <div>
          <h2 :id="titleId" :title="name">{{ name }}</h2>
          <span>文件详情</span>
        </div>
      </div>
    </template>
    <ElSkeleton v-if="busy" :rows="8" animated class="detail-loading" />
    <div v-else-if="error" class="detail-error">
      <RequestError :error="error" :advice="errorAdvice" />
      <ElButton :icon="Refresh" @click="load">重新读取文件信息</ElButton>
    </div>
    <div v-else-if="file" class="detail-layout">
      <section class="preview-stage" aria-label="文件内容预览">
        <img
          v-if="previewKind === 'image' && contentUrl && !mediaFailed"
          :key="contentUrl"
          :src="contentUrl"
          :alt="file.name"
          class="detail-media"
          @error="mediaFailed = true"
        />
        <video
          v-else-if="previewKind === 'video' && contentUrl && !mediaFailed"
          :key="contentUrl"
          :src="contentUrl"
          :aria-label="file.name"
          controls
          preload="metadata"
          class="detail-media"
          @error="mediaFailed = true"
        />
        <div v-else class="file-placeholder">
          <ElIcon :size="56"><Document /></ElIcon>
          <strong>{{ mediaFailed ? '预览未能加载' : '此文件可下载查看' }}</strong>
          <span v-if="mediaFailed">可重新读取文件，或下载后查看。</span>
          <span v-else>{{ file.contentType || '未提供文件类型' }}</span>
          <ElButton v-if="mediaFailed" :icon="Refresh" @click="load">重新加载</ElButton>
          <ElButton :icon="Download" :loading="downloading" @click="emit('download', file)">
            下载文件
          </ElButton>
        </div>
      </section>
      <aside class="file-information" aria-label="文件信息">
        <h3>文件信息</h3>
        <dl>
          <dt>文件名称</dt>
          <dd>{{ file.name }}</dd>
          <dt>文件类型</dt>
          <dd>{{ file.contentType || '未提供' }}</dd>
          <dt>文件大小</dt>
          <dd>
            {{ bytes(file.sizeBytes) }}<small>{{ exactBytes }}</small>
          </dd>
          <dt>最后修改</dt>
          <dd>{{ time(file.lastModified) }}</dd>
          <dt>图床服务器</dt>
          <dd>{{ instance.name }}</dd>
          <dt>存储桶</dt>
          <dd>{{ scope.bucket }}</dd>
          <dt>存储路径</dt>
          <dd class="object-path">{{ file.objectKey }}</dd>
        </dl>
        <details class="technical-details">
          <summary>更多信息</summary>
          <dl>
            <dt>ETag</dt>
            <dd>{{ file.etag || '未提供' }}</dd>
            <template v-if="file.objectVersionId"
              ><dt>对象版本</dt>
              <dd>{{ file.objectVersionId }}</dd></template
            >
          </dl>
        </details>
      </aside>
    </div>
    <template #footer>
      <div class="detail-actions">
        <ElButton @click="emit('close')">关闭</ElButton>
        <div v-if="file" class="file-actions">
          <ElButton :icon="Link" @click="emit('showLink', file)">查看链接</ElButton>
          <ElButton @click="emit('copyLink', file)">复制预览链接</ElButton>
          <ElButton
            type="primary"
            :icon="Download"
            :loading="downloading"
            @click="emit('download', file)"
            >下载文件</ElButton
          >
        </div>
      </div>
    </template>
  </ElDialog>
</template>

<style scoped>
:global(.file-details-dialog .el-dialog__body) {
  max-height: calc(90vh - 170px);
  overflow-y: auto;
}
.detail-heading {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
  padding-right: 30px;
}
.detail-heading > .el-icon {
  flex-shrink: 0;
  color: var(--el-color-primary);
}
.detail-heading h2 {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  margin: 0 0 5px;
  font-size: 17px;
  font-weight: 500;
  overflow-wrap: anywhere;
  line-height: 1.5;
}
.detail-heading span {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.detail-layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 310px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 7px;
  overflow: hidden;
}
.preview-stage {
  display: flex;
  align-items: center;
  justify-content: center;
  min-width: 0;
  height: clamp(320px, 58vh, 650px);
  padding: 24px;
  background: var(--el-fill-color-extra-light);
}
.detail-media {
  display: block;
  width: 100%;
  height: 100%;
  min-width: 0;
  object-fit: contain;
  object-position: center;
}
.file-placeholder {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 14px;
  flex-direction: column;
  text-align: center;
  overflow-wrap: anywhere;
  max-width: 100%;
}
.file-placeholder > .el-icon {
  color: var(--el-text-color-placeholder);
  margin-bottom: 6px;
}
.file-placeholder strong {
  font-size: 15px;
  font-weight: 500;
}
.file-placeholder span {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.file-placeholder :deep(.el-button + .el-button) {
  margin-left: 0;
}
.file-information {
  padding: 20px;
  min-width: 0;
  max-height: clamp(320px, 58vh, 650px);
  overflow-y: auto;
  border-left: 1px solid var(--el-border-color-lighter);
}
.file-information h3 {
  margin: 0 0 18px;
  font-size: 14px;
  font-weight: 600;
}
dl {
  margin: 0;
}
dt {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin: 12px 0 4px;
}
dt:first-child {
  margin-top: 0;
}
dd {
  margin: 0;
  font-size: 13px;
  line-height: 1.65;
  overflow-wrap: anywhere;
  user-select: text;
}
dd small {
  display: block;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.object-path {
  font-family: var(--el-font-family);
}
.technical-details {
  border-top: 1px solid var(--el-border-color-lighter);
  margin-top: 22px;
  padding-top: 16px;
}
.technical-details summary {
  cursor: pointer;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.technical-details dl {
  margin-top: 16px;
}
.detail-loading,
.detail-error {
  padding: 24px 0;
}
.detail-error > .el-button {
  margin-top: 16px;
}
.detail-actions,
.file-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.detail-actions {
  justify-content: space-between;
}
.file-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}
@media (max-width: 760px) {
  .detail-layout {
    grid-template-columns: minmax(0, 1fr);
  }
  .preview-stage {
    height: clamp(200px, 35vh, 360px);
    padding: 16px;
  }
  .file-information {
    border-left: 0;
    border-top: 1px solid var(--el-border-color-lighter);
    max-height: none;
    padding: 20px;
  }
  .file-information h3 {
    margin-bottom: 20px;
  }
  .detail-actions > .el-button {
    display: none;
  }
  .file-actions {
    justify-content: flex-end;
    width: 100%;
  }
}
</style>
