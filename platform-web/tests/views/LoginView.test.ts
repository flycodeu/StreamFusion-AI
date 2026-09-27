// @vitest-environment vue-renderer
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import type { App } from 'vue'
import LoginView from '../../src/pages/auth/LoginView.vue'
import { node, nodes, renderer, text } from '../support/renderer'
import type { Host } from '../support/renderer'
import { ApiRequestError } from '../../src/lib/http/error'
import { clearIdentity, sessionState } from '../../src/session/state'

const api = vi.hoisted(() => ({
  create: vi.fn(),
  login: vi.fn(),
  csrf: vi.fn(),
  replace: vi.fn(),
  read: vi.fn(),
  save: vi.fn(),
  forget: vi.fn(),
  warning: vi.fn(),
}))
vi.mock('../../src/api/auth/captcha', async (actual) => ({
  ...(await actual<typeof import('../../src/api/auth/captcha')>()),
  createLoginCaptcha: api.create,
}))
vi.mock('../../src/session/session', () => ({ signIn: api.login, refreshCsrf: api.csrf }))
vi.mock('../../src/session/rememberedLogin', () => ({
  readRememberedLogin: api.read,
  saveRememberedLogin: api.save,
  forgetRememberedLogin: api.forget,
}))
vi.mock('vue-router', () => ({
  useRouter: () => ({ replace: api.replace }),
  useRoute: () => ({ query: {} }),
}))
vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    ElMessage: { warning: api.warning },
    ...Object.fromEntries(
      ['ElAlert', 'ElButton', 'ElCheckbox', 'ElForm', 'ElFormItem', 'ElInput'].map((name) => [
        name,
        defineComponent({
          inheritAttrs: false,
          setup(_, { attrs, slots }) {
            return () => h(name, attrs, slots.default?.())
          },
        }),
      ]),
    ),
  }
})

const firstId = '0123456789abcdef0123456789abcdef'
const secondId = 'abcdef0123456789abcdef0123456789'
const challenge = (captchaId = firstId) => ({
  captchaId,
  expiresAt: new Date(Date.now() + 120000).toISOString(),
  imageUrl: `/auth/captcha/${captchaId}/image`,
})
let app: App | null = null
const control = (root: Host, id: string) => nodes(root).find((n) => n.props.id === id)!
const submitButton = (root: Host) => nodes(root).find((n) => n.type === 'ElButton')!
const refreshButton = (root: Host) =>
  nodes(root).find((n) => n.props['aria-label'] === '刷新图片验证码')!
const imageNode = (root: Host) => nodes(root).find((n) => n.props['data-captcha-id'])!
const formNode = (root: Host) => nodes(root).find((n) => n.type === 'ElForm')!
const setValue = async (root: Host, id: string, value: string) => {
  ;(control(root, id).props['onUpdate:modelValue'] as (value: string) => void)(value)
  await nextTick()
}
const submit = (root: Host) =>
  (formNode(root).props.onSubmit as (event: { preventDefault: () => void }) => Promise<void>)({
    preventDefault() {},
  })
const loadImage = async (root: Host) => {
  const img = imageNode(root)
  ;(img.props.onLoad as (event: unknown) => void)({
    target: { dataset: { captchaId: img.props['data-captcha-id'] } },
  })
  await nextTick()
}
async function mount(waitForImage = true) {
  const root = node('root')
  app = renderer.createApp(LoginView)
  app.mount(root)
  if (waitForImage) await vi.waitFor(() => expect(imageNode(root)).toBeDefined())
  return root
}
async function fill(root: Host) {
  await setValue(root, 'account', 'operator')
  await setValue(root, 'password', 'ExamplePass1!')
  await setValue(root, 'captcha', 'A2B3C')
}

beforeEach(() => {
  clearIdentity()
  sessionState.csrf = { headerName: 'X-CSRF-TOKEN', token: 'csrf' }
  for (const fn of Object.values(api)) fn.mockReset().mockResolvedValue(undefined)
  api.create.mockResolvedValue(challenge())
})
afterEach(() => {
  app?.unmount()
  app = null
  vi.unstubAllGlobals()
})

