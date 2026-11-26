import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";
import { createDeploymentStatusReader } from "../src/features/project-delivery/deploymentStatusReader.ts";

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
const projectDeployments = load(await read("projectDeployments.ts"));
const component = (await read("ProjectServiceInstances.vue")).split('<script setup lang="ts">')[1].split("</script>")[0];

async function workspace(t, { archived = false, configured = true, algorithms = false, source, pageSize, publishing = false, fixed = false } = {}) {
  const project = {
    id: "project", name: "Demo", version: "1", archived,
    modules: [{ id: "api", name: "platform-api", directory: "backend" }, { id: "web", name: "platform-web", directory: "web" }],
    environments: ["test", "stage"].map(id => ({
      id, name: id, deployments: [{ id: id + "-api", moduleId: "api", hostId: id + "-host", managedInstanceId: id === "stage" ? "managed" : "", runtimeSoftwareId: id === "test" ? "ci" : "app" }],
    })),
  };
  if (algorithms) {
    project.modules.push({ id: "runtime", name: "runtime", directory: "algorithm-node/runtime", catalog: { kind: "ALGORITHM" } });
    project.environments[0].deployments.push(
      { id: "runtime-east", moduleId: "runtime", hostId: "east" },
      { id: "runtime-west", moduleId: "runtime", hostId: "west" },
    );
  }
  if (publishing) {
    project.modules[0].catalog = { kind: "BACKEND" };
    for (const { deployments: [registration] } of project.environments) {
      registration.managedInstanceId = "";
      registration.deployMode = "NATIVE";
    }
  }
  const software = [
    { id: "ci", name: "Jenkins", softwareType: "JENKINS" },
    { id: "app", name: "API", softwareType: "OTHER" },
  ];
  const scope = vue.effectScope(), events = [], calls = [], cleanup = [], mounts = [];
  const props = vue.reactive({ hosts: [], source, fixedProject: fixed ? project : undefined });
  const flow = scope.run(() => load(component + `
    export const flow = { visibleRows, pagedRows, rowPage, rowPageSize, focusedRowKey, useSource, listElement,
      openRelease, openBuild, panel, moduleSearch, environmentFilter, setupNotice, maintain, addAlgorithmHost,
      deploymentPanel, closeDeployment };
  `, {
    vue: { ...vue, onMounted: cb => mounts.push(cb), onUnmounted: cb => cleanup.push(cb) },
    "./listPages": { requestAllPages: async path => path === "/software-instances" ? software : [project] },
    "./api": { request: async (path, method) => {
      calls.push([path, method]);
      if (path.includes("/deployments/"))
        return { health: "UNKNOWN", ...(publishing && { pending: { status: "RUNNING" } }) };
      return path.endsWith("jenkins-build") ? { number: 9, status: "SUCCESS" } : { configured, reason: "请设置项目流水线" };
    } },
    "./projectJenkins": { moduleJenkinsPath: (project, module, id) => "/application-projects/" + project + (id ? "/service-instances/" + id : "/modules/" + module) },
    "./applicationProjects": model,
    "./projectDeployments": projectDeployments,
    "./features/project-delivery/deploymentStatusReader": { createDeploymentStatusReader },
    "./software": { softwareState: item => ({ label: item.name + "服务已启动", tone: "state-success" }) },
  }, {
    defineProps: () => props, defineEmits: () => (...event) => events.push(event),
    document: { hidden: false },
  }).flow);
  t.after(() => { scope.stop(); cleanup.forEach(cb => cb()); });
  if (pageSize) flow.rowPageSize.value = pageSize;
  const scrolled = [];
  flow.listElement.value = { querySelector: (selector) => ({
    scrollIntoView: () => scrolled.push({ selector, page: flow.rowPage.value, key: flow.focusedRowKey.value }),
  }) };
  await Promise.all(mounts.map(cb => cb()));
  await new Promise(resolve => setImmediate(resolve));
  return { flow, events, calls, scrolled, props };
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

test("algorithm hosts are listed independently, and adding a host retains the module and environment but not the old binding", async t => {
  const { flow, events } = await workspace(t, { algorithms: true });
  const nodes = flow.visibleRows.value.filter(row => row.module.id === "runtime");
  assert.deepEqual(nodes.map(row => row.registration.hostId), ["east", "west"]);
  assert.ok(nodes.every(row => row.environment.id === "test"));
  flow.addAlgorithmHost(flow.visibleRows.value[0]);
  flow.addAlgorithmHost(nodes[1]);
  flow.maintain(nodes[1]);
  assert.deepEqual(events, [
    ["maintain", { projectId: "project", moduleId: "runtime", algorithmHostEnvironmentId: "test" }],
    ["maintain", { projectId: "project", moduleId: "runtime", serviceInstanceId: "runtime-west" }],
  ]);
});

test("the delivery link opens the page of the exact algorithm registration in its environment", async t => {
  const { flow, events, scrolled } = await workspace(t, {
    algorithms: true, pageSize: 2,
    source: { projectId: "project", moduleId: "runtime", serviceInstanceId: "runtime-west" },
  });
  assert.equal(flow.environmentFilter.value, "test");
  assert.equal(flow.rowPage.value, 2);
  assert.deepEqual(flow.pagedRows.value.map(({ registration }) => registration.id), ["runtime-west"]);
  assert.deepEqual(scrolled, [{ selector: "[data-focused=true]", page: 2, key: "registration:runtime-west" }]);
  assert.deepEqual(events, [["source-used"]]);
});

test("opening release detail speeds up its row without adding another status poller", async t => {
  t.mock.timers.enable({ apis: ["Date", "setTimeout"] });
  const { flow, calls } = await workspace(t, { publishing: true });
  const paths = () => calls.splice(0).map(([path]) => path);
  const testPath = "/application-projects/project/deployments/test-api";
  const stagePath = "/application-projects/project/deployments/stage-api";
  const tick = async ms => {
    t.mock.timers.tick(ms);
    await new Promise(resolve => setImmediate(resolve));
  };
  assert.deepEqual(paths(), [testPath, stagePath]);

  flow.openRelease(flow.pagedRows.value[0]);
  await vue.nextTick();
  assert.equal(flow.deploymentPanel.value.registration.id, "test-api");
  assert.deepEqual(paths(), []);
  await tick(2500);
  assert.deepEqual(paths(), [testPath]);
  await tick(2500);
  assert.deepEqual(paths(), [testPath, stagePath]);

  flow.closeDeployment();
  await vue.nextTick();
  await tick(2500);
  assert.deepEqual(paths(), []);
  await tick(2500);
  assert.deepEqual(paths(), [testPath, stagePath]);
});

test("refreshing a registration keeps its current detail but binding changes discard the open confirmation", async t => {
  const { flow, props } = await workspace(t, { publishing: true, fixed: true });
  const [first] = flow.pagedRows.value;
  flow.openRelease(first);
  await vue.nextTick();
  assert.equal(flow.deploymentPanel.value.registration.hostId, "test-host");

  const data = () => structuredClone(vue.toRaw(props.fixedProject));
  const renamed = data();
  renamed.environments[0].name = "Renamed";
  props.fixedProject = renamed;
  await vue.nextTick();
  assert.equal(flow.deploymentPanel.value.environment.name, "Renamed");

  const moved = data();
  moved.environments[0].deployments[0].hostId = "replacement";
  props.fixedProject = moved;
  await vue.nextTick();
  assert.equal(flow.deploymentPanel.value, undefined);

  flow.openRelease(flow.pagedRows.value[0]);
  const revised = data();
  revised.version = "2";
  props.fixedProject = revised;
  await vue.nextTick();
  assert.equal(flow.deploymentPanel.value, undefined);

  flow.openRelease(flow.pagedRows.value[0]);
  const removed = data();
  removed.environments[0].deployments = [];
  props.fixedProject = removed;
  await vue.nextTick();
  assert.equal(flow.deploymentPanel.value, undefined);
});

test("module QA is shared across environments while legacy publication retains its host and environment", async t => {
  const { flow, events, calls } = await workspace(t);
  const [test, stage] = flow.visibleRows.value;
  assert.deepEqual(calls.map(([path]) => path), [
    "/application-projects/project/deployments/test-api",
    "/application-projects/project/deployments/stage-api",
  ]);
  calls.length = 0;
  await flow.openBuild(test, "qa");
  assert.equal(flow.panel.value.context.serviceInstanceId, undefined);
  assert.equal(flow.panel.value.context.action, "qa");
  await flow.openBuild(stage, "logs");
  assert.equal(flow.panel.value.context.serviceInstanceId, undefined);
  assert.equal(flow.panel.value.context.action, "logs");
  assert.deepEqual(events, []);
  assert.deepEqual(calls.map(([path]) => path), [
    "/application-projects/project/modules/api/jenkins-monitor",
    "/application-projects/project/modules/api/jenkins-monitor",
  ]);
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
  calls.length = 0;
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
  calls.length = 0;
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
