<script setup lang="ts">
import { menuTypeLabels, menuTypeLabel } from '../../features/menu/presentation'
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  ElButton,
  ElDialog,
  ElForm,
  ElFormItem,
  ElInput,
  ElInputNumber,
  ElMessageBox,
  ElOption,
  ElSelect,
  ElSwitch,
  ElTable,
  ElTableColumn,
  ElTag,
  ElTreeSelect,
  vLoading,
} from 'element-plus'
import * as api from '../../api/menus/api'
import type { MenuNode } from '../../api/menus/types'
import RequestError from '../../components/feedback/RequestError.vue'
import ColumnPicker from '../../components/table/ColumnPicker.vue'
import TablePanel from '../../components/table/TablePanel.vue'
import TableActions from '../../components/table/TableActions.vue'
import { useColumns } from '../../composables/table/useColumns'
import { useTableSelection } from '../../composables/table/useTableSelection'
import { editMenuForm, menuParentOptions, menuWrite, newMenuForm } from '../../features/menu/form'
import IconPicker from '../../components/icons/IconPicker.vue'
import { availablePagePaths, normalizeViewPath, resolvePage } from '../../router/dynamic/views'
import { validMenuPath } from '../../router/dynamic/routes'
import { refreshIdentity } from '../../session/session'
import { flattenTree } from '../../utils/tree'
import { usePageScope } from '../../composables/usePageScope'

defineOptions({ name: 'SystemMenu' })

const router = useRouter()
const route = useRoute()
const columns = useColumns('menus', [
  'name',
  'type',
  'key',
  'path',
  'enabled',
  'visible',
  'sortOrder',
])
const { singleSelected, setSelection, clearSelection } = useTableSelection<MenuNode>()
const rows = ref<MenuNode[]>([])
const parentRows = ref<MenuNode[]>([])
const tableRevision = ref(0)
const appliedQuery = ref<Parameters<typeof api.getMenus>[0]>({})
const name = ref('')
const type = ref('')
const enabled = ref<boolean | null>(null)
const loading = ref(false)
const saving = ref(false)
const deleting = ref(false)
const captureScope = usePageScope()
const error = ref<unknown>(null)
const dialog = ref(false)
const editing = ref<MenuNode | null>(null)
const submitted = ref(false)
const form = reactive(newMenuForm())
const asMenu = (row: unknown): MenuNode => row as MenuNode
const parentOptions = computed(() =>
  menuParentOptions(
    parentRows.value,
    editing.value
      ? (flattenTree(parentRows.value).find((node) => node.id === editing.value?.id) ??
          editing.value)
      : null,
  ),
)
const pathError = computed(() => {
  if (form.type !== 'PAGE') return ''
  const payload = menuWrite(form, editing.value)
  if (!validMenuPath(payload.path || '')) return '请输入以 / 开头的页面路径，不能占用系统固定路径'
  const component = payload.componentKey || payload.path || ''
  const normalized = normalizeViewPath(component)
  if (!normalized || normalized.length > 64) return '页面文件路径最多 64 个字符，省略 .vue'
  if (!resolvePage(component)) return '未找到页面文件，请核对 views 下的路径'
  return ''
})
const keyError = computed(() =>
  form.type === 'PAGE' && !/^[A-Za-z][A-Za-z0-9_:-]{0,63}$/.test(form.routeName?.trim() || '')
    ? '以字母开头，支持字母、数字、下划线、冒号和短横线'
    : '',
)
let sequence = 0
let editorSequence = 0
let failedChild: { id: string; cause: unknown } | null = null
const editorLoading = ref(false)

