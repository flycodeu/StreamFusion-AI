<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref } from 'vue'
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
const visualHighlights = ['多源流媒体接入', '实时视觉智能分析', '边缘节点协同管理'] as const
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
const capsLockOn = ref(false)
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
let pendingCaptchaFocus = false

function focusInput(id: 'account' | 'captcha') {
  void nextTick(() => {
    if (!active) return
    const el = globalThis.document?.getElementById(id) as globalThis.HTMLInputElement | null
    if (el && !el.disabled) el.focus()
  })
}

function checkCapsLock(event: globalThis.Event) {
  if (
    typeof globalThis.KeyboardEvent !== 'undefined' &&
    event instanceof globalThis.KeyboardEvent &&
    typeof event.getModifierState === 'function'
  ) {
    capsLockOn.value = event.getModifierState('CapsLock')
  }
}

function passwordBlur() {
  touched.password = true
  capsLockOn.value = false
}

onMounted(async () => {
  void refreshCaptcha()
  try {
    const saved = await readRememberedLogin()
    if (!active) return
    hadSaved = !!saved
    if (saved && !edited) {
      Object.assign(form, saved)
      remember.value = true
      if (captchaReady.value) {
        focusInput('captcha')
      } else {
        pendingCaptchaFocus = true
      }
      return
    }
  } catch {
    /* Remembering is optional; a disabled browser store never blocks login. */
  }
  if (!edited) focusInput('account')
})
onUnmounted(() => {
  active = false
})
function captchaImageEvent(event: globalThis.Event, loaded: boolean) {
  const id = (event.target as globalThis.HTMLImageElement).dataset.captchaId
  if (!id) return
  if (loaded) {
    imageLoaded(id)
    if (pendingCaptchaFocus && captchaReady.value) {
      pendingCaptchaFocus = false
      focusInput('captcha')
    }
  } else {
    pendingCaptchaFocus = false
    imageFailed(id)
  }
}
async function replaceCaptcha(): Promise<void> {
  if (busy.value || loginEstablished.value) return
  touched.captcha = false
  error.value = ''
  pendingCaptchaFocus = true
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
    pendingCaptchaFocus = Boolean(form.username.trim() && form.password)
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
              stroke="#9EC2F2"
              stroke-width="3"
              stroke-dasharray="6 5"
            />
            <rect
              x="32"
              y="38"
              width="146"
              height="104"
              rx="12"
              fill="white"
              stroke="#C5DBF5"
              stroke-width="2"
            />
            <rect x="46" y="52" width="118" height="69" rx="6" fill="#E6F0FC" />
            <path d="m96 71 22 14-22 14V71Z" fill="#2878E8" opacity="0.82" />
            <path d="M57 131h51" stroke="#B8D2F2" stroke-width="3.5" stroke-linecap="round" />
            <rect
              x="378"
              y="38"
              width="146"
              height="104"
              rx="12"
              fill="white"
              stroke="#C5DBF5"
              stroke-width="2"
            />
            <path d="M397 67h29v29h-29zM441 67h29v29h-29zM485 67h18v29h-18z" fill="#DCEBFC" />
            <path d="M397 114h102" stroke="#B8D2F2" stroke-width="3.5" stroke-linecap="round" />
            <rect
              x="55"
              y="215"
              width="146"
              height="89"
              rx="12"
              fill="white"
              stroke="#C5DBF5"
              stroke-width="2"
            />
            <path
              d="M77 278v-25m29 25v-40m29 40v-17m29 17v-34"
              stroke="#5A9CF0"
              stroke-width="12"
              stroke-linecap="round"
            />
            <rect
              x="368"
              y="215"
              width="146"
              height="89"
              rx="12"
              fill="white"
              stroke="#C5DBF5"
              stroke-width="2"
            />
            <path
              d="M390 239h18m12 0h71M390 259h18m12 0h55M390 279h18m12 0h71"
              stroke="#A9C8F0"
              stroke-width="5"
              stroke-linecap="round"
            />
            <rect
              x="225"
              y="119"
              width="110"
              height="110"
              rx="26"
              fill="white"
              stroke="#BBD5F6"
              stroke-width="2.5"
            />
            <image :href="brandIcon" x="246" y="140" width="68" height="68" />
          </svg>
        </div>
        <h2>{{ systemConfig.loginTagline }}</h2>
        <p class="visual-subtitle">{{ systemConfig.name }} {{ systemConfig.description }}</p>
        <div class="visual-highlights" aria-label="平台核心能力">
          <span v-for="item in visualHighlights" :key="item" class="highlight-pill">{{
            item
          }}</span>
        </div>
      </div>
    </section>
    <section class="login-form-region">
      <div class="login-card">
        <div class="mobile-brand">
          <img :src="brandIcon" alt="" width="34" height="34" />{{ systemConfig.name }}
        </div>
        <h1>账号登录</h1>
        <ElForm :model="form" label-position="top" size="large" @submit.prevent="submit">
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
              @keydown="checkCapsLock"
              @keyup="checkCapsLock"
              @blur="passwordBlur"
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
            <div v-if="capsLockOn && !passwordError" class="caps-lock-hint" role="status">
              已开启大写锁定 (Caps Lock)
            </div>
          </ElFormItem>
          <ElFormItem label="验证码" :error="captchaInputError" class="captcha-field">
            <div class="captcha-row">
              <ElInput
                id="captcha"
                v-model="captchaAnswer"
                name="captcha"
                aria-label="验证码"
                placeholder="请输入验证码"
                autocomplete="off"
                autocapitalize="off"
                :spellcheck="false"
                :maxlength="loginValidation.captcha.length"
                :disabled="busy || !captchaReady"
                :validate-event="false"
                @input="changed('captcha')"
                @blur="touched.captcha = true"
              />
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
  grid-template-columns: minmax(0, 1.18fr) minmax(420px, 1fr);
  background: #f8fafc;
}
.login-visual {
  position: relative;
  display: flex;
  flex-direction: column;
  padding: 36px 56px;
  background:
    radial-gradient(circle at 18% 22%, rgba(40, 120, 232, 0.11), transparent 46%),
    radial-gradient(circle at 82% 78%, rgba(59, 130, 246, 0.09), transparent 48%),
    linear-gradient(145deg, #eef4fc 0%, #e5effc 52%, #dce9fa 100%);
  border-right: 1px solid #e2eaf4;
  overflow: hidden;
}
.login-brand {
  display: inline-flex;
  align-items: center;
  gap: 12px;
  width: fit-content;
  font-size: 21px;
  font-weight: 600;
  color: #1e293b;
}
.visual-content {
  margin: auto;
  width: 100%;
  max-width: 620px;
  padding: 24px 0 48px;
  text-align: center;
}
.stream-illustration {
  max-width: 580px;
  margin: 0 auto 28px;
  filter: drop-shadow(0 12px 24px rgba(30, 64, 115, 0.06));
}
.stream-illustration svg {
  width: 100%;
  display: block;
}
.visual-content h2 {
  margin: 0 0 12px;
  color: #1e3a68;
  font-size: 28px;
  font-weight: 600;
  letter-spacing: 1.5px;
}
.visual-subtitle {
  margin: 0;
  font-size: 16px;
  color: #5b7290;
}
.visual-highlights {
  display: flex;
  flex-wrap: wrap;
  justify-content: center;
  gap: 10px;
  margin-top: 24px;
}
.highlight-pill {
  display: inline-flex;
  align-items: center;
  padding: 6px 14px;
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.78);
  border: 1px solid rgba(186, 210, 242, 0.85);
  color: #2c5282;
  font-size: 13px;
  font-weight: 500;
  box-shadow: 0 2px 6px rgba(30, 58, 104, 0.04);
}
.login-form-region {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 40px;
  min-width: 0;
  background: linear-gradient(180deg, #ffffff 0%, #f8fafc 100%);
}
.login-card {
  width: 100%;
  max-width: 440px;
  min-width: 0;
  padding: 36px 36px 32px;
  border: 1px solid #e2e8f0;
  border-radius: 16px;
  background: #fff;
  box-shadow:
    0 12px 36px -8px rgba(15, 23, 42, 0.07),
    0 4px 12px -2px rgba(15, 23, 42, 0.04);
}
h1 {
  margin: 0 0 28px;
  font-size: 24px;
  font-weight: 600;
  color: #0f172a;
  text-align: center;
}
.login-card :deep(.el-form-item) {
  margin-bottom: 20px;
}
.login-card :deep(.el-form-item__label) {
  padding-bottom: 6px;
  line-height: 1.4;
  font-size: 13px;
  font-weight: 500;
  color: #334155;
}
.login-card :deep(.el-input__wrapper) {
  transition: box-shadow 160ms ease;
}
.login-card :deep(.el-form-item__error) {
  line-height: 1.45;
  padding-top: 4px;
}
.caps-lock-hint {
  margin-top: 5px;
  font-size: 12px;
  line-height: 1.4;
  color: #d97706;
}
.login-options {
  display: flex;
  align-items: center;
  margin: -2px 0 20px;
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
  flex: 0 0 124px;
  height: 40px;
  padding: 0;
  border: 1px solid #cbd5e1;
  border-radius: 6px;
  overflow: hidden;
  background: #f8fafc;
  color: #64748b;
  cursor: pointer;
  transition:
    border-color 150ms ease,
    box-shadow 150ms ease;
}
.captcha-image:hover:not(:disabled) {
  border-color: var(--el-color-primary);
}
.captcha-image:focus-visible {
  outline: 2px solid var(--el-color-primary);
  outline-offset: 2px;
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
  font-size: 15px;
  font-weight: 500;
  border-radius: 8px;
}
.login-error {
  margin-bottom: 18px;
}
.mobile-brand {
  display: none;
}
@media (max-width: 960px) {
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
    padding: 30px 26px;
  }
  .captcha-image {
    flex-basis: 112px;
  }
}
@media (max-width: 768px) {
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
    margin-bottom: 20px;
    font-size: 20px;
    font-weight: 600;
    color: #0f172a;
  }
}
@media (max-width: 380px) {
  .login-form-region {
    padding: 12px;
  }
  .login-card {
    padding: 24px 18px;
  }
  .captcha-row {
    gap: 8px;
  }
  .captcha-image {
    flex-basis: 96px;
  }
}
@media (prefers-reduced-motion: reduce) {
  .login-card :deep(.el-input__wrapper),
  .captcha-image,
  .captcha-refresh-link {
    transition: none;
  }
}
</style>
