<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
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
import { accountHint, captchaHint, passwordHint } from '../../utils/loginValidation'
import { ApiRequestError } from '../../lib/http/error'
import { systemConfig } from '../../config/system'
import { loginValidation } from '../../config/validation'
import { useLoginCaptcha } from '../../features/auth/useLoginCaptcha'
import { captchaErrorMessage, loginErrorMessage } from '../../features/auth/loginFeedback'

const router = useRouter()
const route = useRoute()
const brandIcon = systemConfig.logo
const form = reactive({ username: '', password: '' })
const touched = reactive({ username: false, password: false, captcha: false })
const {
  challenge: captcha,
  answer: captchaAnswer,
  requesting: captchaRequesting,
  imageReady: captchaImageReady,
  imageSource: captchaImage,
  ready: captchaReady,
  error: captchaError,
  refresh: refreshCaptcha,
  imageLoaded,
  imageFailed,
  submission: captchaSubmission,
} = useLoginCaptcha()
const remember = ref(false)
const showPassword = ref(false)
const busy = ref(false)
const loginEstablished = ref(false)
const error = ref(route.query.reason === 'ip-blocked' ? '登录暂时受限，请联系管理员' : '')
const storageNotice = ref('')
const feedback = computed(
  () => captchaErrorMessage(captchaError.value) || error.value || storageNotice.value,
)
const accountError = computed(() => (touched.username ? accountHint(form.username.trim()) : ''))
const passwordError = computed(() => (touched.password ? passwordHint(form.password) : ''))
const captchaInputError = computed(() => (touched.captcha ? captchaHint(captchaAnswer.value) : ''))
let edited = false
let rememberRevision = 0
let hadSaved = false
let active = true

