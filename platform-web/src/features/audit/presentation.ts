import type { AuditDetail, AuditReference } from '../../api/audit/types'

export const changeNames: Record<string, string> = {
  name: '名称',
  code: '编码',
  username: '账号',
  nickname: '昵称',
  parentId: '上级',
  roleIds: '角色',
  menuIds: '菜单',
  departmentIds: '部门',
  beforeRoleIds: '变更前角色',
  afterRoleIds: '变更后角色',
  beforeMenuIds: '变更前菜单',
  afterMenuIds: '变更后菜单',
  beforeDepartmentIds: '变更前部门',
  afterDepartmentIds: '变更后部门',
}

const reasonNames: Record<string, string> = {
  INVALID_CREDENTIALS: '账号或密码不正确',
  ACCOUNT_UNAVAILABLE: '账号已不可用或登录状态已变化',
  ACCOUNT_COOLING_DOWN: '连续登录失败，账号暂时锁定',
  RETRY_REQUIRED: '账号限制已变化，请重新登录',
  LOGIN_FAILURE_THRESHOLD: '登录失败次数超限',
}

export function auditReason(code: string | null): string {
  return code ? reasonNames[code] || code : '—'
}

export function referenceName(reference: AuditReference | null, fallback = '—'): string {
  if (!reference) return fallback
  if (reference.source === 'MISSING') return `已删除或未知对象（ID ${reference.id}）`
  const name = reference.name || reference.code || `ID ${reference.id}`
  return reference.code && reference.code !== name ? `${name}（${reference.code}）` : name
}

export function referenceSource(reference: AuditReference): string {
  return reference.source === 'SNAPSHOT'
    ? '操作时名称'
    : reference.source === 'CURRENT'
      ? '当前名称'
      : '无名称记录'
}

export function auditChanges(detail: AuditDetail | null) {
  return Object.entries(detail?.changes ?? {}).map(([key, value]) => ({
    key,
    name: changeNames[key] ?? key,
    value: Array.isArray(value) ? value.join('、') || '无' : (value ?? '无'),
    references: detail?.relations[key] ?? [],
  }))
}

export function normalizedTraceId(value: string): string | null {
  const normalized = value.trim().toLowerCase()
  return /^[a-f0-9]{32}$/.test(normalized) ? normalized : null
}

export function traceDiagnostic(traceId: string): string | null {
  const normalized = normalizedTraceId(traceId)
  return normalized ? `traceId=${normalized}` : null
}
