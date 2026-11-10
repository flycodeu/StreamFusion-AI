import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";

const component = await readFile(new URL("../src/SoftwareCommandTemplates.vue", import.meta.url), "utf8");
const source = component.split('<script setup lang="ts">')[1].split("</script>")[0];
const installation = {};
const pagination = {};
new Function("require", "exports", ts.transpileModule(await readFile(new URL("../src/pagination.ts", import.meta.url), "utf8"), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
}).outputText)(() => vue, pagination);
new Function("exports", ts.transpileModule(await readFile(new URL("../src/serviceInstallation.ts", import.meta.url), "utf8"), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
}).outputText)(installation);
const copy = value => JSON.parse(JSON.stringify(value));
test("official uninstall steps use receipt-owned paths and never show system root as a deletion scope", () => {
  const recipe = { native: { buildRecipe: "SYSTEM_APT" } };
  const context = { paths: { root: "/", data: "/" }, official: { paths: { data: "", ownedPaths: [] } } };
  for (const action of ["UNINSTALL_PREPARE", "UNINSTALL_CONFIRM"]) {
    const rows = installation.recipeSystemStepDetails(recipe, { kind: "SYSTEM", action }, context);
    assert.ok(rows.length);
    assert.ok(rows.every(row => row.value !== "/"));
  }
  context.official.paths.ownedPaths = ["/var/lib/example"];
  assert.ok(installation.recipeSystemStepDetails(recipe, { kind: "SYSTEM", action: "UNINSTALL_PREPARE" }, context).some(row => row.value === "/var/lib/example"));
});
class ApiError extends Error {
  constructor(message, code) { super(message); this.code = code; }
}
function harness() {
  const calls = [], events = [];
  let stored;
  const request = async (path, method = "GET", data) => {
    calls.push({ path, method, data: data && copy(data) });
    if (method === "GET") return path === "/command-template-examples" ? [] : stored ? [copy(path === "/software-recipes" ? stored.draft : stored)] : [];
    if (path === "/command-templates" && method === "POST") {
      stored = { id: "template-a", version: "1", revision: 0, draft: copy(data.definition), updatedAt: "2026-11-06" };
    } else {
      if (data.expectedVersion !== stored.version) throw new ApiError("配置已变化，请刷新后重试", "VERSION_CONFLICT");
      stored.version = String(Number(stored.version) + 1);
      if (method === "PUT") stored.draft = copy(data.definition);
      else if (method === "DELETE") delete stored.published;
      else if (path.endsWith("/publish")) stored.published = { ...copy(stored.draft), revision: String(++stored.revision) };
      else throw new Error(`Unexpected request: ${method} ${path}`);
    }
    return copy(stored);
  };
  const module = { exports: {} }, scope = vue.effectScope();
  const compiled = ts.transpileModule(source + "\nexport { draft, commands, records, examples, selectedExample, createFromExample, selectedId, editingVersion, dirty, preview, error, loadDraft, refresh, reloadSelected, save, publish, previewPublish, withdraw, changeKind, changeSource, validate, templateStatus, softwareGroups, selectedGroupKey, groupRecords, activePhase, selectPhase, phaseSteps, visibleSteps, viewingRuntime, viewingSource, selectVariant, activeStep, addStep, removeStep, moveStep, canMoveStep };", {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
  }).outputText;
  scope.run(() => new Function("require", "module", "exports", "defineEmits", "defineProps", compiled)(name => {
    if (name === "vue") return { ...vue, onMounted: () => {} };
    if (name === "./api") return { request, ApiError };
    if (name === "./serviceInstallation") return installation;
    if (name === "./pagination") return pagination;
    if (name === "element-plus") return { ElMessageBox: { confirm: async () => {} } };
    return {};
  }, module, module.exports, () => (...args) => events.push(args), () => ({ active: true })));
  return { form: module.exports, calls, events, get stored() { return stored; }, dispose: () => scope.stop() };
}

