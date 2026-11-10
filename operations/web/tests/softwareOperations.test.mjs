import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";

const compile = (source) => ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
}).outputText;
function moduleFrom(source, imports = {}, timers = {}) {
  const module = { exports: {} };
  new Function("require", "module", "exports", "setTimeout", "clearTimeout", "document", compile(source))(
    (name) => imports[name], module, module.exports, timers.setTimeout || (() => 1), timers.clearTimeout || (() => {}), timers.document || { hidden: true },
  );
  return module.exports;
}
const installationSource = await readFile(new URL("../src/serviceInstallation.ts", import.meta.url), "utf8");
const installation = moduleFrom(installationSource);
const software = moduleFrom(await readFile(new URL("../src/software.ts", import.meta.url), "utf8"));
const softwareCatalog = moduleFrom(await readFile(new URL("../src/softwareCatalog.ts", import.meta.url), "utf8"));
const webAccess = moduleFrom(await readFile(new URL("../src/webAccess.ts", import.meta.url), "utf8"));
test("operator labels hide protocol codes without claiming unknown work succeeded", () => {
  assert.equal(software.softwareJobPhaseLabel("SERVICE_INSTALLED"), "软件已安装");
  assert.equal(software.softwareJobPhaseLabel("SOFTWARE_UNINSTALLED"), "软件已卸载");
  assert.equal(software.softwareJobStatusLabel("UNKNOWN"), "结果未确认");
  assert.equal(software.softwareJobStatusLabel("FUTURE_STATE"), "状态待确认");
  assert.equal(software.softwareJobPhaseLabel("FUTURE_PHASE"), "阶段待确认");
  assert.equal(software.softwareObservationMessage("SERVICE_INSTALLED"), "");
  assert.equal(software.softwareObservationMessage("服务启动成功"), "服务启动成功");
  assert.equal(software.softwareObservationMessage("FUTURE_CODE"), "检查结果待确认");
  assert.equal(software.softwareObservationMessage("SERVICE_INSTALLED", "SSH_TIMEOUT"), "检查未完成，请查看诊断信息");
  assert.equal(software.softwareVersionLabel({ versionSource: "TEMPLATE", versionStatus: "DECLARED" }, "DOCKER"), "配置版本");
});
const useSource = await readFile(new URL("../src/useServiceInstallation.ts", import.meta.url), "utf8");
const preparationUpdate = moduleFrom(await readFile(new URL("../src/preparationUpdate.ts", import.meta.url), "utf8"));
const uninstallComponent = await readFile(new URL("../src/SoftwareUninstallDialog.vue", import.meta.url), "utf8");
const uninstallScript = uninstallComponent.split('<script setup lang="ts">')[1].split("</script>")[0];
const catalogComponent = await readFile(new URL("../src/SoftwareRecipes.vue", import.meta.url), "utf8");
const catalogScript = catalogComponent.split('<script setup lang="ts">')[1].split("</script>")[0];
const installComponent = await readFile(new URL("../src/ServiceInstallDialog.vue", import.meta.url), "utf8");
const installScript = installComponent.split('<script setup lang="ts">')[1].split("</script>")[0];
const recipe = {
  key: "java", name: "Java", kind: "TOOL", version: "21", revision: "r1", platform: "linux/amd64",
  runtimeModes: ["DOCKER", "NATIVE"], sourceModes: ["ARCHIVE", "ONLINE", "REGISTRY"], port: 0,
};
const material = (id, mode, origin = "UPLOADED", available = true) => ({
  id, name: "jdk.tar.gz", recipeName: "Eclipse Temurin JDK", recipeKey: "java", recipeVersion: "21",
  recipeRevision: "r1", runtimeMode: mode, origin: { kind: origin }, status: "AVAILABLE", available, byteSize: 1024,
});
test("installation defaults distinguish a missing engine from unavailable or stale evidence", () => {
  const service = { ...recipe, key: "redis", kind: "SERVICE" };
  const host = (status, code, expiresAt = "2099-01-01T00:00:00Z") => ({ capabilities: { docker: { status, code }, expiresAt } });
  for (const [facts, expected] of [
    [host("AVAILABLE", "DOCKER_ENGINE_READY"), "DOCKER"],
    [host("UNAVAILABLE", "DOCKER_NOT_INSTALLED"), "NATIVE"],
    [host("UNAVAILABLE", "DOCKER_ENGINE_UNAVAILABLE"), "DOCKER"],
    [host("UNKNOWN", "DOCKER_ACCESS_UNCONFIRMED"), undefined],
    [host("UNAVAILABLE", "DOCKER_NOT_INSTALLED", "2000-01-01"), undefined],
  ]) assert.equal(installation.defaultInstallationRuntime([service], facts), expected);
  assert.equal(installation.defaultInstallationRuntime([recipe]), "NATIVE");
  assert.equal(installation.defaultInstallationRuntime([{ ...service, runtimeModes: ["DOCKER"] }], host("UNAVAILABLE", "DOCKER_NOT_INSTALLED")), "DOCKER");
});
async function flush() { for (let i = 0; i < 10; i++) { await Promise.resolve(); await vue.nextTick(); } }
function executionTimer() {
  let callback;
  const document = { hidden: false };
  return { document, setTimeout(fn) { callback = fn; return 1; }, clearTimeout() { callback = undefined; },
    async tick() { const fn = callback; callback = undefined; if (fn) await fn(); await flush(); } };
}
function installationHarness(materials, options = {}) {
  const calls = [], scopes = [];
  const props = vue.reactive({ open: true, recipe: options.recipe || recipe, method: options.method, sourceMode: options.sourceMode, runtimeMode: options.runtimeMode, existing: options.existing, host: { id: "host-a", version: "1", address: "10.1.2.3", kind: options.hostKind, sshUser: options.sshUser } });
  const request = async (path, method, data) => {
    calls.push({ path, method, data });
    if (options.request) return options.request(path, method, data);
    if (path === "/credentials") return [];
    if (path.startsWith("/installation-materials?")) return materials;
    if (path.endsWith("/software-install-plans")) return { id: "plan", expiresAt: "2099-01-01T00:00:00Z", source: data.source };
    throw new Error(`Unexpected request ${path}`);
  };
  const composable = moduleFrom(useSource, {
    vue: { ...vue, onUnmounted: (fn) => scopes.push(fn) },
    "./api": { ApiError: Error, request }, "./serviceInstallation": installation,
    "./preparationUpdate": preparationUpdate,
    "./webAccess": webAccess,
  }, options.timers);
  const scope = vue.effectScope();
  const flow = scope.run(() => composable.useServiceInstallation(props, () => {}, () => {}));
  return { flow, props, calls, dispose() { scopes.forEach((fn) => fn()); scope.stop(); } };
}

test("installation defaults to ready bundled library material without a network retrieval", async () => {
  const h = installationHarness([
    material("uploaded", "NATIVE"), material("bundle-missing", "NATIVE", "BUNDLED", false),
    material("bundled", "NATIVE", "BUNDLED"), material("docker", "DOCKER", "BUNDLED"),
  ]);
  await flush();
  assert.equal(h.flow.sourceType.value, "ARCHIVE");
  assert.equal(h.flow.materialId.value, "bundled");
  assert.equal(h.calls.some((call) => call.path === "/installation-materials/official"), false);
  await h.flow.preflight();
  assert.deepEqual(h.calls.at(-1).data.source, { type: "ARCHIVE", materialId: "bundled" });
  h.dispose();
});

test("native online installation does not need or transfer a platform material", async () => {
  const h = installationHarness([], { sourceMode: "ONLINE", runtimeMode: "NATIVE" });
  await flush();
  await h.flow.next();
  assert.equal(h.flow.fieldError.value, "");
  assert.equal(h.flow.step.value, 3);
  assert.deepEqual(h.calls.at(-1).data.source, { type: "ONLINE" });
  assert.equal(h.calls.some(({ path }) => path.startsWith("/installation-materials")), false);
  h.dispose();
});

test("changing source preserves instance settings and never sends an offline material for online install", async () => {
  const h = installationHarness([material("bundle", "NATIVE", "BUNDLED")], { sourceMode: "ONLINE", runtimeMode: "NATIVE" });
  await flush();
  h.flow.configuration.value.name = "Project Java";
  await h.flow.next();
  h.props.sourceMode = "ARCHIVE";
  await flush();
  assert.equal(h.flow.plan.value, undefined);
  assert.equal(h.flow.configuration.value.name, "Project Java");
  assert.equal(h.flow.materialId.value, "bundle");
  await h.flow.next();
  assert.deepEqual(h.calls.at(-1).data.source, { type: "ARCHIVE", materialId: "bundle" });
  h.props.sourceMode = "ONLINE";
  await flush();
  await h.flow.next();
  assert.deepEqual(h.calls.at(-1).data.source, { type: "ONLINE" });
  h.dispose();
});

