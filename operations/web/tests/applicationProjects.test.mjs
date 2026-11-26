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

const read = async name => readFile(new URL(`../src/${name}`, import.meta.url), "utf8");
const model = load(await read("applicationProjects.ts"));
const component = (await read("ApplicationProjects.vue")).split('<script setup lang="ts">')[1].split("</script>")[0];
const projectInspection = await read("features/project-delivery/useProjectInspection.ts");
class ApiError extends Error {
  code = "APPLICATION_CONFIG_INVALID";
  traceId = "request-id";
}

function sampleProject() {
  return {
    id: "project", name: "StreamFusion AI", jenkinsUrl: "", description: "", version: "3",
    archived: false, repositories: [
      { id: "primary", name: "主仓库", url: "https://git.example/main.git", branch: "main", gitAccountId: "profile-id" },
      { id: "separate", name: "算法源码", url: "https://git.example/algorithm.git", branch: "", gitAccountId: "" },
    ],
    modules: [{
      id: "backend", name: "旧后端", repositoryId: "primary", directory: "platform-api",
      runtime: { kind: "DOCKER", command: "old-runtime-command" },
      build: { kind: "COMMAND", account: "runner" },
    }],
    environments: [{ id: "production", name: "生产", deployments: [{
      id: "deployment", moduleId: "backend", hostId: "host", variables: [{ name: "DB_PASSWORD", credentialId: "secret", value: "old-value" }],
    }] }],
  };
}

const detected = [
  { directory: "platform-api", name: "platform-api", kind: "BACKEND", environment: "Java 21", startCommand: "sh mvnw spring-boot:run", evidence: ["platform-api/pom.xml"] },
  { directory: "platform-web", name: "platform-web", kind: "FRONTEND", environment: "Node.js 24 · pnpm", startCommand: "pnpm dev", evidence: ["platform-web/package.json"] },
];

function harness(override = () => undefined) {
  const calls = [], cleanup = [], events = [];
  const props = vue.reactive({ active: true, source: undefined });
  const request = async (path, method = "GET", body, signal) => {
    calls.push({ path, method, body: body && JSON.parse(JSON.stringify(body)), signal });
    const result = override(path, method, body, signal);
    if (result !== undefined) return result;
    if (path.startsWith("/application-projects?")) return { items: [], total: 0 };
    if (path.endsWith("/inspection-sources")) return { items: [{ id: "registered-source", name: "StreamFusion AI" }] };
    if (path.endsWith("/inspect")) return { modules: body.directory ? detected.filter(item => item.directory === body.directory) : detected };
    if (path === "/application-projects/project") return method === "DELETE" ? null : sampleProject();
    if (path.endsWith("/catalog")) return { ...sampleProject(), version: "4" };
    if (path.endsWith("/archive")) return sampleProject();
    throw Error("Unexpected request " + path);
  };
  const scope = vue.effectScope();
  const hooks = { ...vue, onMounted: () => {}, onUnmounted: fn => cleanup.push(fn) };
  const api = { request, ApiError };
  const inspection = load(projectInspection, { vue: hooks, "../../api": api, "../../applicationProjects": model });
  const flow = scope.run(() => load(component + `
    export const flow = {
      project, projectEditorOpen, projectDraft, moduleEditorOpen, moduleDraft, formError, inspectionOpen,
      inspectionMode, inspectionRepoUrl, inspectionBranch, inspectionGitAccountId,
      inspectionResults, sourcesLoading, sourceId, localRegistrationRepository, selectedDirectories, editProject, saveProject, openProject,
      editModule, saveModule, openInspection, inspect, selectNewDirectories, reviewSuggestion,
      addSuggestions, setArchived, confirmProjectDeletion, deleteProject, projectDeleteOpen, projectDeleteTarget,
      selectedModuleId, moduleDeleteOpen, confirmModuleDeletion, deleteModule, environmentBinding,
    };`, {
    vue: hooks,
    "element-plus": {},
    "./api": api,
    "./applicationProjects": model,
    "./features/project-delivery/useProjectInspection": inspection,
  }, { defineProps: () => props, defineEmits: () => (...event) => events.push(event) }).flow);
  return { flow, calls, props, events, close() { cleanup.forEach(fn => fn()); scope.stop(); } };
}

