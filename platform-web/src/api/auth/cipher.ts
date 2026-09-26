interface Challenge {
  challengeId: string
  serverPublicKey: string
}

function fromBase64Url(value: string): Uint8Array<ArrayBuffer> {
  if (!/^[A-Za-z0-9_-]+$/.test(value)) throw new Error('Invalid login challenge')
  const binary = atob(value.replaceAll('-', '+').replaceAll('_', '/'))
  return Uint8Array.from(binary, (char) => char.charCodeAt(0))
}

function base64Url(bytes: Uint8Array): string {
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replaceAll('+', '-').replaceAll('/', '_').replaceAll('=', '')
}

export async function encryptLogin(
  challenge: Challenge,
  username: string,
  password: string,
): Promise<{ challengeId: string; clientPublicKey: string; iv: string; ciphertext: string }> {
  if (!/^[a-f0-9]{32}$/.test(challenge.challengeId)) throw new Error('Invalid login challenge')
  const subtle = crypto.subtle
  const pair = (await subtle.generateKey({ name: 'ECDH', namedCurve: 'P-256' }, true, [
    'deriveBits',
  ])) as CryptoKeyPair
  const serverKey = await subtle.importKey(
    'spki',
    fromBase64Url(challenge.serverPublicKey),
    { name: 'ECDH', namedCurve: 'P-256' },
    false,
    [],
  )
  const shared = await subtle.deriveBits({ name: 'ECDH', public: serverKey }, pair.privateKey, 256)
  const material = await subtle.importKey('raw', shared, 'HKDF', false, ['deriveKey'])
  const key = await subtle.deriveKey(
    {
      name: 'HKDF',
      hash: 'SHA-256',
      salt: Uint8Array.from(challenge.challengeId.match(/.{2}/g)!, (part) => parseInt(part, 16)),
      info: new TextEncoder().encode('streamfusion-login-v1'),
    },
    material,
    { name: 'AES-GCM', length: 256 },
    false,
    ['encrypt'],
  )
  const iv = crypto.getRandomValues(new Uint8Array(12))
  const encrypted = await subtle.encrypt(
    { name: 'AES-GCM', iv, additionalData: new TextEncoder().encode(challenge.challengeId) },
    key,
    new TextEncoder().encode(JSON.stringify({ username, password })),
  )
  const publicKey = await subtle.exportKey('spki', pair.publicKey)
  return {
    challengeId: challenge.challengeId,
    clientPublicKey: base64Url(new Uint8Array(publicKey)),
    iv: base64Url(iv),
    ciphertext: base64Url(new Uint8Array(encrypted)),
  }
}
