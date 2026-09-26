<script setup lang="ts">
import { useRouter } from 'vue-router'
import { ElButton, ElResult } from 'element-plus'
const router = useRouter()
</script>

<template>
  <ElResult
    icon="warning"
    title="页面暂不可用"
    sub-title="页面文件未发布或菜单路径不匹配，请联系管理员检查菜单配置。"
  >
    <template #extra
      ><ElButton type="primary" @click="router.push('/home')">返回首页</ElButton></template
    >
  </ElResult>
</template>
