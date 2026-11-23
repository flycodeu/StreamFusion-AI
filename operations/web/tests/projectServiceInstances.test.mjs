import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";

const read = name => readFile(new URL(`../src/${name}`, import.meta.url), "utf8");
function load(source, imports = {}, globals = {}) {
  const exports = {};
  const code = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  new Function("require", "exports", ...Object.keys(globals), code)(name => imports[name], exports, ...Object.values(globals));
  return exports;
}
const model = load(await read("applicationProjects.ts"));
const component = (await read("ProjectServiceInstances.vue")).split('<script setup lang="ts">')[1].split("</script>")[0];

async function workspace(t, { archived = false } = {}) {
  const project = {
    id: "project", name: "Demo", archived,
    modules: [{ id: "api", name: "platform-api", directory: "backend" }, { id: "web", name: "platform-web", directory: "web" }],
    environments: ["test", "stage"].map(id => ({
      id, name: id, deployments: [{ id: id + "-api", moduleId: "api", hostId: id + "-host", runtimeSoftwareId: id === "test" ? "ci" : "app" }],
    })),
  };
  const software = [
    { id: "ci", name: "Jenkins", softwareType: "JENKINS" },
    { id: "app", name: "API", softwareType: "OTHER" },
  ];
  const scope = vue.effectScope(), events = [], cleanup = [], mounts = [];
  const flow = scope.run(() => load(component + `
    export const flow = { visibleRows, openRelease, openBuild, panel, moduleSearch, environmentFilter };
  `, {
    vue: { ...vue, onMounted: cb => mounts.push(cb), onUnmounted: cb => cleanup.push(cb) },
    "./listPages": { requestAllPages: async path => path === "/software-instances" ? software : [project] },
    "./applicationProjects": model,
    "./software": { softwareState: item => ({ label: item.name + "服务已启动", tone: "state-success" }) },
  }, {
    defineProps: () => ({ hosts: [] }), defineEmits: () => (...event) => events.push(event),
  }).flow);
  t.after(() => { scope.stop(); cleanup.forEach(cb => cb()); });
  await Promise.all(mounts.map(cb => cb()));
  await new Promise(resolve => setImmediate(resolve));
  return { flow, events };
}

test("one row per module and environment, build infrastructure is never an application status", async t => {
  const { flow } = await workspace(t);
  const [test, stage, unbound] = flow.visibleRows.value;
  assert.equal(test.registration.id, "test-api");
  assert.equal(test.buildService, true);
  assert.equal(test.state, undefined);
  assert.equal(stage.state.label, "API服务已启动");
  assert.equal(unbound.module.name, "platform-web");
  assert.equal(unbound.registration, undefined);
  flow.moduleSearch.value = "BACKEND";
  flow.environmentFilter.value = "stage";
  assert.deepEqual(flow.visibleRows.value.map(row => row.registration.id), ["stage-api"]);
});

test("QA and build logs stay with the selected binding, publication retains its host and environment", async t => {
  const { flow, events } = await workspace(t);
  const [test, stage] = flow.visibleRows.value;
  flow.openBuild(test, "qa");
  assert.equal(flow.panel.value.serviceInstanceId, "test-api");
  assert.equal(flow.panel.value.action, "qa");
  flow.openBuild(stage, "logs");
  assert.equal(flow.panel.value.serviceInstanceId, "stage-api");
  assert.equal(flow.panel.value.action, "logs");
  assert.deepEqual(events, []);
  flow.openRelease(stage);
  assert.deepEqual(events, [["release", {
    projectId: "project", moduleId: "api", moduleName: "platform-api", serviceInstanceId: "stage-api",
    hostId: "stage-host", environmentId: "stage", environmentName: "stage",
  }]]);
});

test("unbound and archived modules cannot initiate a QA or a release", async t => {
  const { flow, events } = await workspace(t, { archived: true });
  const [bound, , unbound] = flow.visibleRows.value;
  for (const row of [bound, unbound]) {
    flow.openBuild(row, "qa");
    flow.openRelease(row);
  }
  flow.openBuild(unbound, "logs");
  assert.equal(flow.panel.value, undefined);
  assert.deepEqual(events, []);
  flow.openBuild(bound, "logs");
  assert.equal(flow.panel.value.serviceInstanceId, "test-api");
});
