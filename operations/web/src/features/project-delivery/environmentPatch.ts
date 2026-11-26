import type {
  ModuleConfigDraft, ModuleVariable, NginxDeployConfig, ProjectServiceRegistration,
} from "../../applicationProjects";

export interface EnvironmentFormVariable {
  name: string;
  mode: "PLAIN" | "CREDENTIAL";
  value: string;
  credentialRef: string;
  profile?: string;
}

export interface EnvironmentFormProxyRule {
  location: string;
  targetType: "MODULE" | "URL";
  targetModuleId: string;
  upstreamUrl: string;
  stripPrefix: boolean;
  webSocket: boolean;
  sse: boolean;
}

interface EnvironmentForm {
  version: string;
  moduleId: string;
  environment: string;
  hostId: string;
  managedInstanceId: string;
  runtimeSoftwareId: string;
  deployMode: string;
  profile: string;
  bindAddress: string;
  servicePort: number | undefined;
  containerPort: number | undefined;
  memoryMiB: number | undefined;
  startCommand: string;
  healthPath: string;
  dependencyIds: string[];
  variables: EnvironmentFormVariable[];
  configDrafts?: ModuleConfigDraft[];
  nginx: {
    nginxSoftwareId: string;
    siteName: string;
    serverName: string;
    listenPort: number | undefined;
    spa: boolean;
    proxyRules: EnvironmentFormProxyRule[];
  };
}

type RegistrationFields = Pick<ProjectServiceRegistration,
  "id" | "moduleId" | "hostId" | "recordedVersion" | "managedInstanceId" | "runtimeSoftwareId" |
  "deployMode" | "profile" | "bindAddress" | "servicePort" | "memoryMiB" | "ports" | "startCommand" |
  "healthPath" | "dependencyIds" | "variables" | "configDrafts" | "nginx">;
type EnvironmentPatch = Partial<RegistrationFields> & {
  version: string;
  moduleId: string;
  environment: string;
  hostId: string;
  recordedVersion: string;
};

export function buildEnvironmentPatch(form: EnvironmentForm, original?: RegistrationFields): EnvironmentPatch {
  const isStatic = form.deployMode === "STATIC";
  const servicePort = isStatic ? 0 : Number(form.servicePort) || 0;
  const memoryMiB = isStatic ? 0 : Number(form.memoryMiB) || 0;
  const startCommand = isStatic ? "" : form.startCommand.trim();
  const healthPath = isStatic ? "" : form.healthPath.trim();
  const profile = form.profile.trim();
  const bindAddress = form.bindAddress.trim();
  const dependencyIds = form.dependencyIds.filter(Boolean);
  const variables = form.variables.flatMap((variable): ModuleVariable[] => {
    const name = variable.name.trim();
    if (!name) return [];
    if (variable.mode !== "CREDENTIAL" || !variable.credentialRef) return [{ name, value: variable.value }];
    const [credentialId, credentialRevision] = variable.credentialRef.split(":");
    return [{ name, credentialId, credentialRevision }];
  });
  const nginx: NginxDeployConfig | undefined = isStatic ? {
    nginxSoftwareId: form.nginx.nginxSoftwareId,
    siteName: form.nginx.siteName.trim(),
    serverName: form.nginx.serverName.trim() || "_",
    listenPort: Number(form.nginx.listenPort) || 80,
    spa: form.nginx.spa,
    proxyRules: form.nginx.proxyRules.flatMap((rule) => {
      const location = rule.location.trim();
      if (!location) return [];
      return [{
        location,
        ...(rule.targetType === "MODULE" && rule.targetModuleId
          ? { targetModuleId: rule.targetModuleId }
          : { upstreamUrl: rule.upstreamUrl.trim() }),
        ...(rule.stripPrefix ? { stripPrefix: true } : {}),
        ...(rule.webSocket ? { webSocket: true } : {}),
        ...(rule.sse ? { sse: true } : {}),
      }];
    }),
  } : undefined;
  const patch: EnvironmentPatch = {
    ...(original?.id ? { id: original.id } : {}),
    version: form.version,
    moduleId: form.moduleId,
    environment: form.environment.trim(),
    hostId: form.hostId,
    recordedVersion: original?.recordedVersion || "",
  };

  // Omitted fields leave existing values in place; empty values explicitly clear them.
  if (form.managedInstanceId !== (original?.managedInstanceId || "")) patch.managedInstanceId = form.managedInstanceId;
  if (form.runtimeSoftwareId !== (original?.runtimeSoftwareId || "")) patch.runtimeSoftwareId = form.runtimeSoftwareId;
  if (form.deployMode !== (original?.deployMode || "")) patch.deployMode = form.deployMode;
  if (profile !== (original?.profile || "")) patch.profile = profile;
  if (bindAddress !== (original?.bindAddress || "")) patch.bindAddress = bindAddress;
  if (servicePort !== (original?.servicePort || 0)) patch.servicePort = servicePort;
  if (memoryMiB !== (original?.memoryMiB || 0)) patch.memoryMiB = memoryMiB;
  if (form.deployMode === "DOCKER" && servicePort > 0) {
    patch.ports = [{ hostPort: servicePort, containerPort: Number(form.containerPort) || servicePort, protocol: "TCP" }];
  }
  if (startCommand !== (original?.startCommand || "")) patch.startCommand = startCommand;
  if (healthPath !== (original?.healthPath || "")) patch.healthPath = healthPath;
  if (JSON.stringify(variables) !== JSON.stringify(original?.variables || [])) patch.variables = variables;
  if (JSON.stringify(dependencyIds) !== JSON.stringify(original?.dependencyIds || [])) patch.dependencyIds = dependencyIds;
  if (nginx) {
    if (JSON.stringify(nginx) !== JSON.stringify(original?.nginx || null)) patch.nginx = nginx;
  } else if (original?.nginx) patch.nginx = {};
  if (form.configDrafts && JSON.stringify(form.configDrafts) !== JSON.stringify(original?.configDrafts || []))
    patch.configDrafts = form.configDrafts;
  return patch;
}
