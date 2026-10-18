<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { ElOption, ElSelect } from 'element-plus'
import type { PreviewChannel, PreviewProfile } from '../../api/preview/types'
import { usageLabels } from './model'
import type { PreviewMenuAction } from './model'

const props = defineProps<{
  position: { x: number; y: number }
  channels: PreviewChannel[]
  profiles: PreviewProfile[]
  cameraId: string
  profileId?: string
  playing: boolean
  failed: boolean
}>()
const emit = defineEmits<{
  close: []
  action: [action: PreviewMenuAction]
  channel: [cameraId: string]
  profile: [profileId: string]
}>()
const root = ref<globalThis.HTMLElement | null>(null)
const position = ref(props.position)
function focusable(): globalThis.HTMLElement[] {
  return Array.from(
    root.value?.querySelectorAll<globalThis.HTMLElement>(
      'button:not(:disabled), input:not(:disabled), [role="combobox"]',
    ) ?? [],
  ).filter(
    (element) => element.getAttribute('tabindex') !== '-1' && element.getClientRects().length > 0,
  )
}
watch(
  () => props.position,
  async (value) => {
    position.value = value
    await nextTick()
    if (!root.value) return
    const box = root.value.getBoundingClientRect()
    position.value = {
      x: Math.max(8, Math.min(value.x, globalThis.innerWidth - box.width - 8)),
      y: Math.max(8, Math.min(value.y, globalThis.innerHeight - box.height - 8)),
    }
    focusable()[0]?.focus()
  },
  { immediate: true, flush: 'post' },
)
function keydown(event: globalThis.KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    event.stopPropagation()
    emit('close')
  } else if (event.key === 'Tab') {
    const elements = focusable()
    const first = elements[0],
      last = elements[elements.length - 1]
    if (event.shiftKey && globalThis.document.activeElement === first) {
      event.preventDefault()
      last?.focus()
    } else if (!event.shiftKey && globalThis.document.activeElement === last) {
      event.preventDefault()
      first?.focus()
    }
  }
}
</script>

<template>
  <button
    class="context-backdrop"
    type="button"
    tabindex="-1"
    aria-label="关闭画面操作"
    @click="emit('close')"
    @contextmenu.prevent="emit('close')"
  />
  <div
    id="preview-slot-menu"
    ref="root"
    class="preview-context"
    :style="{ left: `${position.x}px`, top: `${position.y}px` }"
    role="dialog"
    aria-label="画面操作"
    @keydown="keydown"
  >
    <div v-if="channels.length > 1" class="context-section">
      <span>通道</span>
      <ElSelect
        :model-value="cameraId"
        size="small"
        aria-label="切换通道"
        @change="emit('channel', $event)"
      >
        <ElOption
          v-for="channel in channels"
          :key="channel.cameraId"
          :value="channel.cameraId"
          :label="channel.name"
          :disabled="channel.lifecycle !== 'ENABLED'"
        />
      </ElSelect>
    </div>
    <div v-if="profiles.length" class="context-section">
      <span>码流</span>
      <ElSelect
        :model-value="profileId"
        size="small"
        aria-label="切换码流"
        @change="emit('profile', $event)"
      >
        <ElOption
          v-for="profile in profiles"
          :key="profile.streamProfileId"
          :value="profile.streamProfileId"
          :label="`${usageLabels[profile.usage]} · ${profile.label}`"
        />
      </ElSelect>
    </div>
    <button type="button" :disabled="!playing" @click="emit('action', 'snapshot')">抓图</button>
    <button type="button" @click="emit('action', 'maximize')">大屏</button>
    <button type="button" @click="emit('action', 'reconnect')">重新连接</button>
    <button v-if="failed" type="button" @click="emit('action', 'diagnostics')">排查信息</button>
    <button type="button" @click="emit('action', 'close')">关闭预览</button>
  </div>
</template>

<style scoped>
.context-backdrop {
  position: fixed;
  inset: 0;
  z-index: 29;
  border: 0;
  background: transparent;
  cursor: default;
}
.preview-context {
  position: fixed;
  z-index: 30;
  width: 224px;
  max-width: calc(100vw - 16px);
  max-height: calc(100dvh - 16px);
  overflow: auto;
  padding: 6px;
  border: 1px solid var(--preview-line);
  border-radius: 6px;
  background: #fff;
  box-shadow: 0 10px 28px #273b5224;
}
.preview-context > button {
  display: block;
  width: 100%;
  padding: 9px 11px;
  border: 0;
  border-radius: 3px;
  background: transparent;
  color: var(--preview-ink);
  text-align: left;
  font-size: 13px;
  cursor: pointer;
}
.preview-context > button:hover,
.preview-context > button:focus-visible {
  background: var(--preview-hover);
}
.preview-context > button:focus-visible {
  outline: 2px solid var(--preview-accent);
  outline-offset: -2px;
}
.preview-context > button:disabled {
  opacity: 0.4;
  cursor: default;
}
.context-section {
  display: grid;
  gap: 6px;
  padding: 8px 10px;
}
.context-section > span {
  color: var(--preview-muted);
  font-size: 12px;
}
</style>
