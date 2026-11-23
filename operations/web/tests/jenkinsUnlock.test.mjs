import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import ts from "typescript";
import * as vue from "vue";

const compile = source => ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;

async function load(name, imports = {}) {
  const source = await readFile(new URL(`../src/${name}.ts`, import.meta.url), "utf8");
  const exports = {};
  new Function("require", "exports", compile(source))(key => imports[key], exports);
  return exports;
}

const helpers = await load("jenkinsUnlock", {
  "./officialInstallation": await load("officialInstallation"),
  "./webAccess": await load("webAccess"),
});
const component = (await readFile(new URL("../src/JenkinsUnlockPassword.vue", import.meta.url), "utf8"))
  .split('<script setup lang="ts">')[1].split("</script>")[0];
const controller = new Function("require", "exports", "defineProps", "withDefaults", "navigator", "setTimeout", "clearTimeout", compile(
  component + "\nexport { open, busy, currentPassword, error, diagnostic, fallbackCommand, waitingForCopy, showDialog, verifyAndCopy, copyPassword };",
));

const id = "00000000-0000-0000-0000-000000000001";
const item = {
  id, version: "3", installationStatus: "READY", managementMode: "MANAGED", actualState: "RUNNING", runtimeKind: "DOCKER",
  locator: "jenkins-test", installationPaths: { data: "/opt/jenkins fixture/data" },
  installation: { definition: { key: "custom-jenkins-docker-online", catalogKey: "jenkins" } },
};

const fixtureTraceId = "a".repeat(32);
class ApiError extends Error {
  constructor(message, code = message, traceId = fixtureTraceId, timestamp = "") {
    super(message === code ? "raw response must stay hidden" : message);
    this.code = code;
    this.traceId = traceId;
    this.timestamp = timestamp;
  }
}

function flow({ instance = item, request, copy } = {}) {
  const props = vue.reactive({ instanceId: id, instance, active: true });
  const scope = vue.effectScope();
  const exports = {};
  const messages = [];
  let timer;
  let cleanup;
  scope.run(() => controller(key => ({
    vue: { ...vue, onUnmounted: fn => { cleanup = fn; } },
    "element-plus": { ElMessage: { success: value => messages.push(value), info: value => messages.push(value) } },
    "./api": { request, ApiError },
    "./jenkinsUnlock": helpers,
  }[key]), exports, () => props, value => value, { clipboard: { writeText: copy } },
  fn => { timer = fn; return 1; }, () => { timer = undefined; }));
  return { ...exports, props, messages, expire: () => timer?.(), close: () => { cleanup(); scope.stop(); } };
}

test("on-demand Jenkins unlock verifies the selected version and copies directly without rendering the returned password", async () => {
  const calls = [];
  const clipboard = [];
  const response = { password: "a".repeat(32) };
  const screen = flow({ request: async (path, method, data) => { calls.push([path, method, { ...data }]); return response; },
    copy: async value => { clipboard.push(value); } });
  screen.showDialog();
  assert.deepEqual(calls, []);
  screen.currentPassword.value = "ops-test-reauth";
  await screen.verifyAndCopy();
  assert.deepEqual(calls, [[`/software-instances/${id}/jenkins-unlock`, "POST", { password: "ops-test-reauth", expectedVersion: "3" }]]);
  assert.deepEqual(clipboard, ["a".repeat(32)]);
  assert.equal(response.password, "");
  assert.equal(screen.open.value, false);
  assert.equal(screen.waitingForCopy.value, false);
  assert.equal(screen.currentPassword.value, "");
  assert.equal(screen.messages.length, 1);
  screen.close();
});

