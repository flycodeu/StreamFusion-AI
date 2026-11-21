import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import ts from "typescript";

async function load(name, dependencies = {}) {
  const source = await readFile(new URL("../src/" + name + ".ts", import.meta.url), "utf8");
  const js = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText;
  const exports = {};
  new Function("exports", "require", js)(exports, key => dependencies[key]);
  return exports;
}
const { softwareConnectionCommands: commands } = await load("softwareConnectionCommands", {
  "./officialInstallation": await load("officialInstallation"), "./webAccess": await load("webAccess"),
});
function database(type, runtime = "DOCKER") {
  return {
    softwareType: type, runtimeKind: runtime, installationStatus: "READY", actualState: "RUNNING", managementMode: "MANAGED",
    freshness: "FRESH", locator: runtime === "DOCKER" ? "production-db" : "sfops-db.service",
    installation: { definition: { key: type.toLowerCase(), commands: { builtin: true } },
      config: { bindAddress: "0.0.0.0", hostPort: 16380, username: "", credentialId: "private-ref", parameters: { PASSWORD: "private-value" } } },
  };
}

test("Docker uses the container and published host port separately; remote MySQL root is never inferred", () => {
  const item = database("MYSQL");
  item.observation = { portMappings: ["0.0.0.0:19306 → 3306/tcp", "127.0.0.1:33060 → 33060/tcp"] };
  const [inside, outside] = commands(item);
  assert.match(inside.command, /docker exec -it production-db mysql --protocol=SOCKET --user=root --password$/);
  assert.match(outside.command, /--host=127.0.0.1 --port=19306 --user="\$sf_user" --password$/);
  assert.doesNotMatch(outside.command, /--user=root/);
  Object.assign(item.installation.config, { username: "worker", database: "warehouse" });
  for (const row of commands(item)) {
    assert.match(row.command, /--user=worker --password --database=warehouse/);
    assert.doesNotMatch(JSON.stringify(row), /private-ref|private-value/);
  }
});

test("Docker Redis prompts for the password; IPv6 wildcards connect through loopback", () => {
  const item = database("REDIS");
  item.installation.config.bindAddress = "::";
  const rows = commands(item);
  assert.equal(rows[0].command, "docker exec -it production-db redis-cli --askpass");
  assert.equal(rows[1].command, "redis-cli -h ::1 -p 16380 --askpass");
});

