<script setup lang="ts">
import { useRouter } from 'vue-router'
import { ElButton } from 'element-plus'
import { House } from '@element-plus/icons-vue'
import PageState from '../../components/feedback/PageState.vue'
const router = useRouter()
</script>

<template>
  <PageState
    kind="missing"
    title="页面暂不可用"
    description="页面文件尚未发布，或菜单路径与页面不匹配。请联系管理员检查菜单配置，也可以返回首页继续操作。"
  >
    <template #actions>
      <ElButton type="primary" :icon="House" @click="router.replace('/home')">返回首页</ElButton>
    </template>
  </PageState>
</template>
