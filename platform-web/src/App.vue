<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { ElConfigProvider } from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import AdminLayout from './layouts/AdminLayout.vue'
import { sessionState } from './session/state'
import SessionEndedDialog from './components/feedback/SessionEndedDialog.vue'
import { useSessionMonitor } from './session/monitor'
import { useCrossTabSession } from './session/crossTab'
import { reloadSharedSession } from './session/session'

useSessionMonitor()
useCrossTabSession(reloadSharedSession)

const route = useRoute()
const standalone = computed(() => route.path === '/login' || route.path === '/unavailable')
</script>

<template>
  <ElConfigProvider :locale="zhCn">
    <SessionEndedDialog />
    <RouterView v-if="standalone" />
    <AdminLayout
      v-else
      :display-name="sessionState.me?.user.nickname || sessionState.me?.user.username"
    >
      <RouterView />
    </AdminLayout>
  </ElConfigProvider>
</template>
