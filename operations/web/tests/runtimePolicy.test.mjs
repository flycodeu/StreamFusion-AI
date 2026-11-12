import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";

function load(source, imports = {}, globals = {}) {
  const exports = {};
  const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText;
  new Function("require", "exports", ...Object.keys(globals), code)(name => imports[name], exports, ...Object.values(globals));
  return exports;
}
const model = load(await readFile(new URL("../src/runtimePolicy.ts", import.meta.url), "utf8"));
const component = (await readFile(new URL("../src/SoftwareRuntimePolicyPanel.vue", import.meta.url), "utf8")).split('<script setup lang="ts">')[1].split("</script>")[0];
const detailsComponent = (await readFile(new URL("../src/SoftwareDetailsDialog.vue", import.meta.url), "utf8")).split('<script setup lang="ts">')[1].split("</script>")[0];
class ApiError extends Error { constructor(status, message = "请重新检查") { super(message); this.status = status; } }
const flush = async () => { for (let i = 0; i < 6; i++) await vue.nextTick(); };

test("software details can close during a read without bypassing change submission protection", async t => {
  const props = vue.reactive({ busy: true, observing: true }), scope = vue.effectScope();
  t.after(() => scope.stop());
  const flow = scope.run(() => load(detailsComponent + "\nexport const flow = { close, editingBlocked, policyCloseState };", { vue }, { defineProps: () => props, defineEmits: () => () => {} }).flow);
  let closed = false;
  await flow.close(() => { closed = true; });
  assert.equal(closed, true);
  props.observing = false;
  assert.equal(flow.editingBlocked.value, true);
  props.observing = true;
  flow.policyCloseState.value.blocked = true;
  assert.equal(flow.editingBlocked.value, true);
});
function software(id = "redis", state = {}) {
  return { id, version: "1", runtimeKind: "DOCKER", managementMode: "MANAGED", freshness: "FRESH", observation: { runtimePolicy: {
    actual: model.defaultRuntimePolicy("DOCKER"), editable: true, held: false, ...state,
  } } };
}
function harness(detail, responder = async () => ({ softwareInstanceId: detail.id, jobId: "policy-job" })) {
  const props = vue.reactive({ detail, active: true, disabled: false }), events = [], calls = [];
  const scope = vue.effectScope();
  const flow = scope.run(() => load(component + "\nexport const flow = { policy, dirty, busy, attempted, accepted, error, canEdit, changed, save, requiresUpdate };", {
    vue, "./api": { ApiError, request: async (path, method, body) => { calls.push({ path, method, body: structuredClone(body) }); return responder(path, method, body); } },
    "./runtimePolicy": model,
  }, { defineProps: () => props, defineEmits: () => (...event) => events.push(event) }).flow);
  return { flow, props, calls, events, close() { scope.stop(); } };
}

test("runtime policy uses each supervisor's real options and keeps inherited values absent", () => {
  assert.equal(model.runtimePolicySummary(undefined), "保持当前配置");
  const docker = model.normalizeRuntimePolicy({ restart: "on-failure", autoStart: true, restartDelaySeconds: 5, maxRetries: 3, stopTimeoutSeconds: 10 }, "DOCKER");
  assert.deepEqual(docker, { restart: "on-failure", autoStart: false, maxRetries: 3, stopTimeoutSeconds: 10 });
  assert.equal(model.normalizeRuntimePolicy({ ...docker, restart: "always" }, "DOCKER").maxRetries, undefined);
  const native = model.normalizeRuntimePolicy({ restart: "always", autoStart: false, maxRetries: 2, stopTimeoutSeconds: 30 }, "SYSTEMD");
  assert.equal(native.autoStart, false);
  assert.equal(native.maxRetries, undefined);
  assert.match(model.runtimePolicyError({ restart: "no", autoStart: false }, "SYSTEMD"), /停止等待/);
});

