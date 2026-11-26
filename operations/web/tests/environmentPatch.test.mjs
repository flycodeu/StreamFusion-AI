import assert from "node:assert/strict";
import { test } from "node:test";
import { buildEnvironmentPatch } from "../src/features/project-delivery/environmentPatch.ts";

function draft() {
  return {
    version: "3", moduleId: "web", environment: " test ", hostId: "server-a",
    managedInstanceId: "", runtimeSoftwareId: "", deployMode: "NATIVE",
    profile: "", bindAddress: "", servicePort: 0, containerPort: 0, memoryMiB: 0,
    startCommand: "", healthPath: "", dependencyIds: [], variables: [],
    nginx: { nginxSoftwareId: "", siteName: "", serverName: "", listenPort: 80, spa: false, proxyRules: [] },
  };
}

test("an unchanged registration omits optional fields, while explicit clearing sends empties", () => {
  const original = {
    id: "registration-east", moduleId: "web", hostId: "server-a", recordedVersion: "old",
    deployMode: "NATIVE", runtimeSoftwareId: "run-east", variables: [{ name: "PORT", value: "8080" }],
    dependencyIds: ["db"], configDrafts: [{ filename: "application.yml", content: "logging: {}" }],
  };
  const expected = {
    id: "registration-east", version: "3", moduleId: "web", environment: "test",
    hostId: "server-a", recordedVersion: "old",
  };
  assert.deepEqual(buildEnvironmentPatch({
    ...draft(), runtimeSoftwareId: "run-east", dependencyIds: ["db"],
    variables: [{ name: "PORT", mode: "PLAIN", value: "8080", credentialRef: "" }],
  }, original), expected);
  assert.deepEqual(buildEnvironmentPatch({ ...draft(), configDrafts: [] }, original), {
    ...expected, runtimeSoftwareId: "", dependencyIds: [], variables: [], configDrafts: [],
  });
});

test("a site draft serializes route targets and dropping static mode clears the Nginx configuration", () => {
  const form = {
    ...draft(), deployMode: "STATIC", servicePort: 8080, startCommand: "java -jar app.jar",
    nginx: { ...draft().nginx, serverName: " site.test ", spa: true, proxyRules: [{
      location: " /api ", targetType: "MODULE", targetModuleId: "api", upstreamUrl: "",
      stripPrefix: true, webSocket: false, sse: false,
    }] },
  };
  const site = buildEnvironmentPatch(form);
  assert.deepEqual(site.nginx, {
    nginxSoftwareId: "", siteName: "", serverName: "site.test", listenPort: 80, spa: true,
    proxyRules: [{ location: "/api", targetModuleId: "api", stripPrefix: true }],
  });
  assert.equal("startCommand" in site, false);
  assert.equal("servicePort" in site, false);
  const next = buildEnvironmentPatch({ ...draft(), deployMode: "DOCKER", servicePort: 9010 }, { id: "site", ...site });
  assert.deepEqual(next.ports, [{ hostPort: 9010, containerPort: 9010, protocol: "TCP" }]);
  assert.deepEqual(next.nginx, {});
});
