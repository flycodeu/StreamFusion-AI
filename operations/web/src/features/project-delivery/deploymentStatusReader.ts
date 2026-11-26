import { shallowReactive } from "vue";
import type { DeploymentStatus } from "../../projectDeployments";

interface DeploymentObservation {
  status?: DeploymentStatus;
  error?: unknown;
  loading: boolean;
}

interface Target {
  binding: string;
  observedAt?: number;
  flight?: { abort: AbortController; done: Promise<void> };
}

// The delivery page owns these observations. A row and its dialog may both read
// the same path, while writes keep their own confirmation and cancellation.
export function createDeploymentStatusReader(
  fetchStatus: (path: string, signal: AbortSignal) => Promise<DeploymentStatus>,
  onError?: (problem: unknown) => void,
) {
  const targets = new Map<string, Target>();
  const observations = shallowReactive(new Map<string, DeploymentObservation>());

  function retain(active: ReadonlyMap<string, string>) {
    for (const [path, target] of targets) {
      if (active.get(path) === target.binding) continue;
      target.flight?.abort.abort();
      targets.delete(path);
      observations.delete(path);
    }
    for (const [path, binding] of active) {
      if (targets.has(path)) continue;
      targets.set(path, { binding });
      observations.set(path, { loading: true });
    }
  }

  function refresh(path: string, maxAgeMs = 0): Promise<void> {
    const target = targets.get(path);
    if (!target) return Promise.resolve();
    if (target.flight) return target.flight.done;
    if (target.observedAt !== undefined && Date.now() - target.observedAt < maxAgeMs)
      return Promise.resolve();

    const abort = new AbortController();
    const done = Promise.resolve().then(async () => {
      try {
        if (abort.signal.aborted) return;
        const status = await fetchStatus(path, abort.signal);
        if (abort.signal.aborted) return;
        target.observedAt = Date.now();
        observations.set(path, { status, loading: false });
      } catch (problem) {
        if (abort.signal.aborted) return;
        observations.set(path, { status: observations.get(path)?.status, error: problem, loading: false });
        onError?.(problem);
      } finally {
        if (target.flight?.abort === abort) target.flight = undefined;
      }
    });
    target.flight = { abort, done };
    return done;
  }

  function recordQueued(path: string, status: DeploymentStatus) {
    const target = targets.get(path);
    if (!target) return;
    target.flight?.abort.abort();
    target.flight = undefined;
    target.observedAt = undefined;
    observations.set(path, { status, loading: false });
  }

  return {
    retain,
    refresh,
    recordQueued,
    observation: (path: string) => observations.get(path),
  };
}

export type DeploymentStatusReader = ReturnType<typeof createDeploymentStatusReader>;
