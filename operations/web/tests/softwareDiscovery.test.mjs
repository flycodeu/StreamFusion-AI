import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";

const compile = source => ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
const softwareModule = { exports: {} };
new Function("module", "exports", compile(await readFile(new URL("../src/software.ts", import.meta.url), "utf8")))(softwareModule, softwareModule.exports);

async function component(name, values, request, exposed) {
  const content = await readFile(new URL(`../src/${name}.vue`, import.meta.url), "utf8");
  const source = content.split('<script setup lang="ts">')[1].split("</script>")[0];
  const props = vue.reactive(values), events = [], cleanup = [], scope = vue.effectScope(), module = { exports: {} };
  scope.run(() => new Function("require", "module", "exports", "defineProps", "defineEmits", compile(source + `\nexport { ${exposed} };`))(
    name => name === "vue" ? { ...vue, onUnmounted: fn => cleanup.push(fn) } : name === "./software" ? softwareModule.exports : name === "./api" ? { request, ApiError: Error } : {},
    module, module.exports, () => props, () => (event, value) => events.push([event, value]),
  ));
  return { props, events, panel: module.exports, dispose() { cleanup.forEach(fn => fn()); scope.stop(); } };
}

test("Label Studio discovery only prefills registration and explicit save stays read-only", async () => {
  const host = { id: "host-a", version: "1", maintenance: false };
  const candidate = { name: "Label Studio", softwareType: "LABEL_STUDIO", runtimeKind: "SYSTEMD", locator: "label-studio.service", state: "RUNNING", version: "1.22.0" };
  const calls = [];
  const discovery = await component("SoftwareDiscoveryDialog", { open: false, hosts: [host], hostId: host.id }, async (path, method, data) => {
    calls.push({ path, method, data });
    return { hostId: host.id, hostVersion: host.version, candidates: [candidate], checks: [] };
  }, "discover, register");
  discovery.props.open = true;
  await vue.nextTick();
  await discovery.panel.discover();
  discovery.panel.register(candidate);
  const draft = discovery.events.find(([event]) => event === "register")[1];
  assert.equal(draft.softwareType, "LABEL_STUDIO");
  assert.equal(draft.locator, "label-studio.service");
  assert.equal(calls.length, 1);
  assert.equal(calls[0].path, "/hosts/host-a/software-discovery");
  const registration = await component("SoftwareRegistrationDialog", { open: false, hosts: [host], hostId: host.id, prefill: draft }, async (path, method, data) => {
    calls.push({ path, method, data });
    return { ...data, id: "external-label-studio", managementMode: "OBSERVE" };
  }, "draft, availableTypes, save");
  registration.props.open = true;
  await vue.nextTick();
  assert.ok(registration.panel.availableTypes.value.some(([key, label]) => key === "LABEL_STUDIO" && label === "Label Studio"));
  assert.equal(registration.panel.draft.value.softwareType, "LABEL_STUDIO");
  assert.equal(calls.length, 1);
  await registration.panel.save();
  assert.equal(calls.length, 2);
  assert.equal(calls[1].path, "/software-instances");
  assert.equal(calls[1].data.softwareType, "LABEL_STUDIO");
  assert.equal(calls[1].data.installation, undefined);
  assert.equal(calls[1].data.managementMode, undefined);
  assert.equal(registration.events.find(([event]) => event === "saved")[1].managementMode, "OBSERVE");
  discovery.dispose(); registration.dispose();
});
