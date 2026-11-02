import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";
const compile = (s) =>
  ts.transpileModule(s, {
    compilerOptions: {
      target: ts.ScriptTarget.ES2022,
      module: ts.ModuleKind.CommonJS,
    },
  }).outputText;
function load(s, imports = {}, props = {}) {
  const module = { exports: {} };
  new Function(
    "require",
    "module",
    "exports",
    "defineProps",
    "defineEmits",
    "setTimeout",
    "clearTimeout",
    "document",
    compile(s),
  )(
    (n) => imports[n],
    module,
    module.exports,
    () => props,
    () => () => {},
    () => 1,
    () => {},
    { hidden: true },
  );
  return module.exports;
}
const model = load(
  await readFile(new URL("../src/nodeEnvironment.ts", import.meta.url), "utf8"),
);
const source = (
  await readFile(
    new URL("../src/NodeEnvironmentPanel.vue", import.meta.url),
    "utf8",
  )
)
  .split('<script setup lang="ts">')[1]
  .split("</script>")[0];
const recipe = {
  key: "node",
  name: "Node.js",
  version: "v24.11.0",
  revision: "node24",
  runtimeModes: ["NATIVE"],
  sourceModes: ["OFFICIAL", "ARCHIVE"],
};
const base = {
  id: "env",
  hostId: "host",
  name: "Node test",
  account: "ubuntu",
  rootPath: "/home/ubuntu/node/env",
  version: "4",
  managementMode: "MANAGED",
  observation: { versions: [] },
  versions: [],
};
async function flush() {
  for (let i = 0; i < 10; i++) {
    await Promise.resolve();
    await vue.nextTick();
  }
}
function harness(options = {}) {
  const calls = [],
    stops = [],
    props = vue.reactive({
      host: { id: "host", version: "2", sshUser: "ubuntu" },
    });
  const env = structuredClone({ ...base, ...options.env });
  const request = async (path, method = "GET", data) => {
    calls.push({
      path,
      method,
      data: data === undefined ? undefined : JSON.parse(JSON.stringify(data)),
    });
    if (options.request) {
      const out = options.request(path, method, data);
      if (out !== undefined) return out;
    }
    if (path.endsWith("/node-environments")) return [env];
    if (path === "/node-environments/env") return env;
    if (path.endsWith("/jobs")) return [];
    if (path === "/node-environment-recipes")
      return {
        node: [recipe],
        tools: [{ ...recipe, key: "nvm", version: "0.40.8" }],
      };
    if (path === "/installation-materials") return [];
    if (path.endsWith("-plans"))
      return { id: "plan", expiresAt: "2099-01-01T00:00:00Z" };
    if (path.endsWith("/submit")) throw new Error("response lost");
    throw new Error(path);
  };
  const scope = vue.effectScope();
  const flow = scope.run(
    () =>
      load(
        source +
          "\nexport const flow={draft,plan,environment,error,attempted,openAction,preflight,submit,source,matchingMaterials,job,initialIntent,continuationRetry,continueInitialInstallation};",
        {
          vue: { ...vue, onUnmounted: (fn) => stops.push(fn) },
          "./api": { request, ApiError: class extends Error {} },
          "./nodeEnvironment": model,
          "./hosts": {},
        },
        props,
      ).flow,
  );
  return {
    flow,
    calls,
    props,
    dispose() {
      stops.forEach((fn) => fn());
      scope.stop();
    },
  };
}
test("Node online preflight sends no material operations and freezes environment version", async () => {
  const h = harness();
  await flush();
  h.flow.openAction("INSTALL_NODE");
  await h.flow.preflight();
  const call = h.calls.at(-1);
  assert.deepEqual(call.data.source, { type: "OFFICIAL" });
  assert.deepEqual(call.data.node, {
    environmentId: "env",
    environmentVersion: "4",
    makeDefault: true,
  });
  assert.equal(
    h.calls.some((c) => c.method === "POST" && c.path.includes("materials")),
    false,
  );
  h.dispose();
});
test("Node offline empty materials blocks preflight without downloading", async () => {
  const h = harness();
  await flush();
  h.flow.openAction("INSTALL_NODE");
  h.flow.draft.value.source = "ARCHIVE";
  await h.flow.preflight();
  assert.match(h.flow.error.value.message, /离线材料/);
  assert.equal(
    h.calls.some((c) => c.path.endsWith("-plans")),
    false,
  );
  h.dispose();
});
test("lost Node software submission retries original body and idempotency key", async () => {
  const h = harness();
  await flush();
  h.flow.openAction("INSTALL_NODE");
  await h.flow.preflight();
  await h.flow.submit();
  h.flow.environment.value.version = "99";
  await h.flow.submit();
  const posts = h.calls.filter((c) => c.path.endsWith("/submit"));
  assert.equal(posts.length, 2);
  assert.deepEqual(posts[0], posts[1]);
  assert.deepEqual(Object.keys(posts[0].data), ["idempotencyKey"]);
  assert.equal(h.flow.attempted.value, true);
  h.dispose();
});
test("default change requires explicit submit and never installs Node implicitly", async () => {
  const h = harness();
  await flush();
  h.flow.openAction("SET_DEFAULT", "v24.11.0");
  await h.flow.preflight();
  assert.equal(h.calls.at(-1).data.action, "SET_DEFAULT");
  assert.equal(
    h.calls.some((c) => c.path.endsWith("/submit")),
    false,
  );
  h.dispose();
});
test("external Node registration is read only for mutation flows", async () => {
  const h = harness({ env: { managementMode: "OBSERVE" } });
  await flush();
  h.flow.openAction("UPDATE_NVM");
  await h.flow.preflight();
  assert.match(h.flow.error.value.message, /仅支持检测/);
  assert.equal(
    h.calls.some((c) => c.method === "POST"),
    false,
  );
  h.dispose();
});
test("uninstall default Node blocks until explicit clear-default and only exact version record", async () => {
  const row = {
    id: "node-row",
    installationStatus: "READY",
    name: "Node.js v24.11.0",
    softwareVersion: "v24.11.0",
    version: "8",
    installationPaths: {
      root: "/env/nvm/versions/node/v24.11.0",
      data: "/env/cache/v24.11.0",
      logs: "/env/backups/v24.11.0",
    },
  };
  const h = harness({
    env: {
      observedDefaultVersion: "v24.11.0",
      observation: { versions: [{ version: "v24.11.0" }] },
      versions: [row],
    },
    request: (p) =>
      p.endsWith("/actions") ? Promise.reject(new Error("lost")) : undefined,
  });
  await flush();
  h.flow.openAction("UNINSTALL_NODE", "v24.11.0");
  h.flow.draft.value.confirmationName = row.name;
  await h.flow.submit();
  assert.equal(
    h.calls.some((c) => c.path.endsWith("/actions")),
    false,
  );
  h.flow.draft.value.clearNodeDefault = true;
  await h.flow.submit();
  const call = h.calls.at(-1);
  assert.equal(call.path, "/software-instances/node-row/actions");
  assert.equal(call.data.clearNodeDefault, true);
  assert.equal(call.data.deleteData, false);
  assert.equal(call.data.confirmationName, row.name);
  h.dispose();
});
test("late host response cannot overwrite a new host environment", async () => {
  let release;
  const wait = new Promise((r) => (release = r));
  const h = harness({
    request: (p) => (p === "/hosts/host/node-environments" ? wait : undefined),
  });
  await flush();
  h.props.host = { id: "second", version: "1" };
  await flush();
  release([{ ...base, id: "old-host-env" }]);
  await flush();
  assert.notEqual(h.flow.environment.value?.id, "old-host-env");
  h.dispose();
});
test("material matching excludes stale revisions and Docker archives", () => {
  const values = [
    {
      id: "ready",
      available: true,
      recipeKey: "node",
      recipeVersion: recipe.version,
      recipeRevision: recipe.revision,
      runtimeMode: "NATIVE",
    },
    {
      id: "old",
      available: true,
      recipeKey: "node",
      recipeVersion: recipe.version,
      recipeRevision: "old",
      runtimeMode: "NATIVE",
    },
  ];
  assert.deepEqual(
    model.nodeMaterials(values, recipe).map((x) => x.id),
    ["ready"],
  );
  assert.equal(model.nodeJobPending({ status: "UNKNOWN" }), true);
});

