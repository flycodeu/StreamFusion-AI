import { loginValidation } from '../config/validation'

export function accountHint(value: string): string {
  const { minLength, maxLength, pattern } = loginValidation.account
  if (!value) return '请输入账号'
  if (!pattern.test(value)) return '账号只能包含英文字母和数字'
  if (value.length < minLength)
    return `账号至少 ${minLength} 位，还需输入 ${minLength - value.length} 位`
  if (value.length > maxLength) return `账号不能超过 ${maxLength} 位`
  return ''
}
export function passwordHint(value: string): string {
  const { minLength, maxLength } = loginValidation.password
  if (!value) return '请输入密码'
  if (Array.from(value).length < minLength) return `密码至少 ${minLength} 位，请检查是否输入完整`
  if (Array.from(value).length > maxLength) return '密码长度超出限制'
  return ''
}

export function captchaHint(value: string): string {
  if (!value.trim()) return '请输入验证码'
  return loginValidation.captcha.pattern.test(value.trim())
    ? ''
    : `请输入 ${loginValidation.captcha.length} 位字母或数字`
}
