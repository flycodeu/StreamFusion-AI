<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElButton } from 'element-plus'
import 'element-plus/es/components/button/style/css'
import { ApiRequestError, getJson } from './lib/http'

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
    checkedAt.value = new Date().toLocaleTimeString('zh-CN', { hour12: false })
    loading.value = false
  }
}

onMounted(checkHealth)
</script>

<template>
  <div class="shell">
    <header>
      <span class="brand-mark" aria-hidden="true">SF</span
      ><span>StreamFusion <strong>AI</strong></span
      ><span class="phase">P0 / 工程初始化</span>
    </header>
    <main>
      <p class="eyebrow">VIDEO INTELLIGENCE PLATFORM</p>
      <h1>从一条视频流开始。</h1>
      <p class="intro">实时视频智能分析平台 · 初始工程</p>
      <section class="health" aria-labelledby="health-title">
        <div class="section-heading">
          <h2 id="health-title">后端连接</h2>
          <ElButton :loading="loading" @click="checkHealth">重新检查</ElButton>
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
      <p class="footnote">当前仅提供可启动的工程骨架。视频接入、推理及业务功能将在后续阶段实现。</p>
    </main>
    <footer><span>STREAMFUSION AI</span><span>0.1.0 / P0-W01</span></footer>
  </div>
</template>
