import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";

function loadTS(source, imports = {}) {
  const exports = {};
  const code = ts.transpileModule(source, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
  }).outputText;
  new Function("require", "exports", code)((name) => imports[name], exports);
  return exports;
}

const readSrc = (name) => readFile(new URL(`../src/${name}`, import.meta.url), "utf8");

test("cleanObservedVersionText strips Maven jansi /tmp noexec stack trace and extracts Maven version", async () => {
  const softwareMod = loadTS(await readSrc("software.ts"), {});
  const noisy = `Failed to load native library:jansi-2.4.2-c540835401c0e949-libjansi.so. The native library file at /tmp/jansi-2.4.2-c540835401c0e949-libjansi.so is not executable
java.lang.UnsatisfiedLinkError: /tmp/jansi-2.4.2-c540835401c0e949-libjansi.so: failed to map segment from shared object
Apache Maven 3.9.11 (3e54c93a704957b63ee3494413a2b544fd3d825b)
Maven home: /usr/share/maven`;

  const cleaned = softwareMod.cleanObservedVersionText(noisy);
  assert.ok(cleaned.startsWith("Apache Maven 3.9.11"));
  assert.ok(!cleaned.includes("jansi"));
  assert.equal(softwareMod.softwareVersionText(noisy), "3.9.11");
});

test("style.css excludes .el-descriptions__cell from global td nowrap rule", async () => {
  const css = await readSrc("style.css");
  assert.match(css, /td:not\(\.el-table__cell\):not\(\.el-descriptions__cell\)/);
});

test("saving a pulled YAML draft records only the project draft and credential reference", async () => {
  const src = (await readSrc("ProjectEnvironmentBinding.vue"))
    .split('<script setup lang="ts">')[1]
    .split("</script>")[0];
  const calls = [];
  const fullProject = {
    id: "app",
    version: "8",
    archived: false,
    modules: [
      {
        id: "api",
        name: "platform-api",
        catalog: {
          kind: "BACKEND",
          environment: "JDK 21",
          startCommand: "java -jar app.jar",
          runtimeMode: "NATIVE",
          defaultPort: 18080,
          defaultProfile: "test",
          configTemplates: [
            {
              filename: "application-test.yml",
              profile: "test",
              format: "yml",
              content: "server:\n  port: 18080\n",
            },
          ],
        },
      },
    ],
    environments: [],
  };

  let startup;
  let unmount;
  const scope = vue.effectScope();
  const code = ts.transpileModule(
    `${src}\nexport const flow = { configFiles, activeConfigFile, pullRepoConfigTemplates, removeActiveConfigFile, save, formError };`,
    { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } },
  ).outputText;
  const exports = {};
  const request = async (path, method, data) => {
    if (path === "/hosts") return [{ id: "h1", name: "App Host", address: "10.0.0.8" }];
    if (path === "/credentials") return [{ id: "c1", revision: "1", name: "db-pwd", kind: "PASSWORD", revokedAt: "" }];
    calls.push([path, method, data]);
    if (path.endsWith("/config-templates")) {
      return {
        templates: [
          {
            filename: "application-test.yml",
            profile: "test",
            content: "server:\n  port: 18080\nspring:\n  datasource:\n    password: ${SFOPS_CRED:db-pwd}\n",
          },
        ],
      };
    }
    return fullProject;
  };
  scope.run(() =>
    new Function("require", "exports", "defineProps", "defineEmits", code)(
      (name) =>
        ({
          vue: { ...vue, onMounted: (fn) => { startup = fn; }, onUnmounted: (fn) => { unmount = fn; } },
          "./api": { request, ApiError: class extends Error {} },
          "element-plus": { ElMessage: { success() {}, info() {} } },
          "./listPages": { requestAllPages: async () => [] },
          "./applicationProjects": { isProjectBuildService: () => false },
        }[name]),
      exports,
      () => ({ project: fullProject, moduleId: "api" }),
      () => () => {},
    ),
  );
  try {
    await startup();
    assert.equal(exports.flow.activeConfigFile.value.filename, "application-test.yml");
    await exports.flow.pullRepoConfigTemplates(true);
    assert.ok(exports.flow.activeConfigFile.value.content.includes("${SFOPS_CRED:db-pwd}"));
    await exports.flow.save();
    assert.equal(calls.length, 2);
    assert.equal(calls[0][0], "/application-projects/app/modules/api/config-templates");
    assert.deepEqual(calls[1].slice(0, 2), ["/application-projects/app/service-instances", "POST"]);
    assert.deepEqual(calls[1][2].configDrafts, [{
      filename: "application-test.yml",
      content: "server:\n  port: 18080\nspring:\n  datasource:\n    password: ${SFOPS_CRED:db-pwd}\n",
    }]);
    assert.equal("configDir" in calls[1][2], false);
    exports.flow.removeActiveConfigFile();
    assert.equal(calls.some(([path]) => path.includes("/config-files")), false);
  } finally {
    unmount();
    scope.stop();
  }
});
