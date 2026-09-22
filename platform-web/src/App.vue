<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElButton } from 'element-plus'
import 'element-plus/es/components/button/style/css'
import { ApiRequestError } from './lib/http/error'
import { getHealth } from './api/health/api'
import AdminLayout from './layouts/AdminLayout.vue'

type HealthStatus = 'idle' | 'checking' | 'up' | 'failed'

const statusLabels: Record<HealthStatus, string> = {
  idle: '等待检查',
  checking: '检查中',
  up: '运行正常',
  failed: '连接失败',
}
const status = ref<HealthStatus>('idle')
const loading = computed(() => status.value === 'checking')
const detail = ref('')
const checkedAt = ref('')

async function checkHealth(): Promise<void> {
  if (loading.value) return
  status.value = 'checking'
  detail.value = ''
  try {
    await getHealth()
    status.value = 'up'
    detail.value = '后端服务连接正常'
  } catch (error: unknown) {
    status.value = 'failed'
    detail.value = `${error instanceof Error ? error.message : '未知错误'}。请确认后端服务已启动，或联系管理员检查连接配置。`
    if (error instanceof ApiRequestError) {
      detail.value += ` 错误码：${error.code}，请求标识：${error.traceId}`
    }
  } finally {
    checkedAt.value = new Date().toLocaleTimeString('zh-CN', {
      hour12: false,
      timeZone: 'Asia/Shanghai',
    })
  }
}

onMounted(checkHealth)
</script>

<template>
  <AdminLayout>
    <div class="page-heading">
      <h1>系统概览</h1>
    </div>
    <section class="health" aria-labelledby="health-title">
      <div class="section-heading">
        <h2 id="health-title">服务状态</h2>
        <ElButton :loading="loading" @click="checkHealth">刷新状态</ElButton>
      </div>
      <div role="status" aria-live="polite">
        <p class="status">
          <span class="dot" :class="{ ready: status === 'up', failed: status === 'failed' }"></span
          >{{ statusLabels[status] }}
        </p>
        <p class="detail">{{ detail || '正在访问健康接口…' }}</p>
        <p v-if="checkedAt" class="timestamp">检查时间 {{ checkedAt }}</p>
      </div>
    </section>
  </AdminLayout>
</template>

<style scoped>
.page-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  margin-bottom: 26px;
}
h1 {
  margin: 0;
  font-size: 24px;
  font-weight: 600;
  letter-spacing: -0.5px;
}
.health {
  max-width: 880px;
  background: white;
  padding: 26px 28px;
  border: 1px solid #e2e7eb;
  border-radius: 8px;
  box-shadow: 0 2px 3px #142d4303;
}
.section-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
h2 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
}
.status {
  display: flex;
  align-items: center;
  gap: 9px;
  margin: 30px 0 12px;
  font-size: 19px;
  font-weight: 500;
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #a5b3bd;
}
.dot.ready {
  background: #32917d;
}
.dot.failed {
  background: #cd7755;
}
.detail {
  font-size: 12px;
  color: #71808d;
  line-height: 1.8;
  overflow-wrap: anywhere;
}
.timestamp {
  margin: 24px 0 0;
  padding-top: 16px;
  border-top: 1px solid #f0f2f4;
  font-size: 11px;
  color: #8c98a3;
  font-variant-numeric: tabular-nums;
}
@media (max-width: 680px) {
  .health {
    padding: 20px;
  }
  h1 {
    font-size: 22px;
  }
}
</style>