test("project list parameters and Jenkins links are safe for browser navigation", () => {
  const url = new URL(model.projectListPath(2, 50, " A&B /项目 ", "all"), "http://ops.test");
  assert.equal(url.searchParams.get("q"), "A&B /项目");
  assert.equal(url.searchParams.get("archived"), "all");
  assert.equal(model.jenkinsLink("https://jenkins.example/job/team/job/project/"), "https://jenkins.example/job/team/job/project/");
  for (const value of ["javascript:alert(1)", "https://user:secret@jenkins.example/job/x", "https://jenkins.example/job/x?token=s", "https://jenkins.example/job/x?", "https://jenkins.example/job/x#fragment", "https:jenkins.example"]) {
    assert.equal(model.jenkinsLink(value), "");
  }
});

test("name-only registration and reading an existing project need no host or deployment requests", async t => {
  const h = harness();
  t.after(h.close);
  h.flow.editProject();
  h.flow.projectDraft.value.name = " 新项目 ";
  await h.flow.saveProject();
  assert.equal(h.flow.projectEditorOpen.value, false);
  await h.flow.openProject(sampleProject());
  assert.deepEqual(h.calls.map(({ signal, ...call }) => call), [
    { path: "/application-projects/catalog", method: "POST", body: { name: "新项目", description: "" } },
    { path: model.projectListPath(1, 10, "", "false"), method: "GET", body: undefined },
    { path: "/application-projects/project", method: "GET", body: undefined },
  ]);
});

test("project edits send only the changed primary repository and avoid writing historical children", async t => {
  const h = harness();
  t.after(h.close);
  await h.flow.openProject(sampleProject());
  h.flow.editProject();
  h.flow.projectDraft.value.repositoryUrl = "https://git.example/renamed.git";
  h.flow.projectDraft.value.branch = "qa";
  await h.flow.saveProject();
  assert.deepEqual(h.calls.at(-1).body, {
    version: "3", name: "StreamFusion AI", description: "",
    repositories: [{ id: "primary", name: "主仓库", url: "https://git.example/renamed.git", branch: "qa" }],
  });
});

test("discovery previews findings and registers new directories without overwriting an existing module", async t => {
  const h = harness();
  t.after(h.close);
  await h.flow.openProject(sampleProject());
  await h.flow.openInspection();
  assert.equal(h.flow.sourceId.value, "registered-source");
  await h.flow.inspect();
  assert.deepEqual(h.calls.at(-1).body, { sourceId: "registered-source" });
  assert.deepEqual(h.flow.selectedDirectories.value, ["platform-web"]);
  assert.deepEqual(h.flow.inspectionResults.value[0].evidence, ["platform-api/pom.xml"]);
  await h.flow.addSuggestions();
  assert.deepEqual(h.calls.at(-1).body, {
    version: "3", modules: [{
      name: "platform-web", directory: "platform-web", repositoryId: "primary",
      catalog: { kind: "FRONTEND", environment: "Node.js 24 · pnpm", startCommand: "pnpm dev", runtimeMode: "" },
    }],
  });
});

test("discovery can bind a separately registered repository without matching primary paths", async t => {
  const h = harness();
  t.after(h.close);
  await h.flow.openProject(sampleProject());
  await h.flow.openInspection();
  await h.flow.inspect();
  h.flow.localRegistrationRepository.value = "separate";
  h.flow.selectNewDirectories();
  assert.deepEqual(h.flow.selectedDirectories.value, ["platform-api", "platform-web"]);
});

