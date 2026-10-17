import { describe, expect, it } from 'vitest'
import { parseCluster, parseNodeAction, parsePreviewNode } from './api'
const local = {
  mode: 'LOCAL',
  version: -1,
  nodeId: null,
  name: null,
  controlUri: null,
  mediaAddress: null,
  desiredConfigVersion: null,
  state: 'UNKNOWN',
  reasonCode: null,
  observedAt: null,
  observation: null,
  retainedViewers: 0,
  registrationAvailable: false,
}
describe('preview node contracts', () => {
  it('keeps desired mode and observed health separate and drops unrecognized sensitive fields', () => {
    const value = parsePreviewNode({
      ...local,
      mode: 'DRAINING',
      state: 'UNREACHABLE',
      retainedViewers: 3,
      tokenFile: '/private/key',
      secret: 'not-public',
    })
    expect(value.mode).toBe('DRAINING')
    expect(value.state).toBe('UNREACHABLE')
    expect(value.retainedViewers).toBe(3)
    expect(JSON.stringify(value)).not.toContain('private')
    expect(JSON.stringify(value)).not.toContain('secret')
  })
  it('does not interpret an unsuccessful selection receipt as an enabled node', () => {
    expect(
      parseNodeAction({ applied: false, reasonCode: 'NODE_DRAIN_REQUIRED', node: local }),
    ).toEqual({ applied: false, reasonCode: 'NODE_DRAIN_REQUIRED', node: local })
  })
  it('rejects malformed or missing capacity observations', () => {
    expect(() => parsePreviewNode({ ...local, retainedViewers: null })).toThrow()
    expect(() => parsePreviewNode({ ...local, observation: { resources: [] } })).toThrow()
  })
  it('keeps per-node budgets and retained usage separate from observed physical resources', () => {
    const node = {
      ...local,
      nodeId: 'a',
      name: 'A',
      mode: 'DRAINING',
      version: 3,
      priority: 100,
      desiredConfigVersion: 'config-v3',
      budget: {
        maxSources: 64,
        maxViewers: 64,
        maxTranscodes: 64,
        maxStarting: 4,
        ingressKbps: 655360,
        egressKbps: 655360,
        sourceUpperKbps: 8192,
        outputUpperKbps: 8192,
      },
      usage: {
        sources: 1,
        viewers: 2,
        transcodes: 1,
        starting: 0,
        ingressKbps: 10240,
        egressKbps: 20480,
      },
      protection: { cpuLoad: null, reasonCode: null },
      tokenFile: 'private-path',
    }
    const result = parseCluster({ nodes: [node], registrationOptions: [] }).nodes[0]!
    expect(result.mode).toBe('DRAINING')
    expect(result.budget.maxTranscodes).toBe(64)
    expect(result.usage).toEqual(node.usage)
    expect(result.observation).toBeNull()
    expect(result.protection?.cpuLoad).toBeNull()
    expect(JSON.stringify(result)).not.toContain('private-path')
    expect(() =>
      parseCluster({
        nodes: [{ ...node, usage: { ...node.usage, egressKbps: null } }],
        registrationOptions: [],
      }),
    ).toThrow()
  })
})
