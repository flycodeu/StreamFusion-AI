import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";
import { buildEnvironmentPatch } from "../src/features/project-delivery/environmentPatch.ts";

async function componentScript(name) {
  const file = await readFile(new URL(`../src/${name}.vue`, import.meta.url), "utf8");
  return file.split('<script setup lang="ts">')[1].split("</script>")[0];
}

const connections = await componentScript("ProjectJenkinsConnection");
const environments = await componentScript("ProjectEnvironmentBinding");
const monitor = { configured: true, hasToken: true, softwareId: "builder", username: "automation", version: "7",
  instances: [{ id: "builder", name: "Jenkins", hostName: "build" }, { id: "other", name: "CI", hostName: "other" }] };
const project = { id: "app", version: "3", archived: false, modules: [{ id: "api", name: "platform-api" }], environments: [] };

function mount(source, exposes, props, request, lists = {}) {
  let startup;
  let unmount;
  const events = [];
  const scope = vue.effectScope();
  const code = ts.transpileModule(source + "\nexport const flow = {" + exposes + "};", {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
  }).outputText;
  const exports = {};
  scope.run(() => new Function("require", "exports", "defineProps", "defineEmits", code)(name => ({
    vue: { ...vue, onMounted: fn => { startup = fn; }, onUnmounted: fn => { unmount = fn; } },
    "./api": { request, ApiError: class extends Error {} }, "element-plus": { ElMessage: { success() {} } },
    "./listPages": { requestAllPages: async path => lists[path] || [] },
    "./applicationProjects": { isProjectBuildService: () => false },
    "./features/project-delivery/environmentPatch": { buildEnvironmentPatch },
  }[name]), exports, () => props, () => (...args) => events.push(args)));
  return { flow: exports.flow, startup, events, close() { unmount(); scope.stop(); } };
}

test("project Jenkins keeps existing token only for the same account and service, and never fills the secret field", async t => {
  const calls = [];
  const h = mount(connections, "edit, save, softwareId, username, token, valid, open", { projectId: "app", archived: false },
    async (path, method, data) => { calls.push([path, method, { ...data }]); return monitor; });
  t.after(h.close);
  await h.startup();
  h.flow.edit();
  assert.equal(h.flow.token.value, "");
  assert.equal(h.flow.valid.value, true);
  await h.flow.save();
  assert.deepEqual(calls.at(-1), ["/application-projects/app/jenkins-connection", "POST", {
    expectedVersion: "7", softwareId: "builder", username: "automation", token: "", buildAgentLabel: "sfops-build",
  }]);
  h.flow.edit();
  h.flow.username.value = "other-name";
  assert.equal(h.flow.valid.value, false);
  h.flow.username.value = monitor.username;
  h.flow.softwareId.value = "other";
  assert.equal(h.flow.valid.value, false);
  await h.flow.save();
  assert.equal(calls.length, 2);
  h.flow.token.value = "sample-new-token";
  await h.flow.save();
  assert.equal(calls.at(-1)[2].token, "sample-new-token");
  assert.equal(h.flow.token.value, "");
});

test("testing and saving Jenkins use the project build agent label", async t => {
  const calls = [];
  const h = mount(connections, "edit, testConnection, save, buildAgentLabel", { projectId: "app", archived: false },
    async (path, method, data) => { calls.push([path, method, { ...data }]); return { ...monitor, buildAgentLabel: "ci-linux" }; });
  t.after(h.close);
  await h.startup();
  h.flow.edit();
  assert.equal(h.flow.buildAgentLabel.value, "ci-linux");
  h.flow.buildAgentLabel.value = " build-jdk21 ";
  await h.flow.testConnection();
  assert.equal(calls.at(-1)[0], "/application-projects/app/jenkins-connection/test");
  assert.equal(calls.at(-1)[2].buildAgentLabel, "build-jdk21");
  await h.flow.save();
  assert.equal(calls.at(-1)[0], "/application-projects/app/jenkins-connection");
  assert.equal(calls.at(-1)[2].buildAgentLabel, "build-jdk21");
});

