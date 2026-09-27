import { ApiRequestError } from '../../lib/http/error'

function serviceMessage(error: ApiRequestError): string | undefined {
  if (error.code === 'IP_BLOCKED') return '登录暂时受限，请联系管理员'
  if (error.status === 429 || error.code === 'RATE_LIMITED') return '操作过于频繁，请稍后重试'
  if (
    error.code === 'NETWORK_ERROR' ||
    error.code === 'REQUEST_TIMEOUT' ||
    (error.status !== undefined && error.status >= 500)
  )
    return '服务暂不可用，请稍后重试'
}

export function loginErrorMessage(error: unknown): string {
  if (!(error instanceof ApiRequestError)) return '登录失败，请重试'
  if (error.code === 'LOGIN_FAILED') return '账号或密码错误'
  if (error.code === 'CAPTCHA_INVALID') return '验证码错误'
  if (error.code === 'CSRF_INVALID' || error.status === 401) return '请重新输入验证码后登录'
  return serviceMessage(error) ?? '登录失败，请重试'
}

export function captchaErrorMessage(error: unknown): string {
  if (!error) return ''
  if (error instanceof ApiRequestError) {
    if (error.code === 'CAPTCHA_EXPIRED') return '验证码已过期，请点击图片刷新'
    const message = serviceMessage(error)
    if (message) return message
  }
  return '验证码加载失败，请点击图片重试'
}
