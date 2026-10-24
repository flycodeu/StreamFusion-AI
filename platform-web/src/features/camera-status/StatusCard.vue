<script setup lang="ts">
import { computed } from 'vue'
import { ElButton, ElIcon } from 'element-plus'
import { VideoCamera } from '@element-plus/icons-vue'
import type { StatusChannel, StatusLayer, StatusProfile } from '../../api/camera-status/types'
import { addressLabel, addressText } from '../camera/address'
import {
  connectionLabels,
  mediaLabels,
  representativeConnection,
  representativeProfile,
  upstreamLabels,
} from './health'

const props = withDefaults(defineProps<{ channel: StatusChannel; layer?: StatusLayer }>(), {
  layer: 'CONTROL',
})
const emit = defineEmits<{ details: [channel: StatusChannel, profile: StatusProfile] }>()
const profile = computed(() => representativeProfile(props.channel))
const displayName = computed(() => props.channel.deviceName || props.channel.name)
const state = computed(() => representativeConnection(props.channel, props.layer))
const platform = computed(
  () =>
    !!profile.value?.upstream &&
    profile.value.upstream.blockingReasonCode !== 'STATUS_LAYER_NOT_APPLICABLE',
)
const labels = computed(() =>
  platform.value ? upstreamLabels : props.layer === 'CONTROL' ? connectionLabels : mediaLabels,
)
const statusLabel = computed(() =>
  props.channel.lifecycle === 'DISABLED'
    ? '已停用'
    : props.channel.lifecycle === 'PENDING_ASSIGNMENT'
      ? '待归档'
      : !profile.value
        ? '无可用码流'
        : labels.value[state.value],
)
</script>

<template>
  <article class="camera-card" :class="`state-${state}`">
    <header class="camera-card-heading">
      <div class="camera-identity">
        <h3 :title="displayName">{{ displayName }}</h3>
        <p
          class="camera-address"
          :title="`${addressLabel(channel.accessAddress)}：${addressText(channel.accessAddress)}`"
        >
          {{ addressLabel(channel.accessAddress) }} · {{ addressText(channel.accessAddress) }}
        </p>
      </div>
      <ElIcon class="camera-symbol" aria-hidden="true"><VideoCamera /></ElIcon>
    </header>
    <div class="card-states">
      <div class="video-state">
        <span>{{ platform ? '平台状态' : layer === 'CONTROL' ? '相机连接' : '视频收帧' }}</span
        ><strong :title="layer === 'CONTROL' && !platform ? '最近一次相机连接检查结果' : undefined"
          ><i class="state-dot" aria-hidden="true" />{{ statusLabel }}</strong
        >
      </div>
    </div>
    <footer class="camera-card-footer">
      <ElButton v-if="profile" link type="primary" @click="emit('details', channel, profile)"
        >详情</ElButton
      >
    </footer>
  </article>
</template>

<style scoped>
.camera-card {
  display: flex;
  flex-direction: column;
  min-width: 0;
  border: 1px solid var(--el-border-color-lighter);
  border-top: 3px solid var(--state-color);
  border-radius: 6px;
  overflow: hidden;
}
.camera-card-heading {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
  padding: 12px 12px 8px;
}
.camera-identity {
  min-width: 0;
}
.camera-identity h3 {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
  overflow-wrap: anywhere;
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  line-height: 20px;
}
.camera-identity p {
  margin: 3px 0 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-secondary);
  font-size: 12px;
  line-height: 18px;
}
.camera-symbol {
  color: var(--el-text-color-placeholder);
  font-size: 18px;
  flex-shrink: 0;
}
.card-states {
  display: grid;
  gap: 6px;
  padding: 4px 12px 8px;
  font-size: 13px;
}
.video-state {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.card-states span {
  color: var(--text-secondary);
}
.video-state strong {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--state-color);
  font-size: 13px;
  font-weight: 600;
}
.state-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
  flex-shrink: 0;
}
.camera-card-footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  margin-top: auto;
  padding: 6px 12px;
  min-height: 32px;
  background: var(--el-fill-color-extra-light);
  border-top: 1px solid var(--el-border-color-lighter);
}
.camera-card-footer > .el-button {
  flex-shrink: 0;
}
</style>
