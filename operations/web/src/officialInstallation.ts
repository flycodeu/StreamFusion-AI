export interface OfficialCommand {
  executable: string;
  args: string[];
}

export function shellArgument(value: string): string {
  return /^[A-Za-z0-9_./:=@+,-]+$/.test(value)
    ? value
    : "'" + value.replaceAll("'", "'\\''") + "'";
}

export function officialCommandText(command: OfficialCommand): string {
  return [command.executable, ...command.args].map(shellArgument).join(" ");
}

export function officialCommandTokens(command: OfficialCommand, width = 76) {
  const tokens: {
    text: string;
    kind: "executable" | "option" | "argument" | "continuation";
  }[] = [];
  let column = 0;
  for (const [index, value] of [
    command.executable,
    ...command.args,
  ].entries()) {
    const text = shellArgument(value);
    if (index && column + text.length + 1 > width) {
      tokens.push({ text: " \\\n  ", kind: "continuation" });
      column = 2;
    } else if (index) {
      tokens.push({ text: " ", kind: "continuation" });
      column++;
    }
    tokens.push({
      text,
      kind:
        index === 0
          ? "executable"
          : value.startsWith("-")
            ? "option"
            : "argument",
    });
    column += text.length;
  }
  return tokens;
}

export function officialCommandTitle(command: OfficialCommand): string {
  const executable = command.executable.split("/").at(-1);
  if (executable === "apt-get") {
    if (command.args.includes("update")) return "更新软件索引";
    if (command.args.includes("install")) return "安装软件包";
  }
  if (executable === "systemctl" && command.args[0] === "enable")
    return command.args.includes("--now") ? "启用并启动服务" : "启用服务";
  return executable || "执行命令";
}

export function officialPackageSource(origin: string): {
  label: string;
  href?: string;
} {
  try {
    const url = new URL(origin);
    if (url.protocol === "https:" || url.protocol === "http:")
      return { label: url.host, href: origin };
  } catch {
    // Repository labels such as "Ubuntu noble/main" are plain text.
  }
  return { label: origin.length > 48 ? origin.slice(0, 45) + "…" : origin };
}
