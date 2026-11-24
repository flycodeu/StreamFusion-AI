<script setup lang="ts">
import { ElAlert, ElButton, ElPopover } from "element-plus";
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
  <ElAlert
    v-if="diagnostic"
    class="ops-banner-alert"
    type="error"
    show-icon
    @close="emit('close')"
  >
    <div class="ops-alert-row">
      <div class="ops-alert-copy">
        <span class="ops-alert-title">{{ diagnostic.message }}</span>
        <span
          v-if="diagnostic.code && findErrorReference(diagnostic.code)?.steps?.[0]"
          class="ops-alert-desc"
        >
          建议：{{ findErrorReference(diagnostic.code)!.steps[0] }}
        </span>
      </div>
      <div
        v-if="diagnostic.code || diagnostic.traceId || diagnostic.timestamp"
        class="ops-alert-actions"
      >
        <ElPopover trigger="click" :width="400" placement="bottom-end">
          <template #reference>
            <ElButton link type="danger" size="small">查看诊断信息</ElButton>
          </template>
          <div class="ops-diag-popover">
            <div class="ops-diag-popover-head">
              <span>诊断详情</span>
              <ErrorHelpLink
                v-if="diagnostic.code && findErrorReference(diagnostic.code)"
                :code="diagnostic.code"
              />
            </div>
            <dl class="ops-diag-dl">
              <template v-if="diagnostic.code">
                <dt>错误码</dt>
                <dd><code>{{ diagnostic.code }}</code></dd>
              </template>
              <template v-if="diagnostic.timestamp">
                <dt>发生时间</dt>
                <dd>{{ displayTime(diagnostic.timestamp) }}</dd>
              </template>
              <template v-if="diagnostic.traceId">
                <dt>请求标识</dt>
                <dd><code>{{ diagnostic.traceId }}</code></dd>
              </template>
            </dl>
          </div>
        </ElPopover>
      </div>
    </div>
  </ElAlert>
</template>

<style scoped>
.ops-banner-alert {
  margin-bottom: 14px;
}
</style>
