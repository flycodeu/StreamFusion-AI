import type { SoftwareInstance, SoftwareJob } from "./software";
import type {
  InstallationMaterial,
  ServiceRecipe,
} from "./serviceInstallation";

export interface NodeVersionObservation {
  version: string;
  binaryPath: string;
  binaryHash: string;
  npmVersion: string;
  pnpmVersion: string;
  globalPackages: { name: string; version: string }[];
}
export interface NodeObservation {
  environmentId: string;
  account: string;
  accountUid: string;
  rootPath: string;
  managerType: "NVM" | "SYSTEM";
  nvmVersion: string;
  nvmHash: string;
  defaultVersion: string;
  versions: NodeVersionObservation[];
  fingerprint: string;
  observedAt: string;
  exists: boolean;
}
export interface NodeEnvironment {
  id: string;
  hostId: string;
  name: string;
  account: string;
  accountUid: string;
  rootPath: string;
  managerType: "NVM" | "SYSTEM";
  managementMode: "MANAGED" | "OBSERVE" | "RELEASED";
  nvmVersion: string;
  desiredDefaultSoftwareId: string;
  observedDefaultVersion: string;
  observation?: NodeObservation;
  observedAt: string;
  errorCode: string;
  version: string;
  lastJobId: string;
  versions: SoftwareInstance[];
}
export interface NodeCatalog {
  node: ServiceRecipe[];
  tools: ServiceRecipe[];
}
export interface NodeDiscovery {
  candidates: NodeObservation[];
  expiresAt: string;
}
export type NodeAction =
  | "INITIALIZE"
  | "INSTALL_NODE"
  | "SET_DEFAULT"
  | "UPDATE_NPM"
  | "INSTALL_PNPM"
  | "UPDATE_PNPM"
  | "REMOVE_PNPM"
  | "UPDATE_NVM"
  | "REMOVE_ENVIRONMENT"
  | "UNINSTALL_NODE"
  | "RELEASE";
export interface NodePlan {
  id: string;
  action?: string;
  expiresAt: string;
  commands?: { executable: string; args: string[] }[];
  checks?: { key: string; status: string; message: string }[];
  environmentId?: string;
  change?: {
    context: {
      account: string;
      accountUid: string;
      rootPath: string;
      nvmVersion: string;
      nvmHash: string;
    };
  };
  definition?: ServiceRecipe;
  source?: {
    type: string;
    materialId?: string;
    sha256?: string;
    byteSize?: number;
  };
  config?: Record<string, unknown>;
  node?: {
    environmentVersion: string;
    nvmVersion: string;
    nvmHash: string;
    environmentId: string;
    account: string;
    accountUid: string;
    rootPath: string;
    makeDefault: boolean;
  };
  removedPaths?: string[];
  paths?: { root?: string; bin?: string };
  snapshot?: { rootPath?: string };
}
export interface NodeDraft {
  action: NodeAction;
  name: string;
  account: string;
  nodeVersion: string;
  packageVersion: string;
  initialNodeVersion: string;
  initialNodeMaterialId: string;
  source: "OFFICIAL" | "ARCHIVE";
  materialId: string;
  makeDefault: boolean;
  deleteData: boolean;
  clearNodeDefault: boolean;
  confirmationName: string;
}
export const nodeActionLabels: Record<NodeAction, string> = {
  INITIALIZE: "准备 Node 环境",
  INSTALL_NODE: "安装 Node 版本",
  SET_DEFAULT: "设为环境默认",
  UPDATE_NPM: "更新 npm",
  INSTALL_PNPM: "安装 pnpm",
  UPDATE_PNPM: "更新 pnpm",
  REMOVE_PNPM: "移除 pnpm",
  UPDATE_NVM: "更新 nvm",
  REMOVE_ENVIRONMENT: "移除空环境",
  UNINSTALL_NODE: "卸载 Node 版本",
  RELEASE: "解除环境管理",
};
export function nodeVersionValue(value: string) {
  return value.replace(/^v/, "");
}
export function nodeVersionRow(
  environment: NodeEnvironment | undefined,
  value: string,
) {
  return environment?.versions?.find(
    (item) =>
      nodeVersionValue(item.softwareVersion) === nodeVersionValue(value) &&
      item.managementMode !== "RELEASED" &&
      item.installationStatus === "READY",
  );
}
export function nodeMaterials(
  materials: InstallationMaterial[],
  recipe?: ServiceRecipe,
) {
  return recipe
    ? materials.filter(
        (item) =>
          item.available &&
          item.recipeKey === recipe.key &&
          item.recipeVersion === recipe.version &&
          item.recipeRevision === recipe.revision &&
          item.runtimeMode === "NATIVE",
      )
    : [];
}
export function nodeJobPending(job?: SoftwareJob) {
  return (
    !!job &&
    (["QUEUED", "RUNNING", "UNKNOWN"].includes(job.status) ||
      job.targetAckPending)
  );
}
export function nodeCommand(command: { executable: string; args: string[] }) {
  const quote = (v: string) =>
    /^[A-Za-z0-9_./:=@+,-]+$/.test(v)
      ? v
      : "'" + v.replaceAll("'", "'\\''") + "'";
  return [command.executable, ...command.args].map(quote).join(" ");
}
export function nodeTerminalCommand(environment: NodeEnvironment) {
  const quote = (v: string) => "'" + v.replaceAll("'", "'\\''") + "'";
  return environment.managerType === "NVM"
    ? `export NVM_DIR=${quote(environment.rootPath + "/nvm")}\n. "$NVM_DIR/nvm.sh"\nnvm use default`
    : "node --version\nnpm --version";
}

