<script setup lang="ts">
import type { ApiError } from "./api";
import ErrorHelpLink from "./ErrorHelpLink.vue";
import { findErrorReference } from "./errorReference";

defineProps<{
  diagnostic: ApiError | null;
}>();

const emit = defineEmits<{
  close: [];
}>();

const displayTime = (value?: string) =>
  value
    ? new Date(value).toLocaleString("zh-CN", {
        timeZone: "Asia/Shanghai",
        hour12: false,
      })
    : "—";
</script>

<template>
  <div v-if="diagnostic" class="error diagnostic" role="alert">
    <div class="diagnostic-heading">
      <span>{{ diagnostic.message }}</span>
      <button
        type="button"
        class="diagnostic-close"
        aria-label="关闭错误提示"
        @click.stop="emit('close')"
      >
        ×
      </button>
    </div>
    <div
      v-if="diagnostic.code || diagnostic.traceId || diagnostic.timestamp"
      class="diagnostic-meta"
    >
      <details class="diagnostic-details">
        <summary>查看诊断信息</summary>
        <dl class="diagnostic-dl">
          <template v-if="diagnostic.code">
            <dt>错误码</dt>
            <dd><code>{{ diagnostic.code }}</code></dd>
          </template>
          <template v-if="diagnostic.timestamp">
            <dt>时间</dt>
            <dd>{{ displayTime(diagnostic.timestamp) }}</dd>
          </template>
          <template v-if="diagnostic.traceId">
            <dt>请求标识</dt>
            <dd><code>{{ diagnostic.traceId }}</code></dd>
          </template>
        </dl>
      </details>
      <ErrorHelpLink
        v-if="diagnostic.code && findErrorReference(diagnostic.code)"
        :code="diagnostic.code"
      />
    </div>
  </div>
</template>

<style scoped>
.diagnostic-meta {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px 16px;
  margin-top: 6px;
  font-size: 12px;
}
.diagnostic-details summary {
  cursor: pointer;
  user-select: none;
  opacity: 0.9;
}
.diagnostic-dl {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: 4px 10px;
  margin: 6px 0 0;
}
.diagnostic-dl dt {
  opacity: 0.8;
}
.diagnostic-dl dd {
  margin: 0;
  overflow-wrap: anywhere;
}
</style>
