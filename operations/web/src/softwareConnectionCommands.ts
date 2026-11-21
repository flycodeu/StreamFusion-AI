import { shellArgument } from "./officialInstallation";
import type { SoftwareInstance } from "./software";
import { softwareWebAccess } from "./webAccess";

interface ConnectionCommand {
  label: string;
  command: string;
  note?: string;
}

const text = (value?: string) => value && !/[\u0000-\u001f\u007f]/.test(value) ? value : "";
const absolute = (value?: string) => text(value).startsWith("/") ? value! : "";
const cmd = (...args: string[]) => args.map(shellArgument).join(" ");

function endpoint(address: string, port: number) {
  if (!Number.isInteger(port) || port < 1 || port > 65535) return;
  const ip = address.replace(/^\[([^\]]+)\]$/, "$1");
  if (!/^[\da-f:.]+$/i.test(ip) || !ip.includes(":") && !/^\d+\.\d+\.\d+\.\d+$/.test(ip)) return;
  try {
    const authority = ip.includes(":") ? "[" + ip + "]" : ip;
    const host = new URL("http://" + authority + "/").hostname.replace(/^\[|\]$/g, "");
    return { host: host === "::" ? "::1" : host === "0.0.0.0" ? "127.0.0.1" : host, port };
  } catch { return; }
}

function databaseEndpoint(detail: SoftwareInstance, standardPort: number) {
  const config = detail.installation?.config;
  const saved = config && endpoint(config.bindAddress || "", config.hostPort);
  const docker = detail.runtimeKind === "DOCKER";
  const listeners = docker ? detail.observation?.portMappings : detail.observation?.listeningPorts;
  const candidates = (listeners || []).flatMap(value => {
    const match = docker ? /^(.*):(\d+)\s*(?:→|->)\s*(\d+)\/tcp$/.exec(value) : /^(.*):(\d+)\/tcp$/.exec(value);
    if (!match || docker && Number(match[3]) !== standardPort) return [];
    const found = endpoint(match[1] || "0.0.0.0", Number(match[2]));
    return found ? [found] : [];
  });
  if (saved) {
    if (detail.freshness === "FRESH" && candidates.length) {
      const same = candidates.find(item => item.port === saved.port);
      if (same) return candidates.find(item => item.port === saved.port && item.host === saved.host) || same;
      if (docker || standardPort === 6379) return candidates[0];
    }
    return saved;
  }
  // MySQL may expose a second port for X Protocol.
  return candidates.find(item => item.port === standardPort) || (docker || standardPort === 6379 ? candidates[0] : undefined);
}

function mysqlCommand(prefix: string[], transport: string[], username?: string, database?: string) {
  const base = cmd(...prefix, ...transport);
  const suffix = cmd("--password", ...(text(database) ? ["--database=" + database] : []));
  if (text(username)) return base + " " + cmd("--user=" + username) + " " + suffix;
  return "read -r -p 'MySQL 用户名：' sf_user && " + '[ -n "$sf_user" ] && ' + base + ' --user="$sf_user" ' + suffix;
}

function dockerImageName(detail: SoftwareInstance) {
  if (detail.runtimeKind !== "DOCKER") return "";
  const observation = detail.observation;
  const observedRef = !observation?.versionSource || observation.versionSource === "IMAGE_REFERENCE" ? observation?.version : undefined;
  for (const ref of [observedRef, detail.installation?.definition?.image, detail.softwareVersion]) {
    const candidate = ref?.trim();
    if (!candidate || /^sha256:[a-f0-9]{64}$/i.test(candidate) || /^[a-f0-9]{64}$/i.test(candidate)) continue;
    const name = candidate.split("@")[0]?.split("/").pop()?.split(":")[0]?.toLowerCase() || "";
    if (/^[a-z][a-z0-9_.-]*$/.test(name) && !/\s/.test(candidate)) return name;
  }
  return "";
}