test("initial sequence accepts only the confirmed exact identity and Node plan", () => {
  const context = {
    account: "ubuntu",
    accountUid: "1000",
    rootPath: "/env",
    nvmVersion: "0.40.8",
    nvmHash: "nvmhash",
  };
  const intent = {
    hostId: "host",
    hostVersion: "2",
    environmentId: "env",
    context,
    definition: recipe,
    source: { type: "OFFICIAL" },
    makeDefault: true,
    expiresAt: "2099-01-01T00:00:00Z",
  };
  const env = {
    ...base,
    ...context,
    observation: { nvmHash: "nvmhash", versions: [] },
    observedDefaultVersion: "",
  };
  const plan = {
    definition: recipe,
    source: { type: "OFFICIAL" },
    config: {
      name: "Node.js " + recipe.version,
      runtimeMode: "NATIVE",
      memoryMiB: 512,
    },
    node: {
      ...context,
      environmentId: "env",
      environmentVersion: env.version,
      makeDefault: true,
    },
    paths: { root: "/env/nvm/versions/node/" + recipe.version },
    checks: [{ status: "PASSED" }],
    expiresAt: "2099-01-01T00:00:00Z",
  };
  const host = { id: "host", version: "2" };
  assert.equal(model.nodeInitialPlanMatches(intent, plan, env, host), true);
  for (const changed of [
    { ...plan, definition: { ...recipe, revision: "changed" } },
    { ...plan, node: { ...plan.node, makeDefault: false } },
    { ...plan, paths: { root: "/other" } },
    { ...plan, source: { type: "ARCHIVE" } },
    { ...plan, checks: [{ status: "WARNING" }] },
  ])
    assert.equal(
      model.nodeInitialPlanMatches(intent, changed, env, host),
      false,
    );
  assert.equal(
    model.nodeInitialPlanMatches(
      intent,
      plan,
      { ...env, accountUid: "2000" },
      host,
    ),
    false,
  );
  assert.equal(
    model.nodeInitialPlanMatches(intent, plan, env, { ...host, version: "3" }),
    false,
  );
  assert.equal(
    model.nodeInitialPlanMatches(
      { ...intent, expiresAt: "2000-01-01" },
      plan,
      env,
      host,
    ),
    false,
  );
});

