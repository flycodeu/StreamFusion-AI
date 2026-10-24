<script setup lang="ts">
import { computed } from 'vue'
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
      <span class="fact-status" :class="`tone-${display.tone}`">{{ display.label }}</span>
    </div>
    <template v-if="profile?.observation">
      <p
        v-if="display.historical || profile.observation.value !== 'ACCESSIBLE'"
        class="fact-reason"
      >
        {{ display.historical ? '上次结果：' : ''
        }}{{ reasonLabel(profile.observation.reasonCode) }}
      </p>
      <dl class="fact-grid">
        <div>
          <dt>检查时间</dt>
          <dd>{{ formatDateTime(profile.observation.observedAt) }}</dd>
        </div>
        <div>
          <dt>结果说明</dt>
          <dd>{{ reasonLabel(profile.observation.reasonCode) || display.label }}</dd>
        </div>
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
            <dd class="fact-muted">
              {{
                profile.timeline.lastFailedAt
                  ? formatDateTime(profile.timeline.lastFailedAt)
                  : '暂无记录'
              }}
            </dd>
          </div>
          <div v-if="observationSpan(profile)" class="fact-wide">
            <dt>异常观测跨度</dt>
            <dd>{{ observationSpan(profile) }}</dd>
          </div>
        </template>
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
      </dl>
    </template>
    <dl v-else class="fact-grid">
      <div>
        <dt>检查时间</dt>
        <dd class="fact-muted">暂无检查记录</dd>
      </div>
      <div>
        <dt>检查条件</dt>
        <dd>
          {{
            profile?.canCheck
              ? '支持手动与定时巡检'
              : reasonLabel(profile?.blockingReasonCode ?? null) || '当前不可执行检查'
          }}
        </dd>
      </div>
    </dl>
  </div>
</template>

<style scoped>
.status-fact {
  min-width: 0;
  padding: 16px 18px;
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  background: #fff;
  font-size: 13px;
}
.fact-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  padding-bottom: 12px;
  border-bottom: 1px solid #f1f5f9;
}
.fact-heading > span:first-child {
  color: #0f172a;
  font-size: 14px;
  font-weight: 600;
}
.fact-status {
  padding: 2px 10px;
  border: 1px solid #e2e8f0;
  border-radius: 99px;
  background: #f1f5f9;
  color: #64748b;
  font-size: 12px;
  font-weight: 600;
}
.fact-status.tone-success {
  border-color: #a7f3d0;
  background: #ecfdf5;
  color: #059669;
}
.fact-status.tone-warning {
  border-color: #fecaca;
  background: #fef2f2;
  color: #b91c1c;
}
.fact-reason {
  margin: 12px 0 0;
  padding: 8px 12px;
  border-radius: 6px;
  background: #f8fafc;
  border-left: 3px solid #cbd5e1;
  color: #475569;
  overflow-wrap: anywhere;
}
.fact-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 16px;
  margin: 14px 0 0;
}
.fact-grid > div {
  min-width: 0;
  padding: 10px 12px;
  border-radius: 8px;
  background: #f8fafc;
  border: 1px solid #f1f5f9;
}
.fact-grid dt {
  margin-bottom: 4px;
  color: #64748b;
  font-size: 12px;
}
.fact-grid dd {
  margin: 0;
  color: #0f172a;
  font-weight: 500;
  line-height: 1.5;
  font-variant-numeric: tabular-nums;
  overflow-wrap: anywhere;
}
.fact-grid .fact-muted {
  color: #94a3b8;
  font-weight: 400;
}
.fact-wide {
  grid-column: 1 / -1;
}
@media (max-width: 420px) {
  .fact-grid {
    grid-template-columns: minmax(0, 1fr);
    gap: 10px;
  }
}
</style>
