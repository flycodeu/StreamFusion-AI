<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElButton } from 'element-plus'
import { RefreshRight } from '@element-plus/icons-vue'
import PageState from '../../components/feedback/PageState.vue'
import RequestError from '../../components/feedback/RequestError.vue'
import { navigationFailure } from '../../router/navigationFailure'

const router = useRouter()
const retrying = ref(false)

async function retry(): Promise<void> {
  if (retrying.value) return
  retrying.value = true
  try {
    const target = navigationFailure.value?.path
    await router.replace(target && target !== '/unavailable' ? target : '/home')
  } finally {
    retrying.value = false
  }
}
</script>

<template>
  <PageState
    title="服务暂不可用"
    description="暂时无法连接平台服务，请检查网络连接后重试。如果持续出现，请联系管理员检查服务状态。"
    fullscreen
  >
    <template #actions>
      <ElButton type="primary" :icon="RefreshRight" :loading="retrying" @click="retry">
        重新尝试
      </ElButton>
      <ElButton :disabled="retrying" @click="router.replace('/login')">返回登录</ElButton>
    </template>
    <template v-if="navigationFailure" #details>
      <RequestError :error="navigationFailure.error" />
    </template>
  </PageState>
</template>