test("switching grouped draft recipes preserves shared settings but resets checks and material identity", async () => {
  const port = key => ({ key, label: key, protocol: "tcp", defaultPort: 9000, containerPort: 9000, required: true });
  const offline = { ...recipe, key: "custom-worker-offline", catalogKey: "worker", name: "Worker", kind: "SERVICE", port: 8080,
    runtimeModes: ["NATIVE"], sourceModes: ["ARCHIVE"], ports: [port("admin"), port("legacy")],
    commands: { kind: "SYSTEMD", locator: "worker.service", steps: [{ runAs: "ACCOUNT" }] } };
  const online = { ...offline, key: "custom-worker-online", sourceModes: ["ONLINE"], ports: [port("admin"), port("metrics")] };
  const h = installationHarness([{ ...material("offline-package", "NATIVE"), recipeKey: offline.key }], {
    recipe: offline, sourceMode: "ARCHIVE", runtimeMode: "NATIVE", sshUser: "ubuntu",
  });
  await flush();
  const retained = { name: "Project Worker", storageRoot: "/opt/project-worker", account: "worker", bindAddress: "10.1.2.4", hostPort: 8181, memoryMiB: 1024 };
  Object.assign(h.flow.configuration.value, retained, { ports: { admin: 9191, legacy: 9292 }, credentialId: "old-secret" });
  await h.flow.next();
  assert.equal(h.flow.plan.value.id, "plan");
  assert.equal(h.flow.materialId.value, "offline-package");
  h.props.recipe = online;
  h.props.sourceMode = "ONLINE";
  await flush();
  for (const [field, value] of Object.entries(retained)) assert.equal(h.flow.configuration.value[field], value);
  assert.deepEqual({ ...h.flow.configuration.value.ports }, { admin: 9191, metrics: 9000 });
  assert.equal(h.flow.configuration.value.credentialId, "");
  assert.equal(h.flow.materialId.value, "");
  assert.equal(h.flow.plan.value, undefined);
  await h.flow.next();
  assert.equal(h.calls.at(-1).data.recipeKey, online.key);
  assert.deepEqual(h.calls.at(-1).data.source, { type: "ONLINE" });
  Object.assign(h.flow.configuration.value, { bindAddress: "0.0.0.0", hostPort: 0 });
  delete h.flow.configuration.value.account;
  h.props.recipe = offline;
  h.props.sourceMode = "ARCHIVE";
  await flush();
  assert.equal(h.flow.configuration.value.bindAddress, "10.1.2.3");
  assert.equal(h.flow.configuration.value.hostPort, 8080);
  assert.equal(h.flow.configuration.value.account, "ubuntu");
  h.props.host = { ...h.props.host, id: "host-b", address: "10.9.8.7", sshUser: "deploy" };
  await flush();
  assert.equal(h.flow.configuration.value.name, "Worker");
  assert.equal(h.flow.configuration.value.storageRoot, "");
  assert.equal(h.flow.configuration.value.account, "deploy");
  assert.equal(h.flow.configuration.value.bindAddress, "10.9.8.7");
  assert.equal(h.flow.configuration.value.hostPort, 8080);
  assert.equal(h.flow.plan.value, undefined);
  h.flow.configuration.value.name = "Unsaved";
  h.props.open = false;
  await flush();
  h.props.open = true;
  await flush();
  assert.equal(h.flow.configuration.value.name, "Worker");
  h.dispose();
});

test("custom commands use the server account without requesting credentials or inventing a listening port", async () => {
  const custom = { ...recipe, key: "custom-worker", kind: "SERVICE", port: 0, runtimeModes: ["NATIVE"], sourceModes: ["ONLINE"], commands: {
    kind: "SYSTEMD", locator: "sample-worker.service", steps: [
      { phase: "INSTALL", name: "安装", script: 'mkdir -p "$OPS_INSTALL_ROOT"', runAs: "ACCOUNT", timeoutSeconds: 60 },
      { phase: "VERIFY", name: "验证", script: 'test -d "$OPS_INSTALL_ROOT"', runAs: "ACCOUNT", timeoutSeconds: 30 },
    ],
  } };
  const h = installationHarness([], { recipe: custom, sourceMode: "ONLINE", runtimeMode: "NATIVE", sshUser: "ubuntu" });
  await flush();
  assert.equal(h.flow.configuration.value.account, "ubuntu");
  assert.equal(installation.requiresServicePort(custom), false);
  assert.equal(h.calls.length, 0);
  h.flow.configuration.value.account = "root";
  await h.flow.next();
  assert.match(h.flow.fieldError.value, /普通执行账号/);
  assert.equal(h.calls.length, 0);
  h.flow.configuration.value.account = "ubuntu";
  await h.flow.next();
  assert.equal(h.flow.fieldError.value, "");
  assert.equal(h.calls.at(-1).data.config.account, "ubuntu");
  assert.deepEqual(h.calls.at(-1).data.source, { type: "ONLINE" });
  h.dispose();
});

function installDialogHarness(properties, respond = () => [], ui = {}) {
  const calls = [], requests = [], cleanups = [], events = [];
  const request = async (path, method, data) => { calls.push(path); requests.push({ path, method, data }); return respond(path, method, data); };
  const imports = {
    vue: { ...vue, onUnmounted: fn => cleanups.push(fn) },
    "./api": { ApiError: Error, request }, "./serviceInstallation": installation,
    "./preparationUpdate": preparationUpdate,
    "./webAccess": webAccess,
    "element-plus": { ElMessageBox: { confirm: ui.confirm || (async () => true) } },
  };
  const composable = moduleFrom(useSource, imports);
  const props = vue.reactive({ open: true, host: { id: "host-a", version: "1", address: "10.1.2.3" }, ...properties });
  const module = { exports: {} }, scope = vue.effectScope();
  scope.run(() => new Function("require", "module", "exports", "defineProps", "defineEmits", compile(
    installScript + "\nexport { javaVersion, selectedVersion, sourceChoice, sourceMode, recipe, method, nativeAvailable, onlineAvailable, archivePackages, choices, configuration, credentialChoices, mode, installCommands, next, engineInstalling, touched, receipt, job, canRetryInstallation, retryFailedInstallation, showJobLogs, showEngineLogs, resumeSoftware, close };",
  ))(name => name === "./useServiceInstallation" ? composable : imports[name] || {}, module, module.exports, () => props, () => (event, ...args) => events.push([event, ...args])));
  return { calls, requests, props, events, form: module.exports, dispose() { cleanups.forEach(fn => fn()); scope.stop(); } };
}

test("engine log navigation confirms the outer draft and never rechecks or submits after leaving", async () => {
  let confirm = false;
  const h = installDialogHarness({ recipe }, () => [], { confirm: async () => { if (!confirm) throw new Error("continue editing"); } });
  await flush();
  h.form.engineInstalling.value = true;
  h.form.touched.value = true;
  h.form.configuration.value.name = "Project Java";
  await h.form.showEngineLogs("engine-job");
  assert.equal(h.form.engineInstalling.value, true);
  assert.equal(h.form.configuration.value.name, "Project Java");
  assert.deepEqual(h.events, []);
  confirm = true;
  await h.form.showEngineLogs("engine-job");
  assert.deepEqual(h.events, [["logs", "engine-job"], ["close"]]);
  assert.equal(h.form.engineInstalling.value, false);
  const count = h.calls.length;
  await h.form.resumeSoftware();
  assert.equal(h.calls.length, count);
  assert.equal(h.requests.some(call => call.method === "POST"), false);
  h.dispose();
});

test("embedded engine log navigation leaves closing to the outer draft confirmation", async () => {
  const h = installDialogHarness({ recipe, embedded: true });
  await flush();
  h.form.receipt.value = { jobId: "engine-job", softwareInstanceId: "engine" };
  h.form.showJobLogs();
  assert.deepEqual(h.events, [["logs", "engine-job"]]);
  h.dispose();
});

test("a delayed discard confirmation cannot close a different installation context", async () => {
  let accept;
  const h = installDialogHarness({ recipe }, () => [], { confirm: () => new Promise(resolve => { accept = resolve; }) });
  await flush();
  h.form.touched.value = true;
  const closing = h.form.close();
  h.props.host.id = "host-b";
  await flush();
  accept();
  await closing;
  assert.deepEqual(h.events, []);
  h.dispose();
});

test("explicit pre-queue installation rejection returns to editable settings without automatic resubmission", async () => {
  for (const code of ["INSTALL_PLAN_STALE", "INSTALL_MATERIAL_NOT_READY", "INSTALL_MATERIAL_CHANGED", "OFFICIAL_PLAN_CHANGED", "INSTALL_PREFLIGHT_MISMATCH", "QUEUE_FULL", "TOOL_ALREADY_INSTALLED", "OPERATION_LOCKED"]) {
    let plans = 0;
    const h = installationHarness([], { sourceMode: "ONLINE", runtimeMode: "NATIVE", request: async (path, method, data) => {
      if (path === "/credentials") return [];
      if (path.endsWith("/software-install-plans")) return { id: `plan-${++plans}`, expiresAt: "2099-01-01T00:00:00Z", source: data.source };
      throw Object.assign(new Error("check again"), { code, status: 409 });
    } });
    await flush();
    h.flow.configuration.value.name = "Project Java";
    await h.flow.preflight();
    await h.flow.submit();
    const first = h.calls.filter(call => call.path.endsWith("/submit"));
    assert.equal(first.length, 1, code);
    assert.equal(h.flow.submissionAttempted.value, false, code);
    assert.equal(h.flow.plan.value, undefined, code);
    assert.equal(h.flow.step.value, 0, code);
    assert.equal(h.flow.configuration.value.name, "Project Java", code);
    assert.equal(h.flow.busy.value, false, code);
    await h.flow.submit();
    assert.equal(h.calls.filter(call => call.path.endsWith("/submit")).length, 1, code);
    await h.flow.preflight();
    await h.flow.submit();
    const attempts = h.calls.filter(call => call.path.endsWith("/submit"));
    assert.notEqual(attempts[0].data.idempotencyKey, attempts[1].data.idempotencyKey, code);
    h.dispose();
  }
});

test("uncertain installation acceptance keeps the original plan and submission key", async () => {
  for (const error of [new Error("connection lost"), Object.assign(new Error("server failure"), { code: "INSTALL_PLAN_STALE", status: 500 }), Object.assign(new Error("conflicting request"), { code: "IDEMPOTENCY_CONFLICT", status: 409 })]) {
    const h = installationHarness([], { sourceMode: "ONLINE", runtimeMode: "NATIVE", request: async (path, method, data) => {
      if (path === "/credentials") return [];
      if (path.endsWith("/software-install-plans")) return { id: "original-plan", expiresAt: "2099-01-01T00:00:00Z", source: data.source };
      throw error;
    } });
    await flush();
    await h.flow.preflight();
    await h.flow.submit();
    assert.equal(h.flow.submissionAttempted.value, true);
    assert.equal(h.flow.plan.value.id, "original-plan");
    await h.flow.submit();
    const attempts = h.calls.filter(call => call.path.endsWith("/submit"));
    assert.equal(attempts.length, 2);
    assert.deepEqual(attempts[0], attempts[1]);
    h.dispose();
  }
});

