import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import * as vue from "vue";
import ts from "typescript";
const compile = (source) =>
  ts.transpileModule(source, {
    compilerOptions: {
      target: ts.ScriptTarget.ES2022,
      module: ts.ModuleKind.CommonJS,
    },
  }).outputText;
function load(
  source,
  imports = {},
  props = {},
  clipboard = { writeText: async () => {} },
) {
  const module = { exports: {} };
  new Function(
    "require",
    "module",
    "exports",
    "defineProps",
    "navigator",
    "setTimeout",
    "clearTimeout",
    compile(source),
  )(
    (name) => imports[name],
    module,
    module.exports,
    () => props,
    { clipboard },
    () => 1,
    () => {},
  );
  return module.exports;
}
const model = load(
  await readFile(
    new URL("../src/officialInstallation.ts", import.meta.url),
    "utf8",
  ),
);
const component = (
  await readFile(
    new URL("../src/OfficialInstallationDetails.vue", import.meta.url),
    "utf8",
  )
)
  .split('<script setup lang="ts">')[1]
  .split("</script>")[0];

test("official command copy preserves every argument with shell-safe quoting", () => {
  const command = {
    executable: "/usr/bin/apt-get",
    args: [
      "install",
      "--yes",
      "package=1.2",
      "a b",
      "one'two",
      "$(echo secret)",
      "",
      "line\nbreak",
    ],
  };
  assert.equal(
    model.officialCommandText(command),
    "/usr/bin/apt-get install --yes package=1.2 'a b' 'one'\\''two' '$(echo secret)' '' 'line\nbreak'",
  );
});

test("terminal display wraps only between arguments and retains exact quoted tokens", () => {
  const command = {
    executable: "/usr/bin/apt-get",
    args: [
      "install",
      "--no-install-recommends",
      "--no-remove",
      "package=" + "1".repeat(100),
      "a b'c",
    ],
  };
  const tokens = model.officialCommandTokens(command, 40);
  assert.deepEqual(
    tokens.filter((t) => t.kind !== "continuation").map((t) => t.text),
    [command.executable, ...command.args].map(model.shellArgument),
  );
  assert.ok(
    tokens.some((t) => t.kind === "continuation" && t.text === " \\\n  "),
  );
  assert.equal(tokens[0].kind, "executable");
  assert.equal(tokens.find((t) => t.text === "--no-remove").kind, "option");
  assert.equal(model.officialCommandText(command).includes("\\\n"), false);
});

test("step titles recognize fixed APT actions after options", () => {
  assert.equal(
    model.officialCommandTitle({
      executable: "/usr/bin/apt-get",
      args: [
        "-o",
        "DPkg::Lock::Timeout=15",
        "-o",
        "APT::Update::Error-Mode=any",
        "update",
      ],
    }),
    "更新软件索引",
  );
  assert.equal(
    model.officialCommandTitle({
      executable: "/usr/bin/apt-get",
      args: [
        "--assume-yes",
        "--no-remove",
        "--no-upgrade",
        "--no-install-recommends",
        "-o",
        "DPkg::Lock::Timeout=15",
        "install",
        "--",
        "nginx=1.24",
      ],
    }),
    "安装软件包",
  );
  assert.equal(
    model.officialCommandTitle({
      executable: "/usr/bin/systemctl",
      args: ["enable", "--now", "--", "nginx.service"],
    }),
    "启用并启动服务",
  );
  assert.equal(
    model.officialCommandTitle({
      executable: "/usr/bin/apt-get",
      args: ["--version"],
    }),
    "apt-get",
  );
});

test("package sources retain exact URL but only safe protocols become links", () => {
  const source =
    "https://download.docker.com/linux/ubuntu/dists/noble/pool/stable/amd64/docker-ce_5%3a29.9.2-1~ubuntu.24.04~noble_amd64.deb";
  assert.deepEqual(model.officialPackageSource(source), {
    label: "download.docker.com",
    href: source,
  });
  assert.equal(
    model.officialPackageSource("Ubuntu noble-updates/main").label,
    "Ubuntu noble-updates/main",
  );
  for (const value of [
    "javascript:alert(1)",
    "data:text/html,<script>alert(1)</script>",
    "<img src=x onerror=alert(1)>",
  ])
    assert.equal(model.officialPackageSource(value).href, undefined);
  assert.equal(model.officialPackageSource("x".repeat(100)).label.length, 46);
});

test("copy action writes original command rather than presentation tokens and reports denial", async () => {
  const written = [];
  const command = {
    executable: "/usr/bin/apt-get",
    args: [
      "install",
      "--yes",
      ...Array.from({ length: 8 }, (_, i) => "pkg" + i + "=1.0"),
    ],
  };
  let denied = false;
  const flow = load(
    component + "\nexport const flow={copyCommand,copied,copyError};",
    {
      vue: { ...vue, onUnmounted: () => {} },
      "./officialInstallation": model,
    },
    { installation: { packages: [], commands: [command] } },
    {
      writeText: async (value) => {
        if (denied) throw new Error("permission denied");
        written.push(value);
      },
    },
  ).flow;
  await flow.copyCommand(0);
  assert.deepEqual(written, [model.officialCommandText(command)]);
  assert.equal(flow.copied.value, 0);
  denied = true;
  await flow.copyCommand(0);
  assert.equal(flow.copyError.value, 0);
  assert.equal(written.length, 1);
});
