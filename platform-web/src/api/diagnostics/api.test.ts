import { afterEach, describe, expect, it, vi } from 'vitest'
import { getDiagnostics } from './api'

function reply(data: unknown) {
  return new Response(
    JSON.stringify({
      code: 'SUCCESS',
      msg: '成功',
      data,
      traceId: 'a'.repeat(32),
      timestamp: '2026-10-16T06:00:00Z',
    }),
    {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    },
  )
}

afterEach(() => vi.unstubAllGlobals())

describe('diagnostic cursor API', () => {
  const data = {
    items: [],
    truncated: false,
    unavailable: false,
    retentionDays: 7,
    limit: 50,
    nextCursor: 'next-cursor',
    cursorExpired: false,
    droppedEvents: 3,
  }

  it('sends backend page size and cursor and decodes completeness metadata', async () => {
    const fetch = vi.fn().mockResolvedValue(reply(data))
    vi.stubGlobal('fetch', fetch)
    await expect(getDiagnostics({ size: 50, cursor: 'current-cursor' })).resolves.toEqual(data)
    const url = new URL(fetch.mock.calls[0]![0], 'http://localhost')
    expect(url.pathname).toBe('/api/diagnostics/events')
    expect(url.searchParams.get('size')).toBe('50')
    expect(url.searchParams.get('cursor')).toBe('current-cursor')
  })

  it('rejects invalid completeness metadata instead of claiming a complete page', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(reply({ ...data, cursorExpired: 'false' })))
    await expect(getDiagnostics({ size: 50 })).rejects.toMatchObject({ code: 'INVALID_RESPONSE' })
  })
})
