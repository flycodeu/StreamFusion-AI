import type { CameraDeviceGroup } from '../../api/camera/types'

export function deviceStatus(device: CameraDeviceGroup, now = Date.now()) {
  const summary = device.statusSummary
  let state = summary?.state ?? 'NEVER'
  if (summary?.validUntil && Date.parse(summary.validUntil) <= now && state !== 'CHECKING')
    state = 'STALE'
  const labels = {
    ACCESSIBLE: '可达',
    FAILED: '连接异常',
    PARTIAL: '部分未确认 / 异常',
    CHECKING: '检测中',
    STALE: '结果已过期',
    NEVER: '未检测',
  }
  return {
    label: labels[state],
    tone:
      state === 'ACCESSIBLE'
        ? ('success' as const)
        : state === 'FAILED' || state === 'PARTIAL'
          ? ('warning' as const)
          : ('info' as const),
  }
}
