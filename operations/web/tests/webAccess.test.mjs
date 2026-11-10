import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";

const compile = source => ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText;
const access = {};
new Function("exports", compile(await readFile(new URL("../src/webAccess.ts", import.meta.url), "utf8")))(access);
const recipe = { kind: "SERVICE", port: 8080, web: { scheme: "http", path: "/", embed: true, urlParameter: "PUBLIC_URL" }, parameters: [{ key: "PUBLIC_URL", type: "TEXT" }] };
const host = { kind: "SSH", address: "192.0.2.20" };
const config = { bindAddress: "192.0.2.20", hostPort: 18080, ports: {}, parameters: {} };
const item = () => ({ id: "label-instance", name: "标注平台", managementMode: "MANAGED", installationStatus: "READY", actualState: "RUNNING", freshness: "STALE", installation: { definition: structuredClone(recipe), config: structuredClone(config) } });

test("web entry uses the frozen host port, selected TCP console and IPv6 without probing a server", () => {
  assert.equal(access.installationWebAccess(recipe, config, host).url, "http://192.0.2.20:18080/");
  assert.equal(access.installationWebAccess(recipe, { ...config, bindAddress: "0.0.0.0" }, host).url, "http://192.0.2.20:18080/");
  assert.equal(access.installationWebAccess(recipe, { ...config, bindAddress: "10.0.0.20" }, host).url, "http://192.0.2.20:18080/");
  assert.equal(access.installationWebAccess(recipe, { ...config, bindAddress: "::" }, { ...host, address: "2001:db8::8" }).url, "http://[2001:db8::8]:18080/");
  const consoleRecipe = { ...recipe, web: { scheme: "https", portKey: "console", path: "/ui/" }, ports: [{ key: "console", protocol: "TCP" }] };
  assert.equal(access.installationWebAccess(consoleRecipe, { ...config, ports: { console: 18443 } }, host).url, "https://192.0.2.20:18443/ui/");
  assert.equal(access.installationWebAccess(consoleRecipe, config, host).url, "");
});

test("remote loopback never opens the operator computer, while a local controller can be reached explicitly", () => {
  for (const bindAddress of ["127.0.0.1", "127.2.3.4", "::1", "::ffff:127.0.0.1", "localhost", "0x7f000001"])
    assert.equal(access.installationWebAccess(recipe, { ...config, bindAddress }, host, "localhost").url, "", bindAddress);
  const local = { kind: "LOCAL", address: "127.0.0.1" };
  assert.equal(access.installationWebAccess(recipe, { ...config, bindAddress: "127.0.0.1" }, local, "localhost").url, "http://127.0.0.1:18080/");
  assert.equal(access.installationWebAccess(recipe, { ...config, bindAddress: "127.0.0.1" }, local, "ops.example.com").url, "");
  assert.equal(access.installationWebAccess(recipe, { ...config, bindAddress: "0.0.0.0" }, local, "ops.example.com").url, "http://ops.example.com:18080/");
  const explicit = { ...config, bindAddress: "127.0.0.1", parameters: { PUBLIC_URL: "https://annotation.example.com/label/" } };
  assert.equal(access.installationWebAccess(recipe, explicit, host).url, "https://annotation.example.com/label/");
  for (const PUBLIC_URL of ["http://127.0.0.1:18080/", "http://localhost:18080/", "http://[::1]:18080/", "http://0.0.0.0:18080/"])
    assert.equal(access.installationWebAccess(recipe, { ...config, parameters: { PUBLIC_URL } }, host).url, "");
});

test("web URLs reject authentication, token queries, fragments and literal or encoded control characters", () => {
  const invalid = ["javascript:alert(1)", "//annotation.example.com", "https://user:secret@annotation.example.com/", "https://annotation.example.com/?token=secret", "https://annotation.example.com/#token", "https://annotation.example.com/?", "https://annotation.example.com/#", " https://annotation.example.com/", "https://annotation.example.com/path%20name", "https://annotation.example.com/%0a", "https://annotation.example.com/%E2%80%A8", "https://annotation.example.com/\\evil", "https://annotation.example.com:", "https://annotation.example.com/%xx"];
  for (const value of invalid) assert.equal(access.installationWebAccess(recipe, { ...config, parameters: { PUBLIC_URL: value } }, host).url, "", value);
  for (const path of ["//evil.example.com", "/%2fevil.example.com", "/%0d", "/%20", "/?token=x", "/#x", "/\\evil"])
    assert.ok(access.webDefinitionError({ ...recipe, web: { ...recipe.web, path } }), path);
  assert.ok(access.webDefinitionError({ ...recipe, parameters: [{ key: "PUBLIC_URL", type: "CREDENTIAL" }] }));
  assert.ok(access.webDefinitionError({ ...recipe, kind: "TOOL" }));
  assert.ok(access.webDefinitionError({ ...recipe, web: { scheme: "http", portKey: "unconfigured" } }));
  assert.ok(access.webDefinitionError({ ...recipe, ports: [{ key: "main", protocol: "UDP" }] }));
  assert.equal(access.webDefinitionError({ ...recipe, defaultBindAddress: "0.0.0.0" }), "");
  assert.match(access.webDefinitionError({ ...recipe, web: undefined, defaultBindAddress: "0.0.0.0" }), /启用 Web 入口/);
  assert.ok(access.webDefinitionError({ ...recipe, defaultBindAddress: "example.com" }));
});

