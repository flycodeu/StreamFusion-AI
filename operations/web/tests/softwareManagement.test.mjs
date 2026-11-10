import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";

const compile = source => ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText;
const parameters = {};
new Function("exports", compile(await readFile(new URL("../src/softwareManagement.ts", import.meta.url), "utf8")))(parameters);
const component = (await readFile(new URL("../src/SoftwareManagementPanel.vue", import.meta.url), "utf8")).split('<script setup lang="ts">')[1].split("</script>")[0];
const copy = value => JSON.parse(JSON.stringify(value));
class TestApiError extends Error { constructor(code) { super("资源已变化，请重新检查"); this.code = code; this.status = 409; } }
const definition = { management: { parameters: [{ key: "ADMIN", label: "管理密码", type: "CREDENTIAL" }], operations: [
  { key: "query", name: "查看列表", readOnly: true, runtimeModes: ["NATIVE"], parameters: [], steps: [], resultFormat: "TABLE" },
  { key: "create", name: "新增项目", parameters: [{ key: "NAME", label: "名称", type: "TEXT", required: true }], steps: [], verifySteps: [], resultFormat: "TEXT" },
  { key: "container", name: "容器检查", readOnly: true, runtimeModes: ["DOCKER"], parameters: [], steps: [], resultFormat: "TEXT" },
] } };
function harness({ lastJobId = "", status = "SUCCEEDED", loseReceipt = false, managementDefinition = definition, presentation, table, reactive = false, intercept, timers } = {}) {
  const calls = [], events = [], cleanups = [], scope = vue.effectScope();
  const context = { definition: copy(managementDefinition), softwareVersion: "7", templateId: "template", templateRevision: 3, stateVersion: "0", parameters: {}, credentialRefs: {}, lastJobId };
  const rawProps = { detail: { id: "software", version: "7", runtimeKind: "SYSTEMD", installation: { config: {} } }, active: false, disabled: false, presentation };
  const props = reactive ? vue.reactive(rawProps) : rawProps;
  let plannedOperation = "", receipts = 0;
  const request = async (path, method = "GET", data) => {
    calls.push({ path, method, data: data && copy(data) });
    const intercepted = intercept?.(path, method, data);
    if (intercepted !== undefined) return intercepted;
    if (path.endsWith("/management")) return copy(context);
    if (path.endsWith("/operation-plans")) { plannedOperation = data.operationKey; return { id: "plan", operation: copy(context.definition.management.operations.find(item => item.key === data.operationKey)), checks: [], resources: [], expiresAt: "2099-01-01" }; }
    if (path.endsWith("/submit")) {
      if (loseReceipt) { loseReceipt = false; throw new Error("连接中断"); }
      context.softwareVersion = String(Number(context.softwareVersion) + 2);
      return { jobId: ++receipts === 1 ? "original-job" : `job-${receipts}`, softwareInstanceId: props.detail.id };
    }
    if (path.endsWith("/reconcile")) { status = "SUCCEEDED"; return {}; }
    if (path.startsWith("/jobs/")) return { id: path.split("/")[2], status, operationKey: plannedOperation, kind: "SOFTWARE_OPERATION", targetAckPending: false, result: { management: { table: table || { columns: ["名称"], rows: [{ "名称": "示例" }], total: 3 }, output: "" } } };
    throw new Error(`Unexpected request ${method} ${path}`);
  };
  const exports = {};
  scope.run(() => new Function("require", "exports", "defineProps", "defineEmits", "setTimeout", "clearTimeout", compile(component + "\nexport { load, check, submit, reconcile, chooseOperation, selectOperation, operations, operationKey, operationParameters, operationCredentialRefs, parameters, credentialRefs, context, job, pending, blocked, closeBlocked, dirty, pendingSelection, error, sharedFields, configurationReady, currentValues, readConfiguration, checkMigration, migrationChecked, acknowledgementPaused, needsAccount };"))(name => {
    if (name === "vue") return { ...vue, onUnmounted: callback => cleanups.push(callback) };
    if (name === "./api") return { request, ApiError: TestApiError };
    if (name === "./softwareManagement") return parameters;
    if (name === "./software") return {};
    return {};
  }, exports, () => props, () => (...args) => events.push(args), timers?.setTimeout || setTimeout, timers?.clearTimeout || clearTimeout));
  props.active = true;
  return { form: exports, calls, events, props, dispose() { cleanups.forEach(callback => callback()); scope.stop(); } };
}

