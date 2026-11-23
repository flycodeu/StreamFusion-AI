import { shellArgument } from "./officialInstallation";
import type { SoftwareInstance } from "./software";
import { isPresetJenkins } from "./webAccess";

export function isOpsUnlockConfirmationPassword(password: string): boolean {
  return password.length > 0 && new TextEncoder().encode(password).length <= 64;
}

export function isJenkinsBootstrapPassword(value: unknown): value is string {
  return typeof value === "string" && value.length === 32 && /^[0-9a-f]+$/.test(value);
}

export function canCopyJenkinsUnlockPassword(item?: SoftwareInstance): boolean {
  const recipe = item?.installation?.definition;
  return !!recipe && isPresetJenkins(recipe) && item?.managementMode === "MANAGED" &&
    item.installationStatus === "READY" && item.actualState === "RUNNING";
}

export function jenkinsUnlockFallbackCommand(item: SoftwareInstance): string {
  const directory = item.installationPaths?.data || "";
  const data = directory.startsWith("/") && !/[\u0000-\u001f\u007f]/.test(directory) ? directory.replace(/\/+$/, "") : "";
  if (data) return "sudo cat -- " + shellArgument(data + "/secrets/initialAdminPassword");
  if (item.runtimeKind !== "DOCKER") return "";
  const container = [item.locator, item.observation?.containerName, item.installationEvidence?.containerId]
    .find(value => value && /^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/.test(value));
  return container ? `sudo docker exec ${shellArgument(container)} cat /var/jenkins_home/secrets/initialAdminPassword` : "";
}
