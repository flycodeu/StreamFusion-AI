<script setup lang="ts">
import { computed } from 'vue'
import { ApiRequestError } from '../../lib/http/error'
import { formatDateTime } from '../../utils/dateTime'

const props = defineProps<{ error: unknown }>()
const detail = computed(() => (props.error instanceof ApiRequestError ? props.error : null))
const fieldNames: Record<string, string> = {
  name: '名称',
  code: '编码',
  username: '账号',
  nickname: '昵称',
  phone: '电话',
  email: '邮箱',
  gender: '性别',
  avatarKey: '头像',
  parentId: '上级',
  sortOrder: '排序',
  type: '类型',
  routeName: '唯一标识',
  path: '页面路径',
  componentKey: '页面文件',
  moduleKey: '业务模块',
  enabled: '启用状态',
  visible: '导航显示',
  version: '数据版本',
  roleIds: '角色',
  departmentIds: '所属部门',
  menuIds: '页面授权',
  currentPassword: '当前密码',
  newPassword: '新密码',
  page: '页码',
  size: '每页条数',
  keyword: '查询条件',
  user: '操作人',
  action: '动作',
  module: '模块',
  result: '结果',
  startTime: '开始时间',
  endTime: '结束时间',
  traceId: '请求标识',
  sourceIp: '来源IP',
  status: '状态',
}
const fieldErrors = computed(() => {
  const error = detail.value
  if (error?.code !== 'VALIDATION_ERROR' || !error.data || typeof error.data !== 'object') return []
  const fields: unknown = 'fieldErrors' in error.data ? error.data.fieldErrors : null
  if (!Array.isArray(fields) || fields.length > 20) return []
  return fields.flatMap((field: unknown) => {
    if (
      !field ||
      typeof field !== 'object' ||
      !('field' in field) ||
      !('code' in field) ||
      typeof field.field !== 'string' ||
      !/^[A-Za-z][A-Za-z0-9_]{0,63}$/.test(field.field) ||
      field.code !== 'INVALID'
    )
      return []
    if (field.field === 'moduleKey')
      return ['业务模块：新建页面的唯一标识须对应已实现的模块，请核对菜单配置。']
    if (field.field === 'componentKey') return ['页面文件：请核对页面路径与已发布的页面文件。']
    return [`${fieldNames[field.field] || field.field}：格式不正确，请检查后重试。`]
  })
})
const advice = computed(() => {
  const error = detail.value
  if (!error) return '请检查输入后重试。'
  if (error.code === 'INITIAL_PASSWORD_UNAVAILABLE')
    return '请配置有效的 SF_AUTH_INITIAL_PASSWORD，并重启后端服务。'
  if (error.code === 'CSRF_INVALID') return '请求凭证已失效，请稍后重试；仍失败时刷新页面。'
  if (error.code === 'USERNAME_TAKEN') return '请更换账号后重试。'
  if (error.code === 'PROTECTED_ACCOUNT')
    return '请通过个人中心修改本人资料；受保护账号由超级管理员处理。'
  if (error.code === 'VERSION_EXHAUSTED') return '请联系管理员维护，不能通过刷新或重置版本解决。'
  if (error.code === 'IP_BLOCKED') return '请由超级管理员在操作记录的 IP 封禁列表中解除限制。'
  if (error.status === 429)
    return error.retryAfter
      ? `请求过于频繁，请在 ${error.retryAfter} 秒后重试。`
      : '请求过于频繁，请稍后重试。'
  if (error.status === 401) return '会话已失效，请重新登录。'
  if (error.status === 403) return '请联系管理员检查页面授权。'
  if (error.code === 'VERSION_CONFLICT' || error.status === 412)
    return '数据已变化，请刷新列表后重试。'
  if (error.status === 409) return '请检查名称、引用关系或对象状态后重试。'
  if (error.status === 503 || error.code === 'DEPENDENCY_UNAVAILABLE')
    return '服务暂不可用，请稍后重试；持续出现时提供请求标识给管理员。'
  return '请检查输入后重试；持续出现时提供请求标识给管理员。'
})
const time = computed(() => {
  const value = new Date(detail.value?.timestamp || detail.value?.receivedAt || Date.now())
  return formatDateTime(Number.isFinite(value.getTime()) ? value : new Date())
})
</script>

<template>
  <div v-if="error" class="request-error" role="alert">
    <strong>{{ detail?.message || '操作失败，请重试' }}</strong>
    <span>{{ advice }}</span>
    <span v-for="field in fieldErrors" :key="field">{{ field }}</span>
    <details v-if="detail">
      <summary>{{ detail.code }} · {{ time }}</summary>
      <div v-if="detail.traceId" class="trace-id">请求标识 {{ detail.traceId }}</div>
    </details>
  </div>
</template>

<style scoped>
.request-error {
  display: grid;
  gap: 5px;
  padding: 12px 14px;
  margin: 10px 0;
  background: #fff4f2;
  border: 1px solid #eac9c2;
  border-radius: 6px;
  color: #8f2d28;
  font-size: 13px;
}
.request-error summary {
  cursor: pointer;
}
.trace-id {
  margin-top: 6px;
  overflow-wrap: anywhere;
}
</style>
