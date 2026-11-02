import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";

function load(source, imports = {}, globals = {}) {
  const module = { exports: {} };
  const names = ["require", "module", "exports", ...Object.keys(globals)];
  const output = ts.transpileModule(source, {
    compilerOptions: {
      target: ts.ScriptTarget.ES2022,
      module: ts.ModuleKind.CommonJS,
    },
  }).outputText;
  new Function(...names, output)(
    (name) => imports[name],
    module,
    module.exports,
    ...Object.values(globals),
  );
  return module.exports;
}
const model = load(
  await readFile(new URL("../src/dockerAccess.ts", import.meta.url), "utf8"),
);
const source = (
  await readFile(
    new URL("../src/DockerAccessPanel.vue", import.meta.url),
    "utf8",
  )
)
  .split('<script setup lang="ts">')[1]
  .split("</script>")[0];
const engine = {
  id: "engine",
  hostId: "host",
  version: "1",
  softwareType: "DOCKER",
  managementMode: "MANAGED",
  dataOwnership: "OPS",
  installationStatus: "READY",
  runtimeKind: "SYSTEMD",
  locator: "docker.service",
  installation: { definition: { key: "docker" } },
};
const ubuntu = {
  name: "ubuntu",
  uid: "1000",
  gid: "1000",
  shell: "/bin/bash",
  eligible: true,
  member: false,
  primaryGroup: false,
  groups: [],
};
const state = () => ({
  softwareId: "engine",
  expectedVersion: "1",
  expectedHostVersion: "1",
  sshAccount: "ubuntu",
  observation: {
    softwareId: "engine",
    fingerprint: "a".repeat(64),
    dockerGid: "999",
    observedAt: "2026-11-02T00:00:00Z",
    socket: { ready: true },
    accounts: [
      structuredClone(ubuntu),
      { ...ubuntu, name: "operator", uid: "1001" },
    ],
  },
});
const tick = async () => {
  for (let i = 0; i < 12; i++) {
    await Promise.resolve();
    await vue.nextTick();
  }
};
class TestApiError extends Error {
  constructor(code, status) {
    super(code);
    this.code = code;
    this.status = status;
  }
}
function harness(custom) {
  const calls = [],
    unmount = [],
    events = [],
    timers = new Map();
  let timerId = 0;
  const snapshot = state();
  const currentJob = {
    id: "job",
    kind: "SOFTWARE_DOCKER_ACCESS",
    status: "RUNNING",
    phase: "DOCKER_ACCESS_APPLY",
    targetAckPending: false,
  };
  const props = vue.reactive({
    detail: structuredClone(engine),
    host: { id: "host", sshUser: "ubuntu", version: "1" },
    active: true,
    disabled: false,
  });
  const request = async (path, method = "GET", body, signal) => {
    calls.push({ path, method, body: body && structuredClone(body), signal });
    const override = custom && (await custom(path, method, body, signal));
    if (override !== undefined) return override;
    if (path.endsWith("/docker-access")) return structuredClone(snapshot);
    if (path.endsWith("/actions"))
      return { jobId: "job", softwareInstanceId: "engine" };
    if (path === "/jobs/job") return structuredClone(currentJob);
    if (path === "/hosts") return [{ ...props.host, version: "2" }];
    if (path === "/software-instances")
      return [{ ...props.detail, version: "2" }];
    if (path.endsWith("/reconcile")) return {};
    throw new Error("Unexpected " + path);
  };
  const scope = vue.effectScope();
  const flow = scope.run(
    () =>
      load(
        source +
          "\nexport const flow={snapshot,selectedAccount,confirmation,attempted,job,jobId,busy,error,localHost,localDetail,refresh,prepare,submit,jobAction,preparationUpdated};",
        {
          vue: { ...vue, onUnmounted: (fn) => unmount.push(fn) },
          "./api": { ApiError: TestApiError, request },
          "./dockerAccess": model,
        },
        {
          defineProps: () => props,
          defineEmits:
            () =>
            (...args) =>
              events.push(args),
          setTimeout: (fn) => {
            const id = ++timerId;
            timers.set(id, fn);
            return id;
          },
          clearTimeout: (id) => timers.delete(id),
          document: { hidden: false },
        },
      ).flow,
  );
  return {
    flow,
    calls,
    props,
    snapshot,
    currentJob,
    events,
    timers,
    async poll() {
      const [id, fn] = timers.entries().next().value;
      timers.delete(id);
      await fn();
    },
    close() {
      unmount.forEach((fn) => fn());
      scope.stop();
    },
  };
}