test("late installation actions cannot clear a new context's busy state or populate its error", async () => {
  const h = installationHarness([]);
  await flush();
  let failOld, finishNew;
  const old = h.flow.act(() => new Promise((resolve, reject) => { failOld = reject; }));
  h.props.host.id = "host-b";
  await flush();
  const fresh = h.flow.act(() => new Promise(resolve => { finishNew = resolve; }));
  failOld(new Error("old host failed"));
  await old;
  assert.equal(h.flow.busy.value, true);
  assert.equal(h.flow.actionError.value, undefined);
  finishNew();
  await fresh;
  assert.equal(h.flow.busy.value, false);
  h.dispose();
});

test("late credential saves do not select a credential in another installation context", async () => {
  let save;
  const h = installationHarness([], { request: async (path, method) =>
    path === "/credentials" && method === "POST" ? new Promise(resolve => { save = resolve; }) : [] });
  await flush();
  h.flow.editCredential("primary");
  h.flow.credentialSecret.value = "fixture-secret";
  const pending = h.flow.saveCredential();
  h.props.host.id = "host-b";
  await flush();
  save({ id: "old-host-credential", revision: "1" });
  await pending;
  assert.deepEqual({ ...h.flow.credentialChoices.value }, { primary: "", access: "", user: "" });
  assert.equal(h.flow.credentialSecret.value, "");
  assert.equal(h.flow.credentialRole.value, undefined);
  assert.equal(h.flow.busy.value, false);
  h.dispose();
});

test("Java 8 stays online through its release package when only Java 21 has a system recipe", async () => {
  const java8 = { ...recipe, version: "8", revision: "java8" };
  const h = installDialogHarness({ recipe: java8, recipes: [java8, recipe],
    officialRecipes: [{ ...recipe, version: "21-ubuntu24.04-repository", revision: "system21", sourceModes: ["OFFICIAL"] }],
  });
  await flush();
  const { form, calls } = h;
  assert.equal(form.sourceChoice.value, "ONLINE");
  assert.equal(form.sourceMode.value, "ONLINE");
  assert.equal(form.recipe.value.version, "8");
  assert.equal(form.selectedVersion.value, installation.recipeChoiceId(java8));
  assert.equal(calls.some(path => path.startsWith("/installation-materials")), false);
  form.sourceChoice.value = "LIBRARY";
  await flush();
  assert.equal(form.sourceMode.value, "ARCHIVE");
  assert.equal(form.recipe.value.version, "8");
  assert.equal(form.method.value, "OFFLINE");
  h.dispose();
});

test("switching installation plans updates automatic names while preserving an operator's name", async () => {
  const official = { ...recipe, name: "OpenJDK 21 官方仓库", version: "21-ubuntu24.04-repository", revision: "system21", sourceModes: ["OFFICIAL"] };
  const h = installDialogHarness({ recipe, recipes: [recipe], officialRecipes: [official] });
  await flush();
  assert.equal(h.form.configuration.value.name, official.name);
  h.form.sourceChoice.value = "LIBRARY";
  await flush();
  assert.equal(h.form.configuration.value.name, "Java 21");
  h.form.configuration.value.name = "支付项目 JDK";
  h.form.sourceChoice.value = "ONLINE";
  await flush();
  h.form.selectedVersion.value = installation.recipeChoiceId(official);
  await flush();
  assert.equal(h.form.configuration.value.name, "支付项目 JDK");
  h.dispose();
});

test("GitLab external URL follows the target and mapped port until the user supplies a domain", async () => {
  const gitlab = { ...recipe, key: "custom-gitlab", catalogKey: "gitlab", kind: "SERVICE", port: 8929, runtimeModes: ["NATIVE"], sourceModes: ["ONLINE"],
    web: { scheme: "http", portKey: "main", urlParameter: "EXTERNAL_URL" }, parameters: [{ key: "EXTERNAL_URL", type: "TEXT", default: "http://gitlab.example.com/" }], commands: { kind: "SYSTEMD", steps: [] } };
  const h = installDialogHarness({ recipe: gitlab, recipes: [gitlab] });
  await flush();
  assert.equal(h.form.configuration.value.bindAddress, "0.0.0.0");
  assert.equal(h.form.configuration.value.parameters.EXTERNAL_URL, "http://10.1.2.3:8929/");
  h.form.configuration.value.hostPort = 18929;
  await flush();
  assert.equal(h.form.configuration.value.parameters.EXTERNAL_URL, "http://10.1.2.3:18929/");
  h.props.host.address = "10.9.8.7";
  await flush();
  assert.equal(h.form.configuration.value.parameters.EXTERNAL_URL, "http://10.9.8.7:18929/");
  h.form.configuration.value.bindAddress = "127.0.0.1";
  await flush();
  assert.equal(h.form.configuration.value.parameters.EXTERNAL_URL, "");
  h.form.configuration.value.bindAddress = "0.0.0.0";
  await flush();
  assert.equal(h.form.configuration.value.parameters.EXTERNAL_URL, "http://10.9.8.7:18929/");
  h.form.configuration.value.parameters.EXTERNAL_URL = "https://git.company.example/";
  h.form.configuration.value.hostPort = 28929;
  await flush();
  assert.equal(h.form.configuration.value.parameters.EXTERNAL_URL, "https://git.company.example/");
  assert.ok(h.requests.every(item => !item.method || item.method === "GET"));
  h.dispose();
});

test("Web wildcard listening reaches installation preflight while non-Web wildcard remains blocked", async () => {
  for (const web of [{ scheme: "http", path: "/" }, undefined]) {
    const custom = { ...recipe, key: "custom-server", kind: "SERVICE", port: 8080, runtimeModes: ["NATIVE"], sourceModes: ["ONLINE"], web, commands: { kind: "SYSTEMD", steps: [] } };
    const h = installationHarness([], { recipe: custom, runtimeMode: "NATIVE", sourceMode: "ONLINE", request: async (path, method, data) =>
      path.endsWith("/software-install-plans") ? { id: "web-plan", expiresAt: "2099-01-01T00:00:00Z", definition: custom, source: data.source, config: data.config, checks: [] } : [] });
    await flush();
    h.flow.configuration.value.bindAddress = "0.0.0.0";
    await h.flow.next();
    assert.equal(h.calls.some(item => item.path.endsWith("/software-install-plans")), !!web);
    assert.equal(!!h.flow.fieldError.value, !web);
    h.dispose();
  }
});

test("builtin command preview follows the selected runtime and source without uninstall steps", async () => {
  const definition = { ...recipe, commands: { builtin: true, steps: [
    { name: "download", phase: "INSTALL", runtimeMode: "NATIVE", sourceModes: ["ONLINE"] },
    { name: "extract", phase: "INSTALL", runtimeMode: "NATIVE", sourceModes: ["ARCHIVE"] },
    { name: "pull", phase: "INSTALL", runtimeMode: "DOCKER", sourceModes: ["REGISTRY"] },
    { name: "verify", phase: "VERIFY" },
    { name: "uninstall", phase: "UNINSTALL" },
  ] } };
  const h = installDialogHarness({ recipe: definition, recipes: [definition] });
  await flush();
  const names = () => h.form.installCommands.value.map(step => step.name);
  assert.deepEqual(names(), ["download", "verify"]);
  h.form.sourceChoice.value = "LIBRARY";
  await flush();
  assert.deepEqual(names(), ["extract", "verify"]);
  h.form.mode.value = "DOCKER";
  h.form.sourceChoice.value = "ONLINE";
  await flush();
  assert.deepEqual(names(), ["pull", "verify"]);
  h.dispose();
});

test("an archive-only custom recipe offers only its supported source and declared target distribution", async () => {
  const custom = { ...recipe, key: "custom-centos", revision: "custom1", runtimeModes: ["NATIVE"], sourceModes: ["ARCHIVE"], commands: {
    kind: "TOOL", locator: "demo", distributions: ["centos"], steps: [],
  } };
  const h = installDialogHarness({ recipe: custom, recipes: [custom], host: {
    id: "host-centos", version: "1", address: "10.1.2.3", capabilities: { distribution: "centos", platform: "linux/amd64" },
  } });
  await flush();
  assert.equal(h.form.nativeAvailable.value, true);
  assert.equal(h.form.onlineAvailable.value, false);
  assert.equal(h.form.sourceChoice.value, "LIBRARY");
  assert.equal(h.form.sourceMode.value, "ARCHIVE");
  assert.equal(h.form.selectedVersion.value, installation.recipeChoiceId(custom));
  h.dispose();
});

test("grouped recipes keep exact identity across equal revisions, sources and Java versions", async () => {
  const variant = (key, version, source) => ({ ...recipe, key, catalogKey: "java", version, revision: "1", runtimeModes: ["NATIVE"], sourceModes: [source], commands: {
    kind: "TOOL", locator: "${OPS_INSTALL_ROOT}/bin/java", steps: [],
  } });
  const online = variant("custom-java21-online", "21", "ONLINE");
  const offline = variant("custom-java21-offline", "21", "ARCHIVE");
  const java8 = variant("custom-java8-offline", "8", "ARCHIVE");
  const h = installDialogHarness({ recipe: online, recipes: [recipe, online, offline, java8] }, (path, method, data) => {
    if (path.startsWith("/installation-materials?")) return [variant("custom-other-java21", "21", "ARCHIVE"), offline, java8].map(item => ({ ...material(item.key + "-file", "NATIVE"), recipeKey: item.key, recipeVersion: item.version, recipeRevision: "1" }));
    if (path.endsWith("/software-install-plans")) return { id: "plan", expiresAt: "2099-01-01T00:00:00Z", checks: [], source: data.source };
    return [];
  });
  await flush();
  h.form.selectedVersion.value = installation.recipeChoiceId(online);
  await flush(); await h.form.next();
  assert.equal(h.requests.at(-1).data.recipeKey, online.key);
  assert.deepEqual(h.requests.at(-1).data.source, { type: "ONLINE" });
  h.form.sourceChoice.value = "LIBRARY";
  await flush();
  h.form.selectedVersion.value = installation.recipeChoiceId(offline);
  await flush(); await h.form.next();
  assert.equal(h.requests.at(-1).data.recipeKey, offline.key);
  assert.deepEqual(h.requests.at(-1).data.source, { type: "ARCHIVE", materialId: offline.key + "-file" });
  h.form.javaVersion.value = "8";
  await flush(); await h.form.next();
  assert.equal(h.form.recipe.value.key, java8.key);
  assert.equal(h.requests.at(-1).data.recipeVersion, "8");
  assert.deepEqual(h.requests.at(-1).data.source, { type: "ARCHIVE", materialId: java8.key + "-file" });
  h.dispose();
});

