import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import ts from "typescript";
import {
  encryptSensitiveRequest,
  isSensitiveRequest,
} from "../src/requestCipher.ts";

// Compile the real request layer with the project's compiler for Node tests.
const apiSource = (
  await readFile(new URL("../src/api.ts", import.meta.url), "utf8")
).replace(
  '"./requestCipher"',
  JSON.stringify(new URL("../src/requestCipher.ts", import.meta.url).href),
);
const compiled = ts.transpileModule(apiSource, {
  compilerOptions: {
    target: ts.ScriptTarget.ES2022,
    module: ts.ModuleKind.ESNext,
  },
}).outputText;
const { request, setCSRF, ApiError } = await import(
  "data:text/javascript;base64," +
    Buffer.from(compiled + "\n//# sourceURL=ops-api.test.js").toString("base64")
);

async function challenge() {
  const server = await crypto.subtle.generateKey(
    { name: "ECDH", namedCurve: "P-256" },
    false,
    ["deriveBits"],
  );
  return {
    server,
    public: {
      challengeId: Buffer.from(
        crypto.getRandomValues(new Uint8Array(32)),
      ).toString("hex"),
      serverPublicKey: Buffer.from(
        await crypto.subtle.exportKey("raw", server.publicKey),
      ).toString("base64url"),
    },
  };
}

async function decrypt(server, envelope, path) {
  const client = await crypto.subtle.importKey(
    "raw",
    Buffer.from(envelope.clientPublicKey, "base64url"),
    { name: "ECDH", namedCurve: "P-256" },
    false,
    [],
  );
  const shared = await crypto.subtle.deriveBits(
    { name: "ECDH", public: client },
    server.privateKey,
    256,
  );
  const material = await crypto.subtle.importKey("raw", shared, "HKDF", false, [
    "deriveKey",
  ]);
  const key = await crypto.subtle.deriveKey(
    {
      name: "HKDF",
      hash: "SHA-256",
      salt: new TextEncoder().encode(envelope.challengeId),
      info: new TextEncoder().encode("streamfusion-ops-sensitive-v1"),
    },
    material,
    { name: "AES-GCM", length: 256 },
    false,
    ["decrypt"],
  );
  const plaintext = await crypto.subtle.decrypt(
    {
      name: "AES-GCM",
      iv: Buffer.from(envelope.iv, "base64url"),
      additionalData: new TextEncoder().encode(
        `streamfusion-ops-sensitive-v1\nPOST\n${path}\n${envelope.challengeId}`,
      ),
    },
    key,
    Buffer.from(envelope.ciphertext, "base64url"),
  );
  return JSON.parse(new TextDecoder().decode(plaintext));
}

function reply(data, status = 200, code = "SUCCESS") {
  return Response.json(
    {
      data,
      code,
      msg: "测试响应",
      traceId: "test-trace",
      timestamp: "2026-10-25T00:00:00Z",
    },
    { status },
  );
}

test("only login, password changes and credential writes use the envelope", () => {
  for (const path of [
    "/auth/login",
    "/auth/password",
    "/credentials",
    "/credentials/00000000-0000-0000-0000-000000000001/revisions",
    "/hosts/00000000-0000-0000-0000-000000000001/preparation/install",
    "/hosts/00000000-0000-0000-0000-000000000001/preparation/repair",
  ]) {
    assert.equal(isSensitiveRequest(path, "POST"), true);
    assert.equal(isSensitiveRequest(path, "GET"), false);
  }
  for (const path of [
    "/auth/csrf",
    "/auth/secure-challenge",
    "/settings",
    "/credentials/00000000-0000-0000-0000-000000000001/revisions/1/revoke",
    "/hosts/00000000-0000-0000-0000-000000000001/preparation/reconcile",
  ]) {
    assert.equal(isSensitiveRequest(path, "POST"), false);
  }
});

test("actual request layer sends ciphertext, preserves CSRF and does not echo plaintext", async (t) => {
  const value = { username: "admin", password: "test-secret-password" };
  const issued = await challenge();
  const calls = [];
  setCSRF("csrf-for-test");
  t.mock.method(globalThis, "fetch", async (url, options) => {
    calls.push({ url, options });
    if (url.endsWith("/auth/secure-challenge")) return reply(issued.public);
    assert.deepEqual(
      await decrypt(issued.server, JSON.parse(options.body), "/auth/login"),
      value,
    );
    return reply({ username: "admin", csrfToken: "rotated" });
  });
  await request("/auth/login", "post", value);
  assert.equal(calls.length, 2);
  assert.deepEqual(JSON.parse(calls[0].options.body), { path: "/auth/login" });
  const body = JSON.parse(calls[1].options.body);
  assert.deepEqual(Object.keys(body).sort(), [
    "challengeId",
    "ciphertext",
    "clientPublicKey",
    "iv",
  ]);
  assert.equal(calls[1].options.body.includes(value.password), false);
  assert.equal(calls[1].options.body.includes('"username"'), false);
  for (const call of calls) {
    assert.equal(call.options.headers["X-CSRF-Token"], "csrf-for-test");
    assert.equal(call.options.cache, "no-store");
    assert.equal(call.options.credentials, "same-origin");
  }
});