async function load(): Promise<void> {
  const current = ++sequence
  const inScope = captureScope()
  loading.value = true
  error.value = null
  failedChild = null
  clearSelection()
  try {
    const requested = query()
    const result = await api.getMenus({ ...requested, lazy: true })
    if (current !== sequence || !inScope()) return
    rows.value = result
    appliedQuery.value = requested
    tableRevision.value++
  } catch (cause) {
    if (current === sequence && inScope()) error.value = cause
  } finally {
    if (current === sequence && inScope()) loading.value = false
  }
}
function query() {
  return {
    name: name.value.trim() || undefined,
    type: type.value || undefined,
    enabled: typeof enabled.value === 'boolean' ? enabled.value : undefined,
  }
}
async function loadChildren(
  row: MenuNode,
  treeNode: { loading?: boolean },
  resolve: (data: MenuNode[]) => void,
): Promise<void> {
  const current = sequence
  const inScope = captureScope()
  try {
    const children = await api.getMenus({ ...appliedQuery.value, lazy: true, parentId: row.id })
    if (current === sequence && inScope()) {
      resolve(children)
      if (failedChild?.id === row.id && error.value === failedChild.cause) {
        error.value = null
        failedChild = null
      }
    }
  } catch (cause) {
    if (current === sequence && inScope()) {
      failedChild = { id: row.id, cause }
      error.value = cause
    }
    // Keep this node retryable; resolving [] would permanently cache a failed load as empty.
  } finally {
    treeNode.loading = false
  }
}
function reset(): void {
  name.value = ''
  type.value = ''
  enabled.value = null
  void load()
}
async function openEditor(node: MenuNode | null, parent: MenuNode | null = null): Promise<void> {
  if (parent?.type === 'PAGE' || saving.value) return
  const current = ++editorSequence
  const inScope = captureScope()
  editorLoading.value = true
  error.value = null
  try {
    const directories = await api.getMenus({ type: 'DIRECTORY' })
    if (current !== editorSequence || !inScope()) return
    parentRows.value = directories
    editing.value = node
    submitted.value = false
    Object.assign(form, node ? editMenuForm(node) : newMenuForm(parent?.id ?? null))
    dialog.value = true
  } catch (cause) {
    if (current === editorSequence && inScope()) error.value = cause
  } finally {
    if (current === editorSequence && inScope()) editorLoading.value = false
  }
}
const blank = (parent: MenuNode | null = null) => openEditor(null, parent)
const openEdit = (node: MenuNode) => openEditor(node)
async function refreshNavigation(): Promise<void> {
  await refreshIdentity()
  // Re-enter the guard so a renamed, disabled or removed current menu cannot remain mounted.
  await router.replace({ path: route.path, query: route.query, hash: route.hash, force: true })
}
async function save(): Promise<void> {
  submitted.value = true
  if (!form.name.trim() || pathError.value || keyError.value || saving.value) return
  saving.value = true
  const inScope = captureScope()
  error.value = null
  try {
    const payload = menuWrite(form, editing.value)
    if (editing.value) await api.updateMenu(editing.value.id, editing.value.version, payload)
    else await api.createMenu(payload)
    if (!inScope()) return
    dialog.value = false
    await load()
    if (inScope()) await refreshNavigation()
  } catch (cause) {
    if (inScope()) error.value = cause
  } finally {
    saving.value = false
  }
}
async function remove(node: MenuNode): Promise<void> {
  if (deleting.value) return
  const inScope = captureScope()
  deleting.value = true
  try {
    await ElMessageBox.confirm(`删除菜单“${node.name}”？`, '确认删除', {
      type: 'warning',
      confirmButtonClass: 'el-button--danger',
      confirmButtonText: '确认删除',
      cancelButtonText: '取消',
    })
    if (!inScope()) return
    await api.deleteMenu(node)
    if (!inScope()) return
    await load()
    if (inScope()) await refreshNavigation()
  } catch (cause) {
    if (inScope() && cause !== 'cancel' && cause !== 'close') error.value = cause
  } finally {
    deleting.value = false
  }
}
onMounted(load)
</script>