test("first-install continuation stops on drift and preserves a lost second submission for same-key retry", async () => {
  const context = {
    account: "ubuntu",
    accountUid: "1000",
    rootPath: "/env",
    nvmVersion: "0.40.8",
    nvmHash: "hash",
  };
  const env = {
    ...base,
    ...context,
    observedDefaultVersion: "",
    observation: { versions: [], nvmHash: "hash" },
  };
  const intent = {
    hostId: "host",
    hostVersion: "2",
    environmentId: "env",
    context,
    definition: recipe,
    source: { type: "OFFICIAL" },
    makeDefault: true,
    expiresAt: "2099-01-01",
    initializationJobId: "init-job",
    submitKey: "stable-second-key",
  };
  const makePlan = () => ({
    id: "second",
    expiresAt: "2099-01-01",
    definition: recipe,
    source: { type: "OFFICIAL" },
    config: {
      name: "Node.js " + recipe.version,
      runtimeMode: "NATIVE",
      memoryMiB: 512,
    },
    node: {
      ...context,
      environmentId: "env",
      environmentVersion: "4",
      makeDefault: true,
    },
    paths: { root: "/env/nvm/versions/node/" + recipe.version },
    checks: [{ status: "PASSED" }],
  });
  for (const drift of [true, false]) {
    const h = harness({
      env,
      request: (p) =>
        p.endsWith("/software-install-plans")
          ? Promise.resolve({
              ...makePlan(),
              ...(drift
                ? { definition: { ...recipe, revision: "changed" } }
                : {}),
            })
          : undefined,
    });
    await flush();
    h.flow.job.value = { id: "init-job", status: "SUCCEEDED" };
    h.flow.initialIntent.value = structuredClone(intent);
    await h.flow.continueInitialInstallation();
    const submitted = h.calls.filter((c) => c.path.endsWith("/submit"));
    assert.equal(submitted.length, drift ? 0 : 1);
    assert.equal(h.flow.initialIntent.value, undefined);
    if (!drift)
      assert.deepEqual(
        JSON.parse(JSON.stringify(h.flow.continuationRetry.value)),
        {
          path: "/software-install-plans/second/submit",
          key: "stable-second-key",
        },
      );
    else assert.match(h.flow.error.value.message, /不一致/);
    h.dispose();
  }
});

test("version selection ignores uninstalled history when the same version is installed again", () => {
  const old = {
    softwareVersion: "v24.11.0",
    installationStatus: "UNINSTALLED",
    id: "old",
  };
  const active = { ...old, installationStatus: "READY", id: "active" };
  assert.equal(
    model.nodeVersionRow({ ...base, versions: [old, active] }, "v24.11.0").id,
    "active",
  );
});

test("offline Node preflight reuses selected archive and performs no material network mutation", async () => {
  const material = {
    id: "node-archive",
    available: true,
    recipeKey: "node",
    recipeVersion: recipe.version,
    recipeRevision: recipe.revision,
    runtimeMode: "NATIVE",
  };
  const h = harness({
    request: (p) =>
      p === "/installation-materials" ? Promise.resolve([material]) : undefined,
  });
  await flush();
  h.flow.openAction("INSTALL_NODE");
  await flush();
  h.flow.draft.value.source = "ARCHIVE";
  await h.flow.preflight();
  assert.deepEqual(h.calls.at(-1).data.source, {
    type: "ARCHIVE",
    materialId: "node-archive",
  });
  assert.equal(
    h.calls.some((c) => c.method === "POST" && c.path.includes("materials")),
    false,
  );
  h.dispose();
});

test("cancelled first initialization stops the continuation without submitting Node", async () => {
  const h = harness();
  await flush();
  h.flow.job.value = { id: "cancelled-init", status: "CANCELLED" };
  h.flow.initialIntent.value = { initializationJobId: "cancelled-init" };
  await h.flow.continueInitialInstallation();
  assert.equal(h.flow.initialIntent.value, undefined);
  assert.equal(
    h.calls.some((c) => c.method === "POST"),
    false,
  );
  h.dispose();
});
