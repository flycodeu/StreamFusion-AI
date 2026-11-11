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
const model = load(await readFile(new URL("../src/applicationProjects.ts", import.meta.url), "utf8"));
const softwareModel = load(await readFile(new URL("../src/software.ts", import.meta.url), "utf8"));
const component = (await readFile(new URL("../src/ApplicationProjects.vue", import.meta.url), "utf8")).split('<script setup lang="ts">')[1].split("</script>")[0];
const listPagesSource = await readFile(new URL("../src/listPages.ts", import.meta.url), "utf8");
class ApiError extends Error { constructor(code) { super(code); this.code = code; } }
const flush = async () => { for (let i = 0; i < 8; i++) { await Promise.resolve(); await vue.nextTick(); } };
function sampleProject() {
  const project = model.newApplicationProject(), repository = model.newProjectRepository(), module = model.newProjectModule(repository.id), environment = model.newProjectEnvironment();
  Object.assign(project, { id: "project", name: "智能化平台", version: "1" });
  repository.name = "业务源码"; module.name = "后端"; environment.name = "test";
  environment.deployments.push(model.newProjectDeployment(module.id));
  project.repositories.push(repository); project.modules.push(module); project.environments.push(environment);
  return project;
}
function harness(override = () => undefined) {
  const calls = [], cleanup = [], events = [];
  const request = async (path, method = "GET", body, signal) => {
    calls.push({ path, method, body: body && structuredClone(body), signal });
    const result = await override(path, method, body);
    if (result !== undefined) return result;
    if (path === "/hosts" || path === "/git-accounts" || path === "/credentials") return [];
    if (path.startsWith("/software-instances?")) return { items: [], total: 0 };
    if (path.startsWith("/application-projects?")) return { items: [], total: 0, page: 1, pageSize: 10 };
    if (path.startsWith("/application-projects/")) return { ...sampleProject(), id: path.split("/")[2] };
    throw Error("unexpected request " + path);
  };
  const scope = vue.effectScope();
  const flow = scope.run(() => load(component + "\nexport const flow = { draft, baseline, records, total, page, pageSize, query, archived, listError, editorError, editorOpen, loading, saving, editProject, saveProject, load, addRepository, removeRepository, addModule, removeModule, addEnvironment, addDeployment, selectedModuleId, selectedEnvironmentId, selectedDeploymentId, changeVariableMode, usesCredential, setVariableCredential, credentials, software, softwareChoices };", {
    vue: { ...vue, onMounted: () => {}, onUnmounted: fn => cleanup.push(fn) },
    "element-plus": { ElMessageBox: { confirm: async () => {} } },
    "./api": { request, ApiError }, "./listPages": load(listPagesSource, { "./api": { request } }),
    "./applicationProjects": model,
    "./software": softwareModel,
  }, { defineEmits: () => (...args) => events.push(args) }).flow);
  return { flow, calls, events, close() { cleanup.forEach(fn => fn()); scope.stop(); } };
}

test("new project configuration has stable module references and no active retention policy", () => {
  const project = sampleProject(), target = project.environments[0].deployments[0];
  assert.equal(project.modules[0].repositoryId, project.repositories[0].id);
  assert.equal(target.moduleId, project.modules[0].id);
  assert.notEqual(project.repositories[0].id, project.modules[0].id);
  assert.equal(target.logs.retentionDays, 0);
  assert.equal(target.logs.maxMiB, 0);
  assert.equal(target.runtimeSoftwareId, "");
  assert.equal("softwareId" in project.modules[0].runtime, false);
  assert.equal(project.modules[0].build.outputPath, "");
});

test("detached draft and usage checks preserve list data and environment references", () => {
  const project = sampleProject(), copy = model.copyApplicationProject(project);
  copy.modules[0].name = "正在编辑";
  assert.equal(project.modules[0].name, "后端");
  assert.deepEqual(model.projectRepositoryUsage(project, project.repositories[0].id), ["后端"]);
  assert.deepEqual(model.projectModuleUsage(project, project.modules[0].id), ["test"]);
  assert.equal(model.projectNameError(project), "");
  copy.modules.push({ ...copy.modules[0], id: "other", name: " 正在编辑 " });
  assert.match(model.projectNameError(copy), /模块名称重复/);
});

