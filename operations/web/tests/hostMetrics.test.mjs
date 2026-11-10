import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import { ref } from "vue";
import ts from "typescript";

const source = await readFile(
  new URL("../src/hostMetrics.ts", import.meta.url),
  "utf8",
);
const compiled = ts.transpileModule(source, {
  compilerOptions: {
    target: ts.ScriptTarget.ES2022,
    module: ts.ModuleKind.ESNext,
  },
}).outputText;
const {
  appendCpuSample,
  metricSnapshot,
  metricNotice,
  metricBytes,
  metricDuration,
  metricsLive,
} = await import(
  `data:text/javascript;base64,${Buffer.from(compiled).toString("base64")}`
);

test("CPU history keeps only distinct valid samples and stays bounded", () => {
  let history = [];
  for (let index = 0; index < 75; index++)
    history = appendCpuSample(history, {
      code: "READY",
      observedAt: new Date(1700000000000 + index * 30000).toISOString(),
      cpuPercent: index,
    });
  assert.equal(history.length, 60);
  assert.equal(history[0].value, 15);
  const latest = {
    code: "READY",
    observedAt: history.at(-1).at,
    cpuPercent: 99,
  };
  assert.equal(
    appendCpuSample(history, latest),
    history,
    "polling one cached snapshot must not add samples",
  );
  assert.equal(
    appendCpuSample(history, { ...latest, observedAt: history[0].at }),
    history,
  );
  for (const value of [undefined, Number.NaN, -1, 101])
    assert.equal(
      appendCpuSample(history, {
        ...latest,
        observedAt: new Date(1800000000000).toISOString(),
        cpuPercent: value,
      }),
      history,
    );
});

test("failed and paused observations retain confirmed data with plain Chinese status", () => {
  const observation = {
    hostId: "host-a",
    enabled: true,
    freshness: "STALE",
    lastConfirmed: {
      code: "READY",
      observedAt: "2026-11-10T01:00:00Z",
      cpuPercent: 25,
    },
    latestAttempt: { code: "SSH_AUTH_FAILED" },
  };
  const current = metricSnapshot(observation);
  assert.equal(current.cpuPercent, 25);
  assert.equal(metricsLive(current), false);
  assert.match(metricNotice(current), /SSH 登录失败/);
  assert.equal(metricNotice(current).includes("SSH_AUTH_FAILED"), false);
  assert.equal(metricNotice({ ...current, enabled: false }), "监测已暂停");
  assert.equal(metricBytes(0), "0 B");
  assert.equal(metricBytes(1024 ** 3), "1.0 GiB");
  assert.equal(metricBytes(undefined), "—");
  assert.equal(metricDuration(90060), "1 天 1 小时");
});

const panelSource = (
  await readFile(new URL("../src/HostSystemPanel.vue", import.meta.url), "utf8")
)
  .split('<script setup lang="ts">')[1]
  .split("</script>")[0]
  .replace(/^import[\s\S]*?;\r?$/gm, "");
