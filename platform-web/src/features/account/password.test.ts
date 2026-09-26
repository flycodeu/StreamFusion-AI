import { describe, expect, it } from 'vitest'
import { parsePasswordPolicy } from '../../api/auth/passwordPolicy'
import { newPasswordError, passwordChecks } from './password'

const policy = {
  minLength: 8,
  maxLength: 64,
  requireUppercase: true,
  requireLowercase: true,
  requireDigit: true,
  requireSymbol: true,
}
describe('password feedback follows server policy', () => {
  it('uses configured requirements instead of fixed defaults', () => {
    const relaxed = {
      ...policy,
      minLength: 12,
      requireUppercase: false,
      requireDigit: false,
      requireSymbol: false,
    }
    expect(newPasswordError('abcdefghijkl', 'old', relaxed)).toBe('')
    expect(newPasswordError('abcdefgh', 'old', relaxed)).toContain('12～64')
    expect(passwordChecks('abcdefghijkl', relaxed).map((c) => c.label)).not.toContain('数字')
  })
  it('counts Unicode code points and rejects invisible spacing/control characters', () => {
    const relaxed = {
      ...policy,
      minLength: 8,
      maxLength: 8,
      requireUppercase: false,
      requireLowercase: false,
      requireDigit: false,
      requireSymbol: false,
    }
    expect(newPasswordError('😀'.repeat(8), 'old', relaxed)).toBe('')
    expect(newPasswordError('😀'.repeat(4), 'old', relaxed)).toContain('8～8')
    expect(newPasswordError('Aa1!abcd\u00a0', 'old', policy)).toContain('无空格')
    expect(newPasswordError('Aa1!abcd\u0001', 'old', policy)).toContain('控制字符')
  })
  it('rejects the unchanged password and missing requirements', () => {
    expect(newPasswordError('Valid12!', 'Valid12!', policy)).toContain('不能与当前密码相同')
    expect(newPasswordError('abcdefgh', 'old', policy)).toContain('大写字母、数字、符号')
  })
  it('rejects malformed or inconsistent backend rules', () => {
    expect(() => parsePasswordPolicy({ ...policy, minLength: 65 })).toThrow()
    expect(() => parsePasswordPolicy({ ...policy, requireSymbol: 'true' })).toThrow()
    expect(parsePasswordPolicy({ ...policy, maxLength: 128 }).maxLength).toBe(128)
  })
})