export interface NodeInitialIntent {
  hostId: string;
  hostVersion: string;
  environmentId: string;
  context: NonNullable<NodePlan["change"]>["context"];
  definition: ServiceRecipe;
  source: NonNullable<NodePlan["source"]>;
  makeDefault: boolean;
  expiresAt: string;
  initializationJobId: string;
  submitKey: string;
}
function canonical(value: unknown): string {
  if (Array.isArray(value)) return "[" + value.map(canonical).join(",") + "]";
  if (value && typeof value === "object")
    return (
      "{" +
      Object.keys(value)
        .sort()
        .map(
          (k) =>
            JSON.stringify(k) +
            ":" +
            canonical((value as Record<string, unknown>)[k]),
        )
        .join(",") +
      "}"
    );
  return JSON.stringify(value);
}
export function nodeInitialPlanMatches(
  intent: NodeInitialIntent,
  plan: NodePlan,
  env: NodeEnvironment,
  host: { id: string; version: string },
) {
  const source = plan.source,
    node = plan.node;
  return (
    host.id === intent.hostId &&
    host.version === intent.hostVersion &&
    env.id === intent.environmentId &&
    env.managementMode === "MANAGED" &&
    env.account === intent.context.account &&
    env.accountUid === intent.context.accountUid &&
    env.rootPath === intent.context.rootPath &&
    env.nvmVersion === intent.context.nvmVersion &&
    env.observation?.nvmHash === intent.context.nvmHash &&
    !env.observation.versions.length &&
    !env.observedDefaultVersion &&
    canonical(plan.definition) === canonical(intent.definition) &&
    source?.type === intent.source.type &&
    (intent.source.type !== "ARCHIVE" ||
      (source.materialId === intent.source.materialId &&
        source.sha256 === intent.source.sha256 &&
        source.byteSize === intent.source.byteSize)) &&
    node?.environmentId === env.id &&
    node.environmentVersion === env.version &&
    node.nvmVersion === intent.context.nvmVersion &&
    node.nvmHash === intent.context.nvmHash &&
    plan.config?.name === "Node.js " + intent.definition.version &&
    plan.config.runtimeMode === "NATIVE" &&
    plan.config.memoryMiB === 512 &&
    Object.entries(plan.config).every(
      ([key, value]) =>
        ["name", "runtimeMode", "memoryMiB"].includes(key) ||
        value == null ||
        value === "" ||
        value === 0 ||
        value === false ||
        (typeof value === "object" && Object.keys(value).length === 0),
    ) &&
    node.account === env.account &&
    node.accountUid === env.accountUid &&
    node.rootPath === env.rootPath &&
    node.makeDefault === intent.makeDefault &&
    plan.paths?.root ===
      env.rootPath + "/nvm/versions/node/" + intent.definition.version &&
    !plan.checks?.some((c) => c.status !== "PASSED") &&
    Date.parse(intent.expiresAt) > Date.now() &&
    Date.parse(plan.expiresAt) > Date.now()
  );
}
