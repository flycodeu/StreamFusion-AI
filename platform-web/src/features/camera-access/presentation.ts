import type { AccessAdapter, AccessJob, JobStatus } from '../../api/camera-access/types'
import { labelOf } from '../../utils/display'
import { hikPlatformConnection } from './platformConnection'

/** Compatibility names for old/offline data; registered adapters own their current labels. */
export const methodLabels: Readonly<Record<string, string>> = {
  AUTO: '设备自动识别',
  ONVIF: 'ONVIF 设备',
  HIKVISION: '海康设备',
  DAHUA: '大华设备',
  HIK_PLATFORM: hikPlatformConnection.defaultName,
  RTSP: '已知 RTSP 地址',
}
export function accessMethodLabel(method: string, adapters: readonly AccessAdapter[] = []): string {
  return adapters.find((adapter) => adapter.type === method)?.label || labelOf(methodLabels, method)
}
export function connectionLabel(
  type: string | null | undefined,
  category?: string | null,
  adapters: readonly AccessAdapter[] = [],
): string {
  return type
    ? accessMethodLabel(type, adapters)
    : category === 'PLATFORM'
      ? '平台连接'
      : '设备连接（未指定方式）'
}
const catalogStates = {
  QUEUED: '等待读取',
  RUNNING: '正在读取设备',
  SUCCEEDED: '读取完成',
  PARTIAL: '读取到部分目录',
  FAILED: '读取失败',
  CANCELLED: '已取消',
  EXPIRED: '读取结果已过期',
} as const satisfies Record<JobStatus, string>
const importStates = {
  QUEUED: '等待导入',
  RUNNING: '正在导入',
  SUCCEEDED: '导入完成',
  PARTIAL: '部分完成',
  FAILED: '导入中断',
  CANCELLED: '已取消',
  EXPIRED: '已到期',
} as const satisfies Record<JobStatus, string>
const scanStates = {
  QUEUED: '等待搜索',
  RUNNING: '正在搜索',
  SUCCEEDED: '搜索完成',
  PARTIAL: '部分完成',
  FAILED: '搜索失败',
  CANCELLED: '已取消',
  EXPIRED: '搜索结果已过期',
} as const satisfies Record<JobStatus, string>
const jobStates = {
  CATALOG: catalogStates,
  BULK_IMPORT: importStates,
  SCAN: scanStates,
} satisfies Record<AccessJob['kind'], Record<JobStatus, string>>
export function accessJobLabel(job: Pick<AccessJob, 'kind' | 'status'>): string {
  return Object.hasOwn(jobStates, job.kind) ? labelOf(jobStates[job.kind], job.status) : job.status
}