test("the entry follows confirmed running state, retains a stale observation link, and removes uninstalled instances", () => {
  const instance = item();
  assert.ok(access.softwareWebAccess(instance, host).url);
  for (const actualState of ["STOPPED", "UNKNOWN", "MISSING", "FAILED", "REMOVED"])
    assert.equal(access.softwareWebAccess({ ...instance, actualState }, host).url, "");
  for (const installationStatus of ["INSTALLING", "FAILED", "UNKNOWN", "UNINSTALLED"])
    assert.equal(access.softwareWebAccess({ ...instance, installationStatus }, host).url, "");
  assert.equal(access.softwareWebAccess({ ...instance, managementMode: "RELEASED" }, host).url, "");
  assert.equal(access.softwareWebAccess({ ...instance, installation: undefined }, host), undefined);
});

test("closing a preview or changing software context unloads the iframe instead of retaining its session", async () => {
  const component = await readFile(new URL("../src/SoftwareWebAccess.vue", import.meta.url), "utf8");
  const script = component.split('<script setup lang="ts">')[1].split("</script>")[0];
  const props = vue.reactive({ item: item(), host, active: true, compact: false });
  const exports = {}, scope = vue.effectScope();
  scope.run(() => new Function("require", "exports", "defineProps", "withDefaults", compile(script + "\nexport { preview, previewURL, togglePreview, access };"))(name => name === "vue" ? vue : name === "./webAccess" ? access : {}, exports, () => props, value => value));
  try {
    assert.equal(exports.preview.value, false, "no iframe loads merely from opening details");
    exports.togglePreview();
    props.item = item(); await vue.nextTick();
    assert.equal(exports.preview.value, true, "a polling snapshot with the same address does not reload the embedded application");
    props.item.actualState = "UNKNOWN"; await vue.nextTick();
    assert.equal(exports.access.value.url, "", "unconfirmed health prevents a new entry");
    assert.equal(exports.previewURL.value, "http://192.0.2.20:18080/");
    assert.equal(exports.preview.value, true, "a temporary observation failure cannot discard unsaved annotation state");
    props.active = false; await vue.nextTick();
    assert.equal(exports.preview.value, false);
    assert.equal(exports.previewURL.value, "");
    props.active = true; await vue.nextTick();
    props.item.actualState = "RUNNING"; await vue.nextTick(); exports.togglePreview();
    props.item.installation.config.hostPort = 18081; await vue.nextTick();
    assert.equal(exports.preview.value, false);
    assert.equal(exports.access.value.url, "http://192.0.2.20:18081/");
  } finally { scope.stop(); }
});

test("a recipe's declared default bind address is used without changing the selected host", async () => {
  const installation = {};
  new Function("exports", compile(await readFile(new URL("../src/serviceInstallation.ts", import.meta.url), "utf8")))(installation);
  const definition = { ...recipe, key: "custom-ytlabel-native-online", runtimeModes: ["NATIVE"], defaultBindAddress: "127.0.0.1", name: "ytlabel" };
  assert.equal(installation.initialServiceConfiguration(definition, "192.0.2.20", "NATIVE").bindAddress, "127.0.0.1");
  assert.equal(installation.initialServiceConfiguration({ ...definition, defaultBindAddress: undefined }, "192.0.2.20", "NATIVE").bindAddress, "0.0.0.0");
  assert.equal(installation.initialServiceConfiguration({ ...definition, defaultBindAddress: undefined, web: undefined }, "192.0.2.20", "NATIVE").bindAddress, "192.0.2.20");
});

test("saved network configuration changes immediately update the target-host web entry without changing history", () => {
  const instance = item();
  instance.installation.config.bindAddress = "127.0.0.1";
  const original = structuredClone(instance.installation.config);
  assert.equal(access.softwareWebAccess(instance, host).url, "");
  const updated = { ...instance, installation: { ...instance.installation, config: { ...original, bindAddress: "0.0.0.0", hostPort: 19924 } } };
  assert.equal(access.softwareWebAccess(updated, host).url, "http://192.0.2.20:19924/");
  assert.deepEqual(instance.installation.config, original);
  assert.equal(access.softwareWebAccess({ ...updated, actualState: "UNKNOWN" }, host).url, "");
});

test("database ports and command tools do not become web pages without a declared web interface", () => {
  for (const [key, kind, port] of [["mysql", "SERVICE", 3306], ["postgresql", "SERVICE", 5432], ["redis", "SERVICE", 6379], ["java", "TOOL", 0], ["python", "TOOL", 0]])
    assert.equal(access.installationWebAccess({ key, kind, port }, { ...config, hostPort: port }, host), undefined);
  assert.equal(access.installationWebAccess({ ...recipe, kind: "TOOL" }, config, host).url, "");
});

test("declared Web software uses the target host and the selected HTTP port", () => {
  for (const [key, port, portKey] of [["jenkins", 8080, "main"], ["gitlab", 8929, "main"], ["prometheus", 9090, "main"], ["nacos", 8080, "CONSOLE"], ["nginx", 80, "main"]]) {
    const definition = { key, kind: "SERVICE", port, ports: [{ key: portKey, protocol: "TCP" }], defaultBindAddress: "0.0.0.0", web: { scheme: "http", portKey, path: "/" } };
    assert.equal(access.installationWebAccess(definition, { ...config, bindAddress: "0.0.0.0", hostPort: portKey === "main" ? port : 8848, ports: { CONSOLE: port } }, host).url, `http://192.0.2.20${port === 80 ? "" : `:${port}`}/`);
  }
});
