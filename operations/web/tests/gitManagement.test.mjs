import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";

function load(source, imports = {}, globals = {}) {
  const module = { exports: {} };
  const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText;
  new Function("require", "module", "exports", ...Object.keys(globals), code)((name) => imports[name], module, module.exports, ...Object.values(globals));
  return module.exports;
}
const model = load(await readFile(new URL("../src/gitManagement.ts", import.meta.url), "utf8"));
const preparation = load(await readFile(new URL("../src/preparationUpdate.ts", import.meta.url), "utf8"));
const source = (await readFile(new URL("../src/GitManagementPanel.vue", import.meta.url), "utf8")).split('<script setup lang="ts">')[1].split("</script>")[0];
const profile = { id: "profile", name: "工作账号", sshHost: "git.example.test", sshPort: 22, sshUser: "git", authorName: "开发者", authorEmail: "dev@example.test", credentialId: "key", credentialRevision: "2", hostPublicKey: "ssh-ed25519 AAAAfixture", keyFingerprint: "SHA256:key", version: "3" };
const binding = { id: "repo", hostId: "host", account: "flycode", accountUid: "1000", repoPath: "/home/flycode/app", profileId: "profile", profileName: "工作账号", version: "4", status: "BOUND", lastJobId: "" };
const checked = { id: "plan", action: "BIND", hostId: "host", change: { ...profile, account: "flycode", repoPath: binding.repoPath }, observation: { account: "flycode", repoPath: binding.repoPath }, expiresAt: "2099-01-01T00:00:00Z" };
class ApiError extends Error { constructor(code, status = 409) { super(code); this.code = code; this.status = status; } }
async function flush() { for (let i = 0; i < 14; i++) { await Promise.resolve(); await vue.nextTick(); } }
function harness(custom) {
  const calls = [], events = [], unmount = [], timers = new Map(); let timerId = 0;
  const props = vue.reactive({ host: { id: "host", name: "服务器", kind: "SSH", sshUser: "flycode", version: "1", capabilities: { mutation: { status: "AVAILABLE" } } }, active: true, showTabs: true });
  const state = { profiles: [structuredClone(profile)], bindings: [structuredClone(binding)], job: { id: "job", status: "RUNNING", phase: "GIT_REPOSITORY_APPLY", traceId: "trace", targetAckPending: false } };
  const request = async (path, method = "GET", body, signal) => {
    calls.push({ path, method, body: body && structuredClone(body), signal });
    const override = custom && await custom(path, method, body, signal, state);
    if (override !== undefined) return override;
    if (path === "/git-accounts" && method === "GET") return structuredClone(state.profiles);
    if (path.startsWith("/git-accounts") && method !== "GET") return {};
    if (path === "/credentials") return [{ id: "key", revision: "2", kind: "SSH_KEY", name: "工作密钥" }];
    if (path.endsWith("/git-repositories")) return structuredClone(state.bindings);
    if (path === "/git-host-key") return { hostPublicKey: "ssh-ed25519 AAAAcandidate", fingerprint: "SHA256:candidate" };
    if (path.endsWith("/git-repository-plans")) return { ...structuredClone(checked), action: body.action };
    if (path.endsWith("/submit")) return { jobId: "job", bindingId: "repo" };
    if (path === "/jobs/job") return structuredClone(state.job);
    if (path.endsWith("/reconcile")) return {};
    if (path === "/hosts") return [{ ...props.host, version: "2" }];
    throw new Error("Unexpected " + path);
  };
  const scope = vue.effectScope();
  const flow = scope.run(() => load(source + "\nexport const flow={profiles,bindings,profileDraft,repoDraft,editingBinding,plan,receipt,job,attempted,error,preparationOpen,automaticUpdate,editProfile,scanHostKey,saveProfile,editRepository,checkRepository,unbind,submit,jobAction,refresh,inspect,preparationUpdated,clearDraft};", {
    vue: { ...vue, onUnmounted: fn => unmount.push(fn) }, "./api": { request, ApiError }, "./gitManagement": model, "./preparationUpdate": preparation,
  }, { defineProps: () => props, withDefaults: value => value, defineEmits: () => (...args) => events.push(args), setTimeout: fn => { const id = ++timerId; timers.set(id, fn); return id; }, clearTimeout: id => timers.delete(id) }).flow);
  return { flow, calls, props, state, timers, events, async poll() { const [id, fn] = timers.entries().next().value; timers.delete(id); await fn(); }, close() { unmount.forEach(fn => fn()); scope.stop(); } };
}

