import { describe, expect, it, vi } from 'vitest'
import { webcrypto } from 'node:crypto'
import { encryptLogin } from './cipher'

function b64(bytes: Uint8Array): string {
  return Buffer.from(bytes).toString('base64url')
}

describe('encrypted login envelope', () => {
  it('seals both account and password for a one-time server challenge', async () => {
    vi.stubGlobal('crypto', webcrypto)
    const server = (await webcrypto.subtle.generateKey(
      { name: 'ECDH', namedCurve: 'P-256' },
      true,
      ['deriveBits'],
    )) as CryptoKeyPair
    const challengeId = '0123456789abcdef0123456789abcdef'
    const body = await encryptLogin(
      {
        challengeId,
        serverPublicKey: b64(
          new Uint8Array(await webcrypto.subtle.exportKey('spki', server.publicKey)),
        ),
      },
      'Admin01',
      'AdminPass1!',
    )
    expect(JSON.stringify(body)).not.toContain('Admin01')
    expect(JSON.stringify(body)).not.toContain('AdminPass1!')
    const client = await webcrypto.subtle.importKey(
      'spki',
      Buffer.from(body.clientPublicKey, 'base64url'),
      { name: 'ECDH', namedCurve: 'P-256' },
      false,
      [],
    )
    const secret = await webcrypto.subtle.deriveBits(
      { name: 'ECDH', public: client },
      server.privateKey,
      256,
    )
    const material = await webcrypto.subtle.importKey('raw', secret, 'HKDF', false, ['deriveKey'])
    const key = await webcrypto.subtle.deriveKey(
      {
        name: 'HKDF',
        hash: 'SHA-256',
        salt: Buffer.from(challengeId, 'hex'),
        info: new TextEncoder().encode('streamfusion-login-v1'),
      },
      material,
      { name: 'AES-GCM', length: 256 },
      false,
      ['decrypt'],
    )
    const clear = await webcrypto.subtle.decrypt(
      {
        name: 'AES-GCM',
        iv: Buffer.from(body.iv, 'base64url'),
        additionalData: new TextEncoder().encode(challengeId),
      },
      key,
      Buffer.from(body.ciphertext, 'base64url'),
    )
    expect(JSON.parse(new TextDecoder().decode(clear))).toEqual({
      username: 'Admin01',
      password: 'AdminPass1!',
    })
    vi.unstubAllGlobals()
  })
})