test("management parameters validate paths, required fields and narrowed port ranges before a request", () => {
  const fields = [{ key: "ROOT", label: "目录", type: "PATH", required: true }, { key: "PORT", label: "端口", type: "PORT", min: 1024, max: 9000 }];
  for (const value of ["relative", "/var//app", "/var/../app", "/var/app/", " "])
    assert.ok(parameters.parameterError(fields, { ROOT: value, PORT: "8080" }));
  assert.ok(parameters.parameterError(fields, { ROOT: "/var/app", PORT: "80" }));
  assert.equal(parameters.parameterError(fields, { ROOT: "/var/app", PORT: "8080" }), "");
  assert.equal(parameters.parameterError([{ key: "TOKEN", label: "令牌", type: "CREDENTIAL", required: true }], {}, { TOKEN: { id: "credential", revision: "1" } }), "");
});

const configurationDefinition = { management: { parameters: [
  { key: "WEB_PORT", label: "端口", type: "PORT", required: true, default: "8080" },
  { key: "SOURCE_DIRECTORY", label: "素材目录", type: "PATH", default: "" },
], operations: [
  { key: "read-config", name: "读取配置", readOnly: true, steps: [], resultFormat: "TABLE" },
  { key: "configure", name: "保存配置", rebind: true, steps: [], verifySteps: [], resultFormat: "TEXT" },
  { key: "check-data-migration", name: "检查迁移", readOnly: true, parameters: [{ key: "DATA_DIRECTORY", label: "目标数据目录", type: "PATH", required: true }], steps: [], resultFormat: "TABLE" },
  { key: "migrate-data", name: "迁移数据", rebind: true, parameters: [{ key: "DATA_DIRECTORY", label: "目标数据目录", type: "PATH", required: true }], steps: [], verifySteps: [], resultFormat: "TEXT" },
] } };
const configurationTable = { columns: ["key", "value"], rows: [{ key: "WEB_PORT", value: "9924" }, { key: "SOURCE_DIRECTORY", value: "/data/images" }, { key: "DATA_DIRECTORY", value: "/data/app" }] };

test("configuration reads actual values before preview and never saves until explicitly confirmed", async () => {
  const h = harness({ managementDefinition: configurationDefinition, presentation: "configuration", table: configurationTable });
  try {
    await h.form.load();
    assert.equal(h.form.configurationReady.value, true);
    assert.equal(h.form.parameters.value.WEB_PORT, "9924");
    assert.equal(h.form.parameters.value.DATA_DIRECTORY, undefined);
    assert.equal(h.form.operationKey.value, "configure");
    assert.equal(h.calls.filter(call => call.path.endsWith("/submit")).length, 1, "only the read-config job was submitted");
    h.form.parameters.value.WEB_PORT = "19924";
    await h.form.check();
    assert.equal(h.calls.filter(call => call.path.endsWith("/submit")).length, 1);
    await h.form.submit();
    assert.equal(h.calls.filter(call => call.path.endsWith("/submit")).length, 2);
    assert.equal(h.events.filter(([event]) => event === "submitted").length, 1);
    const request = h.calls.filter(call => call.path.endsWith("/operation-plans")).at(-1).data;
    assert.equal(request.expectedVersion, "9", "readonly job committed a new software version");
    assert.equal(request.parameters.SOURCE_DIRECTORY, "/data/images");
    assert.equal(request.installation, undefined);
  } finally { h.dispose(); }
});

test("failed or incomplete config reads cannot turn into a writable plan", async () => {
  for (const [status, table] of [["FAILED", configurationTable], ["SUCCEEDED", { columns: ["key", "value"], rows: [] }]]) {
    const h = harness({ managementDefinition: configurationDefinition, presentation: "configuration", status, table });
    try {
      await h.form.load();
      assert.equal(h.form.configurationReady.value, false);
      assert.ok(h.form.error.value);
      const before = h.calls.length;
      h.form.selectOperation("configure"); await h.form.check(); await h.form.submit();
      assert.equal(h.calls.length, before);
    } finally { h.dispose(); }
  }
});

