import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";

const source = await readFile(new URL("../src/OpsWorkspace.vue", import.meta.url), "utf8");
const script = ts.transpileModule(source.split('<script setup lang="ts">')[1].split("</script>")[0]
  .replace(/^import[\s\S]*?;\r?$/gm, ""), {
  compilerOptions: { module: ts.ModuleKind.None, target: ts.ScriptTarget.ES2022 },
}).outputText;
const makeWorkspace = new Function("ref", "computed", "watch", "onMounted", "onUnmounted", "defineProps", "defineEmits", "request", "requestAllPages", "softwareJobPhaseLabels", "softwareJobKindLabels", "setTimeout", "clearTimeout", "ElMessage",
  script + "\nreturn { refresh, loadJobs, changeJobPage, changeJobSize, jobs, jobPage, jobPageSize, jobCursors, jobStatusFilter, jobKindFilter, detail, dialog, artifactList, instanceList, bindingList, releaseList, selected, bindingOptions, credentials, openRuntimePolicy, instanceProgressLabel, reconcile, instanceDraft, saveInstance }; ");
const flush = async () => { for (let i = 0; i < 15; i++) await vue.nextTick(); };

function harness(page = "jobs") {
  const props = vue.reactive({ page }), calls = [], writes = [], errors = [], unmounts = [];
  const jobs = Array.from({ length: 130 }, (_, i) => ({ id: `job-${i}`, status: i % 2 ? "FAILED" : "SUCCEEDED", kind: i % 3 ? "PUBLISH" : "ROLLBACK", phase: "TEST", createdAt: "2026-11-10", updatedAt: "2026-11-10" }));
  const lists = Object.fromEntries(["artifacts", "instances", "bindings", "releases"].map(path => [path, Array.from({ length: 127 }, (_, i) => ({ id: `${path}-${i}`, name: `${path}-${i}` }))]));
  let mounted, failNext = false, holdNext = false, release;
  const request = async (path, _method, _body, signal) => {
    calls.push(path);
    if (_method && _method !== "GET") writes.push({ path, method: _method, body: _body && JSON.parse(JSON.stringify(_body)) });
    const url = new URL(path, "http://fixture"), query = url.searchParams;
    if (url.pathname === "/hosts") return [{ id: "host-a", name: "服务器" }];
    if (url.pathname === "/credentials") return [{ id: "credential-a", revision: "1" }, { id: "credential-a", revision: "2" }];
    if (url.pathname.startsWith("/jobs/")) return { ...jobs.find(job => job.id === url.pathname.split("/").at(-1)), updatedAt: "refreshed" };
    let result;
    if (url.pathname === "/jobs") {
      const before = Number(query.get("before") || 0), size = Number(query.get("limit"));
      const matches = jobs.filter(job => (!query.get("status") || job.status === query.get("status")) && (!query.get("kind") || job.kind === query.get("kind")));
      result = { items: matches.slice(before, before + size), nextCursor: before + size < matches.length ? String(before + size) : "" };
    } else {
      const items = lists[url.pathname.split("/").at(-1)] || [];
      const size = Number(query.get("pageSize")), offset = (Number(query.get("page")) - 1) * size;
      result = { items: items.slice(offset, offset + size), total: items.length };
    }
    if (failNext) { failNext = false; throw new Error("connection failed"); }
    if (holdNext) {
      holdNext = false;
      // Intentionally ignore the signal to verify stale-response guards too.
      return new Promise(resolve => { release = () => resolve(result); });
    }
    return result;
  };
  const requestAllPages = async path => (await request(`${path}?page=1&pageSize=100`)).items;
  const scope = vue.effectScope();
  const panel = scope.run(() => makeWorkspace(vue.ref, vue.computed, vue.watch, callback => { mounted = callback; }, callback => unmounts.push(callback), () => props, () => (_event, error) => errors.push(error), request, requestAllPages, {}, {}, () => 1, () => {}, { success() {} }));
  return { panel, props, calls, writes, errors, lists, start: () => mounted(), fail() { failNext = true; }, hold() { holdNext = true; }, release() { release(); }, close() { unmounts.forEach(callback => callback()); scope.stop(); } };
}

test("workspace jobs use server cursors and filters and keep details while paging", async t => {
  const fixture = harness(); t.after(() => fixture.close());
  await fixture.start();
  const { panel } = fixture;
  assert.equal(panel.jobs.value.length, 10);
  assert.equal(panel.jobPageSize.value, 10);
  panel.detail.value = panel.jobs.value[0]; panel.dialog.value = "job";
  await flush();
  panel.changeJobPage(2); await flush();
  assert.equal(panel.jobPage.value, 2);
  assert.equal(panel.jobs.value[0].id, "job-10");
  const before = fixture.calls.length;
  await panel.refresh();
  assert.deepEqual(fixture.calls.slice(before).map(path => path.split("?")[0]), ["/jobs", "/jobs/job-0"]);
  assert.equal(panel.detail.value.id, "job-0");
  assert.equal(panel.detail.value.updatedAt, "refreshed");
  panel.jobKindFilter.value = "ROLLBACK"; panel.jobStatusFilter.value = "SUCCEEDED";
  await flush();
  const query = new URL(fixture.calls.filter(path => path.startsWith("/jobs?")).at(-1), "http://fixture").searchParams;
  assert.equal(query.get("kind"), "ROLLBACK"); assert.equal(query.get("status"), "SUCCEEDED");
  assert.equal(query.has("before"), false);
  assert.equal(panel.jobPage.value, 1);
  assert.equal(panel.jobs.value.length, 10, "filters must run before the server page boundary");
  assert.equal(panel.detail.value.id, "job-0");
});