test("changing the registration repository for a local scan does not change the Git scan selection", async t => {
  const h = harness();
  t.after(h.close);
  await h.flow.openProject(sampleProject());
  await h.flow.openInspection();
  await h.flow.inspect();
  h.flow.localRegistrationRepository.value = "separate";
  h.flow.selectNewDirectories();
  assert.deepEqual(h.flow.selectedDirectories.value, ["platform-api", "platform-web"]);
  h.flow.inspectionMode.value = "repo";
  await h.flow.inspect();
  assert.deepEqual(h.calls.at(-1).body, {
    projectId: "project", repositoryId: "primary", repositoryUrl: "https://git.example/main.git",
    branch: "main", gitAccountId: "profile-id",
  });
});

test("adding a primary repository after manual registration does not duplicate an unbound module", async t => {
  const record = sampleProject();
  record.modules[0].repositoryId = "";
  const h = harness(path => path === "/application-projects/project" ? record : undefined);
  t.after(h.close);
  await h.flow.openProject(record);
  await h.flow.openInspection();
  await h.flow.inspect();
  assert.deepEqual(h.flow.selectedDirectories.value, ["platform-web"]);
});

test("per-module detection fills missing catalog fields but preserves manual values and production choices", async t => {
  const record = sampleProject();
  record.modules[0].catalog = { kind: "OTHER", environment: "团队手填环境", startCommand: "", runtimeMode: "已有容器" };
  const h = harness(path => path === "/application-projects/project" ? record : undefined);
  t.after(h.close);
  await h.flow.openProject(record);
  await h.flow.openInspection(record.modules[0]);
  await h.flow.inspect();
  assert.deepEqual(h.calls.at(-1).body, { sourceId: "registered-source", directory: "platform-api" });
  h.flow.reviewSuggestion(h.flow.inspectionResults.value[0]);
  assert.equal(h.flow.moduleEditorOpen.value, true);
  assert.deepEqual(model.moduleEdit(h.flow.moduleDraft.value).catalog,
    { kind: "OTHER", environment: "团队手填环境", startCommand: detected[0].startCommand, runtimeMode: "已有容器" });
  assert.equal(record.modules[0].catalog.startCommand, "");
});

test("failed inspection leaves manual registration available including a new separate repository", async t => {
  const h = harness(path => {
    if (path.endsWith("/inspect")) throw new ApiError("识别目录无法读取");
  });
  t.after(h.close);
  await h.flow.openProject(sampleProject());
  await h.flow.openInspection();
  await h.flow.inspect();
  assert.match(h.flow.formError.value, /识别目录无法读取.*request-id/);
  h.flow.inspectionOpen.value = false;
  h.flow.editModule();
  Object.assign(h.flow.moduleDraft.value, {
    name: "实验算法", directory: "./src/model/", kind: "ALGORITHM", environment: "Python 3.12",
    repositoryId: "new-repository", repositoryUrl: "https://git.example/new-model.git",
  });
  await h.flow.saveModule();
  const { modules: [module], repositories: [repository] } = h.calls.at(-1).body;
  assert.match(repository.id, /^[0-9a-f-]{36}$/);
  assert.equal(module.repositoryId, repository.id);
  assert.equal(module.directory, "src/model");
  assert.equal(repository.url, "https://git.example/new-model.git");
  assert.equal(module.catalog.environment, "Python 3.12");
  assert.equal(h.flow.moduleEditorOpen.value, false);
});

test("closing an inspection rejects late results from a previous source selection", async t => {
  let finish;
  const h = harness(path => path.endsWith("/inspect") ? new Promise(resolve => { finish = resolve; }) : undefined);
  t.after(h.close);
  await h.flow.openProject(sampleProject());
  await h.flow.openInspection();
  const scan = h.flow.inspect();
  const scanCall = h.calls.at(-1);
  h.flow.inspectionOpen.value = false;
  assert.equal(scanCall.signal.aborted, true);
  finish({ modules: detected });
  await scan;
  assert.deepEqual(h.flow.inspectionResults.value, []);
});

