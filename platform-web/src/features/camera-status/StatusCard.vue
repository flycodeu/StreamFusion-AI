<script setup lang="ts">
import { computed } from 'vue'
import { ElButton, ElIcon } from 'element-plus'
import { VideoCamera } from '@element-plus/icons-vue'
import type { StatusChannel, StatusProfile } from '../../api/camera-status/types'
import { addressLabel, addressText } from '../camera/address'
import { formatDateTime } from '../../utils/dateTime'
import { connectionLabels, representativeConnection, representativeProfile } from './health'
import { observationSpan } from './timeline'

const props = defineProps<{ channel: StatusChannel }>()
const emit = defineEmits<{ details: [channel: StatusChannel, profile: StatusProfile] }>()
const profile = computed(() => representativeProfile(props.channel))
const state = computed(() => representativeConnection(props.channel))
const connection = computed(() =>
  props.channel.lifecycle === 'DISABLED'
    ? '已停用'
    : props.channel.lifecycle === 'PENDING_ASSIGNMENT'
      ? '待归档'
      : !profile.value
        ? '无可用码流'
        : connectionLabels[state.value],
)
const failureSince = computed(() =>
  profile.value?.configurationCurrent && profile.value.observation?.value === 'FAILED'
    ? profile.value.timeline?.stateSince
    : null,
)
const lastResult = computed(() => {
  const fact = profile.value?.observation
  if (!fact) return '暂无检测记录'
  return `上次${fact.value === 'ACCESSIBLE' ? '连接通过' : fact.value === 'FAILED' ? '连接异常' : '检测待适配'}`
})
</script>

<template>
  <article class="camera-card" :class="`state-${state}`">
    <header class="camera-card-heading">
      <div class="camera-identity">
        <h3 :title="channel.name">{{ channel.name }}</h3>
        <p
          class="camera-address"
          :title="`${addressLabel(channel.accessAddress)}：${addressText(channel.accessAddress)}`"
        >
          {{ addressLabel(channel.accessAddress) }} · {{ addressText(channel.accessAddress) }}
        </p>
        <p
          v-if="channel.deviceName && channel.deviceName !== channel.name"
          :title="channel.deviceName"
        >
          {{ channel.deviceName }}
        </p>
      </div>
      <ElIcon class="camera-symbol" aria-hidden="true"><VideoCamera /></ElIcon>
    </header>
    <div class="card-states">
      <div class="connection-state">
        <span>连接</span><strong><i class="state-dot" aria-hidden="true" />{{ connection }}</strong>
      </div>
      <div class="video-state">
        <span>视频流</span><span>{{ channel.lifecycle !== 'ENABLED' ? '未启用' : '未检测' }}</span>
      </div>
    </div>
    <dl class="card-times">
      <div>
        <dt>最近检查</dt>
        <dd>
          <time v-if="profile?.observation" :datetime="profile.observation.observedAt">{{
            formatDateTime(profile.observation.observedAt)
          }}</time
          ><span v-else>暂无记录</span>
        </dd>
      </div>
      <div v-if="failureSince">
        <dt>异常首检</dt>
        <dd>
          <time :datetime="failureSince">{{ formatDateTime(failureSince) }}</time>
        </dd>
      </div>
      <div v-if="failureSince">
        <dt>观测跨度</dt>
        <dd :title="'截止最近检查，不是连续离线时长'">{{ observationSpan(profile) }}</dd>
      </div>
    </dl>
    <footer class="camera-card-footer">
      <span v-if="profile?.observation" class="last-check">{{ lastResult }}</span>
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
.connection-state,
.video-state {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.card-states span {
  color: var(--text-secondary);
}
.connection-state strong {
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
.card-times {
  display: grid;
  gap: 4px;
  margin: 0;
  padding: 6px 12px 10px;
  font-size: 12px;
  line-height: 18px;
  font-variant-numeric: tabular-nums;
}
.card-times > div {
  display: flex;
  justify-content: space-between;
  gap: 8px;
}
.card-times dt {
  color: var(--text-secondary);
  flex-shrink: 0;
}
.card-times dd {
  margin: 0;
  text-align: right;
  overflow-wrap: anywhere;
}
.camera-card-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: auto;
  padding: 6px 12px;
  min-height: 32px;
  background: var(--el-fill-color-extra-light);
  border-top: 1px solid var(--el-border-color-lighter);
}
.last-check {
  color: var(--text-secondary);
  font-size: 12px;
}
.camera-card-footer > .el-button {
  flex-shrink: 0;
}
</style>
