import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope } from 'vue'
import type { EffectScope } from 'vue'
import type { CaptchaChallenge } from '../../api/auth/captcha'
import { useLoginCaptcha } from './useLoginCaptcha'
import { clearIdentity, sessionState } from '../../session/state'
import { ApiRequestError } from '../../lib/http/error'

const api = vi.hoisted(() => ({ create: vi.fn(), csrf: vi.fn() }))
vi.mock('../../api/auth/captcha', async (actual) => ({
  ...(await actual<typeof import('../../api/auth/captcha')>()),
  createLoginCaptcha: api.create,
}))
vi.mock('../../session/session', () => ({ refreshCsrf: api.csrf }))

const firstId = '0123456789abcdef0123456789abcdef'
const secondId = 'abcdef0123456789abcdef0123456789'
const challenge = (captchaId = firstId): CaptchaChallenge => ({
  captchaId,
  expiresAt: new Date(Date.now() + 120000).toISOString(),
  imageUrl: `/auth/captcha/${captchaId}/image`,
})
let scope: EffectScope
let captcha: ReturnType<typeof useLoginCaptcha>
beforeEach(() => {
  clearIdentity()
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-09-27T12:00:00Z'))
  api.create.mockReset().mockResolvedValue(challenge())
  api.csrf.mockReset().mockResolvedValue(undefined)
  sessionState.csrf = { headerName: 'X-CSRF-TOKEN', token: 'csrf' }
  scope = effectScope()
  captcha = scope.run(useLoginCaptcha)!
})
afterEach(() => {
  scope.stop()
  vi.useRealTimers()
})