test("starting a Git scan while local sources load keeps the scan and settles both requests", async t => {
  const sources = Promise.withResolvers();
  const h = harness(path => path.endsWith("/inspection-sources") ? sources.promise : undefined);
  t.after(h.close);
  await h.flow.openProject(sampleProject());
  const discovery = h.flow.openInspection();
  assert.equal(h.flow.sourcesLoading.value, true);
  await h.flow.inspect();
  sources.resolve({ items: [{ id: "registered-source", name: "StreamFusion AI" }] });
  await discovery;
  assert.equal(h.flow.sourcesLoading.value, false);
  assert.equal(h.flow.inspectionMode.value, "repo");
  assert.deepEqual(h.flow.selectedDirectories.value, ["platform-web"]);
  h.flow.inspectionMode.value = "local";
  assert.equal(h.flow.sourceId.value, "registered-source");
  assert.deepEqual(h.flow.inspectionResults.value, []);
});

test("editing any Git scan input discards its previous registration and fill suggestions", async t => {
  const h = harness(path => path.endsWith("/inspection-sources") ? { items: [] } : undefined);
  t.after(h.close);
  await h.flow.openProject(sampleProject());
  await h.flow.openInspection();
  for (const [field, next] of [
    ["inspectionBranch", "release"],
    ["inspectionGitAccountId", "other-account"],
    ["inspectionRepoUrl", "https://git.example/other.git"],
  ]) {
    await h.flow.inspect();
    const [existing] = h.flow.inspectionResults.value;
    assert.deepEqual(h.flow.selectedDirectories.value, ["platform-web"]);
    const calls = h.calls.length;
    h.flow[field].value = next;
    assert.deepEqual(h.flow.inspectionResults.value, []);
    assert.deepEqual(h.flow.selectedDirectories.value, []);
    h.flow.reviewSuggestion(existing);
    assert.equal(h.flow.moduleEditorOpen.value, false);
    await h.flow.addSuggestions();
    assert.equal(h.calls.length, calls);
  }
});

test("a temporary Git address can be scanned but cannot change modules in the registered repository", async t => {
  const h = harness(path => path.endsWith("/inspection-sources") ? { items: [] } : undefined);
  t.after(h.close);
  const record = sampleProject();
  await h.flow.openProject(record);
  await h.flow.openInspection();

  h.flow.inspectionRepoUrl.value = "https://git.example/other.git";
  await h.flow.inspect();
  assert.equal(h.calls.at(-1).body.repositoryUrl, "https://git.example/other.git");
  assert.equal(h.flow.inspectionResults.value.length, 2);
  h.flow.reviewSuggestion(h.flow.inspectionResults.value[0]);
  assert.equal(h.flow.moduleEditorOpen.value, false);
  const calls = h.calls.length;
  h.flow.selectedDirectories.value = ["platform-web"];
  await h.flow.addSuggestions();
  assert.equal(h.calls.length, calls);

  h.flow.inspectionRepoUrl.value = record.repositories[0].url;
  assert.deepEqual(h.flow.inspectionResults.value, []);
  await h.flow.inspect();
  h.flow.reviewSuggestion(h.flow.inspectionResults.value[0]);
  assert.equal(h.flow.moduleEditorOpen.value, true);
  h.flow.moduleEditorOpen.value = false;

  await h.flow.openInspection();
  await h.flow.inspect();
  await h.flow.addSuggestions();
  assert.equal(h.calls.at(-1).method, "PATCH");
  assert.equal(h.calls.at(-1).body.modules[0].repositoryId, "primary");
});

