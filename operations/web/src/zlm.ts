export interface ZLMConfig {
  apiPort: number;
  rtcPort: number;
  mediaAddress: string;
  platformSourceIps: string[];
  secretCredentialId: string;
  secretRevision: string;
  cpus: number;
  maxSources: number;
  maxViewers: number;
}

export const zlmDefaults = (): ZLMConfig => ({
  apiPort: 18080,
  rtcPort: 18000,
  mediaAddress: "",
  platformSourceIps: [],
  secretCredentialId: "",
  secretRevision: "",
  cpus: 4,
  maxSources: 64,
  maxViewers: 64,
});
