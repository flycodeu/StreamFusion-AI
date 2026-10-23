import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import {
  draftCheck,
  objectContentUrl,
  listObjects,
  scanUsage,
  getHistoryPolicy,
  saveHistoryPolicy,
} from './api'
import { parseObject, parsePage } from './types'
import { bytes, time } from '../../features/storage/display'
import { clearIdentity, sessionState } from '../../session/state'

const file = {
  objectKey: '证据/中文+%25.jpg',
  name: '中文+%25.jpg',
  sizeBytes: '9007199254740993',
  etag: '"etag"',
  lastModified: '2026-10-22T01:00:00Z',
  contentType: null,
  objectVersionId: null,
}
function response(data: unknown, status = 200) {
  return new Response(
    JSON.stringify({
      code: 'SUCCESS',
      msg: '成功',
      data,
      traceId: 'a'.repeat(32),
      timestamp: '2026-10-22T01:00:00Z',
    }),
    { status, headers: { 'Content-Type': 'application/json' } },
  )
}
beforeEach(() => {
  clearIdentity()
  sessionState.csrf = { headerName: 'X-CSRF-TOKEN', token: 'test' }
  vi.stubEnv('DEV', true)
})
afterEach(() => {
  clearIdentity()
  vi.unstubAllGlobals()
  vi.unstubAllEnvs()
})
it('loads and saves explicit metadata retention without enabling a default', async () => {
  const policy = {
    version: '0',
    inheritGlobal: false,
    enabled: false,
    retentionDays: null,
    effective: { enabled: false, retentionDays: null, source: 'GLOBAL' },
  }
  const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(response(policy)))
  vi.stubGlobal('fetch', fetchMock)
  expect((await getHistoryPolicy()).enabled).toBe(false)
  expect(fetchMock.mock.calls[0]![0]).toBe('/api/storage/history-policy')
  await saveHistoryPolicy(
    { version: '0', inheritGlobal: false, enabled: true, retentionDays: 90 },
    '10',
  )
  expect(fetchMock.mock.calls[1]![0]).toBe('/api/storage/instances/10/history-policy')
  expect(fetchMock.mock.calls[1]![1].method).toBe('PUT')
  expect(JSON.parse(fetchMock.mock.calls[1]![1].body)).toEqual({
    version: '0',
    inheritGlobal: false,
    enabled: true,
    retentionDays: 90,
  })
})
it('keeps keys and large byte counters exact and unknown capacity distinct from zero', () => {
  expect(parseObject(file).sizeBytes).toBe('9007199254740993')
  expect(parseObject(file).objectKey).toBe(file.objectKey)
  expect(bytes(undefined)).toBe('暂无统计')
  expect(bytes('0')).toBe('0 B')
  expect(time(undefined)).toBe('尚无结果')
  expect(() => parseObject({ ...file, sizeBytes: Number('9007199254740993') })).toThrow()
  expect(() =>
    parsePage({ items: [], page: 1, size: 20, total: '9999999999999999999' }, parseObject),
  ).toThrow()
})
it('encodes a protected content URL once and uses only the current origin', () => {
  const path = objectContentUrl('10', file.objectKey, 'signed+token/%')
  const url = new URL(path, 'http://localhost')
  expect(url.pathname).toBe('/api/storage/scopes/10/object-content')
  expect(url.searchParams.get('key')).toBe(file.objectKey)
  expect(url.searchParams.get('objectToken')).toBe('signed+token/%')
})
it('preserves signed opaque list cursors without reusing a content token', async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValue(
      response({ items: [file], commonPrefixes: [], hasMore: false, nextCursor: null }),
    )
  vi.stubGlobal('fetch', fetchMock)
  expect((await listObjects('10', '中文/', 'opaque+/=')).items[0]?.objectKey).toBe(file.objectKey)
  const url = new URL(fetchMock.mock.calls[0]![0], 'http://localhost')
  expect(url.searchParams.get('cursor')).toBe('opaque+/=')
  expect(url.searchParams.get('prefix')).toBe('中文/')
})
it('draft checks send credentials only in the body and do not auto save configuration', async () => {
  const fetchMock = vi.fn().mockResolvedValue(response({ status: 'ACCESSIBLE' }))
  vi.stubGlobal('fetch', fetchMock)
  await draftCheck(
    {
      name: '草稿',
      endpoint: 'http://10.0.1.5:8333',
      region: 'us-east-1',
      networkPolicyKey: 'test',
      metricsEndpoint: '',
      managementUrl: 'http://127.0.0.1:23646',
      remark: '',
      accessKey: { action: 'REPLACE', value: 'test-access' },
      secretKey: { action: 'REPLACE', value: 'private-secret' },
      idempotencyKey: crypto.randomUUID(),
      version: '0',
    },
    '10',
  )
  expect(fetchMock).toHaveBeenCalledOnce()
  const [path, init] = fetchMock.mock.calls[0]!
  expect(path).toBe('/api/storage/connection-tests?instanceId=10')
  expect(path).not.toContain('private-secret')
  expect(init.method).toBe('POST')
})
it('does not replay an ambiguous task submission after a network failure', async () => {
  const fetchMock = vi.fn().mockRejectedValue(new TypeError('network failed'))
  vi.stubGlobal('fetch', fetchMock)
  await expect(scanUsage('10', '0')).rejects.toMatchObject({ code: 'NETWORK_ERROR' })
  expect(fetchMock).toHaveBeenCalledOnce()
})
