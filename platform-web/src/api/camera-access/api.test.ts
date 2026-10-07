import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  createAccessJob,
  createScanJob,
  getAccessJob,
  importCandidates,
  previewImport,
  getImportJobs,
  previewBulkImport,
  startBulkImport,
  getImportItems,
} from './api'
import { parseAccessJob, parseAccessOptions } from './types'
import { parseSource } from '../camera-sources/types'
import { clearIdentity, sessionState } from '../../session/state'

const job = {
  jobId: '9007199254740993',
  version: '2',
  status: 'PARTIAL',
  method: 'ONVIF',
  sourceId: null,
  expiresAt: '2026-10-06T00:15:00Z',
  complete: false,
  device: { name: 'NVR', manufacturer: 'vendor', model: 'model' },
  diagnostic: null,
  result: null,
  warnings: ['部分通道不可读取'],
  candidates: [
    {
      candidateId: 'local-channel-id',
      name: '通道一',
      mappingRequired: false,
      profiles: [
        {
          profileId: 'local-profile-id',
          name: '主码流',
          usageHint: 'MAIN',
          videoCodec: 'H264',
          width: 1920,
          height: 1080,
          frameRate: 25,
          bitrateKbps: null,
        },
      ],
    },
  ],
}
function response(data: unknown, status = 200) {
  return new Response(
    JSON.stringify({
      code: 'SUCCESS',
      msg: '成功',
      data,
      traceId: 'a'.repeat(32),
      timestamp: '2026-10-06T00:00:00Z',
    }),
    { status, headers: { 'Content-Type': 'application/json', 'X-Trace-Id': 'a'.repeat(32) } },
  )
}
beforeEach(() => {
  vi.stubEnv('DEV', true)
  clearIdentity()
  sessionState.csrf = { headerName: 'X-CSRF-TOKEN', token: 'test-csrf' }
})
afterEach(() => {
  clearIdentity()
  vi.unstubAllEnvs()
  vi.unstubAllGlobals()
})

