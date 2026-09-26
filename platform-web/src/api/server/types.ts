import { boolean, integer, list, object, optionalString, string } from '../parse'

export interface ServerSnapshot {
  sampledAt: string | null
  stale: boolean
  state: string
  host: {
    os: string
    architecture: string
    processors: number
    cpuPercent: number | null
    memoryTotalBytes: number | null
    memoryUsedBytes: number | null
  } | null
  jvm: {
    javaVersion: string
    pid: string
    cpuPercent: number | null
    heapUsedBytes: number
    heapMaxBytes: number
    nonHeapUsedBytes: number
    uptimeMillis: number
    startedAt: string
  } | null
  services: {
    key: string
    name: string
    host: string | null
    port: number | null
    status: string
  }[]
  gpuStatus: string
  gpus: {
    index: number
    name: string
    utilizationPercent: number | null
    memoryTotalMiB: number | null
    memoryUsedMiB: number | null
  }[]
}

function metric(value: unknown): number | null {
  if (value == null) return null
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) throw new Error('metric')
  return value
}

function percentage(value: unknown): number | null {
  const result = metric(value)
  if (result != null && result > 100) throw new Error('percentage')
  return result
}

export function parseServerSnapshot(value: unknown): ServerSnapshot {
  const row = object(value)
  const host = row.host == null ? null : object(row.host)
  const jvm = row.jvm == null ? null : object(row.jvm)
  return {
    sampledAt: optionalString(row.sampledAt),
    stale: boolean(row.stale),
    state: string(row.state),
    host: host && {
      os: string(host.os),
      architecture: string(host.architecture),
      processors: integer(host.processors),
      cpuPercent: percentage(host.cpuPercent),
      memoryTotalBytes: metric(host.memoryTotalBytes),
      memoryUsedBytes: metric(host.memoryUsedBytes),
    },
    jvm: jvm && {
      javaVersion: string(jvm.javaVersion),
      pid: string(jvm.pid),
      cpuPercent: percentage(jvm.cpuPercent),
      heapUsedBytes: integer(jvm.heapUsedBytes),
      heapMaxBytes: integer(jvm.heapMaxBytes),
      nonHeapUsedBytes: integer(jvm.nonHeapUsedBytes),
      uptimeMillis: integer(jvm.uptimeMillis),
      startedAt: string(jvm.startedAt),
    },
    services: list(row.services, (value) => {
      const item = object(value)
      return {
        key: string(item.key),
        name: string(item.name),
        host: optionalString(item.host),
        port: item.port == null ? null : integer(item.port),
        status: string(item.status),
      }
    }),
    gpuStatus: string(row.gpuStatus),
    gpus: list(row.gpus, (value) => {
      const item = object(value)
      return {
        index: integer(item.index),
        name: string(item.name),
        utilizationPercent: percentage(item.utilizationPercent),
        memoryTotalMiB: metric(item.memoryTotalMiB),
        memoryUsedMiB: metric(item.memoryUsedMiB),
      }
    }),
  }
}
