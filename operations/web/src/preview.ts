export interface PreviewConfig {
  nodeId: string;
  controlHost: string;
  mediaAddress: string;
  allowedTargets: string[];
  cpus: number;
  maxSources: number;
  maxViewers: number;
  maxTranscodes: number;
  maxStarting: number;
  ingressKbps: number;
  egressKbps: number;
  sourceUpperKbps: number;
  outputUpperKbps: number;
}
export const previewDefaults = (): PreviewConfig => ({
  nodeId: "",
  controlHost: "",
  mediaAddress: "",
  allowedTargets: [],
  cpus: 8,
  maxSources: 64,
  maxViewers: 64,
  maxTranscodes: 64,
  maxStarting: 4,
  ingressKbps: 655360,
  egressKbps: 655360,
  sourceUpperKbps: 8192,
  outputUpperKbps: 8192,
});
