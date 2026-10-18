import { describe, expect, it, vi } from 'vitest'
import type { Camera } from '../../api/camera/types'
import {
  captureView,
  readSavedViews,
  resolveSavedView,
  savedViewsKey,
  viewName,
  writeSavedViews,
  type SavedView,
} from './saved-views'

vi.mock('../../api/camera/api', () => ({ getCamera: vi.fn() }))
const view = (extra: Partial<SavedView> = {}): SavedView => ({
  id: 'view-1',
  name: '日常巡看',
  layout: '36',
  updatedAt: '2026-10-18T01:00:00Z',
  slots: [
    { index: 0, cameraId: '1', profileId: '11' },
    { index: 35, cameraId: '2', profileId: '22' },
  ],
  ...extra,
})
function storage() {
  const values = new Map<string, string>()
  return {
    values,
    getItem: vi.fn((key: string) => values.get(key) ?? null),
    setItem: vi.fn((key: string, value: string) => values.set(key, value)),
  } as unknown as Storage & { values: Map<string, string> }
}
const camera = (cameraId: string): Camera =>
  ({
    cameraId,
    name: `相机${cameraId}`,
    deviceId: '5',
    lifecycle: 'ENABLED',
    groupId: null,
    groupPath: null,
    profiles: [
      {
        streamProfileId: `${cameraId}1`,
        label: '主码流',
        usageHint: 'MAIN',
        enabled: true,
        locatorSummary: { configured: true, host: 'private-host', port: 554 },
        videoCodec: 'H264',
        width: 1920,
        height: 1080,
        frameRate: 25,
        bitrateKbps: 1024,
      },
    ],
    accessAddress: { kind: 'RTSP', host: 'private-host', port: 554 },
  }) as Camera

describe('bounded account-local preview views', () => {
  it('keeps 6×6 positions, empty slots and exact profile IDs without storing playback state', () => {
    const slots = Array.from({ length: 36 }, () => ({
      channel: null,
      profile: null,
    })) as Parameters<typeof captureView>[1][number][]
    slots[0] = { channel: { cameraId: '1' }, profile: { streamProfileId: '11' } }
    slots[35] = { channel: { cameraId: '2' }, profile: { streamProfileId: '22' } }
    expect(captureView('36', slots)).toEqual({ layout: '36', slots: view().slots })
    const local = storage()
    const unsafe = {
      ...view(),
      lease: 'private-lease',
      rtsp: 'rtsp://user:pass@camera',
      muted: false,
      slots: [{ ...view().slots[0]!, sdp: 'private-sdp', node: 'private-node' }],
    }
    writeSavedViews('1', [unsafe], local)
    const encoded = local.values.get(savedViewsKey('1'))!
    expect(JSON.parse(encoded)).toEqual({
      schemaVersion: 1,
      views: [view({ slots: [view().slots[0]!] })],
    })
    expect(encoded).not.toMatch(/private|rtsp|muted|lease|sdp|node/)
  })
  it('isolates users and never persists while only reading the list', () => {
    const local = storage()
    writeSavedViews('1', [view()], local)
    expect(readSavedViews('2', local)).toEqual([])
    expect(readSavedViews('1', local)).toEqual([view()])
    expect(local.setItem).toHaveBeenCalledTimes(1)
    expect(() => savedViewsKey('anonymous')).toThrow('请登录')
  })
  it.each([
    { schemaVersion: 2, views: [] },
    {
      schemaVersion: 1,
      views: Array.from({ length: 21 }, (_, index) => view({ id: `v-${index}` })),
    },
    { schemaVersion: 1, views: [view(), view()] },
    { schemaVersion: 1, views: [view({ layout: '37' as '36' })] },
    { schemaVersion: 1, views: [view({ updatedAt: 'not-a-date' })] },
    { schemaVersion: 1, views: [view({ slots: [{ index: 36, cameraId: '1', profileId: '11' }] })] },
    { schemaVersion: 1, views: [view({ slots: [view().slots[0]!, view().slots[0]!] })] },
    { schemaVersion: 1, views: [view({ slots: [{ index: -1, cameraId: '1', profileId: '11' }] })] },
    { schemaVersion: 1, views: [view({ slots: [{ index: 1, cameraId: '01', profileId: '11' }] })] },
    {
      schemaVersion: 1,
      views: [view({ slots: [{ index: 1, cameraId: '1', profileId: 'url://private' }] })],
    },
  ])('rejects bad schema/limits/positions/IDs without replacing local data', (document) => {
    const local = storage()
    local.values.set(savedViewsKey('1'), JSON.stringify(document))
    expect(() => readSavedViews('1', local)).toThrow()
    expect(local.setItem).not.toHaveBeenCalled()
  })
  it('surfaces malformed, oversized and unavailable storage without a silent overwrite', () => {
    const local = storage(),
      key = savedViewsKey('1')
    local.values.set(key, '{')
    expect(() => readSavedViews('1', local)).toThrow()
    local.values.set(key, ' '.repeat(131073))
    expect(() => readSavedViews('1', local)).toThrow('过大')
    vi.mocked(local.setItem).mockImplementationOnce(() => {
      throw new Error('quota')
    })
    expect(() => writeSavedViews('1', [view()], local)).toThrow('quota')
    expect(local.values.get(key)).toHaveLength(131073)
    vi.mocked(local.getItem).mockImplementationOnce(() => {
      throw new Error('denied')
    })
    expect(() => readSavedViews('1', local)).toThrow('denied')
  })
  it('counts names by code points, trims them and rejects control characters', () => {
    expect(viewName('  厂区  ')).toBe('厂区')
    expect(viewName('📹'.repeat(40))).toHaveLength(80)
    for (const name of ['', ' ', 'x'.repeat(41), '厂区\u0000', '厂\n区'])
      expect(() => viewName(name)).toThrow()
  })
})

