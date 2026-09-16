<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElButton } from 'element-plus'
import 'element-plus/es/components/button/style/css'
import { ApiRequestError, getJson } from './lib/http'
import AdminLayout from './layouts/AdminLayout.vue'

const loading = ref(false)
const status = ref('等待检查')
const detail = ref('')
const checkedAt = ref('')

async function checkHealth(): Promise<void> {
  if (loading.value) return
  loading.value = true
  status.value = '检查中'
  detail.value = ''
  try {
    const data = await getJson('/actuator/health')
    if (typeof data !== 'object' || data === null || !('status' in data) || data.status !== 'UP') {
      throw new Error('健康接口未返回 UP')
    }
    status.value = '运行正常'
    detail.value = 'Platform API · UP'
  } catch (error: unknown) {
    status.value = '连接失败'
    detail.value = `${error instanceof Error ? error.message : '未知错误'}。请检查 Platform API 是否启动及 API_TARGET 配置。`
    if (error instanceof ApiRequestError) {
      detail.value += ` 错误码：${error.code}，请求标识：${error.traceId}`
    }
  } finally {
    checkedAt.value = new Date().toLocaleTimeString('zh-CN', {
      hour12: false,
      timeZone: 'Asia/Shanghai',
    })
    loading.value = false
  }
}

onMounted(checkHealth)
</script>

<template>
  <AdminLayout>
    <div class="page-heading">
      <div>
        <p class="eyebrow">WORKSPACE</p>
        <h1>系统概览</h1>
      </div>
    </div>
    <section class="health" aria-labelledby="health-title">
      <div class="section-heading">
        <div>
          <h2 id="health-title">服务状态</h2>
          <p class="section-subtitle">Platform API</p>
        </div>
        <ElButton :loading="loading" @click="checkHealth">刷新状态</ElButton>
      </div>
      <div role="status" aria-live="polite">
        <p class="status">
          <span
            class="dot"
            :class="{ ready: status === '运行正常', failed: status === '连接失败' }"
          ></span
          >{{ status }}
        </p>
        <p class="detail">{{ detail || '正在访问健康接口…' }}</p>
        <p v-if="checkedAt" class="timestamp">检查时间 {{ checkedAt }}</p>
      </div>
    </section>
  </AdminLayout>
</template>
