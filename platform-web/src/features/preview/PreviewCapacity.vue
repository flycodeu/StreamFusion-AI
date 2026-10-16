<script setup lang="ts">
import { computed } from 'vue'
import { ElButton } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { sessionState } from '../../session/state'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePreviewCapacity } from './usePreviewCapacity'

const { capacity, error, loading, paused, refresh } = usePreviewCapacity(
  computed(() => !!sessionState.me),
)
</script>

<template>
  <div class="preview-capacity" aria-label="全站预览容量">
    <div class="capacity-line">
      <template v-if="capacity">
        <span :title="`本实例容量快照：${capacity.observedAt}；最终以服务端准入为准`"
          >全站预览 {{ capacity.occupiedSessions }}/{{ capacity.maxSessions }}</span
        >
        <span v-if="!capacity.transcodesAvailable"
          >转码待核对<span v-if="capacity.maxTranscodes === 0">（已关闭）</span></span
        >
        <span v-else-if="capacity.maxTranscodes === 0"
          >转码已关闭<span v-if="capacity.occupiedTranscodes"
            >（登记占用 {{ capacity.occupiedTranscodes }}）</span
          ></span
        >
        <span v-else>转码 {{ capacity.occupiedTranscodes }}/{{ capacity.maxTranscodes }}</span>
        <span v-if="capacity.closingSessions">待释放 {{ capacity.closingSessions }}</span>
      </template>
      <span v-else>{{
        paused ? '容量读取已暂停' : loading ? '正在读取容量…' : '容量暂不可用'
      }}</span>
      <ElButton
        :icon="Refresh"
        text
        circle
        size="small"
        :loading="loading"
        aria-label="刷新预览容量"
        @click="refresh()"
      />
    </div>
    <RequestError :error="error" advice="容量读取失败，请刷新重试；新开画面仍由服务端校验配额。" />
  </div>
</template>

<style scoped>
.preview-capacity {
  margin-right: auto;
  min-width: 0;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.capacity-line {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 12px;
}
.capacity-line > span {
  white-space: nowrap;
}
@media (max-width: 680px) {
  .preview-capacity {
    flex-basis: 100%;
    order: 2;
  }
}
</style>