test("native MySQL uses the instance socket even when paths require shell quoting; native Redis uses the bundled client", () => {
  const item = database("MYSQL", "SYSTEMD");
  item.installationPaths = { root: "/data/a b's", data: "/data/a b's/data" };
  const rows = commands(item);
  assert.equal(rows[0].command, "'/data/a b'\\''s/runtime/mysql/bin/mysql' --protocol=SOCKET '--socket=/data/a b'\\''s/data/mysql.sock' --user=root --password");
  assert.match(rows[1].command, /\/runtime\/mysql\/bin\/mysql' --protocol=TCP/);
  assert.doesNotMatch(rows[0].command, /health\.cnf/);
  const redis = database("REDIS", "SYSTEMD");
  redis.installationPaths = { root: "/srv/redis" };
  assert.equal(commands(redis)[0].command, "/srv/redis/bin/redis-cli -h 127.0.0.1 -p 16380 --askpass");
});

test("official packages use system clients and can authenticate over the Ubuntu MySQL socket", () => {
  const item = database("MYSQL", "SYSTEMD");
  item.installation.official = { manager: "apt" };
  assert.equal(commands(item)[0].command, "sudo /usr/bin/mysql --protocol=SOCKET --user=root");
  assert.match(commands(item)[1].command, /\/usr\/bin\/mysql --protocol=TCP/);
  const redis = database("REDIS", "SYSTEMD");
  redis.installation.official = { manager: "apt" };
  assert.equal(commands(redis)[0].command, "/usr/bin/redis-cli -h 127.0.0.1 -p 16380");
});

test("registered older containers use recorded ports without fabricating a host endpoint or credentials", () => {
  const item = database("MYSQL");
  delete item.installation;
  delete item.installationStatus;
  assert.equal(commands(item).length, 1);
  assert.doesNotMatch(commands(item)[0].command, /root/);
  item.observation = { portMappings: ["[::1]:17001 → 3306/tcp"] };
  assert.match(commands(item)[1].command, /--host=::1 --port=17001/);
  item.locator = "bad; echo NO";
  assert.equal(commands(item).length, 1);
  assert.doesNotMatch(commands(item)[0].command, /echo|docker exec/);
  item.managementMode = "RELEASED";
  assert.deepEqual(commands(item), []);
});

test("a legacy Docker id and mapped port are sufficient; old native installs can use the binary receipt", () => {
  const item = { softwareType: "REDIS", runtimeKind: "DOCKER", locator: "e".repeat(64), observation: { portMappings: ["10.1.2.3:16220 → 6379/tcp"] } };
  assert.equal(commands(item)[0].command, "docker exec -it " + item.locator + " redis-cli");
  assert.equal(commands(item)[1].command, "redis-cli -h 10.1.2.3 -p 16220");
  const native = database("MYSQL", "SYSTEMD");
  native.installationEvidence = { binaryPath: "/srv/instance/runtime/mysql/bin/mysqld" };
  native.observation = { listeningPorts: ["10.1.2.4:16380/tcp", "0.0.0.0:33060/tcp"] };
  assert.match(commands(native)[0].command, /\/srv\/instance\/runtime\/mysql\/bin\/mysql --protocol=TCP --host=10.1.2.4 --port=16380/);
});

test("discovered MariaDB uses its own CLI inside and the installed mysql client on the host", () => {
  const item = database("MYSQL");
  delete item.installation;
  item.observation = { version: "registry:5000/db/mariadb:11.4", portMappings: ["127.0.0.1:13308 → 3306/tcp"] };
  const [inside, outside] = commands(item);
  assert.match(inside.command, /docker exec -it production-db mariadb --protocol=SOCKET/);
  assert.match(outside.command, /&& mysql --protocol=TCP --host=127.0.0.1 --port=13308/);
});

test("legacy PostgreSQL container IDs use observed endpoints and ask for missing credentials", () => {
  const id = "b".repeat(64);
  const item = { runtimeKind: "DOCKER", softwareType: "OTHER", locator: id, softwareVersion: "postgres:17",
    observation: { version: "registry:5000/library/postgres@sha256:" + "d".repeat(64), portMappings: ["[::1]:15432 → 5432/tcp"] } };
  const [inside, outside] = commands(item);
  assert.ok(inside.command.includes(`docker exec -it ${id} psql -U "$sf_pg_user" -d "$sf_pg_db" -W`));
  assert.match(outside.command, /^read -r -p .*sf_pg_user && read -r -p .*sf_pg_db && psql -h ::1 -p 15432/);
  item.observation.version = "";
  assert.equal(commands(item)[1].command, outside.command);
  item.observation.version = "postgres:5000/unrelated:1";
  assert.doesNotMatch(JSON.stringify(commands(item)), /psql/);
});

test("an image digest or a probed version does not replace a known database image name", () => {
  const item = database("MYSQL");
  item.installation.definition = { key: "custom-db", image: "registry:5000/db/mariadb:11.4" };
  item.observation = { version: "11.4.8", versionSource: "INSTALL_PROBE" };
  assert.match(commands(item)[0].command, /docker exec -it production-db mariadb /);

  const postgres = { softwareType: "OTHER", runtimeKind: "DOCKER", locator: "an-existing-postgres",
    softwareVersion: "postgres:17", observation: { versionSource: "IMAGE_REFERENCE", version: "sha256:" + "d".repeat(64) } };
  assert.match(commands(postgres)[0].command, /docker exec -it an-existing-postgres psql /);
  postgres.observation.version = "registry:5000/db/another-service:2";
  assert.doesNotMatch(JSON.stringify(commands(postgres)), /psql/);
});

test("tools, declared PostgreSQL accounts and other service runtimes get distinct commands", () => {
  const image = "sha256:" + "e".repeat(64);
  assert.equal(commands({ runtimeKind: "TOOL", softwareType: "JAVA", locator: "image:" + image })[0].command, "sf-docker run " + image + " java -version");
  assert.match(commands({ runtimeKind: "TOOL", softwareType: "OTHER", locator: "image:" + image })[0].command, /image inspect/);
  assert.equal(commands({ runtimeKind: "TOOL", softwareType: "FFMPEG", locator: "/mx/ff mp/ffmpeg" })[0].command, "'/mx/ff mp/ffmpeg' -version");
  const item = database("OTHER");
  Object.assign(item.installation.definition, { catalogKey: "postgresql" });
  item.installation.config.parameters = { DB_USER: "analyst", DB_NAME: "metrics", DB_PASSWORD: "private-value" };
  assert.equal(commands(item)[0].command, "docker exec -it production-db psql -U analyst -d metrics -W");
  assert.doesNotMatch(JSON.stringify(commands(item)), /private-value/);
  delete item.installation;
  assert.match(commands(item)[0].command, /inspect --format/);
  assert.equal(commands(item)[1].command, "sf-docker ports production-db");
  assert.equal(commands({ runtimeKind: "SYSTEMD", locator: "worker.service" })[0].command, "systemctl is-active -- worker.service");
});

function webInstance() {
  const item = database("OTHER");
  item.installation.definition = { kind: "SERVICE", port: 8000, web: { scheme: "https", path: "/ready", portKey: "console", urlParameter: "PUBLIC_URL" },
    ports: [{ key: "console", protocol: "TCP" }], parameters: [{ key: "PUBLIC_URL", type: "TEXT" }] };
  item.installation.config.ports = { console: 18443 };
  return item;
}

test("HTTP commands executed on the server follow the listener address and selected port even on SSH targets", () => {
  const item = webInstance();
  const checks = [
    ["127.0.0.1", "127.0.0.1"], ["192.0.2.43", "192.0.2.43"],
    ["0.0.0.0", "127.0.0.1"], ["::", "[::1]"], ["2001:db8::4", "[2001:db8::4]"],
  ];
  for (const [bind, target] of checks) {
    item.installation.config.bindAddress = bind;
    assert.ok(commands(item)[0].command.includes("https://" + target + ":18443/ready"), bind);
  }
});

test("HTTP commands honor a valid declared URL, do not invent an alternative, and require a running installation", () => {
  const item = webInstance();
  for (const url of ["https://gateway.internal/console", "http://127.0.0.1:7200/site/"]) {
    item.installation.config.parameters.PUBLIC_URL = url;
    assert.ok(commands(item)[0].command.endsWith(url));
  }
  for (const invalid of ["https://admin:private@internal/", "http://127.0.0.1:7200/?secret=private", "file:///private", "http://0.0.0.0/private", "unknown"]) {
    item.installation.config.parameters.PUBLIC_URL = invalid;
    assert.doesNotMatch(JSON.stringify(commands(item)), /curl|private|18443/);
  }
  delete item.installation.config.parameters.PUBLIC_URL;
  for (const state of ["UNKNOWN", "STOPPED", "PAUSED", "FAILED"]) {
    item.actualState = state;
    assert.doesNotMatch(JSON.stringify(commands(item)), /curl|18443/);
  }
  item.actualState = "RUNNING";
  item.installationStatus = "INSTALLING";
  assert.deepEqual(commands(item), []);
});
