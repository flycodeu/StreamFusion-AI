import type { CameraDeviceGroup } from '../../api/camera/types'

export function deviceStatus(device: CameraDeviceGroup) {
  const summary = device.statusSummary
  const state = summary?.state ?? 'NEVER'
  const labels = {
    ACCESSIBLE: '在线',
    FAILED: '连接异常',
    PARTIAL: '部分未确认 / 异常',
    CHECKING: '检测中',
    STALE: '配置已变更',
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
