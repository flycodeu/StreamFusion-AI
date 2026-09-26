export function accountHint(value: string): string {
  if (!value) return '请输入账号'
  if (/[^A-Za-z0-9]/.test(value)) return '账号只能包含英文字母和数字'
  if (value.length < 4) return `账号至少 4 位，还需输入 ${4 - value.length} 位`
  if (value.length > 32) return '账号不能超过 32 位'
  return ''
}
export function passwordHint(value: string): string {
  if (!value) return '请输入密码'
  if (Array.from(value).length < 8) return '密码至少 8 位，请检查是否输入完整'
  if (Array.from(value).length > 128) return '密码长度超出限制'
  return ''
}
