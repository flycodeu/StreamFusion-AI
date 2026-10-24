<script setup lang="ts">
import {
  genderOptions,
  userStatusLabel,
  userStatusTone,
  userStatusOptions,
} from '../../features/user/presentation'
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  ElButton,
  ElDialog,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessageBox,
  ElMessage,
  ElDropdown,
  ElDropdownMenu,
  ElDropdownItem,
  ElOption,
  ElPagination,
  ElSelect,
  ElEmpty,
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
import UserRolesField from '../../features/user/UserRolesField.vue'
import { protectedRoleSelection, roleChoices } from '../../features/user/roleSelection'
import { refreshIdentity } from '../../session/session'
import LoginRecordTable from '../../features/login-records/LoginRecordTable.vue'
import { formatDateTime } from '../../utils/dateTime'
import { usePageScope } from '../../composables/usePageScope'

defineOptions({ name: 'SystemUser' })

const router = useRouter()
const route = useRoute()
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
const forcingLogout = ref<string[]>([])
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
  roleIds: [] as string[],
})
const roleOptions = ref<RoleOption[]>([])
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
const relationOnlyEdit = computed(() => !!editing.value && isProtectedAccount(editing.value))
const canEditDepartments = computed(
  () => !relationOnlyEdit.value || !!sessionState.me?.isSuperAdmin,
)
const protectOwnSuperAdmin = computed(
  () => !!sessionState.me?.isSuperAdmin && editing.value?.id === sessionState.me?.user.id,
)
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
  return canManageRelations(row)
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
    const [options, roles] = await Promise.all([api.getDepartmentOptions(), api.getRoleOptions()])
    if (current !== detailSequence || !inScope()) return
    departmentOptions.value = options
    roleOptions.value = roles
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
    roleIds: [],
  })
  dialog.value = true
}
async function openEdit(row: UserSummary): Promise<void> {
  const current = ++detailSequence
  const inScope = captureScope()
  error.value = null
  try {
    const [user, options, roles] = await Promise.all([
      api.getUser(row.id),
      api.getDepartmentOptions(),
      api.getRoleOptions(),
    ])
    if (current !== detailSequence || !inScope()) return
    departmentOptions.value = options
    roleOptions.value = roles
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
      roleIds: user.roles.map((role) => role.id),
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
    (!relationOnlyEdit.value && (accountError.value || phoneError.value || emailError.value))
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
      departmentIds: canEditDepartments.value ? [...form.departmentIds] : undefined,
      roleIds: protectedRoleSelection(
        form.roleIds,
        roleChoices(roleOptions.value, editing.value?.roles ?? [], protectOwnSuperAdmin.value),
      ),
    }
    const editsOwnRoles = editing.value?.id === sessionState.me?.user.id
    if (editing.value) await api.updateUser({ ...editing.value, ...fields })
    else {
      const created = await api.createUser({ username: form.username.trim(), ...fields })
      if (!inScope()) return
      resultTitle.value = '用户创建成功'
      passwordResults.value = [successfulPasswordResult(created)]
      resultDialog.value = true
    }
    if (!inScope()) return
    dialog.value = false
    if (editsOwnRoles) {
      await refreshIdentity()
      if (inScope()) {
        await router.replace({
          path: route.path,
          query: route.query,
          hash: route.hash,
          force: true,
        })
      }
    }
    if (inScope()) await load()
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

async function forceLogout(row: UserSummary): Promise<void> {
  if (isProtectedAccount(row) || forcingLogout.value.includes(row.id)) return
  const inScope = captureScope()
  forcingLogout.value.push(row.id)
  try {
    await ElMessageBox.confirm(
      `将结束账号「${row.username}」的当前登录，对方会收到登出提示。账号仍可重新登录。`,
      '强制登出',
      {
        type: 'warning',
        confirmButtonClass: 'el-button--danger',
        confirmButtonText: '确认登出',
        cancelButtonText: '取消',
      },
    )
    if (!inScope()) return
    error.value = null
    await api.forceLogoutUser(row)
    if (!inScope()) return
    ElMessage.success('已强制登出')
    await load()
  } catch (cause) {
    if (cause !== 'cancel' && cause !== 'close' && inScope()) error.value = cause
  } finally {
    forcingLogout.value = forcingLogout.value.filter((id) => id !== row.id)
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

async function copyPassword(password: string | null): Promise<void> {
  if (!password) return
  const inScope = captureScope()
  try {
    await globalThis.navigator.clipboard.writeText(password)
    if (inScope()) ElMessage.success('已复制初始密码')
  } catch {
    if (inScope()) ElMessage.warning('复制失败，请选中密码手动复制')
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
      confirmButtonClass: 'el-button--danger',
      confirmButtonText: '确认重置',
      cancelButtonText: '取消',
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
  const current = sequence
  deleting.value = true
  try {
    await ElMessageBox.confirm(`删除用户“${row.username}”？`, '确认删除', {
      type: 'warning',
      confirmButtonClass: 'el-button--danger',
      confirmButtonText: '确认删除',
      cancelButtonText: '取消',
    })
    if (!inScope()) return
    await api.deleteUser(row)
    if (!inScope()) return
    if (
      current === sequence &&
      rows.value.length === 1 &&
      rows.value[0]?.id === row.id &&
      page.value > 1
    )
      page.value--
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
          <ElOption
            v-for="option in userStatusOptions"
            :key="option.value"
            :label="option.label"
            :value="option.value"
          />
        </ElSelect>
      </ElFormItem>
      <ElFormItem>
        <ElButton type="primary" native-type="submit" :loading="loading">查询</ElButton>
        <ElButton @click="resetSearch">重置</ElButton>
      </ElFormItem>
    </ElForm>
    <RequestError :error="error" />
    <TablePanel :initial-loading="loading && !rows.length">
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
        <template #empty>
          <ElEmpty
            :image-size="64"
            :description="
              error
                ? '用户列表读取失败'
                : keyword || status !== null
                  ? '未找到符合条件的用户'
                  : '暂无用户'
            "
          >
            <ElButton v-if="error" @click="load">重新加载</ElButton>
            <ElButton v-else-if="keyword || status !== null" @click="resetSearch"
              >重置筛选</ElButton
            >
            <ElButton v-else type="primary" @click="openCreate">新建用户</ElButton>
          </ElEmpty>
        </template>
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
            ><ElTag :type="userStatusTone(row.status)" effect="plain">{{
              userStatusLabel(row.status)
            }}</ElTag></template
          ></ElTableColumn
        >
        <ElTableColumn v-if="columns.includes('loginRestriction')" label="登录限制" min-width="195">
          <template #default="{ row }">
            <div v-if="row.loginRestricted" class="restriction-cell">
              <ElTag type="warning" effect="plain">登录受限</ElTag>
              <span>至 {{ formatDateTime(row.lockedUntil) }}</span>
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
          width="150"
          fixed="right"
          class-name="table-actions-column"
          :resizable="false"
          ><template #default="{ row }"
            ><TableActions compact>
              <ElButton
                link
                type="primary"
                :disabled="!canEdit(asUser(row)) || resetting"
                @click="openEdit(asUser(row))"
                >编辑</ElButton
              >
              <ElDropdown trigger="click" class="table-action-end">
                <ElButton
                  link
                  type="primary"
                  :disabled="resetting || deleting || forcingLogout.includes(row.id)"
                  >更多 ▾</ElButton
                >
                <template #dropdown>
                  <ElDropdownMenu>
                    <ElDropdownItem
                      :disabled="!canManageRelations(asUser(row)) || resetting"
                      @click="showLoginRecords(asUser(row))"
                      >登录记录</ElDropdownItem
                    >
                    <ElDropdownItem
                      :disabled="
                        isProtectedAccount(asUser(row)) ||
                        resetting ||
                        statusChanging.includes(row.id)
                      "
                      @click="changeStatus(asUser(row), row.status === 2)"
                    >
                      {{ row.status === 2 ? '启用账号' : '停用账号' }}
                    </ElDropdownItem>
                    <ElDropdownItem
                      :disabled="isProtectedAccount(asUser(row)) || forcingLogout.includes(row.id)"
                      @click="forceLogout(asUser(row))"
                      >强制登出</ElDropdownItem
                    >
                    <ElDropdownItem
                      divided
                      :disabled="isProtectedAccount(asUser(row)) || deleting"
                      @click="remove(asUser(row))"
                      ><span class="danger-action">删除用户</span></ElDropdownItem
                    >
                  </ElDropdownMenu>
                </template>
              </ElDropdown>
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
          layout="total, sizes, prev, pager, next, jumper"
          @change="load"
        />
      </template>
    </TablePanel>
    <ElDialog
      v-model="loginRecordsDialog"
      :title="`${loginRecordsUser?.nickname || loginRecordsUser?.username || ''} · 登录记录`"
      width="1120px"
      class="management-dialog"
      align-center
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
      width="720px"
      class="management-dialog"
      align-center
      destroy-on-close
      :close-on-click-modal="!saving"
      :close-on-press-escape="!saving"
      :show-close="!saving"
    >
      <RequestError :error="error" />
      <ElForm class="user-form-grid" label-position="top" :disabled="saving" @submit.prevent="save">
        <ElFormItem label="账号" :required="!editing" :error="submitted ? accountError : ''"
          ><ElInput v-model="form.username" :disabled="!!editing" maxlength="32"
        /></ElFormItem>
        <ElFormItem label="昵称"
          ><ElInput v-model="form.nickname" maxlength="64" :disabled="relationOnlyEdit"
        /></ElFormItem>
        <ElFormItem label="电话" :error="submitted && !relationOnlyEdit ? phoneError : ''"
          ><ElInput
            v-model="form.phone"
            :disabled="relationOnlyEdit"
            autocomplete="tel"
            maxlength="21"
        /></ElFormItem>
        <ElFormItem label="邮箱" :error="submitted && !relationOnlyEdit ? emailError : ''"
          ><ElInput
            v-model="form.email"
            :disabled="relationOnlyEdit"
            autocomplete="email"
            maxlength="254"
        /></ElFormItem>
        <ElFormItem label="性别"
          ><ElSelect v-model="form.gender" :disabled="relationOnlyEdit"
            ><ElOption
              v-for="option in genderOptions"
              :key="option.value"
              :label="option.label"
              :value="option.value" /></ElSelect
        ></ElFormItem>
        <ElFormItem class="user-form-full" label="所属部门">
          <ElTreeSelect
            v-model="form.departmentIds"
            :disabled="!canEditDepartments"
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
        <ElFormItem class="user-form-full" label="角色">
          <UserRolesField
            v-model="form.roleIds"
            :options="roleOptions"
            :assigned="editing?.roles ?? []"
            :protect-own-super-admin="protectOwnSuperAdmin"
          />
        </ElFormItem>
      </ElForm>
      <template #footer
        ><ElButton :disabled="saving" @click="dialog = false">取消</ElButton
        ><ElButton type="primary" :loading="saving" @click="save">保存</ElButton></template
      >
    </ElDialog>
    <ElDialog
      v-model="resultDialog"
      :title="resultTitle"
      width="760px"
      class="management-dialog"
      align-center
      destroy-on-close
      @closed="passwordResults = []"
    >
      <ElTable :data="passwordResults" row-key="id" border :max-height="380">
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
            ><div v-if="row.success" class="password-cell">
              <code class="temporary-password">{{ row.temporaryPassword }}</code>
              <ElButton
                link
                type="primary"
                :disabled="!row.temporaryPassword"
                :aria-label="`复制 ${row.username} 的初始密码`"
                @click="copyPassword(row.temporaryPassword)"
                >复制</ElButton
              >
            </div>
            <span v-else>—</span></template
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
  padding: 2px 8px;
  border-radius: 4px;
  background: #f1f5f9;
  color: #0f172a;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 13px;
}
.password-cell {
  display: flex;
  align-items: center;
  gap: 12px;
}
.password-cell code {
  min-width: 0;
}
.danger-action {
  color: var(--el-color-danger);
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
