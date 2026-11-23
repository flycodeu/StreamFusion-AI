import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";

async function componentScript(name) {
  const file = await readFile(new URL(`../src/${name}.vue`, import.meta.url), "utf8");
  return file.split('<script setup lang="ts">')[1].split("</script>")[0];
}

const connections = await componentScript("ProjectJenkinsConnection");
const environments = await componentScript("ProjectEnvironmentBinding");
const monitor = { configured: true, hasToken: true, softwareId: "builder", username: "automation", version: "7",
  instances: [{ id: "builder", name: "Jenkins", hostName: "build" }, { id: "other", name: "CI", hostName: "other" }] };
const project = { id: "app", version: "3", archived: false, modules: [{ id: "api", name: "platform-api" }], environments: [] };

function mount(source, exposes, props, request, lists = {}) {
  let startup;
  let unmount;
  const events = [];
  const scope = vue.effectScope();
  const code = ts.transpileModule(source + "\nexport const flow = {" + exposes + "};", {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
  }).outputText;
  const exports = {};
  scope.run(() => new Function("require", "exports", "defineProps", "defineEmits", code)(name => ({
    vue: { ...vue, onMounted: fn => { startup = fn; }, onUnmounted: fn => { unmount = fn; } },
    "./api": { request, ApiError: class extends Error {} }, "element-plus": { ElMessage: { success() {} } },
    "./listPages": { requestAllPages: async path => lists[path] || [] },
    "./applicationProjects": { isProjectBuildService: () => false },
  }[name]), exports, () => props, () => (...args) => events.push(args)));
  return { flow: exports.flow, startup, events, close() { unmount(); scope.stop(); } };
}

test("project Jenkins keeps existing token only for the same account and service, and never fills the secret field", async t => {
  const calls = [];
  const h = mount(connections, "edit, save, softwareId, username, token, valid, open", { projectId: "app", archived: false },
    async (path, method, data) => { calls.push([path, method, { ...data }]); return monitor; });
  t.after(h.close);
  await h.startup();
  h.flow.edit();
  assert.equal(h.flow.token.value, "");
  assert.equal(h.flow.valid.value, true);
  await h.flow.save();
  assert.deepEqual(calls.at(-1), ["/application-projects/app/jenkins-connection", "POST", {
    expectedVersion: "7", softwareId: "builder", username: "automation", token: "",
  }]);
  h.flow.edit();
  h.flow.username.value = "other-name";
  assert.equal(h.flow.valid.value, false);
  h.flow.username.value = monitor.username;
  h.flow.softwareId.value = "other";
  assert.equal(h.flow.valid.value, false);
  await h.flow.save();
  assert.equal(calls.length, 2);
  h.flow.token.value = "sample-new-token";
  await h.flow.save();
  assert.equal(calls.at(-1)[2].token, "sample-new-token");
  assert.equal(h.flow.token.value, "");
});

test("running configuration scopes its release targets and preserves a historical version without editing it", async t => {
  const calls = [];
  const row = { id: "test-api", moduleId: "api", hostId: "east", recordedVersion: "historical-build-2" };
  const environment = { name: "test", id: "test", deployments: [row] };
  const targets = [
    { id: "match", name: "Api", targetId: "east", config: { environment: "test" } },
    { id: "host mismatch", targetId: "west", config: { environment: "test" } },
    { id: "env mismatch", targetId: "east", config: { environment: "prod" } },
  ];
  const h = mount(environments, "targetOptions, managedId, save", { project, moduleId: "api", environment, registration: row },
    async (path, method, data) => {
      if (path === "/hosts") return [{ id: "east", name: "East server" }];
      calls.push([path, method, data]); return project;
    }, { "/instances": targets });
  t.after(h.close);
  await h.startup();
  assert.deepEqual(h.flow.targetOptions.value.map(item => item.id), ["match"]);
  h.flow.managedId.value = "match";
  await h.flow.save();
  assert.deepEqual(calls[0], ["/application-projects/app/service-instances", "POST", {
    id: row.id, version: "3", moduleId: "api", environment: "test", hostId: "east", recordedVersion: row.recordedVersion, managedInstanceId: "match",
  }]);
});

test("running configuration deletes an environment only after explicit confirmation", async t => {
  const calls = [];
  const row = { id: "test-api", moduleId: "api", hostId: "east" };
  const h = mount(environments, "removeEnvironment, removing", { project, moduleId: "api", registration: row },
    async (path, method) => { calls.push([path, method]); return project; });
  t.after(h.close);
  await h.flow.removeEnvironment();
  assert.equal(calls.length, 0);
  h.flow.removing.value = true;
  await h.flow.removeEnvironment();
  assert.deepEqual(calls, [["/application-projects/app/service-instances/test-api","DELETE"]]);
});