test("Docker permission entry excludes containers, unmanaged and unrelated software", () => {
  assert.equal(model.managedDockerAccess(engine), true);
  for (const patch of [
    { managementMode: "OBSERVE" },
    { dataOwnership: "EXTERNAL" },
    { runtimeKind: "DOCKER" },
    { installationStatus: "UNKNOWN" },
    { installation: undefined },
  ])
    assert.equal(model.managedDockerAccess({ ...engine, ...patch }), false);
  assert.equal(model.dockerAccountAction(ubuntu, "GRANT", false), false);
  assert.equal(
    model.dockerAccountAction({ ...ubuntu, member: true }, "GRANT"),
    false,
  );
  assert.equal(
    model.dockerAccountAction({ ...ubuntu, member: true }, "REVOKE", false),
    true,
  );
  assert.equal(
    model.dockerAccountAction(
      { ...ubuntu, member: true, primaryGroup: true },
      "REVOKE",
    ),
    false,
  );
});
test("loading and confirming an account never grants before explicit submission", async () => {
  const h = harness();
  await tick();
  assert.equal(h.flow.selectedAccount.value, "ubuntu");
  h.flow.prepare("GRANT");
  assert.equal(h.flow.confirmation.value, "GRANT");
  assert.equal(h.calls.filter((call) => call.method === "POST").length, 0);
  h.props.host = { ...h.props.host, version: "2" };
  h.props.detail = { ...h.props.detail, version: "2" };
  await tick();
  assert.equal(h.flow.confirmation.value, "GRANT");
  await h.flow.submit();
  const submitted = h.calls.find((call) => call.path.endsWith("/actions"));
  assert.equal(submitted.body.expectedVersion, "1");
  assert.equal(submitted.body.expectedHostVersion, "1");
  assert.equal(submitted.body.confirmed, true);
  assert.deepEqual(submitted.body.dockerAccess, {
    action: "GRANT",
    account: "ubuntu",
    expectedStateHash: "a".repeat(64),
  });
  h.close();
});
test("a lost submission reuses exactly the frozen account, hash and idempotency key", async () => {
  let attempts = 0;
  const h = harness((path) => {
    if (path.endsWith("/actions") && ++attempts === 1)
      throw new Error("connection lost");
  });
  await tick();
  h.flow.prepare("GRANT");
  await h.flow.submit();
  assert.equal(h.flow.attempted.value, true);
  await h.flow.refresh();
  await h.flow.submit();
  const posts = h.calls.filter((call) => call.path.endsWith("/actions"));
  assert.equal(posts.length, 2);
  assert.deepEqual(posts[0].body, posts[1].body);
  h.close();
});
test("UNKNOWN restores the original permission job and only explicit reconciliation mutates", async () => {
  const h = harness();
  h.snapshot.lastJobId = "job";
  h.currentJob.status = "UNKNOWN";
  await tick();
  await h.flow.refresh();
  assert.equal(h.flow.job.value.status, "UNKNOWN");
  assert.equal(h.timers.size, 0);
  h.flow.prepare("GRANT");
  assert.equal(h.flow.confirmation.value, undefined);
  assert.equal(h.calls.filter((call) => call.method === "POST").length, 0);
  await h.flow.jobAction("reconcile");
  assert.equal(
    h.calls.filter((call) => call.method === "POST").at(-1).path,
    "/jobs/job/reconcile",
  );
  h.close();
});
test("explicit pre-queue rejection preserves the account and permits a fresh check and confirmation", async () => {
  let attempts = 0;
  const h = harness((path) => {
    if (path.endsWith("/actions") && ++attempts === 1)
      throw new TestApiError("DOCKER_ACCESS_STATE_CHANGED", 409);
  });
  await tick();
  h.flow.selectedAccount.value = "operator";
  h.flow.prepare("GRANT");
  await h.flow.submit();
  assert.equal(h.flow.attempted.value, false);
  assert.equal(h.flow.confirmation.value, undefined);
  assert.equal(h.flow.snapshot.value, undefined);
  assert.equal(h.flow.selectedAccount.value, "operator");
  await h.flow.refresh();
  h.flow.prepare("GRANT");
  await h.flow.submit();
  const posts = h.calls.filter((call) => call.path.endsWith("/actions"));
  assert.equal(posts.length, 2);
  assert.notEqual(posts[0].body.idempotencyKey, posts[1].body.idempotencyKey);
  h.close();
});
test("server errors retain the original key, even with a recognizable rejection code", async () => {
  let attempts = 0;
  const h = harness((path) => {
    if (path.endsWith("/actions") && ++attempts === 1)
      throw new TestApiError("VERSION_CONFLICT", 503);
  });
  await tick();
  h.flow.prepare("GRANT");
  await h.flow.submit();
  assert.equal(h.flow.attempted.value, true);
  h.props.disabled = true;
  h.props.host.maintenance = true;
  await tick();
  await h.flow.submit();
  const posts = h.calls.filter((call) => call.path.endsWith("/actions"));
  assert.equal(posts.length, 2);
  assert.deepEqual(posts[0].body, posts[1].body);
  h.close();
});
test("maintenance or disabled permissions after confirmation block a first submission", async () => {
  for (const change of [
    (props) => {
      props.disabled = true;
    },
    (props) => {
      props.host.maintenance = true;
    },
  ]) {
    const h = harness();
    await tick();
    h.flow.prepare("GRANT");
    change(h.props);
    await tick();
    await h.flow.submit();
    assert.equal(h.flow.attempted.value, false);
    assert.equal(
      h.calls.filter((call) => call.path.endsWith("/actions")).length,
      0,
    );
    h.close();
  }
});
test("completion refreshes membership and does not schedule another mutation", async () => {
  const h = harness();
  await tick();
  h.flow.prepare("GRANT");
  await h.flow.submit();
  h.currentJob.status = "SUCCEEDED";
  h.snapshot.observation.accounts[0].member = true;
  await h.poll();
  assert.equal(h.flow.snapshot.value.observation.accounts[0].member, true);
  assert.equal(h.flow.attempted.value, false);
  assert.equal(h.timers.size, 0);
  h.flow.prepare("GRANT");
  assert.equal(h.flow.confirmation.value, undefined);
  assert.equal(
    h.calls.filter((call) => call.path.endsWith("/actions")).length,
    1,
  );
  h.close();
});
test("component update keeps selected account, refreshes versions and never authorizes", async () => {
  const h = harness();
  await tick();
  h.flow.selectedAccount.value = "operator";
  h.snapshot.expectedVersion = "2";
  h.snapshot.expectedHostVersion = "2";
  await h.flow.preparationUpdated();
  assert.equal(h.flow.selectedAccount.value, "operator");
  assert.equal(h.flow.localHost.value.version, "2");
  assert.equal(h.flow.localDetail.value.version, "2");
  assert.equal(h.flow.snapshot.value.expectedVersion, "2");
  assert.equal(h.calls.filter((call) => call.method === "POST").length, 0);
  h.close();
});
test("closing or switching the host aborts reads and rejects late results", async () => {
  let resolve;
  const h = harness((path) =>
    path.endsWith("/docker-access")
      ? new Promise((done) => {
          resolve = done;
        })
      : undefined,
  );
  await tick();
  const original = h.calls[0];
  h.props.active = false;
  await tick();
  assert.equal(original.signal.aborted, true);
  resolve(state());
  await tick();
  assert.equal(h.flow.snapshot.value, undefined);
  h.close();
});