describe('explicit saved-view resolution', () => {
  it('looks up current authorization by unique camera with at most four requests and leaves individual failures in position', async () => {
    let active = 0,
      peak = 0
    const lookup = vi.fn(async (id: string) => {
      active++
      peak = Math.max(peak, active)
      await new Promise((resolve) => setTimeout(resolve, 2))
      active--
      if (id === '3') throw new Error('FORBIDDEN')
      return camera(id)
    })
    const slots = Array.from({ length: 8 }, (_, index) => ({
      index,
      cameraId: String(index + 1),
      profileId: `${index + 1}1`,
    }))
    slots.push({ index: 35, cameraId: '1', profileId: '12' })
    const result = await resolveSavedView(view({ slots }), new AbortController().signal, lookup)
    expect(lookup).toHaveBeenCalledTimes(8)
    expect(peak).toBe(4)
    expect(result[2]).toMatchObject({ index: 2, cameraId: '3', channel: null })
    expect(result[2]!.failure).toBeInstanceOf(Error)
    expect(result[8]).toMatchObject({ index: 35, cameraId: '1', profileId: '12' })
    expect(result[8]!.channel?.profiles.map((profile) => profile.streamProfileId)).toEqual(['11'])
    expect(JSON.stringify(result)).not.toContain('private-host')
  })
  it('does not start a request after cancellation or return late account data', async () => {
    const pending: (() => void)[] = []
    const lookup = vi.fn(
      (id: string) => new Promise<Camera>((resolve) => pending.push(() => resolve(camera(id)))),
    )
    const controller = new AbortController()
    const resolving = resolveSavedView(
      view({
        slots: Array.from({ length: 9 }, (_, index) => ({
          index,
          cameraId: String(index + 1),
          profileId: '1',
        })),
      }),
      controller.signal,
      lookup,
    )
    expect(lookup).toHaveBeenCalledTimes(4)
    controller.abort()
    pending.forEach((finish) => finish())
    expect(await resolving).toEqual([])
    expect(lookup).toHaveBeenCalledTimes(4)
    expect(await resolveSavedView(view(), controller.signal, lookup)).toEqual([])
    expect(lookup).toHaveBeenCalledTimes(4)
  })
})
