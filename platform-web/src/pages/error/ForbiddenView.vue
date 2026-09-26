<script setup lang="ts">
import { useRouter } from 'vue-router'
import { ElButton } from 'element-plus'
import { House } from '@element-plus/icons-vue'
import PageState from '../../components/feedback/PageState.vue'

const router = useRouter()
</script>

<template>
  <PageState
    kind="forbidden"
    title="暂无页面访问权限"
    description="该页面未向当前账号开放，或访问权限已调整。你可以返回首页继续操作，或联系管理员检查页面授权。"
  >
    <template #actions>
      <ElButton type="primary" :icon="House" @click="router.replace('/home')">返回首页</ElButton>
    </template>
  </PageState>
</template>