test("native eligibility belongs to each grouped candidate rather than the initial card recipe", async () => {
  const custom = { ...recipe, key: "custom-java-centos", catalogKey: "java", runtimeModes: ["NATIVE"], sourceModes: ["ONLINE"], commands: { kind: "TOOL", distributions: ["centos"], steps: [] } };
  const h = installDialogHarness({ recipe, recipes: [recipe, custom], host: { id: "host-a", version: "1", address: "10.1.2.3", capabilities: { distribution: "centos", platform: "linux/amd64" } } });
  await flush();
  assert.equal(h.form.nativeAvailable.value, true);
  assert.equal(h.form.recipe.value.key, custom.key);
  h.props.host.capabilities.distribution = "ubuntu";
  h.props.host.capabilities.distributionVersion = "24.04";
  await flush();
  assert.equal(h.form.nativeAvailable.value, true);
  assert.equal(h.form.recipe.value.key, "java");
  assert.equal(h.form.choices.value.some(item => item.key === custom.key), false);
  h.dispose();
});

const savedInstallation = (overrides = {}) => ({
  id: "saved-software", hostId: "host-a", installationJobId: "original-install", lastJobId: "later-operation",
  installation: { definition: recipe, config: installation.initialServiceConfiguration(recipe, "10.1.2.3", "NATIVE") },
  ...overrides,
});
test("reopening a saved installation follows its original job without preflight, upload or submit", async () => {
  const timers = executionTimer();
  const h = installationHarness([], { timers, existing: savedInstallation(), request: async (path) => {
    if (path === "/jobs/original-install") return { id: "original-install", status: "RUNNING", phase: "TRANSFER" };
    if (path === "/jobs/original-install/logs") return { text: "original output" };
    throw new Error(`Unexpected request ${path}`);
  } });
  await flush();
  assert.equal(h.flow.step.value, 4);
  assert.equal(h.flow.logs.value, "original output");
  h.props.open = false; await flush();
  const closedCount = h.calls.length; await timers.tick(); assert.equal(h.calls.length, closedCount);
  h.props.open = true; await flush();
  await h.flow.preflight(); await h.flow.submit();
  assert.equal(h.flow.receipt.value.jobId, "original-install");
  assert.ok(h.calls.length > closedCount);
  assert.ok(h.calls.every(({ path, method }) => path.startsWith("/jobs/original-install") && (!method || method === "GET")));
  h.dispose();
});
test("saved UNKNOWN work keeps its original receipt and mismatched hosts cannot read it", async () => {
  const h = installationHarness([], { existing: savedInstallation(), request: async (path) =>
    path.endsWith("/logs") ? { text: "waiting for acknowledgement" } : { id: "original-install", status: "UNKNOWN", targetAckPending: true } });
  await flush();
  assert.equal(h.flow.job.value.status, "UNKNOWN");
  assert.equal(h.flow.receipt.value.jobId, "original-install");
  h.props.host.id = "host-b"; await flush();
  assert.equal(h.flow.receipt.value, undefined);
  assert.match(h.flow.fieldError.value, /不匹配/);
  assert.ok(h.calls.every(({ method }) => !method || method === "GET"));
  h.dispose();
});

test("confirmed failed installation retries with current published commands and preserves settings", async () => {
  const old = { ...recipe, key: "custom-ytlabel", kind: "SERVICE", port: 9090, runtimeModes: ["NATIVE"], sourceModes: ["ONLINE"], commands: { kind: "SYSTEMD", steps: [] } };
  const latest = { ...old, revision: "r2" };
  const config = { ...installation.initialServiceConfiguration(old, "10.1.2.3", "NATIVE"), name: "标注服务", hostPort: 9191, parameters: { PYTHON_BINARY: "/usr/bin/python3.10" } };
  const h = installDialogHarness({ recipe: old, recipes: [latest], existing: savedInstallation({ installation: { definition: old, config, source: { type: "ONLINE" } } }) }, (path, method, data) => {
    if (path === "/jobs/original-install") return { id: "original-install", status: "FAILED", phase: "SOFTWARE_REJECTED", targetAckPending: false };
    if (path.endsWith("/software-install-plans")) return { id: "new-plan", expiresAt: "2099-01-01T00:00:00Z", source: data.source };
    return [];
  });
  await flush();
  assert.equal(h.form.canRetryInstallation.value, true);
  await h.form.retryFailedInstallation();
  await flush();
  assert.equal(h.form.receipt.value, undefined);
  assert.equal(h.form.recipe.value.revision, "r2");
  assert.equal(h.form.configuration.value.name, "标注服务");
  assert.equal(h.form.configuration.value.hostPort, 9191);
  assert.equal(h.form.configuration.value.parameters.PYTHON_BINARY, "/usr/bin/python3.10");
  assert.ok(h.requests.every(({ method }) => !method || method === "GET"));
  await h.form.next();
  assert.equal(h.requests.at(-1).data.recipeRevision, "r2");
  assert.equal(h.requests.at(-1).path, "/hosts/host-a/software-install-plans");
  h.dispose();
});

test("ytlabel Python 3.12 stays selected across sources without altering old uncertain installations", async () => {
  const old = { ...recipe, key: "custom-ytlabel-native-online", catalogKey: "ytlabel", kind: "SERVICE", version: "4.0", runtimeModes: ["NATIVE"], sourceModes: ["ONLINE"],
    parameters: [{ key: "PYTHON_BINARY", type: "PATH", label: "Python 解释器", default: "/usr/bin/python3.10" }], commands: { kind: "SYSTEMD", steps: [] } };
  const online = { ...old, key: "custom-ytlabel-py312-native-online", version: "4.0-python312", parameters: [{ ...old.parameters[0], default: "/usr/bin/python3" }] };
  const offline = { ...online, key: "custom-ytlabel-py312-native-offline", sourceModes: ["ARCHIVE"] };
  const recipes = [old, { ...old, key: "custom-ytlabel-docker", runtimeModes: ["DOCKER"], sourceModes: ["REGISTRY"] }, offline, online];
  const h = installDialogHarness({ recipe: offline, recipes, host: { id: "host-a", version: "1", address: "10.1.2.3", capabilities: { docker: { status: "AVAILABLE" } } } });
  await flush();
  assert.equal(h.form.mode.value, "NATIVE");
  assert.equal(h.form.recipe.value.key, online.key);
  assert.equal(h.form.configuration.value.parameters.PYTHON_BINARY, "/usr/bin/python3");
  h.form.configuration.value.parameters.PYTHON_BINARY = "/opt/python/bin/python3";
  h.form.sourceChoice.value = "LIBRARY";
  await flush();
  assert.equal(h.form.recipe.value.key, offline.key);
  assert.equal(h.form.configuration.value.parameters.PYTHON_BINARY, "/opt/python/bin/python3");
  h.dispose();
  const existing = savedInstallation({ installation: { definition: old, config: { ...installation.initialServiceConfiguration(old, "", "NATIVE"), parameters: { PYTHON_BINARY: "/usr/bin/python3.10" } } } });
  const saved = installDialogHarness({ recipe: old, recipes, existing }, () => ({ id: "original-install", status: "UNKNOWN", targetAckPending: true }));
  await flush();
  assert.equal(saved.form.recipe.value.key, old.key);
  assert.equal(saved.form.configuration.value.parameters.PYTHON_BINARY, "/usr/bin/python3.10");
  assert.equal(saved.form.canRetryInstallation.value, false);
  saved.dispose();
});

test("explicitly switching a failed ytlabel draft replaces the old default Python but keeps an edited path", async () => {
  const old = { ...recipe, key: "custom-ytlabel-native-online", catalogKey: "ytlabel", kind: "SERVICE", version: "4.0", runtimeModes: ["NATIVE"], sourceModes: ["ONLINE"],
    parameters: [{ key: "PYTHON_BINARY", type: "PATH", label: "Python", default: "/usr/bin/python3.10" }], commands: { kind: "SYSTEMD", steps: [] } };
  const compatible = { ...old, key: "custom-ytlabel-py312-native-online", version: "4.0-python312", parameters: [{ ...old.parameters[0], default: "/usr/bin/python3" }] };
  for (const [savedPath, expected] of [["/usr/bin/python3.10", "/usr/bin/python3"], ["/opt/python/bin/python3", "/opt/python/bin/python3"]]) {
    const config = { ...installation.initialServiceConfiguration(old, "", "NATIVE"), parameters: { PYTHON_BINARY: savedPath } };
    const h = installDialogHarness({ recipe: old, recipes: [old, compatible], existing: savedInstallation({ installation: { definition: old, config } }) },
      () => ({ id: "original-install", status: "FAILED", targetAckPending: false }));
    await flush();
    await h.form.retryFailedInstallation();
    await flush();
    assert.equal(h.form.recipe.value.key, old.key);
    assert.equal(h.form.configuration.value.parameters.PYTHON_BINARY, savedPath);
    h.form.selectedVersion.value = installation.recipeChoiceId(compatible);
    await flush();
    assert.equal(h.form.configuration.value.parameters.PYTHON_BINARY, expected);
    assert.ok(h.requests.every(item => !item.method || item.method === "GET"));
    h.dispose();
  }
});

