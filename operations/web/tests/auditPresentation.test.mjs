import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";
const app = await readFile(new URL("../src/App.vue", import.meta.url), "utf8");
const banner = await readFile(new URL("../src/OpsDiagnosticBanner.vue", import.meta.url), "utf8");
const overview = await readFile(new URL("../src/HostOverview.vue", import.meta.url), "utf8");
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
test("diagnostic banner exposes expandable error code, timestamp, traceId and error reference link", () => {
  assert.match(banner, /查看诊断信息/);
  assert.match(banner, /diagnostic\.code/);
  assert.match(banner, /diagnostic\.traceId/);
  assert.match(banner, /diagnostic\.timestamp/);
  assert.match(banner, /<ErrorHelpLink/);
});
test("host overview aborts in-flight observation and paged software reads on unmount", async () => {
  const script = ts.transpileModule(
    overview.split('<script setup lang="ts">')[1].split("</script>")[0].replace(/^import[\s\S]*?;\r?$/gm, ""),
    { compilerOptions: { module: ts.ModuleKind.None, target: ts.ScriptTarget.ES2022 } },
  ).outputText;
  let unmount;
  const signals = [];
  const createOverview = new Function(
    "computed", "onMounted", "onUnmounted", "ref", "defineProps", "defineEmits", "request", "requestAllPages", "ElMessage", "metricSnapshot", "live", "softwareIsHistoryOnly",
    script + "\nreturn { refresh, loading };",
  );
  const scope = vue.effectScope();
  const panel = scope.run(() =>
    createOverview(
      vue.computed,
      () => {},
      (fn) => { unmount = fn; },
      vue.ref,
      () => ({ connected: true, uptime: "1h" }),
      () => () => {},
      (_path, _method, _body, signal) => {
        signals.push(signal);
        return new Promise(() => {});
      },
      (_path, signal) => {
        signals.push(signal);
        return new Promise(() => {});
      },
      { success() {} },
      (x) => x,
      () => true,
      () => false,
    ),
  );
  void panel.refresh();
  assert.equal(signals.length, 3);
  assert.equal(signals.every((s) => s && s.aborted === false), true);
  unmount();
  assert.equal(signals.every((s) => s.aborted === true), true);
  scope.stop();
});
