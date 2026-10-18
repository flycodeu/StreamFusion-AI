import { beforeEach, expect, it, vi } from 'vitest'
import {
  createDirectZlmInstance,
  deleteDirectZlmInstance,
  rotateDirectZlmSecret,
  saveDirectZlmInstance,
  getOpsZlmInstances,
  importOpsZlmInstance,
  type DirectZlmInstance,
} from './api'

const api = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../client', () => ({ request: api.request }))

const instance: DirectZlmInstance = {
  nodeId: 'zlm-a',
  name: 'ZLM A',
  apiHost: '10.0.0.10',
  apiPort: 8080,
  rtcPort: 8000,
  rtspPort: 8554,
  portsObservedAt: '2026-10-18T12:00:00Z',
  zlmVersion: 'v1',
  mediaAddress: '10.0.0.10',
  installPath: '/opt/zlm',
  mode: 'LOCAL',
  version: 3,
  priority: 100,
  budget: {
    maxSources: 64,
    maxViewers: 64,
    maxTranscodes: 0,
    maxStarting: 4,
    ingressKbps: 655360,
    egressKbps: 655360,
    sourceUpperKbps: 8192,
    outputUpperKbps: 8192,
  },
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
}

beforeEach(() => {
  api.request.mockReset()
  api.request.mockResolvedValue({
    data: { applied: true, reasonCode: null },
    meta: {},
  })
})

it('sends the ZLM secret only in the create request', async () => {
  await createDirectZlmInstance({
    name: 'ZLM A',
    apiHost: '10.0.0.10',
    apiPort: 8080,
    mediaAddress: '10.0.0.10',
    installPath: '/opt/zlm',
    secret: 'once-only',
    priority: 100,
    budget: instance.budget,
  })
  expect(api.request).toHaveBeenCalledWith(
    expect.objectContaining({
      path: '/preview-node/zlm-instances',
      method: 'POST',
      body: expect.objectContaining({ secret: 'once-only', installPath: '/opt/zlm' }),
    }),
  )
})

it('updates a direct instance with its version and without a stored secret', async () => {
  await saveDirectZlmInstance(instance, { mode: 'REMOTE' })
  expect(api.request).toHaveBeenCalledWith(
    expect.objectContaining({
      path: '/preview-node/zlm-instances/zlm-a',
      method: 'PUT',
      body: {
        mode: 'REMOTE',
        priority: 100,
        budget: instance.budget,
        installPath: '/opt/zlm',
        version: 3,
      },
    }),
  )
  expect(JSON.stringify(api.request.mock.calls)).not.toContain('secret')
})

it('sends an edited install path through the versioned direct instance update', async () => {
  await saveDirectZlmInstance(instance, { installPath: '/srv/zlm-new' })
  expect(api.request).toHaveBeenCalledWith(
    expect.objectContaining({
      path: '/preview-node/zlm-instances/zlm-a',
      method: 'PUT',
      body: expect.objectContaining({ installPath: '/srv/zlm-new', version: 3 }),
    }),
  )
})

it('uses a separate credential operation and versioned removal', async () => {
  await rotateDirectZlmSecret(instance, 'new-secret')
  expect(api.request).toHaveBeenCalledWith(
    expect.objectContaining({
      path: '/preview-node/zlm-instances/zlm-a/credential',
      method: 'PUT',
      body: { secret: 'new-secret', version: 3 },
    }),
  )
  api.request.mockClear()
  await deleteDirectZlmInstance(instance)
  expect(api.request).toHaveBeenCalledWith(
    expect.objectContaining({
      path: '/preview-node/zlm-instances/zlm-a?version=3',
      method: 'DELETE',
    }),
  )
})

it('reads the Ops ZLM catalog and imports only the selected published release', async () => {
  api.request.mockResolvedValueOnce({
    data: {
      available: true,
      reasonCode: null,
      items: [
        {
          instanceId: 'zlm-one',
          name: 'ZLM A',
          targetName: 'A',
          targetAddress: '10.0.0.10',
          installRoot: '/opt/zlm',
          installPath: '/opt/zlm/instances/zlm-one/releases/release-a',
          releaseId: 'release-a',
          apiUrl: 'http://10.0.0.10:8080',
          mediaAddress: '10.0.0.10',
          apiPort: 8080,
          rtcPort: 8000,
          maxSources: 64,
          maxViewers: 64,
          maxTranscodes: 0,
        },
      ],
    },
    meta: {},
  })
  const listing = await getOpsZlmInstances()
  expect(listing.items).toHaveLength(1)
  expect(listing.items[0]?.installPath).toBe('/opt/zlm/instances/zlm-one/releases/release-a')
  expect(JSON.stringify(listing)).not.toContain('secret')
  expect(api.request).toHaveBeenCalledWith(
    expect.objectContaining({
      path: '/preview-node/ops-zlm-instances',
      method: 'GET',
    }),
  )
  await importOpsZlmInstance('zlm-one', 'release-a')
  expect(api.request).toHaveBeenCalledWith(
    expect.objectContaining({
      path: '/preview-node/ops-zlm-import',
      method: 'POST',
      body: { instanceId: 'zlm-one', releaseId: 'release-a' },
    }),
  )
})