test("retry cannot discard UNKNOWN, active work, successful work or unacknowledged failures", async () => {
  for (const [status, ack] of [["UNKNOWN", false], ["RUNNING", false], ["SUCCEEDED", false], ["FAILED", true]]) {
    const h = installationHarness([], { existing: savedInstallation(), request: async path => path.endsWith("/logs") ? { text: "" } : { id: "original-install", status, targetAckPending: ack } });
    await flush();
    assert.equal(h.flow.canRetryInstallation.value, false);
    assert.equal(h.flow.retryInstallation(), false);
    assert.equal(h.flow.receipt.value.jobId, "original-install");
    assert.ok(h.calls.every(({ method }) => !method || method === "GET"));
    h.dispose();
  }
});

test("saved database retry restores saved credential choices without creating new secrets", async () => {
  const database = { ...recipe, key: "mysql", kind: "SERVICE", port: 3306, runtimeModes: ["NATIVE"], sourceModes: ["ONLINE"] };
  const config = { ...installation.initialServiceConfiguration(database, "10.1.2.3", "NATIVE"), username: "app", credentialId: "root-password", credentialRevision: "2", userCredentialId: "app-password", userCredentialRevision: "3" };
  const h = installDialogHarness({ recipe: database, existing: savedInstallation({ installation: { definition: database, config, source: { type: "ONLINE" } } }) }, path => {
    if (path === "/jobs/original-install") return { id: "original-install", status: "FAILED", targetAckPending: false };
    if (path === "/credentials") return [{ id: "root-password", revision: "2", kind: "PASSWORD" }, { id: "app-password", revision: "3", kind: "PASSWORD" }];
    return [];
  });
  await flush();
  await h.form.retryFailedInstallation();
  await flush();
  assert.deepEqual({ ...h.form.credentialChoices.value }, { primary: "root-password:2", access: "", user: "app-password:3" });
  assert.equal(h.form.configuration.value.credentialId, "root-password");
  assert.equal(h.form.configuration.value.userCredentialId, "app-password");
  assert.ok(h.requests.every(({ method }) => !method || method === "GET"));
  h.dispose();
});
test("only persisted service installation records expose the replay-free progress entry", () => {
  assert.equal(software.softwareHasInstallationRecord(savedInstallation()), true);
  for (const item of [{ lastJobId: "arbitrary-job" }, savedInstallation({ installationJobId: "" }), savedInstallation({ installation: {} })])
    assert.equal(software.softwareHasInstallationRecord(item), false);
});

test("installation output follows running work and reads the final tail without submitting anything", async () => {
  const timers = executionTimer();
  let status = "QUEUED", text = "downloading package\n";
  const h = installationHarness([], { timers, request: async (path) => {
    if (path === "/jobs/job-a") return { id: "job-a", status, phase: status === "SUCCEEDED" ? "SERVICE_INSTALLED" : "TRANSFER" };
    if (path === "/jobs/job-a/logs") return { text, limited: true };
    return [];
  } });
  await flush(); h.flow.receipt.value = { jobId: "job-a" };
  await timers.tick();
  assert.equal(h.calls.filter((c) => c.path.endsWith('/logs')).length, 0);
  status = "RUNNING"; await timers.tick();
  assert.equal(h.flow.logs.value, text);
  assert.equal(h.flow.logsLimited.value, true);
  h.flow.logsPaused.value = true;
  const pausedCount = h.calls.length; await timers.tick(); assert.equal(h.calls.length, pausedCount);
  await h.flow.loadLogs(); assert.equal(h.flow.logsPaused.value, false);
  timers.document.hidden = true;
  const hiddenCount = h.calls.length; await timers.tick(); assert.equal(h.calls.length, hiddenCount);
  timers.document.hidden = false; status = "SUCCEEDED"; text = "installation complete\n";
  await timers.tick(); assert.equal(h.flow.logs.value, text);
  const finalCount = h.calls.length; await timers.tick(); assert.equal(h.calls.length, finalCount);
  assert.equal(h.calls.some((c) => c.method === 'POST'), false);
  h.dispose();
});

test("installation dialog can follow job status without reading centralized output", async () => {
  const timers = executionTimer();
  let status = "RUNNING";
  const h = installationHarness([], { timers, request: async (path) =>
    path === "/jobs/job-a" ? { id: "job-a", status, phase: "TRANSFER" } : [] });
  h.props.includeLogs = false;
  await flush();
  h.flow.receipt.value = { jobId: "job-a" };
  await timers.tick();
  assert.equal(h.flow.job.value.status, "RUNNING");
  assert.equal(h.calls.some(call => call.path.endsWith("/logs")), false);
  status = "SUCCEEDED";
  await timers.tick();
  const completedCount = h.calls.length;
  await timers.tick();
  assert.equal(h.calls.length, completedCount);
  assert.equal(h.flow.job.value.status, "SUCCEEDED");
  h.dispose();
});

test("three output failures pause polling, retain known output, and explicit retry resumes", async () => {
  const timers = executionTimer(); let failing = false;
  const h = installationHarness([], { timers, request: async (path) => {
    if (path === "/jobs/job-a") return { id: "job-a", status: "RUNNING", phase: "IMPORT" };
    if (path.endsWith('/logs')) { if (failing) throw new Error('connection unavailable'); return { text: 'verified previous output' }; }
    return [];
  } });
  await flush(); h.flow.receipt.value = { jobId: 'job-a' }; await timers.tick();
  failing = true; await timers.tick(); await timers.tick(); await timers.tick();
  assert.equal(h.flow.logsPaused.value, true);
  assert.equal(h.flow.logs.value, 'verified previous output');
  assert.equal(h.flow.job.value.status, 'RUNNING');
  assert.equal(h.flow.actionError.value, undefined);
  const count = h.calls.length; await timers.tick(); assert.equal(h.calls.length, count);
  failing = false; await h.flow.loadLogs(); assert.equal(h.flow.logsPaused.value, false);
  assert.equal(h.flow.logError.value, undefined); h.dispose();
});

test("late installation output cannot populate a closed or changed host drawer", async () => {
  let finish;
  const h = installationHarness([], { request: async (path) => {
    if (path === '/jobs/job-a') return { id: 'job-a', status: 'RUNNING' };
    if (path.endsWith('/logs')) return new Promise((resolve) => { finish = resolve; });
    return [];
  } });
  await flush(); h.flow.receipt.value = { jobId: 'job-a' };
  const pending = h.flow.loadLogs(); await flush();
  h.props.open = false; await flush(); finish({ text: 'late output' }); await pending;
  assert.equal(h.flow.logs.value, '');
  h.props.host.id = 'host-b'; h.props.open = true; await flush();
  assert.equal(h.flow.logs.value, ''); assert.equal(h.flow.receipt.value, undefined); h.dispose();
});

test("switching native to Docker selects a matching offline material and invalidates the old plan", async () => {
  const h = installationHarness([material("native", "NATIVE", "BUNDLED"), material("docker", "DOCKER")]);
  await flush();
  await h.flow.preflight();
  h.flow.configuration.value.runtimeMode = "DOCKER";
  await flush();
  assert.equal(h.flow.sourceType.value, "ARCHIVE");
  assert.equal(h.flow.materialId.value, "docker");
  assert.equal(h.flow.plan.value, undefined);
  h.dispose();
});

test("an empty offline library blocks next step instead of silently downloading", async () => {
  const h = installationHarness([]);
  await flush();
  await h.flow.next();
  assert.equal(h.flow.step.value, 0);
  assert.match(h.flow.fieldError.value, /离线/);
  assert.equal(h.calls.length, 2);
  h.dispose();
});

test("catalog keeps selected versions and classifies custom software without a built-in description", async () => {
  const custom = { ...recipe, key: "custom-demo", name: "自定义工具", commands: { kind: "TOOL", steps: [] } };
  const services = [recipe, { ...recipe, version: "8", revision: "java8" }, custom,
    { ...custom, key: "custom-java21-online", catalogKey: "java", name: "Java", sourceModes: ["ONLINE"] },
    { ...custom, key: "custom-java21-offline", catalogKey: "java", name: "Java", sourceModes: ["ARCHIVE"] },
  ];
  const materials = [
    { ...material("uploaded", "NATIVE"), recipeVersion: "21" },
    { ...material("wrong-revision", "NATIVE", "BUNDLED"), recipeVersion: "8", recipeRevision: "obsolete" },
    { ...material("unavailable", "NATIVE", "BUNDLED", false), recipeVersion: "8", recipeRevision: "java8" },
    { ...material("ready", "NATIVE", "BUNDLED"), recipeVersion: "8", recipeRevision: "java8" },
  ];
  const officialRecipes = [{ ...recipe, version: "21-ubuntu24.04-repository", sourceModes: ["OFFICIAL"] }];
  const request = async (path) => path === "/software-recipes" ? services : path === "/software-official-recipes" ? officialRecipes : path === "/installation-materials" ? materials : [];
  const module = { exports: {} };
  const scope = vue.effectScope();
  scope.run(() => new Function("require", "module", "exports", "defineProps", "defineEmits", "setTimeout", "clearTimeout", compile(
    catalogScript + "\nexport { refresh, selectedVersions, officialDefinitions, recipeSubtitle, category, catalogEntries };",
  ))((name) => name === "vue" ? { ...vue, onMounted: () => {}, onUnmounted: () => {} } :
    name === "./api" ? { request } : name === "./serviceInstallation" ? installation : name === "./softwareCatalog" ? softwareCatalog : {}, module, module.exports,
    () => ({ hostId: "" }), () => () => {}, () => 1, () => {}));
  await module.exports.refresh();
  assert.equal(module.exports.selectedVersions.value.java, "8");
  assert.deepEqual(module.exports.officialDefinitions.value, officialRecipes);
  module.exports.selectedVersions.value.java = "21";
  await module.exports.refresh();
  assert.equal(module.exports.selectedVersions.value.java, "21");
  const java = module.exports.catalogEntries.value.find(item => item.key === "java");
  assert.equal(java.versions.length, 4);
  assert.deepEqual(java.versionChoices, ["21", "8"]);
  assert.equal(module.exports.recipeSubtitle(custom), "程序工具");
  module.exports.category.value = "CUSTOM";
  assert.deepEqual(module.exports.catalogEntries.value.map(item => item.key), ["custom-demo", "java"]);
  scope.stop();
});

