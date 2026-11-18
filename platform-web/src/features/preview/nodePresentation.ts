import type { DirectZlmInput, PreviewNodeStatus } from '../../api/preview-node/api'

export const previewMaintenanceLabels = {
  OPEN: '未封闭',
  SEALED: '已签发，等待 Ops 领取',
  CLAIMED: 'Ops 执行中',
  COMPLETE: 'Ops 已完成，等待解除',
} as const
export const previewNodeLabels = {
  LOCAL: '不接新预览',
  REMOTE: '参与调度',
  DRAINING: '排空中',
  READY: '就绪',
  MAINTENANCE: '维护封闭中',
  RECONCILING: '资源待核对',
  UNKNOWN: '观测已过期或未检查',
  DEGRADED: '媒体环境异常',
  ZLM_NOT_READY: '本机媒体未配置',
  NODE_HOST_OVERLOADED: '节点持续高负载，暂停新预览',
  NODE_BITRATE_EXCEEDED: '实测码率超出预算，暂停新预览',
} as const

export const defaultDirectBudget: DirectZlmInput['budget'] = {
  maxSources: 64,
  maxViewers: 64,
  maxTranscodes: 0,
  maxStarting: 4,
  ingressKbps: 655360,
  egressKbps: 655360,
  sourceUpperKbps: 8192,
  outputUpperKbps: 8192,
}

export const nodeBudgetFields: {
  key: keyof PreviewNodeStatus['budget']
  label: string
  min: number
  max: number
}[] = [
  { key: 'maxSources', label: '源流上限', min: 1, max: 64 },
  { key: 'maxViewers', label: '观看上限', min: 1, max: 64 },
  { key: 'maxTranscodes', label: '共享转码上限', min: 0, max: 64 },
  { key: 'maxStarting', label: '同时启动上限', min: 1, max: 4 },
  { key: 'ingressKbps', label: '入站预算（kbps）', min: 1, max: 100000000 },
  { key: 'egressKbps', label: '出口预算（kbps）', min: 1, max: 100000000 },
  { key: 'sourceUpperKbps', label: '每源码率上界（kbps）', min: 1, max: 1000000 },
  { key: 'outputUpperKbps', label: '每观看码率上界（kbps）', min: 1, max: 1000000 },
]

export const directBudgetFields = nodeBudgetFields.filter((field) => field.key !== 'maxTranscodes')

export const nodeUsageMetrics = [
  { usage: 'sources', budget: 'maxSources', label: '源流' },
  { usage: 'viewers', budget: 'maxViewers', label: '观看' },
  { usage: 'transcodes', budget: 'maxTranscodes', label: '转码' },
  { usage: 'starting', budget: 'maxStarting', label: '启动中' },
] as const