describe('camera access HTTP and bounded safe view', () => {
  it('uses persistent bulk task endpoints with signed scope and a stable client request', async () => {
    const bulk = {
      ...job,
      kind: 'BULK_IMPORT',
      status: 'QUEUED',
      sourceId: '3',
      sourceVersion: '4',
      candidates: [],
      bulk: {
        groupId: '5',
        pageNumber: 1,
        total: 1200,
        processedCount: 0,
        createdCount: 0,
        existingCount: 0,
        failedCount: 0,
        duplicateCount: 0,
        maxItems: 10000,
      },
    }
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(
        response({
          confirmation: 'bulk-proof',
          groupPath: '厂区',
          affectedUserCount: 2,
          declaredTotal: 1200,
          maxItems: 10000,
          pageSize: 100,
        }),
      )
      .mockResolvedValueOnce(response(bulk, 202))
    vi.stubGlobal('fetch', fetch)
    const scope = { version: '2', groupId: '5' }
    expect((await previewBulkImport(job.jobId, scope)).declaredTotal).toBe(1200)
    const command = { ...scope, confirmation: 'bulk-proof', clientRequestId: '1791244800000-bulk' }
    const result = await startBulkImport(job.jobId, command)
    expect(result.kind).toBe('BULK_IMPORT')
    expect(result.sourceVersion).toBe('4')
    expect(fetch.mock.calls[0]![0]).toBe(`/api/camera-access/jobs/${job.jobId}/bulk-import-preview`)
    expect(fetch.mock.calls[1]![0]).toBe(`/api/camera-access/jobs/${job.jobId}/bulk-import`)
    expect(JSON.parse(fetch.mock.calls[1]![1].body)).toEqual(command)
  })
  it('recovers paged import summaries and failed items without passing through secret snapshots', async () => {
    const summary = {
      ...job,
      kind: 'BULK_IMPORT',
      sourceId: '3',
      sourceVersion: '4',
      candidates: undefined,
      secret: 'must-not-pass',
      bulk: {
        groupId: null,
        pageNumber: 2,
        total: null,
        processedCount: 100,
        createdCount: 90,
        existingCount: 9,
        failedCount: 1,
        duplicateCount: 0,
        maxItems: 10000,
      },
    }
    const item = {
      id: '6',
      externalKey: 'platform-camera-1',
      pageNumber: 2,
      itemIndex: 4,
      status: 'FAILED',
      name: '点位',
      reasonCode: 'CONFLICT',
      createdAt: '2026-10-06T00:00:00Z',
      secret: 'must-not-pass',
    }
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(response({ items: [summary], page: 2, size: 20, total: 21 }))
      .mockResolvedValueOnce(response({ items: [item], page: 1, size: 20, total: 1 }))
    vi.stubGlobal('fetch', fetch)
    const jobs = await getImportJobs({ page: 2, size: 20 })
    expect(jobs.items[0]).not.toHaveProperty('secret')
    expect(jobs.items[0]!.candidates).toEqual([])
    const items = await getImportItems(job.jobId, { page: 1, size: 20, status: 'FAILED' })
    expect(items.items[0]).not.toHaveProperty('secret')
    expect(items.items[0]!.reasonCode).toBe('CONFLICT')
    const query = new URL(fetch.mock.calls[0]![0], 'http://localhost')
    expect(query.searchParams.get('kind')).toBe('BULK_IMPORT')
    expect(query.searchParams.get('page')).toBe('2')
  })
  it('creates a bounded scan using the scan endpoint rather than a catalog request', async () => {
    const fetch = vi.fn().mockResolvedValue(response({ ...job, kind: 'SCAN' }, 202))
    vi.stubGlobal('fetch', fetch)
    const command = {
      clientRequestId: '1791244800000-scan',
      networkPolicyKey: 'approved',
      startAddress: '192.0.2.1',
      endAddress: '192.0.2.2',
      ports: [80, 443],
    }
    expect(await createScanJob(command)).toEqual({ jobId: job.jobId })
    expect(fetch.mock.calls[0]![0]).toBe('/api/camera-access/scan-jobs')
    expect(JSON.parse(fetch.mock.calls[0]![1].body)).toEqual(command)
  })
  it('preserves port-only candidates, catalog pagination and channels with no streams', () => {
    const parsed = parseAccessJob({
      ...job,
      kind: 'SCAN',
      candidates: [],
      device: null,
      hosts: [
        {
          candidateId: 'opaque-host',
          host: '192.0.2.1',
          openPorts: [80],
          identityConfidence: 'PORT_OPEN_ONLY',
          password: 'must-not-pass',
        },
      ],
      scannedTargets: 2,
      totalTargets: 2,
    })
    expect(parsed.hosts).toEqual([
      {
        candidateId: 'opaque-host',
        host: '192.0.2.1',
        openPorts: [80],
        identityConfidence: 'PORT_OPEN_ONLY',
      },
    ])
    const catalog = parseAccessJob({
      ...job,
      kind: 'CATALOG',
      candidates: [{ candidateId: 'zero', name: '目录相机', mappingRequired: false, profiles: [] }],
      page: { pageNumber: 2, pageSize: 100, total: 101, hasMore: false },
    })
    expect(catalog.candidates[0]!.profiles).toEqual([])
    expect(catalog.page).toEqual({ pageNumber: 2, pageSize: 100, total: 101, hasMore: false })
  })
  it('sends existing-camera binding at the request level, not inside candidate selections', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValue(
        response({ confirmation: 'proof', groupPath: null, cameraCount: 1, affectedUserCount: 0 }),
      )
    vi.stubGlobal('fetch', fetch)
    const command = {
      version: '2',
      targetCameraId: '3',
      targetCameraVersion: '4',
      selections: [{ candidateId: 'zero', profileIds: [] }],
    }
    await previewImport(job.jobId, command)
    expect(JSON.parse(fetch.mock.calls[0]![1].body)).toEqual(command)
  })
  it('accepts HTTP 202 for asynchronous job creation and preserves the explicit idempotency key', async () => {
    const fetch = vi.fn().mockResolvedValue(response(job, 202))
    vi.stubGlobal('fetch', fetch)
    const command = {
      clientRequestId: '1791244800000-key',
      connection: {
        method: 'ONVIF' as const,
        name: '',
        host: 'camera.test',
        port: 80,
        username: 'test',
        password: 'not-a-real-password',
      },
    }
    expect(await createAccessJob(command)).toEqual({ jobId: job.jobId })
    expect(fetch.mock.calls[0]![0]).toBe('/api/camera-access/jobs')
    expect(JSON.parse(fetch.mock.calls[0]![1].body)).toEqual(command)
    expect(fetch.mock.calls[0]![1].headers.get('X-CSRF-TOKEN')).toBe('test-csrf')
    expect(fetch).toHaveBeenCalledOnce()
  })
  it('still rejects HTTP 202 when a query explicitly requires HTTP 200', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response(job, 202)))
    await expect(getAccessJob(job.jobId)).rejects.toMatchObject({
      code: 'INVALID_RESPONSE',
      status: 202,
    })
  })
  it('preserves opaque candidates and unknown parameters without passing through source tokens or URIs', () => {
    const parsed = parseAccessJob({
      ...job,
      secret: 'never-display',
      candidates: [
        {
          ...job.candidates[0],
          token: 'upstream-token',
          uri: 'rtsp://secret',
          profiles: job.candidates[0]!.profiles,
        },
      ],
    })
    expect(parsed.candidates[0]!.candidateId).toBe('local-channel-id')
    expect(parsed.candidates[0]!.profiles[0]!.bitrateKbps).toBeNull()
    expect(parsed.device!.firmware).toBeNull()
    expect(parsed).not.toHaveProperty('secret')
    expect(parsed.candidates[0]).not.toHaveProperty('uri')
    expect(parsed.candidates[0]).not.toHaveProperty('token')
  })
  it('enforces the bounded channel and profile snapshot', () => {
    expect(() =>
      parseAccessJob({ ...job, candidates: Array(257).fill(job.candidates[0]) }),
    ).toThrow()
    expect(() =>
      parseAccessJob({
        ...job,
        candidates: [
          { ...job.candidates[0], profiles: Array(9).fill(job.candidates[0]!.profiles[0]) },
        ],
      }),
    ).toThrow()
  })
  it('accepts registry methods rather than fixing supported vendors in the client', () => {
    expect(
      parseAccessOptions({
        ready: false,
        methods: ['ONVIF'],
        adapters: [
          {
            type: 'ONVIF',
            label: 'ONVIF',
            category: 'DEVICE',
            inputKind: 'DEVICE_LOGIN',
            autoDetect: true,
            paged: false,
          },
        ],
        networkPolicies: [],
        diagnostics: ['not ready'],
      }).ready,
    ).toBe(false)
    expect(
      parseAccessOptions({
        ready: true,
        methods: ['NEW_DEVICE_DRIVER'],
        adapters: [
          {
            type: 'NEW_DEVICE_DRIVER',
            label: '新设备',
            category: 'DEVICE',
            inputKind: 'DEVICE_LOGIN',
            autoDetect: true,
            paged: false,
          },
        ],
        networkPolicies: [],
        diagnostics: [],
      }).methods,
    ).toEqual(['NEW_DEVICE_DRIVER'])
  })
  it('rejects missing descriptors instead of selecting vendors through a second registry', () => {
    expect(() =>
      parseAccessOptions({
        ready: true,
        methods: ['NEW_DRIVER'],
        networkPolicies: [],
        diagnostics: [],
      }),
    ).toThrow()
  })
  it('sends only catalog selection and signed group impact when importing, not the original credentials', async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(
        response({
          confirmation: 'signed-proof',
          groupPath: null,
          cameraCount: 1,
          affectedUserCount: 0,
        }),
      )
      .mockResolvedValueOnce(
        response({
          sourceId: '3',
          cameras: [
            { candidateId: 'local-channel-id', cameraId: '4', version: '0', status: 'EXISTING' },
          ],
          createdCount: 0,
          existingCount: 1,
        }),
      )
    vi.stubGlobal('fetch', fetch)
    const command = {
      version: '2',
      selections: [
        {
          candidateId: 'local-channel-id',
          profileIds: ['local-profile-id'],
          defaultProfileId: 'local-profile-id',
        },
      ],
    }
    const impact = await previewImport(job.jobId, command)
    const result = await importCandidates(job.jobId, {
      ...command,
      confirmation: impact.confirmation,
      clientRequestId: '1791244800000-import',
    })
    expect(result.cameras[0]!.cameraId).toBe('4')
    expect(JSON.parse(fetch.mock.calls[1]![1].body)).toEqual({
      ...command,
      confirmation: 'signed-proof',
      clientRequestId: '1791244800000-import',
    })
  })
  it.each([
    ['ONVIF', 'ONVIF', '/onvif/device_service'],
    ['HIKVISION', 'VENDOR_HTTP', ''],
    ['DAHUA', 'VENDOR_HTTP', ''],
    ['HIK_PLATFORM', 'PLATFORM_HTTP', '/artemis'],
  ])(
    'reads %s control endpoints without converting them to RTSP',
    (adapterType, purpose, basePath) => {
      const source = parseSource({
        sourceId: '1',
        name: '连接',
        adapterType,
        endpointEditable: true,
        networkPolicyKey: 'lab',
        enabled: true,
        version: '2',
        createdAt: '2026-10-06',
        updatedAt: '2026-10-06',
        credentials: [{ purpose, configured: true, usernameMasked: 'a***' }],
        endpoints: [
          {
            purpose,
            scheme: 'https',
            host: 'camera.test',
            port: 443,
            basePath,
            authMode: 'DRIVER_NEGOTIATED',
            credentialPurpose: purpose,
            tlsPolicy: 'SYSTEM_CA',
          },
        ],
      })
      expect(source.endpoints[0]).toMatchObject({
        purpose,
        credentialPurpose: purpose,
        scheme: 'https',
        basePath,
      })
    },
  )
})
