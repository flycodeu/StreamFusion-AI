export interface AutomaticPreparationUpdate {
  mode?: "UPDATE" | "AUTHORIZE" | "INSTALL";
  hostId: string;
  expectedHostVersion: string;
  currentSHA256: string;
  packageSHA256: string;
}

export interface PreparationUpdateCheck {
  hostId: string;
  expectedHostVersion: string;
  update?: {
    status: "CURRENT" | "AVAILABLE" | "MANUAL_REQUIRED" | "BLOCKED";
    code?: string;
    currentSHA256?: string;
    packageSHA256?: string;
  };
}

// Missing fields never grant update authority. The controller repeats these
// checks while holding the host's existing operation lock.
export function automaticPreparationUpdate(value: PreparationUpdateCheck): AutomaticPreparationUpdate | undefined {
  const update = value.update;
  if (update?.status !== "AVAILABLE" || !value.hostId || !value.expectedHostVersion ||
      !/^[a-f0-9]{64}$/.test(update.currentSHA256 || "") ||
      !/^[a-f0-9]{64}$/.test(update.packageSHA256 || "") ||
      update.currentSHA256 === update.packageSHA256) return;
  return { hostId: value.hostId, expectedHostVersion: value.expectedHostVersion, currentSHA256: update.currentSHA256!, packageSHA256: update.packageSHA256! };
}

export function installationPreparation(value: PreparationUpdateCheck, code: string): AutomaticPreparationUpdate | undefined {
  const update = automaticPreparationUpdate(value);
  if (update) return { ...update, mode: "UPDATE" };
  const state = value.update;
  if (!value.hostId || !value.expectedHostVersion || !/^[a-f0-9]{64}$/.test(state?.packageSHA256 || "")) return;
  const context = { hostId: value.hostId, expectedHostVersion: value.expectedHostVersion,
    currentSHA256: state?.currentSHA256 || "", packageSHA256: state!.packageSHA256! };
  if (code === "COMMAND_TRUST_REQUIRED" && state?.status === "CURRENT" && context.currentSHA256 === context.packageSHA256)
    return { ...context, mode: "AUTHORIZE" };
  if (state?.status === "MANUAL_REQUIRED" && ["EXECUTOR_MISSING", "SUDO_AUTHORIZATION_REQUIRED", "EXECUTOR_PRIVILEGE_REQUIRED"].includes(state.code || ""))
    return { ...context, mode: "INSTALL" };
}
