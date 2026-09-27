import { computed, onScopeDispose, ref, shallowRef } from 'vue'
import { createLoginCaptcha, captchaImageSource } from '../../api/auth/captcha'
import type { CaptchaChallenge, LoginCaptchaAnswer } from '../../api/auth/captcha'
import { ApiRequestError } from '../../lib/http/error'
import { refreshCsrf } from '../../session/session'
import { sessionState } from '../../session/state'

/** One challenge at a time: both the JSON and its image must be ready before submitting. */
export function useLoginCaptcha() {
  const challenge = shallowRef<CaptchaChallenge | null>(null)
  const answer = ref('')
  const requesting = ref(false)
  const imageReady = ref(false)
  const error = shallowRef<unknown>(null)
  const ready = computed(() => !!challenge.value && imageReady.value && !requesting.value)
  const imageSource = computed(() => (challenge.value ? captchaImageSource(challenge.value) : ''))
  let disposed = false
  let revision = 0
  let controller: AbortController | undefined
  let expiryTimer: ReturnType<typeof setTimeout> | undefined
  let imageTimer: ReturnType<typeof setTimeout> | undefined

  function clearTimers() {
    clearTimeout(expiryTimer)
    clearTimeout(imageTimer)
  }

  function invalidate(code: 'CAPTCHA_EXPIRED' | 'CAPTCHA_UNAVAILABLE', message: string) {
    clearTimers()
    challenge.value = null
    imageReady.value = false
    answer.value = ''
    error.value = new ApiRequestError(code, message)
  }

  async function refresh(): Promise<void> {
    // Serial generation prevents an older server response replacing a newer session challenge.
    if (disposed || requesting.value || (challenge.value && !imageReady.value)) return
    const current = ++revision
    controller?.abort()
    controller = new AbortController()
    const signal = controller.signal
    clearTimers()
    challenge.value = null
    imageReady.value = false
    answer.value = ''
    error.value = null
    requesting.value = true
    try {
      // A cached token can belong to a revoked session. CSRF preparation restores anonymous access.
      await refreshCsrf(signal)
      if (disposed || current !== revision) return
      let next: CaptchaChallenge
      try {
        next = await createLoginCaptcha(signal)
      } catch (cause) {
        if (
          !(cause instanceof ApiRequestError) ||
          !['UNAUTHORIZED', 'CSRF_INVALID'].includes(cause.code) ||
          disposed ||
          current !== revision ||
          sessionState.me
        )
          throw cause
        // The cookie may expire between preparation and creation; recover once, never loop.
        await refreshCsrf(signal)
        if (disposed || current !== revision) return
        next = await createLoginCaptcha(signal)
      }
      if (disposed || current !== revision) return
      const remaining = new Date(next.expiresAt).getTime() - Date.now()
      if (remaining <= 0) {
        invalidate('CAPTCHA_EXPIRED', '验证码已过期，请点击图片刷新')
        return
      }
      challenge.value = next
      expiryTimer = setTimeout(
        () => invalidate('CAPTCHA_EXPIRED', '验证码已过期，请点击图片刷新'),
        Math.min(remaining, 120000),
      )
      imageTimer = setTimeout(() => imageFailed(next.captchaId), 8000)
    } catch (cause) {
      if (!disposed && current === revision) error.value = cause
    } finally {
      if (!disposed && current === revision) requesting.value = false
    }
  }

  function imageLoaded(captchaId: string) {
    if (disposed || challenge.value?.captchaId !== captchaId) return
    clearTimeout(imageTimer)
    if (new Date(challenge.value.expiresAt).getTime() <= Date.now()) {
      invalidate('CAPTCHA_EXPIRED', '验证码已过期，请点击图片刷新')
      return
    }
    imageReady.value = true
  }

  function imageFailed(captchaId: string) {
    if (disposed || challenge.value?.captchaId !== captchaId) return
    invalidate('CAPTCHA_UNAVAILABLE', '验证码加载失败，请点击图片重试')
  }

  function submission(): LoginCaptchaAnswer | null {
    if (!ready.value || !challenge.value) return null
    if (new Date(challenge.value.expiresAt).getTime() <= Date.now()) {
      invalidate('CAPTCHA_EXPIRED', '验证码已过期，请点击图片刷新')
      return null
    }
    return { captchaId: challenge.value.captchaId, captchaAnswer: answer.value.trim() }
  }

  onScopeDispose(() => {
    disposed = true
    revision++
    controller?.abort()
    clearTimers()
  })

  return {
    challenge,
    answer,
    requesting,
    imageReady,
    imageSource,
    ready,
    error,
    refresh,
    imageLoaded,
    imageFailed,
    submission,
  }
}