test("blank templates cannot be saved, and runtime selection produces one consistent source", async () => {
  const h = harness();
  h.form.loadDraft();
  await h.form.save();
  assert.equal(h.calls.length, 0);
  assert.match(h.form.error.value.message, /custom-/);
  h.form.loadDraft(undefined, true);
  assert.equal(h.form.validate(), "");
  const demoLines = h.form.commands.value.steps[0].script.split("\n");
  assert.equal(demoLines.length, 2);
  assert.ok(demoLines[0].endsWith('> "$OPS_INSTALL_ROOT/bin/demo"'));
  assert.equal(demoLines[1], 'chmod 755 "$OPS_INSTALL_ROOT/bin/demo"');
  h.form.commands.value.kind = "DOCKER";
  h.form.changeKind();
  assert.equal(h.form.draft.value.kind, "SERVICE");
  assert.deepEqual(h.form.draft.value.runtimeModes, ["DOCKER"]);
  assert.deepEqual(h.form.draft.value.sourceModes, ["REGISTRY"]);
  assert.equal(h.form.viewingRuntime.value, "DOCKER");
  assert.equal(h.form.viewingSource.value, "REGISTRY");
  h.form.addStep();
  const added = h.form.commands.value.steps[h.form.activeStep.value];
  h.form.draft.value.sourceModes = ["ARCHIVE"];
  h.form.commands.value.kind = "TOOL";
  h.form.changeKind();
  assert.deepEqual(h.form.draft.value.runtimeModes, ["NATIVE"]);
  assert.deepEqual(h.form.draft.value.sourceModes, ["ARCHIVE"]);
  assert.equal(h.form.viewingRuntime.value, "NATIVE");
  assert.equal(h.form.viewingSource.value, "ARCHIVE");
  assert.equal(added.runtimeMode, "NATIVE");
  assert.deepEqual(copy(added.sourceModes), ["ARCHIVE"]);
  h.dispose();
});

test("preview performs no request, and publishing saves the exact draft before exposing it to the catalog", async () => {
  const h = harness();
  h.form.loadDraft(undefined, true);
  const definition = copy(h.form.draft.value);
  h.form.previewPublish();
  assert.equal(h.form.preview.value, true);
  assert.equal(h.calls.length, 0);
  await h.form.publish();
  assert.deepEqual(h.calls.map(({ path, method }) => [path, method]), [["/command-templates", "POST"], ["/command-templates/template-a/publish", "POST"]]);
  assert.deepEqual(h.calls[0].data.definition, definition);
  assert.deepEqual(h.calls[1].data, { expectedVersion: "1" });
  assert.equal(h.form.templateStatus(h.form.records.value[0]), "已发布");
  assert.equal(h.form.dirty.value, false);
  assert.deepEqual(h.events, [["changed"]]);
  h.dispose();
});

test("list refresh never silently advances the version of an open draft", async () => {
  const h = harness();
  h.form.loadDraft(undefined, true);
  await h.form.save();
  h.form.draft.value.name = "My pending change";
  h.stored.version = "2";
  h.stored.draft.name = "Another administrator's change";
  await h.form.refresh();
  assert.equal(h.form.draft.value.name, "My pending change");
  await h.form.save();
  assert.equal(h.calls.at(-1).data.expectedVersion, "1");
  assert.equal(h.form.error.value.code, "VERSION_CONFLICT");
  assert.equal(h.stored.draft.name, "Another administrator's change");
  await h.form.reloadSelected();
  assert.equal(h.form.draft.value.name, "Another administrator's change");
  assert.equal(h.form.editingVersion.value, "2");
  h.dispose();
});

test("withdraw only removes catalog publication and keeps an unsaved editor draft", async () => {
  const h = harness();
  h.form.loadDraft(undefined, true);
  await h.form.publish();
  h.form.draft.value.name = "Unfinished edit";
  await h.form.withdraw();
  assert.deepEqual(h.calls.at(-1), { path: "/command-templates/template-a", method: "DELETE", data: { expectedVersion: "2" } });
  assert.equal(h.form.draft.value.name, "Unfinished edit");
  assert.equal(h.form.dirty.value, true);
  assert.equal(h.form.records.value.length, 1);
  assert.equal(h.form.records.value[0].published, undefined);
  assert.equal(h.stored.draft.name, "命令示例");
  assert.equal(h.form.editingVersion.value, "3");
  h.dispose();
});