test("a pending instance operation opens its original receipt and can reconcile acknowledgement", async t => {
  const fixture = harness("instances"); t.after(() => fixture.close());
  await fixture.start();
  const instance = { id: "instance-a", currentReleaseId: "release-a", pendingJob: { id: "job-0", status: "SUCCEEDED", targetAckPending: true } };
  assert.equal(fixture.panel.instanceProgressLabel(instance), "等待收尾");
  const before = fixture.calls.length;
  await fixture.panel.openRuntimePolicy(instance);
  assert.deepEqual(fixture.calls.slice(before), ["/jobs/job-0"]);
  assert.equal(fixture.panel.dialog.value, "job");
  assert.equal(fixture.panel.detail.value.id, "job-0");
  await fixture.panel.reconcile(fixture.panel.detail.value);
  assert.ok(fixture.calls.includes("/jobs/job-0/reconcile"));
  assert.ok(!fixture.calls.some(path => path.includes("/actions") || path.includes("/observations")));
  assert.equal(fixture.panel.instanceProgressLabel({ pendingJob: { status: "UNKNOWN" } }), "核对原操作");
});

test("saving an application draft includes its runtime policy without applying it to the server", async t => {
  const fixture = harness("instances"); t.after(() => fixture.close());
  fixture.panel.instanceDraft.value.config.runtimePolicy = { restart: "on-failure", autoStart: false, maxRetries: 3, stopTimeoutSeconds: 10 };
  await fixture.panel.saveInstance();
  assert.equal(fixture.writes.length, 1);
  assert.equal(fixture.writes[0].path, "/instances");
  assert.equal(fixture.writes[0].body.config.runtimePolicy.maxRetries, 3);
  assert.ok(!fixture.calls.some(path => path.includes("/runtime-policy") || path.includes("/actions")));
});

test("failed cursor navigation and size changes preserve the displayed page and history", async t => {
  const fixture = harness(); t.after(() => fixture.close());
  await fixture.start();
  const { panel } = fixture;
  fixture.fail(); panel.changeJobPage(2); await flush();
  assert.equal(panel.jobPage.value, 1);
  assert.deepEqual(panel.jobCursors.value, [""]);
  assert.equal(panel.jobs.value[0].id, "job-0");
  panel.changeJobPage(2); await flush();
  fixture.fail(); panel.changeJobSize(20); await flush();
  assert.equal(panel.jobPage.value, 2);
  assert.equal(panel.jobPageSize.value, 10);
  assert.deepEqual(panel.jobCursors.value, ["", "10"]);
  await panel.refresh();
  assert.equal(panel.jobs.value[0].id, "job-10");
  fixture.fail(); panel.jobKindFilter.value = "ROLLBACK"; await flush();
  assert.equal(panel.jobKindFilter.value, "");
  assert.equal(panel.jobPage.value, 2);
  assert.equal(fixture.errors.length, 3);
});

test("late cursor responses cannot replace a newer filtered page", async t => {
  const fixture = harness(); t.after(() => fixture.close());
  await fixture.start();
  fixture.hold(); fixture.panel.changeJobPage(2); await flush();
  fixture.panel.jobKindFilter.value = "ROLLBACK"; await flush();
  fixture.release(); await flush();
  assert.equal(fixture.panel.jobPage.value, 1);
  assert.deepEqual(fixture.panel.jobCursors.value, [""]);
  assert.equal(fixture.panel.jobs.value[1].id, "job-3");
});

test("workspace lists page beyond one hundred and commit paging only after success", async t => {
  const fixture = harness("artifacts"); t.after(() => fixture.close());
  await fixture.start();
  const list = fixture.panel.artifactList;
  assert.equal(list.total.value, 127); assert.equal(list.items.value.length, 10);
  list.changePage(13); await flush();
  assert.equal(list.items.value[0].id, "artifacts-120");
  fixture.fail(); list.changePage(12); await flush();
  assert.equal(list.page.value, 13); assert.equal(list.items.value[0].id, "artifacts-120");
  fixture.fail(); list.changeSize(20); await flush();
  assert.equal(list.pageSize.value, 10); assert.equal(list.page.value, 13);
  fixture.lists.artifacts.splice(11);
  await list.load();
  assert.equal(list.page.value, 2); assert.equal(list.items.value[0].id, "artifacts-10");
  fixture.hold(); list.changePage(1); await flush();
  list.changeSize(20); await flush(); fixture.release(); await flush();
  assert.equal(list.page.value, 1); assert.equal(list.pageSize.value, 20); assert.equal(list.items.value.length, 11);
});

test("dialog choices load when opened and preserve every credential revision", async t => {
  const fixture = harness("instances"); t.after(() => fixture.close());
  await fixture.start();
  assert.equal(fixture.calls.some(path => path.startsWith("/bindings")), false);
  fixture.panel.dialog.value = "instance"; await flush();
  assert.equal(fixture.panel.credentials.value.length, 2);
  assert.equal(fixture.panel.bindingOptions.value.length, 100);
  const before = fixture.calls.length;
  await fixture.panel.refresh();
  assert.deepEqual(fixture.calls.slice(before).map(path => path.split("?")[0]), ["/instances"]);
  fixture.panel.dialog.value = "history";
  fixture.panel.selected.value = { id: "instance-a" };
  fixture.panel.releaseList.changePage(13); await flush();
  assert.equal(fixture.panel.releaseList.items.value[0].id, "releases-120");
});
