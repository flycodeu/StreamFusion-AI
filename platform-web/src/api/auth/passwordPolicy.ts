import { request } from '../client'
import { boolean, integer, object } from '../parse'
export interface PasswordPolicy {
  minLength: number
  maxLength: number
  requireUppercase: boolean
  requireLowercase: boolean
  requireDigit: boolean
  requireSymbol: boolean
}
export function parsePasswordPolicy(value: unknown): PasswordPolicy {
  const row = object(value)
  const policy = {
    minLength: integer(row.minLength),
    maxLength: integer(row.maxLength),
    requireUppercase: boolean(row.requireUppercase),
    requireLowercase: boolean(row.requireLowercase),
    requireDigit: boolean(row.requireDigit),
    requireSymbol: boolean(row.requireSymbol),
  }
  if (policy.minLength < 8 || policy.maxLength < policy.minLength || policy.maxLength > 128)
    throw new Error('password policy')
  return policy
}
export async function getPasswordPolicy(): Promise<PasswordPolicy> {
  return (
    await request({
      path: '/auth/password-policy',
      method: 'GET',
      successStatus: 200,
      decode: parsePasswordPolicy,
    })
  ).data
}