test("creating from a recipe copies its fixed commands and proposes a new key instead of overwriting", async () => {
  const h = harness();
  h.form.loadDraft(undefined, true);
  await h.form.save();
  const example = copy(h.stored.draft), requests = h.calls.length;
  h.form.examples.value = [example];
  h.form.selectedExample.value = example.key;
  await h.form.createFromExample();
  assert.equal(h.form.selectedId.value, "");
  assert.equal(h.form.draft.value.key, example.key + "-2");
  assert.deepEqual(h.form.draft.value.commands, example.commands);
  assert.equal(h.calls.length, requests);
  h.form.commands.value.steps[0].script = "printf changed";
  assert.notEqual(example.commands.steps[0].script, "printf changed");
  assert.notEqual(h.stored.draft.commands.steps[0].script, "printf changed");
  h.dispose();
});


test("built-in software groups versions and switches command views without changing the definition", async () => {
  const h = harness();
  const base = {
    key: "java", name: "Java", kind: "TOOL", version: "21", revision: "java-21-built-in", platform: "linux/amd64",
    image: "jdk:21", digest: "sha256:pinned", imageId: "sha256:local", officialUrl: "https://example.com/jdk", port: 0,
    runtimeModes: ["NATIVE", "DOCKER"], sourceModes: ["ONLINE", "REGISTRY", "ARCHIVE"],
    commands: { builtin: true, flowVersion: 1, revision: 1, kind: "TOOL", locator: "", steps: [
      { id: "native-install", phase: "INSTALL", name: "Install JDK", script: "install-jdk", runAs: "ROOT", timeoutSeconds: 60, runtimeMode: "NATIVE", sourceModes: ["ONLINE", "ARCHIVE"] },
      { id: "docker-pull", phase: "INSTALL", name: "Pull JDK", script: "docker pull pinned", runAs: "ROOT", timeoutSeconds: 60, runtimeMode: "DOCKER", sourceModes: ["REGISTRY"] },
      { id: "docker-load", phase: "INSTALL", name: "Load JDK", script: "docker load offline", runAs: "ROOT", timeoutSeconds: 60, runtimeMode: "DOCKER", sourceModes: ["ARCHIVE"] },
      { id: "verify", phase: "VERIFY", name: "Verify", script: "java -version", runAs: "ROOT", timeoutSeconds: 60 },
    ] },
  };
  const record = { id: "java-21", version: "1", revision: 1, draft: copy(base), published: { ...copy(base), commands: { ...copy(base.commands), revision: 2 } } };
  h.form.records.value = [record, { ...copy(record), id: "java-8", draft: { ...copy(base), version: "8" } }];
  h.form.loadDraft(record);
  assert.equal(h.form.softwareGroups.value.length, 1);
  assert.equal(h.form.softwareGroups.value[0].records.length, 2);
  assert.equal(h.form.templateStatus(record), "已发布");
  assert.equal(h.form.validate(), "");
  assert.deepEqual(copy(h.form.draft.value), base, "opening must not add metadata to a built-in definition");
  assert.deepEqual(h.form.phaseSteps.value.map(entry => entry.item.id), ["native-install", "verify"]);
  h.form.commands.value.steps[0].script = "install-jdk --checked";
  await h.form.selectVariant("runtime", "DOCKER");
  assert.deepEqual(h.form.phaseSteps.value.map(entry => entry.item.id), ["docker-pull", "verify"]);
  assert.equal(h.form.commands.value.steps[0].script, "install-jdk --checked");
  await h.form.selectVariant("source", "ARCHIVE");
  assert.deepEqual(h.form.phaseSteps.value.map(entry => entry.item.id), ["docker-load", "verify"]);
  h.form.selectPhase("INSTALL");
  assert.deepEqual(h.form.phaseSteps.value.map(entry => entry.item.id), ["docker-load", "verify"]);
  assert.deepEqual(copy(h.form.draft.value.runtimeModes), base.runtimeModes);
  assert.deepEqual(copy(h.form.draft.value.sourceModes), base.sourceModes);
  assert.equal(h.calls.length, 0, "viewing steps must not publish or execute commands");
  h.dispose();
});


