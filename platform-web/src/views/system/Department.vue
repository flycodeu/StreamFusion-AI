<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
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
  ElTable,
  ElTableColumn,
  vLoading,
} from 'element-plus'
import * as api from '../../api/departments/api'
import type { Department } from '../../api/departments/types'
import RequestError from '../../components/feedback/RequestError.vue'
import ColumnPicker from '../../components/table/ColumnPicker.vue'
import { useColumns } from '../../composables/table/useColumns'
import { flattenTree, treeIds } from '../../utils/tree'
import TablePanel from '../../components/table/TablePanel.vue'
import TableActions from '../../components/table/TableActions.vue'
import { useTableSelection } from '../../composables/table/useTableSelection'

defineOptions({ name: 'SystemDepartment' })

const columns = useColumns('departments', ['name', 'memberCount', 'sortOrder'])
const rows = ref<Department[]>([])
const allRows = ref<Department[]>([])
const name = ref('')
const loading = ref(false)
const saving = ref(false)
const error = ref<unknown>(null)
const dialog = ref(false)
const editing = ref<Department | null>(null)
const submitted = ref(false)
const form = reactive({ parentId: null as string | null, name: '', sortOrder: 0 })
const completeNodes = computed(
  () => new Map(flattenTree(allRows.value).map((node) => [node.id, node])),
)
const asDepartment = (row: unknown): Department => row as Department
const { singleSelected, setSelection, clearSelection } = useTableSelection<Department>()
let sequence = 0

async function load(): Promise<void> {
  const current = ++sequence
  loading.value = true
  error.value = null
  try {
    const keyword = name.value.trim()
    const [filtered, all] = keyword
      ? await Promise.all([api.getDepartments(keyword), api.getDepartments()])
      : await api.getDepartments().then((result) => [result, result])
    if (current !== sequence) return
    rows.value = filtered
    allRows.value = all
    clearSelection()
  } catch (cause) {
    if (current === sequence) error.value = cause
  } finally {
    if (current === sequence) loading.value = false
  }
}
function resetSearch(): void {
  name.value = ''
  void load()
}
function editSelected(): void {
  if (singleSelected.value) openEdit(singleSelected.value)
}
function openCreate(parent: Department | null = null): void {
  editing.value = null
  submitted.value = false
  error.value = null
  Object.assign(form, { parentId: parent?.id || null, name: '', sortOrder: 0 })
  dialog.value = true
}
function openEdit(node: Department): void {
  editing.value = completeNodes.value.get(node.id) || node
  submitted.value = false
  error.value = null
  Object.assign(form, { parentId: node.parentId, name: node.name, sortOrder: node.sortOrder })
  dialog.value = true
}
async function save(): Promise<void> {
  submitted.value = true
  if (!form.name.trim() || saving.value) return
  saving.value = true
  error.value = null
  try {
    if (editing.value)
      await api.updateDepartment({
        ...editing.value,
        parentId: form.parentId || null,
        name: form.name.trim(),
        sortOrder: form.sortOrder,
      })
    else
      await api.createDepartment({
        parentId: form.parentId || null,
        name: form.name.trim(),
        sortOrder: form.sortOrder,
      })
    dialog.value = false
    await load()
  } catch (cause) {
    error.value = cause
  } finally {
    saving.value = false
  }
}
async function remove(node: Department): Promise<void> {
  try {
    await ElMessageBox.confirm(`删除部门“${node.name}”？`, '确认删除', { type: 'warning' })
    await api.deleteDepartment(node)
    await load()
  } catch (cause) {
    if (cause !== 'cancel' && cause !== 'close') error.value = cause
  }
}
onMounted(load)
</script>

<template>
  <div class="content-page">
    <ElForm class="search-form" inline @submit.prevent="load">
      <ElFormItem label="部门名称">
        <ElInput v-model="name" class="search-field" placeholder="请输入部门名称" clearable />
      </ElFormItem>
      <ElFormItem>
        <ElButton type="primary" native-type="submit" :loading="loading">查询</ElButton>
        <ElButton @click="resetSearch">重置</ElButton>
      </ElFormItem>
    </ElForm>
    <RequestError :error="error" />
    <TablePanel>
      <template #actions>
        <ElButton type="primary" @click="openCreate()">新建部门</ElButton>
        <ElButton :disabled="!singleSelected || loading" @click="editSelected">编辑</ElButton>
      </template>
      <template #tools>
        <ElButton :loading="loading" @click="load">刷新</ElButton>
        <ColumnPicker
          v-model="columns"
          :options="[
            { key: 'name', label: '部门名称' },
            { key: 'memberCount', label: '直属人数' },
            { key: 'sortOrder', label: '排序' },
          ]"
        />
      </template>
      <ElTable
        v-loading="loading"
        border
        :data="rows"
        row-key="id"
        :tree-props="{ children: 'children', checkStrictly: true }"
        style="width: 100%"
        empty-text="暂无部门"
        default-expand-all
        @selection-change="setSelection"
      >
        <ElTableColumn type="selection" width="48" align="center" />
        <ElTableColumn
          v-if="columns.includes('name')"
          prop="name"
          label="部门名称"
          min-width="260"
        />
        <ElTableColumn
          v-if="columns.includes('memberCount')"
          prop="memberCount"
          label="直属人数"
          min-width="160"
        />
        <ElTableColumn
          v-if="columns.includes('sortOrder')"
          prop="sortOrder"
          label="排序"
          min-width="120"
        />
        <ElTableColumn
          label="操作"
          min-width="264"
          fixed="right"
          class-name="table-actions-column"
          :resizable="false"
          ><template #default="{ row }"
            ><TableActions>
              <ElButton plain type="primary" @click="openEdit(asDepartment(row))">编辑</ElButton>
              <ElButton plain type="primary" @click="openCreate(asDepartment(row))"
                >新增下级</ElButton
              ><ElButton
                plain
                class="table-action-end"
                type="danger"
                :disabled="
                  (completeNodes.get(row.id)?.children.length ?? row.children.length) > 0 ||
                  row.memberCount > 0
                "
                @click="remove(asDepartment(row))"
                >删除</ElButton
              >
            </TableActions></template
          ></ElTableColumn
        >
      </ElTable>
    </TablePanel>
    <ElDialog
      v-model="dialog"
      :title="editing ? '编辑部门' : '新建部门'"
      width="520px"
      destroy-on-close
    >
      <RequestError :error="error" />
      <ElForm label-width="90px" @submit.prevent="save">
        <ElFormItem
          label="部门名称"
          required
          :error="submitted && !form.name.trim() ? '请输入部门名称' : ''"
          ><ElInput v-model="form.name" maxlength="64"
        /></ElFormItem>
        <ElFormItem label="上级部门"
          ><ElSelect v-model="form.parentId" clearable placeholder="根部门"
            ><ElOption
              v-for="node in flattenTree(allRows).filter(
                (item) => !editing || !treeIds(editing).has(item.id),
              )"
              :key="node.id"
              :label="node.name"
              :value="node.id" /></ElSelect
        ></ElFormItem>
        <ElFormItem label="排序"
          ><ElInputNumber
            v-model="form.sortOrder"
            :min="0"
            :max="10000"
            :precision="0"
            controls-position="right"
        /></ElFormItem>
      </ElForm>
      <template #footer
        ><ElButton @click="dialog = false">取消</ElButton
        ><ElButton type="primary" :loading="saving" @click="save">保存</ElButton></template
      >
    </ElDialog>
  </div>
</template>
