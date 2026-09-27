import { request } from '../client'
import { empty, object, string } from '../parse'
import type { CsrfToken } from '../../lib/http/types'
import { encryptLogin } from './cipher'
import { parseAuthUser, parseProfile } from './types'
import type { AuthUser, UserProfile } from './types'
import type { LoginCaptchaAnswer } from './captcha'

export async function getCsrf(signal?: AbortSignal): Promise<CsrfToken> {
  return (
    await request({
      path: '/auth/csrf',
      method: 'GET',
      successStatus: 200,
      signal,
      decode(value) {
        const row = object(value)
        return { headerName: string(row.headerName), token: string(row.token) }
      },
    })
  ).data
}

export async function getMe(): Promise<AuthUser> {
  return (
    await request({ path: '/auth/me', method: 'GET', successStatus: 200, decode: parseAuthUser })
  ).data
}

export async function login(
  username: string,
  password: string,
  captcha: LoginCaptchaAnswer,
): Promise<void> {
  const challenge = (
    await request({
      path: '/auth/login/challenge',
      method: 'GET',
      successStatus: 200,
      decode(value) {
        const row = object(value)
        return {
          challengeId: string(row.challengeId),
          serverPublicKey: string(row.serverPublicKey),
        }
      },
    })
  ).data
  const body = await encryptLogin(challenge, username, password, captcha)
  await request({
    path: '/auth/login/secure',
    method: 'POST',
    body,
    successStatus: 200,
    decode: empty,
  })
}

export async function logout(): Promise<void> {
  await request({ path: '/auth/logout', method: 'POST', successStatus: 200, decode: empty })
}

export async function saveProfile(input: UserProfile): Promise<UserProfile> {
  return (
    await request({
      path: '/auth/me',
      method: 'PUT',
      body: {
        nickname: input.nickname,
        avatarKey: input.avatarKey,
        phone: input.phone,
        email: input.email,
        gender: input.gender,
        version: input.version,
      },
      successStatus: 200,
      decode: parseProfile,
    })
  ).data
}

export async function changePassword(currentPassword: string, newPassword: string): Promise<void> {
  await request({
    path: '/auth/password',
    method: 'PUT',
    body: { currentPassword, newPassword },
    successStatus: 200,
    decode: empty,
  })
}