function uninstallHarness(request) {
  const props = vue.reactive({ open: true, item: {
    id: "software-a", name: "MySQL 业务库", version: "7", managementMode: "MANAGED", installationStatus: "READY", installation: {},
    installationPaths: { root: "/opt/fixture/mysql", data: "/opt/fixture/mysql/data", config: "/opt/fixture/mysql/config", logs: "/opt/fixture/mysql/logs" },
  }, dependentContainers: [] });
  const events = [];
  const module = { exports: {} };
  const source = compile(uninstallScript + "\nexport { deleteData, confirmationName, canSubmit, canDeleteData, uninstallSteps, attempted, key, error, directoryRows, pathsReady, submit };");
  const scope = vue.effectScope();
  scope.run(() => new Function("require", "module", "exports", "defineProps", "defineEmits", source)(
    (name) => name === "vue" ? vue : name === "./api" ? { ApiError: Error, request } : name === "./software" ? software : name === "./serviceInstallation" ? installation : {},
    module, module.exports, () => props, () => (name, payload) => events.push({ name, payload }),
  ));
  return { panel: module.exports, props, events, dispose: () => scope.stop() };
}

test("real uninstall defaults to keeping data and submits the existing action API", async () => {
  const calls = [];
  const h = uninstallHarness(async (...args) => { calls.push(args); return { jobId: "job-a", softwareInstanceId: "software-a" }; });
  assert.equal(h.panel.deleteData.value, false);
  await h.panel.submit();
  assert.equal(calls[0][0], "/software-instances/software-a/actions");
  assert.equal(calls[0][2].action, "UNINSTALL");
  assert.equal(calls[0][2].deleteData, false);
  assert.equal(calls[0][2].confirmed, true);
  assert.equal("confirmationName" in calls[0][2], false);
  assert.equal(h.events[0].name, "submitted");
  h.dispose();
});

test("data removal needs the exact instance name and a lost reply reuses the original request", async () => {
  const calls = [];
  const h = uninstallHarness(async (...args) => { calls.push(args); throw new TypeError("connection lost"); });
  h.panel.deleteData.value = true;
  h.panel.confirmationName.value = "wrong";
  await h.panel.submit();
  assert.equal(calls.length, 0);
  h.panel.confirmationName.value = "MySQL 业务库";
  await h.panel.submit();
  h.props.item.version = "8";
  await h.panel.submit();
  assert.deepEqual(calls[1], calls[0]);
  assert.equal(calls[0][2].confirmationName, "MySQL 业务库");
  assert.equal(calls[0][2].expectedVersion, "7");
  assert.equal(h.events.length, 0);
  h.dispose();
});

test("uninstall uses server paths and missing or incomplete paths block data deletion", async () => {
  const calls = [];
  const h = uninstallHarness(async (...args) => { calls.push(args); return { softwareInstanceId: "software-a" }; });
  assert.deepEqual(h.panel.directoryRows.value.map((row) => row.path), ["/opt/fixture/mysql", "/opt/fixture/mysql/data", "/opt/fixture/mysql/config", "/opt/fixture/mysql/logs"]);
  assert.equal(h.panel.pathsReady.value, true);
  h.panel.deleteData.value = true;
  h.panel.confirmationName.value = "MySQL 业务库";
  h.props.item.installationPaths = undefined;
  assert.equal(h.panel.canSubmit.value, false);
  await h.panel.submit();
  h.props.item.installationPaths = { root: "/opt/fixture/mysql", data: "/opt/fixture/mysql/data", config: "/opt/fixture/mysql/config", logs: "" };
  assert.equal(h.panel.canSubmit.value, false);
  await h.panel.submit();
  assert.equal(calls.length, 0);
  h.panel.deleteData.value = false;
  assert.equal(h.panel.canSubmit.value, true);
  await h.panel.submit();
  assert.equal(calls.length, 1);
  assert.equal(calls[0][2].deleteData, false);
  h.dispose();
});

test("external software cannot be uninstalled and removed instances retain a distinct history state", () => {
  assert.equal(software.softwareUninstallAvailable({ managementMode: "MANAGED" }), false);
  assert.equal(software.softwareUninstallAvailable({ managementMode: "RELEASED", installation: {} }), false);
  assert.deepEqual(software.softwareState({ managementMode: "RELEASED", installationStatus: "UNINSTALLED" }), { label: "已卸载", tone: "state-muted" });
  assert.match(installation.materialChoiceLabel(material("a", "NATIVE", "BUNDLED")), /Eclipse Temurin JDK 21.*随包离线材料.*已就绪/);
  assert.match(software.softwareJobErrorMessages.DOCKER_CONTAINER_DEPENDENCIES_UNCONFIRMED, /先明确启动 Docker.*启动可能恢复原有容器/);
});

test("uninstall previews only the installed runtime and source and only enables its data deletion", () => {
  const h = uninstallHarness(async () => ({}));
  h.props.item.installation = { config: { runtimeMode: "NATIVE" }, source: { type: "ARCHIVE" }, definition: {
    ...recipe, commands: { builtin: true, steps: [
      { name: "native remove", phase: "UNINSTALL", runtimeMode: "NATIVE" },
      { name: "docker remove", phase: "UNINSTALL", runtimeMode: "DOCKER" },
      { name: "online cleanup", phase: "DELETE_DATA", runtimeMode: "NATIVE", sourceModes: ["ONLINE"] },
      { name: "archive cleanup", phase: "DELETE_DATA", runtimeMode: "NATIVE", sourceModes: ["ARCHIVE"] },
    ] },
  } };
  assert.deepEqual(h.panel.uninstallSteps.value.map(step => step.name), ["native remove"]);
  assert.equal(h.panel.canDeleteData.value, true);
  h.panel.deleteData.value = true;
  assert.deepEqual(h.panel.uninstallSteps.value.map(step => step.name), ["native remove", "archive cleanup"]);
  h.props.item.installation.config.runtimeMode = "DOCKER";
  assert.deepEqual(h.panel.uninstallSteps.value.map(step => step.name), ["docker remove"]);
  assert.equal(h.panel.canDeleteData.value, false);
  h.dispose();
});

test("migrated software deletion shows only current data and excludes the old data removal command", () => {
  const h = uninstallHarness(async () => ({}));
  h.props.item.installation = { config: { runtimeMode: "NATIVE" }, source: { type: "ONLINE" }, definition: {
    ...recipe, commands: { steps: [{ name: "remove program", phase: "UNINSTALL" }, { name: "remove original data", phase: "DELETE_DATA" }] },
  } };
  h.props.item.installationPaths.data = "/mnt/apps/current-data";
  h.panel.deleteData.value = true;
  assert.deepEqual(h.panel.directoryRows.value, [{ label: "当前数据目录", path: "/mnt/apps/current-data" }]);
  assert.deepEqual(h.panel.uninstallSteps.value.map(step => step.name), ["remove program"]);
  assert.equal(h.panel.canSubmit.value, false);
  h.panel.confirmationName.value = h.props.item.name;
  assert.equal(h.panel.canSubmit.value, true);
  h.dispose();
});

test("builtin service configuration stays editable and its saved command snapshot uses the installed source", async () => {
  const source = (await readFile(new URL("../src/ServiceConfigurationPanel.vue", import.meta.url), "utf8")).split('<script setup lang="ts">')[1].split("</script>")[0];
  const props = vue.reactive({ disabled: false, detail: { id: "mysql-a", version: "1", managementMode: "MANAGED", installationStatus: "READY", installation: {
    config: { runtimeMode: "NATIVE", ports: {} }, source: { type: "ONLINE" }, definition: { ...recipe, key: "mysql", kind: "SERVICE", commands: { builtin: true, steps: [
      { name: "download", phase: "INSTALL", runtimeMode: "NATIVE", sourceModes: ["ONLINE"] },
      { name: "unpack", phase: "INSTALL", runtimeMode: "NATIVE", sourceModes: ["ARCHIVE"] },
      { name: "docker", phase: "INSTALL", runtimeMode: "DOCKER" },
      { name: "verify", phase: "VERIFY" },
    ] } },
  } } });
  const module = { exports: {} }, scope = vue.effectScope();
  scope.run(() => new Function("require", "module", "exports", "defineProps", "defineEmits", "setInterval", "clearInterval",
    compile(source + "\nexport { canEdit, commandSteps };"))(
    name => name === "vue" ? { ...vue, onUnmounted() {} } : name === "./serviceInstallation" ? installation : {},
    module, module.exports, () => props, () => () => {}, () => 1, () => {},
  ));
  assert.equal(module.exports.canEdit.value, true);
  assert.deepEqual(module.exports.commandSteps.value.map(step => step.name), ["download", "verify"]);
  props.detail.installation.definition.commands.builtin = false;
  assert.equal(module.exports.canEdit.value, false);
  props.detail.installation.definition.commands.builtin = true;
  props.disabled = true;
  assert.equal(module.exports.canEdit.value, false);
  scope.stop();
});

test("custom installations retain their name and uninstall without allowing an undeclared data-delete step", async () => {
  const calls = [];
  const h = uninstallHarness(async (...args) => { calls.push(args); return { softwareInstanceId: "software-a" }; });
  h.props.item.softwareType = "CUSTOM-DEMO";
  h.props.item.installation = { definition: { ...recipe, key: "custom-demo", name: "示例工具", commands: { kind: "TOOL", steps: [{ phase: "UNINSTALL", script: "remove-owned-tool" }] } } };
  assert.equal(software.softwareTypeLabel(h.props.item), "示例工具");
  assert.equal(software.softwareUninstallAvailable(h.props.item), true);
  assert.equal(h.panel.canDeleteData.value, false);
  h.panel.deleteData.value = true;
  h.panel.confirmationName.value = h.props.item.name;
  await h.panel.submit();
  assert.equal(calls.length, 0);
  h.panel.deleteData.value = false;
  await h.panel.submit();
  assert.equal(calls.at(-1)[2].deleteData, false);
  h.props.item.installation.definition.commands.steps.push({ phase: "DELETE_DATA", script: "remove-owned-data" });
  assert.equal(h.panel.canDeleteData.value, true);
  h.dispose();
});

