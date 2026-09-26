<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import {
  ElButton,
  ElDialog,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessageBox,
  ElOption,
  ElPagination,
  ElSelect,
  ElSwitch,
  ElTable,
  ElTableColumn,
  ElTag,
  ElTreeSelect,
  vLoading,
} from 'element-plus'
import * as api from '../../api/user/api'
import type { UserCredentialResult, UserDetail, UserSummary } from '../../api/user/types'
import type { RoleOption } from '../../api/roles/types'
import type { DepartmentOption } from '../../api/departments/types'
import { sessionState } from '../../session/state'
import RequestError from '../../components/feedback/RequestError.vue'
import ColumnPicker from '../../components/table/ColumnPicker.vue'
import { useColumns } from '../../composables/table/useColumns'
import TablePanel from '../../components/table/TablePanel.vue'
import TableActions from '../../components/table/TableActions.vue'
import { useTableSelection } from '../../composables/table/useTableSelection'
import { ApiRequestError } from '../../lib/http/error'
import { accountHint } from '../../utils/loginValidation'
import RoleAssignmentDialog from '../../features/user/RoleAssignmentDialog.vue'
import LoginRecordTable from '../../features/login-records/LoginRecordTable.vue'
import { loginTime } from '../../features/login-records/presentation'
import { usePageScope } from '../../composables/usePageScope'

defineOptions({ name: 'SystemUser' })

const columns = useColumns('users', [
  'username',
  'nickname',
  'status',
  'loginRestriction',
  'departments',
  'roles',
])
const rows = ref<UserSummary[]>([])
const page = ref(1)
const size = ref(20)
const total = ref(0)
const keyword = ref('')
const status = ref<number | null>(null)
const loading = ref(false)
const saving = ref(false)
const deleting = ref(false)
const captureScope = usePageScope()
const error = ref<unknown>(null)
const dialog = ref(false)
const loginRecordsUser = ref<UserSummary | null>(null)
const loginRecordsDialog = ref(false)
const editing = ref<UserDetail | null>(null)
const submitted = ref(false)
const form = reactive({
  username: '',
  nickname: '',
  phone: '',
  email: '',
  gender: 0,
  avatarKey: '',
  departmentIds: [] as string[],
})
const relationDialog = ref(false)
const relationUser = ref<UserSummary | null>(null)
const relationVersion = ref('')
const selectedIds = ref<string[]>([])
const roleOptions = ref<RoleOption[]>([])
const assignedRoles = ref<RoleOption[]>([])
const departmentOptions = ref<DepartmentOption[]>([])
type DepartmentTreeOption = DepartmentOption & { children: DepartmentTreeOption[] }
const departmentTree = computed(() => {
  const nodes = new Map(
    departmentOptions.value.map((department) => [
      department.id,
      { ...department, children: [] } as DepartmentTreeOption,
    ]),
  )
  const roots: DepartmentTreeOption[] = []
  for (const node of nodes.values()) {
    const parent = node.parentId ? nodes.get(node.parentId) : null
    if (parent) parent.children.push(node)
    else roots.push(node)
  }
  return roots
})
const asUser = (row: unknown): UserSummary => row as UserSummary
const { selectedRows, singleSelected, setSelection, clearSelection } =
  useTableSelection<UserSummary>()
const resetCandidates = computed(() =>
  selectedRows.value.filter((user) => !isProtectedAccount(user)),
)
const resetting = ref(false)
const statusChanging = ref<string[]>([])
const resultDialog = ref(false)
const resultTitle = ref('密码重置结果')
interface PasswordResult {
  id: string
  username: string
  success: boolean
  temporaryPassword: string | null
  error: unknown
}
const passwordResults = ref<PasswordResult[]>([])
const departmentOnlyEdit = computed(() => !!editing.value && isProtectedAccount(editing.value))
const accountError = computed(() => (editing.value ? '' : accountHint(form.username.trim())))
const phoneError = computed(() => {
  const phone = form.phone.trim()
  return phone && !/^[+]?[0-9]{7,20}$/.test(phone) ? '请输入 7～20 位号码，可在开头使用 +' : ''
})
const emailError = computed(() => {
  const email = form.email.trim()
  return email && (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email) || email.length > 254)
    ? '请输入有效的邮箱地址'
    : ''
})
let sequence = 0
let detailSequence = 0

function isProtectedAccount(row: UserSummary): boolean {
  return (
    row.id === sessionState.me?.user.id || row.roles.some((role) => role.code === 'SUPER_ADMIN')
  )
}

