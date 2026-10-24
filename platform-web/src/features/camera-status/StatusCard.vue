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
      <div class="camera-symbol-wrap" aria-hidden="true">
        <ElIcon class="camera-symbol"><VideoCamera /></ElIcon>
      </div>
      <div class="camera-identity">
        <h3 :title="displayName">{{ displayName }}</h3>
        <p
          class="camera-address"
          :title="`${addressLabel(channel.accessAddress)}：${addressText(channel.accessAddress)}`"
        >
          {{ addressLabel(channel.accessAddress) }} · {{ addressText(channel.accessAddress) }}
        </p>
      </div>
    </header>
    <footer class="camera-card-footer">
      <div class="video-state">
        <span>{{ platform ? '平台状态' : layer === 'CONTROL' ? '相机连接' : '视频收帧' }}</span
        ><strong :title="layer === 'CONTROL' && !platform ? '最近一次相机连接检查结果' : undefined"
          ><i class="state-dot" aria-hidden="true" />{{ statusLabel }}</strong
        >
      </div>
      <ElButton v-if="profile" link type="primary" @click="emit('details', channel, profile)"
        >详情</ElButton
      >
    </footer>
  </article>
</template>

<style scoped>
.camera-card {
  --state-soft-bg: #f1f5f9;
  display: flex;
  flex-direction: column;
  min-width: 0;
  background: #fff;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  overflow: hidden;
  transition:
    border-color 0.16s ease,
    box-shadow 0.16s ease,
    transform 0.16s ease;
}
.camera-card.state-accessible {
  --state-soft-bg: #ecfdf5;
}
.camera-card.state-failed {
  --state-soft-bg: #fef2f2;
  border-color: #fecaca;
}
.camera-card.state-checking {
  --state-soft-bg: #eff6ff;
}
.camera-card.state-stale {
  --state-soft-bg: #fffbeb;
}
.camera-card:hover {
  border-color: #cbd5e1;
  box-shadow: 0 4px 12px rgba(15, 23, 42, 0.06);
}
.camera-card-heading {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px 16px;
}
.camera-symbol-wrap {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 38px;
  height: 38px;
  border-radius: 8px;
  background: var(--state-soft-bg);
  color: var(--state-color);
  flex-shrink: 0;
}
.camera-symbol {
  font-size: 18px;
}
.camera-identity {
  flex: 1;
  min-width: 0;
}
.camera-identity h3 {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  margin: 0;
  color: #0f172a;
  font-size: 14px;
  font-weight: 600;
  line-height: 20px;
}
.camera-identity p {
  margin: 3px 0 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: #64748b;
  font-size: 12px;
  line-height: 18px;
  font-variant-numeric: tabular-nums;
}
.camera-card-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-top: auto;
  padding: 9px 16px;
  background: #f8fafc;
  border-top: 1px solid #f1f5f9;
}
.video-state {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.video-state span {
  color: #64748b;
  font-size: 12px;
}
.video-state strong {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 8px;
  border-radius: 999px;
  background: var(--state-soft-bg);
  color: var(--state-color);
  font-size: 12px;
  font-weight: 600;
  line-height: 18px;
}
.state-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
  flex-shrink: 0;
}
.camera-card-footer > .el-button {
  flex-shrink: 0;
  font-weight: 500;
}
</style>
