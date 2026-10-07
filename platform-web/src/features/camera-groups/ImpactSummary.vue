<script setup lang="ts">
import { ElAlert } from 'element-plus'
import type { Impact } from '../../api/camera-groups/types'
import { formatDateTime } from '../../utils/dateTime'
defineProps<{ impact: Impact }>()
</script>
<template>
  <ElAlert type="warning" :closable="false" title="确认资源范围影响">
    <p v-if="impact.fromPath || impact.toPath">
      {{ impact.fromPath || '待归档 / 根目录' }} → {{ impact.toPath || '根目录' }}
    </p>
    <p>
      获权账户 {{ impact.gainedUserCount }}，失权账户 {{ impact.lostUserCount
      }}<span v-if="impact.affectedCameraCount !== null"
        >，涉及相机 {{ impact.affectedCameraCount }}</span
      >。
    </p>
    <p>确认有效至 {{ formatDateTime(impact.expiresAt) }}。分组或授权变化后需重新确认。</p>
  </ElAlert>
</template>
