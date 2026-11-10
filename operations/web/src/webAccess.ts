import type { Host } from "./hosts";
import type { ServiceConfiguration, ServiceRecipe } from "./serviceInstallation";
import type { SoftwareInstance } from "./software";

export interface SoftwareAccess {
  url: string;
  reason: string;
  embed: boolean;
}

const browserHost = () => typeof location === "undefined" ? "" : location.hostname;
const forbidden = /[\s\u0000-\u001f\u007f-\u009f\\]/;
const decoded = (value: string) => { try { return decodeURIComponent(value); } catch { return undefined; } };

function normalizedHost(value: string): string {
  if (!value || forbidden.test(value) || /[/?#@]/.test(value)) return "";
  const bare = value.replace(/^\[([^\]]+)\]$/, "$1");
  try {
    const host = new URL(`http://${bare.includes(":") ? `[${bare}]` : bare}/`).hostname;
    return host.replace(/^\[|\]$/g, "").toLowerCase();
  } catch { return ""; }
}

function loopback(host: string): boolean {
  return host === "::1" || host.startsWith("127.") || /^::ffff:7f[0-9a-f]{2}:/.test(host) || /(^|\.)localhost\.?$/.test(host);
}

function publicURL(value: string): string {
  if (value.length > 2048 || !/^https?:\/\//i.test(value) || forbidden.test(value) || /[?#]/.test(value)) return "";
  try {
    const url = new URL(value);
    const path = decoded(url.pathname);
    return ["http:", "https:"].includes(url.protocol) && url.hostname && !url.username && !url.password && !url.search && !url.hash && path !== undefined && !forbidden.test(path) && !value.match(/:\/\/[^/]*:($|\/)/) ? url.href : "";
  } catch { return ""; }
}

export function webDefinitionError(recipe: ServiceRecipe): string {
  if (recipe.defaultBindAddress === "0.0.0.0" && !recipe.web) return "启用 Web 入口后可使用 0.0.0.0";
  if (recipe.defaultBindAddress) {
    const value = recipe.defaultBindAddress;
    const parts = value.split(".");
    if (parts.length !== 4 || parts.some(part => !/^(0|[1-9]\d{0,2})$/.test(part) || Number(part) > 255) || Number(parts[0]) >= 224 && Number(parts[0]) <= 239) return "默认监听地址须为有效的 IPv4 地址";
  }
  const web = recipe.web;
  if (!web) return "";
  if (recipe.kind === "TOOL") return "程序工具不能启用 Web 服务入口";
  if (!["http", "https"].includes(web.scheme)) return "请选择访问协议";
  const portKey = web.portKey || "main";
  const descriptor = recipe.ports?.find(port => port.key === portKey);
  if (!(descriptor ? descriptor.protocol === "TCP" : portKey === "main" && recipe.port > 0)) return "请选择已声明的 TCP 入口端口";
  const path = web.path || "/";
  const pathValue = decoded(path);
  if (path.length > 512 || !path.startsWith("/") || path.startsWith("//") || /[?#]/.test(path) || forbidden.test(path) || pathValue === undefined || pathValue.startsWith("//") || forbidden.test(pathValue)) return "入口路径须为不带参数的站内路径";
  if (web.urlParameter) {
    const field = recipe.parameters?.find(field => field.key === web.urlParameter && field.type === "TEXT");
    if (!field) return "访问地址应选择已声明的文本参数";
    if (field.default && !publicURL(field.default)) return "访问地址默认值须为不含认证信息和参数的 HTTP 或 HTTPS 地址";
  }
  return "";
}

export function installationWebAccess(recipe: ServiceRecipe, config: ServiceConfiguration, host?: Host, controllerHost = browserHost()): SoftwareAccess | undefined {
  const web = recipe.web;
  if (!web) return undefined;
  const unavailable = (reason: string): SoftwareAccess => ({ url: "", reason, embed: !!web.embed });
  const invalid = webDefinitionError(recipe);
  if (invalid) return unavailable(invalid);
  const supplied = web.urlParameter && (config.parameters?.[web.urlParameter] ?? recipe.parameters?.find(field => field.key === web.urlParameter)?.default ?? "");
  if (supplied) {
    const url = publicURL(supplied);
    if (!url) return unavailable("请填写不含账号、参数和片段的 HTTP 或 HTTPS 访问地址");
    const address = normalizedHost(new URL(url).hostname);
    if (address === "0.0.0.0" || address === "::") return unavailable("请填写可访问的主机地址");
    if (loopback(address) && (host?.kind !== "LOCAL" || !loopback(normalizedHost(controllerHost)))) return unavailable("服务仅在服务器本机监听，请填写可访问的系统地址");
    return { url, reason: "", embed: !!web.embed };
  }
  const port = !web.portKey || web.portKey === "main" ? config.hostPort : config.ports?.[web.portKey];
  if (!Number.isInteger(port) || !port || port < 1 || port > 65535) return unavailable("请先配置入口端口");
  const bindAddress = normalizedHost(config.bindAddress || "");
  if (!bindAddress) return unavailable("请填写监听地址");
  if (loopback(bindAddress) && (host?.kind !== "LOCAL" || !loopback(normalizedHost(controllerHost)))) return unavailable("服务仅在服务器本机监听，请填写可访问的系统地址");
  let address = host?.kind === "LOCAL" && loopback(bindAddress) ? bindAddress : normalizedHost(host?.address || "");
  if (host?.kind === "LOCAL" && (!address || loopback(address)) && !loopback(bindAddress)) address = normalizedHost(controllerHost);
  if (!address) return unavailable("请填写可访问的主机地址");
  if (loopback(address) && (host?.kind !== "LOCAL" || !loopback(normalizedHost(controllerHost)))) return unavailable("服务仅在服务器本机监听，请填写可访问的系统地址");
  if (address === "0.0.0.0" || address === "::") return unavailable("请填写可访问的主机地址");
  const url = publicURL(`${web.scheme}://${address.includes(":") ? `[${address}]` : address}:${port}${web.path || "/"}`);
  return url ? { url, reason: "", embed: !!web.embed } : unavailable("访问地址无效，请检查主机、端口和入口路径");
}

export function softwareWebAccess(item: SoftwareInstance, host?: Host, controllerHost = browserHost()): SoftwareAccess | undefined {
  const recipe = item.installation?.definition, config = item.installation?.config;
  if (!recipe?.web || !config) return undefined;
  const access = installationWebAccess(recipe, config, host, controllerHost)!;
  if (item.managementMode === "RELEASED" || item.installationStatus === "UNINSTALLED" || item.actualState === "REMOVED") return { ...access, url: "", reason: "软件已卸载" };
  if (item.installationStatus !== "READY") return { ...access, url: "", reason: "安装完成后可打开系统" };
  if (item.actualState !== "RUNNING") return { ...access, url: "", reason: item.actualState === "STOPPED" ? "软件已停止，请先启动" : "请先检查软件运行状态" };
  return access;
}