test("password and all credential writes are protected without changing their payload", async (t) => {
  const cases = [
    [
      "/auth/password",
      { currentPassword: "before-password", newPassword: "after-password" },
    ],
    [
      "/credentials",
      {
        name: "SSH私钥",
        kind: "SSH_KEY",
        secret:
          "-----BEGIN OPENSSH PRIVATE KEY-----\n" + "key-data".repeat(4000),
      },
    ],
    [
      "/credentials/00000000-0000-0000-0000-000000000001/revisions",
      {
        name: "修订",
        kind: "TOKEN",
        secret: "修订密码\nsecret",
        expectedRevision: "2",
      },
    ],
  ];
  let issued;
  let expected;
  t.mock.method(globalThis, "fetch", async (url, options) => {
    if (url.endsWith("/auth/secure-challenge")) {
      issued = await challenge();
      return reply(issued.public);
    }
    assert.deepEqual(
      await decrypt(issued.server, JSON.parse(options.body), expected[0]),
      expected[1],
    );
    return reply(null);
  });
  for (const item of cases) {
    expected = item;
    await request(item[0], "POST", item[1]);
  }
});

test("encryption produces fresh keys and nonces and binds the exact route", async () => {
  const issued = await challenge();
  const a = await encryptSensitiveRequest(issued.public, "/credentials", {
    secret: "value",
  });
  const b = await encryptSensitiveRequest(issued.public, "/credentials", {
    secret: "value",
  });
  assert.notEqual(a.clientPublicKey, b.clientPublicKey);
  assert.notEqual(a.iv, b.iv);
  assert.notEqual(a.ciphertext, b.ciphertext);
  await assert.rejects(decrypt(issued.server, a, "/auth/password"));
});

test("challenge failure never falls back or submits plaintext", async (t) => {
  const calls = [];
  t.mock.method(globalThis, "fetch", async (url) => {
    calls.push(url);
    return reply(null, 403, "CSRF_INVALID");
  });
  await assert.rejects(
    request("/auth/login", "POST", { username: "admin", password: "secret" }),
    (error) => error instanceof ApiError && error.code === "CSRF_INVALID",
  );
  assert.deepEqual(calls, ["/ops/api/v1/auth/secure-challenge"]);
});

test("unavailable Web Crypto fails before sending any request", async (t) => {
  const original = Object.getOwnPropertyDescriptor(globalThis, "crypto");
  Object.defineProperty(globalThis, "crypto", {
    value: undefined,
    configurable: true,
  });
  t.after(() => Object.defineProperty(globalThis, "crypto", original));
  const fetch = t.mock.method(globalThis, "fetch", async () => {
    throw new Error("must not fetch");
  });
  await assert.rejects(
    request("/auth/login", "POST", { password: "secret" }),
    /HTTPS/,
  );
  assert.equal(fetch.mock.callCount(), 0);
});

test("invalid public key never submits the sensitive request", async (t) => {
  const calls = [];
  t.mock.method(globalThis, "fetch", async (url) => {
    calls.push(url);
    return reply({
      challengeId: "a".repeat(64),
      serverPublicKey: "A".repeat(87),
    });
  });
  await assert.rejects(request("/credentials", "POST", { secret: "value" }));
  assert.deepEqual(calls, ["/ops/api/v1/auth/secure-challenge"]);
});

test("cancelled request does not submit after challenge acquisition", async (t) => {
  const issued = await challenge();
  const controller = new AbortController();
  const fetch = t.mock.method(globalThis, "fetch", async () => {
    controller.abort();
    return reply(issued.public);
  });
  await assert.rejects(
    request("/auth/login", "POST", { password: "secret" }, controller.signal),
    { name: "AbortError" },
  );
  assert.equal(fetch.mock.callCount(), 1);
});

test("oversized payload is rejected before the sensitive submission", async () => {
  const issued = await challenge();
  await assert.rejects(
    encryptSensitiveRequest(issued.public, "/credentials", {
      secret: "界".repeat(24000),
    }),
    /内容过长/,
  );
});

test("ordinary requests retain existing payload and error behavior", async (t) => {
  const calls = [];
  t.mock.method(globalThis, "fetch", async (url, options) => {
    calls.push({ url, options });
    return reply({ version: "2" });
  });
  assert.deepEqual(
    await request("/settings", "PUT", { name: "Ops", version: "1" }),
    { version: "2" },
  );
  assert.equal(calls.length, 1);
  assert.deepEqual(JSON.parse(calls[0].options.body), {
    name: "Ops",
    version: "1",
  });
});
