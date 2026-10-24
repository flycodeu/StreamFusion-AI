import { afterEach, describe, expect, it, vi } from 'vitest'
import { getStatusPage } from './api'
import { parseStatusPage } from './types'

const now = '2026-10-13T01:00:00Z'
const profile = {
  streamProfileId: '9007199254740993',
  name: null,
  streamKind: 'SUB',
  enabled: false,
  control: {
    layer: 'CONTROL',
    probeKind: 'RTSP_DESCRIBE',
    observation: null,
    freshness: 'NEVER',
    configurationCurrent: true,
    latestCheck: null,
    canCheck: false,
    blockingReasonCode: 'PROFILE_DISABLED',
    timeline: null,
  },
  media: {
    layer: 'MEDIA',
    probeKind: 'ZLM_RTP_FRAMES',
    observation: null,
    freshness: 'NEVER',
    configurationCurrent: true,
    latestCheck: null,
    canCheck: false,
    blockingReasonCode: 'PROFILE_DISABLED',
    timeline: null,
  },
}
const channel = {
  cameraId: '1',
  groupKey: 'c1',
  defaultPreviewProfileId: null,
  name: '通道一',
  deviceName: null,
  lifecycle: 'ENABLED',
  profiles: [profile],
}
const summary = {
  total: 1,
  accessible: 0,
  failed: 0,
  checking: 0,
  stale: 0,
  unknown: 1,
  disabled: 0,
}
const data = { items: [channel], page: 2, size: 20, total: 23, serverNow: now, summary }
afterEach(() => {
  vi.unstubAllGlobals()
  vi.unstubAllEnvs()
})
describe('camera status page response and read API', () => {
  it('retains nullable metadata, string IDs, backend eligibility and filtered total', () => {
    expect(parseStatusPage(data)).toEqual(data)
  })
  it('accepts a channel with no Profiles as a row, without fabricating observations', () => {
    expect(
      parseStatusPage({ ...data, items: [{ ...channel, profiles: [] }] }).items[0]?.profiles,
    ).toEqual([])
  })
  it.each([
    { serverNow: '2026-10-13 09:00:00' },
    { items: [channel, channel] },
    { items: [{ ...channel, lifecycle: 'ONLINE' }] },
    { items: [{ ...channel, profiles: [{ ...profile, enabled: null }] }] },
    {
      items: [
        { ...channel, profiles: [{ ...profile, control: { ...profile.control, layer: 'MEDIA' } }] },
      ],
    },
    { page: 0 },
    { summary: undefined },
    { summary: { ...summary, total: 23 } },
    { summary: { ...summary, unknown: -1 } },
    { summary: { ...summary, failed: 1 } },
    { items: [{ ...channel, groupKey: 'd0' }] },
    { items: [{ ...channel, defaultPreviewProfileId: 10 }] },
  ])('rejects invalid metadata or status-page contracts %j', (invalid) => {
    expect(() => parseStatusPage({ ...data, ...invalid })).toThrow()
  })
  it('reads only one paged aggregate GET with filter query and AbortSignal', async () => {
    vi.stubEnv('DEV', true)
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          code: 'SUCCESS',
          msg: '成功',
          data,
          traceId: 'a'.repeat(32),
          timestamp: now,
        }),
        {
          status: 200,
          headers: { 'Content-Type': 'application/json', 'X-Trace-Id': 'a'.repeat(32) },
        },
      ),
    )
    vi.stubGlobal('fetch', fetchMock)
    const controller = new AbortController()
    const response = await getStatusPage(
      { page: 2, size: 20, layer: 'MEDIA', name: '通道', observation: 'FAILED' },
      controller.signal,
    )
    const [path, options] = fetchMock.mock.calls[0]!
    const url = new URL(path, 'http://localhost')
    expect(url.pathname).toBe('/api/camera-status/devices')
    expect(Object.fromEntries(url.searchParams)).toEqual({
      layer: 'MEDIA',
      page: '2',
      size: '20',
      name: '通道',
      observation: 'FAILED',
    })
    expect(options.method).toBe('GET')
    expect(options.body).toBeUndefined()
    expect(options.signal).toBeInstanceOf(AbortSignal)
    expect(response.total).toBe(23)
    expect(fetchMock).toHaveBeenCalledOnce()
  })
})
