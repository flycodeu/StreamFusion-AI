import { describe, expect, it } from 'vitest'
import {
  parseCluster,
  parseDirectZlmInstances,
  parseNodeAction,
  parseOpsPreviewInstances,
  parsePreviewNode,
} from './api'
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
      kind: 'ZLM_DIRECT',
      name: 'A',
      mode: 'DRAINING',
      version: 3,
      priority: 100,
      zlmApiAddress: 'http://10.0.0.10:8080',
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
    expect(result.kind).toBe('ZLM_DIRECT')
    expect(result.zlmApiAddress).toBe('http://10.0.0.10:8080')
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
  it('accepts the Ops catalog identity and capacity without retaining credentials', () => {
    const catalog = parseOpsPreviewInstances({
      available: true,
      reasonCode: null,
      items: [
        {
          instanceId: 'instance-a',
          name: 'A',
          targetName: 'host-a',
          targetAddress: '10.0.0.10',
          installRoot: '/srv/preview',
          releaseId: 'release-1',
          nodeId: 'node-a',
          controlUri: 'https://10.0.0.10:9443',
          mediaAddress: '10.0.0.10',
          maxSources: 64,
          maxViewers: 64,
          maxTranscodes: 64,
          machineToken: 'must-never-appear',
        },
      ],
    })
    expect(catalog.items[0]?.instanceId).toBe('instance-a')
    expect(catalog.items[0]?.maxTranscodes).toBe(64)
    expect(JSON.stringify(catalog)).not.toContain('must-never-appear')
  })
  it('parses direct ZLM instances without retaining the supplied secret', () => {
    const budget = {
      maxSources: 64,
      maxViewers: 64,
      maxTranscodes: 0,
      maxStarting: 4,
      ingressKbps: 655360,
      egressKbps: 655360,
      sourceUpperKbps: 8192,
      outputUpperKbps: 8192,
    }
    const parsed = parseDirectZlmInstances({
      registrationAvailable: true,
      items: [
        {
          nodeId: 'zlm-a',
          name: 'ZLM A',
          apiHost: '10.0.0.10',
          apiPort: 8080,
          mediaAddress: '10.0.0.10',
          mode: 'LOCAL',
          version: 1,
          priority: 100,
          budget,
          usage: {
            sources: 0,
            viewers: 0,
            transcodes: 0,
            starting: 0,
            ingressKbps: 0,
            egressKbps: 0,
          },
          state: 'READY',
          reasonCode: null,
          observedAt: null,
          secret: 'must-never-appear',
        },
      ],
    })
    expect(parsed.items[0]?.apiHost).toBe('10.0.0.10')
    expect(parsed.items[0]?.budget.maxTranscodes).toBe(0)
    expect(JSON.stringify(parsed)).not.toContain('must-never-appear')
  })
})
