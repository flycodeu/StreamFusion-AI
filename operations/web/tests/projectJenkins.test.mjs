import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";

const read = name => readFile(new URL(`../src/${name}`, import.meta.url), "utf8");
const component = (await read("ProjectJenkinsBuild.vue")).split('<script setup lang="ts">')[1].split("</script>")[0];
const project = load(await read("applicationProjects.ts"));
const helpers = load(await read("projectJenkins.ts"), { "./applicationProjects": project });
const path = "/application-projects/project";
const jobUrl = "https://jenkins.example/ci/job/team/job/project/";

function load(source, imports = {}, globals = {}) {
  const exports = {};
  const { outputText } = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } });
  new Function("require", "exports", ...Object.keys(globals), outputText)(name => imports[name], exports, ...Object.values(globals));
  return exports;
}

function harness(handler, context, stored = new Map()) {
  let unmount;
  const calls = [];
  const scope = vue.effectScope();
  const flow = scope.run(() => load(component + `
    export const flow = { monitor, build, queuedWithPrevious, stageLabel, settingsOpen, softwareId, username, token, statusError, log, logError, logOpen, jobDraft, submitError, loadMonitor, refreshStatus, editMonitor, saveMonitor, startQa, toggleLog, readLog };
  `, {
    vue: { ...vue, onMounted: () => {}, onUnmounted: fn => { unmount = fn; } },
    "element-plus": {},
    "./api": { ApiError: Error, request: async (target, method, data, signal) => {
      calls.push({ target, method, data: data && { ...data }, signal });
      return handler(target, method, data, signal);
    } },
    "./projectJenkins": helpers,
    "./applicationProjects": project,
  }, { defineProps: () => ({ projectId: "project", jobUrl, context }), defineEmits: () => () => {},
    sessionStorage: { getItem: key => stored.get(key) ?? null, setItem: (key, v) => stored.set(key, v), removeItem: key => stored.delete(key) } }).flow);
  return { flow, calls, close() { unmount(); scope.stop(); } };
}

const bound = {
  configured: true, version: "1", softwareId: "instance", username: "reader", hasToken: true,
  instances: [{ id: "instance", name: "Jenkins", hostName: "惠普", url: "https://jenkins.example/ci/" }],
};
const run = number => ({ configured: true, status: "SUCCESS", number, currentStage: "归档产物", stagesAvailable: true });

test("build links remain in the project job and unknown stage state stays unknown", () => {
  const artifact = `${jobUrl}7/artifact/platform-api.jar`;
  assert.equal(helpers.jenkinsBuildLink(jobUrl, artifact), artifact);
  for (const candidate of ["javascript:alert(1)", "https://foreign.example/", "https://jenkins.example/ci/job/team/job/other/7/", `${artifact}?token=x`]) {
    assert.equal(helpers.jenkinsBuildLink(jobUrl, candidate), "");
  }
  assert.equal(helpers.jenkinsState("NOT_EXECUTED").label, "未执行");
  assert.equal(helpers.jenkinsState("UNKNOWN").label, "待确认");
});

test("monitor opens with read-only calls and a late log reply cannot attach to a newer build", async t => {
  let latest = 7, oldLog;
  const h = harness((target, method, data, signal) => {
    if (target === `${path}/jenkins-monitor`) return bound;
    if (target === `${path}/jenkins-build`) return run(latest);
    if (target === `${path}/jenkins-build/log?number=7&offset=0`) return new Promise(resolve => { oldLog = { resolve, signal }; });
    if (target === `${path}/jenkins-build/log?number=8&offset=0`) return { number: 8, offset: 0, nextOffset: 9, text: "新构建输出", more: false };
    throw new Error(`unexpected request ${target}`);
  });
  t.after(h.close);
  await h.flow.loadMonitor();
  assert.deepEqual(h.calls.map(({ target, method }) => [target, method]), [[`${path}/jenkins-monitor`, "GET"], [`${path}/jenkins-build`, "GET"]]);
  h.flow.toggleLog();
  latest = 8;
  await h.flow.refreshStatus();
  assert.equal(oldLog.signal.aborted, true);
  oldLog.resolve({ number: 7, offset: 0, nextOffset: 8, text: "旧构建输出", more: false });
  await Promise.resolve();
  assert.equal(h.flow.log.value, "新构建输出");
});

test("queued builds keep previous stages distinct and unavailable stages stay unconfirmed", t => {
  const h = harness(() => { throw new Error("unexpected request"); });
  t.after(h.close);
  h.flow.build.value = {
    configured: true, status: "QUEUED", number: 17, stagesAvailable: true,
    stages: [{ name: "环境预检", status: "FAILURE" }, { name: "归档产物", status: "NOT_EXECUTED" }],
  };
  assert.equal(h.flow.queuedWithPrevious.value, true);
  assert.equal(h.flow.stageLabel.value, "环境预检");
  h.flow.build.value.stagesAvailable = false;
  h.flow.build.value.currentStage = "旧结果";
  assert.equal(h.flow.stageLabel.value, "阶段待确认");
});

