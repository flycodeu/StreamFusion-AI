<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElButton, ElForm, ElFormItem, ElInput, ElMessage } from 'element-plus'
import { getPasswordPolicy } from '../../api/auth/passwordPolicy'
import type { PasswordPolicy } from '../../api/auth/passwordPolicy'
import RequestError from '../../components/feedback/RequestError.vue'
import { updatePassword } from '../../session/session'
import { newPasswordError, passwordChecks } from './password'
const router = useRouter()
const form = reactive({ current: '', password: '', confirm: '' })
const touched = reactive({ current: false, password: false, confirm: false })
const policy = ref<PasswordPolicy | null>(null)
const loading = ref(false)
const busy = ref(false)
const error = ref<unknown>(null)
const checks = computed(() => (policy.value ? passwordChecks(form.password, policy.value) : []))
const passwordError = computed(() =>
  policy.value ? newPasswordError(form.password, form.current, policy.value) : '',
)
const confirmError = computed(() =>
  !form.confirm
    ? '请再次输入新密码'
    : form.confirm !== form.password
      ? '两次输入的新密码不一致'
      : '',
)
async function loadPolicy() {
  loading.value = true
  error.value = null
  try {
    policy.value = await getPasswordPolicy()
  } catch (cause) {
    error.value = cause
  } finally {
    loading.value = false
  }
}
async function submit() {
  if (busy.value || !policy.value) return
  Object.assign(touched, { current: true, password: true, confirm: true })
  if (!form.current || passwordError.value || confirmError.value) return
  busy.value = true
  error.value = null
  try {
    await updatePassword(form.current, form.password)
    Object.assign(form, { current: '', password: '', confirm: '' })
    ElMessage.success('密码已修改，请重新登录')
    await router.replace('/login')
  } catch (cause) {
    error.value = cause
  } finally {
    busy.value = false
  }
}
onMounted(loadPolicy)
onUnmounted(() => Object.assign(form, { current: '', password: '', confirm: '' }))
</script>

<template>
  <section class="password-section">
    <p class="password-notice">修改密码后需要重新登录。</p>
    <RequestError :error="error" />
    <ElButton v-if="!policy" :loading="loading" @click="loadPolicy">重新获取密码规则</ElButton>
    <ElForm v-else label-width="100px" :disabled="busy" @submit.prevent="submit">
      <ElFormItem
        label="当前密码"
        :error="touched.current && !form.current ? '请输入当前密码' : ''"
        required
        ><ElInput
          v-model="form.current"
          type="password"
          autocomplete="current-password"
          show-password
          placeholder="请输入当前密码"
          @blur="touched.current = true"
      /></ElFormItem>
      <ElFormItem label="新密码" :error="touched.password ? passwordError : ''" required
        ><ElInput
          v-model="form.password"
          type="password"
          autocomplete="new-password"
          show-password
          placeholder="请输入新密码"
          @blur="touched.password = true"
      /></ElFormItem>
      <ul class="password-checks" aria-label="密码规则">
        <li v-for="check in checks" :key="check.label" :class="{ passed: check.valid }">
          <span aria-hidden="true">{{ check.valid ? '✓' : '○' }}</span
          >{{ check.label }}
        </li>
      </ul>
      <ElFormItem label="确认新密码" :error="touched.confirm ? confirmError : ''" required
        ><ElInput
          v-model="form.confirm"
          type="password"
          autocomplete="new-password"
          show-password
          placeholder="请再次输入新密码"
          @blur="touched.confirm = true"
      /></ElFormItem>
      <ElFormItem class="password-submit"
        ><ElButton type="primary" native-type="submit" :loading="busy"
          >确认修改</ElButton
        ></ElFormItem
      >
    </ElForm>
  </section>
</template>

<style scoped>
.password-section {
  max-width: 620px;
}
.password-notice {
  margin: 0 0 28px;
  color: var(--el-text-color-regular);
  font-size: 14px;
}
.password-section :deep(.el-form-item) {
  margin-bottom: 28px;
}
.password-checks {
  display: flex;
  flex-wrap: wrap;
  gap: 10px 18px;
  padding: 0;
  margin: -8px 0 28px 100px;
  list-style: none;
  color: var(--el-text-color-secondary);
  font-size: 14px;
}
.password-checks li {
  display: flex;
  align-items: center;
  gap: 6px;
}
.password-checks .passed {
  color: var(--el-color-success);
}
.password-section .password-submit {
  margin-top: 32px;
  margin-bottom: 0;
}
@media (max-width: 540px) {
  .password-checks {
    margin-left: 0;
  }
}
</style>
