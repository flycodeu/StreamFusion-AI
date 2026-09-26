<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElAlert, ElButton, ElCheckbox, ElForm, ElFormItem, ElInput, ElMessage } from 'element-plus'
import { Hide, View } from '@element-plus/icons-vue'
import { signIn } from '../../session/session'
import { sessionState } from '../../session/state'
import {
  forgetRememberedLogin,
  readRememberedLogin,
  saveRememberedLogin,
} from '../../session/rememberedLogin'
import { accountHint, passwordHint } from '../../utils/loginValidation'
import { ApiRequestError } from '../../lib/http/error'

const router = useRouter()
const route = useRoute()
const brandIcon = `${import.meta.env.BASE_URL}streamfusion.svg`
const form = reactive({ username: '', password: '' })
const touched = reactive({ username: false, password: false })
const remember = ref(false)
const showPassword = ref(false)
const busy = ref(false)
const error = ref(
  route.query.reason === 'ip-blocked' ? '当前 IP 已被封禁，请联系超级管理员解除限制' : '',
)
const errorTraceId = ref('')
const storageNotice = ref('')
const accountError = computed(() => (touched.username ? accountHint(form.username.trim()) : ''))
const passwordError = computed(() => (touched.password ? passwordHint(form.password) : ''))
let edited = false
let rememberRevision = 0
let hadSaved = false

