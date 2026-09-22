import assert from "node:assert/strict";
import { execFile, spawn } from "node:child_process";
import { once } from "node:events";
import { realpath } from "node:fs/promises";
import { createServer } from "node:http";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import test from "node:test";

const project = fileURLToPath(new URL("../../platform-web/", import.meta.url));
const launcher = fileURLToPath(
  new URL("../../platform-web/scripts/dev.mjs", import.meta.url),
);
const vite = fileURLToPath(
  new URL("../../platform-web/node_modules/vite/bin/vite.js", import.meta.url),
);
const release = fileURLToPath(
  new URL("../release-web-port.ps1", import.meta.url),
);
const windowsOnly = { skip: process.platform !== "win32", timeout: 60000 };

async function listen(server) {
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  return server.address().port;
}

function start(port) {
  const child = spawn(process.execPath, [launcher, "--port", String(port)], {
    cwd: project,
    stdio: ["ignore", "pipe", "pipe"],
    windowsHide: true,
  });
  let output = "";
  child.stdout.on("data", (chunk) => {
    output += chunk;
  });
  child.stderr.on("data", (chunk) => {
    output += chunk;
  });
  const exited = once(child, "exit");
  return { child, exited, output: () => output };
}

async function ready(instance) {
  const deadline = Date.now() + 20000;
  while (Date.now() < deadline) {
    if (instance.output().includes("Local:")) return;
    assert.equal(instance.child.exitCode, null, instance.output());
    await delay(100);
  }
  assert.fail(`Vite did not start: ${instance.output()}`);
}

async function stopProject(port) {
  await promisify(execFile)(
    "powershell.exe",
    [
      "-NoProfile",
      "-NonInteractive",
      "-File",
      release,
      "-Port",
      String(port),
      "-ViteEntry",
      vite,
      "-ViteRealEntry",
      await realpath(vite),
    ],
    { timeout: 15000, windowsHide: true },
  );
}

test(
  "repeated startup replaces only this checkout Vite and serves the new instance",
  windowsOnly,
  async () => {
    const probe = createServer();
    const port = await listen(probe);
    await new Promise((resolve) => probe.close(resolve));
    const instances = [];
    try {
      const first = start(port);
      instances.push(first);
      await ready(first);
      const second = start(port);
      instances.push(second);
      await ready(second);
      assert.match(second.output(), /Stopped previous project Vite PID/);
      await first.exited;
      const response = await fetch(`http://127.0.0.1:${port}/`, {
        signal: AbortSignal.timeout(5000),
      });
      assert.equal(response.status, 200);
      assert.match(await response.text(), /<title>StreamFusion AI<\/title>/);
    } finally {
      await stopProject(port);
      for (const instance of instances) {
        if (instance.child.exitCode === null) instance.child.kill();
        await instance.exited;
      }
    }
  },
);

test(
  "a foreign listener is preserved and startup explains the conflict",
  windowsOnly,
  async () => {
    const foreign = createServer((_request, response) =>
      response.end("foreign process is alive"),
    );
    const port = await listen(foreign);
    const instance = start(port);
    try {
      const [code] = await instance.exited;
      assert.equal(code, 1);
      assert.match(instance.output(), /not a verified Vite instance/);
      const response = await fetch(`http://127.0.0.1:${port}/`, {
        signal: AbortSignal.timeout(5000),
      });
      assert.equal(await response.text(), "foreign process is alive");
    } finally {
      if (instance.child.exitCode === null) instance.child.kill();
      await new Promise((resolve) => foreign.close(resolve));
    }
  },
);