test("project search and archive filters are encoded for server pagination", () => {
  const path = model.projectListPath(2, 50, " A&B /项目 ", "all"), url = new URL(path, "http://ops.test");
  assert.equal(url.searchParams.get("q"), "A&B /项目");
  assert.equal(url.searchParams.get("page"), "2");
  assert.equal(url.searchParams.get("pageSize"), "50");
  assert.equal(url.searchParams.get("archived"), "all");
});

test("project options honor array hosts, accounts and credentials while paging only software", async () => {
  const credential = { id: "token", name: "部署凭据", kind: "TOKEN", revokedAt: "" };
  const h = harness(path => path === "/credentials" ? [
    { ...credential, revision: "2" }, { ...credential, revision: "10" }, { ...credential, revision: "9" },
    { ...credential, revision: "11", revokedAt: "2026-11-11T00:00:00Z" },
    { ...credential, id: "private-key", kind: "SSH_KEY", revision: "1" },
    { ...credential, id: "password", kind: "PASSWORD", revision: "3" },
  ] : undefined);
  await h.flow.editProject();
  assert.equal(h.flow.editorOpen.value, true);
  assert.equal(h.flow.listError.value, "");
  assert.deepEqual(h.flow.credentials.value.map(item => [item.id, item.revision]), [["token", "10"], ["password", "3"]]);
  assert.equal(h.calls.filter(call => call.path.startsWith("/credentials")).length, 1);
  assert.equal(h.calls.find(call => call.path.startsWith("/credentials")).path, "/credentials");
  assert.equal(h.calls.filter(call => call.path.startsWith("/hosts")).length, 1);
  assert.equal(h.calls.find(call => call.path.startsWith("/hosts")).path, "/hosts");
  assert.equal(h.calls.filter(call => call.path.startsWith("/git-accounts")).length, 1);
  assert.equal(h.calls.find(call => call.path.startsWith("/git-accounts")).path, "/git-accounts");
  assert.equal(h.calls.some(call => call.path === "/software-instances?page=1&pageSize=100"), true);
  h.close();
});

test("unrecognized client errors show an actionable Chinese message", async () => {
  const h = harness(path => { if (path === "/hosts") throw new TypeError("r.items is not iterable"); });
  await h.flow.editProject();
  assert.equal(h.flow.editorOpen.value, false);
  assert.equal(h.flow.listError.value, "读取项目配置失败，请刷新后重试");
  h.close();
});

test("environment choices include only ready named tools and Docker on the selected host", () => {
  const h = harness();
  const tool = { id: "jdk", name: "Java 21", hostId: "hp", softwareType: "JAVA", runtimeKind: "TOOL", installationStatus: "READY", managementMode: "MANAGED", desiredState: "INSTALLED", actualState: "INSTALLED" };
  h.flow.software.value = [tool,
    { ...tool, id: "engine", name: "Docker", softwareType: "DOCKER", runtimeKind: "SYSTEMD" },
    { ...tool, id: "label", name: "Label Studio", softwareType: "LABEL_STUDIO", runtimeKind: "SYSTEMD" },
    { ...tool, id: "mysql", name: "MySQL", softwareType: "MYSQL", runtimeKind: "DOCKER" },
    { ...tool, id: "failed", installationStatus: "FAILED" },
    { ...tool, id: "other-host", hostId: "other" },
    { ...tool, id: "blank", name: " " },
    { ...tool, id: "released", managementMode: "RELEASED" },
    { ...tool, id: "removed", actualState: "REMOVED" },
  ];
  assert.deepEqual(h.flow.softwareChoices(""), []);
  assert.deepEqual(h.flow.softwareChoices("hp").map(item => item.id), ["jdk", "engine"]);
  assert.equal(softwareModel.softwareVersionText('openjdk version "21.0.2" 2024-01-16\nOpenJDK Runtime Environment'), "21.0.2");
  h.close();
});

