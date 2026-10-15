<script setup lang="ts">
import { computed, inject } from 'vue'
import { routerKey, RouterLink } from 'vue-router'
import { sessionState } from '../../session/state'
import { normalizedTraceId } from '../audit/presentation'

const props = defineProps<{
  traceId: string | null
  time?: string | null
  planId?: string | null
}>()
const router = inject(routerKey, null)
const target = computed(() => {
  const traceId = normalizedTraceId(props.traceId ?? '')
  const planId = props.planId && /^[1-9][0-9]{0,18}$/.test(props.planId) ? props.planId : null
  if (!router || (!traceId && !planId) || !sessionState.me?.modules.includes('diagnostics'))
    return null
  if (router.currentRoute.value.meta.componentPath === '/monitor/Diagnostics') return null
  const route = router
    .getRoutes()
    .find(
      (item) =>
        item.meta.moduleKey === 'diagnostics' && item.meta.componentPath === '/monitor/Diagnostics',
    )
  if (!route) return null
  return {
    path: route.path,
    query: { ...(planId ? { planId } : { traceId }), ...(props.time ? { time: props.time } : {}) },
  }
})
</script>

<template>
  <RouterLink v-if="target" class="diagnostic-link" :to="target">查看诊断日志</RouterLink>
</template>

<style scoped>
.diagnostic-link {
  color: var(--el-color-primary);
  font-size: 13px;
  text-decoration: underline;
  text-underline-offset: 3px;
  align-self: center;
}
</style>
