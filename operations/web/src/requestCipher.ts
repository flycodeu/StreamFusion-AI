export interface RequestChallenge {
  challengeId: string;
  serverPublicKey: string;
}

const cipherInfo = "streamfusion-ops-sensitive-v1";
const encoder = new TextEncoder();

export function isSensitiveRequest(path: string, method: string): boolean {
  return (
    method === "POST" &&
    (path === "/auth/login" ||
      path === "/auth/password" ||
      path === "/credentials" ||
      /^\/credentials\/[0-9a-f-]{36}\/revisions$/i.test(path))
  );
}

export function requireRequestCrypto() {
  if (!globalThis.crypto?.subtle) {
    throw new Error("浏览器无法加密安全请求，请使用 HTTPS 或本机回环地址访问");
  }
}

function encode(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary)
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replaceAll("=", "");
}

function decode(value: string): Uint8Array<ArrayBuffer> {
  if (!/^[A-Za-z0-9_-]+$/.test(value)) throw new Error("安全请求参数无效");
  return Uint8Array.from(
    atob(value.replaceAll("-", "+").replaceAll("_", "/")),
    (char) => char.charCodeAt(0),
  );
}

// One fresh private key per submission; no key or plaintext is persisted.
export async function encryptSensitiveRequest(
  challenge: RequestChallenge,
  path: string,
  data: unknown,
) {
  requireRequestCrypto();
  if (
    !/^[a-f0-9]{64}$/.test(challenge.challengeId) ||
    challenge.serverPublicKey.length !== 87
  )
    throw new Error("安全请求参数无效");
  const plaintext = encoder.encode(JSON.stringify(data));
  if (plaintext.length > 64 * 1024) {
    plaintext.fill(0);
    throw new Error("安全请求内容过长，请缩短后重试");
  }
  try {
    const subtle = crypto.subtle;
    const pair = await subtle.generateKey(
      { name: "ECDH", namedCurve: "P-256" },
      false,
      ["deriveBits"],
    );
    const server = await subtle.importKey(
      "raw",
      decode(challenge.serverPublicKey),
      { name: "ECDH", namedCurve: "P-256" },
      false,
      [],
    );
    const shared = await subtle.deriveBits(
      { name: "ECDH", public: server },
      pair.privateKey,
      256,
    );
    let material: CryptoKey;
    try {
      material = await subtle.importKey("raw", shared, "HKDF", false, [
        "deriveKey",
      ]);
    } finally {
      new Uint8Array(shared).fill(0);
    }
    const key = await subtle.deriveKey(
      {
        name: "HKDF",
        hash: "SHA-256",
        salt: encoder.encode(challenge.challengeId),
        info: encoder.encode(cipherInfo),
      },
      material,
      { name: "AES-GCM", length: 256 },
      false,
      ["encrypt"],
    );
    const iv = crypto.getRandomValues(new Uint8Array(12));
    const ciphertext = await subtle.encrypt(
      {
        name: "AES-GCM",
        iv,
        additionalData: encoder.encode(
          `${cipherInfo}\nPOST\n${path}\n${challenge.challengeId}`,
        ),
      },
      key,
      plaintext,
    );
    return {
      challengeId: challenge.challengeId,
      clientPublicKey: encode(
        new Uint8Array(await subtle.exportKey("raw", pair.publicKey)),
      ),
      iv: encode(iv),
      ciphertext: encode(new Uint8Array(ciphertext)),
    };
  } finally {
    plaintext.fill(0);
  }
}