test("migration needs its readonly check, a write preview and an explicit separate confirmation", async () => {
  const h = harness({ managementDefinition: configurationDefinition, presentation: "migration", table: configurationTable });
  try {
    await h.form.load();
    h.form.operationParameters.value.DATA_DIRECTORY = "/data/moved";
    await h.form.check();
    assert.equal(h.calls.filter(call => call.path.endsWith("/submit")).length, 1);
    await h.form.checkMigration();
    assert.equal(h.form.migrationChecked.value, true);
    assert.equal(h.calls.filter(call => call.path.endsWith("/submit")).length, 2, "config read and migration check only");
    await h.form.check();
    assert.equal(h.calls.filter(call => call.path.endsWith("/submit")).length, 2);
    await h.form.submit();
    assert.equal(h.calls.filter(call => call.path.endsWith("/submit")).length, 3);
    assert.equal(h.calls.filter(call => call.path.endsWith("/operation-plans")).at(-1).data.operationKey, "migrate-data");
  } finally { h.dispose(); }
});

test("a late config read from a previously selected software cannot overwrite the current form", async () => {
  let finish;
  const old = new Promise(resolve => { finish = resolve; });
  const h = harness({ reactive: true, managementDefinition: configurationDefinition, presentation: "configuration", table: configurationTable,
    intercept: path => path === "/jobs/original-job" ? old : undefined });
  try {
    await new Promise(resolve => setImmediate(resolve));
    h.props.detail.id = "other-software";
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(h.form.configurationReady.value, true);
    finish({ id: "original-job", status: "SUCCEEDED", operationKey: "read-config", result: { management: { table: { ...configurationTable, rows: configurationTable.rows.map(row => row.key === "WEB_PORT" ? { ...row, value: "1111" } : row) } } } });
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(h.form.parameters.value.WEB_PORT, "9924");
  } finally { h.dispose(); }
});

test("latest configure templates are selected for reading without making diagnostics-only software editable", () => {
  const item = { managementMode: "MANAGED", installationStatus: "READY", runtimeKind: "SYSTEMD", installation: { definition: { commands: { builtin: false }, management: { operations: [{ key: "diagnostics", readOnly: true }] } } } };
  assert.equal(parameters.softwareConfigurationAvailable(item), false);
  const context = parameters.configurationContext({ stateVersion: "7", definition: item.installation.definition, availableTemplate: { stateVersion: "0", definition: configurationDefinition } }, item);
  assert.equal(context.stateVersion, "7");
  assert.equal(parameters.hasConfiguration(context, item), true);
});

test("successful jobs keep polling the acknowledgement window and unlock only after acknowledgement", async () => {
  let reads = 0, next;
  const h = harness({ managementDefinition: configurationDefinition, presentation: "configuration", table: configurationTable,
    timers: { setTimeout: callback => { next = callback; return 1; }, clearTimeout() {} },
    intercept: path => path.startsWith("/jobs/") && ++reads === 1 ? { id: "original-job", status: "SUCCEEDED", targetAckPending: true } : undefined });
  try {
    await h.form.load();
    assert.equal(h.form.pending.value, true);
    assert.equal(h.form.configurationReady.value, false);
    next(); await new Promise(resolve => setImmediate(resolve));
    assert.equal(h.form.pending.value, false);
    assert.equal(h.form.configurationReady.value, true);
    assert.equal(h.calls.filter(call => call.path.endsWith("/submit")).length, 1);
  } finally { h.dispose(); }
});

test("unconfirmed acknowledgement polling is bounded and never resubmits a job", async () => {
  let next;
  const h = harness({ timers: { setTimeout: callback => { next = callback; return 1; }, clearTimeout() {} },
    intercept: path => path.startsWith("/jobs/") ? { id: "original-job", status: "SUCCEEDED", targetAckPending: true } : undefined });
  try {
    await h.form.load(); await h.form.check();
    for (let index = 0; index < 5; index++) { const callback = next; next = undefined; callback(); await new Promise(resolve => setImmediate(resolve)); }
    assert.equal(next, undefined);
    assert.equal(h.form.acknowledgementPaused.value, true);
    assert.equal(h.form.pending.value, true);
    await h.form.check(); await h.form.submit();
    assert.equal(h.calls.filter(call => call.path.endsWith("/submit")).length, 1);
  } finally { h.dispose(); }
});

