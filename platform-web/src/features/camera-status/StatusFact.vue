<script setup lang="ts">
import { computed } from 'vue'
import { ElTag } from 'element-plus'
import type { LayerStatus } from '../../api/camera-status/types'
import { formatDateTime } from '../../utils/dateTime'
import { factPresentation, reasonLabel } from './model'
import { observationSpan, stateSinceLabel } from './timeline'

const props = defineProps<{ profile: LayerStatus | null }>()
const display = computed(() => factPresentation(props.profile))
</script>

<template>
  <div class="status-fact" aria-live="polite">
    <div class="fact-heading">
      <span>{{
        profile?.layer === 'UPSTREAM'
          ? '平台状态'
          : profile?.layer === 'MEDIA'
            ? '视频收帧结果'
            : '连接检查结果'
      }}</span>
      <ElTag :type="display.tone" size="small">{{ display.label }}</ElTag>
    </div>
    <template v-if="profile?.observation">
      <p
        v-if="display.historical || profile.observation.value !== 'ACCESSIBLE'"
        class="fact-reason"
      >
        {{ display.historical ? '上次结果：' : ''
        }}{{ reasonLabel(profile.observation.reasonCode) }}
      </p>
      <dl>
        <div>
          <dt>检查时间</dt>
          <dd>{{ formatDateTime(profile.observation.observedAt) }}</dd>
        </div>
        <template v-if="profile.observation.mediaEvidence">
          <div>
            <dt>采样窗口</dt>
            <dd>
              {{
                (
                  (Date.parse(profile.observation.mediaEvidence.windowEndedAt) -
                    Date.parse(profile.observation.mediaEvidence.windowStartedAt)) /
                  1000
                ).toFixed(1)
              }}
              秒
            </dd>
          </div>
          <div>
            <dt>接收帧增量</dt>
            <dd>
              {{
                profile.observation.mediaEvidence.lastFrames -
                profile.observation.mediaEvidence.firstFrames
              }}
              帧
            </dd>
          </div>
          <div>
            <dt>采样次数</dt>
            <dd>{{ profile.observation.mediaEvidence.sampleCount }} 次</dd>
          </div>
        </template>
        <template v-if="profile.configurationCurrent && profile.timeline">
          <div>
            <dt>{{ stateSinceLabel(profile) }}</dt>
            <dd>{{ formatDateTime(profile.timeline.stateSince) }}</dd>
          </div>
          <div>
            <dt>最近成功</dt>
            <dd>
              {{
                profile.timeline.lastAccessibleAt
                  ? formatDateTime(profile.timeline.lastAccessibleAt)
                  : '暂无记录'
              }}
            </dd>
          </div>
          <div>
            <dt>最近失败</dt>
            <dd>
              {{
                profile.timeline.lastFailedAt
                  ? formatDateTime(profile.timeline.lastFailedAt)
                  : '暂无记录'
              }}
            </dd>
          </div>
          <div v-if="observationSpan(profile)">
            <dt>异常观测跨度</dt>
            <dd>{{ observationSpan(profile) }}</dd>
          </div>
        </template>
      </dl>
    </template>
  </div>
</template>

<style scoped>
.status-fact {
  min-width: 0;
}
.fact-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}
.fact-heading > span,
dt {
  color: var(--text-secondary);
}
.fact-reason {
  margin: 8px 0 0;
  color: var(--text-secondary);
  overflow-wrap: anywhere;
}
dl {
  display: grid;
  gap: 4px;
  margin: 10px 0 0;
  font-size: var(--camera-card-meta, 13px);
}
dl > div {
  display: grid;
  grid-template-columns: 7em minmax(0, 1fr);
  gap: 12px;
}
dd {
  margin: 0;
  font-variant-numeric: tabular-nums;
  overflow-wrap: anywhere;
}
</style>
