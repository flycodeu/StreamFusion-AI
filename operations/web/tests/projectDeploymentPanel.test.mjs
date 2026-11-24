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
  new Function("require", "exports", ...Object.keys(globals), code)(
    name => imports[name], exports, ...Object.values(globals),
  );
  return exports;
}
const deploy = load(await read("projectDeployments.ts"));
const script = (await read("ProjectDeploymentPanel.vue")).split('<script setup lang="ts">')[1].split("</script>")[0];
const target = "/application-projects/demo/deployments/java";
const buildPath = "/application-projects/demo/modules/backend/jenkins-build";
const sampleBuild = { configured: true, status: "SUCCESS", number: 42, artifacts: [{ name: "app.jar" }] };

function panel(t, defaults) {
  const calls = [], events = [], polls = new Map();
  let observedStatus = defaults, onUnmounted, nextPoll = 0;
  const { flow } = load(script + `
    export const flow = { refresh, chooseNumber, observeBuild, readBuild, readExecutionLogs, preflight, preflightCurrent, submit,
      openQa, canDeploy, selectedNumber, selectedBuild, qaMonitor, plan, status, needsCheck, rollbackTarget, siteConfigPath, error };
  `, {
    vue: { ...vue, onMounted() {}, onUnmounted: callback => { onUnmounted = callback; } },
    "./api": { request: async (path, method, body) => {
      calls.push({ path, method, body });
      if (path === target) return structuredClone(observedStatus);
      if (path.endsWith("/jenkins-monitor")) return { configured: true, version: "4" };
      if (path === `${buildPath}?number=41`) return { ...sampleBuild, number: 41, status: "FAILURE" };
      if (path === `${buildPath}?number=42`) return sampleBuild;
      if (path.endsWith("/preflight")) return { token: "test-confirmation", buildNumber: body.buildNumber, commit: "aaaa" };
      return { jobId: "submitted-job" };
    } },
    "./projectJenkins": { moduleJenkinsPath: () => "/application-projects/demo/modules/backend" },
    "./projectDeployments": deploy,
  }, {
    defineProps: () => ({
      projectId: "demo", moduleId: "backend", moduleName: "API", hostName: "测试机",
      environment: "test", deploymentId: "java",
    }),
    defineEmits: () => (...event) => events.push(event),
    setTimeout: callback => { polls.set(++nextPoll, callback); return nextPoll; },
    clearTimeout: id => { polls.delete(id); },
  });
  t.after(onUnmounted);
  return { calls, events, flow, polls, setStatus: value => { observedStatus = value; } };
}

test("the deployment panel previews and submits only the selected successful build with an archived artifact", async t => {
  const { calls, flow } = panel(t, { kind: "JAVA", health: "NOT_DEPLOYED" });
  await flow.refresh();
  await flow.submit("verify");
  assert.equal(calls.some(call => call.path.endsWith("/verify")), false);
  await flow.openQa();
  assert.equal(flow.qaMonitor.value.configured, true);
  assert.equal(calls[1].path, "/application-projects/demo/modules/backend/jenkins-monitor");

  for (const build of [{ ...sampleBuild, building: true }, { ...sampleBuild, artifacts: [] }, { ...sampleBuild, status: "FAILURE" }]) {
    flow.observeBuild(build);
    assert.equal(flow.selectedNumber.value, undefined);
  }
  flow.chooseNumber(41);
  await flow.readBuild();
  await flow.preflight();
  assert.equal(calls.some(call => call.path.endsWith("/preflight")), false);

  flow.observeBuild(sampleBuild);
  assert.equal(flow.selectedNumber.value, 42);
  await flow.preflight();
  assert.deepEqual(calls.at(-1), {
    path: `${target}/preflight`, method: "POST", body: { buildNumber: 42 },
  });
  await flow.submit("submit");
  assert.deepEqual(calls.find(call => call.path.endsWith("/submit")), {
    path: `${target}/submit`, method: "POST", body: { token: "test-confirmation" },
  });
});

test("an unresolved first deployment allows verification and notes the exact job for execution logs", async t => {
  const { calls, events, flow, polls } = panel(t, {
    kind: "JAVA", health: "UNKNOWN", latest: { status: "UNKNOWN", jobId: "unresolved-job", action: "DEPLOY" },
  });
  await flow.refresh();
  assert.equal(flow.needsCheck.value, true);
  assert.equal(flow.status.value.current, undefined);
  assert.equal(polls.size, 0);
  flow.readExecutionLogs();
  assert.deepEqual(events.at(-1), [
    "executionLogs", { jobId: "unresolved-job", title: "API · test · 发布执行日志" },
  ]);
  await flow.preflight();
  await flow.submit("submit");
  await flow.submit("rollback");
  await flow.submit("verify");
  assert.deepEqual(calls.filter(call => call.method === "POST"), [
    { path: `${target}/verify`, method: "POST", body: {} },
  ]);
});

test("an existing release can be checked after its module is no longer eligible for new deployments", async t => {
  const current = { releaseId: "release-7", buildNumber: 7, commit: "aaaa" };
  const { calls, flow } = panel(t, { health: "UNKNOWN", current });
  await flow.refresh();
  await flow.openQa();
  await flow.submit("verify");
  assert.deepEqual(calls.filter(call => call.path !== target), [
    { path: `${target}/verify`, method: "POST", body: {} },
  ]);
});

test("rollback uses the version shown at confirmation even if a refresh finds a newer one", async t => {
  const previous = { releaseId: "release-7", buildNumber: 7, commit: "aaaa" };
  const { calls, flow, setStatus } = panel(t, { kind: "STATIC", health: "READY", previous });
  await flow.refresh();
  flow.rollbackTarget.value = flow.status.value.previous;
  setStatus({ kind: "STATIC", health: "READY", previous: { releaseId: "release-8", buildNumber: 8, commit: "bbbb" } });
  await flow.refresh();
  await flow.submit("rollback");
  assert.deepEqual(calls.find(call => call.path.endsWith("/rollback")), {
    path: `${target}/rollback`, method: "POST", body: { previousReleaseId: "release-7" },
  });
});

test("a live Nginx site reuses its current build and blocks a changed commit", async t => {
  const current = { releaseId: "release-7", buildNumber: 7, commit: "aaaa" };
  const { calls, flow, setStatus } = panel(t, { kind: "STATIC", health: "READY", current });
  await flow.refresh();
  assert.equal(flow.siteConfigPath.value, "/etc/nginx/conf.d/sfops-project-java.conf");
  await flow.preflightCurrent();
  assert.deepEqual(calls.at(-1), {
    path: `${target}/preflight`, method: "POST", body: { buildNumber: 7, currentReleaseId: "release-7" },
  });
  assert.equal(flow.plan.value?.buildNumber, 7);
  await flow.submit("submit");

  setStatus({ kind: "STATIC", health: "READY", current: { ...current, commit: "bbbb" } });
  await flow.refresh();
  await flow.preflightCurrent();
  assert.equal(flow.plan.value, undefined);
  assert.match(flow.error.value, /Jenkins 制品已变化/);
});