const officialRecipe = (key = "nginx", port = 80, kind = "SERVICE") => ({
  ...recipe, key, name: key, kind, version: "ubuntu24.04-repository", revision: `${key}-official-v1`,
  runtimeModes: ["NATIVE"], sourceModes: ["OFFICIAL"], port,
});
const officialInstallation = (ownedPaths = ["/etc/nginx", "/var/www/html", "/var/log/nginx"]) => ({
  manager: "APT", os: "ubuntu24.04", architecture: "amd64",
  packages: [{ name: "nginx", version: "1.24.0-2ubuntu7.5", origin: "https://archive.ubuntu.com/ubuntu noble-updates/main amd64 Packages", direct: true }],
  commands: [{ executable: "/usr/bin/apt-get", args: ["install", "--yes", "nginx=1.24.0-2ubuntu7.5"] }],
  paths: { root: "/", binary: "/usr/sbin/nginx", config: "/etc/nginx", data: "/var/www/html", logs: "/var/log/nginx", unit: "nginx.service", ownedPaths },
});

test("official installation preflights directly without credentials or library requests and sends only the fixed configuration", async () => {
  const h = installationHarness([], { method: "OFFICIAL", recipe: officialRecipe() });
  await flush();
  assert.equal(h.calls.length, 0);
  assert.equal(h.flow.sourceType.value, "OFFICIAL");
  assert.deepEqual(h.flow.credentialRoles.value, []);
  h.flow.configuration.value.name = "  Nginx 官网  ";
  await h.flow.next();
  assert.equal(h.flow.step.value, 3);
  assert.equal(h.calls.length, 1);
  assert.deepEqual(h.calls[0].data, {
    expectedHostVersion: "1", recipeKey: "nginx", recipeVersion: "ubuntu24.04-repository", recipeRevision: "nginx-official-v1",
    config: { name: "Nginx 官网", runtimeMode: "NATIVE", bindAddress: "0.0.0.0", hostPort: 80, memoryMiB: 512 },
    source: { type: "OFFICIAL" },
  });
  h.dispose();
});

test("official service and tool defaults match system packages without offline port or storage settings", () => {
  assert.equal(installation.initialOfficialConfiguration(officialRecipe("redis", 6379)).bindAddress, "127.0.0.1");
  assert.equal(installation.initialOfficialConfiguration(officialRecipe("mysql", 3306)).hostPort, 3306);
  for (const candidate of [officialRecipe("java", 0, "TOOL"), officialRecipe("docker", 0)]) {
    const config = installation.initialOfficialConfiguration(candidate);
    assert.equal(config.bindAddress, "");
    assert.equal(config.hostPort, 0);
    assert.equal(config.storageRoot, "");
    assert.equal(config.memoryMiB, 512);
  }
});

test("switching installation methods isolates official, offline and self-installed requests", async () => {
  const h = installationHarness([material("native", "NATIVE", "BUNDLED")], { method: "OFFICIAL" });
  await flush();
  assert.equal(h.calls.length, 0);
  h.props.method = "OFFLINE";
  await flush();
  assert.equal(h.flow.sourceType.value, "ARCHIVE");
  assert.equal(h.flow.materialId.value, "native");
  assert.equal(h.calls.length, 2);
  h.props.method = "SELF";
  await flush();
  await h.flow.preflight();
  assert.equal(h.calls.length, 2);
  h.props.method = "OFFICIAL";
  await flush();
  assert.equal(h.flow.materialId.value, "");
  assert.equal(h.flow.sourceType.value, "OFFICIAL");
  assert.equal(h.calls.length, 2);
  h.dispose();
});

test("updating the executor keeps the draft and rechecks with the refreshed host version without submitting installation", async () => {
  let upgraded = false;
  const h = installationHarness([], {
    method: "OFFICIAL", recipe: officialRecipe(),
    request: async (path, method, data) => {
      if (path === "/hosts") return [{ id: "host-a", version: "2", capabilities: { mutation: { status: "AVAILABLE" } } }];
      if (path.endsWith("/software-install-plans")) {
        if (!upgraded) throw Object.assign(new Error("接入组件需要更新"), { code: "EXECUTOR_UPGRADE_REQUIRED" });
        return { id: "official-plan", expiresAt: "2099-01-01T00:00:00Z", config: data.config, official: officialInstallation() };
      }
      throw new Error(`Unexpected request ${path}`);
    },
  });
  await flush();
  h.flow.configuration.value.name = "保留这个名称";
  await h.flow.next();
  assert.equal(h.flow.actionError.value.code, "EXECUTOR_UPGRADE_REQUIRED");
  h.flow.preparationOpen.value = true;
  upgraded = true;
  await h.flow.preparationChecked();
  assert.equal(h.flow.configuration.value.name, "保留这个名称");
  assert.equal(h.flow.preparationOpen.value, false);
  assert.equal(h.flow.plan.value.id, "official-plan");
  assert.equal(h.calls.at(-1).data.expectedHostVersion, "2");
  assert.equal(h.calls.at(-1).data.config.name, "保留这个名称");
  assert.equal(h.calls.some((call) => call.path.includes("/submit")), false);
  h.props.host.version = "3";
  await flush();
  assert.equal(h.flow.configuration.value.name, "保留这个名称");
  h.dispose();
});

test("unavailable readiness keeps the update dialog open and does not preflight again", async () => {
  const h = installationHarness([], { method: "OFFICIAL", recipe: officialRecipe(), request: async () => [
    { id: "host-a", capabilities: { mutation: { status: "UNAVAILABLE" } } },
  ] });
  await flush();
  h.flow.preparationOpen.value = true;
  await h.flow.preparationChecked();
  assert.equal(h.flow.preparationOpen.value, true);
  assert.equal(h.calls.length, 1);
  assert.equal(h.flow.plan.value, undefined);
  h.dispose();
});

test("official uninstall deletes only receipt-owned paths and never falls back to system root", async () => {
  const calls = [];
  const h = uninstallHarness(async (...args) => { calls.push(args); return { softwareInstanceId: "software-a" }; });
  h.props.item.installation.official = officialInstallation();
  h.props.item.installationPaths = { root: "/", config: "/etc", data: "/var/lib", logs: "/var/log" };
  assert.deepEqual(h.panel.directoryRows.value.map((row) => row.path), ["/etc/nginx", "/var/www/html", "/var/log/nginx"]);
  assert.equal(h.panel.pathsReady.value, true);
  h.panel.deleteData.value = true;
  h.panel.confirmationName.value = "MySQL 业务库";
  h.props.item.installation.official.paths.ownedPaths = ["/"];
  assert.equal(h.panel.canSubmit.value, false);
  await h.panel.submit();
  h.props.item.installation.official.paths.ownedPaths = [];
  assert.equal(h.panel.pathsReady.value, false);
  assert.deepEqual(h.panel.directoryRows.value, []);
  await h.panel.submit();
  assert.equal(calls.length, 0);
  h.panel.deleteData.value = false;
  await h.panel.submit();
  assert.equal(calls.length, 1);
  assert.equal(calls[0][2].deleteData, false);
  h.dispose();
});

test("missing official receipt paths cannot reuse offline installation paths for data deletion", () => {
  const h = uninstallHarness(async () => ({}));
  h.props.item.installation.definition = officialRecipe();
  assert.equal(h.panel.pathsReady.value, false);
  assert.deepEqual(h.panel.directoryRows.value, []);
  h.panel.deleteData.value = true;
  h.panel.confirmationName.value = "MySQL 业务库";
  assert.equal(h.panel.canSubmit.value, false);
  h.dispose();
});

for (const code of ["EXECUTOR_UPGRADE_REQUIRED", "SERVICE_INSTALL_INPUT_INVALID", "SERVICE_RECIPE_UNTRUSTED", "NATIVE_INSTALL_UPGRADE_REQUIRED"]) test(`SSH preflight ${code} updates only a known component then preserves the draft and never submits software`, async () => {
  let current = false;
  const h = installationHarness([], { method: "OFFICIAL", recipe: officialRecipe(), hostKind: "SSH", request: async (path, method, data) => {
    if (path.includes("?checkUpdate=1")) return {hostId:"host-a",expectedHostVersion:"1",update: current ? {status:"CURRENT"} : {status:"AVAILABLE",currentSHA256:"a".repeat(64),packageSHA256:"b".repeat(64)}};
    if (path === "/hosts") return [{id:"host-a",version:"1",capabilities:{mutation:{status:"AVAILABLE"}}}];
    if (path.endsWith("/software-install-plans")) {
      if (!current) throw Object.assign(new Error("能力不足"), {code});
      return {id:"after-update-plan",expiresAt:"2099-01-01T00:00:00Z",config:data.config};
    }
    throw new Error(`Unexpected request ${path}`);
  }});
  await flush();h.flow.configuration.value.name="保留配置";
  await h.flow.next();
  assert.equal(h.flow.preparationOpen.value,true);
  assert.equal(h.flow.automaticUpdate.value.hostId,"host-a");
  assert.equal(h.calls.filter(x=>x.path.endsWith("/software-install-plans")).length,1);
  current=true;await h.flow.preparationChecked();
  assert.equal(h.flow.preparationOpen.value,false);
  assert.equal(h.flow.configuration.value.name,"保留配置");
  assert.equal(h.flow.plan.value.id,"after-update-plan");
  assert.equal(h.calls.some(x=>x.path.includes("/submit")),false);
  h.dispose();
});

test("a current component never gets reinstalled for invalid installation parameters", async () => {
  const h = installationHarness([], {method:"OFFICIAL",recipe:officialRecipe(),hostKind:"SSH",request:async(path)=>{
    if (path.endsWith("/software-install-plans")) throw Object.assign(new Error("参数不兼容"),{code:"SERVICE_INSTALL_INPUT_INVALID"});
    return {hostId:"host-a",expectedHostVersion:"1",update:{status:"CURRENT"}};
  }});
  await flush(); await h.flow.next();
  assert.equal(h.flow.actionError.value.code,"SERVICE_INSTALL_INPUT_INVALID");
  assert.equal(h.flow.preparationOpen.value,false);
  assert.equal(h.flow.automaticUpdate.value,undefined);
  assert.equal(h.calls.length,2);
  h.dispose();
});

