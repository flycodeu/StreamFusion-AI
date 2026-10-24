import type { AuditDetail, AuditReference } from '../../api/audit/types'

export const changeNames: Record<string, string> = {
  mode: '策略模式',
  keepMonths: '保留月数',
  maxRecords: '保留条数',
  removedLogCount: '清理条数',
  format: '导出格式',
  rowCount: '导出条数',
  fileBytes: '文件字节数',
  name: '名称',
  code: '编码',
  username: '账号',
  nickname: '昵称',
  parentId: '上级',
  version: '版本',
  beforeVersion: '变更前版本',
  afterVersion: '变更后版本',
  addedGroupCount: '新增分组数量',
  removedGroupCount: '移除分组数量',
  addedCameraCount: '新增相机数量',
  removedCameraCount: '移除相机数量',
  addedDeviceCount: '新增相机设备数量',
  removedDeviceCount: '移除相机设备数量',
  groupIdsSummary: '分组编号摘要',
  cameraIdsSummary: '相机编号摘要',
  beforeGroupId: '变更前分组编号',
  afterGroupId: '变更后分组编号',
  beforeLifecycle: '变更前状态',
  afterLifecycle: '变更后状态',
  beforeDefaultProfileId: '变更前默认码流编号',
  afterDefaultProfileId: '变更后默认码流编号',
  label: '码流名称',
  usageHint: '码流用途',
  enabled: '是否启用',
  targetCount: '巡检设备数',
  intervalMinutes: '检测间隔（分钟）',
  durationHours: '执行时长（小时）',
  beforeState: '变更前状态',
  afterState: '变更后状态',
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

export function auditChanges(detail: Pick<AuditDetail, 'changes' | 'relations'> | null) {
  return Object.entries(detail?.changes ?? {}).map(([key, value]) => ({
    key,
    name: changeNames[key] ?? key,
    value: changeValue(key, value),
    references: detail?.relations[key] ?? [],
  }))
}

function changeValue(key: string, value: string | string[] | null) {
  if (Array.isArray(value)) return value.join('、') || '无'
  if (value == null) return '无'
  const enums: Record<string, Record<string, string>> = {
    mode: {
      AGE: '按时间',
      COUNT: '按数量',
      ENABLED: '已启用',
      DISABLED: '已停用',
      DRAINING: '排空中',
      LOCAL: '本地',
      REMOTE: '远程',
      OPEN: '正常开放',
      SEALED: '已准备维护',
      CLAIMED: '维护中',
      COMPLETE: '维护完成',
    },
    enabled: { true: '是', false: '否' },
    lifecycle: { ENABLED: '已启用', DISABLED: '已停用', DRAFT: '待配置' },
    state: { ACTIVE: '执行中', STOPPED: '已停止', EXPIRED: '已到期' },
    usageHint: { MAIN: '主码流', SUB: '辅码流', THIRD: '第三码流', OTHER: '其他' },
  }
  const field = key.replace(/^(before|after)([A-Z])/, (_, _prefix, first: string) =>
    first.toLowerCase(),
  )
  return enums[field]?.[value] ?? value
}

export function normalizedTraceId(value: string): string | null {
  const normalized = value.trim().toLowerCase()
  return /^[a-f0-9]{32}$/.test(normalized) ? normalized : null
}

export function traceDiagnostic(traceId: string): string | null {
  const normalized = normalizedTraceId(traceId)
  return normalized ? `traceId=${normalized}` : null
}
