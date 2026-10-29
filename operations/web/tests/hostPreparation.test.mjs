import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import { computed, ref } from "vue";
import ts from "typescript";

// Exercise the component's real script with controlled requests and timers.
const component = await readFile(new URL("../src/HostPreparationPanel.vue", import.meta.url), "utf8");
const source = component.split('<script setup lang="ts">')[1].split("</script>")[0]
  .replace(/^import .+;\r?$/gm, "");
const compiled = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None },
}).outputText;
const createPanel = new Function(
  "computed", "ref", "watch", "onUnmounted", "defineProps", "defineEmits",
  "ApiError", "request", "setTimeout", "clearTimeout", "fetch", "location", "document", "URL", "navigator",
  compiled + "\nreturn { data, busy, error, confirmed, sudoPassword, checkResult, load, install, check, reconcile, download };",
);

function deferred() {
  let resolve, reject;
  const promise = new Promise((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}

async function flush() {
  for (let i = 0; i < 8; i++) await Promise.resolve();
}

function preparation(id = "host-a", remoteJob) {
  return {
    hostId: id, expectedHostVersion: "1", account: "ubuntu", remoteJob,
    package: { available: true, fileName: `${id}.tar.gz`, downloadUrl: `/ops/api/v1/hosts/${id}/preparation/download?expectedVersion=1` },
  };
}

function harness(request, fetch = async () => Response.json({})) {
  const props = { host: { id: "host-a", version: "1", kind: "SSH" } };
  const timers = new Map();
  const clicks = [];
  const emitted = [];
  let timerId = 0;
  let change, unmount;
  class TestURL extends URL {
    static createObjectURL() { return "blob:test-download"; }
    static revokeObjectURL() {}
  }
  class ApiError extends Error {}
  const panel = createPanel(
    computed, ref,
    (_sources, callback) => { change = callback; callback(); },
    (callback) => { unmount = callback; },
    () => props, () => (name) => emitted.push(name), ApiError, request,
    (callback, delay) => { const id = ++timerId; timers.set(id, { callback, delay }); return id; },
    (id) => timers.delete(id), fetch, { origin: "http://ops.test" },
    { createElement: () => ({ click() { clicks.push(this.download); } }) },
    TestURL, { clipboard: { writeText: async () => {} } },
  );
  return {
    panel, timers, clicks, emitted,
    changeHost(id) { props.host = { id, version: "1", kind: "SSH" }; change(); },
    unmount() { unmount(); },
    async tick() {
      const next = timers.entries().next().value;
      assert.ok(next, "expected a scheduled progress refresh");
      timers.delete(next[0]); next[1].callback(); await flush();
    },
  };
}

test("a stale host load cannot clear the current host's busy state", async () => {
  const old = deferred(), current = deferred();
  const h = harness((path) => path.includes("host-a") ? old.promise : current.promise);
  h.changeHost("host-b");
  old.resolve(preparation("host-a")); await flush();
  assert.equal(h.panel.busy.value, true);
  assert.equal(h.panel.data.value, undefined);
  current.resolve(preparation("host-b")); await flush();
  assert.equal(h.panel.busy.value, false);
  assert.equal(h.panel.data.value.hostId, "host-b");
});

test("a completed check from the previous host cannot emit or overwrite feedback", async () => {
  const check = deferred(), current = deferred();
  const h = harness((path, method) => method === "POST" ? check.promise : path.includes("host-b") ? current.promise : Promise.resolve(preparation()));
  await flush();
  const pending = h.panel.check();
  h.changeHost("host-b");
  check.resolve({ observedAt: "2026-10-29T01:00:00Z" }); await pending;
  assert.equal(h.panel.checkResult.value, undefined);
  assert.deepEqual(h.emitted, []);
  assert.equal(h.panel.busy.value, true);
  current.resolve(preparation("host-b")); await flush();
});

test("running progress retries transient failure and clears its temporary error on recovery", async () => {
  let calls = 0;
  const running = { id: "job-1", status: "RUNNING" };
  const h = harness(async () => {
    if (++calls === 2) throw new Error("暂时断线");
    return preparation("host-a", running);
  });
  await flush(); await h.tick();
  assert.match(h.panel.error.value.message, /重试/);
  assert.equal(h.timers.size, 1);
  await h.tick();
  assert.equal(h.panel.error.value, undefined);
  assert.equal(h.panel.data.value.remoteJob.status, "RUNNING");
  assert.equal(h.timers.size, 1);
  h.unmount();
});

test("progress failures stop after three attempts and manual refresh restores polling", async () => {
  let calls = 0;
  const h = harness(async () => {
    if (++calls > 1 && calls < 5) throw new Error("断线");
    return preparation("host-a", { id: "job-1", status: "RUNNING" });
  });
  await flush(); await h.tick(); await h.tick(); await h.tick();
  assert.equal(h.timers.size, 0);
  assert.match(h.panel.error.value.message, /刷新已暂停/);
  await h.panel.load();
  assert.equal(h.panel.error.value, undefined);
  assert.equal(h.timers.size, 1);
  h.unmount();
});

test("an unconfirmed install request reuses its idempotency key without retaining the sudo password", async () => {
  let reads = 0;
  const inputs = [];
  const h = harness(async (_path, method, input) => {
    if (method === "POST") { inputs.push({ ...input }); throw new Error("响应丢失"); }
    if (++reads > 1) throw new Error("状态读取失败");
    return preparation();
  });
  await flush();
  h.panel.confirmed.value = true;
  h.panel.sudoPassword.value = "temporary-password";
  await h.panel.install();
  assert.equal(h.panel.sudoPassword.value, "");
  assert.equal(h.panel.busy.value, false);
  await h.panel.install();
  assert.equal(inputs.length, 2);
  assert.equal(inputs[0].idempotencyKey, inputs[1].idempotencyKey);
  assert.equal(inputs[0].sudoPassword, "temporary-password");
  assert.equal(inputs[1].sudoPassword, "");
});

test("a confirmed terminal install permits a new install intent", async () => {
  const keys = [];
  let latest;
  const h = harness(async (_path, method, input) => {
    if (method === "POST") {
      keys.push(input.idempotencyKey);
      latest = { id: `job-${keys.length}`, status: "FAILED" };
      return latest;
    }
    return preparation("host-a", latest);
  });
  await flush(); h.panel.confirmed.value = true;
  await h.panel.install(); await h.panel.install();
  assert.equal(keys.length, 2);
  assert.notEqual(keys[0], keys[1]);
});

test("download uses its initial file name and ignores completion after a host switch", async () => {
  const downloaded = deferred();
  const h = harness(async () => preparation(), () => downloaded.promise);
  await flush();
  const pending = h.panel.download();
  h.panel.data.value.package.fileName = "changed-name.tar.gz";
  downloaded.resolve(new Response("archive")); await pending;
  assert.deepEqual(h.clicks, ["host-a.tar.gz"]);

  const stale = deferred();
  const current = deferred();
  const h2 = harness((path) => path.includes("host-b") ? current.promise : Promise.resolve(preparation()), () => stale.promise);
  await flush();
  const staleDownload = h2.panel.download();
  h2.changeHost("host-b");
  stale.resolve(new Response("archive")); await staleDownload;
  assert.deepEqual(h2.clicks, []);
  assert.equal(h2.panel.busy.value, true);
  current.resolve(preparation("host-b")); await flush();
});

test("offline check reports executor and authorization readiness separately from SSH connectivity", async () => {
  const available = { status: "AVAILABLE", code: "OK", message: "可用" };
  const checked = {
    observedAt: "2026-10-29T01:00:00Z",
    capabilities: { reachability: available, readOnly: available, mutation: { status: "UNAVAILABLE", code: "EXECUTOR_NOT_FOUND", message: "请先安装执行器" } },
  };
  const h = harness(async (_path, method) => method === "POST" ? checked : preparation());
  await flush(); await h.panel.check();
  assert.equal(h.panel.checkResult.value.ready, false);
  assert.equal(h.panel.checkResult.value.code, "EXECUTOR_NOT_FOUND");
  assert.equal(h.panel.checkResult.value.message, "请先安装执行器");
  checked.capabilities.mutation = available;
  await h.panel.check();
  assert.equal(h.panel.checkResult.value.ready, true);
  assert.match(h.panel.checkResult.value.message, /可以继续安装软件/);
});
