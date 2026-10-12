import { boolean, integer, list, object, optionalString, string } from '../parse'

export interface RuntimeDiagnostic {
  code: string
  message: string
}
export interface MediaRuntimeCandidate {
  id: string
  kind: 'ZLM' | 'FFMPEG'
  executablePath: string
  configPath: string | null
  apiBase: string | null
  softwareVersion: string | null
  status: string
  processRunning: boolean
  pids: number[]
  ports: { protocol: string; port: number; reachable: boolean | null }[]
  checkedAt: string
  diagnostic: RuntimeDiagnostic | null
}
export interface RuntimeSelection {
  zlmId: string | null
  ffmpegId: string | null
}
export interface MediaRuntimeSnapshot {
  os: string
  version: string
  checkedAt: string
  selected: RuntimeSelection
  candidates: MediaRuntimeCandidate[]
  scanDiagnostics: RuntimeDiagnostic[]
}
function diagnostic(value: unknown): RuntimeDiagnostic {
  const row = object(value)
  return { code: string(row.code), message: string(row.message) }
}
export function parseRuntimeSnapshot(value: unknown): MediaRuntimeSnapshot {
  const row = object(value),
    selected = object(row.selected)
  return {
    os: string(row.os),
    version: string(row.version),
    checkedAt: string(row.checkedAt),
    selected: {
      zlmId: optionalString(selected.zlmId),
      ffmpegId: optionalString(selected.ffmpegId),
    },
    candidates: list(row.candidates, (value) => {
      const item = object(value),
        kind = string(item.kind)
      if (kind !== 'ZLM' && kind !== 'FFMPEG') throw new Error('runtime kind')
      return {
        id: string(item.id),
        kind,
        executablePath: string(item.executablePath),
        configPath: optionalString(item.configPath),
        apiBase: optionalString(item.apiBase),
        softwareVersion: optionalString(item.softwareVersion),
        status: string(item.status),
        processRunning: boolean(item.processRunning),
        pids: list(item.pids, integer),
        ports: list(item.ports, (value) => {
          const port = object(value)
          return {
            protocol: string(port.protocol),
            port: integer(port.port),
            reachable: port.reachable == null ? null : boolean(port.reachable),
          }
        }),
        checkedAt: string(item.checkedAt),
        diagnostic: item.diagnostic == null ? null : diagnostic(item.diagnostic),
      }
    }),
    scanDiagnostics: list(row.scanDiagnostics, diagnostic),
  }
}
