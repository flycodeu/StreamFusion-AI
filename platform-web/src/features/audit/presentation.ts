import type { AuditDetail, AuditReference } from '../../api/audit/types'
import { usageLabels } from '../camera/streamLabels'
import { lifecycleLabels } from '../camera/presentation'
import { planStateLabels } from '../camera-status/presentation'
import { previewNodeLabels, previewMaintenanceLabels } from '../preview/nodePresentation'
import { retentionModeLabels } from './maintenancePresentation'
import { labelOf } from '../../utils/display'

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
  return labelOf(reasonNames, code)
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

export function auditChanges(
  detail:
    | (Pick<AuditDetail, 'changes' | 'relations'> & {
        record?: Pick<AuditDetail['record'], 'module'>
      })
    | null,
) {
  return Object.entries(detail?.changes ?? {}).map(([key, value]) => ({
    key,
    name: labelOf(changeNames, key),
    value: changeValue(key, value, detail?.record?.module),
    references: detail && Object.hasOwn(detail.relations, key) ? (detail.relations[key] ?? []) : [],
  }))
}

const previewModes = { ...previewNodeLabels, ...previewMaintenanceLabels }
const changeEnums: Record<string, Readonly<Record<string, string>>> = {
  enabled: { true: '是', false: '否' },
  lifecycle: { ...lifecycleLabels, DRAFT: '待配置' },
  state: planStateLabels,
  usageHint: { ...usageLabels, OTHER: '其他' },
}

function changeValue(key: string, value: string | string[] | null, module?: string) {
  if (Array.isArray(value)) return value.join('、') || '无'
  if (value == null) return '无'
  const field = key.replace(/^(before|after)([A-Z])/, (_, _prefix, first: string) =>
    first.toLowerCase(),
  )
  if (field === 'mode') {
    if (module === 'AUDIT_RETENTION') return labelOf(retentionModeLabels, value)
    if (module === 'PREVIEW_NODE') return labelOf(previewModes, value)
    return value
  }
  if (field === 'state' && module && module !== 'CAMERA_STATUS_PLAN') return value
  return Object.hasOwn(changeEnums, field) ? labelOf(changeEnums[field]!, value) : value
}

export function normalizedTraceId(value: string): string | null {
  const normalized = value.trim().toLowerCase()
  return /^[a-f0-9]{32}$/.test(normalized) ? normalized : null
}

export function traceDiagnostic(traceId: string): string | null {
  const normalized = normalizedTraceId(traceId)
  return normalized ? `traceId=${normalized}` : null
}