test("binding preserves or replaces the token intentionally without reopening it as input", async t => {
  const h = harness((target, method) => target.endsWith("/jenkins-build") ? run(1) : { ...bound, version: method === "POST" ? "2" : "1" });
  t.after(h.close);
  await h.flow.loadMonitor();
  h.flow.editMonitor();
  assert.equal(h.flow.token.value, "");
  h.flow.username.value = "different-user";
  await h.flow.saveMonitor();
  assert.equal(h.calls.filter(call => call.method === "POST").length, 0);
  h.flow.username.value = "reader";
  await h.flow.saveMonitor();
  assert.deepEqual(h.calls.find(call => call.method === "POST").data, { expectedVersion: "1", softwareId: "instance", username: "reader", token: "" });
  assert.equal(h.flow.settingsOpen.value, false);
  assert.equal(h.flow.token.value, "");
});

const instance = { action: "qa", serviceInstanceId: "deployment", moduleId: "module", moduleName: "platform-api", environmentName: "test" };
const instancePath = `${path}/service-instances/deployment`;

test("QA pins its own queue and build instead of attaching a newer job's console", async t => {
  let queueRead = 0;
  const h = harness((target, method) => {
    if (target === `${instancePath}/jenkins-monitor`) return { ...bound, jobUrl };
    if (target === `${instancePath}/jenkins-build` && method === "GET") return run(34);
    if (target === `${instancePath}/jenkins-build` && method === "POST") return { status: "QUEUED", queueId: 202 };
    if (target.endsWith("?queueId=202")) return ++queueRead === 1 ? { configured: true, status: "QUEUED" } : { ...run(35), status: "RUNNING", building: true };
    if (target.endsWith("?number=35")) return run(35);
    if (target.endsWith("/log?number=35&offset=0")) return { number: 35, offset: 0, nextOffset: 5, text: "build", more: false };
    if (target.endsWith("/log?number=35&offset=5")) return { number: 35, offset: 5, nextOffset: 5, text: "", more: false };
    throw new Error(`unexpected ${target}`);
  }, instance);
  t.after(h.close);
  await h.flow.loadMonitor();
  assert.equal(h.calls.some(call => call.method === "POST"), false);
  await h.flow.startQa();
  assert.equal(h.flow.build.value.status, "QUEUED");
  assert.equal(h.flow.build.value.number, undefined);
  await h.flow.refreshStatus();
  await h.flow.refreshStatus();
  assert.equal(h.flow.log.value, "build");
  assert.equal(h.flow.build.value.number, 35);
  assert.equal(h.calls.some(call => call.target.includes("number=34")), false);
  assert.equal(h.calls.filter(call => call.method === "POST").length, 1);
});

test("uncertain QA submission reuses the request id after reopening, logs stay read-only", async t => {
  const stored = new Map(), ids = [];
  function handler(target, method, data) {
    if (target === `${instancePath}/jenkins-monitor`) return { ...bound, jobUrl };
    if (method === "POST") {
      ids.push(data.requestId);
      if (ids.length === 1) throw new Error("请求超时");
      return { status: "QUEUED", queueId: 102 };
    }
    if (target.includes("queueId=102")) return { configured: true, status: "QUEUED" };
    return { configured: true, status: "NEVER_BUILT" };
  }
  const first = harness(handler, instance, stored);
  await first.flow.loadMonitor();
  await first.flow.startQa();
  assert.ok(first.flow.submitError.value);
  first.close();
  const logs = harness(handler, { ...instance, action: "logs" }, stored);
  await logs.flow.loadMonitor();
  await logs.flow.startQa();
  assert.equal(ids.length, 1);
  logs.close();
  const reopened = harness(handler, instance, stored);
  t.after(reopened.close);
  await reopened.flow.loadMonitor();
  await reopened.flow.startQa();
  assert.match(ids[0], /^[0-9a-f-]{36}$/i);
  assert.equal(ids[0], ids[1]);
  assert.equal(reopened.flow.build.value.status, "QUEUED");
});

test("an instance's first QA job binding uses its own URL and token", async t => {
  const h = harness((target, method, data) => {
    if (method === "POST") return { ...bound, jobUrl: data.jobUrl };
    if (target.endsWith("jenkins-monitor")) return { ...bound, configured: false, hasToken: false, jobUrl: "", version: "0" };
    return run(5);
  }, instance);
  t.after(h.close);
  await h.flow.loadMonitor();
  h.flow.editMonitor();
  assert.equal(h.flow.jobDraft.value, "");
  h.flow.jobDraft.value = "https://example.org/";
  h.flow.token.value = "qa-example-test-token";
  await h.flow.saveMonitor();
  assert.equal(h.calls.some(call => call.method === "POST"), false);
  h.flow.jobDraft.value = jobUrl;
  await h.flow.saveMonitor();
  assert.deepEqual(h.calls.find(call => call.method === "POST").data, { expectedVersion: "0", softwareId: "instance", username: "reader", token: "qa-example-test-token", jobUrl });
});
