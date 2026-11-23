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

async function workspace(t, { archived = false, configured = true } = {}) {
  const project = {
    id: "project", name: "Demo", archived,
    modules: [{ id: "api", name: "platform-api", directory: "backend" }, { id: "web", name: "platform-web", directory: "web" }],
    environments: ["test", "stage"].map(id => ({
      id, name: id, deployments: [{ id: id + "-api", moduleId: "api", hostId: id + "-host", managedInstanceId: id === "stage" ? "managed" : "", runtimeSoftwareId: id === "test" ? "ci" : "app" }],
    })),
  };
  const software = [
    { id: "ci", name: "Jenkins", softwareType: "JENKINS" },
    { id: "app", name: "API", softwareType: "OTHER" },
  ];
  const scope = vue.effectScope(), events = [], calls = [], cleanup = [], mounts = [];
  const flow = scope.run(() => load(component + `
    export const flow = { visibleRows, openRelease, openBuild, panel, moduleSearch, environmentFilter, setupNotice, maintain };
  `, {
    vue: { ...vue, onMounted: cb => mounts.push(cb), onUnmounted: cb => cleanup.push(cb) },
    "./listPages": { requestAllPages: async path => path === "/software-instances" ? software : [project] },
    "./api": { request: async (path, method) => {
      calls.push([path, method]);
      return path.endsWith("jenkins-build") ? { number: 9, status: "SUCCESS" } : { configured, reason: "请设置项目流水线" };
    } },
    "./projectJenkins": { moduleJenkinsPath: (project, module, id) => "/application-projects/" + project + (id ? "/service-instances/" + id : "/modules/" + module) },
    "./applicationProjects": model,
    "./software": { softwareState: item => ({ label: item.name + "服务已启动", tone: "state-success" }) },
  }, {
    defineProps: () => ({ hosts: [] }), defineEmits: () => (...event) => events.push(event),
  }).flow);
  t.after(() => { scope.stop(); cleanup.forEach(cb => cb()); });
  await Promise.all(mounts.map(cb => cb()));
  await new Promise(resolve => setImmediate(resolve));
  return { flow, events, calls };
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
  await flow.openBuild(test, "qa");
  assert.equal(flow.panel.value.context.serviceInstanceId, "test-api");
  assert.equal(flow.panel.value.context.action, "qa");
  await flow.openBuild(stage, "logs");
  assert.equal(flow.panel.value.context.serviceInstanceId, "stage-api");
  assert.equal(flow.panel.value.context.action, "logs");
  assert.deepEqual(events, []);
  flow.openRelease(stage);
  flow.openRelease(stage, "verify");
  const target = {
    projectId: "project", moduleId: "api", moduleName: "platform-api", serviceInstanceId: "stage-api",
    hostId: "stage-host", environmentId: "stage", environmentName: "stage", managedInstanceId: "managed",
  };
  assert.deepEqual(events, [["release", target], ["verify", target]]);
});

test("a module without a runtime server can still start QA and read its records", async t => {
  const { flow, calls, events } = await workspace(t);
  const unbound = flow.visibleRows.value.at(-1);
  await flow.openBuild(unbound, "record");
  assert.equal(flow.visibleRows.value.at(-1).record.number, 9);
  assert.equal(flow.panel.value, undefined);
  await flow.openBuild(unbound, "qa");
  assert.equal(flow.panel.value.context.action, "qa");
  assert.equal(flow.panel.value.context.serviceInstanceId, undefined);
  assert.ok(calls.every(([path, method]) => method === "GET" && path.startsWith("/application-projects/project/modules/web/")));
  flow.openRelease(unbound);
  assert.match(flow.setupNotice.value.message, /上线目标/);
  assert.deepEqual(events, []);
});

test("archived modules can only inspect QA logs", async t => {
  const { flow, calls, events } = await workspace(t, { archived: true });
  const [bound, , unbound] = flow.visibleRows.value;
  for (const row of [bound, unbound]) {
    await flow.openBuild(row, "qa");
    flow.openRelease(row);
  }
  assert.deepEqual(calls, []);
  assert.deepEqual(events, []);
  await flow.openBuild(unbound, "logs");
  assert.equal(flow.panel.value.context.moduleId, "web");
  assert.equal(flow.panel.value.context.readOnly, true);
});

test("missing execution settings link to the exact module and environment on the maintenance page", async t => {
  const { flow, events } = await workspace(t, { configured: false });
  const [row] = flow.visibleRows.value;
  await flow.openBuild(row, "qa");
  assert.equal(flow.panel.value, undefined);
  assert.equal(flow.setupNotice.value.message, "请设置项目流水线");
  flow.maintain(flow.setupNotice.value.row);
  assert.deepEqual(events, [["maintain", { projectId: "project", moduleId: "api", serviceInstanceId: "test-api" }]]);
  flow.openRelease(row);
  assert.match(flow.setupNotice.value.message, /上线目标/);
});
