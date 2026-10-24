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
