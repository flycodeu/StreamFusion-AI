import type { PasswordPolicy } from '../../api/auth/passwordPolicy'
export function passwordChecks(value: string, policy: PasswordPolicy) {
  const length = Array.from(value).length
  return [
    {
      label: `${policy.minLength}～${policy.maxLength} 位`,
      valid: length >= policy.minLength && length <= policy.maxLength,
    },
    ...(policy.requireUppercase ? [{ label: '大写字母', valid: /[A-Z]/.test(value) }] : []),
    ...(policy.requireLowercase ? [{ label: '小写字母', valid: /[a-z]/.test(value) }] : []),
    ...(policy.requireDigit ? [{ label: '数字', valid: /[0-9]/.test(value) }] : []),
    ...(policy.requireSymbol ? [{ label: '符号', valid: /[^\p{L}\p{Nd}]/u.test(value) }] : []),
    { label: '无空格或控制字符', valid: !!value && !/[\p{Cc}\p{Z}]/u.test(value) },
  ]
}
export function newPasswordError(value: string, current: string, policy: PasswordPolicy): string {
  if (!value) return '请输入新密码'
  const failed = passwordChecks(value, policy).filter((check) => !check.valid)
  if (failed.length) return `新密码需要满足：${failed.map((check) => check.label).join('、')}`
  return value === current ? '新密码不能与当前密码相同' : ''
}