<template>
  <div class="content-page">
    <ElForm inline class="search-form" @submit.prevent="load">
      <ElFormItem label="菜单名称"
        ><ElInput v-model="name" class="search-field" placeholder="请输入菜单名称" clearable
      /></ElFormItem>
      <ElFormItem label="菜单类型"
        ><ElSelect v-model="type" class="status-field" placeholder="全部" clearable
          ><ElOption
            v-for="(label, value) in menuTypeLabels"
            :key="value"
            :label="label"
            :value="value" /></ElSelect
      ></ElFormItem>
      <ElFormItem label="状态"
        ><ElSelect v-model="enabled" class="status-field" placeholder="全部" clearable
          ><ElOption label="启用" :value="true" /><ElOption label="停用" :value="false" /></ElSelect
      ></ElFormItem>
      <ElFormItem
        ><ElButton type="primary" native-type="submit" :loading="loading">查询</ElButton
        ><ElButton @click="reset">重置</ElButton></ElFormItem
      >
    </ElForm>
    <RequestError :error="error" />
    <TablePanel :initial-loading="loading && !rows.length">
      <template #actions
        ><ElButton type="primary" :disabled="editorLoading" @click="blank()">新建菜单</ElButton
        ><ElButton
          :disabled="!singleSelected || editorLoading"
          @click="singleSelected && openEdit(singleSelected)"
          >编辑</ElButton
        ></template
      >
      <template #tools
        ><ElButton :loading="loading" @click="load">刷新</ElButton
        ><ColumnPicker
          v-model="columns"
          :options="[
            { key: 'name', label: '菜单名称' },
            { key: 'type', label: '类型' },
            { key: 'key', label: '唯一标识' },
            { key: 'path', label: '页面路径' },
            { key: 'enabled', label: '状态' },
            { key: 'visible', label: '导航显示' },
            { key: 'sortOrder', label: '排序' },
          ]"
      /></template>
      <ElTable
        v-loading="loading"
        border
        :key="tableRevision"
        :data="rows"
        row-key="id"
        :lazy="true"
        :load="loadChildren"
        :tree-props="{ children: 'children', hasChildren: 'hasChildren', checkStrictly: true }"
        style="width: 100%"
        empty-text="暂无菜单"
        @selection-change="setSelection"
      >
        <ElTableColumn type="selection" width="48" align="center" />
        <ElTableColumn
          v-if="columns.includes('name')"
          prop="name"
          label="菜单名称"
          min-width="190"
          show-overflow-tooltip
        />
        <ElTableColumn v-if="columns.includes('type')" label="类型" width="80"
          ><template #default="{ row }">{{ menuTypeLabel(row.type) }}</template></ElTableColumn
        >
        <ElTableColumn
          v-if="columns.includes('key')"
          label="唯一标识"
          min-width="130"
          show-overflow-tooltip
          ><template #default="{ row }">{{ row.page?.routeName || '—' }}</template></ElTableColumn
        >
        <ElTableColumn
          v-if="columns.includes('path')"
          label="页面路径"
          min-width="180"
          show-overflow-tooltip
          ><template #default="{ row }">{{ row.page?.path || '—' }}</template></ElTableColumn
        >
        <ElTableColumn v-if="columns.includes('enabled')" label="状态" width="80"
          ><template #default="{ row }"
            ><ElTag :type="row.enabled ? 'success' : 'info'" size="small">{{
              row.enabled ? '启用' : '停用'
            }}</ElTag></template
          ></ElTableColumn
        >
        <ElTableColumn v-if="columns.includes('visible')" label="导航显示" width="90"
          ><template #default="{ row }">{{
            row.visible ? '显示' : '隐藏'
          }}</template></ElTableColumn
        >
        <ElTableColumn
          v-if="columns.includes('sortOrder')"
          prop="sortOrder"
          label="排序"
          width="70"
        />
        <ElTableColumn
          label="操作"
          min-width="190"
          fixed="right"
          class-name="table-actions-column"
          :resizable="false"
          ><template #default="{ row }"
            ><TableActions compact>
              <ElButton link type="primary" :disabled="editorLoading" @click="openEdit(asMenu(row))"
                >编辑</ElButton
              >
              <ElButton
                v-if="row.type === 'DIRECTORY'"
                link
                type="primary"
                @click="blank(asMenu(row))"
                >新增下级</ElButton
              ><ElButton
                link
                class="table-action-end"
                type="danger"
                :disabled="deleting || row.hasChildren || row.children.length > 0"
                @click="remove(asMenu(row))"
                >删除</ElButton
              >
            </TableActions></template
          ></ElTableColumn
        >
      </ElTable>
    </TablePanel>
    <ElDialog
      v-model="dialog"
      :title="editing ? '编辑菜单' : '新建菜单'"
      width="640px"
      class="management-dialog"
      align-center
      destroy-on-close
      :close-on-click-modal="!saving"
      :close-on-press-escape="!saving"
      :show-close="!saving"
    >
      <RequestError :error="error" />
      <ElForm class="menu-form-grid" label-position="top" :disabled="saving" @submit.prevent="save">
        <ElFormItem
          label="菜单名称"
          required
          :error="submitted && !form.name.trim() ? '请输入菜单名称' : ''"
          ><ElInput v-model="form.name" maxlength="64"
        /></ElFormItem>
        <ElFormItem label="菜单类型"
          ><ElSelect v-model="form.type" :disabled="!!editing"
            ><ElOption
              v-for="(label, value) in menuTypeLabels"
              :key="value"
              :label="label"
              :value="value" /></ElSelect
        ></ElFormItem>
        <ElFormItem class="menu-form-full" label="上级目录">
          <ElTreeSelect
            v-model="form.parentId"
            :data="parentOptions"
            :props="{ label: 'name', children: 'children' }"
            node-key="id"
            check-strictly
            default-expand-all
            clearable
            filterable
            placeholder="根目录"
          />
        </ElFormItem>
        <template v-if="form.type === 'PAGE'">
          <ElFormItem label="唯一标识" required :error="submitted ? keyError : ''"
            ><ElInput v-model="form.routeName" placeholder="例如 camera" maxlength="64"
          /></ElFormItem>
          <ElFormItem label="页面路径" required :error="submitted ? pathError : ''"
            ><ElSelect
              v-model="form.path"
              filterable
              allow-create
              default-first-option
              placeholder="例如 /camera/manage"
              ><ElOption
                v-for="path in availablePagePaths"
                :key="path"
                :label="path"
                :value="path" /></ElSelect
          ></ElFormItem>
        </template>
        <ElFormItem label="图标"><IconPicker v-model="form.icon" /></ElFormItem>
        <ElFormItem label="排序"
          ><ElInputNumber v-model="form.sortOrder" :min="0" :max="10000" :precision="0"
        /></ElFormItem>
        <ElFormItem label="导航显示"><ElSwitch v-model="form.visible" /></ElFormItem>
        <ElFormItem label="启用"><ElSwitch v-model="form.enabled" /></ElFormItem>
      </ElForm>
      <template #footer
        ><ElButton :disabled="saving" @click="dialog = false">取消</ElButton
        ><ElButton type="primary" :loading="saving" @click="save">保存</ElButton></template
      >
    </ElDialog>
  </div>
</template>

<style scoped>
.menu-form-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0 20px;
}
.menu-form-full {
  grid-column: 1 / -1;
}
@media (max-width: 640px) {
  .menu-form-grid {
    grid-template-columns: minmax(0, 1fr);
    gap: 0;
  }
}
</style>
