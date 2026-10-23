<script setup lang="ts">
import { computed, ref, defineAsyncComponent } from 'vue'
import { ElButton, ElDialog } from 'element-plus'
import { sessionState } from '../../session/state'
import { normalizedTraceId } from '../audit/presentation'

const props = defineProps<{
  traceId: string | null
  time?: string | null
  planId?: string | null
  buttonStyle?: 'link' | 'secondary'
}>()
const Diagnostics = defineAsyncComponent(() => import('../../views/monitor/Diagnostics.vue'))
const open = ref(false)
const context = computed(() => {
  const traceId = normalizedTraceId(props.traceId ?? '')
  const planId = props.planId && /^[1-9][0-9]{0,18}$/.test(props.planId) ? props.planId : null
  if ((!traceId && !planId) || !sessionState.me?.modules.includes('diagnostics')) return null
  return {
    ...(planId ? { planId } : { traceId: traceId! }),
    ...(props.time ? { time: props.time } : {}),
  }
})
</script>

<template>
  <template v-if="context">
    <ElButton
      :link="buttonStyle !== 'secondary'"
      :type="buttonStyle === 'secondary' ? 'default' : 'primary'"
      :size="buttonStyle === 'secondary' ? 'small' : 'default'"
      @click="open = true"
      >查看诊断日志</ElButton
    >
    <ElDialog
      v-if="open"
      v-model="open"
      title="关联诊断日志"
      width="min(1280px, calc(100vw - 32px))"
      append-to-body
      destroy-on-close
    >
      <Diagnostics :context="context" />
    </ElDialog>
  </template>
</template>
