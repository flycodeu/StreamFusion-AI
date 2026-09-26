import { describe, expect, it } from 'vitest'
import { accountHint, passwordHint } from './loginValidation'
describe('login field feedback matches backend account rules', () => {
  it('distinguishes missing, short, invalid and oversized accounts', () => {
    expect(accountHint('')).toBe('请输入账号')
    expect(accountHint('abc')).toContain('还需输入 1 位')
    expect(accountHint('user_name')).toContain('英文字母和数字')
    expect(accountHint('a'.repeat(33))).toContain('32')
    expect(accountHint('admin')).toBe('')
  })
  it('checks completeness without imposing new-password strength rules on login', () => {
    expect(passwordHint('')).toBe('请输入密码')
    expect(passwordHint('short')).toContain('8')
    expect(passwordHint('abc12345')).toBe('')
    expect(passwordHint('🙂'.repeat(128))).toBe('')
    expect(passwordHint('a'.repeat(129))).toContain('超出')
  })
})