test("running configuration scopes its release targets and preserves a historical version without editing it", async t => {
  const calls = [];
  const row = { id: "test-api", moduleId: "api", hostId: "east", recordedVersion: "historical-build-2" };
  const environment = { name: "test", id: "test", deployments: [row] };
  const targets = [
    { id: "match", name: "Api", targetId: "east", config: { environment: "test" } },
    { id: "host mismatch", targetId: "west", config: { environment: "test" } },
    { id: "env mismatch", targetId: "east", config: { environment: "prod" } },
  ];
  const h = mount(environments, "targetOptions, managedId, save", { project, moduleId: "api", environment, registration: row },
    async (path, method, data) => {
      if (path === "/hosts") return [{ id: "east", name: "East server" }];
      calls.push([path, method, data]); return project;
    }, { "/instances": targets });
  t.after(h.close);
  await h.startup();
  assert.deepEqual(h.flow.targetOptions.value.map(item => item.id), ["match"]);
  h.flow.managedId.value = "match";
  await h.flow.save();
  assert.deepEqual(calls[0], ["/application-projects/app/service-instances", "POST", {
    id: row.id, version: "3", moduleId: "api", environment: "test", hostId: "east", recordedVersion: row.recordedVersion, managedInstanceId: "match",
  }]);
});

test("running configuration deletes an environment only after explicit confirmation", async t => {
  const calls = [];
  const row = { id: "test-api", moduleId: "api", hostId: "east" };
  const h = mount(environments, "removeEnvironment, removing", { project, moduleId: "api", registration: row },
    async (path, method) => { calls.push([path, method]); return project; });
  t.after(h.close);
  await h.flow.removeEnvironment();
  assert.equal(calls.length, 0);
  h.flow.removing.value = true;
  await h.flow.removeEnvironment();
  assert.deepEqual(calls, [["/application-projects/app/service-instances/test-api","DELETE"]]);
});

test("adding an algorithm server stays in the environment and does not overwrite the existing host", async t => {
  const calls = [];
  const environment = { id: "test", name: "test", deployments: [
    { id: "runtime-east", moduleId: "runtime", hostId: "east", servicePort: 8101 },
    { id: "api-west", moduleId: "api", hostId: "west", servicePort: 8080 },
  ] };
  const algorithmProject = { ...project, environments: [environment], modules: [
    ...project.modules,
    { id: "runtime", name: "runtime", catalog: { kind: "ALGORITHM", runtimeMode: "STATIC", defaultPort: 8101 } },
  ] };
  const h = mount(environments, "availableHosts, hostId, formError, save, previewAccessUrl, runtimeId, runtimeOptions, deployMode, canUseStaticMode", {
    project: algorithmProject, moduleId: "runtime", environment, addingAlgorithmHost: true,
  }, async (path, method, data) => {
    if (path === "/hosts") return [{ id: "east", name: "East" }, { id: "west", name: "West", address: "192.0.2.10" }];
    if (path === "/credentials") return [];
    calls.push([path, method, data]);
    return algorithmProject;
  }, { "/software-instances": [
    { id: "east-runtime", hostId: "east", runtimeKind: "SYSTEMD", observation: { identityHash: "verified" } },
    { id: "west-runtime", hostId: "west", runtimeKind: "SYSTEMD", observation: { identityHash: "verified" } },
    { id: "unknown-runtime", hostId: "west", runtimeKind: "SYSTEMD" },
  ],
  });
  t.after(h.close);
  await h.startup();
  assert.deepEqual(h.flow.availableHosts.value.map(item => item.id), ["west"]);
  assert.equal(h.flow.deployMode.value, "NATIVE", "a historical module setting cannot default a new algorithm registration to STATIC");
  assert.equal(h.flow.canUseStaticMode.value, false);
  h.flow.hostId.value = "east";
  await h.flow.save();
  assert.equal(calls.length, 0);
  assert.match(h.flow.formError.value, /尚未登记/);
  h.flow.hostId.value = "west";
  await vue.nextTick();
  assert.equal(h.flow.previewAccessUrl.value, "");
  assert.deepEqual(h.flow.runtimeOptions.value.map(item => item.id), ["west-runtime"]);
  h.flow.runtimeId.value = "west-runtime";
  await h.flow.save();
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0].slice(0, 2), ["/application-projects/app/service-instances", "POST"]);
  assert.equal(calls[0][2].id, undefined);
  assert.equal(calls[0][2].moduleId, "runtime");
  assert.equal(calls[0][2].environment, "test");
  assert.equal(calls[0][2].hostId, "west");
  assert.equal(calls[0][2].deployMode, "NATIVE");
  assert.equal(calls[0][2].servicePort, 8101);
  assert.equal(calls[0][2].runtimeSoftwareId, "west-runtime");
});