test("only the current maintenance link can open a registered environment editor", async t => {
  const record = sampleProject();
  const deployments = record.environments[0].deployments;
  deployments.push({ ...deployments[0], id: "second", hostId: "other-host" });
  const reads = [];
  const h = harness(path => {
    if (path === "/application-projects/project")
      return reads.length ? reads.shift().promise : record;
  });
  t.after(h.close);
  await h.flow.openProject(record);

  const first = Promise.withResolvers(), second = Promise.withResolvers();
  reads.push(first, second);
  const source = { projectId: "project", moduleId: "backend" };
  h.props.source = { ...source, serviceInstanceId: "deployment" };
  await vue.nextTick();
  h.props.source = { ...source, serviceInstanceId: "second" };
  await vue.nextTick();
  first.resolve(record);
  await new Promise(setImmediate);
  assert.equal(h.flow.environmentBinding.value, undefined);

  second.resolve(record);
  await new Promise(setImmediate);
  assert.equal(h.flow.environmentBinding.value?.registration?.id, "second");
  assert.deepEqual(h.events, [["source-used"]]);

  h.props.source = { ...source, serviceInstanceId: "deleted" };
  await new Promise(setImmediate);
  assert.equal(h.flow.environmentBinding.value, undefined);
  assert.match(h.events.at(-2)[1].message, /运行配置已变更/);
});

test("leaving the project page during maintenance lookup does not open a deferred editor", async t => {
  const detail = Promise.withResolvers();
  const h = harness(path => path === "/application-projects/project" ? detail.promise : undefined);
  t.after(h.close);
  h.props.source = { projectId: "project", moduleId: "backend", serviceInstanceId: "deployment" };
  await vue.nextTick();
  h.props.source = undefined;
  h.props.active = false;
  await vue.nextTick();
  detail.resolve(sampleProject());
  await new Promise(setImmediate);
  assert.equal(h.flow.environmentBinding.value, undefined);
  assert.deepEqual(h.events, []);
});

test("archive restoration sends a versioned state change without serializing the old configuration", async t => {
  const h = harness();
  t.after(h.close);
  const record = sampleProject();
  await h.flow.setArchived(record, true);
  await h.flow.setArchived(record, false);
  const writes = h.calls.filter(call => call.method !== "GET").map(({ signal, ...call }) => call);
  assert.deepEqual(writes, [
    { path: "/application-projects/project/archive", method: "POST", body: { version: "3" } },
    { path: "/application-projects/project/catalog", method: "PATCH", body: { version: "3", archived: false } },
  ]);
});

test("delete sends a versioned request only after explicit confirmation and refreshes the list", async t => {
  const h = harness();
  t.after(h.close);
  const record = sampleProject();
  h.flow.confirmProjectDeletion(record);
  assert.equal(h.flow.projectDeleteTarget.value.name, record.name);
  assert.equal(h.calls.length, 0);
  h.flow.projectDeleteOpen.value = false;
  await h.flow.deleteProject();
  assert.equal(h.calls.length, 0);

  h.flow.confirmProjectDeletion(record);
  await h.flow.deleteProject();
  assert.equal(h.flow.projectDeleteOpen.value, false);
  assert.deepEqual(h.calls[0], { path: "/application-projects/project", method: "DELETE", body: { version: "3" }, signal: undefined });
  assert.match(h.calls[1].path, /^\/application-projects\?/);
});

test("a referenced project keeps its confirmation dialog and shows the controller response", async t => {
  const h = harness((path, method) => {
    if (method === "DELETE") throw new ApiError("项目仍关联应用实例");
  });
  t.after(h.close);
  h.flow.confirmProjectDeletion(sampleProject());
  await h.flow.deleteProject();
  assert.equal(h.flow.projectDeleteOpen.value, true);
  assert.match(h.flow.formError.value, /项目仍关联应用实例.*request-id/);
  assert.equal(h.calls.length, 1);
});

