<script setup lang="ts">
import { onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElCard } from 'element-plus'
import { sessionState } from '../../session/state'
import PasswordForm from '../../features/account/PasswordForm.vue'

const router = useRouter()
onMounted(() => {
  if (!sessionState.me?.user.mustChangePassword)
    void router.replace({ path: '/profile', query: { tab: 'password' } })
})
</script>

<template>
  <div v-if="sessionState.me?.user.mustChangePassword" class="content-page">
    <ElCard shadow="never" class="required-password-card">
      <template #header><h2>设置新的登录密码</h2></template>
      <PasswordForm />
    </ElCard>
  </div>
</template>

<style scoped>
.required-password-card {
  max-width: 760px;
  margin: 24px auto;
}
.required-password-card h2 {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
}
.required-password-card :deep(.el-card__body) {
  padding: 32px;
}
@media (max-width: 680px) {
  .required-password-card :deep(.el-card__body) {
    padding: 24px 16px;
  }
}
</style>