test("a historical static algorithm registration retains its mode until the operator converts it", async t => {
  const calls = [];
  const row = { id: "legacy", moduleId: "runtime", hostId: "east", deployMode: "STATIC",
    nginx: { siteName: "legacy", serverName: "legacy.example", listenPort: 8090, spa: true, proxyRules: [] } };
  const environment = { id: "test", name: "test", deployments: [row] };
  const algorithmProject = { ...project, environments: [environment], modules: [
    { id: "runtime", name: "runtime", catalog: { kind: "ALGORITHM" } },
  ] };
  const h = mount(environments, "deployMode, canUseStaticMode, save", {
    project: algorithmProject, moduleId: "runtime", environment, registration: row,
  }, async (path, method, data) => {
    if (path === "/hosts") return [{ id: "east", name: "East" }];
    if (path === "/credentials") return [];
    calls.push([path, method, data]);
    return algorithmProject;
  });
  t.after(h.close);
  await h.startup();
  assert.equal(h.flow.canUseStaticMode.value, true);
  assert.equal(h.flow.deployMode.value, "STATIC");
  await h.flow.save();
  assert.equal(calls[0][2].deployMode, undefined);
  h.flow.deployMode.value = "DOCKER";
  await vue.nextTick();
  await h.flow.save();
  assert.equal(calls[1][2].deployMode, "DOCKER");
  assert.deepEqual(calls[1][2].nginx, {});
});

