import type { SecurityCategory, SecurityRule } from '../../api/security/alerts'

export const securityRules = {
  LOGIN_FAILURE_BURST: '登录失败集中出现',
  ACCESS_DENIAL_BURST: '频繁触发权限拒绝',
  PATH_SCAN: '疑似路径扫描',
  CSRF_FAILURE_BURST: '请求凭证连续异常',
  MIXED_PROBING: '同源多类异常',
} as const satisfies Record<SecurityRule, string>
export const securityCategories = {
  LOGIN_FAILURE: '登录失败',
  ACCESS_DENIED: '权限拒绝',
  PATH_PROBE: '路径探测',
  CSRF_FAILURE: 'CSRF 异常',
} as const satisfies Record<SecurityCategory, string>
