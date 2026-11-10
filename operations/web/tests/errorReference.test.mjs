import assert from "node:assert/strict";
import { readFile, readdir } from "node:fs/promises";
import { test } from "node:test";
import ts from "typescript";
import * as vue from "vue";

function compile(source, imports = {}) {
  const module = { exports: {} };
  const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText;
  new Function("require", "module", "exports", compiled)((name) => imports[name], module, module.exports);
  return module.exports;
}
const reference = compile(await readFile(new URL("../src/errorReference.ts", import.meta.url), "utf8"));
const help = compile(await readFile(new URL("../src/errorHelp.ts", import.meta.url), "utf8"), { vue });

test("error lookup accepts exact codes and Chinese search without guessing unknown causes", () => {
  assert.equal(reference.findErrorReference(" port_observation_denied ")?.name, "监听端口读取受限");
  assert.equal(reference.findErrorReference("PORT_UNKNOWN_NEW_CODE"), undefined);
  assert.equal(reference.searchErrorReference("PORT_UNKNOWN_NEW_CODE").length, 0);
  assert.ok(reference.searchErrorReference("来源 未确认").some((entry) => entry.code === "OFFICIAL_PACKAGE_ORIGIN_UNAVAILABLE"));
  assert.ok(reference.searchErrorReference("包状态").some((entry) => entry.code === "OFFICIAL_PACKAGE_STATE_UNAVAILABLE"));
  assert.equal(reference.searchErrorReference(" ").length, reference.errorReference.length);
});

test("error guidance distinguishes observation failures from confirmed package transactions", () => {
  const port = reference.findErrorReference("PORT_OBSERVATION_DENIED");
  const packages = reference.findErrorReference("OFFICIAL_PACKAGE_STATE_UNAVAILABLE");
  const pending = reference.findErrorReference("DOCKER_PACKAGE_TRANSACTION_PENDING");
  assert.match(port.meaning, /不能认定是 sudo 权限问题/);
  assert.match(packages.meaning, /不代表一定需要修复包事务/);
  assert.match(pending.meaning, /发现未完成/);
  assert.ok(packages.steps.some((step) => step.includes("dpkg --audit")));
  assert.ok(packages.steps.some((step) => step.includes("traceId")));
});

test("official path guidance distinguishes standard system links from existing packages and files", () => {
  const paths = reference.findErrorReference("OFFICIAL_PATH_NOT_PROTECTED");
  const packages = reference.findErrorReference("OFFICIAL_PACKAGE_ALREADY_INSTALLED");
  const existing = reference.findErrorReference("OFFICIAL_EXISTING_CONFIGURATION_OR_DATA");
  assert.ok(reference.searchErrorReference("标准 /lib").includes(paths));
  assert.match(paths.meaning, /旧版执行器会误拒绝/);
  assert.ok(paths.steps.some((step) => step.includes("namei -l")));
  assert.ok(paths.steps.some((step) => step.includes("root 所有") && step.includes("精确为 usr/lib 或 /usr/lib") && step.includes("实际目标目录仍须通过保护检查")));
  assert.match(packages.meaning, /不代表整套服务已经安装完成或正在运行/);
  assert.match(existing.meaning, /无法确认该位置不存在/);
  assert.ok(existing.steps.some((step) => step.includes("具体路径") && step.includes("traceId")));
  for (const entry of [paths, packages, existing]) {
    assert.doesNotMatch(entry.steps.join(" "), /\b(?:chmod|chown|rm|unlink)\b/);
  }
});

test("error help opens a local reference without navigating or changing page data", () => {
  help.showErrorHelp(" OFFICIAL_PACKAGE_STATE_UNAVAILABLE ");
  assert.deepEqual({ ...help.errorHelp }, { open: true, code: "OFFICIAL_PACKAGE_STATE_UNAVAILABLE" });
  help.errorHelp.open = false;
  help.showErrorHelp("NEW_UNLISTED_CODE");
  assert.deepEqual({ ...help.errorHelp }, { open: true, code: "NEW_UNLISTED_CODE" });
});

test("error guidance points to current configuration, log and task recovery entries", () => {
  const configuration = reference.findErrorReference("COMMAND_STEP_INVALID");
  const failedStep = reference.findErrorReference("COMMAND_STEP_FAILED");
  const pendingStep = reference.findErrorReference("COMMAND_STEP_RECONCILE_REQUIRED");
  const docker = reference.findErrorReference("DOCKER_NOT_READY");
  assert.equal(configuration.category, "软件配置");
  assert.match(configuration.steps.join(" "), /软件配置/);
  assert.match(failedStep.steps.join(" "), /日志记录 → 执行记录/);
  assert.match(pendingStep.steps.join(" "), /软件安装.*查看进度/);
  assert.match(docker.steps.join(" "), /软件安装.*Docker Engine/);
  const instructions = reference.errorReference.flatMap(entry => entry.steps).join(" ");
  assert.doesNotMatch(instructions, /在安装配置中|重新打开安装配置|在原安装记录中|软件管理的安装入口|或 UNKNOWN 作业/);
});

async function goSources(directory) {
  let text = "";
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const file = new URL(entry.name + (entry.isDirectory() ? "/" : ""), directory);
    if (entry.isDirectory()) text += await goSources(file);
    else if (entry.name.endsWith(".go") && !entry.name.endsWith("_test.go")) text += await readFile(file, "utf8");
  }
  return text;
}

test("every listed code exists in production source and has its own checks", async () => {
  const source = await goSources(new URL("../../internal/", import.meta.url));
  const codes = new Set();
  for (const entry of reference.errorReference) {
    assert.ok(!codes.has(entry.code), `duplicate ${entry.code}`);
    codes.add(entry.code);
    const mappedPackageCode = entry.code.startsWith("OFFICIAL_PACKAGE_")
      && source.includes('return "OFFICIAL_" + strings.TrimPrefix(err.Error(), "DOCKER_")')
      && source.includes(`"${entry.code.replace("OFFICIAL_", "DOCKER_")}"`);
    assert.ok(source.includes(`"${entry.code}"`) || mappedPackageCode, `unimplemented code ${entry.code}`);
    assert.ok(entry.name && entry.meaning && entry.steps.length >= 1, entry.code);
  }
});
