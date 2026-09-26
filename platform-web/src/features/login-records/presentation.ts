export { formatDateTime as loginTime } from '../../utils/dateTime'

export function sessionDuration(seconds: number): string {
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const rest = seconds % 60
  if (hours) return `${hours}小时 ${minutes}分`
  if (minutes) return `${minutes}分 ${rest}秒`
  return `${rest}秒`
}

const reasons: Record<string, string> = {
  LOGOUT: '主动退出',
  IDLE_TIMEOUT: '空闲超时',
  ABSOLUTE_TIMEOUT: '会话到期',
  PASSWORD_CHANGED: '密码已修改',
  PASSWORD_RESET: '密码已重置',
  SESSION_REVOKED: '会话已失效',
  SESSION_INVALIDATED: '会话已失效',
  ACCOUNT_DISABLED: '账号已停用',
  ACCOUNT_DELETED: '账号已删除',
  LOGIN_ABORTED: '登录未完成',
  REPLACED: '重新登录',
}

export function loginEndReason(reason: string | null): string {
  return reason ? reasons[reason] || reason : '—'
}