test("one-stop NATIVE backend configuration saves variables, credentials, start command and port", async t => {
  const calls = [];
  const fullProject = {
    id: "app", version: "5", archived: false,
    modules: [{
      id: "api", name: "platform-api",
      catalog: {
        kind: "BACKEND", environment: "JDK 21", startCommand: "sh mvnw spring-boot:run",
        runtimeMode: "NATIVE", defaultPort: 18080, healthPath: "/actuator/health", defaultProfile: "test",
        profiles: ["test", "prod"],
        envHints: [
          { name: "DB_URL", defaultValue: "jdbc:mysql://127.0.0.1:3306/app", profile: "test" },
          { name: "DB_PASSWORD", sensitive: true, profile: "test" },
        ],
      },
    }],
    environments: [],
  };
  const h = mount(
    environments,
    "hostId, deployMode, servicePort, profile, startCommand, variables, importRecognizedHints, activeSecretCredentials, save, formError",
    { project: fullProject, moduleId: "api" },
    async (path, method, data) => {
      if (path === "/hosts") return [{ id: "h1", name: "App Host", address: "10.0.0.8" }];
      if (path === "/credentials") return [{ id: "cred-1", revision: "2", name: "MySQL Pass", kind: "PASSWORD", revokedAt: "" }];
      calls.push([path, method, data]);
      return fullProject;
    },
    { "/instances": [], "/software-instances": [] },
  );
  t.after(h.close);
  await h.startup();
  assert.equal(h.flow.deployMode.value, "NATIVE");
  assert.equal(h.flow.servicePort.value, 18080);
  assert.equal(h.flow.profile.value, "test");
  h.flow.importRecognizedHints();
  assert.equal(h.flow.variables.value.length, 2);
  // Plain sensitive or empty credential ref should be blocked before POST.
  await h.flow.save();
  assert.equal(calls.length, 0);
  assert.match(h.flow.formError.value, /凭据引用/);
  h.flow.variables.value[1].credentialRef = "cred-1:2";
  h.flow.startCommand.value = "java -Xms256m -jar app.jar --spring.profiles.active=test";
  await h.flow.save();
  assert.equal(calls.length, 1);
  assert.equal(calls[0][0], "/application-projects/app/service-instances");
  assert.deepEqual(calls[0][2].variables, [
    { name: "DB_URL", value: "jdbc:mysql://127.0.0.1:3306/app" },
    { name: "DB_PASSWORD", credentialId: "cred-1", credentialRevision: "2" },
  ]);
  assert.equal(calls[0][2].deployMode, "NATIVE");
  assert.equal(calls[0][2].servicePort, 18080);
  assert.equal(calls[0][2].profile, "test");
  assert.equal(calls[0][2].startCommand, "java -Xms256m -jar app.jar --spring.profiles.active=test");
});

test("one-stop STATIC frontend configuration pre-fills Nginx instance and backend proxy rules", async t => {
  const calls = [];
  const fullProject = {
    id: "app", version: "6", archived: false,
    modules: [
      { id: "api", name: "platform-api", catalog: { kind: "BACKEND", runtimeMode: "NATIVE" } },
      {
        id: "web", name: "platform-web",
        catalog: {
          kind: "FRONTEND", runtimeMode: "STATIC",
          proxyHints: [{ location: "/api", stripPrefix: true, targetPort: 8080 }],
        },
      },
    ],
    environments: [],
  };
  const software = [
    { id: "ng-docker", name: "Nginx Docker", hostId: "h1", softwareType: "NGINX", runtimeKind: "DOCKER", managementMode: "MANAGED", actualState: "RUNNING", installationStatus: "READY" },
    { id: "ng-1", name: "Nginx", hostId: "h1", softwareType: "NGINX", runtimeKind: "SYSTEMD", managementMode: "MANAGED", actualState: "RUNNING", installationStatus: "READY" },
  ];
  const h = mount(
    environments,
    "deployMode, nginxSoftwareId, nginxListenPort, nginxProxyRules, previewAccessUrl, save",
    { project: fullProject, moduleId: "web" },
    async (path, method, data) => {
      if (path === "/hosts") return [{ id: "h1", name: "Web Host", address: "100.76.165.8" }];
      if (path === "/credentials") return [];
      calls.push([path, method, data]);
      return fullProject;
    },
    { "/instances": [], "/software-instances": software },
  );
  t.after(h.close);
  await h.startup();
  assert.equal(h.flow.deployMode.value, "STATIC");
  assert.equal(h.flow.nginxSoftwareId.value, "ng-1");
  assert.equal(h.flow.previewAccessUrl.value, "http://100.76.165.8");
  assert.equal(h.flow.nginxProxyRules.value.length, 1);
  assert.equal(h.flow.nginxProxyRules.value[0].targetModuleId, "api");
  await h.flow.save();
  assert.equal(calls.length, 1);
  assert.equal(calls[0][2].deployMode, "STATIC");
  assert.deepEqual(calls[0][2].nginx, {
    nginxSoftwareId: "ng-1",
    siteName: "platform-web-test",
    serverName: "_",
    listenPort: 80,
    spa: true,
    proxyRules: [{ location: "/api", targetModuleId: "api", stripPrefix: true }],
  });
});
