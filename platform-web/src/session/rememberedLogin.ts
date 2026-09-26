export interface RememberedLogin {
  username: string
  password: string
}
export interface SealedLogin {
  version: 1
  key: CryptoKey
  iv: Uint8Array<ArrayBuffer>
  ciphertext: ArrayBuffer
  expiresAt: number
}
const lifetime = 30 * 24 * 60 * 60 * 1000
const databaseName = 'sf-login-vault'
const storeName = 'credentials'
const recordId = 'remembered-login'
const additionalData = (expiresAt: number) => new TextEncoder().encode(`sf-login-v1|${expiresAt}`)

export async function sealLogin(value: RememberedLogin, now = Date.now()): Promise<SealedLogin> {
  const key = await crypto.subtle.generateKey({ name: 'AES-GCM', length: 256 }, false, [
    'encrypt',
    'decrypt',
  ])
  const iv = crypto.getRandomValues(new Uint8Array(12))
  const expiresAt = now + lifetime
  const plaintext = new TextEncoder().encode(JSON.stringify(value))
  try {
    const ciphertext = await crypto.subtle.encrypt(
      { name: 'AES-GCM', iv, additionalData: additionalData(expiresAt) },
      key,
      plaintext,
    )
    return { version: 1, key, iv, ciphertext, expiresAt }
  } finally {
    plaintext.fill(0)
  }
}

export async function unsealLogin(
  record: SealedLogin,
  now = Date.now(),
): Promise<RememberedLogin | null> {
  if (
    record.version !== 1 ||
    !Number.isFinite(record.expiresAt) ||
    record.expiresAt <= now ||
    record.expiresAt > now + lifetime ||
    record.key?.extractable !== false
  )
    return null
  const plaintext = new Uint8Array(
    await crypto.subtle.decrypt(
      { name: 'AES-GCM', iv: record.iv, additionalData: additionalData(record.expiresAt) },
      record.key,
      record.ciphertext,
    ),
  )
  try {
    const value: unknown = JSON.parse(new TextDecoder().decode(plaintext))
    if (
      !value ||
      typeof value !== 'object' ||
      !('username' in value) ||
      !('password' in value) ||
      typeof value.username !== 'string' ||
      typeof value.password !== 'string'
    )
      return null
    return { username: value.username, password: value.password }
  } finally {
    plaintext.fill(0)
  }
}

function openVault(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    if (!globalThis.indexedDB || !crypto.subtle) {
      reject(new Error('本机凭据存储不可用'))
      return
    }
    let finished = false
    const timer = setTimeout(() => fail(), 2500)
    function fail() {
      if (!finished) {
        finished = true
        clearTimeout(timer)
        reject(new Error('本机凭据存储不可用'))
      }
    }
    let request: IDBOpenDBRequest
    try {
      request = globalThis.indexedDB.open(databaseName, 1)
    } catch {
      fail()
      return
    }
    request.onupgradeneeded = () => request.result.createObjectStore(storeName)
    request.onerror = fail
    request.onblocked = fail
    request.onsuccess = () => {
      if (finished) {
        request.result.close()
        return
      }
      finished = true
      clearTimeout(timer)
      request.result.onversionchange = () => request.result.close()
      resolve(request.result)
    }
  })
}

async function accessVault<T>(
  mode: IDBTransactionMode,
  operation: (store: IDBObjectStore) => IDBRequest<T>,
): Promise<T> {
  const database = await openVault()
  try {
    return await new Promise<T>((resolve, reject) => {
      const transaction = database.transaction(storeName, mode)
      let finished = false
      let request: IDBRequest<T> | undefined
      function abort() {
        try {
          transaction.abort()
        } catch {
          /* Already completed. */
        }
      }
      function fail() {
        clearTimeout(timer)
        if (!finished) {
          finished = true
          reject(new Error('本机凭据存储不可用'))
        }
      }
      const timer = setTimeout(() => {
        fail()
        abort()
      }, 2500)
      transaction.oncomplete = () => {
        clearTimeout(timer)
        if (!finished && request) {
          finished = true
          resolve(request.result)
        }
      }
      transaction.onerror = transaction.onabort = fail
      try {
        request = operation(transaction.objectStore(storeName))
      } catch {
        fail()
        abort()
      }
    })
  } finally {
    database.close()
  }
}

export async function saveRememberedLogin(value: RememberedLogin): Promise<void> {
  const record = await sealLogin(value)
  await accessVault('readwrite', (store) => store.put(record, recordId))
}
export async function forgetRememberedLogin(): Promise<void> {
  await accessVault('readwrite', (store) => store.delete(recordId))
}
export async function readRememberedLogin(): Promise<RememberedLogin | null> {
  const record: SealedLogin | undefined = await accessVault('readonly', (store) =>
    store.get(recordId),
  )
  if (!record) return null
  try {
    const value = await unsealLogin(record)
    if (value) return value
  } catch {
    /* Corrupt or expired ciphertext is discarded, without logging credentials. */
  }
  await forgetRememberedLogin()
  return null
}
