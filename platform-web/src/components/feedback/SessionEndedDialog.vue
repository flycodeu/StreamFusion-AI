<script setup lang="ts">
import { ElButton, ElDialog } from 'element-plus'
import { Lock } from '@element-plus/icons-vue'
import { dismissSessionEnded, endedNotice, restoreSessionEnded } from '../../session/endedNotice'
import { formatDateTime } from '../../utils/dateTime'

restoreSessionEnded()
</script>

<template>
  <ElDialog
    :model-value="!!endedNotice"
    title="登录状态已变更"
    width="460px"
    align-center
    :close-on-click-modal="false"
    @close="dismissSessionEnded"
  >
    <div v-if="endedNotice" class="session-ended">
      <div class="session-ended-icon"><Lock /></div>
      <h2>
        {{ endedNotice.reason === 'REPLACED' ? '您的账号有新的登录' : '您已被管理员强制登出' }}
      </h2>
      <p>
        {{
          endedNotice.reason === 'REPLACED'
            ? '当前登录已退出。如非本人操作，请重新登录后修改密码。'
            : '当前会话已结束。如有疑问，请联系管理员。'
        }}
      </p>
      <dl>
        <dt>{{ endedNotice.reason === 'REPLACED' ? '登录时间' : '登出时间' }}</dt>
        <dd>{{ formatDateTime(endedNotice.loginAt || endedNotice.occurredAt) }}</dd>
        <template v-if="endedNotice.reason === 'REPLACED'">
          <dt>IP 地址</dt>
          <dd>{{ endedNotice.sourceIp || '未获取' }}</dd>
          <dt>地区</dt>
          <dd>{{ endedNotice.region || '未获取' }}</dd>
          <dt>浏览器环境</dt>
          <dd>
            {{ [endedNotice.browser, endedNotice.os].filter(Boolean).join(' / ') || '未获取' }}
          </dd>
        </template>
      </dl>
    </div>
    <template #footer
      ><ElButton type="primary" @click="dismissSessionEnded">我知道了</ElButton></template
    >
  </ElDialog>
</template>

<style scoped>
.session-ended {
  text-align: center;
}
.session-ended-icon {
  width: 56px;
  height: 56px;
  margin: 0 auto 18px;
  padding: 14px;
  border-radius: 50%;
  color: var(--el-color-warning);
  background: var(--el-color-warning-light-9);
}
.session-ended h2 {
  font-size: 19px;
  color: var(--el-text-color-primary);
  margin: 0 0 10px;
}
.session-ended p {
  line-height: 1.7;
  margin: 0 0 22px;
  color: var(--el-text-color-secondary);
}
.session-ended dl {
  display: grid;
  grid-template-columns: 90px minmax(0, 1fr);
  gap: 12px;
  text-align: left;
  padding: 18px;
  background: var(--el-fill-color-light);
  border-radius: 8px;
}
.session-ended dt {
  color: var(--el-text-color-secondary);
}
.session-ended dd {
  margin: 0;
  overflow-wrap: anywhere;
  color: var(--el-text-color-primary);
}
</style>