const panelScript = ts.transpileModule(panelSource, {
  compilerOptions: {
    target: ts.ScriptTarget.ES2022,
    module: ts.ModuleKind.None,
  },
}).outputText;
const createPanel = new Function(
  "ref",
  "watch",
  "onUnmounted",
  "defineProps",
  "defineEmits",
  "metricSnapshot",
  "request",
  "ElMessage",
  "ApiError",
  "setTimeout",
  "clearTimeout",
  "document",
  panelScript +
    "\nreturn { current, loading, detecting, monitoring, readError, load, detect, toggleMonitoring };",
);
const deferred = () => {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });
  return { promise, resolve };
};
const flush = async () => {
  for (let index = 0; index < 8; index++) await Promise.resolve();
};
function harness(request) {
  const props = { host: { id: "host-a", version: "1" } },
    timers = new Map(),
    emitted = [];
  let changed,
    unmounted,
    timerId = 0;
  const panel = createPanel(
    ref,
    (_source, callback) => {
      changed = callback;
      callback();
    },
    (callback) => {
      unmounted = callback;
    },
    () => props,
    () =>
      (...event) =>
        emitted.push(event),
    metricSnapshot,
    request,
    { success() {} },
    class ApiError extends Error {},
    (callback) => {
      const id = ++timerId;
      timers.set(id, callback);
      return id;
    },
    (id) => timers.delete(id),
    { hidden: false },
  );
  return {
    panel,
    timers,
    emitted,
    changeHost(id) {
      props.host = { id, version: "1" };
      changed();
    },
    unmount() {
      unmounted();
    },
  };
}
const snapshot = (hostId) => ({
  hostId,
  enabled: true,
  freshness: "FRESH",
  lastConfirmed: {
    code: "READY",
    observedAt: "2026-11-10T01:00:00Z",
    hostname: hostId,
  },
  latestAttempt: { code: "READY" },
});

test("host changes ignore late observations and preserve the new loading state", async () => {
  const old = deferred(),
    current = deferred(),
    calls = [];
  const h = harness((path, method, _body, signal) => {
    calls.push({ path, method, signal });
    return path.includes("host-a") ? old.promise : current.promise;
  });
  h.changeHost("host-b");
  assert.equal(calls[0].signal.aborted, true);
  old.resolve(snapshot("host-a"));
  await flush();
  assert.equal(h.panel.current.value, undefined);
  assert.equal(h.panel.loading.value, true);
  current.resolve(snapshot("host-b"));
  await flush();
  assert.equal(h.panel.current.value.hostId, "host-b");
  assert.equal(h.panel.loading.value, false);
  assert.equal(h.timers.size, 1);
  assert.ok(
    calls.every((call) => call.method === "GET"),
    "opening details only reads cached observations",
  );
  h.unmount();
  assert.equal(h.timers.size, 0);
});

test("closing system information cancels pending reads without emitting stale results", async () => {
  const pending = deferred();
  let signal;
  const h = harness((_path, _method, _body, value) => {
    signal = value;
    return pending.promise;
  });
  h.unmount();
  assert.equal(signal.aborted, true);
  pending.resolve(snapshot("host-a"));
  await flush();
  assert.equal(h.panel.current.value, undefined);
  assert.equal(h.timers.size, 0);
  assert.deepEqual(h.emitted, []);
});

test("monitoring replies only change enabled and a previous poll cannot undo them", async () => {
  const old = deferred(),
    calls = [];
  let reads = 0;
  const h = harness(async (path, method, body, signal) => {
    calls.push({ path, method, body, signal });
    if (method === "PATCH") return { enabled: false };
    reads++;
    if (reads === 2) return old.promise;
    return { ...snapshot("host-a"), enabled: reads === 1 };
  });
  await flush();
  const late = h.panel.load();
  await h.panel.toggleMonitoring();
  assert.equal(calls[1].signal.aborted, true);
  old.resolve(snapshot("host-a"));
  await late;
  assert.equal(h.panel.current.value.hostId, "host-a");
  assert.equal(h.panel.current.value.enabled, false);
  assert.deepEqual(calls.find((call) => call.method === "PATCH").body, {
    enabled: false,
    expectedVersion: "1",
  });
  assert.deepEqual(h.emitted, [], "monitoring replies are not host records");
  assert.equal(h.timers.size, 1);
  h.unmount();
});

test("read failures retain the last snapshot without repeated global error dialogs", async () => {
  let failed = false;
  const h = harness(async () => {
    if (failed) throw new Error("offline");
    return snapshot("host-a");
  });
  await flush();
  failed = true;
  await h.panel.load();
  await h.panel.load();
  assert.equal(h.panel.current.value.hostname, "host-a");
  assert.match(h.panel.readError.value, /暂时无法读取/);
  assert.deepEqual(h.emitted, []);
  h.unmount();
});