test("resource-change conflicts release the stale plan so the form can be checked again", async () => {
  let rejected = false;
  const h = harness({ intercept: path => {
    if (path.endsWith("/submit") && !rejected) { rejected = true; throw new TestApiError("MANAGEMENT_RESOURCES_CHANGED"); }
  } });
  try {
    await h.form.load(); h.form.selectOperation("create"); h.form.operationParameters.value.NAME = "A";
    await h.form.check(); await h.form.submit();
    assert.equal(h.form.blocked.value, false);
    assert.equal(h.form.pending.value, false);
    await h.form.check();
    assert.equal(h.calls.filter(call => call.path.endsWith("/operation-plans")).length, 2);
  } finally { h.dispose(); }
});

test("an account is requested when only rollback steps need it", async () => {
  const configured = copy(definition);
  configured.management.operations[1].rollbackSteps = [{ name: "恢复", runAs: "ACCOUNT", script: "true" }];
  const h = harness({ managementDefinition: configured });
  try { await h.form.load(); h.form.selectOperation("create"); assert.equal(h.form.needsAccount.value, true); }
  finally { h.dispose(); }
});

test("configuration readonly work can close while a missing writable receipt stays protected", async () => {
  let finish;
  const delayed = new Promise(resolve => { finish = resolve; });
  const h = harness({ managementDefinition: configurationDefinition, presentation: "configuration", intercept: path => path === "/jobs/original-job" ? delayed : undefined });
  try {
    const loading = h.form.load(); await new Promise(resolve => setImmediate(resolve));
    assert.equal(h.form.blocked.value, true);
    assert.equal(h.form.closeBlocked.value, false);
    h.dispose(); finish({ id: "original-job", status: "SUCCEEDED", result: { management: { table: configurationTable } } }); await loading;
    assert.equal(h.form.configurationReady.value, false);
  } finally { h.dispose(); }
  const write = harness({ presentation: "configuration", loseReceipt: true });
  try {
    await write.form.load();
    write.form.configurationReady.value = true; write.form.selectOperation("create"); write.form.operationParameters.value.NAME = "A";
    await write.form.check(); await write.form.submit();
    assert.equal(write.form.closeBlocked.value, true);
  } finally { write.dispose(); }
});

test("closing an initial configuration load aborts it and ignores its late response", async () => {
  const source = (await readFile(new URL("../src/SoftwareConfigurationDialog.vue", import.meta.url), "utf8")).split('<script setup lang="ts">')[1].split("</script>")[0];
  const props = { item: { id: "software", name: "测试" } }, events = [], cleanups = [], scope = vue.effectScope(), form = {};
  let finish, signal;
  const delayed = new Promise(resolve => { finish = resolve; });
  scope.run(() => new Function("require", "exports", "defineProps", "defineEmits", compile(source + "\nexport { loading, detail, close };"))(name => {
    if (name === "vue") return { ...vue, onUnmounted: callback => cleanups.push(callback) };
    if (name === "./api") return { request: (_path, _method, _body, abort) => { signal = abort; return delayed; } };
    if (name === "./softwareManagement") return parameters;
    return {};
  }, form, () => props, () => (...args) => events.push(args)));
  try {
    assert.equal(form.loading.value, true);
    await form.close();
    assert.equal(signal.aborted, true);
    assert.deepEqual(events, [["close"]]);
    finish({ id: "software", installation: {} }); await new Promise(resolve => setImmediate(resolve));
    assert.equal(form.detail.value, undefined);
  } finally { cleanups.forEach(callback => callback()); scope.stop(); }
});

