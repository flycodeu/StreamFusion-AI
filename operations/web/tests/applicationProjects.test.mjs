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
class ApiError extends Error { constructor(code, message = code) { super(message); this.code = code; } }
const flush = async () => { for (let i = 0; i < 8; i++) { await Promise.resolve(); await vue.nextTick(); } };
function sampleProject() {
  const project = model.newApplicationProject(), repository = model.newProjectRepository(), module = model.newProjectModule(repository.id), environment = model.newProjectEnvironment();
  Object.assign(project, { id: "project", code: "intelligentize", name: "智能化平台", version: "1" });
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
  const flow = scope.run(() => load(component + "\nexport const flow = { draft, baseline, records, total, page, pageSize, query, archived, listError, editorError, editorOpen, activeStep, selectStep, nextStep, previousStep, variableSections, loading, saving, editProject, saveProject, load, addRepository, removeRepository, addModule, removeModule, addEnvironment, addDeployment, selectedModuleId, selectedEnvironmentId, selectedDeploymentId, changeVariableMode, usesCredential, setVariableCredential, credentials, software, softwareChoices };", {
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
  assert.equal(model.projectValidationError(project), undefined);
  copy.modules.push({ ...copy.modules[0], id: "other", name: " 正在编辑 " });
  assert.match(model.projectValidationError(copy).message, /模块名称重复/);
});

test("project search and archive filters are encoded for server pagination", () => {
  const path = model.projectListPath(2, 50, " A&B /项目 ", "all"), url = new URL(path, "http://ops.test");
  assert.equal(url.searchParams.get("q"), "A&B /项目");
  assert.equal(url.searchParams.get("page"), "2");
  assert.equal(url.searchParams.get("pageSize"), "50");
  assert.equal(url.searchParams.get("archived"), "all");
});

test("wizard navigation preserves the entire draft and never submits while moving between steps", async () => {
  const h = harness(); await h.flow.editProject({ id: "project" });
  h.flow.draft.value.description = "尚未保存的配置";
  const snapshot = JSON.stringify(h.flow.draft.value), calls = h.calls.length;
  await h.flow.previousStep();
  assert.equal(h.flow.activeStep.value, "basics");
  await h.flow.nextStep();
  assert.equal(h.flow.activeStep.value, "repositories");
  await h.flow.nextStep();
  assert.equal(h.flow.activeStep.value, "modules");
  await h.flow.selectStep("environments");
  await h.flow.nextStep();
  assert.equal(h.flow.activeStep.value, "environments");
  await h.flow.previousStep();
  assert.equal(h.flow.activeStep.value, "modules");
  await h.flow.selectStep("basics");
  assert.equal(h.flow.activeStep.value, "basics");
  assert.equal(JSON.stringify(h.flow.draft.value), snapshot);
  assert.equal(h.calls.length, calls);
  h.close();
});

test("next validates only the current step while direct step selection remains available", async () => {
  const h = harness(); await h.flow.editProject();
  await h.flow.nextStep();
  assert.equal(h.flow.activeStep.value, "basics");
  assert.match(h.flow.editorError.value, /项目名称/);
  await h.flow.selectStep("repositories");
  assert.equal(h.flow.editorError.value, "");
  await h.flow.nextStep();
  assert.equal(h.flow.activeStep.value, "modules");
  await h.flow.selectStep("basics");
  Object.assign(h.flow.draft.value, { name: "新项目", code: "team-app" });
  h.flow.addModule();
  await h.flow.nextStep();
  assert.equal(h.flow.activeStep.value, "repositories");
  await h.flow.nextStep();
  await h.flow.nextStep();
  assert.equal(h.flow.activeStep.value, "modules");
  assert.match(h.flow.editorError.value, /模块名称/);
  assert.equal(h.flow.selectedModuleId.value, h.flow.draft.value.modules[0].id);
  h.flow.draft.value.modules[0].name = "后端";
  h.flow.addEnvironment();
  await h.flow.nextStep();
  assert.equal(h.flow.activeStep.value, "environments");
  assert.equal(h.calls.some(call => call.method !== "GET"), false);
  h.close();
});

test("an in-flight save prevents next, previous and top-step navigation", async () => {
  let resolveSave;
  const h = harness((path, method) => method === "PATCH" ? new Promise(resolve => { resolveSave = resolve; }) : undefined);
  await h.flow.editProject({ id: "project" });
  await h.flow.selectStep("modules");
  const pending = h.flow.saveProject(); await flush();
  assert.equal(h.flow.saving.value, true);
  await h.flow.nextStep();
  await h.flow.previousStep();
  await h.flow.selectStep("basics");
  assert.equal(h.flow.activeStep.value, "modules");
  assert.equal(h.calls.filter(call => call.method === "PATCH").length, 1);
  resolveSave(sampleProject()); await pending;
  assert.equal(h.flow.saving.value, false);
  h.close();
});

test("creating a project requires its code and keeps advanced sections optional", async () => {
  const h = harness((path, method, body) => method === "POST" ? { ...body, id: "created", version: "1" } : undefined);
  await h.flow.editProject();
  h.flow.draft.value.name = "新项目";
  h.flow.activeStep.value = "environments";
  await h.flow.saveProject();
  assert.match(h.flow.editorError.value, /请填写项目代码/);
  assert.equal(h.flow.activeStep.value, "basics");
  assert.equal(h.calls.some(call => call.method === "POST"), false);
  Object.assign(h.flow.draft.value, { code: " team-app ", description: "项目说明" });
  await h.flow.saveProject();
  const saved = h.calls.find(call => call.method === "POST").body;
  assert.equal(saved.code, "team-app");
  assert.equal(saved.description, "项目说明");
  assert.deepEqual([saved.repositories, saved.modules, saved.environments], [[], [], []]);
  assert.equal(h.flow.editorOpen.value, false);
  h.close();
});

test("project codes follow the server's character limit without restricting their language", () => {
  const project = sampleProject();
  project.code = "项目".repeat(30);
  assert.equal(model.projectValidationError(project), undefined);
  project.code += "一";
  assert.deepEqual(model.projectValidationError(project), { section: "basics", message: "项目代码最多60个字符" });
});

test("a duplicate code returns to basic information and preserves the unsaved configuration", async () => {
  const h = harness((path, method) => { if (method === "PATCH") throw new ApiError("APPLICATION_CODE_DUPLICATED", "项目代码已存在，请使用其他代码"); });
  await h.flow.editProject({ id: "project" });
  h.flow.draft.value.code = "duplicate";
  h.flow.draft.value.modules[0].directory = "backend";
  h.flow.activeStep.value = "environments";
  await h.flow.saveProject();
  assert.equal(h.flow.editorOpen.value, true);
  assert.equal(h.flow.activeStep.value, "basics");
  assert.equal(h.flow.draft.value.code, "duplicate");
  assert.equal(h.flow.draft.value.modules[0].directory, "backend");
  assert.match(h.flow.editorError.value, /项目代码已存在/);
  h.close();
});

test("name validation reveals the specific module or environment even when another one is selected", async () => {
  const h = harness(); await h.flow.editProject({ id: "project" });
  const module = model.newProjectModule(h.flow.draft.value.repositories[0].id);
  h.flow.draft.value.modules.push(module);
  await h.flow.saveProject();
  assert.equal(h.flow.activeStep.value, "modules");
  assert.equal(h.flow.selectedModuleId.value, module.id);
  assert.match(h.flow.editorError.value, /模块名称/);
  module.name = "前端";
  const environment = model.newProjectEnvironment();
  h.flow.draft.value.environments.push(environment);
  await h.flow.saveProject();
  assert.equal(h.flow.activeStep.value, "environments");
  assert.equal(h.flow.selectedEnvironmentId.value, environment.id);
  assert.match(h.flow.editorError.value, /环境名称/);
  assert.equal(h.calls.some(call => call.method === "PATCH"), false);
  h.close();
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
  const environment = model.newProjectEnvironment(); environment.name = "prod";
  const target = model.newProjectDeployment(h.flow.draft.value.modules[0].id);
  environment.deployments.push(target); h.flow.draft.value.environments.push(environment);
  target.variables.push({ name: "API_TOKEN", value: "not-for-credential-body", credentialId: "", credentialRevision: "" });
  const variable = target.variables[0];
  h.flow.changeVariableMode(variable, "CREDENTIAL");
  assert.equal(h.flow.usesCredential(variable), true);
  assert.equal(variable.value, "");
  await h.flow.saveProject();
  assert.match(h.flow.editorError.value, /选择连接凭据/);
  assert.equal(h.flow.activeStep.value, "environments");
  assert.equal(h.flow.selectedEnvironmentId.value, environment.id);
  assert.equal(h.flow.selectedDeploymentId.value, target.id);
  assert.equal(h.flow.variableSections.has(target.id), true);
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