test("a completed install loads the exact instance only after submission, and clipboard retry expires", async () => {
  const calls = [];
  const screen = flow({ instance: null, request: async (path, method, data) => {
    calls.push([path, method, data && { ...data }]);
    return method === "GET" ? item : { password: "a".repeat(32) };
  }, copy: async () => { throw new Error("Clipboard permission denied"); } });
  screen.showDialog();
  assert.deepEqual(calls, []);
  screen.currentPassword.value = "ops-test-reauth";
  await screen.verifyAndCopy();
  assert.equal(calls[0][0], `/software-instances/${id}`);
  assert.deepEqual(calls[1][2], { password: "ops-test-reauth", expectedVersion: "3" });
  assert.equal(screen.waitingForCopy.value, true);
  assert.equal(screen.currentPassword.value, "");
  assert.match(screen.error.value, /无法写入剪贴板/);
  assert.doesNotMatch(screen.error.value, /a{32}/);
  screen.expire();
  assert.equal(screen.waitingForCopy.value, false);
  assert.match(screen.error.value, /已过期/);
  screen.close();
});

test("an instance change aborts the request and ignores a late response", async () => {
  let resolve;
  let signal;
  let writes = 0;
  const screen = flow({ request: async (_, _method, _data, abortSignal) => {
    signal = abortSignal;
    return new Promise(callback => { resolve = callback; });
  }, copy: async () => { writes++; } });
  screen.showDialog();
  screen.currentPassword.value = "password";
  const pending = screen.verifyAndCopy();
  screen.props.instanceId = "another-instance";
  assert.equal(signal.aborted, true);
  resolve({ password: "a".repeat(32) });
  await pending;
  assert.equal(writes, 0);
  assert.equal(screen.open.value, false);
  assert.equal(screen.currentPassword.value, "");
  screen.close();
});

test("the legacy host command is shown only when the executor does not support the secure read", async () => {
  let code = "JENKINS_UNLOCK_UNSUPPORTED";
  const screen = flow({ request: async () => { throw new ApiError(code); } });
  screen.showDialog();
  screen.currentPassword.value = "password";
  await screen.verifyAndCopy();
  assert.equal(screen.fallbackCommand.value, "sudo cat -- '/opt/jenkins fixture/data/secrets/initialAdminPassword'");
  assert.doesNotMatch(screen.error.value, /raw response/);
  code = "JENKINS_UNLOCK_SETUP_INACTIVE";
  screen.currentPassword.value = "password";
  await screen.verifyAndCopy();
  assert.equal(screen.fallbackCommand.value, "");
  assert.equal(screen.open.value, false);
  assert.match(screen.messages[0], /无需首次解锁/);
  screen.showDialog();
  assert.equal(screen.open.value, false);
  screen.close();
});

test("invalid Ops confirmation input never sends a request or touches the clipboard", async () => {
  const unexpected = () => assert.fail("no request or clipboard write should occur");
  const screen = flow({ request: unexpected, copy: unexpected });
  screen.showDialog();
  screen.currentPassword.value = "测".repeat(22);
  await screen.verifyAndCopy();
  assert.match(screen.error.value, /Ops 管理员的登录密码.*64 字节/);
  assert.equal(screen.currentPassword.value, "");
  assert.equal(screen.waitingForCopy.value, false);
  assert.equal(screen.messages.length, 0);
  assert.equal(helpers.isOpsUnlockConfirmationPassword("测".repeat(21) + "a"), true);
  screen.close();
});

test("failed or malformed unlock responses leave clipboard unchanged and report safe diagnostics", async () => {
  let failure = "SECURE_REQUEST_INVALID";
  const response = { password: "z".repeat(32) };
  const screen = flow({
    request: async () => {
      if (failure) throw new ApiError(failure);
      return response;
    },
    copy: () => assert.fail("failed reads must leave the clipboard unchanged"),
  });
  screen.showDialog();
  screen.currentPassword.value = "ops-test-reauth";
  await screen.verifyAndCopy();
  assert.match(screen.error.value, /刷新 Ops 页面/);
  assert.equal(screen.diagnostic.value.code, failure);
  assert.equal(screen.diagnostic.value.traceId, fixtureTraceId);
  assert.doesNotMatch(screen.diagnostic.value.message, /raw response/);
  failure = "";
  screen.currentPassword.value = "ops-test-reauth";
  await screen.verifyAndCopy();
  assert.match(screen.error.value, /格式异常.*未复制/);
  assert.equal(screen.diagnostic.value, null);
  assert.equal(screen.waitingForCopy.value, false);
  assert.equal(screen.messages.length, 0);
  assert.equal(response.password, "");
  screen.close();
});