onMounted(async () => {
  void refreshCaptcha()
  try {
    const saved = await readRememberedLogin()
    if (!active) return
    hadSaved = !!saved
    if (saved && !edited) {
      Object.assign(form, saved)
      remember.value = true
    }
  } catch {
    /* Remembering is optional; a disabled browser store never blocks login. */
  }
})
onUnmounted(() => {
  active = false
})
function captchaImageEvent(event: globalThis.Event, loaded: boolean) {
  const id = (event.target as globalThis.HTMLImageElement).dataset.captchaId
  if (id) (loaded ? imageLoaded : imageFailed)(id)
}
async function replaceCaptcha(): Promise<void> {
  if (busy.value || loginEstablished.value) return
  touched.captcha = false
  error.value = ''
  await refreshCaptcha()
}
function reloadPage() {
  globalThis.location.reload()
}
function changed(field: 'username' | 'password' | 'captcha') {
  edited = true
  touched[field] = true
  error.value = ''
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
  if (busy.value || loginEstablished.value || !captchaReady.value) return
  touched.username = touched.password = touched.captcha = true
  if (accountError.value || passwordError.value || captchaInputError.value) return
  const verification = captchaSubmission()
  if (!verification) return
  busy.value = true
  error.value = ''
  try {
    const account = form.username.trim()
    const password = form.password
    await signIn(account, password, verification)
    loginEstablished.value = true
    if (!active) return
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
    if (!active) return
    form.password = ''
    const next =
      typeof route.query.next === 'string' &&
      route.query.next.startsWith('/') &&
      !route.query.next.startsWith('//')
        ? route.query.next
        : '/home'
    await router.replace(next)
  } catch (cause) {
    if (!active) return
    if (
      loginEstablished.value ||
      sessionState.me ||
      (cause instanceof ApiRequestError && cause.code === 'LOGIN_RESTORE_FAILED')
    ) {
      loginEstablished.value = true
      error.value = '已完成登录，页面暂未打开，请刷新页面继续'
      return
    }
    error.value = loginErrorMessage(cause)
    // Every attempted login consumes its code, including an incorrect account or password.
    touched.captcha = false
    await refreshCaptcha()
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <main class="login-page">
    <section class="login-visual" aria-label="平台品牌展示">
      <a class="login-brand" href="/login"
        ><img :src="brandIcon" alt="" width="38" height="38" /><span>{{
          systemConfig.name
        }}</span></a
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
        <h2>{{ systemConfig.loginTagline }}</h2>
        <p>{{ systemConfig.name }} {{ systemConfig.description }}</p>
      </div>
    </section>
    <section class="login-form-region">
      <div class="login-card">
        <div class="mobile-brand">
          <img :src="brandIcon" alt="" width="34" height="34" />{{ systemConfig.name }}
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
          <ElFormItem label="验证码" :error="captchaInputError" class="captcha-field">
            <div class="captcha-row">
              <button
                class="captcha-image"
                type="button"
                :disabled="busy || captchaRequesting || (!!captcha && !captchaImageReady)"
                :aria-busy="captchaRequesting || (!!captcha && !captchaImageReady)"
                aria-label="刷新图片验证码"
                title="点击换一张验证码"
                @click="replaceCaptcha"
              >
                <img
                  v-if="captcha"
                  :key="captcha.captchaId"
                  :src="captchaImage"
                  :data-captcha-id="captcha.captchaId"
                  alt="图片验证码，点击换一张"
                  width="120"
                  height="40"
                  :class="{ 'is-loading': !captchaImageReady }"
                  @load="captchaImageEvent($event, true)"
                  @error="captchaImageEvent($event, false)"
                />
                <span v-if="!captchaImageReady" class="captcha-placeholder">{{
                  captchaRequesting || captcha ? '加载中…' : '点击获取'
                }}</span>
              </button>
              <ElInput
                id="captcha"
                v-model="captchaAnswer"
                name="captcha"
                aria-label="验证码"
                placeholder="验证码"
                autocomplete="off"
                autocapitalize="off"
                :spellcheck="false"
                :maxlength="loginValidation.captcha.length"
                :disabled="busy || !captchaReady"
                :validate-event="false"
                @input="changed('captcha')"
                @blur="touched.captcha = true"
              />
            </div>
          </ElFormItem>
          <div class="login-options">
            <ElCheckbox v-model="remember" :disabled="busy" @change="rememberChanged"
              >记住账号和密码</ElCheckbox
            >
          </div>
          <ElAlert
            v-if="feedback"
            class="login-error"
            :title="feedback"
            :type="error || captchaError ? 'error' : 'warning'"
            :closable="false"
            show-icon
          />
          <ElButton v-if="loginEstablished" class="login-submit" type="primary" @click="reloadPage"
            >刷新页面</ElButton
          >
          <ElButton
            v-else
            class="login-submit"
            native-type="submit"
            type="primary"
            :loading="busy"
            :disabled="!captchaReady"
            >{{ busy ? '正在登录' : '登录' }}</ElButton
          >
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
  min-width: 0;
}
.login-card {
  width: 100%;
  max-width: 460px;
  min-width: 0;
  padding: 34px 32px;
  border: 1px solid #e1e7ef;
  border-radius: 12px;
  background: #fff;
}
h1 {
  margin: 0 0 34px;
  font-size: 25px;
  font-weight: 600;
  color: #303133;
  text-align: center;
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
.captcha-row {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  min-width: 0;
}
.captcha-row :deep(.el-input) {
  flex: 1;
  min-width: 0;
}
.captcha-image {
  position: relative;
  display: grid;
  place-items: center;
  flex: 0 0 120px;
  height: 40px;
  padding: 0;
  border: 1px solid #dce4ee;
  border-radius: 4px;
  overflow: hidden;
  background: #f5f8fc;
  color: #6b7d94;
  cursor: pointer;
}
.captcha-image:hover:not(:disabled) {
  border-color: var(--el-color-primary);
}
.captcha-image:focus-visible {
  outline: 2px solid var(--el-color-primary);
  outline-offset: 3px;
}
.captcha-image:disabled {
  cursor: default;
}
.captcha-image img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: contain;
}
.captcha-image img.is-loading {
  visibility: hidden;
}
.captcha-placeholder {
  position: absolute;
  font-size: 12px;
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
.mobile-brand {
  display: none;
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
  .login-card {
    padding: 30px 24px;
  }
  .captcha-image {
    flex-basis: 100px;
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
    padding: 20px;
  }
  .mobile-brand {
    display: flex;
    align-items: center;
    gap: 10px;
    justify-content: center;
    margin-bottom: 28px;
    font-size: 20px;
    font-weight: 600;
  }
}
@media (max-width: 380px) {
  .login-form-region {
    padding: 12px;
  }
  .login-card {
    padding: 28px 18px;
  }
  .captcha-row {
    gap: 8px;
  }
  .captcha-image {
    flex-basis: 92px;
  }
}
@media (prefers-reduced-motion: reduce) {
  .login-card :deep(.el-input__wrapper) {
    transition: none;
  }
}
</style>
