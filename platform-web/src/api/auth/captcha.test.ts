import { describe, expect, it, vi } from 'vitest'
import { captchaImageSource, createLoginCaptcha, parseCaptcha } from './captcha'

const request = vi.hoisted(() => vi.fn())
vi.mock('../client', () => ({ request }))

const challenge = {
  captchaId: '0123456789abcdef0123456789abcdef',
  expiresAt: '2026-09-27T12:00:00Z',
  imageUrl: '/auth/captcha/0123456789abcdef0123456789abcdef/image',
}

describe('login captcha contract', () => {
  it('accepts only the image endpoint belonging to its challenge', () => {
    expect(parseCaptcha(challenge)).toEqual(challenge)
    expect(captchaImageSource(challenge)).toBe(`/api${challenge.imageUrl}`)
  })

  it.each([
    { imageUrl: 'https://baidu.com/image.png' },
    { imageUrl: '//baidu.com/image.png' },
    { imageUrl: `${challenge.imageUrl}?redirect=https://baidu.com` },
    { imageUrl: '/auth/captcha/11111111111111111111111111111111/image' },
    { captchaId: '../image' },
    { expiresAt: '2026-09-27 12:00:00' },
    { expiresAt: '2026-02-30T12:00:00Z' },
  ])('rejects a malformed or redirected challenge: %j', (invalid) => {
    expect(() => parseCaptcha({ ...challenge, ...invalid })).toThrow()
    expect(() => captchaImageSource({ ...challenge, ...invalid })).toThrow()
  })

  it('uses the shared authenticated transport and passes cancellation to metadata requests', async () => {
    request.mockResolvedValueOnce({ data: challenge })
    const controller = new AbortController()
    expect(await createLoginCaptcha(controller.signal)).toEqual(challenge)
    expect(request).toHaveBeenCalledExactlyOnceWith({
      path: '/auth/captcha',
      method: 'POST',
      successStatus: 200,
      signal: controller.signal,
      decode: parseCaptcha,
    })
  })
})
