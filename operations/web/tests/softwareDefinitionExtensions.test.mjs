import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";

const source = (await readFile(new URL("../src/SoftwareDefinitionExtensions.vue", import.meta.url), "utf8")).split('<script setup lang="ts">')[1].split("</script>")[0];
function editor(definition) {
  const exports = {};
  const js = ts.transpileModule(source + "\nexport { validationError };", { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText;
  new Function("require", "exports", "defineProps", "defineExpose", js)(name => name === "vue" ? vue : name === "./webAccess" ? { webDefinitionError: () => "" } : {}, exports, () => ({ definition }), () => {});
  return exports;
}

test("configuration templates require explicit replacement and recovery while queries stay read only", () => {
  const step = { name: "应用", script: "true", timeoutSeconds: 60 };
  const op = { key: "configure", name: "修改配置", rebind: true, steps: [step], verifySteps: [step] };
  const definition = { management: { scriptPrelude: "check_owner() { true; }", operations: [op] } };
  const form = editor(definition);
  assert.match(form.validationError(), /失败恢复/);
  op.rollbackSteps = [step];
  assert.equal(form.validationError(), "");
  op.key = "diagnostics";
  assert.match(form.validationError(), /configure/);
  op.key = "configure"; op.readOnly = true;
  assert.match(form.validationError(), /configure/);
  op.readOnly = false; definition.management.scriptPrelude = "汉".repeat(6000);
  assert.equal(form.validationError(), "");
  definition.management.scriptPrelude = "汉".repeat(11000);
  assert.match(form.validationError(), /32 KiB/);
});