test("editing a held service updates its saved strategy without sending a start operation", async t => {
  const configured = model.defaultRuntimePolicy("DOCKER");
  const fixture = harness(software("redis", { held: true, actual: { ...configured, restart: "no", autoStart: false }, configured }));
  t.after(() => fixture.close());
  assert.equal(fixture.flow.policy.value.restart, "unless-stopped");
  fixture.flow.changed({ ...configured, restart: "on-failure", autoStart: false });
  await fixture.flow.save(); await flush();
  assert.equal(fixture.calls.length, 1);
  assert.equal(fixture.calls[0].body.action, "RUNTIME_POLICY");
  assert.equal(fixture.calls[0].body.runtimePolicy.restart, "on-failure");
  assert.equal(fixture.flow.accepted.value, true);
  assert.equal(fixture.flow.dirty.value, false);
  assert.equal(fixture.events.some(event => event[0] === "submitted" && event[1].jobId === "policy-job"), true);
});

test("a lost policy response freezes the original account, version and idempotency key", async t => {
  let first = true;
  const fixture = harness(software(), async () => { if (first) { first = false; throw new Error("network lost"); } return { softwareInstanceId: "redis", jobId: "same-job" }; });
  t.after(() => fixture.close());
  fixture.flow.changed({ ...fixture.flow.policy.value, restart: "no", autoStart: false });
  await fixture.flow.save();
  assert.equal(fixture.flow.attempted.value, true);
  fixture.flow.changed(model.defaultRuntimePolicy("DOCKER"));
  fixture.props.detail.version = "9";
  await fixture.flow.save();
  assert.deepEqual(fixture.calls[1].body, fixture.calls[0].body);
  assert.equal(fixture.calls[1].body.expectedVersion, "1");
});

test("read-only, stale and unsupported services cannot apply a runtime policy", async t => {
  for (const updates of [{ managementMode: "OBSERVE" }, { freshness: "STALE" }, { observation: { runtimePolicy: { ...software().observation.runtimePolicy, editable: false } } }]) {
    const fixture = harness({ ...software(), ...updates }); t.after(() => fixture.close());
    fixture.flow.changed({ ...fixture.flow.policy.value, restart: "no", autoStart: false });
    await fixture.flow.save(); assert.equal(fixture.calls.length, 0);
  }
  const fixture = harness({ ...software(), observation: {} }); t.after(() => fixture.close());
  assert.equal(fixture.events.some(event => event[0] === "observe"), true);
  assert.equal(fixture.flow.canEdit.value, undefined);
});

test("changing software discards the old draft and an explicit rejection permits correcting it", async t => {
  const fixture = harness(software(), async () => { throw new ApiError(400); }); t.after(() => fixture.close());
  fixture.flow.changed({ ...fixture.flow.policy.value, restart: "no", autoStart: false });
  await fixture.flow.save();
  assert.equal(fixture.flow.attempted.value, false);
  fixture.props.detail = software("nginx", { actual: { ...model.defaultRuntimePolicy("DOCKER"), restart: "always" } });
  await flush();
  assert.equal(fixture.flow.policy.value.restart, "always");
  assert.equal(fixture.flow.dirty.value, false);
});

test("a failed policy read permits a manual retry and auto-reads only once per opening", async t => {
  const fixture = harness({ ...software(), freshness: "STALE", errorCode: "SSH_TIMEOUT" }); t.after(() => fixture.close());
  assert.equal(fixture.flow.canEdit.value, false);
  assert.equal(fixture.events.filter(event => event[0] === "observe").length, 1);
  fixture.props.disabled = true; await flush();
  fixture.props.disabled = false; await flush();
  assert.equal(fixture.events.filter(event => event[0] === "observe").length, 1);
  fixture.props.active = false; await flush();
  fixture.props.active = true; await flush();
  assert.equal(fixture.events.filter(event => event[0] === "observe").length, 2);
  assert.equal(fixture.calls.length, 0);
});

test("an older executor's successful observation exposes the component update path", t => {
  const fixture = harness({ ...software(), observation: { observedAt: "2026-11-12T00:00:00Z" } }); t.after(() => fixture.close());
  assert.equal(fixture.flow.requiresUpdate.value, true);
  fixture.props.detail.errorCode = "SSH_TIMEOUT";
  assert.equal(fixture.flow.requiresUpdate.value, false);
  assert.equal(fixture.flow.canEdit.value, undefined);
});