test("failed save retains the draft and server conflict message", async () => {
  const h = harness((path, method) => { if (method === "PATCH") throw Error("项目已被修改，请刷新后重试"); });
  await h.flow.editProject({ id: "project" });
  h.flow.draft.value.modules[0].name = "尚未保存的模块";
  await h.flow.saveProject();
  assert.equal(h.flow.draft.value.modules[0].name, "尚未保存的模块");
  assert.equal(h.flow.editorOpen.value, true);
  assert.match(h.flow.editorError.value, /项目已被修改/);
  assert.equal(h.flow.saving.value, false);
  h.close();
});

test("removing a referenced repository or module requires adjusting its usage first", async () => {
  const h = harness(); await h.flow.editProject({ id: "project" });
  h.flow.removeRepository(h.flow.draft.value.repositories[0].id);
  assert.equal(h.flow.draft.value.repositories.length, 1);
  assert.match(h.flow.editorError.value, /后端/);
  h.flow.removeModule();
  assert.equal(h.flow.draft.value.modules.length, 1);
  assert.match(h.flow.editorError.value, /test/);
  h.close();
});

test("credential variable mode remains selectable before an ID is chosen and clears plaintext", async () => {
  const h = harness(); await h.flow.editProject({ id: "project" });
  const target = h.flow.draft.value.environments[0].deployments[0];
  target.variables.push({ name: "API_TOKEN", value: "not-for-credential-body", credentialId: "", credentialRevision: "" });
  const variable = target.variables[0];
  h.flow.changeVariableMode(variable, "CREDENTIAL");
  assert.equal(h.flow.usesCredential(variable), true);
  assert.equal(variable.value, "");
  await h.flow.saveProject();
  assert.match(h.flow.editorError.value, /选择连接凭据/);
  assert.equal(h.calls.some(call => call.method === "PATCH"), false);
  h.flow.credentials.value = [{ id: "token", revision: "4", name: "Git Token", kind: "TOKEN", revokedAt: "" }];
  h.flow.setVariableCredential(variable, "token");
  assert.equal(variable.credentialRevision, "4");
  h.flow.changeVariableMode(variable, "VALUE");
  assert.equal(variable.credentialId, ""); assert.equal(variable.credentialRevision, "");
  h.close();
});

test("switching an empty variable to credentials updates the displayed mode", async () => {
  const h = harness(); await h.flow.editProject({ id: "project" });
  const target = h.flow.draft.value.environments[0].deployments[0];
  target.variables.push({ name: "TOKEN", value: "", credentialId: "", credentialRevision: "" });
  const variable = target.variables[0], visibleMode = vue.computed(() => h.flow.usesCredential(variable));
  assert.equal(visibleMode.value, false);
  h.flow.changeVariableMode(variable, "CREDENTIAL");
  assert.equal(visibleMode.value, true);
  h.close();
});

test("a late project response cannot overwrite the most recently opened draft", async () => {
  let resolveOld;
  const h = harness(path => path === "/application-projects/old" ? new Promise(resolve => { resolveOld = resolve; }) : undefined);
  const previous = h.flow.editProject({ id: "old" }); await flush();
  await h.flow.editProject({ id: "new" });
  resolveOld({ ...sampleProject(), id: "old" }); await previous;
  assert.equal(h.flow.draft.value.id, "new");
  assert.equal(h.flow.editorOpen.value, true);
  h.close();
});

test("list responses cannot replace a newer page with an older request", async () => {
  let resolveOld, count = 0;
  const h = harness(path => {
    if (!path.startsWith("/application-projects?")) return;
    if (++count === 1) return new Promise(resolve => { resolveOld = resolve; });
    return { items: [{ ...sampleProject(), id: "new" }], total: 1, page: 1, pageSize: 10 };
  });
  const previous = h.flow.load(); await flush(); await h.flow.load();
  resolveOld({ items: [{ ...sampleProject(), id: "old" }], total: 1, page: 1, pageSize: 10 }); await previous;
  assert.equal(h.flow.records.value[0].id, "new");
  h.close();
});
