import { describe, expect, it } from 'vitest'
import { sealLogin, unsealLogin } from './rememberedLogin'

describe('remembered credential encryption', () => {
  const input = { username: 'PreviewAdmin', password: 'TestPassword1!' }
  it('round trips without persisting plaintext and uses a non-exportable key', async () => {
    const record = await sealLogin(input, 1000)
    expect(new TextDecoder().decode(record.ciphertext)).not.toContain(input.password)
    expect(record.key.extractable).toBe(false)
    await expect(crypto.subtle.exportKey('raw', record.key)).rejects.toThrow()
    expect(await unsealLogin(record, 1001)).toEqual(input)
  })
  it('uses independent keys and nonce for each save', async () => {
    const first = await sealLogin(input)
    const second = await sealLogin(input)
    expect(first.iv).not.toEqual(second.iv)
    expect(new Uint8Array(first.ciphertext)).not.toEqual(new Uint8Array(second.ciphertext))
  })
  it('does not restore expired credentials', async () => {
    const record = await sealLogin(input, 1000)
    expect(await unsealLogin(record, record.expiresAt)).toBeNull()
  })
  it('rejects modified ciphertext or expiry metadata', async () => {
    const record = await sealLogin(input, 1000)
    await expect(
      unsealLogin({ ...record, expiresAt: record.expiresAt - 1 }, 1001),
    ).rejects.toThrow()
    const changed = record.ciphertext.slice(0)
    new Uint8Array(changed)[0] ^= 1
    await expect(unsealLogin({ ...record, ciphertext: changed }, 1001)).rejects.toThrow()
  })
})
