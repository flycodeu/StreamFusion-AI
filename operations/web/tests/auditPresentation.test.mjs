import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import ts from "typescript";
const app = await readFile(new URL("../src/App.vue", import.meta.url), "utf8");
const source = app.slice(app.indexOf("const actionLabels:"), app.indexOf("function report("));
const compiled = ts.transpileModule(source + "\nreturn { auditResult };", { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
const { auditResult } = new Function(compiled)();
test("audit displays successful worker receipts as success and unknown as unconfirmed", () => {
  for (const action of ["HOST_PREPARATION_RESULT", "SOFTWARE_INSTALL_RESULT", "NODE_ENVIRONMENT_CHANGE_RESULT"]) {
    assert.deepEqual(auditResult(action, "SUCCEEDED"), { label: "成功", tone: "state-success" });
    assert.deepEqual(auditResult(action, "UNKNOWN"), { label: "待确认", tone: "state-muted" });
    assert.deepEqual(auditResult(action, "FAILED"), { label: "失败", tone: "state-error" });
  }
  assert.equal(auditResult("PLUGIN_VERIFIED", "READY").label, "成功");
  assert.equal(auditResult("HOST_PORTS_OBSERVED", "").label, "成功");
  assert.equal(auditResult("NEW_ACTION", "NEW_RESULT").label, "待核对");
  assert.equal(auditResult("LOGIN_FAILED", "AUTH_FAILED").label, "失败");
  assert.equal(auditResult("GIT_REPOSITORY_TEST", "GIT_REMOTE_READABLE").label, "成功");
  assert.equal(auditResult("GIT_REPOSITORY_TEST", "GIT_REMOTE_READ_FAILED").label, "待核对");
});
test("discovery audits distinguish complete and partial observations without guessing unknown result formats", () => {
  assert.equal(auditResult("HOST_SOFTWARE_DISCOVERED", "SUCCESS candidates=2 unavailable=0 limited=false").label, "成功");
  assert.equal(auditResult("HOST_SOFTWARE_DISCOVERED", "SUCCESS candidates=2 unavailable=1 limited=false").label, "部分未确认");
  assert.equal(auditResult("HOST_SOFTWARE_DISCOVERED", "SUCCESS candidates=2 unavailable=0 limited=true").label, "部分未确认");
  assert.equal(auditResult("HOST_SOFTWARE_DISCOVERED", "SUCCESS new-format").label, "待核对");
});
