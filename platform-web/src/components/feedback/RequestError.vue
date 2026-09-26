<script setup lang="ts">
import { computed } from 'vue'
import { ApiRequestError } from '../../lib/http/error'

const props = defineProps<{ error: unknown }>()
const detail = computed(() => (props.error instanceof ApiRequestError ? props.error : null))
const advice = computed(() => {
  const error = detail.value
  if (!error) return '请检查输入后重试。'
  if (error.code === 'INITIAL_PASSWORD_UNAVAILABLE')
    return '请配置有效的 SF_AUTH_INITIAL_PASSWORD，并重启后端服务。'
  if (error.code === 'CSRF_INVALID') return '请求凭证已更新，请重试当前操作。'
  if (error.code === 'IP_BLOCKED') return '请由超级管理员在操作记录的 IP 封禁列表中解除限制。'
  if (error.status === 429)
    return error.retryAfter
      ? `请求过于频繁，请在 ${error.retryAfter} 秒后重试。`
      : '请求过于频繁，请稍后重试。'
  if (error.status === 401) return '会话已失效，请重新登录。'
  if (error.status === 403) return '请联系管理员检查页面授权。'
  if (error.status === 409 || error.status === 412) return '数据已变化，请刷新列表后重试。'
  if (error.status === 503 || error.code === 'DEPENDENCY_UNAVAILABLE')
    return '服务暂不可用，请稍后重试；持续出现时提供请求标识给管理员。'
  return '请检查输入后重试；持续出现时提供请求标识给管理员。'
})
const time = computed(() => {
  const value = new Date(detail.value?.timestamp || detail.value?.receivedAt || Date.now())
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: 'Asia/Shanghai',
    dateStyle: 'short',
    timeStyle: 'medium',
  }).format(Number.isFinite(value.getTime()) ? value : new Date())
})
</script>

<template>
  <div v-if="error" class="request-error" role="alert">
    <strong>{{ detail?.message || '操作失败，请重试' }}</strong>
    <span>{{ advice }}</span>
    <details v-if="detail">
      <summary>{{ detail.code }} · {{ time }}</summary>
      <div v-if="detail.traceId" class="trace-id">请求标识 {{ detail.traceId }}</div>
    </details>
  </div>
</template>

<style scoped>
.request-error {
  display: grid;
  gap: 5px;
  padding: 12px 14px;
  margin: 10px 0;
  background: #fff4f2;
  border: 1px solid #eac9c2;
  border-radius: 6px;
  color: #8f2d28;
  font-size: 13px;
}
.request-error summary {
  cursor: pointer;
}
.trace-id {
  margin-top: 6px;
  overflow-wrap: anywhere;
}
</style>
