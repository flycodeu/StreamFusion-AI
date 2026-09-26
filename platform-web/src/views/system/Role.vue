<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
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
  ElTable,
  ElTableColumn,
  ElTree,
  ElTag,
  vLoading,
} from 'element-plus'
import * as api from '../../api/roles/api'
import type { Role, RoleMenuNode, RoleMenus } from '../../api/roles/types'
import RequestError from '../../components/feedback/RequestError.vue'
import ColumnPicker from '../../components/table/ColumnPicker.vue'
import { useColumns } from '../../composables/table/useColumns'
import TablePanel from '../../components/table/TablePanel.vue'
import TableActions from '../../components/table/TableActions.vue'
import { useTableSelection } from '../../composables/table/useTableSelection'

defineOptions({ name: 'SystemRole' })

const columns = useColumns('roles', ['code', 'name', 'description', 'status'])
const rows = ref<Role[]>([])
const page = ref(1)
const size = ref(20)
const total = ref(0)
const keyword = ref('')
const status = ref('')
const loading = ref(false)
const error = ref<unknown>(null)
const dialog = ref(false)
const editing = ref<Role | null>(null)
const form = reactive({ code: '', name: '', description: '' })
const submitted = ref(false)
const codeError = computed(() => {
  if (editing.value) return ''
  const code = form.code.trim()
  if (!code) return '请输入角色编码'
  if (!/^[A-Z][A-Z0-9_]{1,63}$/.test(code))
    return '请输入 2～64 位编码，以大写字母开头，仅支持大写字母、数字和下划线'
  return code === 'SUPER_ADMIN' ? '该编码为系统保留编码' : ''
})
const saving = ref(false)
const menuDialog = ref(false)
const roleMenus = ref<RoleMenus | null>(null)
const menuRole = ref<Role | null>(null)
const asRole = (row: unknown): Role => row as Role
const treeRef = ref<InstanceType<typeof ElTree> | null>(null)
const { singleSelected, setSelection, clearSelection } = useTableSelection<Role>()
let sequence = 0
let menuSequence = 0

async function load(): Promise<void> {
  const current = ++sequence
  loading.value = true
  error.value = null
  try {
    const result = await api.getRoles({
      page: page.value,
      size: size.value,
      keyword: keyword.value.trim() || undefined,
      status: status.value || undefined,
    })
    if (current !== sequence) return
    rows.value = result.items
    clearSelection()
    total.value = result.total
  } catch (cause) {
    if (current === sequence) error.value = cause
  } finally {
    if (current === sequence) loading.value = false
  }
}

function search(): void {
  page.value = 1
  void load()
}

function resetSearch(): void {
  keyword.value = ''
  status.value = ''
  search()
}

function editSelected(): void {
  if (singleSelected.value && singleSelected.value.code !== 'SUPER_ADMIN')
    openEdit(singleSelected.value)
}

function openCreate(): void {
  editing.value = null
  submitted.value = false
  error.value = null
  Object.assign(form, { code: '', name: '', description: '' })
  dialog.value = true
}
function openEdit(role: Role): void {
  editing.value = role
  submitted.value = false
  error.value = null
  Object.assign(form, { code: role.code, name: role.name, description: role.description || '' })
  dialog.value = true
}
async function save(): Promise<void> {
  submitted.value = true
  if (!form.name.trim() || codeError.value || saving.value) return
  saving.value = true
  error.value = null
  try {
    if (editing.value)
      await api.updateRole({
        ...editing.value,
        name: form.name.trim(),
        description: form.description.trim() || null,
      })
    else
      await api.createRole({
        code: form.code.trim(),
        name: form.name.trim(),
        description: form.description.trim() || null,
      })
    dialog.value = false
    await load()
  } catch (cause) {
    error.value = cause
  } finally {
    saving.value = false
  }
}
async function changeStatus(role: Role): Promise<void> {
  error.value = null
  try {
    await api.changeRoleStatus(role, role.status !== 'ENABLED')
    await load()
  } catch (cause) {
    error.value = cause
  }
}
async function remove(role: Role): Promise<void> {
  try {
    await ElMessageBox.confirm(`删除角色“${role.name}”？`, '确认删除', { type: 'warning' })
    await api.deleteRole(role)
    if (rows.value.length === 1 && page.value > 1) page.value--
    await load()
  } catch (cause) {
    if (cause !== 'cancel' && cause !== 'close') error.value = cause
  }
}
function pageIds(nodes: RoleMenuNode[]): string[] {
  return nodes.flatMap((node) => [
    ...(node.type === 'PAGE' ? [node.id] : []),
    ...pageIds(node.children),
  ])
}
async function openMenus(role: Role): Promise<void> {
  const current = ++menuSequence
  error.value = null
  try {
    const menus = await api.getRoleMenus(role.id)
    if (current !== menuSequence) return
    menuRole.value = role
    roleMenus.value = menus
    menuDialog.value = true
    await nextTick()
    if (current === menuSequence) treeRef.value?.setCheckedKeys(menus.selectedPageIds)
  } catch (cause) {
    if (current === menuSequence) error.value = cause
  }
}
async function saveMenus(): Promise<void> {
  if (!roleMenus.value || !menuRole.value || saving.value) return
  saving.value = true
  error.value = null
  try {
    const allowed = new Set(pageIds(roleMenus.value.tree))
    const selected = (treeRef.value?.getCheckedKeys() ?? [])
      .map(String)
      .filter((id) => allowed.has(id))
    await api.setRoleMenus(menuRole.value.id, roleMenus.value.version, selected)
    menuDialog.value = false
    await load()
  } catch (cause) {
    error.value = cause
  } finally {
    saving.value = false
  }
}
interface MenuCheckboxNode extends Omit<RoleMenuNode, 'children'> {
  disabled: boolean
  children: MenuCheckboxNode[]
}