describe('login captcha lifecycle', () => {
  it('prepares an anonymous session even when a previous CSRF token is still cached', async () => {
    await captcha.refresh()
    expect(api.csrf).toHaveBeenCalledOnce()
    expect(api.csrf.mock.invocationCallOrder[0]).toBeLessThan(
      api.create.mock.invocationCallOrder[0]!,
    )
  })

  it.each(['UNAUTHORIZED', 'CSRF_INVALID'])(
    'recovers once when captcha creation rejects stale session state with %s',
    async (code) => {
      api.create.mockRejectedValueOnce(new ApiRequestError(code, 'internal detail'))
      await captcha.refresh()
      expect(api.csrf).toHaveBeenCalledTimes(2)
      expect(api.create).toHaveBeenCalledTimes(2)
      expect(captcha.error.value).toBeNull()
      expect(captcha.challenge.value?.captchaId).toBe(firstId)
    },
  )

  it('stops recovery after one rejected retry and leaves manual retry available', async () => {
    api.create.mockRejectedValue(new ApiRequestError('UNAUTHORIZED', '请先登录', { status: 401 }))
    await captcha.refresh()
    expect(api.create).toHaveBeenCalledTimes(2)
    expect(api.csrf).toHaveBeenCalledTimes(2)
    expect(captcha.requesting.value).toBe(false)
    expect(captcha.challenge.value).toBeNull()
    api.create.mockResolvedValue(challenge())
    await captcha.refresh()
    expect(captcha.error.value).toBeNull()
  })

  it('does not repeat creation for rate limits or a dependency outage', async () => {
    api.create.mockRejectedValue(
      new ApiRequestError('DEPENDENCY_UNAVAILABLE', 'unavailable', { status: 503 }),
    )
    await captcha.refresh()
    expect(api.csrf).toHaveBeenCalledOnce()
    expect(api.create).toHaveBeenCalledOnce()
    expect(captcha.error.value).toMatchObject({ code: 'DEPENDENCY_UNAVAILABLE' })
  })

  it('requires CSRF and a loaded matching image before it can be submitted', async () => {
    sessionState.csrf = null
    await captcha.refresh()
    expect(api.csrf).toHaveBeenCalledWith(api.create.mock.calls[0]![0])
    expect(captcha.ready.value).toBe(false)
    expect(captcha.submission()).toBeNull()
    captcha.imageLoaded(secondId)
    expect(captcha.ready.value).toBe(false)
    captcha.imageLoaded(firstId)
    captcha.answer.value = 'aB123'
    expect(captcha.ready.value).toBe(true)
    expect(captcha.submission()).toEqual({ captchaId: firstId, captchaAnswer: 'aB123' })
  })

  it('does not overlap generation while either metadata or the image is loading', async () => {
    let finish!: (value: CaptchaChallenge) => void
    api.create.mockReturnValue(
      new Promise<CaptchaChallenge>((resolve) => {
        finish = resolve
      }),
    )
    const pending = captcha.refresh()
    await captcha.refresh()
    expect(api.create).toHaveBeenCalledOnce()
    finish(challenge())
    await pending
    await captcha.refresh()
    expect(api.create).toHaveBeenCalledOnce()
    captcha.imageLoaded(firstId)
    api.create.mockResolvedValue(challenge(secondId))
    await captcha.refresh()
    expect(api.create).toHaveBeenCalledTimes(2)
  })

  it('clears the answer on refresh and ignores load or error events from an older image', async () => {
    await captcha.refresh()
    captcha.imageLoaded(firstId)
    captcha.answer.value = 'A2B3C'
    api.create.mockResolvedValue(challenge(secondId))
    await captcha.refresh()
    expect(captcha.answer.value).toBe('')
    captcha.imageLoaded(firstId)
    captcha.imageFailed(firstId)
    expect(captcha.ready.value).toBe(false)
    expect(captcha.error.value).toBeNull()
    captcha.imageLoaded(secondId)
    expect(captcha.ready.value).toBe(true)
  })

  it.each(['error', 'timeout'] as const)(
    'allows another request after image %s',
    async (failure) => {
      await captcha.refresh()
      if (failure === 'error') captcha.imageFailed(firstId)
      else await vi.advanceTimersByTimeAsync(8000)
      expect(captcha.ready.value).toBe(false)
      expect(captcha.challenge.value).toBeNull()
      expect(captcha.error.value).toMatchObject({ code: 'CAPTCHA_UNAVAILABLE' })
      await captcha.refresh()
      captcha.imageLoaded(firstId)
      expect(captcha.ready.value).toBe(true)
      expect(captcha.error.value).toBeNull()
    },
  )

  it('expires the image and answer together', async () => {
    await captcha.refresh()
    captcha.imageLoaded(firstId)
    captcha.answer.value = 'A2B3C'
    await vi.advanceTimersByTimeAsync(120000)
    expect(captcha.answer.value).toBe('')
    expect(captcha.challenge.value).toBeNull()
    expect(captcha.submission()).toBeNull()
    expect(captcha.error.value).toMatchObject({ code: 'CAPTCHA_EXPIRED' })
  })

  it('rechecks expiry when a suspended tab submits before the timer runs', async () => {
    await captcha.refresh()
    captcha.imageLoaded(firstId)
    vi.setSystemTime(Date.now() + 120001)
    expect(captcha.submission()).toBeNull()
    expect(captcha.ready.value).toBe(false)
  })

  it('shows metadata errors and can retry', async () => {
    api.create.mockRejectedValueOnce(new Error('unavailable'))
    await captcha.refresh()
    expect(captcha.error.value).toMatchObject({ message: 'unavailable' })
    expect(captcha.requesting.value).toBe(false)
    await captcha.refresh()
    expect(captcha.error.value).toBeNull()
  })

  it('aborts metadata on disposal and ignores a late response', async () => {
    let finish!: (value: CaptchaChallenge) => void
    api.create.mockReturnValue(
      new Promise<CaptchaChallenge>((resolve) => {
        finish = resolve
      }),
    )
    const pending = captcha.refresh()
    await Promise.resolve()
    const signal = api.create.mock.calls[0]![0] as AbortSignal
    scope.stop()
    expect(signal.aborted).toBe(true)
    finish(challenge())
    await pending
    expect(captcha.challenge.value).toBeNull()
    await captcha.refresh()
    expect(api.create).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('does not request metadata after disposal during the CSRF refresh', async () => {
    sessionState.csrf = null
    let finish!: () => void
    api.csrf.mockReturnValue(
      new Promise<void>((resolve) => {
        finish = resolve
      }),
    )
    const pending = captcha.refresh()
    scope.stop()
    finish()
    await pending
    expect(api.create).not.toHaveBeenCalled()
  })
})