test("installation checks upgrade then authorize once in the same dialog without submitting software", async () => {
  let phase = 0;
  const h = installationHarness([], { method: "OFFICIAL", recipe: officialRecipe(), hostKind: "SSH", request: async path => {
    if (path === "/hosts") return [{ id: "host-a", version: "1", capabilities: { mutation: { status: "AVAILABLE" } } }];
    if (path.includes("checkUpdate=1")) return { hostId: "host-a", expectedHostVersion: "1", update: {
      status: phase === 0 ? "AVAILABLE" : "CURRENT", currentSHA256: (phase === 0 ? "a" : "b").repeat(64), packageSHA256: "b".repeat(64),
    } };
    if (path.endsWith("/software-install-plans")) {
      if (phase < 2) throw Object.assign(new Error("安装环境未就绪"), { code: phase === 0 ? "EXECUTOR_UPGRADE_REQUIRED" : "COMMAND_TRUST_REQUIRED" });
      return { id: "ready", expiresAt: "2099-01-01T00:00:00Z" };
    }
    throw new Error(`Unexpected request ${path}`);
  } });
  await flush(); await h.flow.next();
  assert.equal(h.flow.automaticUpdate.value.mode, "UPDATE");
  phase = 1; await h.flow.preparationChecked();
  assert.equal(h.flow.automaticUpdate.value.mode, "AUTHORIZE");
  assert.equal(h.flow.preparationOpen.value, true);
  phase = 2; await h.flow.preparationChecked();
  assert.equal(h.flow.preparationOpen.value, false);
  assert.equal(h.flow.plan.value.id, "ready");
  assert.equal(h.calls.some(call => call.path.includes("/submit")), false);
  h.dispose();
});

test("missing component or sudo opens inline preparation only after a matching safe check", async () => {
  for (const code of ["EXECUTOR_MISSING", "SUDO_AUTHORIZATION_REQUIRED"]) {
    const h = installationHarness([], { method: "OFFICIAL", recipe: officialRecipe(), hostKind: "SSH", request: async path => {
      if (path.endsWith("/software-install-plans")) throw Object.assign(new Error("需要安装权限"), { code });
      return { hostId: "host-a", expectedHostVersion: "1", update: { status: "MANUAL_REQUIRED", code, packageSHA256: "b".repeat(64) } };
    } });
    await flush(); await h.flow.next();
    assert.equal(h.flow.preparationOpen.value, true);
    assert.equal(h.flow.automaticUpdate.value.mode, "INSTALL");
    assert.equal(h.calls.some(call => call.path.includes("/submit")), false);
    h.dispose();
  }
});

test("trust authorization rejects blocked or foreign checks and never repeats a completed attempt", async () => {
  for (const mode of ["blocked", "foreign", "same"]) {
    const h = installationHarness([], { method: "OFFICIAL", recipe: officialRecipe(), hostKind: "SSH", request: async path => {
      if (path === "/hosts") return [{ id: "host-a", version: "1", capabilities: { mutation: { status: "AVAILABLE" } } }];
      if (path.endsWith("/software-install-plans")) throw Object.assign(new Error("需要安装权限"), { code: "COMMAND_TRUST_REQUIRED" });
      return { hostId: mode === "foreign" ? "host-b" : "host-a", expectedHostVersion: "1", update: {
        status: mode === "blocked" ? "BLOCKED" : "CURRENT", code: "HOST_BUSY", currentSHA256: "b".repeat(64), packageSHA256: "b".repeat(64),
      } };
    } });
    await flush(); await h.flow.next();
    assert.equal(h.flow.preparationOpen.value, mode === "same");
    if (mode === "same") {
      await h.flow.preparationChecked();
      assert.equal(h.flow.preparationOpen.value, false);
      assert.equal(h.calls.filter(call => call.path.includes("checkUpdate=1")).length, 1);
      assert.equal(h.flow.actionError.value.code, "COMMAND_TRUST_REQUIRED");
    }
    h.dispose();
  }
});

test("compatible SSH targets preflight directly without checking build hashes", async () => {
  const h = installationHarness([], {method:"OFFICIAL",recipe:officialRecipe(),hostKind:"SSH",request:async(path)=>{
    assert.ok(path.endsWith("/software-install-plans"));
    return {id:"compatible-plan",expiresAt:"2099-01-01T00:00:00Z"};
  }});
  await flush();await h.flow.next();
  assert.equal(h.flow.plan.value.id,"compatible-plan");
  assert.equal(h.calls.length,1);
  assert.equal(h.flow.preparationOpen.value,false);
  h.dispose();
});

test("a successful historical preparation code cannot replace the real installation rejection", async () => {
  const h = installationHarness([], { method: "OFFICIAL", recipe: officialRecipe(), hostKind: "SSH", request: async path => {
    if (path.endsWith("/software-install-plans")) throw Object.assign(new Error("当前执行器需要更新"), { code: "EXECUTOR_UPGRADE_REQUIRED" });
    return { hostId: "host-a", expectedHostVersion: "1", update: { status: "MANUAL_REQUIRED", code: "PREPARATION_READY" } };
  } });
  await flush(); await h.flow.next();
  assert.equal(h.flow.actionError.value.code, "EXECUTOR_UPGRADE_REQUIRED");
  assert.equal(h.flow.actionError.value.message, "当前执行器需要更新");
  assert.equal(h.flow.preparationOpen.value, false);
  h.dispose();
});

test("identity, transport and business failures never trigger component update", async () => {
  for (const code of ["EXECUTOR_IDENTITY_MISMATCH","SSH_TIMEOUT","OFFICIAL_PATH_NOT_PROTECTED","OFFICIAL_PACKAGE_STATE_UNAVAILABLE"]) {
    const h=installationHarness([], {method:"OFFICIAL",recipe:officialRecipe(),hostKind:"SSH",request:async()=>{
      throw Object.assign(new Error(code),{code});
    }});
    await flush();await h.flow.next();
    assert.equal(h.flow.actionError.value.code,code);
    assert.equal(h.calls.length,1);
    assert.equal(h.flow.preparationOpen.value,false);
    h.dispose();
  }
});

test("unknown components, blocked jobs and stale hosts cannot trigger automatic updates", async () => {
  for (const state of ["MANUAL_REQUIRED","BLOCKED","stale-host"]) {
    const h=installationHarness([], {method:"OFFICIAL",recipe:officialRecipe(),hostKind:"SSH",request:async(path)=>{
      if (path.endsWith("/software-install-plans")) throw Object.assign(new Error("能力不足"),{code:"EXECUTOR_UPGRADE_REQUIRED"});
      return {hostId:"host-a",expectedHostVersion:state==="stale-host"?"2":"1",update:{status:state==="stale-host"?"CURRENT":state,code:"HOST_BUSY"}};
    }});
    await flush();await h.flow.next();
    assert.equal(h.flow.preparationOpen.value,false,state);
    assert.equal(h.flow.automaticUpdate.value,undefined,state);
    assert.equal(h.flow.plan.value,undefined,state);
    assert.equal(h.calls.length,2,state);
    h.dispose();
  }
});

function unavailableInstallation(path) {
  if (path.endsWith("/software-install-plans")) throw Object.assign(new Error("能力不足"),{code:"EXECUTOR_UPGRADE_REQUIRED"});
  if (path === "/hosts") return [{id:"host-a",version:"1",capabilities:{mutation:{status:"AVAILABLE"}}}];
  return {hostId:"host-a",expectedHostVersion:"1",update:{status:"AVAILABLE",currentSHA256:"a".repeat(64),packageSHA256:"b".repeat(64)}};
}

test("an update completion cannot start another automatic update in the same continuation", async () => {
  const h=installationHarness([], {method:"OFFICIAL",recipe:officialRecipe(),hostKind:"SSH",request:async(path)=>unavailableInstallation(path)});
  await flush();await h.flow.next();assert.equal(h.flow.preparationOpen.value,true);
  await h.flow.preparationChecked();
  assert.equal(h.flow.preparationOpen.value,false);
  assert.equal(h.flow.plan.value,undefined);
  assert.equal(h.flow.actionError.value.code,"EXECUTOR_UPGRADE_REQUIRED");
  assert.equal(h.calls.filter(x=>x.path.includes("checkUpdate=1")).length,1);
  assert.equal(h.calls.filter(x=>x.path.endsWith("/software-install-plans")).length,2);
  h.dispose();
});

test("closing the install drawer clears automatic update intent before any reopen", async () => {
 const h=installationHarness([], {method:"OFFICIAL",recipe:officialRecipe(),hostKind:"SSH",request:async(path)=>unavailableInstallation(path)});
 await flush();await h.flow.next();assert.equal(h.flow.preparationOpen.value,true);
 h.props.open=false;await flush();
 assert.equal(h.flow.preparationOpen.value,false);assert.equal(h.flow.automaticUpdate.value,undefined);
 h.props.open=true;await flush();
 assert.equal(h.flow.preparationOpen.value,false);assert.equal(h.calls.length,2);
 h.dispose();
});

test("job summaries use Chinese labels without repeating protocol codes", () => {
  assert.equal(
    software.softwareJobDiagnostic("SUCCEEDED", "NODE_ENVIRONMENT_UPDATED"),
    "已完成 · Node 环境已更新",
  );
  assert.equal(
    software.softwareJobDiagnostic("SUCCEEDED", "DOCKER_ACCESS_UPDATED"),
    "已完成 · Docker 账号权限已更新",
  );
  assert.equal(
    software.softwareJobDiagnostic("FUTURE_STATE", "FUTURE_PHASE"),
    "状态未识别 · 阶段未识别",
  );
  assert.equal(
    software.softwareJobDiagnostic("SUCCEEDED", "READY", { READY: "材料已就绪" }),
    "已完成 · 材料已就绪",
  );
  assert.equal(software.softwareJobDiagnostic(), "状态未提供 · 阶段未提供");
});
