<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { ElIcon } from 'element-plus'
import { Picture, VideoPlay, Document } from '@element-plus/icons-vue'
import * as api from '../../api/storage/api'
import type { StorageObject } from '../../api/storage/types'
import { sameObject, thumbnailCandidate, type ThumbnailQueue } from './file-browser'

const props = defineProps<{ file: StorageObject; scopeId: string; queue: ThumbnailQueue }>()
const host = ref<globalThis.HTMLElement>(),
  url = ref(''),
  ready = ref(false)
let observer: globalThis.IntersectionObserver | undefined, cancel: (() => void) | undefined
let complete: (() => void) | undefined
function start() {
  if (cancel || !thumbnailCandidate(props.file)) return
  observer?.disconnect()
  cancel = props.queue.add(async (signal) => {
    if (signal.aborted) return
    const item = await api.getObject(props.scopeId, props.file.objectKey, signal)
    if (
      signal.aborted ||
      !sameObject(item, props.file) ||
      !thumbnailCandidate(item) ||
      !item.objectToken
    )
      return
    await new Promise<void>((resolve) => {
      const stop = () => {
        url.value = ''
        finish()
      }
      const timer = globalThis.setTimeout(stop, 15000)
      function finish() {
        globalThis.clearTimeout(timer)
        signal.removeEventListener('abort', stop)
        complete = undefined
        resolve()
      }
      complete = finish
      signal.addEventListener('abort', stop, { once: true })
      if (signal.aborted) stop()
      else url.value = api.objectContentUrl(props.scopeId, item.objectKey, item.objectToken!)
    })
  })
}
function loaded() {
  ready.value = true
  complete?.()
}
function failed() {
  url.value = ''
  complete?.()
}
onMounted(() => {
  if (typeof globalThis.IntersectionObserver === 'undefined') start()
  else {
    observer = new globalThis.IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) start()
      },
      { rootMargin: '100px' },
    )
    if (host.value) observer.observe(host.value)
  }
})
onBeforeUnmount(() => {
  observer?.disconnect()
  cancel?.()
  complete?.()
  url.value = ''
})
</script>

<template>
  <span ref="host" class="thumbnail">
    <img
      v-if="url"
      :src="url"
      :alt="file.name"
      decoding="async"
      :class="{ ready }"
      @load="loaded"
      @error="failed"
    />
    <ElIcon v-if="!ready || !url" :size="36"
      ><VideoPlay v-if="/\.(mp4|webm)$/i.test(file.name)" /><Picture
        v-else-if="/\.(png|jpg|jpeg|webp|gif)$/i.test(file.name)" /><Document v-else
    /></ElIcon>
  </span>
</template>

<style scoped>
.thumbnail {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  height: 142px;
  background: var(--el-fill-color-light);
  color: var(--el-text-color-placeholder);
}
img {
  position: absolute;
  inset: 12px;
  width: calc(100% - 24px);
  height: calc(100% - 24px);
  object-fit: contain;
  object-position: center;
  opacity: 0;
}
img.ready {
  opacity: 1;
}
</style>
