import type { ServiceRecipe } from "./serviceInstallation";
import type { SoftwareInstance } from "./software";

export interface CredentialReference { id: string; revision: string }
export interface ParameterDefinition {
  key: string; label: string;
  type: "TEXT" | "INTEGER" | "BOOLEAN" | "SELECT" | "PATH" | "PORT" | "CREDENTIAL";
  required?: boolean; default?: string; options?: string[]; min?: number; max?: number;
}
export interface ResourceDefinition {
  key: string; type: "PATH_READ" | "PATH_WRITE" | "PORT" | "UNIT" | "CONTAINER";
  value: string; protocol?: string; bindAddress?: string; release?: boolean;
}
export interface ManagementStep {
  name: string; script: string; runAs: "ROOT" | "ACCOUNT"; timeoutSeconds: number;
  workingDirectory?: string; phase?: string;
}
export interface ManagementOperation {
  key: string; name: string; group?: string; readOnly?: boolean; runtimeModes?: string[];
  parameters?: ParameterDefinition[]; resources?: ResourceDefinition[]; steps: ManagementStep[]; verifySteps?: ManagementStep[];
  resultFormat: "TEXT" | "TABLE";
  rebind?: boolean;
  rollbackSteps?: ManagementStep[];
}
export interface ManagementDefinition {
  scriptPrelude?: string;
  parameters?: ParameterDefinition[]; operations: ManagementOperation[]; resources?: ResourceDefinition[];
}
export interface ManagementTable { columns: string[]; rows: Record<string, string>[]; total?: number }
export interface ManagementContext {
  softwareVersion?: string;
  definition?: ServiceRecipe; templateId?: string; templateRevision?: number; stateVersion: string;
  parameters?: Record<string, string>; credentialRefs?: Record<string, CredentialReference>;
  operations?: ManagementOperation[];
  lastJobId?: string;
  availableTemplate?: ManagementContext;
  templates?: { id: string; name: string; revision: number; definition: ServiceRecipe }[];
}
export function parameterDefaults(fields: ParameterDefinition[] = [], values: Record<string, string> = {}) {
  return Object.fromEntries(fields.filter(field => field.type !== "CREDENTIAL").map(field => [field.key, values[field.key] ?? field.default ?? ""]));
}
export function managementOperations(context: ManagementContext, item: SoftwareInstance) {
  const runtime = item.installation?.config?.runtimeMode || (item.runtimeKind === "DOCKER" ? "DOCKER" : "NATIVE");
  return (context.definition?.management?.operations || context.operations || []).filter(operation => !operation.runtimeModes?.length || operation.runtimeModes.includes(runtime));
}
export function configurationContext(context: ManagementContext, item: SoftwareInstance) {
  const latest = context.availableTemplate;
  return latest && managementOperations(latest, item).some(operation => operation.key === "configure" && !operation.readOnly)
    ? { ...latest, softwareVersion: context.softwareVersion, stateVersion: context.stateVersion, parameters: context.parameters, credentialRefs: context.credentialRefs, lastJobId: context.lastJobId } : context;
}
export function hasConfiguration(context: ManagementContext, item: SoftwareInstance) {
  const operations = managementOperations(context, item);
  return operations.some(operation => operation.key === "configure" && !operation.readOnly) && operations.some(operation => operation.key === "read-config" && operation.readOnly);
}
export function builtinConfigurationAvailable(item: SoftwareInstance) {
  const definition = item.installation?.definition;
  return !!definition && (!definition.commands || !!definition.commands.builtin) && !item.installation?.official && definition.kind !== "TOOL" && definition.key !== "docker" && item.managementMode === "MANAGED" && item.installationStatus === "READY";
}
export function softwareConfigurationAvailable(item: SoftwareInstance) {
  return item.managementMode === "MANAGED" && item.installationStatus === "READY" &&
    (item.configurationAvailable ?? (hasConfiguration({ definition: item.installation?.definition, stateVersion: "0" }, item) || builtinConfigurationAvailable(item)));
}
export function configurationValues(table: ManagementTable | undefined, fields: ParameterDefinition[]) {
  if (!table?.columns.includes("key") || !table.columns.includes("value")) throw new Error("当前配置读取结果不完整，请检查管理模板");
  const allowed = fields.filter(field => field.type !== "CREDENTIAL"), values: Record<string, string> = {};
  for (const row of table.rows) {
    if (!allowed.some(field => field.key === row.key)) continue;
    if (Object.hasOwn(values, row.key) || typeof row.value !== "string") throw new Error("当前配置读取结果不完整，请重新读取");
    values[row.key] = row.value;
  }
  if (allowed.some(field => !Object.hasOwn(values, field.key))) throw new Error("当前配置读取结果缺少参数，请检查管理模板");
  return values;
}
export function parameterChanges(fields: ParameterDefinition[], before: Record<string, string>, after: Record<string, string>) {
  return fields.filter(field => field.type !== "CREDENTIAL" && (before[field.key] || "") !== (after[field.key] || ""))
    .map(field => ({ key: field.key, label: field.label, before: before[field.key] || "未设置", after: after[field.key] || "未设置" }));
}
export function parameterError(fields: ParameterDefinition[] = [], values: Record<string, string> = {}, refs: Record<string, CredentialReference> = {}): string {
  for (const field of fields) {
    const value = values[field.key] ?? field.default ?? "";
    if (field.type === "CREDENTIAL") {
      if (field.required && !refs[field.key]?.id) return `请填写${field.label}`;
      continue;
    }
    if (!value.trim()) { if (field.required) return `请填写${field.label}`; if (!value) continue; }
    if (["INTEGER", "PORT"].includes(field.type)) {
      const number = Number(value), min = field.type === "PORT" ? Math.max(1, field.min ?? 1) : field.min, max = field.type === "PORT" ? Math.min(65535, field.max ?? 65535) : field.max;
      if (!/^-?\d+$/.test(value) || !Number.isSafeInteger(number) || min != null && number < min || max != null && number > max) return `${field.label}超出允许范围`;
    }
    if (field.type === "BOOLEAN" && !["true", "false"].includes(value)) return `请检查${field.label}`;
    if (field.type === "SELECT" && !field.options?.includes(value)) return `请选择${field.label}`;
    if (field.type === "PATH" && (!value.startsWith("/") || value.length > 512 || /[\r\n]/.test(value) || value !== "/" && value.slice(1).split("/").some(part => !part || part === "." || part === ".."))) return `请填写${field.label}的完整规范路径`;
    if (value.length > 8192 || value.includes("\0")) return `${field.label}内容无效或过长`;
  }
  return "";
}
