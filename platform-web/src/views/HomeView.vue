<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElButton } from 'element-plus'
import 'element-plus/es/components/button/style/css'
import { ApiRequestError } from '../lib/http/error'
import { getHealth } from '../api/health/api'

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
  <section class="home-page" aria-label="首页">
    <div class="page-heading">
      <h1>系统概览</h1>
      <p>查看平台服务的连接状态。</p>
    </div>
    <section class="health" aria-labelledby="health-title">
      <div class="section-heading">
        <h2 id="health-title">服务状态</h2>
        <ElButton class="refresh-button" :loading="loading" round @click="checkHealth">
          <svg
            v-if="!loading"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.6"
            aria-hidden="true"
          >
            <path d="M20 7v5h-5M4 17v-5h5" />
            <path d="M6.1 6.1A8 8 0 0 1 20 12M4 12a8 8 0 0 0 13.9 5.9" />
          </svg>
          刷新状态
        </ElButton>
      </div>
      <div class="health-body" role="status" aria-live="polite" :aria-busy="loading">
        <div class="status-summary">
          <p class="service-name">后端服务</p>
          <p class="status">
            <span
              class="dot"
              :class="{ ready: status === 'up', failed: status === 'failed' }"
              aria-hidden="true"
            ></span
            >{{ statusLabels[status] }}
          </p>
        </div>
        <div class="status-description">
          <p class="detail">{{ detail || '正在检查服务连接…' }}</p>
          <p v-if="checkedAt" class="timestamp">上次检查 {{ checkedAt }}</p>
        </div>
      </div>
    </section>
  </section>
</template>

<style scoped>
.page-heading {
  margin-bottom: 24px;
}
h1 {
  margin: 0;
  font-size: 26px;
  line-height: 1.4;
  font-weight: 600;
  letter-spacing: -0.8px;
}
.page-heading p {
  margin: 12px 0 0;
  color: var(--text-secondary);
  font-size: 14px;
  line-height: 1.7;
}
.health {
  border-bottom: 1px solid var(--border-subtle);
}
.section-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding-bottom: 18px;
  border-bottom: 1px solid var(--border-subtle);
}
h2 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
}
.refresh-button {
  --el-button-bg-color: #fff;
  --el-button-border-color: var(--border-subtle);
  --el-button-text-color: var(--text-primary);
  --el-button-hover-bg-color: var(--surface-muted);
  --el-button-hover-border-color: #cdd2c9;
  --el-button-hover-text-color: var(--text-primary);
  --el-button-active-bg-color: #e5e9e1;
  --el-button-active-border-color: #cdd2c9;
  --el-button-active-text-color: var(--text-primary);
  min-height: 36px;
  padding: 8px 14px;
  font-size: 13px;
}
.refresh-button svg {
  width: 16px;
  height: 16px;
  margin-right: 7px;
}
.health-body {
  display: grid;
  grid-template-columns: minmax(180px, 1fr) minmax(0, 1.5fr);
  align-items: center;
  gap: 24px;
  padding: 24px 0;
}
.service-name {
  margin: 0 0 12px;
  font-size: 13px;
  color: var(--text-secondary);
}
.status {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 0;
  font-size: 24px;
  line-height: 1.4;
  font-weight: 500;
}
.dot {
  width: 8px;
  height: 8px;
  flex-shrink: 0;
  border-radius: 50%;
  background: #939990;
}
.dot.ready {
  background: #39815b;
}
.dot.failed {
  background: #bb533d;
}
.detail {
  margin: 0;
  font-size: 14px;
  color: var(--text-secondary);
  line-height: 1.8;
  overflow-wrap: anywhere;
}
.timestamp {
  margin: 10px 0 0;
  font-size: 12px;
  color: var(--text-secondary);
  font-variant-numeric: tabular-nums;
}
@media (max-width: 960px) {
  .health-body {
    grid-template-columns: minmax(0, 1fr);
    gap: 20px;
  }
}
@media (max-width: 680px) {
  .page-heading {
    margin-bottom: 20px;
  }
  h1 {
    font-size: 24px;
  }
  .section-heading {
    flex-wrap: wrap;
  }
}
</style>