onMounted(async () => {
  try {
    const saved = await readRememberedLogin()
    hadSaved = !!saved
    if (saved && !edited) {
      Object.assign(form, saved)
      remember.value = true
    }
  } catch {
    /* Remembering is optional; a disabled browser store never blocks login. */
  }
})
function changed(field: 'username' | 'password') {
  edited = true
  touched[field] = true
  error.value = ''
  errorTraceId.value = ''
}
async function rememberChanged(): Promise<void> {
  edited = true
  rememberRevision++
  if (!remember.value) {
    try {
      await forgetRememberedLogin()
      hadSaved = false
      storageNotice.value = ''
    } catch {
      storageNotice.value = '无法清除本机保存，请在浏览器设置中清除此网站的数据'
    }
  }
}
async function submit(): Promise<void> {
  if (busy.value) return
  touched.username = touched.password = true
  if (accountError.value || passwordError.value) return
  busy.value = true
  error.value = ''
  errorTraceId.value = ''
  try {
    const account = form.username.trim()
    const password = form.password
    await signIn(account, password)
    // A temporary credential must not remain saved after the required password change.
    const revision = rememberRevision
    try {
      if (remember.value && !sessionState.me?.user.mustChangePassword) {
        await saveRememberedLogin({ username: account, password })
        if (!remember.value || revision !== rememberRevision) await forgetRememberedLogin()
      } else if (hadSaved || remember.value) await forgetRememberedLogin()
    } catch {
      ElMessage.warning('登录成功，浏览器未能保存本机凭据')
    }
    form.password = ''
    const next =
      typeof route.query.next === 'string' &&
      route.query.next.startsWith('/') &&
      !route.query.next.startsWith('//')
        ? route.query.next
        : '/home'
    await router.replace(next)
  } catch (cause) {
    if (cause instanceof ApiRequestError) {
      errorTraceId.value = cause.traceId
      if (cause.code === 'LOGIN_FAILED')
        error.value =
          '账号或密码错误，或登录暂时受限。连续 5 次错误会限制登录 15 分钟，可稍后重试或联系管理员重置密码。'
      else if (cause.code === 'IP_BLOCKED')
        error.value = '当前 IP 已被封禁，请联系超级管理员解除限制'
      else if (cause.status === 429 || cause.code.includes('LOCKED'))
        error.value = '登录尝试过于频繁，请稍后重试'
      else if (cause.code === 'NETWORK_ERROR' || cause.code === 'REQUEST_TIMEOUT')
        error.value = '暂时无法连接服务，请检查网络后重试'
      else error.value = cause.message
    } else error.value = '登录未完成，请重试'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <main class="login-page">
    <section class="login-visual" aria-label="平台品牌展示">
      <a class="login-brand" href="/login"
        ><img :src="brandIcon" alt="StreamFusion" width="38" height="38" /><span
          >StreamFusion <strong>AI</strong></span
        ></a
      >
      <div class="visual-content">
        <div class="stream-illustration" aria-hidden="true">
          <svg viewBox="0 0 560 330" fill="none">
            <path
              d="M124 94H230Q260 94 260 130V164M424 94H330Q300 94 300 130V164M130 258H230Q260 258 260 220V180M424 258H330Q300 258 300 220V180"
              stroke="#B2CFF5"
              stroke-width="3"
            />
            <rect
              x="32"
              y="38"
              width="146"
              height="104"
              rx="10"
              fill="white"
              stroke="#CADCF4"
              stroke-width="2"
            />
            <rect x="46" y="52" width="118" height="69" rx="5" fill="#E5EFFB" />
            <path d="m96 71 22 14-22 14V71Z" fill="#87B5F1" />
            <path d="M57 131h51" stroke="#CADCF4" stroke-width="3" stroke-linecap="round" />
            <rect
              x="378"
              y="38"
              width="146"
              height="104"
              rx="10"
              fill="white"
              stroke="#CADCF4"
              stroke-width="2"
            />
            <path d="M397 67h29v29h-29zM441 67h29v29h-29zM485 67h18v29h-18z" fill="#DEEAFA" />
            <path d="M397 114h102" stroke="#CADCF4" stroke-width="3" stroke-linecap="round" />
            <rect
              x="55"
              y="215"
              width="146"
              height="89"
              rx="10"
              fill="white"
              stroke="#CADCF4"
              stroke-width="2"
            />
            <path
              d="M77 278v-25m29 25v-40m29 40v-17m29 17v-34"
              stroke="#8BB8F2"
              stroke-width="12"
              stroke-linecap="round"
            />
            <rect
              x="368"
              y="215"
              width="146"
              height="89"
              rx="10"
              fill="white"
              stroke="#CADCF4"
              stroke-width="2"
            />
            <path
              d="M390 239h18m12 0h71M390 259h18m12 0h55M390 279h18m12 0h71"
              stroke="#B9D2F3"
              stroke-width="5"
              stroke-linecap="round"
            />
            <rect
              x="227"
              y="121"
              width="106"
              height="106"
              rx="24"
              fill="white"
              stroke="#D4E3F8"
              stroke-width="2"
            />
            <image :href="brandIcon" x="246" y="140" width="68" height="68" />
          </svg>
        </div>
        <h2>连接视频与智能</h2>
        <p>StreamFusion AI 视频分析平台</p>
      </div>
    </section>
    <section class="login-form-region">
      <div class="login-card">
        <div class="mobile-brand">
          <img :src="brandIcon" alt="" width="34" height="34" />StreamFusion AI
        </div>
        <h1>账号登录</h1>
        <ElForm
          :model="form"
          label-width="54px"
          label-position="left"
          size="large"
          @submit.prevent="submit"
        >
          <ElFormItem label="账号" :error="accountError">
            <ElInput
              id="account"
              v-model="form.username"
              name="username"
              autocomplete="username"
              placeholder="请输入账号"
              :disabled="busy"
              :validate-event="false"
              @input="changed('username')"
              @blur="touched.username = true"
            />
          </ElFormItem>
          <ElFormItem label="密码" :error="passwordError">
            <ElInput
              id="password"
              v-model="form.password"
              name="password"
              :type="showPassword ? 'text' : 'password'"
              autocomplete="current-password"
              placeholder="请输入密码"
              :disabled="busy"
              :validate-event="false"
              @input="changed('password')"
              @blur="touched.password = true"
            >
              <template #suffix
                ><button
                  class="password-eye"
                  type="button"
                  :aria-label="showPassword ? '隐藏密码' : '显示密码'"
                  :aria-pressed="showPassword"
                  @click="showPassword = !showPassword"
                >
                  <component :is="showPassword ? Hide : View" /></button
              ></template>
            </ElInput>
          </ElFormItem>
          <div class="login-options">
            <ElCheckbox v-model="remember" :disabled="busy" @change="rememberChanged"
              >记住账号和密码</ElCheckbox
            >
          </div>
          <ElAlert
            v-if="error || storageNotice"
            class="login-error"
            :title="error || storageNotice"
            :type="error ? 'error' : 'warning'"
            :closable="false"
            show-icon
          >
            <div v-if="error && errorTraceId" class="login-trace">
              请求标识 <code>{{ errorTraceId }}</code>
            </div>
          </ElAlert>
          <ElButton class="login-submit" native-type="submit" type="primary" :loading="busy">{{
            busy ? '正在登录' : '登录'
          }}</ElButton>
        </ElForm>
      </div>
    </section>
  </main>
</template>

<style scoped>
.login-page {
  min-height: 100dvh;
  display: grid;
  grid-template-columns: minmax(0, 1.3fr) minmax(420px, 1fr);
  background: #fff;
}
.login-visual {
  position: relative;
  display: flex;
  flex-direction: column;
  padding: 36px 48px;
  background: #f0f5fc;
  border-right: 1px solid #e8eef6;
}
.login-brand {
  display: flex;
  align-items: center;
  gap: 12px;
  font-size: 21px;
  font-weight: 600;
  color: #26374e;
}
.login-brand strong {
  color: #2878e8;
  font-weight: 600;
}
.visual-content {
  margin: auto 0;
  padding: 28px 0 70px;
  text-align: center;
}
.stream-illustration {
  max-width: 610px;
  margin: 0 auto 30px;
}
.stream-illustration svg {
  width: 100%;
}
.visual-content h2 {
  margin: 0 0 16px;
  color: #294f80;
  font-size: 28px;
  font-weight: 600;
  letter-spacing: 2px;
}
.visual-content p {
  margin: 0;
  font-size: 16px;
  color: #6f839f;
}
.login-form-region {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 40px;
}
.login-card {
  width: 100%;
  max-width: 390px;
}
h1 {
  margin: 0 0 42px;
  font-size: 25px;
  font-weight: 600;
  color: #303133;
}
.login-card :deep(.el-form-item) {
  margin-bottom: 28px;
}
.login-card :deep(.el-input__wrapper) {
  transition: box-shadow 160ms ease;
}
.login-card :deep(.el-form-item__error) {
  line-height: 1.5;
  padding-top: 5px;
}
.login-options {
  margin: -8px 0 20px 54px;
}
.password-eye {
  display: grid;
  place-items: center;
  padding: 5px;
  color: var(--el-text-color-secondary);
  background: transparent;
  border: 0;
  cursor: pointer;
}
.password-eye:hover {
  color: var(--el-color-primary);
}
.password-eye svg {
  width: 19px;
  height: 19px;
}
.login-submit {
  width: 100%;
  height: 44px;
  font-size: 16px;
}
.login-error {
  margin-bottom: 20px;
}
.login-trace {
  margin-top: 4px;
  overflow-wrap: anywhere;
  user-select: all;
}
.mobile-brand {
  display: none;
}
@media (min-width: 1500px) {
  .login-form-region {
    padding-right: 12%;
  }
}
@media (max-width: 900px) {
  .login-visual {
    padding: 30px;
  }
  .visual-content h2 {
    font-size: 23px;
  }
  .login-page {
    grid-template-columns: minmax(0, 1fr) minmax(380px, 1fr);
  }
  .login-form-region {
    padding: 28px;
  }
}
@media (max-width: 720px) {
  .login-page {
    display: flex;
  }
  .login-visual {
    display: none;
  }
  .login-form-region {
    width: 100%;
    padding: 26px;
  }
  .mobile-brand {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 40px;
    font-size: 20px;
    font-weight: 600;
  }
}
@media (prefers-reduced-motion: reduce) {
  .login-card :deep(.el-input__wrapper) {
    transition: none;
  }
}
</style>
