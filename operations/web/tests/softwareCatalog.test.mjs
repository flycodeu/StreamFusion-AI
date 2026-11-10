import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import ts from "typescript";

const source = await readFile(new URL("../src/softwareCatalog.ts", import.meta.url), "utf8");
const module = { exports: {} };
new Function("exports", ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText)(module.exports);
const { catalogDefaultRecipe, installedPythonPaths, catalogInstallState, catalogPending, catalogPendingRecords, catalogVersionMatches, catalogInstances } = module.exports;
const recipe = { key: "java", version: "21", runtimeModes: ["NATIVE", "DOCKER"] };
const instance = (version, extra = {}) => ({ id: version, hostId: "hp", softwareType: "JAVA", softwareVersion: version,
  managementMode: "MANAGED", actualState: "INSTALLED", installationStatus: "READY", createdAt: "2026-11-09T00:00:00Z",
  installation: { definition: { ...recipe, version }, config: { runtimeMode: "NATIVE" } }, ...extra });

test("installed environments remain selectable across versions without merging history", () => {
  const items = [instance("8"), instance("21"), instance("17", { actualState: "REMOVED" }), instance("25", { actualState: "UNKNOWN", installationStatus: "FAILED" })];
  assert.equal(catalogInstallState(items, "hp", recipe).label, "已安装");
  assert.equal(catalogInstallState(items, "hp", { ...recipe, version: "25" }).label, "已安装其他版本");
  assert.equal(catalogInstances(items, "hp", "java").length, 2);
  assert.equal(catalogInstallState(items, "other-host", recipe).label, "");
});
test("pending work matches the exact variant and version rather than the latest catalog record", () => {
  const items = [instance("8", { installationStatus: "UNKNOWN", installationJobId: "java8" }), instance("21", { installationStatus: "INSTALLING", installationJobId: "java21" })];
  assert.equal(catalogPending(items, "hp", recipe).installationJobId, "java21");
  assert.equal(catalogPending(items, "hp", { ...recipe, key: "custom-other-java", catalogKey: "java" }), undefined);
  assert.equal(catalogPending(items, "hp", { ...recipe, version: "17" }), undefined);
  assert.deepEqual(catalogPendingRecords(items, "hp", "java").map(item => item.installationJobId).sort(), ["java21", "java8"]);
  assert.equal(catalogPendingRecords(items, "other-host", "java").length, 0);
});
test("externally discovered Java majors are recognized without guessing exact package revisions", () => {
  const detected = instance("", { installation: undefined, installationStatus: "", observation: { state: "INSTALLED", version: 'openjdk version "21.0.12"' } });
  assert.equal(catalogVersionMatches(detected, recipe), true);
  assert.equal(catalogVersionMatches(detected, { ...recipe, version: "8" }), false);
  assert.equal(catalogVersionMatches({ ...detected, observation: { version: 'java version "1.8.0_504"' } }, { ...recipe, version: "8" }), true);
});
test("unrelated custom software and released objects cannot become installed by display name", () => {
  const other = instance("21", { softwareType: "OTHER", name: "Java", installation: undefined });
  assert.equal(catalogInstallState([other], "hp", recipe).instances.length, 0);
  assert.equal(catalogInstallState([instance("21", { managementMode: "RELEASED" })], "hp", recipe).instances.length, 0);
});

test("ytlabel defaults to Python 3.12 while an explicit old version remains selected", () => {
  const old = { key: "custom-ytlabel-native-online", catalogKey: "ytlabel", version: "4.0", sourceModes: ["ONLINE"] };
  const compatible = { ...old, key: "custom-ytlabel-py312-native-online", version: "4.0-python312" };
  assert.equal(catalogDefaultRecipe([old, compatible]), compatible);
  assert.equal(catalogDefaultRecipe([old, compatible], "4.0"), old);
  assert.equal(catalogDefaultRecipe([old]), old);
});

test("Python path candidates come only from installed native Python on the selected host", () => {
  const python = { hostId: "hp", softwareType: "PYTHON", runtimeKind: "TOOL", managementMode: "MANAGED", actualState: "INSTALLED", observation: { state: "INSTALLED", executablePath: "/opt/python/bin/python3" } };
  assert.deepEqual(installedPythonPaths([python, python, { ...python, hostId: "other" }, { ...python, runtimeKind: "DOCKER" },
    { ...python, managementMode: "RELEASED" }, { ...python, observation: { state: "MISSING", executablePath: "/usr/bin/python3.10" } },
    { ...python, softwareType: "JAVA" }, { ...python, locator: "/usr/bin/python3", observation: undefined }], "hp"), ["/opt/python/bin/python3"]);
});

test("read-only external Label Studio maps to its catalog without an Ops installation receipt", () => {
  const definition = { key: "custom-label-studio-native-online", catalogKey: "label-studio", version: "1.22.0", runtimeModes: ["NATIVE"] };
  const external = { hostId: "hp", softwareType: "LABEL_STUDIO", managementMode: "OBSERVE", runtimeKind: "SYSTEMD", locator: "label-studio.service", actualState: "RUNNING", observation: { state: "RUNNING", version: "1.22.0" } };
  assert.equal(catalogInstallState([external], "hp", definition).label, "已安装");
  assert.equal(catalogInstallState([external], "hp", { ...definition, version: "1.21.0" }).label, "已安装其他版本");
  assert.equal(catalogInstallState([{ ...external, observation: { state: "RUNNING", version: "" } }], "hp", definition).label, "已安装 · 版本待确认");
  assert.equal(catalogInstallState([{ ...external, actualState: "MISSING" }], "hp", definition).label, "");
  assert.equal(external.managementMode, "OBSERVE");
  assert.equal(external.installation, undefined);
});

test("fresh installed-package evidence marks restarting Label Studio installed without claiming it is running", () => {
  const definition = { key: "custom-label-studio-native-online", catalogKey: "label-studio", version: "1.22.0", runtimeModes: ["NATIVE"] };
  const external = { hostId: "hp", softwareType: "LABEL_STUDIO", managementMode: "OBSERVE", runtimeKind: "SYSTEMD", locator: "label-studio.service", actualState: "UNKNOWN", freshness: "FRESH",
    observation: { state: "UNKNOWN", version: "1.22.0", packageName: "label-studio", versionSource: "PACKAGE", versionStatus: "INSTALLED_PACKAGE" } };
  assert.equal(catalogInstallState([external], "hp", definition).label, "已安装");
  assert.equal(catalogInstallState([external], "hp", { ...definition, version: "1.23.2" }).label, "已安装其他版本");
  assert.equal(external.observation.state, "UNKNOWN");
  for (const excluded of [{ managementMode: "RELEASED" }, { actualState: "REMOVED" }, { actualState: "MISSING" }, { installationStatus: "UNINSTALLED" }, { freshness: "STALE" }])
    assert.equal(catalogInstallState([{ ...external, ...excluded }], "hp", definition).label, "");
  assert.equal(catalogInstallState([{ ...external, softwareVersion: "1.22.0", observation: { state: "UNKNOWN", version: "1.22.0" } }], "hp", definition).label, "");
});
