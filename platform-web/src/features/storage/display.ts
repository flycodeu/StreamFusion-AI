import { formatDateTime } from '../../utils/dateTime'
import { formatBytes } from '../../utils/formatBytes'
import { labelOf } from '../../utils/display'

export const statusLabels: Record<string, string> = {
  UNCHECKED: '未检查',
  ACCESSIBLE: '访问正常',
  AUTH_FAILED: '认证失败',
  UNREACHABLE: '无法访问',
  DEGRADED: '部分异常',
  FRESH: '最近有效',
  STALE: '待更新',
  CONFIG_CHANGED: '配置已变化',
  UNAVAILABLE: '暂无观测',
  UNKNOWN: '未确认',
  QUEUED: '排队中',
  RUNNING: '执行中',
  IN_PROGRESS: '处理中',
  SUCCEEDED: '已完成',
  PARTIAL: '部分完成',
  FAILED: '失败',
  CANCELLED: '已取消',
  PAUSED_LIMIT: '达到上限，已暂停',
  CHECK: '连接检查',
  USAGE_SCAN: '文件统计',
  CLEANUP_PREVIEW: '清理预览',
  FILE_UPLOAD: '上传文件',
  FILE_DELETE: '删除文件',
  FOLDER_CREATE: '新建文件夹',
  FOLDER_PREVIEW: '文件夹删除统计',
  FOLDER_DELETE: '删除文件夹',
  READY: '候选已生成',
  SCANNING: '扫描中',
  INCOMPLETE: '扫描未完成',
  EXPIRED: '已过期',
}
export function label(value: string) {
  return labelOf(statusLabels, value)
}
export function bytes(value: unknown): string {
  if (value == null || value === '') return '暂无统计'
  return formatBytes(
    typeof value === 'number' || typeof value === 'bigint' || typeof value === 'string'
      ? value
      : null,
    {
      precision: Number(value) < 1024 ? 0 : 2,
      empty: '不可获取',
    },
  )
}
export function time(value: unknown): string {
  if (typeof value !== 'string' || !value) return '尚无结果'
  return formatDateTime(value)
}

const checkStages = {
  AUTH_LIST: '认证与目录访问',
  HEAD: '文件元数据读取',
  AUTH_LIST_HEAD: '认证与文件访问',
}
const checkResults = { PASSED: '通过', FAILED: '失败', NOT_TESTED: '未检测' }
export const checkStageLabel = (value: string) => labelOf(checkStages, value)
export const checkResultLabel = (value: string) => labelOf(checkResults, value)
