import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";

const component = await readFile(new URL("../src/PortManagement.vue", import.meta.url), "utf8");
const script = component.split('<script setup lang="ts">')[1].split("</script>")[0];
const compile = source => ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText;
function loadModule(source, imports) {
  const module = { exports: {} };
  new Function("require", "module", "exports", compile(source))(name => imports[name] || {}, module, module.exports);
  return module.exports;
}
const pagination = loadModule(await readFile(new URL("../src/pagination.ts", import.meta.url), "utf8"), { vue });
class ApiError extends Error {
  constructor(message, code = "SSH_TIMEOUT") { super(message); Object.assign(this, { code, timestamp: "2026-11-10T00:00:00Z", traceId: "test-trace" }); }
}
const deferred = () => {
  let resolve, reject;
  const promise = new Promise((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
};
async function flush() { for (let i = 0; i < 16; i++) { await Promise.resolve(); await vue.nextTick(); } }
const hosts = [{ id: "host-a", name: "A", version: "1" }, { id: "host-b", name: "B", version: "2" }];
function view(host = "host-a") {
  return { freshness: "FRESH", snapshot: { hash: host, status: "ACTIVE", ports: Array.from({ length: 26 }, (_, i) => ({ port: 8000 + i, protocol: "tcp", address: "0.0.0.0" })),
    managedRules: Array.from({ length: 23 }, (_, i) => ({ id: `rule-${i}`, action: "allow", protocol: "tcp", source: "0.0.0.0/0", start: 8000 + i, end: 8000 + i })) } };
}
function savedPlan() { return { id: "original-plan", expiresAt: "2099-01-01T00:00:00Z", snapshot: view().snapshot, change: { operation: "ADD", rule: { start: 8080, end: 8080, action: "allow", protocol: "tcp", source: "0.0.0.0/0" } } }; }
function harness(respond = () => undefined) {
  const calls = [], events = [], messages = [], mounts = [], unmounts = [], scope = vue.effectScope();
  const request = async (path, method, data) => {
    calls.push({ path, method, data });
    const result = respond(path, method, data);
    if (result !== undefined) return result;
    if (path === "/hosts") return hosts;
    if (path.startsWith("/jobs?")) return { items: [{ id: "first", kind: "FIREWALL_CHANGE", targetId: new URL(path, "http://ops.test").searchParams.get("hostId") }], nextCursor: "cursor-1" };
    if (path.endsWith("/ports")) return view(path.includes("host-b") ? "host-b" : "host-a");
    throw new Error(`Unexpected request ${path}`);
  };
  const imports = {
    vue: { ...vue, onMounted: fn => mounts.push(fn), onUnmounted: fn => unmounts.push(fn) },
    "./api": { ApiError, request }, "./pagination": pagination, "./hosts": { hostTime: value => value },
    "element-plus": { ElMessage: { success: value => messages.push(value) } },
  };
  const module = { exports: {} };
  scope.run(() => new Function("require", "module", "exports", "defineEmits", "setTimeout", "clearTimeout", "setInterval", "clearInterval", "document", compile(
    script + "\nexport { hostId, view, jobs, busy, loading, search, error, errorDetails, plan, now, receipt, submitKey, submissionAttempted, dialog, jobLogs, portPage, portRows, portTotal, rulePage, ruleRows, historyPage, historyPageSize, nextCursor, act, refresh, preflight, submit, logs, open, changeHistoryPage, changeHistorySize };",
  ))(name => imports[name] || {}, module, module.exports, () => (name, ...args) => events.push([name, ...args]), () => 1, () => {}, () => 1, () => {}, { hidden: false }));
  mounts.forEach(fn => fn());
  return { panel: module.exports, calls, events, messages, dispose() { unmounts.forEach(fn => fn()); scope.stop(); } };
}

test("ports and rules paginate independently and host changes reset their pages", async () => {
  const h = harness();
  await flush();
  assert.equal(h.panel.portRows.value.length, 10);
  assert.equal(h.panel.ruleRows.value.length, 10);
  h.panel.portPage.value = 3;
  h.panel.rulePage.value = 3;
  assert.equal(h.panel.portRows.value.length, 6);
  assert.equal(h.panel.ruleRows.value.length, 3);
  h.panel.search.value = "8025";
  assert.equal(h.panel.portPage.value, 1);
  assert.equal(h.panel.portRows.value.length, 1);
  h.panel.hostId.value = "host-b";
  await flush();
  assert.equal(h.panel.portPage.value, 1);
  assert.equal(h.panel.rulePage.value, 1);
  assert.equal(h.panel.view.value.snapshot.hash, "host-b");
  h.dispose();
});

test("firewall history pages use server cursors and page size changes restart at the first page", async () => {
  const h = harness(path => {
    if (!path.startsWith("/jobs?")) return;
    const params = new URL(path, "http://ops.test").searchParams;
    const second = params.get("before") === "cursor-1";
    return { items: [{ id: second ? "second" : "first", kind: "FIREWALL_CHANGE", targetId: params.get("hostId") }], nextCursor: second ? "" : "cursor-1" };
  });
  await flush();
  const initial = new URL(h.calls.find(call => call.path.startsWith("/jobs?")).path, "http://ops.test").searchParams;
  assert.equal(initial.get("limit"), "10");
  assert.equal(initial.get("kind"), "FIREWALL_CHANGE");
  assert.equal(initial.get("hostId"), "host-a");
  await h.panel.changeHistoryPage(2);
  assert.equal(h.panel.historyPage.value, 2);
  assert.equal(h.panel.jobs.value[0].id, "second");
  assert.equal(h.panel.nextCursor.value, "");
  const count = h.calls.length;
  await h.panel.changeHistoryPage(3);
  assert.equal(h.calls.length, count);
  await h.panel.changeHistoryPage(1);
  await h.panel.changeHistoryPage(2);
  await h.panel.changeHistorySize(20);
  assert.equal(h.panel.historyPage.value, 1);
  assert.equal(h.panel.historyPageSize.value, 20);
  const last = new URL(h.calls.at(-1).path, "http://ops.test").searchParams;
  assert.equal(last.get("limit"), "20");
  assert.equal(last.has("before"), false);
  h.dispose();
});

test("a failed history page preserves the current page and separates diagnostics from the message", async () => {
  const h = harness(path => { if (path.includes("before=")) throw new ApiError("读取操作记录超时"); });
  await flush();
  await h.panel.changeHistoryPage(2);
  assert.equal(h.panel.historyPage.value, 1);
  assert.equal(h.panel.jobs.value[0].id, "first");
  assert.equal(h.panel.error.value, "读取操作记录超时");
  assert.equal(h.panel.errorDetails.value.code, "SSH_TIMEOUT");
  assert.equal(h.panel.errorDetails.value.traceId, "test-trace");
  assert.equal(h.panel.busy.value, false);
  h.dispose();
});

test("late refresh and preflight responses cannot replace another host or clear its loading state", async () => {
  const oldPorts = deferred(), newPorts = deferred(), check = deferred();
  const h = harness((path, method) => {
    if (path === "/hosts/host-a/ports") return oldPorts.promise;
    if (path === "/hosts/host-b/ports") return newPorts.promise;
    if (method === "POST" && path.endsWith("/firewall-plans")) return check.promise;
  });
  await flush();
  h.panel.view.value = view();
  const checking = h.panel.preflight();
  h.panel.hostId.value = "host-b";
  await flush();
  oldPorts.resolve(view());
  check.resolve(savedPlan());
  await checking;
  await flush();
  assert.equal(h.panel.loading.value, true);
  assert.equal(h.panel.plan.value, undefined);
  assert.equal(h.panel.view.value, undefined);
  newPorts.resolve(view("host-b"));
  await flush();
  assert.equal(h.panel.view.value.snapshot.hash, "host-b");
  assert.equal(h.panel.loading.value, false);
  h.dispose();
});

test("late log errors and output stay with their original host", async () => {
  for (const reject of [false, true]) {
    const log = deferred();
    const h = harness(path => path === "/jobs/old-job/logs" ? log.promise : undefined);
    await flush();
    const pending = h.panel.act(() => h.panel.logs({ id: "old-job" }));
    h.panel.hostId.value = "host-b";
    await flush();
    if (reject) log.reject(new ApiError("旧主机日志超时"));
    else log.resolve({ text: "旧主机输出" });
    await pending;
    assert.equal(h.panel.jobLogs.value, "");
    assert.equal(h.panel.error.value, "");
    assert.deepEqual(h.events, []);
    h.dispose();
  }
});

test("late history cannot replace the selected host's records", async () => {
  const previous = deferred();
  const h = harness(path => path.startsWith("/jobs?") && path.includes("hostId=host-a") ? previous.promise : undefined);
  await flush();
  h.panel.hostId.value = "host-b";
  await flush();
  previous.resolve({ items: [{ id: "old-job", kind: "FIREWALL_CHANGE", targetId: "host-a" }], nextCursor: "old-cursor" });
  await flush();
  assert.equal(h.panel.jobs.value[0].targetId, "host-b");
  assert.equal(h.panel.nextCursor.value, "cursor-1");
  assert.equal(h.panel.busy.value, false);
  h.dispose();
});

test("a lost submit reply reuses its original plan and key after expiry", async () => {
  let attempts = 0;
  const h = harness(path => {
    if (!path.endsWith("/submit")) return;
    if (++attempts === 1) throw new ApiError("响应未确认");
    return { jobId: "accepted-job" };
  });
  await flush();
  await h.panel.changeHistoryPage(2);
  h.panel.open("ADD");
  h.panel.plan.value = savedPlan();
  await h.panel.act(h.panel.submit);
  assert.equal(h.panel.submissionAttempted.value, true);
  h.panel.now.value = Date.parse("2100-01-01T00:00:00Z");
  await h.panel.act(h.panel.submit);
  const submits = h.calls.filter(call => call.path.endsWith("/submit"));
  assert.equal(submits.length, 2);
  assert.deepEqual(submits[0], submits[1]);
  assert.equal(h.panel.receipt.value, "accepted-job");
  assert.equal(h.panel.dialog.value, false);
  assert.equal(h.panel.historyPage.value, 1);
  assert.equal(new URL(h.calls.filter(call => call.path.startsWith("/jobs?")).at(-1).path, "http://ops.test").searchParams.has("before"), false);
  assert.equal(h.messages.length, 1);
  h.dispose();
});