function databaseCommands(detail: SoftwareInstance, container: string): ConnectionCommand[] {
  const mysql = detail.softwareType === "MYSQL";
  const docker = detail.runtimeKind === "DOCKER";
  const config = detail.installation?.config;
  const official = !!detail.installation?.official;
  const recipe = detail.installation?.definition;
  const builtin = recipe?.key === (mysql ? "mysql" : "redis") && (!recipe.commands || recipe.commands.builtin);
  const target = databaseEndpoint(detail, mysql ? 3306 : 6379);
  const result: ConnectionCommand[] = [];
  const observedMariaDB = dockerImageName(detail) === "mariadb" ||
    /^mariadb-server(?:-|$)/.test(detail.observation?.packageName || "");
  const client = mysql ? (!builtin && observedMariaDB ? "mariadb" : "mysql") : "redis-cli";
  const password = !official && builtin ? ["--askpass"] : [];
  const redisNote = password.length ? "按提示输入 Redis 密码。" : "如服务启用了密码，可在命令末尾加 --askpass。";
  if (docker && container) {
    const prefix = ["docker", "exec", "-it", container, client];
    result.push({
      label: "容器内连接",
      command: mysql ? mysqlCommand(prefix, ["--protocol=SOCKET"], config?.username || (builtin ? "root" : ""), config?.database)
        : cmd(...prefix, ...password),
      note: mysql ? "使用有 Docker 执行权限的主机账号，按提示输入数据库密码。" : redisNote,
    });
  }
  if (!docker && mysql && official)
    result.push({ label: "系统管理员本机连接", command: "sudo /usr/bin/mysql --protocol=SOCKET --user=root", note: "适用于 Ubuntu 软件包的本机套接字认证。" });
  const paths = detail.installationPaths;
  const root = absolute(paths?.root);
  const binary = absolute(detail.installationEvidence?.binaryPath);
  const native = !official && !docker && builtin && detail.runtimeKind === "SYSTEMD";
  let nativeClient = "";
  if (native && root && root !== "/") nativeClient = root + (mysql ? "/runtime/mysql/bin/mysql" : "/bin/redis-cli");
  else if (native && binary.endsWith(mysql ? "/bin/mysqld" : "/bin/redis-server"))
    nativeClient = binary.slice(0, binary.lastIndexOf("/") + 1) + client;
  const data = absolute(paths?.data) || (root && root !== "/" ? root + "/data" : "");
  if (nativeClient && mysql && data)
    result.push({ label: "实例套接字连接", command: mysqlCommand([nativeClient], ["--protocol=SOCKET", "--socket=" + data + "/mysql.sock"], config?.username || "root", config?.database),
      note: "在能访问该实例目录的主机账号下执行。" });
  if (target) {
    const hostClient = docker && mysql ? "mysql" : official ? "/usr/bin/" + client : nativeClient || client;
    result.push({ label: "宿主机 TCP 连接",
      command: mysql ? mysqlCommand([hostClient], ["--protocol=TCP", "--host=" + target.host, "--port=" + target.port], config?.username, config?.database)
        : cmd(hostClient, "-h", target.host, "-p", String(target.port), ...password),
      note: nativeClient ? "在能访问该实例目录的主机账号下执行，按提示输入数据库密码。" : mysql ? "使用允许从该地址登录的数据库账号。" : redisNote });
  }
  return result;
}