test("ordered installation steps keep checks in place and allow adding, moving and removing scoped commands", () => {
  const h = harness();
  h.form.loadDraft(undefined, true);
  const command = (id, phase, runtimeMode = "NATIVE", sourceModes = ["ONLINE"]) => ({ id, kind: "COMMAND", phase, name: id, script: "true", runAs: "ROOT", timeoutSeconds: 60, runtimeMode, sourceModes });
  const system = { ...command("material", "INSTALL"), kind: "SYSTEM", action: "INSTALL_MATERIAL", script: "" };
  h.form.commands.value.steps = [system, command("download", "INSTALL"), command("check", "VERIFY"), command("configure", "INSTALL"), command("offline", "INSTALL", "NATIVE", ["ARCHIVE"]), command("remove", "UNINSTALL")];
  h.form.selectPhase("INSTALL");
  assert.deepEqual(h.form.phaseSteps.value.map(({ item }) => item.id), ["material", "download", "check", "configure"]);
  const original = copy(h.form.commands.value.steps);
  h.form.moveStep(1);
  h.form.activeStep.value = 0;
  h.form.removeStep();
  h.form.moveStep(1);
  assert.deepEqual(copy(h.form.commands.value.steps), original, "system actions cannot be deleted or moved by the editor");
  h.form.activeStep.value = 2;
  h.form.addStep();
  const added = h.form.commands.value.steps[3];
  assert.equal(added.kind, "COMMAND");
  assert.equal(added.phase, "INSTALL");
  assert.equal(added.runAs, "ROOT");
  assert.equal(added.runtimeMode, "NATIVE");
  assert.deepEqual(copy(added.sourceModes), ["ONLINE"]);
  added.script = "printf configured";
  h.form.moveStep(1);
  assert.deepEqual(h.form.phaseSteps.value.map(({ item }) => item.id), ["material", "download", "check", "configure", added.id]);
  assert.deepEqual(copy(h.form.commands.value.steps.find(item => item.id === "offline")), original[4]);
  h.form.removeStep();
  assert.deepEqual(copy(h.form.commands.value.steps), original);
  h.form.viewingSource.value = "ARCHIVE";
  h.form.selectPhase("INSTALL");
  assert.deepEqual(h.form.phaseSteps.value.map(({ item }) => item.id), ["offline"]);
  h.form.viewingSource.value = "ONLINE";
  const confirm = { ...system, id: "confirm", action: "INSTALL_CONFIRM" };
  h.form.commands.value.steps.splice(4, 0, confirm);
  h.form.activeStep.value = 4;
  h.form.addStep();
  assert.equal(h.form.commands.value.steps[5].action, "INSTALL_CONFIRM", "new commands stay before the final confirmation");
  assert.equal(h.form.canMoveStep(1), false);
  const boundaryOrder = copy(h.form.commands.value.steps);
  h.form.moveStep(1);
  assert.deepEqual(copy(h.form.commands.value.steps), boundaryOrder);
  h.dispose();
});

test("new flows do not require a separate verification phase and platform material steps reveal real sources", () => {
  const h = harness();
  h.form.loadDraft(undefined, true);
  h.form.commands.value.steps = h.form.commands.value.steps.filter(step => step.phase !== "VERIFY");
  assert.equal(h.form.validate(), "");
  h.form.draft.value.native = { parts: [{ name: "jdk-21.tar.gz", url: "https://example.com/jdk-21.tar.gz", sha256: "a".repeat(64), byteSize: 1048576, format: "tar.gz", stripComponents: 1 }] };
  h.form.commands.value.steps.unshift({ id: "material", kind: "SYSTEM", action: "INSTALL_MATERIAL", phase: "INSTALL", name: "获取安装包", script: "", runAs: "ROOT", timeoutSeconds: 0 });
  assert.equal(h.form.validate(), "");
  const details = installation.recipeSystemStepDetails(h.form.draft.value, h.form.commands.value.steps[0], { runtimeMode: "NATIVE", sourceMode: "ONLINE" });
  assert.ok(details.some(item => item.value === "https://example.com/jdk-21.tar.gz" && item.url));
  assert.ok(details.some(item => item.value.includes("a".repeat(64)) && item.value.includes("1.0 MiB")));
  const paths = installation.recipeSystemStepDetails(h.form.draft.value, { kind: "SYSTEM", action: "NATIVE_PREPARE" }, { paths: { root: "/opt/tools/jdk-21", config: "/opt/tools/jdk-21/config", data: "/opt/tools/jdk-21/data" } });
  assert.ok(paths.some(item => item.value === "/opt/tools/jdk-21"));
  assert.ok(paths.every(item => !item.value.includes("curl ")));
  h.dispose();
});