test("queries use the selected runtime and signed job flow while returning structured totals", async () => {
  const h = harness();
  try {
    await h.form.load();
    assert.deepEqual(h.form.operations.value.map(item => item.key), ["query", "create"]);
    h.form.credentialRefs.value = { ADMIN: { id: "credential", revision: "2" } };
    await h.form.check();
    assert.deepEqual(h.calls.filter(call => call.method === "POST").map(call => call.path), ["/software-instances/software/operation-plans", "/software-operation-plans/plan/submit"]);
    const plan = h.calls.find(call => call.path.endsWith("operation-plans")).data;
    assert.deepEqual(plan.credentialRefs, { ADMIN: { id: "credential", revision: "2" } });
    assert.equal(plan.expectedVersion, "7");
    assert.equal(h.form.job.value.result.management.table.total, 3);
    assert.equal(h.form.pending.value, false);
  } finally { h.dispose(); }
  const dockerTool = harness();
  try {
    dockerTool.props.detail.runtimeKind = "TOOL";
    dockerTool.props.detail.installation.config.runtimeMode = "DOCKER";
    await dockerTool.form.load();
    assert.deepEqual(dockerTool.form.operations.value.map(item => item.key), ["create", "container"]);
  } finally { dockerTool.dispose(); }
});

test("mutations require preview and explicit submit, and missing input never reaches the controller", async () => {
  const h = harness();
  try {
    await h.form.load(); h.form.selectOperation("create");
    await h.form.check();
    assert.equal(h.calls.filter(call => call.method === "POST").length, 0);
    h.form.operationParameters.value.NAME = "项目A";
    await h.form.check();
    assert.equal(h.calls.filter(call => call.path.endsWith("/submit")).length, 0);
    await h.form.submit();
    assert.equal(h.calls.filter(call => call.path.endsWith("/submit")).length, 1);
    assert.deepEqual(h.events.find(([name]) => name === "submitted")[1], { jobId: "original-job", softwareInstanceId: "software" });
  } finally { h.dispose(); }
});

test("a lost submission receipt is checked with the original plan and idempotency key", async () => {
  const h = harness({ loseReceipt: true });
  try {
    await h.form.load(); h.form.selectOperation("create"); h.form.operationParameters.value.NAME = "A";
    await h.form.check(); await h.form.submit();
    assert.equal(h.form.blocked.value, true);
    await h.form.check(); await h.form.submit();
    const submits = h.calls.filter(call => call.path.endsWith("/submit"));
    assert.equal(h.calls.filter(call => call.path.endsWith("operation-plans")).length, 1);
    assert.equal(submits.length, 2);
    assert.deepEqual(submits[0], submits[1]);
    assert.equal(h.form.job.value.id, "original-job");
  } finally { h.dispose(); }
});

test("reopening an uncertain job offers reconciliation and never replays its operation", async () => {
  const h = harness({ lastJobId: "uncertain-job", status: "UNKNOWN" });
  try {
    await h.form.load(); await new Promise(resolve => setImmediate(resolve));
    assert.equal(h.form.pending.value, true);
    h.form.chooseOperation("create"); await h.form.check();
    assert.equal(h.form.operationKey.value, "query");
    assert.equal(h.calls.filter(call => call.method === "POST").length, 0);
    await h.form.reconcile();
    assert.deepEqual(h.calls.filter(call => call.method === "POST").map(call => call.path), ["/jobs/uncertain-job/reconcile"]);
    assert.equal(h.form.pending.value, false);
  } finally { h.dispose(); }
});

test("operation switches retain shared configuration and confirm discarding operation input", async () => {
  const h = harness();
  try {
    await h.form.load(); h.form.credentialRefs.value = { ADMIN: { id: "new-secret-ref", revision: "1" } };
    await vue.nextTick(); h.form.chooseOperation("create");
    assert.equal(h.form.operationKey.value, "create");
    assert.equal(h.form.credentialRefs.value.ADMIN.id, "new-secret-ref");
    h.form.operationParameters.value.NAME = "未保存";
    h.form.chooseOperation("query");
    assert.equal(h.form.pendingSelection.value, "query");
    assert.equal(h.form.operationKey.value, "create");
  } finally { h.dispose(); }
});

test("leaving the panel during credential save prevents a later operation request", async () => {
  const h = harness();
  try {
    await h.form.load();
    let release;
    h.form.sharedFields.value = { saveCredentials: () => new Promise(resolve => { release = resolve; }) };
    const checking = h.form.check();
    h.props.active = false; release(true); await checking;
    assert.equal(h.calls.filter(call => call.method === "POST").length, 0);
  } finally { h.dispose(); }
});
