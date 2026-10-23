<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElBadge, ElButton, ElDrawer, ElNotification } from 'element-plus'
import type { NotificationHandle } from 'element-plus'
import { Bell } from '@element-plus/icons-vue'
import { useSecurityAlerts } from './alertMonitor'
import { securityDegraded } from '../../api/security/alerts'
import { sessionState } from '../../session/state'
import SecurityAlertPanel from './SecurityAlertPanel.vue'

const open = ref(false)
let notification: NotificationHandle | undefined
const { allowed, feed, unavailable } = useSecurityAlerts((count) => {
  notification?.close()
  notification = ElNotification({
    title: '新增安全告警',
    message: `${count} 条告警待核查`,
    type: 'warning',
    duration: 8000,
    onClick: () => {
      open.value = true
      notification?.close()
    },
  })
})
const unhealthy = computed(
  () => unavailable.value || (feed.value && (!feed.value.enabled || securityDegraded(feed.value))),
)
function clear() {
  open.value = false
  notification?.close()
  notification = undefined
}
watch(() => [allowed.value, sessionState.me?.user.id, sessionState.epoch], clear)
onBeforeUnmount(clear)
</script>

<template>
  <template v-if="allowed">
    <ElBadge :value="feed?.retainedAlerts || 0" :hidden="!feed?.retainedAlerts" :max="99">
      <ElButton
        :icon="Bell"
        :type="unhealthy ? 'warning' : undefined"
        :aria-label="unhealthy ? '安全告警，监测异常' : '查看安全告警'"
        @click="open = true"
        >安全告警<span v-if="unhealthy"> · 异常</span></ElButton
      >
    </ElBadge>
    <ElDrawer
      v-model="open"
      title="安全监测"
      size="min(1000px, 100vw)"
      append-to-body
      destroy-on-close
    >
      <SecurityAlertPanel v-if="open" />
    </ElDrawer>
  </template>
</template>
