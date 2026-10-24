export const exportStates: Record<string, string> = {
  QUEUED: '等待导出',
  RUNNING: '正在导出',
  COMPLETED: '已完成',
  FAILED: '失败',
  CANCELLED: '已取消',
  EXPIRED: '文件已过期',
}
export const exportReasons: Record<string, string> = {
  WORKER_LOST: '服务中断，请重新导出',
  EXPORT_TIMEOUT: '导出超时，请缩小范围',
  EXPORT_ROW_LIMIT: '超过10万条，请缩小范围',
  EXPORT_ROW_TOO_LARGE: '单条日志过大，请缩小范围或联系管理员',
  EXPORT_PERMISSION_REVOKED: '导出权限已失效',
  EXPORT_IO_ERROR: '文件生成失败，请检查存储空间',
  WORKER_STOPPED: '服务已停止',
  EXPORT_CANCELLED: '任务已取消',
}
export const retentionResults: Record<string, string> = {
  RUNNING: '正在清理',
  COMPLETED: '已完成',
  PAUSED: '已暂停',
  PARTIAL: '本轮完成，等待续清',
  FAILED: '清理失败',
  STOPPED: '已停止',
  INTERRUPTED: '已中断',
}

export const retentionModeLabels = { AGE: '按时间', COUNT: '按数量' } as const