function postgresCommands(detail: SoftwareInstance, container: string): ConnectionCommand[] {
  const parameters = detail.installation?.config?.parameters;
  const identifier = (value?: string) => /^[a-z_][a-z0-9_]*$/.test(value || "") ? value : undefined;
  const user = identifier(parameters?.DB_USER), db = identifier(parameters?.DB_NAME);
  const prompt = [user ? "" : "read -r -p 'PostgreSQL 用户名：' sf_pg_user",
    db ? "" : "read -r -p '数据库名：' sf_pg_db"].filter(Boolean);
  const args = `-U ${user ? shellArgument(user) : '"$sf_pg_user"'} -d ${db ? shellArgument(db) : '"$sf_pg_db"'} -W`;
  const connect = (...program: string[]) => [...prompt, cmd(...program) + " " + args].join(" && ");
  const target = databaseEndpoint(detail, 5432);
  const rows: ConnectionCommand[] = [];
  if (detail.runtimeKind === "DOCKER" && container)
    rows.push({ label: "容器内连接", command: connect("docker", "exec", "-it", container, "psql") });
  if (target) rows.push({ label: "宿主机 TCP 连接", command: connect("psql", "-h", target.host, "-p", String(target.port)) });
  return rows;
}

const toolCommands: Record<string, [string, string]> = {
  JAVA: ["java", "-version"], PYTHON: ["python3", "--version"], FFMPEG: ["ffmpeg", "-version"],
  GIT: ["git", "--version"], MAVEN: ["mvn", "--version"], NODE: ["node", "--version"],
  CODEX: ["codex", "--version"], CLAUDE: ["claude", "--version"],
};

export function softwareConnectionCommands(detail: SoftwareInstance): ConnectionCommand[] {
  if (detail.managementMode === "RELEASED" || detail.actualState === "REMOVED" || detail.actualState === "MISSING" ||
      detail.installationStatus && detail.installationStatus !== "READY") return [];
  const container = [detail.locator, detail.observation?.containerName, detail.installationEvidence?.containerId]
    .find(value => value && /^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/.test(value)) || "";
  if (detail.softwareType === "MYSQL" || detail.softwareType === "REDIS") {
    const rows = databaseCommands(detail, container);
    if (rows.length) return rows;
  }
  if (detail.installation?.definition?.catalogKey === "postgresql" || detail.softwareType === "OTHER" && ["postgres", "postgresql"].includes(dockerImageName(detail))) {
    const rows = postgresCommands(detail, container);
    if (rows.length) return rows;
  }
  const access = softwareWebAccess(detail, "HOST_TERMINAL");
  if (access?.url) return [{ label: "访问 HTTP 入口", command: cmd("curl", "--silent", "--show-error", "--output", "/dev/null", "--write-out", "%{http_code}\\n", "--", access.url) }];
  if (detail.runtimeKind === "TOOL" && toolCommands[detail.softwareType]) {
    const [name, flag] = toolCommands[detail.softwareType]!;
    const binary = absolute(detail.locator) || absolute(detail.installationEvidence?.binaryPath) || absolute(detail.observation?.executablePath);
    if (binary || detail.locator === name || detail.locator === "python" && name === "python3")
      return [{ label: "查看程序版本", command: cmd(binary || name, flag) }];
    if (/^image:sha256:[a-f0-9]{64}$/.test(detail.locator))
      return [{ label: "查看镜像内程序版本", command: cmd("sf-docker", "run", detail.locator.slice(6), name, flag),
        note: "在项目目录中执行。镜像使用当前登录账号和工作目录。" }];
  }
  if (detail.runtimeKind === "SYSTEMD" && /^[a-zA-Z0-9][a-zA-Z0-9@_.:-]*\.service$/.test(detail.locator))
    return [{ label: "查看服务状态", command: cmd("systemctl", "is-active", "--", detail.locator) }];
  if (detail.runtimeKind === "DOCKER" && container)
    return [
      { label: "查看容器状态", command: cmd("docker", "container", "inspect", "--format", "{{.State.Status}}", container) },
      { label: "查看容器发布端口", command: cmd("sf-docker", "ports", container) },
    ];
  if (detail.runtimeKind === "TOOL" && /^image:sha256:[a-f0-9]{64}$/.test(detail.locator))
    return [{ label: "查看工具镜像", command: cmd("docker", "image", "inspect", "--format", "{{.Id}}", detail.locator.slice(6)),
      note: "使用 sf-docker run 镜像 程序 参数 可在此镜像中执行命令。" }];
  return [];
}
