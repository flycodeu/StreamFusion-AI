import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import { computed, ref } from "vue";
import ts from "typescript";

// Exercise the component's real script with controlled requests and timers.
const component = await readFile(new URL("../src/HostPreparationPanel.vue", import.meta.url), "utf8");
const hostManagement = await readFile(new URL("../src/HostManagement.vue", import.meta.url), "utf8");
const hostOverview = await readFile(new URL("../src/HostOverview.vue", import.meta.url), "utf8");
const app = await readFile(new URL("../src/App.vue", import.meta.url), "utf8");
const source = component.split('<script setup lang="ts">')[1].split("</script>")[0]
  .replace(/^import .+;\r?$/gm, "");
const compiled = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None },
}).outputText;
const createPanel = new Function(
  "computed", "ref", "watch", "onUnmounted", "defineProps", "defineEmits",
  "ApiError", "request", "setTimeout", "clearTimeout", "fetch", "location", "document", "URL", "navigator",
  compiled + "\nreturn { data, busy, visibleStatus, currentPhaseIndex, statusTitle, statusSummary, transferPercent, transferSpeed, phaseState, error, sudoPassword, needsSudoPassword, canRecover, checkResult, updateRequested, openUpdate, load, install, repair, check, reconcile, download };",
);

function deferred() {
  let resolve, reject;
  const promise = new Promise((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}

class TestApiError extends Error {
  constructor(message, code, timestamp = "2026-10-29T01:01:00Z", traceId = "test-trace") {
    super(message);
    this.code = code;
    this.timestamp = timestamp;
    this.traceId = traceId;
  }
}

async function flush() {
  for (let i = 0; i < 8; i++) await Promise.resolve();
}

function preparation(id = "host-a", remoteJob) {
  return {
    hostId: id, expectedHostVersion: "1", account: "ubuntu", installRoot: "/opt/streamfusion-ops-target", remoteJob,
    package: { available: true, sha256: "b".repeat(64), fileName: `${id}.tar.gz`, downloadUrl: `/ops/api/v1/hosts/${id}/preparation/download?expectedVersion=1` },
  };
}

function harness(request, fetch = async () => Response.json({}), options = {}) {
  const props = { host: { id: "host-a", version: "1", kind: "SSH" }, updateOnly: options.updateOnly, automaticUpdate: options.automaticUpdate };
  const timers = new Map();
  const clicks = [];
  const emitted = [];
  let timerId = 0;
  let change, unmount;
  class TestURL extends URL {
    static createObjectURL() { return "blob:test-download"; }
    static revokeObjectURL() {}
  }
  const panel = createPanel(
    computed, ref,
    (_sources, callback) => { change = callback; callback(); },
    (callback) => { unmount = callback; },
    () => props, () => (name) => { if (name !== "closeBlocked") emitted.push(name); }, TestApiError, request,
    (callback, delay) => { const id = ++timerId; timers.set(id, { callback, delay }); return id; },
    (id) => timers.delete(id), fetch, { origin: "http://ops.test" },
    { createElement: () => ({ click() { clicks.push(this.download); } }) },
    TestURL, { clipboard: { writeText: async () => {} } },
  );
  return {
    panel, timers, clicks, emitted,
    changeHost(id) { props.host = { id, version: "1", kind: "SSH" }; change(); },
    unmount() { unmount(); },
    async tick() {
      const next = timers.entries().next().value;
      assert.ok(next, "expected a scheduled progress refresh");
      timers.delete(next[0]); next[1].callback(); await flush();
    },
  };
}

test("a stale host load cannot clear the current host's busy state", async () => {
  const old = deferred(), current = deferred();
  const h = harness((path) => path.includes("host-a") ? old.promise : current.promise);
  h.changeHost("host-b");
  old.resolve(preparation("host-a")); await flush();
  assert.equal(h.panel.busy.value, true);
  assert.equal(h.panel.data.value, undefined);
  current.resolve(preparation("host-b")); await flush();
  assert.equal(h.panel.busy.value, false);
  assert.equal(h.panel.data.value.hostId, "host-b");
});

test("a completed check from the previous host cannot emit or overwrite feedback", async () => {
  const check = deferred(), current = deferred();
  const h = harness((path, method) => method === "POST" ? check.promise : path.includes("host-b") ? current.promise : Promise.resolve(preparation()));
  await flush();
  const pending = h.panel.check();
  h.changeHost("host-b");
  check.resolve({ observedAt: "2026-10-29T01:00:00Z" }); await pending;
  assert.equal(h.panel.checkResult.value, undefined);
  assert.deepEqual(h.emitted, []);
  assert.equal(h.panel.busy.value, true);
  current.resolve(preparation("host-b")); await flush();
});

test("running progress retries transient failure and clears its temporary error on recovery", async () => {
  let calls = 0;
  const running = { id: "job-1", status: "RUNNING" };
  const h = harness(async () => {
    if (++calls === 2) throw new Error("暂时断线");
    return preparation("host-a", running);
  });
  await flush(); await h.tick();
  assert.match(h.panel.error.value.message, /重试/);
  assert.equal(h.timers.size, 1);
  await h.tick();
  assert.equal(h.panel.error.value, undefined);
  assert.equal(h.panel.data.value.remoteJob.status, "RUNNING");
  assert.equal(h.timers.size, 1);
  h.unmount();
});

test("progress failures stop after three attempts and manual refresh restores polling", async () => {
  let calls = 0;
  const h = harness(async () => {
    if (++calls > 1 && calls < 5) throw new Error("断线");
    return preparation("host-a", { id: "job-1", status: "RUNNING" });
  });
  await flush(); await h.tick(); await h.tick(); await h.tick();
  assert.equal(h.timers.size, 0);
  assert.match(h.panel.error.value.message, /刷新已暂停/);
  await h.panel.load();
  assert.equal(h.panel.error.value, undefined);
  assert.equal(h.timers.size, 1);
  h.unmount();
});

test("an install with a lost response reuses its idempotency key without retaining the sudo password", async () => {
  let reads = 0;
  const inputs = [];
  const h = harness(async (_path, method, input) => {
    if (method === "POST") { inputs.push({ ...input }); throw new Error("响应丢失"); }
    if (++reads > 1) throw new Error("状态读取失败");
    return preparation();
  });
  await flush();
  h.panel.sudoPassword.value = "temporary-password";
  await h.panel.install();
  assert.equal(h.panel.sudoPassword.value, "");
  assert.equal(h.panel.busy.value, false);
  await h.panel.install();
  assert.equal(inputs.length, 2);
  assert.equal(inputs[0].idempotencyKey, inputs[1].idempotencyKey);
  assert.equal(inputs[0].sudoPassword, "temporary-password");
  assert.equal(inputs[1].sudoPassword, "");
});

test("a confirmed terminal install permits a new install intent", async () => {
  const keys = [];
  let latest;
  const h = harness(async (_path, method, input) => {
    if (method === "POST") {
      keys.push(input.idempotencyKey);
      latest = { id: `job-${keys.length}`, status: "FAILED" };
      return latest;
    }
    return preparation("host-a", latest);
  });
  await flush();
  await h.panel.install();
  await h.panel.install();
  assert.equal(keys.length, 2);
  assert.notEqual(keys[0], keys[1]);
});

test("a successful historical connection offers an explicit update without auto installing", async () => {
  const mutations = [];
  const h = harness(async (path, method, input) => {
    if (path.endsWith("/checks")) return { capabilities: {} };
    if (method === "POST") { mutations.push({ path, input: { ...input } }); return { id: "update-job", status: "SUCCEEDED" }; }
    return preparation("host-a", { id: "old-success", status: "SUCCEEDED" });
  });
  await flush();
  assert.equal(mutations.length, 0);
  assert.equal(h.panel.data.value.remoteJob.id, "old-success");
  h.panel.openUpdate();
  assert.equal(h.panel.updateRequested.value, true);
  assert.equal(mutations.length, 0);
  await h.panel.install();
  assert.equal(mutations.length, 1);
  assert.equal(mutations[0].path, "/hosts/host-a/preparation/install");
  assert.equal(h.panel.updateRequested.value, false);
});

test("inline update requires confirmation and emits completion only for the explicitly updated component", async () => {
  let latest = { id: "old-success", status: "SUCCEEDED" };
  const installs = [];
  const h = harness(async (path, method, input) => {
    if (path.endsWith("/checks")) return { capabilities: {} };
    if (path.endsWith("/preparation/install")) {
      installs.push({ ...input });
      latest = { id: "new-update", status: "SUCCEEDED" };
      return latest;
    }
    return preparation("host-a", latest);
  }, undefined, { updateOnly: true });
  await flush();
  assert.equal(h.panel.updateRequested.value, true);
  assert.equal(installs.length, 0);
  assert.equal(h.emitted.includes("updated"), false);
  await h.panel.install();
  assert.equal(installs.length, 1);
  assert.equal(h.emitted.filter((event) => event === "updated").length, 1);
  await h.panel.load();
  assert.equal(h.emitted.filter((event) => event === "updated").length, 1);
  h.unmount();
});

test("an update completion from a previous host cannot close the current inline flow", async () => {
  const check = deferred();
  let latest = { id: "old-success", status: "SUCCEEDED" }, checks = 0;
  const h = harness(async (path) => {
    if (path.endsWith("/checks")) return ++checks === 1 ? {} : check.promise;
    if (path.endsWith("/preparation/install")) return latest = { id: "new-update", status: "SUCCEEDED" };
    return preparation(path.includes("host-b") ? "host-b" : "host-a", latest);
  }, undefined, { updateOnly: true });
  await flush();
  const pending = h.panel.install();
  await flush();
  h.changeHost("host-b");
  check.resolve({});
  await pending;
  await flush();
  assert.equal(h.emitted.includes("updated"), false);
  h.unmount();
});

test("download uses its initial file name and ignores completion after a host switch", async () => {
  const downloaded = deferred();
  const h = harness(async () => preparation(), () => downloaded.promise);
  await flush();
  const pending = h.panel.download();
  h.panel.data.value.package.fileName = "changed-name.tar.gz";
  downloaded.resolve(new Response("archive")); await pending;
  assert.deepEqual(h.clicks, ["host-a.tar.gz"]);

  const stale = deferred();
  const current = deferred();
  const h2 = harness((path) => path.includes("host-b") ? current.promise : Promise.resolve(preparation()), () => stale.promise);
  await flush();
  const staleDownload = h2.panel.download();
  h2.changeHost("host-b");
  stale.resolve(new Response("archive")); await staleDownload;
  assert.deepEqual(h2.clicks, []);
  assert.equal(h2.panel.busy.value, true);
  current.resolve(preparation("host-b")); await flush();
});

test("offline check reports executor and authorization readiness separately from SSH connectivity", async () => {
  const available = { status: "AVAILABLE", code: "OK", message: "可用" };
  const checked = {
    observedAt: "2026-10-29T01:00:00Z",
    capabilities: { reachability: available, readOnly: available, mutation: { status: "UNAVAILABLE", code: "EXECUTOR_NOT_FOUND", message: "请先安装执行器" } },
  };
  const h = harness(async (_path, method) => method === "POST" ? checked : preparation());
  await flush(); await h.panel.check();
  assert.equal(h.panel.checkResult.value.ready, false);
  assert.equal(h.panel.checkResult.value.code, "EXECUTOR_NOT_FOUND");
  assert.equal(h.panel.checkResult.value.message, "请先安装执行器");
  checked.capabilities.mutation = available;
  await h.panel.check();
  assert.equal(h.panel.checkResult.value.ready, true);
  assert.match(h.panel.checkResult.value.message, /继续安装和管理软件/);
});

test("unknown reconciliation gives a concise path problem without exposing codes", async () => {
  const job = { id: "job-unknown", status: "UNKNOWN", phase: "INSTALLING_EXECUTOR" };
  const h = harness(async (_path, method) => {
    if (method === "POST") {
      throw new TestApiError(
        "接入目录不满足安全要求，请让服务器管理员检查符号链接、所有者和写入权限。",
        "EXECUTOR_PATH_UNSAFE",
      );
    }
    return preparation("host-a", job);
  });
  await flush();
  await h.panel.reconcile();
  assert.equal(h.panel.checkResult.value.title, "接入目录权限不符合要求");
  assert.match(h.panel.checkResult.value.message, /点击下方按钮修复/);
  assert.equal(h.panel.checkResult.value.code, "EXECUTOR_PATH_UNSAFE");
  assert.equal(h.panel.error.value, undefined);
});

test("permission repair uses one transient sudo input and refreshes actual host readiness", async () => {
  let job = { id: "job-unknown", status: "UNKNOWN", phase: "INSTALLING_EXECUTOR" };
  const repairs = [];
  let checks = 0;
  const h = harness(async (path, method, input) => {
    if (path.endsWith("/checks")) { checks++; return {}; }
    if (method === "POST" && path.endsWith("/preparation/repair")) {
      repairs.push({ ...input });
      job = { ...job, status: "SUCCEEDED" }; return job;
    }
    return preparation("host-a", job);
  });
  await flush();
  h.panel.checkResult.value = {
    ready: false,
    title: "接入目录权限不符合要求",
    message: "现有安装目录未通过安全检查。",
    code: "EXECUTOR_PATH_UNSAFE",
  };
  h.panel.sudoPassword.value = "one-use-sudo-password";
  await h.panel.repair();
  assert.equal(h.panel.sudoPassword.value, "");
  assert.deepEqual(repairs, [{
    jobId: "job-unknown",
    expectedVersion: "1",
    confirmed: true,
    sudoPassword: "one-use-sudo-password",
  }]);
  assert.equal(h.panel.checkResult.value, undefined);
  assert.equal(h.panel.data.value.remoteJob.status, "SUCCEEDED");
  assert.equal(checks, 1);
});

test("operator-facing preparation is a single concise install flow", () => {
  for (const text of [
    "控制器没有收到可信的最终回执",
    "执行器或授权尚未就绪，请保留原目录",
    "请先确认当前页面状态",
    "安装期间请保持此页面打开",
    "任务编号",
    "状态代码",
    "请求标识",
    "技术详情",
    "接入检查与高级维护",
  ]) {
    assert.equal(component.includes(text), false, text);
  }
  assert.match(component, /安装位置/);
  assert.match(component, /开始安装/);
  assert.match(component, /检查安装结果/);
  assert.match(component, /bash install\.sh/);
  const ordinaryContent = component.replace(/<details[\s\S]*?<\/details>/g, "");
  assert.equal(ordinaryContent.includes("{{ error.code }}"), false);
  assert.match(component, /<details v-if="error.code"><summary>错误详情<\/summary>/);
  assert.match(component, /<ErrorHelpLink :code="error.code"/);
  assert.equal(component.includes("{{ checkResult.code }}"), false);
  assert.equal(hostManagement.includes("<code>{{ detail.capabilities?.[key]?.code }}</code>"), false);
  assert.equal(hostOverview.includes("<code>{{ current.latestAttempt.code }}</code>"), false);
  assert.match(component, /修复权限并继续安装/);
  assert.match(component, /用于 sudo 提权，仅本次使用/);
  assert.equal(component.includes("Ops 会核对接入归属和可信摘要，只修复固定控制文件的权限"), false);
  assert.match(app, /HOST_PREPARATION_REPAIR_ACCEPTED: "开始修复接入权限"/);
  assert.match(app, /HOST_PREPARATION_REPAIR_RESULT: "接入权限修复结果"/);
});

test("missing executor can recover the original unknown installation", async () => {
  let latest = { id: "job-missing", status: "UNKNOWN" };
  const repairs = [];
  const h = harness(async (path, method, input) => {
    if (method === "POST" && path.endsWith("/reconcile")) throw new TestApiError("安装目录不存在", "EXECUTOR_MISSING");
    if (method === "POST" && path.endsWith("/repair")) {
      repairs.push({ ...input }); latest = { id: "job-missing", status: "SUCCEEDED" }; return latest;
    }
    return preparation("host-a", latest);
  });
  await flush(); await h.panel.reconcile();
  assert.equal(h.panel.checkResult.value.title, "接入组件尚未安装");
  await h.panel.repair();
  assert.equal(repairs.length, 1);
  assert.equal(repairs[0].jobId, "job-missing");
  assert.equal(h.panel.data.value.remoteJob.status, "SUCCEEDED");
  assert.equal(h.panel.checkResult.value, undefined);
  assert.match(component, /恢复接入并继续安装/);
});

test("sudo password appears only after the server requires it, and repair stays available", async () => {
  const job = { id: "job-unknown", status: "UNKNOWN" };
  const passwords = [];
  const h = harness(async (path, method, input) => {
    if (method === "POST" && path.endsWith("/repair")) {
      passwords.push(input.sudoPassword);
      throw new TestApiError("请输入当前 SSH 账号的系统密码", "PREPARATION_SUDO_REQUIRED");
    }
    return preparation("host-a", job);
  });
  await flush();
  assert.equal(h.panel.needsSudoPassword.value, false);
  h.panel.checkResult.value = { ready: false, code: "EXECUTOR_MISSING" };
  await h.panel.repair();
  assert.equal(h.panel.needsSudoPassword.value, true);
  assert.equal(h.panel.canRecover.value, true);
  h.panel.sudoPassword.value = "current-account-password";
  await h.panel.repair();
  assert.deepEqual(passwords, ["", "current-account-password"]);
  assert.equal(h.panel.sudoPassword.value, "");
});

test("opening a completed installation refreshes stale host readiness once", async () => {
  let checks = 0;
  const h = harness(async (path) => {
    if (path.endsWith("/checks")) { checks++; return {}; }
    return preparation("host-a", { id: "job-ready", status: "SUCCEEDED" });
  });
  await flush();
  assert.equal(checks, 1);
  assert.deepEqual(h.emitted, ["checked"]);
  await h.panel.load();
  assert.equal(checks, 1);
});

test("successful reconciliation clears the previous failure and refreshes host readiness", async () => {
  let job = { id: "job-unknown", status: "UNKNOWN" };
  let checks = 0;
  const h = harness(async (path) => {
    if (path.endsWith("/checks")) { checks++; return {}; }
    if (path.endsWith("/reconcile")) { job = { ...job, status: "SUCCEEDED" }; return job; }
    return preparation("host-a", job);
  });
  await flush();
  h.panel.checkResult.value = { ready: false, code: "EXECUTOR_RESPONSE_INVALID" };
  await h.panel.reconcile();
  assert.equal(h.panel.checkResult.value, undefined);
  assert.equal(h.panel.data.value.remoteJob.status, "SUCCEEDED");
  assert.equal(checks, 1);
});

const automaticUpdate = { hostId: "host-a", expectedHostVersion: "1", currentSHA256: "a".repeat(64), packageSHA256: "b".repeat(64) };

test("approved automatic update sends frozen hashes once and resumes only its completed job", async () => {
  let latest = { id: "old", status: "SUCCEEDED" };
  const installs = [];
  const h = harness(async (path, method, input) => {
    if (path.endsWith("/checks")) return {};
    if (path.endsWith("/install")) { installs.push({ ...input }); return latest = { id: "auto", status: "SUCCEEDED" }; }
    return preparation("host-a", latest);
  }, undefined, { updateOnly: true, automaticUpdate });
  await flush(); await flush();
  assert.equal(installs.length, 1);
  assert.equal(installs[0].updateOnly, true);
  assert.equal(installs[0].expectedCurrentSHA256, automaticUpdate.currentSHA256);
  assert.equal(installs[0].expectedPackageSHA256, automaticUpdate.packageSHA256);
  assert.equal(installs[0].sudoPassword, "");
  assert.equal(h.emitted.filter((name) => name === "updated").length, 1);
  await h.panel.load(); await flush();
  assert.equal(installs.length, 1);
  h.unmount();
});

test("automatic update stops for sudo and uses a fresh key only after explicit password input", async () => {
  let latest = { id: "old", status: "SUCCEEDED" };
  const installs = [];
  const h = harness(async (path, method, input) => {
    if (path.endsWith("/checks")) return {};
    if (path.endsWith("/install")) {
      installs.push({ ...input });
      return latest = input.sudoPassword ? { id: "done", status: "SUCCEEDED" } : { id: "sudo", status: "FAILED", errorCode: "PREPARATION_SUDO_REQUIRED" };
    }
    return preparation("host-a", latest);
  }, undefined, { updateOnly: true, automaticUpdate });
  await flush(); await flush();
  assert.equal(installs.length, 1);
  assert.equal(h.panel.needsSudoPassword.value, true);
  assert.equal(h.emitted.includes("updated"), false);
  await h.panel.load(); assert.equal(installs.length, 1);
  h.panel.sudoPassword.value = "transient-password";
  await h.panel.install();
  assert.equal(installs.length, 2);
  assert.notEqual(installs[0].idempotencyKey, installs[1].idempotencyKey);
  assert.equal(h.panel.sudoPassword.value, "");
  assert.equal(h.emitted.filter((name) => name === "updated").length, 1);
  h.unmount();
});

test("automatic update refuses changed materials, another host and unresolved jobs", async () => {
  for (const mode of ["material", "host", "unknown"]) {
    const calls = [];
    const h = harness(async (path, method) => {
      calls.push({path,method});
      const value = preparation("host-a", { id: "old", status: mode === "unknown" ? "UNKNOWN" : "SUCCEEDED" });
      if (mode === "material") value.package.sha256 = "c".repeat(64);
      if (mode === "host") value.hostId = "host-b";
      return value;
    }, undefined, { updateOnly: true, automaticUpdate });
    await flush(); await flush();
    assert.equal(calls.some((item) => item.path.endsWith("/install")), false, mode);
    assert.equal(h.emitted.includes("updated"), false, mode);
    h.unmount();
  }
});

test("confirming an unknown automatic update resumes once without replaying installation", async () => {
 let latest={id:"old",status:"SUCCEEDED"};let installs=0,reconciles=0;
 const h=harness(async(path)=>{
  if(path.endsWith("/checks"))return {};
  if(path.endsWith("/install")){installs++;return latest={id:"unknown-auto",status:"UNKNOWN"};}
  if(path.endsWith("/reconcile")){reconciles++;return latest={id:"unknown-auto",status:"SUCCEEDED"};}
  return preparation("host-a",latest);
 },undefined,{updateOnly:true,automaticUpdate});
 await flush();await flush();
 assert.equal(installs,1);assert.equal(h.emitted.includes("updated"),false);
 await h.panel.reconcile();
 assert.equal(installs,1);assert.equal(reconciles,1);
 assert.equal(h.panel.updateRequested.value,false);
 assert.equal(h.emitted.filter(x=>x==="updated").length,1);
 await h.panel.load();assert.equal(installs,1);
 h.unmount();
});

test("update progress reacts to polled stages before the install POST completes", async () => {
  const submitted = deferred();
  let latest = { id: "old-success", status: "SUCCEEDED", phase: "VERIFYING_EXECUTOR" };
  let postCompleted = false;
  const h = harness(async (path) => {
    if (path.endsWith("/install")) { const result = await submitted.promise; postCompleted = true; return result; }
    if (path.endsWith("/checks")) return {};
    return preparation("host-a", latest);
  }, undefined, { updateOnly: true });
  await flush();
  const pending = h.panel.install();
  assert.equal(h.panel.visibleStatus.value, "RUNNING");
  assert.equal(h.panel.currentPhaseIndex.value, 0);
  assert.equal(h.panel.statusTitle.value, "准备更新接入组件");
  latest = { id: "new-update", status: "RUNNING", phase: "UPLOADING_EXECUTOR" };
  await h.tick();
  assert.equal(postCompleted, false);
  assert.equal(h.panel.currentPhaseIndex.value, 1);
  assert.equal(h.panel.statusTitle.value, "传输接入组件");
  assert.equal(h.panel.phaseState(0), "done");
  assert.equal(h.panel.phaseState(1), "active");
  latest = { ...latest, phase: "INSTALLING_EXECUTOR" };
  await h.tick();
  assert.equal(h.panel.currentPhaseIndex.value, 2);
  latest = { ...latest, status: "UNKNOWN" };
  await h.tick();
  assert.equal(h.panel.visibleStatus.value, "UNKNOWN");
  assert.equal(h.panel.phaseState(2), "uncertain");
  latest = { ...latest, status: "SUCCEEDED", phase: "VERIFYING_EXECUTOR" };
  await h.tick();
  assert.equal(postCompleted, false);
  assert.equal(h.panel.visibleStatus.value, "SUCCEEDED");
  assert.equal(h.panel.currentPhaseIndex.value, 3);
  assert.equal(h.panel.phaseState(3), "done");
  submitted.resolve(latest); await pending;
  h.unmount();
});


test("upload failure after sudo retry keeps the real cause and transfer progress", async () => {
  let latest = {id: "sudo", status: "FAILED", phase: "CONNECTING", errorCode: "PREPARATION_SUDO_REQUIRED"};
  const h = harness(async () => preparation("host-a", latest), undefined, {updateOnly: true});
  await flush();
  assert.equal(h.panel.needsSudoPassword.value, true);
  latest = {id: "upload", status: "FAILED", phase: "UPLOADING_EXECUTOR", errorCode: "PREPARATION_UPLOAD_TIMEOUT", result: {message: "组件上传超时", transfer: {sentBytes: 1048576, totalBytes: 4194304, startedAt: "2026-11-02T00:00:00Z", updatedAt: "2026-11-02T00:00:10Z"}}};
  await h.panel.load();
  assert.equal(h.panel.statusTitle.value, "组件更新未完成");
  assert.equal(h.panel.statusSummary.value, "组件上传超时");
  assert.equal(h.panel.transferPercent.value, 25);
  assert.equal(h.panel.transferSpeed.value, " · 102.4 KiB/s");
  h.unmount();
});