function menuTree(nodes: RoleMenuNode[], ancestorsEnabled = true): MenuCheckboxNode[] {
  return nodes.map((node) => {
    const effectiveEnabled = ancestorsEnabled && node.enabled
    const children = menuTree(node.children, effectiveEnabled)
    return {
      ...node,
      disabled:
        node.type === 'PAGE'
          ? !effectiveEnabled && !roleMenus.value?.selectedPageIds.includes(node.id)
          : children.every((child) => child.disabled),
      children,
    }
  })
}
onMounted(load)
</script>

<template>
  <div class="content-page">
    <ElForm class="search-form" inline @submit.prevent="search">
      <ElFormItem label="角色">
        <ElInput v-model="keyword" class="search-field" placeholder="请输入编码或名称" clearable />
      </ElFormItem>
      <ElFormItem label="状态">
        <ElSelect v-model="status" class="status-field" placeholder="全部状态" clearable>
          <ElOption label="启用" value="ENABLED" />
          <ElOption label="停用" value="DISABLED" />
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
        <ElButton type="primary" @click="openCreate">新建角色</ElButton>
        <ElButton
          :disabled="!singleSelected || singleSelected.code === 'SUPER_ADMIN' || loading"
          @click="editSelected"
          >编辑</ElButton
        >
      </template>
      <template #tools>
        <ElButton :loading="loading" @click="load">刷新</ElButton>
        <ColumnPicker
          v-model="columns"
          :options="[
            { key: 'code', label: '编码' },
            { key: 'name', label: '名称' },
            { key: 'description', label: '说明' },
            { key: 'status', label: '状态' },
          ]"
        />
      </template>
      <ElTable
        v-loading="loading"
        border
        :data="rows"
        row-key="id"
        style="width: 100%"
        empty-text="暂无角色"
        @selection-change="setSelection"
      >
        <ElTableColumn type="selection" width="48" align="center" />
        <ElTableColumn v-if="columns.includes('code')" prop="code" label="编码" min-width="150" />
        <ElTableColumn v-if="columns.includes('name')" prop="name" label="名称" min-width="150" />
        <ElTableColumn
          v-if="columns.includes('description')"
          prop="description"
          label="说明"
          min-width="170"
          show-overflow-tooltip
        />
        <ElTableColumn v-if="columns.includes('status')" label="状态" width="100"
          ><template #default="{ row }"
            ><ElTag :type="row.status === 'ENABLED' ? 'success' : 'info'" effect="plain">{{
              row.status === 'ENABLED' ? '启用' : '停用'
            }}</ElTag></template
          ></ElTableColumn
        >
        <ElTableColumn
          label="操作"
          min-width="360"
          fixed="right"
          class-name="table-actions-column"
          :resizable="false"
          ><template #default="{ row }"
            ><TableActions>
              <ElButton
                plain
                type="primary"
                :disabled="row.code === 'SUPER_ADMIN'"
                @click="openEdit(asRole(row))"
                >编辑</ElButton
              >
              <ElButton
                plain
                type="primary"
                :disabled="row.code === 'SUPER_ADMIN'"
                @click="openMenus(asRole(row))"
                >配置页面</ElButton
              >
              <ElButton
                plain
                :disabled="row.code === 'SUPER_ADMIN'"
                @click="changeStatus(asRole(row))"
                >{{ row.status === 'ENABLED' ? '停用' : '启用' }}</ElButton
              >
              <ElButton
                plain
                type="danger"
                :disabled="row.code === 'SUPER_ADMIN'"
                @click="remove(asRole(row))"
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
      v-model="dialog"
      :title="editing ? '编辑角色' : '新建角色'"
      width="520px"
      destroy-on-close
    >
      <RequestError :error="error" />
      <ElForm label-width="90px" @submit.prevent="save">
        <ElFormItem label="角色编码" required :error="submitted ? codeError : ''"
          ><ElInput v-model="form.code" :disabled="!!editing" maxlength="64"
        /></ElFormItem>
        <ElFormItem
          label="角色名称"
          required
          :error="submitted && !form.name.trim() ? '请输入角色名称' : ''"
          ><ElInput v-model="form.name" maxlength="64"
        /></ElFormItem>
        <ElFormItem label="说明"
          ><ElInput v-model="form.description" type="textarea" maxlength="255"
        /></ElFormItem>
      </ElForm>
      <template #footer
        ><ElButton @click="dialog = false">取消</ElButton
        ><ElButton type="primary" :loading="saving" @click="save">保存</ElButton></template
      >
    </ElDialog>
    <ElDialog
      v-model="menuDialog"
      :title="`配置页面 · ${menuRole?.name || ''}`"
      width="600px"
      destroy-on-close
    >
      <RequestError :error="error" />
      <ElTree
        v-if="roleMenus"
        ref="treeRef"
        :data="menuTree(roleMenus.tree)"
        node-key="id"
        :props="{ label: 'name', children: 'children', disabled: 'disabled' }"
        show-checkbox
        default-expand-all
      />
      <template #footer
        ><ElButton @click="menuDialog = false">取消</ElButton
        ><ElButton type="primary" :loading="saving" @click="saveMenus">保存</ElButton></template
      >
    </ElDialog>
  </div>
</template>