test("Git key candidates require explicit trust and edits invalidate confirmation", async () => {
  const h = harness(); await flush(); h.flow.editProfile(profile); await flush();
  await h.flow.scanHostKey(); await flush();
  assert.equal(h.flow.profileDraft.value.hostKeyConfirmed, false);
  await h.flow.saveProfile(); assert.equal(h.calls.filter(c => c.path.startsWith("/git-accounts") && c.method !== "GET").length, 0);
  h.flow.profileDraft.value.hostKeyConfirmed = true;
  h.flow.profileDraft.value.sshHost = "another.example.test"; await flush();
  assert.equal(h.flow.profileDraft.value.hostKeyConfirmed, false);
  h.flow.profileDraft.value.hostKeyConfirmed = true; await h.flow.saveProfile();
  const saved = h.calls.find(c => c.method === "PATCH");
  assert.equal(saved.body.expectedVersion, "3"); assert.equal(saved.body.hostKeyConfirmed, true);
  assert.equal(saved.body.credentialRevision, "2"); assert.equal(saved.body.keyFingerprint, undefined); assert.equal(saved.body.id, undefined);
  h.close();
});
test("Git repository preflight freezes profile revision without submitting and lost replies reuse one intent", async () => {
  let submits = 0;
  const h = harness((path) => { if (path.endsWith("/submit") && ++submits === 1) throw new Error("connection lost"); });
  await flush(); h.flow.editRepository(binding); await h.flow.checkRepository();
  const preflight = h.calls.find(c => c.path.endsWith("/git-repository-plans"));
  assert.equal(preflight.body.expectedProfileVersion, "3"); assert.equal(preflight.body.expectedBindingVersion, "4");
  assert.equal(h.calls.filter(c => c.path.endsWith("/submit")).length, 0);
  await h.flow.submit(); assert.equal(h.flow.attempted.value, true); h.flow.clearDraft(); assert.ok(h.flow.plan.value);
  await h.flow.submit();
  const attempts = h.calls.filter(c => c.path.endsWith("/submit")); assert.deepEqual(attempts[0].body, attempts[1].body); assert.equal(attempts[0].body.confirmed, true);
  h.close();
});
test("Git component update resumes the same unbind preflight and never submits automatically", async () => {
  let checks = 0;
  const h = harness((path) => {
    if (path.endsWith("/git-repository-plans") && ++checks === 1) throw new ApiError("EXECUTOR_GIT_UNSUPPORTED");
    if (path.includes("checkUpdate=1")) return { hostId: "host", expectedHostVersion: "1", update: { status: "AVAILABLE", currentSHA256: "a".repeat(64), packageSHA256: "b".repeat(64) } };
  });
  await flush(); await h.flow.unbind(binding); assert.equal(h.flow.preparationOpen.value, true);
  await h.flow.preparationUpdated();
  const attempts = h.calls.filter(c => c.path.endsWith("/git-repository-plans"));
  assert.equal(attempts.length, 2); assert.equal(attempts[1].body.action, "UNBIND"); assert.equal(attempts[1].body.expectedHostVersion, "2");
  assert.equal(h.calls.filter(c => c.path.endsWith("/submit")).length, 0); h.close();
});
test("Git confirmed pre-queue rejection releases the plan but server errors keep its intent", async () => {
  for (const status of [409, 500]) {
    const h = harness(path => { if (path.endsWith("/submit")) throw new ApiError("GIT_PLAN_STALE", status); });
    await flush(); h.flow.editRepository(binding); await h.flow.checkRepository(); await h.flow.submit();
    assert.equal(h.flow.attempted.value, status === 500); assert.equal(!!h.flow.plan.value, status === 500);
    assert.equal(h.flow.repoDraft.value.repoPath, binding.repoPath); h.close();
  }
});
test("Git identity rejection does not trigger preparation or write any repository", async () => {
  const h = harness(path => { if (path.endsWith("/git-repository-plans")) throw new ApiError("GIT_REPOSITORY_OWNER_MISMATCH"); });
  await flush(); h.flow.editRepository(binding); await h.flow.checkRepository();
  assert.equal(h.flow.error.value.code, "GIT_REPOSITORY_OWNER_MISMATCH");
  assert.equal(h.calls.some(c => c.path.includes("/preparation") || c.path.endsWith("/submit")), false); h.close();
});
test("Git host changes discard late preflight and abort its request", async () => {
  let finish;
  const h = harness(path => path.endsWith("/git-repository-plans") ? new Promise(resolve => finish = resolve) : undefined);
  await flush(); h.flow.editRepository(binding); const checking = h.flow.checkRepository(); await flush();
  const attempt = h.calls.find(c => c.path.endsWith("/git-repository-plans"));
  h.props.host = { ...h.props.host, id: "other-host" }; await flush(); finish(checked); await checking;
  assert.equal(attempt.signal.aborted, true); assert.equal(h.flow.plan.value, undefined); h.close();
});
test("Git uncertain jobs restore from bindings and require reconciliation without resubmission", async () => {
  const h = harness(); h.state.bindings[0].status = "UNKNOWN"; h.state.bindings[0].lastJobId = "job"; h.state.job.status = "UNKNOWN";
  await flush(); await h.flow.refresh();
  assert.equal(h.flow.receipt.value.jobId, "job"); assert.equal(h.timers.size, 0);
  h.state.job.status = "SUCCEEDED"; h.state.bindings[0].status = "BOUND";
  await h.flow.jobAction("reconcile"); assert.equal(h.flow.job.value.status, "SUCCEEDED");
  assert.equal(h.calls.some(c => c.path.endsWith("/submit")), false); h.close();
});
test("Git read test uses the saved binding and does not claim write capability", async () => {
  const h = harness(path => path.endsWith("/test") ? { ready: true, code: "GIT_REMOTE_READABLE", message: "仓库可读取" } : undefined);
  await flush(); await h.flow.inspect(binding, true);
  const calls = h.calls.filter(c => c.method !== "GET");
  assert.deepEqual(calls.map(c => ({path:c.path,body:c.body})), [{ path:"/git-repositories/repo/test",body:{expectedVersion:"4"} }]); h.close();
});
