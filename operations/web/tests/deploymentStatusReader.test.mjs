import assert from "node:assert/strict";
import { test } from "node:test";
import { createDeploymentStatusReader } from "../src/features/project-delivery/deploymentStatusReader.ts";

function stateReader() {
  const calls = [];
  const reader = createDeploymentStatusReader((path, signal) => new Promise((resolve, reject) => {
    calls.push({ path, signal, resolve, reject });
  }));
  return { reader, calls };
}

test("list and dialog use the same ongoing read; other registrations are independent", async () => {
  const { reader, calls } = stateReader();
  reader.retain(new Map([["project/east", "host-east"], ["project/west", "host-west"]]));
  const list = reader.refresh("project/east");
  const dialog = reader.refresh("project/east");
  const west = reader.refresh("project/west");
  assert.equal(dialog, list);
  await Promise.resolve();
  assert.deepEqual(calls.map(({ path }) => path), ["project/east", "project/west"]);
  calls[0].resolve({ health: "READY" });
  calls[1].resolve({ health: "UNHEALTHY" });
  await Promise.all([list, west, dialog]);
  assert.equal(reader.observation("project/east").status.health, "READY");
  assert.equal(reader.observation("project/west").status.health, "UNHEALTHY");
  await reader.refresh("project/east", 2500);
  assert.equal(calls.length, 2);
});

test("a read started before a queued action cannot overwrite its job or later observation", async () => {
  const { reader, calls } = stateReader();
  reader.retain(new Map([["project/east", "host-east"]]));
  const oldRead = reader.refresh("project/east");
  await Promise.resolve();
  const job = { jobId: "accepted", status: "QUEUED" };
  reader.recordQueued("project/east", { pending: job });
  assert.equal(calls[0].signal.aborted, true);
  const newRead = reader.refresh("project/east");
  await Promise.resolve();
  calls[1].resolve({ pending: { ...job, status: "RUNNING" } });
  await newRead;
  calls[0].resolve({ health: "NOT_DEPLOYED" });
  await oldRead;
  assert.equal(reader.observation("project/east").status.pending.status, "RUNNING");
});

test("leaving an instance or changing its host cancels the old read and discards its result", async () => {
  const { reader, calls } = stateReader();
  reader.retain(new Map([["project/east", "host-east"]]));
  const first = reader.refresh("project/east");
  await Promise.resolve();
  reader.retain(new Map([["project/east", "new-host"]]));
  const second = reader.refresh("project/east");
  await Promise.resolve();
  calls[0].resolve({ health: "READY" });
  await first;
  assert.equal(calls[0].signal.aborted, true);
  assert.equal(reader.observation("project/east").status, undefined);
  reader.retain(new Map());
  assert.equal(calls[1].signal.aborted, true);
  calls[1].reject(new Error("request cancelled"));
  await second;
  assert.equal(reader.observation("project/east"), undefined);
});