function canManageRelations(row: UserSummary): boolean {
  return !!sessionState.me?.isSuperAdmin || !row.roles.some((role) => role.code === 'SUPER_ADMIN')
}

function canEdit(row: UserSummary): boolean {
  return !isProtectedAccount(row) || !!sessionState.me?.isSuperAdmin
}

async function load(): Promise<void> {
  const current = ++sequence
  const inScope = captureScope()
  loading.value = true
  error.value = null
  try {
    const result = await api.getUsers({
      page: page.value,
      size: size.value,
      keyword: keyword.value.trim() || undefined,
      status: typeof status.value === 'number' ? status.value : undefined,
    })
    if (current !== sequence || !inScope()) return
    rows.value = result.items
    clearSelection()
    total.value = result.total
  } catch (cause) {
    if (current === sequence && inScope()) error.value = cause
  } finally {
    if (current === sequence && inScope()) loading.value = false
  }
}

function search(): void {
  page.value = 1
  void load()
}

function resetSearch(): void {
  keyword.value = ''
  status.value = null
  search()
}

function editSelected(): void {
  if (singleSelected.value && canEdit(singleSelected.value)) void openEdit(singleSelected.value)
}

async function openCreate(): Promise<void> {
  const current = ++detailSequence
  const inScope = captureScope()
  error.value = null
  try {
    const options = await api.getDepartmentOptions()
    if (current !== detailSequence || !inScope()) return
    departmentOptions.value = options
  } catch (cause) {
    if (current === detailSequence && inScope()) error.value = cause
    return
  }
  editing.value = null
  submitted.value = false
  Object.assign(form, {
    username: '',
    nickname: '',
    phone: '',
    email: '',
    gender: 0,
    avatarKey: '',
    departmentIds: [],
  })
  dialog.value = true
}
async function openEdit(row: UserSummary): Promise<void> {
  const current = ++detailSequence
  const inScope = captureScope()
  error.value = null
  try {
    const [user, options] = await Promise.all([api.getUser(row.id), api.getDepartmentOptions()])
    if (current !== detailSequence || !inScope()) return
    departmentOptions.value = options
    editing.value = user
    submitted.value = false
    Object.assign(form, {
      username: user.username,
      nickname: user.nickname || '',
      phone: user.phone || '',
      email: user.email || '',
      gender: user.gender,
      avatarKey: user.avatarKey || '',
      departmentIds: user.departments.map((department) => department.id),
    })
    dialog.value = true
  } catch (cause) {
    if (current === detailSequence && inScope()) error.value = cause
  }
}
async function save(): Promise<void> {
  submitted.value = true
  if (
    saving.value ||
    (!departmentOnlyEdit.value && (accountError.value || phoneError.value || emailError.value))
  )
    return
  saving.value = true
  const inScope = captureScope()
  error.value = null
  try {
    const fields = {
      nickname: form.nickname.trim() || null,
      phone: form.phone.trim() || null,
      email: form.email.trim() || null,
      gender: form.gender,
      avatarKey: form.avatarKey.trim() || null,
      departmentIds: [...form.departmentIds],
    }
    if (editing.value && departmentOnlyEdit.value)
      await api.setUserDepartments(editing.value.id, editing.value.version, fields.departmentIds)
    else if (editing.value) await api.updateUser({ ...editing.value, ...fields })
    else {
      const created = await api.createUser({ username: form.username.trim(), ...fields })
      if (!inScope()) return
      resultTitle.value = '用户创建成功'
      passwordResults.value = [successfulPasswordResult(created)]
      resultDialog.value = true
    }
    if (!inScope()) return
    dialog.value = false
    await load()
  } catch (cause) {
    if (inScope()) error.value = cause
  } finally {
    saving.value = false
  }
}
async function openRelation(row: UserSummary): Promise<void> {
  const currentRequest = ++detailSequence
  const inScope = captureScope()
  error.value = null
  try {
    const [current, options] = await Promise.all([api.getUserRoles(row.id), api.getRoleOptions()])
    if (currentRequest !== detailSequence || !inScope()) return
    relationVersion.value = current.version
    selectedIds.value = current.roles.map((role) => role.id)
    assignedRoles.value = current.roles
    roleOptions.value = options
    relationUser.value = row
    relationDialog.value = true
  } catch (cause) {
    if (currentRequest !== detailSequence || !inScope()) return
    relationDialog.value = false
    error.value = cause
  }
}
async function saveRelation(roleIds: string[]): Promise<void> {
  if (!relationUser.value || !relationDialog.value || saving.value) return
  saving.value = true
  const inScope = captureScope()
  error.value = null
  try {
    await api.setUserRoles(relationUser.value.id, relationVersion.value, roleIds)
    if (!inScope()) return
    relationDialog.value = false
    await load()
  } catch (cause) {
    if (inScope()) error.value = cause
  } finally {
    saving.value = false
  }
}
async function changeStatus(row: UserSummary, enabled: boolean): Promise<void> {
  if (isProtectedAccount(row) || statusChanging.value.includes(row.id) || resetting.value) return
  const inScope = captureScope()
  const current = sequence
  statusChanging.value.push(row.id)
  error.value = null
  try {
    const updated = await api.changeUserStatus(row, enabled)
    if (!inScope()) return
    if (
      current === sequence &&
      typeof status.value === 'number' &&
      status.value !== updated.status &&
      rows.value.length === 1 &&
      rows.value[0]?.id === row.id &&
      page.value > 1
    )
      page.value--
    await load()
  } catch (cause) {
    if (inScope()) error.value = cause
  } finally {
    statusChanging.value = statusChanging.value.filter((id) => id !== row.id)
  }
}

