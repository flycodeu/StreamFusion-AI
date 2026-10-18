import { beforeEach, expect, it, vi } from 'vitest'
import { parseCluster, parseOpsSyncReview, synchronizeOpsNode } from './api'

const api = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../client', () => ({ request: api.request }))
const source = {
  controllerId: 'controller-a',
  targetId: 'target-a',
  instanceId: 'instance-a',
  releaseId: 'release-b',
  syncedAt: null,
}
const budget = {
  maxSources: 8,
  maxViewers: 8,
  maxTranscodes: 1,
  maxStarting: 4,
  ingressKbps: 81920,
  egressKbps: 81920,
  sourceUpperKbps: 8192,
  outputUpperKbps: 8192,
}
const review = {
  nodeId: 'node-a',
  version: 7,
  previousSource: null,
  candidateSource: source,
  previousConfigVersion: 'a',
  candidateConfigVersion: 'b',
  previousName: 'A',
  candidateName: 'B',
  previousInstallPath: null,
  candidateInstallPath: null,
  retainedBudget: budget,
  candidateLimit: budget,
  credentialChanged: false,
  digest: 'a'.repeat(64),
  checkedAt: '2026-10-18T02:00:00Z',
}
const input = {
  controllerId: 'controller-a',
  targetId: 'target-a',
  instanceId: 'instance-a',
  releaseId: 'release-b',
  version: 7,
}
beforeEach(() => {
  api.request.mockReset()
})

it('uses separate preflight and apply with a version and reviewed candidate digest', async () => {
  api.request.mockResolvedValue({ data: { applied: true, reasonCode: null, review }, meta: {} })
  await synchronizeOpsNode('node-a', input)
  expect(api.request).toHaveBeenLastCalledWith(
    expect.objectContaining({
      path: '/preview-node/nodes/node-a/ops-sync/check',
      method: 'POST',
      body: input,
    }),
  )
  await synchronizeOpsNode('node-a', { ...input, digest: review.digest }, true)
  expect(api.request).toHaveBeenLastCalledWith(
    expect.objectContaining({
      path: '/preview-node/nodes/node-a/ops-sync',
      body: { ...input, digest: review.digest },
    }),
  )
})

it('never interprets an Ops handoff conflict as a successful sync', async () => {
  api.request.mockResolvedValue({
    data: { applied: false, reasonCode: 'OPS_JOB_PENDING', review: null },
    meta: {},
  })
  await expect(synchronizeOpsNode('node-a', input, true)).rejects.toMatchObject({
    code: 'OPS_JOB_PENDING',
  })
})

it('retains only non-sensitive review fields and keeps nullable legacy provenance compatible', () => {
  const decoded = parseOpsSyncReview({
    ...review,
    apiSecret: 'sensitive',
    machineToken: 'sensitive',
    tokenFile: '/private',
  })
  expect(decoded.previousSource).toBeNull()
  expect(decoded.candidateSource).toEqual(source)
  expect(decoded.previousInstallPath).toBeNull()
  expect(decoded.candidateInstallPath).toBeNull()
  expect(JSON.stringify(decoded)).not.toContain('sensitive')
  expect(JSON.stringify(decoded)).not.toContain('/private')
  expect(() =>
    parseOpsSyncReview({ ...review, candidateSource: { ...source, controllerId: null } }),
  ).toThrow()
  const node = {
    nodeId: 'node-a',
    name: 'A',
    mode: 'DRAINING',
    version: 7,
    priority: 27,
    budget,
    usage: { sources: 0, viewers: 0, transcodes: 0, starting: 0, ingressKbps: 0, egressKbps: 0 },
    state: 'READY',
    reasonCode: null,
    desiredConfigVersion: 'a',
  }
  expect(parseCluster({ nodes: [node], registrationOptions: [] }).nodes[0]?.opsSource).toBeNull()
  expect(
    parseCluster({ nodes: [{ ...node, opsSource: source }], registrationOptions: [] }).nodes[0]
      ?.opsSource,
  ).toEqual(source)
})
