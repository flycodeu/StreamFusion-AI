<script setup lang="ts">
import { computed } from 'vue'
import { ElButton, ElIcon } from 'element-plus'
import { Connection, Refresh } from '@element-plus/icons-vue'
import { ApiRequestError } from '../../lib/http/error'
import { formatDateTime } from '../../utils/dateTime'
import DiagnosticLink from '../diagnostics/DiagnosticLink.vue'

const props = defineProps<{ error: unknown; busy?: boolean }>()
const emit = defineEmits<{ retry: []; settings: [] }>()
const detail = computed(() => (props.error instanceof ApiRequestError ? props.error : null))
const denied = computed(() => detail.value?.status === 403)
const title = computed(() => (denied.value ? '暂无存储访问权限' : '暂时无法访问存储'))
const advice = computed(() =>
  denied.value
    ? '请核对账号权限及存储凭据后重试。'
    : '请确认存储服务已启动，并检查网络和接入配置。',
)
</script>

<template>
  <div class="storage-access-state" role="alert">
    <div class="access-symbol" aria-hidden="true">
      <ElIcon :size="30"><Connection /></ElIcon><i class="access-dot" />
    </div>
    <h3>{{ title }}</h3>
    <p>{{ advice }}</p>
    <div class="access-actions">
      <ElButton type="primary" :icon="Refresh" :loading="busy" @click="emit('retry')"
        >重新连接</ElButton
      >
      <ElButton :disabled="busy" @click="emit('settings')">检查设置</ElButton>
    </div>
    <details v-if="detail" class="access-details">
      <summary>排查信息</summary>
      <dl>
        <div>
          <dt>错误信息</dt>
          <dd>{{ detail.message }}</dd>
        </div>
        <div>
          <dt>错误码</dt>
          <dd>{{ detail.code }}</dd>
        </div>
        <div>
          <dt>发生时间</dt>
          <dd>{{ formatDateTime(detail.timestamp || detail.receivedAt) }}</dd>
        </div>
        <div v-if="detail.traceId">
          <dt>请求标识</dt>
          <dd>{{ detail.traceId }}</dd>
        </div>
      </dl>
      <DiagnosticLink :trace-id="detail.traceId" :time="detail.timestamp || detail.receivedAt" />
    </details>
  </div>
</template>

<style scoped>
.storage-access-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 330px;
  padding: 36px 16px;
  text-align: center;
  box-sizing: border-box;
}
.access-symbol {
  position: relative;
  display: grid;
  place-items: center;
  width: 68px;
  height: 68px;
  margin-bottom: 22px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 20px;
  background: var(--el-fill-color-extra-light);
  color: var(--el-text-color-secondary);
}
.access-dot {
  position: absolute;
  width: 10px;
  height: 10px;
  bottom: 8px;
  right: 8px;
  border: 3px solid var(--el-bg-color);
  border-radius: 50%;
  background: var(--el-color-warning);
}
h3 {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: var(--el-text-color-primary);
}
p {
  margin: 10px 0 22px;
  font-size: 13px;
  line-height: 1.7;
  color: var(--el-text-color-secondary);
  max-width: 350px;
}
.access-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 10px;
}
.access-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}
.access-details {
  width: 100%;
  max-width: 520px;
  margin-top: 22px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
summary {
  width: fit-content;
  margin: 0 auto;
  cursor: pointer;
}
dl {
  margin: 14px 0 0;
  padding: 14px 16px;
  background: var(--el-fill-color-extra-light);
  border-radius: 8px;
  text-align: left;
}
dl > div {
  display: grid;
  grid-template-columns: 62px minmax(0, 1fr);
  gap: 12px;
  line-height: 1.7;
}
dl > div + div {
  margin-top: 6px;
}
dt,
dd {
  margin: 0;
  overflow-wrap: anywhere;
}
dd {
  color: var(--el-text-color-regular);
  user-select: text;
}
@media (max-width: 700px) {
  .storage-access-state {
    min-height: 300px;
    padding: 28px 0;
  }
}
</style>