function successfulPasswordResult(user: UserCredentialResult): PasswordResult {
  return {
    id: user.id,
    username: user.username,
    success: true,
    temporaryPassword: user.temporaryPassword,
    error: null,
  }
}

async function resetPasswords(users: UserSummary[]): Promise<void> {
  const targets = users.filter((user) => !isProtectedAccount(user))
  if (!targets.length || resetting.value) return
  const inScope = captureScope()
  resetting.value = true
  try {
    await ElMessageBox.confirm(`确认重置 ${targets.length} 个用户的密码？`, '重置密码', {
      type: 'warning',
    })
    if (!inScope()) return
    passwordResults.value = []
    for (const user of targets) {
      if (!inScope()) return
      try {
        const result = await api.resetUserPassword(user)
        if (!inScope()) return
        passwordResults.value.push(successfulPasswordResult(result))
      } catch (cause) {
        if (!inScope()) return
        passwordResults.value.push({
          id: user.id,
          username: user.username,
          success: false,
          temporaryPassword: null,
          error: cause,
        })
        if (cause instanceof ApiRequestError && cause.status === 401) break
      }
    }
    resultTitle.value = '密码重置结果'
    resultDialog.value = true
    await load()
  } catch (cause) {
    if (inScope() && cause !== 'cancel' && cause !== 'close') error.value = cause
  } finally {
    resetting.value = false
  }
}
async function remove(row: UserSummary): Promise<void> {
  if (deleting.value) return
  const inScope = captureScope()
  deleting.value = true
  try {
    await ElMessageBox.confirm(`删除用户“${row.username}”？`, '确认删除', { type: 'warning' })
    if (!inScope()) return
    await api.deleteUser(row)
    if (!inScope()) return
    if (rows.value.length === 1 && page.value > 1) page.value--
    await load()
  } catch (cause) {
    if (inScope() && cause !== 'cancel' && cause !== 'close') error.value = cause
  } finally {
    deleting.value = false
  }
}

function showLoginRecords(row: UserSummary): void {
  loginRecordsUser.value = row
  loginRecordsDialog.value = true
}

onMounted(load)
</script>