test("module deletion requires confirmation, preserves the project version, and selects a remaining module", async t => {
  const record = sampleProject();
  const removable = { id: "unused", name: "空模块", directory: ".", repositoryId: "primary" };
  record.modules.push(removable);
  const h = harness((path, method) => {
    if (path === "/application-projects/project" && method === "GET") return record;
    if (path.endsWith("/modules/unused")) return { ...record, version: "4", modules: [record.modules[0]] };
  });
  t.after(h.close);
  await h.flow.openProject(record);
  h.flow.selectedModuleId.value = removable.id;
  h.flow.confirmModuleDeletion(removable);
  h.flow.moduleDeleteOpen.value = false;
  await h.flow.deleteModule();
  assert.equal(h.calls.length, 1);

  h.flow.confirmModuleDeletion(removable);
  await h.flow.deleteModule();
  assert.deepEqual(h.calls.at(-1), { path: "/application-projects/project/modules/unused", method: "DELETE", body: { version: "3" }, signal: undefined });
  assert.equal(h.flow.moduleDeleteOpen.value, false);
  assert.equal(h.flow.selectedModuleId.value, "backend");
  assert.equal(h.flow.project.value.version, "4");
});

test("a referenced module stays at the selected position and shows why deletion failed", async t => {
  const h = harness((path, method) => {
    if (path.endsWith("/modules/backend") && method === "DELETE") {
      const error = new ApiError("请先解除该模块关联的环境或服务实例");
      error.code = "APPLICATION_MODULE_IN_USE";
      throw error;
    }
  });
  t.after(h.close);
  const record = sampleProject();
  await h.flow.openProject(record);
  h.flow.confirmModuleDeletion(record.modules[0]);
  await h.flow.deleteModule();
  assert.equal(h.flow.moduleDeleteOpen.value, true);
  assert.equal(h.flow.selectedModuleId.value, "backend");
  assert.match(h.flow.formError.value, /运行配置.*APPLICATION_MODULE_IN_USE.*request-id/);
});

test("remote Git repository auto-inspection defaults when local sources are empty and imports deep metadata", async t => {
  const remoteDetected = [
    {
      directory: "platform-web",
      name: "platform-web",
      kind: "FRONTEND",
      environment: "Vue、Vite、Node.js 24、pnpm",
      startCommand: "pnpm dev",
      evidence: ["platform-web/package.json", "platform-web/vite.config.ts"],
      dependencies: ["Vue", "Vite", "Node.js", "pnpm", "Nginx"],
      buildCommand: "pnpm run build",
      testCommand: "pnpm run test",
      artifactPath: "dist",
      recommendedRuntimeMode: "STATIC",
      proxyHints: [{ location: "/api", stripPrefix: true, targetPort: 8080 }],
    },
  ];
  const h = harness((path) => {
    if (path.endsWith("/inspection-sources")) return { items: [] };
    if (path.endsWith("/inspect")) return { modules: remoteDetected };
  });
  t.after(h.close);
  await h.flow.openProject(sampleProject());
  await h.flow.openInspection();
  assert.equal(h.flow.inspectionMode.value, "repo");
  assert.equal(h.flow.inspectionRepoUrl.value, "https://git.example/main.git");
  assert.equal(h.flow.inspectionBranch.value, "main");
  await h.flow.inspect();
  assert.deepEqual(h.calls.at(-1).body, {
    projectId: "project",
    repositoryId: "primary",
    repositoryUrl: "https://git.example/main.git",
    branch: "main",
    gitAccountId: "profile-id",
  });
  await h.flow.addSuggestions();
  const savedModule = h.calls.at(-1).body.modules[0];
  assert.equal(savedModule.catalog.runtimeMode, "STATIC");
  assert.equal(savedModule.catalog.buildCommand, "pnpm run build");
  assert.equal(savedModule.catalog.artifactPath, "dist");
  assert.deepEqual(savedModule.catalog.dependencies, ["Vue", "Vite", "Node.js", "pnpm", "Nginx"]);
  assert.deepEqual(savedModule.catalog.proxyHints, [{ location: "/api", stripPrefix: true, targetPort: 8080 }]);
});
