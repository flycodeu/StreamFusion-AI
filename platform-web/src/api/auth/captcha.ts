import { request } from '../client'
import { object, string } from '../parse'
import { isUtcTimestamp } from '../../lib/http/error'

export interface CaptchaChallenge {
  captchaId: string
  expiresAt: string
  imageUrl: string
}

export interface LoginCaptchaAnswer {
  captchaId: string
  captchaAnswer: string
}

export function parseCaptcha(value: unknown): CaptchaChallenge {
  const row = object(value)
  const captchaId = string(row.captchaId)
  const expiresAt = string(row.expiresAt)
  const imageUrl = string(row.imageUrl)
  if (
    !/^[a-f0-9]{32}$/.test(captchaId) ||
    !isUtcTimestamp(expiresAt) ||
    imageUrl !== `/auth/captcha/${captchaId}/image`
  )
    throw new Error('captcha')
  return { captchaId, expiresAt, imageUrl }
}

export async function createLoginCaptcha(signal: AbortSignal): Promise<CaptchaChallenge> {
  return (
    await request({
      path: '/auth/captcha',
      method: 'POST',
      successStatus: 200,
      signal,
      decode: parseCaptcha,
    })
  ).data
}

/** PNG uses the same session cookie as the JSON request; arbitrary image URLs are never accepted. */
export function captchaImageSource(challenge: CaptchaChallenge): string {
  const safe = parseCaptcha(challenge)
  return (import.meta.env.DEV ? '/api' : '') + safe.imageUrl
}
