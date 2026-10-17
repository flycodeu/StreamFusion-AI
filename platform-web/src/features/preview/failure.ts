import { ApiRequestError, isTraceId, isUtcTimestamp } from '../../lib/http/error'
import { previewReason } from './model'

export interface PreviewFailure {
  code: string
  message: string
  traceId: string | null
  timestamp: string
  cameraId: string | null
  cameraName: string | null
  streamProfileId: string | null
  profileName: string | null
  leaseId: string | null
  requestCode: string | null
}

export interface PreviewFailureContext {
  code?: string
  cameraId?: string | null
  cameraName?: string | null
  streamProfileId?: string | null
  profileName?: string | null
  leaseId?: string | null
}

function diagnosticCode(value: unknown): string | null {
  return typeof value === 'string' && /^[A-Z][A-Z0-9_]{0,79}$/.test(value) ? value : null
}

function requestError(cause: unknown): ApiRequestError | null {
  // Cleanup can wrap the failed request without exposing its body or raw error text.
  for (let depth = 0; depth < 4 && cause instanceof Error; depth++) {
    if (cause instanceof ApiRequestError) return cause
    cause = cause.cause
  }
  return null
}

/** Only explicit diagnostic fields leave the player; request data and secrets stay out. */
export function previewFailure(
  cause: unknown,
  context: PreviewFailureContext = {},
): PreviewFailure {
  const request = requestError(cause)
  const candidate =
    context.code ??
    (cause instanceof ApiRequestError ? cause.code : cause instanceof Error ? cause.message : cause)
  const requestCode = diagnosticCode(request?.code)
  const code = diagnosticCode(candidate) ?? requestCode ?? 'PREVIEW_FAILED'
  const occurred = isUtcTimestamp(request?.timestamp) ? request.timestamp : request?.receivedAt
  return {
    code,
    message:
      !context.code && request && requestCode === code && code !== 'RATE_LIMITED'
        ? request.message
        : previewReason(code),
    traceId: isTraceId(request?.traceId) ? request.traceId : null,
    timestamp: isUtcTimestamp(occurred) ? occurred : new Date().toISOString(),
    cameraId: context.cameraId ?? null,
    cameraName: context.cameraName ?? null,
    streamProfileId: context.streamProfileId ?? null,
    profileName: context.profileName ?? null,
    leaseId: context.leaseId ?? null,
    requestCode: requestCode && requestCode !== code ? requestCode : null,
  }
}
