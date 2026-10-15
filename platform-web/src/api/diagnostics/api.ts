import { request } from '../client'
import { boolean, integer, list, object, optionalString, string } from '../parse'

export interface DiagnosticQuery {
  startTime?: string
  endTime?: string
  traceId?: string
  planId?: string
  checkId?: string
  kind?: string
  level?: string
}
export interface DiagnosticEvent {
  occurredAt: string
  kind: string
  level: string
  traceId: string | null
  method: string | null
  route: string | null
  status: number | null
  durationMs: number | null
  errorCode: string | null
  reasonCode: string | null
  planId: string | null
  checkId: string | null
  cameraId: string | null
  profileId: string | null
  state: string | null
  result: string | null
  exceptionType: string | null
  stack: string[]
  request: unknown | null
  response: unknown | null
  rootExceptionType: string | null
}
export function parseEvent(value: unknown): DiagnosticEvent {
  const row = object(value)
  return {
    occurredAt: string(row.occurredAt),
    kind: string(row.kind),
    level: string(row.level),
    traceId: optionalString(row.traceId),
    method: optionalString(row.method),
    route: optionalString(row.route),
    status: row.status == null ? null : integer(row.status),
    durationMs: row.durationMs == null ? null : integer(row.durationMs),
    errorCode: optionalString(row.errorCode),
    reasonCode: optionalString(row.reasonCode),
    planId: optionalString(row.planId),
    checkId: optionalString(row.checkId),
    cameraId: optionalString(row.cameraId),
    profileId: optionalString(row.profileId),
    state: optionalString(row.state),
    result: optionalString(row.result),
    exceptionType: optionalString(row.exceptionType),
    stack: list(row.stack, string),
    request: row.request ?? null,
    response: row.response ?? null,
    rootExceptionType: optionalString(row.rootExceptionType),
  }
}

export interface CaptureWindow {
  enabled: boolean
  ownerId: string | null
  expiresAt: string | null
  matched: number
  recorded: number
  limit: number
  routes: string[]
  actorId: string | null
  errorsOnly: boolean
}
function parseCapture(value: unknown): CaptureWindow {
  const r = object(value)
  return {
    enabled: boolean(r.enabled),
    ownerId: optionalString(r.ownerId),
    expiresAt: optionalString(r.expiresAt),
    matched: integer(r.matched),
    recorded: integer(r.recorded),
    limit: integer(r.limit),
    routes: list(r.routes, string),
    actorId: optionalString(r.actorId),
    errorsOnly: boolean(r.errorsOnly),
  }
}
export const getCapture = async () =>
  (
    await request({
      path: '/diagnostics/capture',
      method: 'GET',
      successStatus: 200,
      decode: parseCapture,
    })
  ).data
export const captureRoutes = async () =>
  (
    await request({
      path: '/diagnostics/capture/routes',
      method: 'GET',
      successStatus: 200,
      decode: (value) =>
        list(value, (item) => {
          const r = object(item)
          return { key: string(r.key), label: string(r.label) }
        }),
    })
  ).data
export const startCapture = async (body: {
  routes: string[]
  actorId: string | null
  minutes: number
  errorsOnly: boolean
}) =>
  (
    await request({
      path: '/diagnostics/capture',
      method: 'POST',
      body,
      successStatus: 200,
      decode: parseCapture,
    })
  ).data
export const stopCapture = async () =>
  (
    await request({
      path: '/diagnostics/capture',
      method: 'DELETE',
      successStatus: 200,
      decode: parseCapture,
    })
  ).data
export async function getDiagnostics(query: DiagnosticQuery) {
  return (
    await request({
      path: '/diagnostics/events',
      method: 'GET',
      params: { ...query },
      successStatus: 200,
      decode: (value) => {
        const row = object(value)
        return {
          items: list(row.items, parseEvent),
          truncated: boolean(row.truncated),
          unavailable: boolean(row.unavailable),
          retentionDays: integer(row.retentionDays),
          limit: integer(row.limit),
        }
      },
    })
  ).data
}
