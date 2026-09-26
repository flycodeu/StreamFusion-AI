<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElAlert, ElButton, ElCard, ElDescriptions, ElDescriptionsItem, ElTag } from 'element-plus'
import { ApiRequestError } from '../../lib/http/error'
import { getHealth } from '../../api/health/api'
import { formatDateTime } from '../../utils/dateTime'

type HealthStatus = 'idle' | 'checking' | 'up' | 'failed'

const statusLabels: Record<HealthStatus, string> = {
  idle: '等待检查',
  checking: '检查中',
  up: '连接正常',
  failed: '连接失败',
}
const status = ref<HealthStatus>('idle')
const loading = computed(() => status.value === 'checking')
const detail = ref('')
const errorCode = ref('')
const traceId = ref('')
const checkedAt = ref('')

async function checkHealth(): Promise<void> {
  if (loading.value) return
  status.value = 'checking'
  detail.value = ''
  errorCode.value = ''
  traceId.value = ''
  try {
    await getHealth()
    status.value = 'up'
  } catch (error: unknown) {
    status.value = 'failed'
    detail.value = error instanceof Error ? error.message : '检查失败，请稍后重试。'
    if (error instanceof ApiRequestError) {
      errorCode.value = error.code
      traceId.value = error.traceId
    }
  } finally {
    checkedAt.value = formatDateTime(new Date())
  }
}

onMounted(checkHealth)
</script>

<template>
  <div class="content-page">
    <ElCard shadow="never" class="service-panel">
      <template #header>
        <div class="panel-header">
          <h2>平台服务</h2>
          <ElButton :loading="loading" @click="checkHealth">刷新</ElButton>
        </div>
      </template>
      <ElDescriptions :column="1" :label-width="100" border :aria-busy="loading">
        <ElDescriptionsItem label="服务状态">
          <ElTag
            :type="status === 'up' ? 'success' : status === 'failed' ? 'danger' : 'info'"
            effect="plain"
            role="status"
            aria-live="polite"
            >{{ statusLabels[status] }}</ElTag
          >
        </ElDescriptionsItem>
        <ElDescriptionsItem label="检查时间">{{ checkedAt || '—' }}</ElDescriptionsItem>
      </ElDescriptions>
      <ElAlert
        v-if="status === 'failed'"
        class="health-error"
        :title="detail"
        type="error"
        :closable="false"
        show-icon
      >
        <p v-if="errorCode">错误码：{{ errorCode }}</p>
        <p v-if="traceId">请求标识：{{ traceId }}</p>
        <p>请确认平台服务已启动，检查连接配置后重试。</p>
      </ElAlert>
    </ElCard>
  </div>
</template>

<style scoped>
.service-panel {
  border-radius: 4px;
}
.panel-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
h2 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
}
.health-error {
  margin-top: 16px;
}
.health-error p {
  margin: 4px 0;
  overflow-wrap: anywhere;
}
</style>
