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
  border: 1px solid var(--sf-border);
  border-radius: var(--sf-radius-lg);
  box-shadow: var(--sf-shadow-xs);
}
.required-password-card h2 {
  margin: 0;
  color: var(--sf-text-primary);
  font-size: var(--sf-fs-section);
  font-weight: 600;
}
.required-password-card :deep(.el-card__body) {
  padding: 28px 32px;
}
@media (max-width: 680px) {
  .required-password-card :deep(.el-card__body) {
    padding: 20px 16px;
  }
}
</style>
