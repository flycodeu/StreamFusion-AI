import { describe, expect, it } from 'vitest'
import { parseServerSnapshot } from './types'

const unavailable = {
  sampledAt: null,
  stale: true,
  state: 'UNAVAILABLE',
  host: null,
  jvm: null,
  services: [],
  gpuStatus: 'UNAVAILABLE',
  gpus: [],
}

describe('server information response parsing', () => {
  it.each(['REACHABLE', 'UNREACHABLE', 'UNAVAILABLE'])(
    'preserves the configured Platform Web endpoint and its %s probe state',
    (status) => {
      const service = {
        key: 'web',
        name: 'Platform Web',
        host: 'web.internal',
        port: 8443,
        status,
      }
      expect(parseServerSnapshot({ ...unavailable, services: [service] }).services).toEqual([
        service,
      ])
    },
  )

  it('preserves a disabled Platform Web probe without inventing a running endpoint', () => {
    const service = {
      key: 'web',
      name: 'Platform Web',
      host: null,
      port: null,
      status: 'NOT_CONFIGURED',
    }
    expect(parseServerSnapshot({ ...unavailable, services: [service] }).services).toEqual([service])
  })

  it('represents unavailable collection without fabricated zero metrics', () => {
    expect(parseServerSnapshot(unavailable)).toEqual(unavailable)
  })
  it('preserves partial metrics and explicit project endpoint states', () => {
    const snapshot = {
      ...unavailable,
      sampledAt: '2026-09-26T00:00:00Z',
      stale: false,
      state: 'READY',
      host: {
        os: 'Windows',
        architecture: 'amd64',
        processors: 8,
        cpuPercent: null,
        memoryTotalBytes: 1024,
        memoryUsedBytes: 512,
      },
      services: [
        { key: 'agent', name: 'Node Agent', host: '127.0.0.1', port: 8100, status: 'UNREACHABLE' },
      ],
      gpuStatus: 'AVAILABLE',
      gpus: [
        {
          index: 0,
          name: 'GPU',
          utilizationPercent: null,
          memoryTotalMiB: 8192,
          memoryUsedMiB: null,
        },
      ],
    }
    expect(parseServerSnapshot(snapshot)).toEqual(snapshot)
  })
  it('rejects nonfinite metrics instead of rendering misleading resource values', () => {
    expect(() =>
      parseServerSnapshot({
        ...unavailable,
        host: {
          os: 'Test',
          architecture: 'test',
          processors: 1,
          cpuPercent: Infinity,
          memoryTotalBytes: null,
          memoryUsedBytes: null,
        },
      }),
    ).toThrow()
  })

  it.each([null, 0, 37.5, 100])('accepts a host percentage of %s', (cpuPercent) => {
    const result = parseServerSnapshot({
      ...unavailable,
      host: {
        os: 'Test',
        architecture: 'test',
        processors: 8,
        cpuPercent,
        memoryTotalBytes: 2048,
        memoryUsedBytes: 1024,
      },
    })
    expect(result.host?.cpuPercent).toBe(cpuPercent)
    expect(result.host?.memoryTotalBytes).toBe(2048)
  })

  it.each([-1, 100.1, Infinity, NaN])('rejects an invalid CPU percentage of %s', (cpuPercent) => {
    expect(() =>
      parseServerSnapshot({
        ...unavailable,
        host: {
          os: 'Test',
          architecture: 'test',
          processors: 8,
          cpuPercent,
          memoryTotalBytes: null,
          memoryUsedBytes: null,
        },
      }),
    ).toThrow()
  })

  it('applies the same percentage range to JVM and GPU metrics', () => {
    expect(() =>
      parseServerSnapshot({
        ...unavailable,
        jvm: { javaVersion: '21', pid: '1', cpuPercent: 101 },
      }),
    ).toThrow('percentage')
    expect(() =>
      parseServerSnapshot({
        ...unavailable,
        gpus: [{ index: 0, name: 'Test GPU', utilizationPercent: 101 }],
      }),
    ).toThrow('percentage')
  })
})
