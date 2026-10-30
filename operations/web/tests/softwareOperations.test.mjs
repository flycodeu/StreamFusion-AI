import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";

const compile = (source) => ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
}).outputText;
function moduleFrom(source, imports = {}) {
  const module = { exports: {} };
  new Function("require", "module", "exports", "setTimeout", "clearTimeout", "document", compile(source))(
    (name) => imports[name], module, module.exports, () => 1, () => {}, { hidden: true },
  );
  return module.exports;
}
const installationSource = await readFile(new URL("../src/serviceInstallation.ts", import.meta.url), "utf8");
const installation = moduleFrom(installationSource);
const software = moduleFrom(await readFile(new URL("../src/software.ts", import.meta.url), "utf8"));
const useSource = await readFile(new URL("../src/useServiceInstallation.ts", import.meta.url), "utf8");
const uninstallComponent = await readFile(new URL("../src/SoftwareUninstallDialog.vue", import.meta.url), "utf8");
const uninstallScript = uninstallComponent.split('<script setup lang="ts">')[1].split("</script>")[0];
const catalogComponent = await readFile(new URL("../src/SoftwareRecipes.vue", import.meta.url), "utf8");
const catalogScript = catalogComponent.split('<script setup lang="ts">')[1].split("</script>")[0];
const recipe = {
  key: "java", name: "Java", kind: "TOOL", version: "21", revision: "r1", platform: "linux/amd64",
  runtimeModes: ["DOCKER", "NATIVE"], sourceModes: ["ARCHIVE", "ONLINE", "REGISTRY"], port: 0,
};
const material = (id, mode, origin = "UPLOADED", available = true) => ({
  id, name: "jdk.tar.gz", recipeName: "Eclipse Temurin JDK", recipeKey: "java", recipeVersion: "21",
  recipeRevision: "r1", runtimeMode: mode, origin: { kind: origin }, status: "AVAILABLE", available, byteSize: 1024,
});
async function flush() { for (let i = 0; i < 10; i++) { await Promise.resolve(); await vue.nextTick(); } }
function installationHarness(materials) {
  const calls = [], scopes = [];
  const props = vue.reactive({ open: true, recipe, host: { id: "host-a", version: "1", address: "10.1.2.3" } });
  const request = async (path, method, data) => {
    calls.push({ path, method, data });
    if (path === "/credentials") return [];
    if (path.startsWith("/installation-materials?")) return materials;
    if (path.endsWith("/software-install-plans")) return { id: "plan", expiresAt: "2099-01-01T00:00:00Z", source: data.source };
    throw new Error(`Unexpected request ${path}`);
  };
  const composable = moduleFrom(useSource, {
    vue: { ...vue, onUnmounted: (fn) => scopes.push(fn) },
    "./api": { ApiError: Error, request }, "./serviceInstallation": installation,
  });
  const scope = vue.effectScope();
  const flow = scope.run(() => composable.useServiceInstallation(props, () => {}, () => {}));
  return { flow, calls, dispose() { scopes.forEach((fn) => fn()); scope.stop(); } };
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

test("catalog initially prefers a matching ready bundled version and preserves a user's selection on refresh", async () => {
  const services = [recipe, { ...recipe, version: "8", revision: "java8" }];
  const materials = [
    { ...material("uploaded", "NATIVE"), recipeVersion: "21" },
    { ...material("wrong-revision", "NATIVE", "BUNDLED"), recipeVersion: "8", recipeRevision: "obsolete" },
    { ...material("unavailable", "NATIVE", "BUNDLED", false), recipeVersion: "8", recipeRevision: "java8" },
    { ...material("ready", "NATIVE", "BUNDLED"), recipeVersion: "8", recipeRevision: "java8" },
  ];
  const request = async (path) => path === "/software-recipes" ? services : path === "/installation-materials" ? materials : [];
  const module = { exports: {} };
  const scope = vue.effectScope();
  scope.run(() => new Function("require", "module", "exports", "defineProps", "defineEmits", "setTimeout", "clearTimeout", compile(
    catalogScript + "\nexport { refresh, selectedVersions };",
  ))((name) => name === "vue" ? { ...vue, onMounted: () => {}, onUnmounted: () => {} } :
    name === "./api" ? { request } : name === "./serviceInstallation" ? installation : {}, module, module.exports,
    () => ({ hostId: "" }), () => () => {}, () => 1, () => {}));
  await module.exports.refresh();
  assert.equal(module.exports.selectedVersions.value.java, "8");
  module.exports.selectedVersions.value.java = "21";
  await module.exports.refresh();
  assert.equal(module.exports.selectedVersions.value.java, "21");
  scope.stop();
});

function uninstallHarness(request) {
  const props = vue.reactive({ open: true, item: {
    id: "software-a", name: "MySQL 业务库", version: "7", managementMode: "MANAGED", installationStatus: "READY", installation: {},
    installationPaths: { root: "/opt/fixture/mysql", data: "/opt/fixture/mysql/data", config: "/opt/fixture/mysql/config", logs: "/opt/fixture/mysql/logs" },
  }, dependentContainers: [] });
  const events = [];
  const module = { exports: {} };
  const source = compile(uninstallScript + "\nexport { deleteData, confirmationName, canSubmit, attempted, key, error, directoryRows, pathsReady, submit };");
  const scope = vue.effectScope();
  scope.run(() => new Function("require", "module", "exports", "defineProps", "defineEmits", source)(
    (name) => name === "vue" ? vue : name === "./api" ? { ApiError: Error, request } : name === "./software" ? software : {},
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