<template>
  <div class="content-page">
    <ElForm class="search-form" inline @submit.prevent="search">
      <ElFormItem label="用户">
        <ElInput v-model="keyword" class="search-field" placeholder="请输入账号或昵称" clearable />
      </ElFormItem>
      <ElFormItem label="状态">
        <ElSelect v-model="status" class="status-field" placeholder="全部状态" clearable>
          <ElOption label="待改密" :value="0" />
          <ElOption label="正常" :value="1" />
          <ElOption label="停用" :value="2" />
        </ElSelect>
      </ElFormItem>
      <ElFormItem>
        <ElButton type="primary" native-type="submit" :loading="loading">查询</ElButton>
        <ElButton @click="resetSearch">重置</ElButton>
      </ElFormItem>
    </ElForm>
    <RequestError :error="error" />
    <TablePanel>
      <template #actions>
        <ElButton type="primary" :disabled="resetting" @click="openCreate">新建用户</ElButton>
        <ElButton
          :disabled="!resetCandidates.length || loading"
          :loading="resetting"
          @click="resetPasswords(resetCandidates)"
          >批量重置密码</ElButton
        >
        <ElButton
          :disabled="!singleSelected || !canEdit(singleSelected) || loading || resetting"
          @click="editSelected"
          >编辑</ElButton
        >
      </template>
      <template #tools>
        <ElButton :loading="loading" @click="load">刷新</ElButton>
        <ColumnPicker
          v-model="columns"
          :options="[
            { key: 'username', label: '账号' },
            { key: 'nickname', label: '昵称' },
            { key: 'status', label: '状态' },
            { key: 'loginRestriction', label: '登录限制' },
            { key: 'departments', label: '部门' },
            { key: 'roles', label: '角色' },
          ]"
        />
      </template>
      <ElTable
        v-loading="loading"
        border
        :data="rows"
        row-key="id"
        style="width: 100%"
        empty-text="暂无用户"
        @selection-change="setSelection"
      >
        <ElTableColumn
          type="selection"
          width="48"
          align="center"
          :selectable="(row: UserSummary) => !isProtectedAccount(row) && !resetting"
        />
        <ElTableColumn
          v-if="columns.includes('username')"
          prop="username"
          label="账号"
          min-width="145"
        />
        <ElTableColumn
          v-if="columns.includes('nickname')"
          prop="nickname"
          label="昵称"
          min-width="130"
        />
        <ElTableColumn v-if="columns.includes('status')" label="状态" width="100"
          ><template #default="{ row }"
            ><ElTag
              :type="row.status === 2 ? 'danger' : row.status === 0 ? 'warning' : 'success'"
              effect="plain"
              >{{ row.status === 0 ? '待改密' : row.status === 1 ? '正常' : '停用' }}</ElTag
            ></template
          ></ElTableColumn
        >
        <ElTableColumn v-if="columns.includes('loginRestriction')" label="登录限制" min-width="195">
          <template #default="{ row }">
            <div v-if="row.loginRestricted" class="restriction-cell">
              <ElTag type="warning" effect="plain">登录受限</ElTag>
              <span>至 {{ loginTime(row.lockedUntil) }}</span>
            </div>
            <span v-else>—</span>
          </template>
        </ElTableColumn>
        <ElTableColumn
          v-if="columns.includes('departments')"
          label="部门"
          min-width="160"
          show-overflow-tooltip
          ><template #default="{ row }">{{
            row.departments.map((item: DepartmentOption) => item.name).join('、') || '—'
          }}</template></ElTableColumn
        >
        <ElTableColumn
          v-if="columns.includes('roles')"
          label="角色"
          min-width="160"
          show-overflow-tooltip
          ><template #default="{ row }">{{
            row.roles.map((item: RoleOption) => item.name).join('、') || '—'
          }}</template></ElTableColumn
        >
        <ElTableColumn
          label="操作"
          min-width="410"
          fixed="right"
          class-name="table-actions-column"
          :resizable="false"
          ><template #default="{ row }"
            ><TableActions>
              <ElButton
                plain
                type="primary"
                :disabled="!canEdit(asUser(row)) || resetting"
                @click="openEdit(asUser(row))"
                >编辑</ElButton
              >
              <ElButton
                plain
                type="primary"
                :disabled="!canManageRelations(asUser(row)) || resetting"
                @click="openRelation(asUser(row))"
                >角色</ElButton
              >
              <ElButton
                plain
                type="primary"
                :disabled="!canManageRelations(asUser(row))"
                @click="showLoginRecords(asUser(row))"
                >登录记录</ElButton
              >
              <ElSwitch
                :model-value="row.status !== 2"
                :disabled="isProtectedAccount(asUser(row)) || resetting"
                :loading="statusChanging.includes(row.id)"
                :aria-label="`启用用户 ${row.username}`"
                inline-prompt
                active-text="开"
                inactive-text="关"
                @change="changeStatus(asUser(row), Boolean($event))"
              />
              <ElButton
                plain
                type="danger"
                class="table-action-end"
                :disabled="isProtectedAccount(asUser(row)) || resetting || deleting"
                @click="remove(asUser(row))"
                >删除</ElButton
              >
            </TableActions></template
          ></ElTableColumn
        >
      </ElTable>
      <template #footer>
        <ElPagination
          v-model:current-page="page"
          v-model:page-size="size"
          :total="total"
          :page-sizes="[20, 50, 100]"
          layout="total, sizes, prev, pager, next"
          @change="load"
        />
      </template>
    </TablePanel>
    <ElDialog
      v-model="loginRecordsDialog"
      :title="`${loginRecordsUser?.nickname || loginRecordsUser?.username || ''} · 登录记录`"
      width="1120px"
      destroy-on-close
    >
      <LoginRecordTable
        v-if="loginRecordsDialog && loginRecordsUser"
        :user-id="loginRecordsUser.id"
      />
    </ElDialog>
    <ElDialog
      v-model="dialog"
      :title="editing ? '编辑用户' : '新建用户'"
      width="800px"
      destroy-on-close
      :close-on-click-modal="!saving"
      :close-on-press-escape="!saving"
      :show-close="!saving"
    >
      <RequestError :error="error" />
      <ElForm class="user-form-grid" label-width="80px" :disabled="saving" @submit.prevent="save">
        <ElFormItem label="账号" :required="!editing" :error="submitted ? accountError : ''"
          ><ElInput v-model="form.username" :disabled="!!editing" maxlength="32"
        /></ElFormItem>
        <ElFormItem label="昵称"
          ><ElInput v-model="form.nickname" maxlength="64" :disabled="departmentOnlyEdit"
        /></ElFormItem>
        <ElFormItem label="电话" :error="submitted && !departmentOnlyEdit ? phoneError : ''"
          ><ElInput
            v-model="form.phone"
            :disabled="departmentOnlyEdit"
            autocomplete="tel"
            maxlength="21"
        /></ElFormItem>
        <ElFormItem label="邮箱" :error="submitted && !departmentOnlyEdit ? emailError : ''"
          ><ElInput
            v-model="form.email"
            :disabled="departmentOnlyEdit"
            autocomplete="email"
            maxlength="254"
        /></ElFormItem>
        <ElFormItem label="性别"
          ><ElSelect v-model="form.gender" :disabled="departmentOnlyEdit"
            ><ElOption label="未设置" :value="0" /><ElOption label="男" :value="1" /><ElOption
              label="女"
              :value="2" /></ElSelect
        ></ElFormItem>
        <ElFormItem class="user-form-full" label="所属部门">
          <ElTreeSelect
            v-model="form.departmentIds"
            :data="departmentTree"
            :props="{ label: 'name', children: 'children' }"
            node-key="id"
            multiple
            :multiple-limit="20"
            check-strictly
            show-checkbox
            default-expand-all
            filterable
            clearable
            collapse-tags
            collapse-tags-tooltip
            placeholder="请选择部门"
          />
        </ElFormItem>
      </ElForm>
      <template #footer
        ><ElButton :disabled="saving" @click="dialog = false">取消</ElButton
        ><ElButton type="primary" :loading="saving" @click="save">保存</ElButton></template
      >
    </ElDialog>
    <RoleAssignmentDialog
      v-model="relationDialog"
      v-model:selected-ids="selectedIds"
      :username="relationUser?.username || ''"
      :options="roleOptions"
      :assigned="assignedRoles"
      :protect-own-super-admin="
        !!sessionState.me?.isSuperAdmin && relationUser?.id === sessionState.me?.user.id
      "
      :saving="saving"
      :error="error"
      @save="saveRelation"
    />
    <ElDialog
      v-model="resultDialog"
      :title="resultTitle"
      width="760px"
      destroy-on-close
      @closed="passwordResults = []"
    >
      <ElTable :data="passwordResults" row-key="id" border>
        <ElTableColumn prop="username" label="账号" min-width="140" />
        <ElTableColumn label="结果" width="90">
          <template #default="{ row }"
            ><ElTag :type="row.success ? 'success' : 'danger'">{{
              row.success ? '成功' : '失败'
            }}</ElTag></template
          >
        </ElTableColumn>
        <ElTableColumn label="初始密码" min-width="220">
          <template #default="{ row }"
            ><code v-if="row.success" class="temporary-password">{{ row.temporaryPassword }}</code
            ><span v-else>—</span></template
          >
        </ElTableColumn>
        <ElTableColumn label="错误信息" min-width="200">
          <template #default="{ row }"
            ><RequestError v-if="!row.success" :error="row.error" /><span v-else>—</span></template
          >
        </ElTableColumn>
      </ElTable>
      <template #footer
        ><ElButton type="primary" @click="resultDialog = false">关闭</ElButton></template
      >
    </ElDialog>
  </div>
</template>

<style scoped>
.restriction-cell {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
}
.temporary-password {
  user-select: all;
  overflow-wrap: anywhere;
}
.user-form-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0 24px;
}
.user-form-full {
  grid-column: 1 / -1;
}
@media (max-width: 680px) {
  .user-form-grid {
    grid-template-columns: minmax(0, 1fr);
    gap: 0;
  }
}
</style>
