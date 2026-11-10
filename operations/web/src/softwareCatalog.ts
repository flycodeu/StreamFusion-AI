import type { ServiceRecipe } from "./serviceInstallation";
import type { SoftwareInstance } from "./software";

export function catalogDefaultRecipe(recipes: ServiceRecipe[], selectedVersion?: string): ServiceRecipe | undefined {
  return recipes.find(item => item.version === selectedVersion) ||
    recipes.find(item => (item.catalogKey || item.key) === "ytlabel" && item.version === "4.0-python312" && item.sourceModes.includes("ONLINE")) || recipes[0];
}

export function installedPythonPaths(items: SoftwareInstance[], hostId: string): string[] {
  return Array.from(new Set(items.filter(item => item.hostId === hostId && item.softwareType === "PYTHON" &&
    item.runtimeKind === "TOOL" && item.managementMode !== "RELEASED" &&
    (item.observation?.state || item.actualState) === "INSTALLED")
    .map(item => item.observation?.executablePath).filter((path): path is string => !!path && path.startsWith("/") && path !== "/"))).sort();
}

export function softwareCatalogKey(item: SoftwareInstance): string {
  const definition = item.installation?.definition;
  if (definition) return definition.catalogKey || definition.key;
  if (item.softwareType === "LABEL_STUDIO") return "label-studio";
  return item.softwareType === "OTHER" ? "" : item.softwareType.toLowerCase();
}

export function catalogInstalled(item: SoftwareInstance): boolean {
  if (item.managementMode === "RELEASED" || ["REMOVED", "MISSING"].includes(item.actualState) || item.installationStatus === "UNINSTALLED") return false;
  return item.installationStatus === "READY" || ["RUNNING", "STOPPED", "INSTALLED", "PAUSED"].includes(item.observation?.state || item.actualState) ||
    item.freshness === "FRESH" && item.observation?.versionStatus === "INSTALLED_PACKAGE";
}

export function catalogInstances(items: SoftwareInstance[], hostId: string, key: string): SoftwareInstance[] {
  return items.filter(item => item.hostId === hostId && softwareCatalogKey(item) === key && catalogInstalled(item));
}

export function catalogVersionMatches(item: SoftwareInstance, definition: ServiceRecipe): boolean {
  if (softwareCatalogKey(item) !== (definition.catalogKey || definition.key)) return false;
  if (item.installation?.definition) return item.installation.definition.version === definition.version;
  const version = (item.observation?.version || item.softwareVersion || "").trim();
  const wanted = definition.version.replace(/^v/, "");
  if (version.replace(/^v/, "") === wanted) return true;
  // A major Java release is a selectable environment, not a patch package.
  if ((definition.catalogKey || definition.key) === "java" && /^\d+$/.test(wanted)) {
    const match = version.match(/(?:^|\bversion\s+["']?)(?:1\.)?(\d+)(?:[._+"'\s]|$)/i);
    return match?.[1] === wanted;
  }
  return false;
}

export function catalogPending(items: SoftwareInstance[], hostId: string, definition: ServiceRecipe): SoftwareInstance | undefined {
  return catalogPendingRecords(items, hostId, definition.catalogKey || definition.key).filter(item =>
    catalogVersionMatches(item, definition) && item.installation!.definition!.key === definition.key &&
    definition.runtimeModes.includes(item.installation!.config?.runtimeMode || (item.runtimeKind === "DOCKER" ? "DOCKER" : "NATIVE")))
    .sort((a, b) => b.createdAt.localeCompare(a.createdAt))[0];
}

export function catalogPendingRecords(items: SoftwareInstance[], hostId: string, catalogKey: string) {
  return items.filter(item => item.hostId === hostId && softwareCatalogKey(item) === catalogKey &&
    item.managementMode !== "RELEASED" && item.installation?.definition && item.installationJobId &&
    ["INSTALLING", "UNKNOWN"].includes(item.installationStatus || ""))
    .sort((a, b) => b.createdAt.localeCompare(a.createdAt));
}

export function catalogInstallState(items: SoftwareInstance[], hostId: string, definition: ServiceRecipe) {
  const pending = catalogPending(items, hostId, definition);
  const instances = catalogInstances(items, hostId, definition.catalogKey || definition.key);
  const matching = instances.filter(item => catalogVersionMatches(item, definition));
  if (pending) return { label: pending.installationStatus === "UNKNOWN" ? "结果待确认" : "安装中", tone: "warning", instances, matching, pending };
  if (matching.length) return { label: matching.length > 1 ? `已安装 · ${matching.length} 个实例` : "已安装", tone: "success", instances, matching };
  if (instances.length) return { label: instances.some(item => !(item.installation?.definition?.version || item.observation?.version || item.softwareVersion || "").trim())
    ? "已安装 · 版本待确认" : "已安装其他版本", tone: "info", instances, matching };
  return { label: "", tone: "", instances, matching };
}
