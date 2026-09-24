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
    detail.value = '平台 API 已响应健康检查。'
  } catch (error: unknown) {
    status.value = 'failed'
    detail.value = error instanceof Error ? error.message : '检查失败，请稍后重试。'
    if (error instanceof ApiRequestError) {
      errorCode.value = error.code
      traceId.value = error.traceId
    }
  } finally {
    checkedAt.value = new Intl.DateTimeFormat('zh-CN', {
      timeZone: 'Asia/Shanghai',
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    }).format(new Date())
  }
}

onMounted(checkHealth)
</script>

<template>
  <div class="home-page">
    <div class="page-heading">
      <div>
        <h1>系统概览</h1>
        <p class="intro">查看管理服务的连接状态。</p>
      </div>
      <ElButton class="refresh-button" :loading="loading" @click="checkHealth">
        <svg
          v-if="!loading"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.7"
          aria-hidden="true"
        >
          <path d="M20 7v5h-5M4 17v-5h5" />
          <path d="M6.1 6.1A8 8 0 0 1 20 12M4 12a8 8 0 0 0 13.9 5.9" />
        </svg>
        重新检查
      </ElButton>
    </div>

    <section class="status-panel" aria-labelledby="status-title">
      <div class="panel-heading">
        <div>
          <p class="panel-kicker">连接检查</p>
          <h2 id="status-title">平台 API</h2>
        </div>
        <span class="status-badge" :class="status" role="status" aria-live="polite">
          <span class="status-dot" aria-hidden="true"></span>
          {{ statusLabels[status] }}
        </span>
      </div>

      <div class="panel-body" :aria-busy="loading">
        <div class="status-main">
          <p class="status-label">当前状态</p>
          <p class="status-text" :class="status">{{ statusLabels[status] }}</p>
          <p class="status-detail">{{ detail || '正在检查服务连接…' }}</p>
          <p v-if="status === 'failed'" class="help-text">
            请确认后端服务已启动，并检查连接配置后重试。
          </p>
        </div>

        <dl class="status-facts">
          <div>
            <dt>检查接口</dt>
            <dd class="mono">/actuator/health</dd>
          </div>
          <div>
            <dt>检查时间</dt>
            <dd>{{ checkedAt || '—' }}<span v-if="checkedAt" class="timezone">北京时间</span></dd>
          </div>
          <div v-if="errorCode">
            <dt>错误码</dt>
            <dd class="mono">{{ errorCode }}</dd>
          </div>
          <div v-if="traceId">
            <dt>请求标识</dt>
            <dd class="mono break-text">{{ traceId }}</dd>
          </div>
        </dl>
      </div>
    </section>

    <p class="scope-note">此处仅显示平台 API 健康检查结果，不代表视频任务或算法运行状态。</p>
  </div>
</template>

<style scoped>
.home-page {
  width: 100%;
  padding: 20px var(--content-spacing) 40px;
}
.page-heading {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 24px;
  margin-bottom: 18px;
}
.panel-kicker {
  margin: 0 0 12px;
  color: var(--text-tertiary);
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.04em;
}
h1 {
  margin: 0;
  color: var(--text-primary);
  font-size: 24px;
  font-weight: 650;
  letter-spacing: -0.045em;
  line-height: 1.25;
}
.intro {
  margin: 6px 0 0;
  color: var(--text-secondary);
  font-size: 14px;
}
.refresh-button {
  --el-button-bg-color: #fff;
  --el-button-border-color: #cbd3d1;
  --el-button-text-color: #243533;
  --el-button-hover-bg-color: #f1f6f4;
  --el-button-hover-border-color: #608f89;
  --el-button-hover-text-color: #165f58;
  --el-button-active-bg-color: #e7f1ee;
  min-height: 38px;
  padding: 8px 16px;
  border-radius: 6px;
  font-size: 13px;
  font-weight: 600;
}
.refresh-button svg {
  width: 16px;
  height: 16px;
  margin-right: 7px;
}
.status-panel {
  overflow: hidden;
  border: 1px solid var(--border-subtle);
  border-radius: 8px;
  background: #fff;
  box-shadow: 0 2px 10px rgba(23, 38, 36, 0.025);
}
.panel-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  padding: 20px 24px;
  border-bottom: 1px solid var(--border-subtle);
}
.panel-kicker {
  margin-bottom: 5px;
}
h2 {
  margin: 0;
  font-size: 17px;
  font-weight: 650;
  letter-spacing: -0.02em;
}
.status-badge {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
  padding: 6px 10px;
  border: 1px solid #dce2e0;
  border-radius: 4px;
  color: #53615d;
  background: #f5f7f6;
  font-size: 12px;
  font-weight: 600;
}
.status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #8b9995;
}
.status-badge.up {
  color: #1f6a4f;
  background: #eff8f2;
  border-color: #cde6d4;
}
.status-badge.up .status-dot {
  background: #348a61;
}
.status-badge.failed {
  color: #a44736;
  background: #fff4f0;
  border-color: #f1d9d1;
}
.status-badge.failed .status-dot {
  background: #cb6652;
}
.panel-body {
  display: grid;
  grid-template-columns: minmax(0, 1.2fr) minmax(290px, 0.8fr);
}
.status-main {
  padding: 24px;
}
.status-label {
  margin: 0 0 10px;
  color: var(--text-tertiary);
  font-size: 12px;
}
.status-text {
  margin: 0;
  color: #485653;
  font-size: 29px;
  font-weight: 650;
  letter-spacing: -0.035em;
  line-height: 1.3;
}
.status-text.up {
  color: #20674d;
}
.status-text.failed {
  color: #a44736;
}
.status-detail,
.help-text {
  max-width: 460px;
  margin: 14px 0 0;
  color: var(--text-secondary);
  font-size: 13px;
  line-height: 1.7;
  overflow-wrap: anywhere;
}
.help-text {
  margin-top: 8px;
  color: #7b4e42;
}
.status-facts {
  display: grid;
  align-content: start;
  gap: 0;
  margin: 0;
  padding: 16px 24px;
  border-left: 1px solid var(--border-subtle);
  background: #fafbfa;
}
.status-facts > div {
  padding: 12px 0;
}
.status-facts > div + div {
  border-top: 1px solid var(--border-subtle);
}
.status-facts dt {
  margin-bottom: 6px;
  color: var(--text-tertiary);
  font-size: 12px;
}
.status-facts dd {
  margin: 0;
  color: var(--text-primary);
  font-size: 13px;
  font-weight: 550;
}
.mono {
  font-family: 'Cascadia Code', Consolas, monospace;
  font-size: 12px !important;
}
.break-text {
  overflow-wrap: anywhere;
}
.timezone {
  margin-left: 8px;
  color: var(--text-tertiary);
  font-size: 11px;
  font-weight: 400;
}
.scope-note {
  margin: 19px 0 0;
  color: var(--text-tertiary);
  font-size: 12px;
  line-height: 1.7;
}
@media (max-width: 760px) {
  .panel-body {
    grid-template-columns: minmax(0, 1fr);
  }
  .status-facts {
    border-left: 0;
    border-top: 1px solid var(--border-subtle);
  }
}
@media (max-width: 560px) {
  .page-heading {
    align-items: flex-start;
    flex-direction: column;
    gap: 20px;
  }
  .panel-heading,
  .status-main,
  .status-facts {
    padding-left: 20px;
    padding-right: 20px;
  }
}
</style>