describe('login form captcha interaction', () => {
  it('keeps login and replacement disabled until the first image loads', async () => {
    const root = await mount()
    expect(submitButton(root).props.disabled).toBe(true)
    expect(refreshButton(root).props.disabled).toBe(true)
    await fill(root)
    await submit(root)
    expect(api.login).not.toHaveBeenCalled()
    await loadImage(root)
    expect(submitButton(root).props.disabled).toBe(false)
    expect(refreshButton(root).props.disabled).toBe(false)
  })

  it('submits captcha with credentials and prevents a duplicate login', async () => {
    const root = await mount()
    await loadImage(root)
    await fill(root)
    let finish!: () => void
    api.login.mockReturnValue(
      new Promise<void>((resolve) => {
        finish = resolve
      }),
    )
    const pending = submit(root)
    await submit(root)
    expect(api.login).toHaveBeenCalledExactlyOnceWith('operator', 'ExamplePass1!', {
      captchaId: firstId,
      captchaAnswer: 'A2B3C',
    })
    finish()
    await pending
    expect(api.replace).toHaveBeenCalledWith('/home')
    expect(api.create).toHaveBeenCalledOnce()
    expect(api.save).not.toHaveBeenCalled()
  })

  it('clears and replaces the consumed code after a rejected login', async () => {
    const root = await mount()
    await loadImage(root)
    await fill(root)
    api.login.mockRejectedValue(new ApiRequestError('CAPTCHA_INVALID', 'invalid', { status: 400 }))
    api.create.mockResolvedValue(challenge(secondId))
    await submit(root)
    await nextTick()
    expect(control(root, 'captcha').props.modelValue).toBe('')
    expect(imageNode(root).props['data-captcha-id']).toBe(secondId)
    expect(submitButton(root).props.disabled).toBe(true)
    expect(nodes(root).find((n) => n.type === 'ElAlert')?.props.title).toBe('验证码错误')
    expect(api.replace).not.toHaveBeenCalled()
  })

  it('offers a keyboard-accessible refresh button and retries an image error', async () => {
    const root = await mount()
    const img = imageNode(root)
    ;(img.props.onError as (event: unknown) => void)({
      target: { dataset: { captchaId: firstId } },
    })
    await nextTick()
    expect(nodes(root).find((n) => n.type === 'ElAlert')?.props.title).toBe(
      '验证码加载失败，请点击图片重试',
    )
    expect(refreshButton(root).type).toBe('button')
    expect(refreshButton(root).props.type).toBe('button')
    expect(refreshButton(root).props.disabled).toBe(false)
    await (refreshButton(root).props.onClick as () => Promise<void>)()
    await nextTick()
    expect(api.create).toHaveBeenCalledTimes(2)
    expect(imageNode(root)).toBeDefined()
  })

  it('does not request an anonymous captcha when navigation fails after login', async () => {
    const root = await mount()
    await loadImage(root)
    await fill(root)
    api.replace.mockRejectedValue(new Error('navigation failed'))
    await submit(root)
    await nextTick()
    expect(api.create).toHaveBeenCalledOnce()
    expect(nodes(root).find((n) => n.type === 'ElAlert')?.props.title).toContain('已完成登录')
    expect(text(submitButton(root))).toBe('刷新页面')
  })

  it('offers reload without generating another captcha after the authenticated identity read fails', async () => {
    const root = await mount()
    await loadImage(root)
    await fill(root)
    vi.stubGlobal('location', { reload: vi.fn() })
    api.login.mockRejectedValue(
      new ApiRequestError('LOGIN_RESTORE_FAILED', 'read failed', { traceId: firstId }),
    )
    await submit(root)
    await nextTick()
    expect(api.create).toHaveBeenCalledOnce()
    expect(text(root)).not.toContain(firstId)
    expect(text(submitButton(root))).toBe('刷新页面')
    ;(submitButton(root).props.onClick as () => void)()
    expect(globalThis.location.reload).toHaveBeenCalledOnce()
    await submit(root)
    expect(api.login).toHaveBeenCalledOnce()
  })

  it('does not navigate or save credentials after the form has been removed', async () => {
    const root = await mount()
    await loadImage(root)
    await fill(root)
    let finish!: () => void
    api.login.mockReturnValue(
      new Promise<void>((resolve) => {
        finish = resolve
      }),
    )
    const pending = submit(root)
    app?.unmount()
    app = null
    finish()
    await pending
    expect(api.replace).not.toHaveBeenCalled()
    expect(api.save).not.toHaveBeenCalled()
  })

  it.each([
    ['LOGIN_FAILED', 401, '账号或密码错误'],
    ['CAPTCHA_INVALID', 400, '验证码错误'],
    ['INTERNAL_ERROR', 500, '服务暂不可用，请稍后重试'],
    ['UNAUTHORIZED', 401, '请重新输入验证码后登录'],
  ])(
    'shows only the login message for %s without server diagnostics',
    async (code, status, message) => {
      const root = await mount()
      await loadImage(root)
      await fill(root)
      api.login.mockRejectedValue(
        new ApiRequestError(code, '服务内部信息，请先登录', { status, traceId: firstId }),
      )
      await submit(root)
      await nextTick()
      expect(nodes(root).find((n) => n.type === 'ElAlert')?.props.title).toBe(message)
      expect(text(root)).not.toMatch(/请求标识|排查信息|会话已失效|请先登录|服务内部信息/)
      expect(text(root)).not.toContain(firstId)
    },
  )

  it('shows a retryable captcha message instead of an authentication warning on the login page', async () => {
    api.create.mockRejectedValue(
      new ApiRequestError('UNAUTHORIZED', '请先登录', { status: 401, traceId: firstId }),
    )
    const root = await mount(false)
    await vi.waitFor(() =>
      expect(nodes(root).find((n) => n.type === 'ElAlert')?.props.title).toBe(
        '验证码加载失败，请点击图片重试',
      ),
    )
    expect(api.create).toHaveBeenCalledTimes(2)
    expect(text(root)).not.toMatch(/请求标识|排查信息|会话已失效|请先登录/)
    expect(submitButton(root).props.disabled).toBe(true)
    expect(refreshButton(root).props.disabled).toBe(false)
  })
})
