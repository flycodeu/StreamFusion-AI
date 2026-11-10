import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";

const helperSource = await readFile(new URL("../src/pagination.ts", import.meta.url), "utf8");
const exports = {};
new Function("require", "exports", ts.transpileModule(helperSource, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText)(() => vue, exports);
const { usePagination, pageSizes } = exports;

test("client pagination resets filters and size, clamps deleted pages, and preserves refresh position", () => {
  const items = vue.ref(Array.from({ length: 25 }, (_, index) => index));
  const query = vue.ref("");
  const pager = usePagination(() => items.value.filter(value => String(value).includes(query.value)), [query]);
  assert.equal(pager.pageSize.value, 10);
  assert.deepEqual(pager.pageItems.value, items.value.slice(0, 10));
  pager.page.value = 3;
  assert.equal(pager.pageItems.value.length, 5);
  items.value = items.value.slice();
  assert.equal(pager.page.value, 3);
  items.value = items.value.slice(0, 11);
  assert.equal(pager.page.value, 2);
  assert.deepEqual(pager.pageItems.value, [10]);
  pager.pageSize.value = 20;
  assert.equal(pager.page.value, 1);
  query.value = "1";
  assert.deepEqual(pager.pageItems.value, [1, 10]);
  items.value = [];
  assert.equal(pager.pageCount.value, 1);
  assert.equal(pager.page.value, 1);
  assert.deepEqual(pager.pageItems.value, []);
});

function componentScript(source) {
  return ts.transpileModule(source.split('<script setup lang="ts">')[1].split("</script>")[0]
    .replace(/^import[\s\S]*?;\r?$/gm, ""), {
    compilerOptions: { module: ts.ModuleKind.None, target: ts.ScriptTarget.ES2022 },
  }).outputText;
}
const pagerSource = await readFile(new URL("../src/OpsPagination.vue", import.meta.url), "utf8");
const makePager = new Function("computed", "pageSizes", "defineProps", "withDefaults", "defineEmits",
  componentScript(pagerSource) + "\nreturn { changePage, changeSize };");

test("pager blocks disabled and unavailable cursor actions without inventing totals", () => {
  const props = vue.reactive({ page: 1, pageSize: 10, disabled: true, hasNext: true });
  const events = [];
  const pager = makePager(vue.computed, pageSizes, () => props, (value, defaults) => {
    for (const key in defaults) if (value[key] === undefined) value[key] = defaults[key];
    return value;
  }, () => (...event) => events.push(event));
  pager.changePage(2); pager.changeSize(20);
  assert.deepEqual(events, []);
  props.disabled = false; props.hasNext = false;
  pager.changePage(2); pager.changePage(0);
  assert.deepEqual(events, []);
  props.hasNext = true;
  pager.changePage(2); pager.changeSize(20);
  assert.deepEqual(events, [["update:page", 2], ["update:pageSize", 20]]);
  events.length = 0;
  props.page = 3; props.total = 21;
  pager.changePage(99);
  assert.deepEqual(events, [], "a clamp to the current page must not replay a fetch");
});

const logsSource = await readFile(new URL("../src/SoftwareLogs.vue", import.meta.url), "utf8");
const makeLogs = new Function("computed", "ref", "watch", "onUnmounted", "defineProps", "defineEmits", "request", "requestAllPages", "ApiError", "hostTime", "jobKindLabel", "softwareJobStatusLabel", "softwareLifecycleAvailable", "softwareState", "setTimeout", "clearTimeout",
  componentScript(logsSource) + "\nreturn { page, pageSize, records, selected, selectedId, query, cursors, loadRecords, changePage }; ");
const listPagesSource = await readFile(new URL("../src/listPages.ts", import.meta.url), "utf8");
const makeListPages = new Function("request", ts.transpileModule(listPagesSource.replace(/^import .+;\r?$/gm, "").replace(/\bexport /g, ""), {
  compilerOptions: { module: ts.ModuleKind.None, target: ts.ScriptTarget.ES2022 },
}).outputText + "\nreturn requestAllPages;");
const flush = async () => { for (let index = 0; index < 12; index++) await vue.nextTick(); };
const appSource = await readFile(new URL("../src/App.vue", import.meta.url), "utf8");
const makeApp = new Function("computed", "ref", "watch", "nextTick", "onMounted", "onUnmounted", "request", "usePagination", "document",
  componentScript(appSource) + "\nreturn { loadAudit, latestAudit, changeAuditPage, auditPage, auditPageSize, audits, auditLoading, auditDetail, credentials, credentialPage, pagedCredentials, latest }; ");

test("audit paging fetches older pages, preserves detail, resets size once and ignores stale replies", async t => {
  const calls = [], rows = Array.from({ length: 25 }, (_, index) => ({ id: `audit-${index}` }));
  let hold = false, fail = false, release;
  const scope = vue.effectScope(); t.after(() => scope.stop());
  const panel = scope.run(() => makeApp(vue.computed, vue.ref, vue.watch, vue.nextTick, () => {}, () => {}, async path => {
    calls.push(path);
    if (fail) { fail = false; throw new Error("读取失败"); }
    const params = new URL(path, "http://fixture").searchParams;
    const start = Number(params.get("before") || 0), size = Number(params.get("limit"));
    const result = { items: rows.slice(start, start + size), nextCursor: start + size < rows.length ? String(start + size) : "" };
    if (hold) { hold = false; return new Promise(resolve => { release = () => resolve(result); }); }
    return result;
  }, usePagination, {}));
  await panel.loadAudit();
  assert.equal(panel.audits.value.length, 10);
  panel.auditDetail.value = panel.audits.value[0];
  panel.changeAuditPage(2); await flush();
  assert.equal(panel.audits.value[0].id, "audit-10");
  assert.equal(panel.auditDetail.value.id, "audit-0");
  fail = true;
  await assert.rejects(panel.latestAudit(), /读取失败/);
  await panel.loadAudit();
  assert.equal(panel.auditPage.value, 2);
  assert.equal(panel.audits.value[0].id, "audit-10", "a failed refresh must retain the second page cursor");
  const previousCalls = calls.length;
  await panel.loadAudit(1, 20);
  assert.equal(calls.length, previousCalls + 1);
  assert.equal(panel.auditPage.value, 1);
  assert.equal(panel.audits.value.length, 20);
  hold = true;
  const stale = panel.loadAudit();
  await panel.loadAudit(1, 10);
  release(); await stale;
  assert.equal(panel.audits.value.length, 10);
  assert.equal(panel.auditLoading.value, false);
  panel.credentials.value = Array.from({ length: 11 }, (_, index) => ({ id: `credential-${index}`, revision: "1" }));
  panel.credentials.value[10] = { id: "credential-0", revision: "2" };
  assert.equal(panel.pagedCredentials.value.length, 10);
  assert.equal(panel.latest(panel.pagedCredentials.value[0]), false, "revision checks must include credentials on other pages");
});

test("host filters reset the list page while the open host remains selected", async t => {
  const source = await readFile(new URL("../src/HostManagement.vue", import.meta.url), "utf8");
  const scope = vue.effectScope(); t.after(() => scope.stop());
  const setup = new Function("computed", "ref", "watch", "nextTick", "onMounted", "onUnmounted", "defineProps", "defineEmits", "usePagination", "hostObservationExpired",
    componentScript(source) + "\nreturn { hosts, query, page, pagedHosts, detailId, detail }; ");
  const panel = scope.run(() => setup(vue.computed, vue.ref, vue.watch, vue.nextTick, () => {}, () => {}, () => ({}), () => () => {}, usePagination, () => false));
  panel.hosts.value = Array.from({ length: 16 }, (_, index) => ({ id: `host-${index}`, name: `服务器 ${index}`, address: `192.0.2.${index}`, sshUser: "ubuntu" }));
  panel.detailId.value = "host-15";
  panel.page.value = 2;
  assert.equal(panel.pagedHosts.value.length, 6);
  panel.query.value = "服务器 0"; await flush();
  assert.equal(panel.page.value, 1);
  assert.equal(panel.pagedHosts.value.length, 1);
  assert.equal(panel.detail.value.id, "host-15");
});
function logHarness(mode = "jobs") {
  const props = vue.reactive({ mode });
  const calls = [], timers = new Map();
  const jobs = Array.from({ length: 25 }, (_, index) => ({ id: `job-${index}`, targetId: "host-a", status: "SUCCEEDED", kind: "SOFTWARE_INSTALL", subjectName: `软件 ${index}`, createdAt: "2026-11-10" }));
  const software = Array.from({ length: 25 }, (_, index) => ({ id: `software-${index}`, hostId: "host-a", name: `软件 ${index}`, actualState: "RUNNING", updatedAt: String(100 - index) }));
  const scope = vue.effectScope();
  let unmount, timer = 0, holdPage = false, failPage = false, releasePage;
  const request = async path => {
    calls.push(path);
    if (path === "/hosts") return [{ id: "host-a", name: "服务器" }];
    if (path.startsWith("/software-instances")) {
      const params = new URL(path, "http://fixture").searchParams;
      const start = (Number(params.get("page")) - 1) * Number(params.get("pageSize"));
      return { items: software.slice(start, start + Number(params.get("pageSize"))), total: software.length };
    }
    if (path.startsWith("/jobs/")) return jobs.find(item => item.id === path.split("/").at(-1));
    const query = new URL(path, "http://fixture").searchParams;
    const before = Number(query.get("before") || 0), size = Number(query.get("limit"));
    if (failPage && before) { failPage = false; throw new Error("读取下一页失败"); }
    const matches = jobs.filter(item => !query.get("query") || item.subjectName.includes(query.get("query")));
    const result = { items: matches.slice(before, before + size), nextCursor: before + size < matches.length ? String(before + size) : "" };
    if (holdPage && before) { holdPage = false; return new Promise(resolve => { releasePage = () => resolve(result); }); }
    return result;
  };
  const panel = scope.run(() => makeLogs(vue.computed, vue.ref, vue.watch, callback => { unmount = callback; }, () => props, () => () => {}, request, makeListPages(request), class ApiError extends Error {}, value => value, value => value, value => value, () => true, item => ({ label: item.actualState }), callback => { const id = ++timer; timers.set(id, callback); return id; }, id => timers.delete(id)));
  return { panel, calls, software, holdPage() { holdPage = true; }, failPage() { failPage = true; }, releasePage() { releasePage(); }, runTimers() { for (const [id, callback] of [...timers]) { timers.delete(id); callback(); } }, close() { unmount(); scope.stop(); } };
}

test("log cursor paging defaults to ten, reuses cursor history and retains current output", async t => {
  const fixture = logHarness(); t.after(() => fixture.close());
  const { panel, calls } = fixture;
  await flush();
  assert.equal(panel.records.value.length, 10);
  assert.equal(panel.selectedId.value, "job-0");
  panel.changePage(2); await flush();
  assert.deepEqual(panel.cursors.value, ["", "10"]);
  assert.equal(panel.records.value[0].id, "job-10");
  assert.equal(panel.selected.value.id, "job-0", "list navigation must keep the output being read");
  panel.changePage(1); await flush();
  assert.equal(new URL(calls.filter(path => path.startsWith("/jobs?")).at(-1), "http://fixture").searchParams.has("before"), false);
  panel.pageSize.value = 20; await flush(); fixture.runTimers(); await flush();
  assert.equal(panel.page.value, 1);
  assert.deepEqual(panel.cursors.value, [""]);
  assert.equal(panel.records.value.length, 20);
});

test("runtime log paging clamps a shortened list without discarding the open output", async t => {
  const fixture = logHarness("runtime"); t.after(() => fixture.close());
  const { panel, software } = fixture;
  await flush();
  const selected = panel.selectedId.value;
  panel.changePage(3); await flush();
  assert.equal(panel.records.value.length, 5);
  assert.equal(panel.selected.value.id, selected);
  software.splice(11);
  await panel.loadRecords();
  assert.equal(panel.page.value, 2);
  assert.equal(panel.records.value.length, 1);
  assert.equal(panel.selected.value.id, selected);
});

test("a failed log page retains its rows, cursor and open output before retry", async t => {
  const fixture = logHarness(); t.after(() => fixture.close());
  const { panel } = fixture;
  await flush();
  panel.changePage(2); await flush();
  fixture.failPage(); panel.changePage(3); await flush();
  assert.equal(panel.page.value, 2);
  assert.deepEqual(panel.cursors.value, ["", "10"]);
  assert.equal(panel.records.value[0].id, "job-10");
  assert.equal(panel.selected.value.id, "job-0");
  panel.changePage(3); await flush();
  assert.equal(panel.page.value, 3);
  assert.deepEqual(panel.cursors.value, ["", "10", "20"]);
  assert.equal(panel.records.value[0].id, "job-20");
  assert.equal(panel.selected.value.id, "job-0");
});

test("a late cursor page cannot replace the next filter result", async t => {
  const fixture = logHarness(); t.after(() => fixture.close());
  const { panel } = fixture;
  await flush();
  fixture.holdPage(); panel.changePage(2); await flush();
  panel.query.value = "软件 0"; await flush(); fixture.runTimers(); await flush();
  fixture.releasePage(); await flush();
  assert.equal(panel.page.value, 1);
  assert.deepEqual(panel.cursors.value, [""]);
  assert.deepEqual(panel.records.value.map(item => item.id), ["job-0"]);
  assert.equal(panel.selected.value.id, "job-0");
});
