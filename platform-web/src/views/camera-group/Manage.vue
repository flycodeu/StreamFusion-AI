<script setup lang="ts">
import { formError as validationError } from '../../features/camera/form'
import { onMounted, reactive, ref } from 'vue'
import {
  ElButton,
  ElDialog,
  ElForm,
  ElFormItem,
  ElInput,
  ElInputNumber,
  ElMessageBox,
  ElPagination,
  ElTable,
  ElTableColumn,
  vLoading,
} from 'element-plus'
import * as api from '../../api/camera-groups/api'
import { getCameraOptions } from '../../api/camera/api'
import type { CameraGroup, Impact } from '../../api/camera-groups/types'
import RequestError from '../../components/feedback/RequestError.vue'
import TableActions from '../../components/table/TableActions.vue'
import TablePanel from '../../components/table/TablePanel.vue'
import GroupSelect from '../../features/camera-groups/GroupSelect.vue'
import ImpactSummary from '../../features/camera-groups/ImpactSummary.vue'
import { usePageScope } from '../../composables/usePageScope'

defineOptions({ name: 'CameraGroupManage' })
const captureScope = usePageScope(),
  rows = ref<CameraGroup[]>([]),
  parent = ref<CameraGroup | null>(null)
const ancestors = ref<CameraGroup[]>([]),
  page = ref(1),
  total = ref(0),
  name = ref('')
const loading = ref(false),
  saving = ref(false),
  canManage = ref(false),
  error = ref<unknown>(null),
  formError = ref<unknown>(null)
const dialog = ref(false),
  editing = ref<CameraGroup | null>(null),
  impact = ref<Impact | null>(null)
const form = reactive({ name: '', parentId: null as string | null, sortOrder: 0, remark: '' })
let loadSequence = 0,
  detailSequence = 0
const asGroup = (value: unknown) => value as CameraGroup
async function load() {
  const sequence = ++loadSequence,
    active = captureScope()
  loading.value = true
  error.value = null
  try {
    const [result, options] = await Promise.all([
      api.getGroups({
        parentId: parent.value?.groupId,
        name: name.value.trim() || undefined,
        page: page.value,
        size: 20,
      }),
      getCameraOptions(),
    ])
    if (!active() || sequence !== loadSequence) return
    rows.value = result.items
    total.value = result.total
    canManage.value = options.canManageShared
  } catch (cause) {
    if (active() && sequence === loadSequence) error.value = cause
  } finally {
    if (active() && sequence === loadSequence) loading.value = false
  }
}
function resetSearch() {
  name.value = ''
  search()
}
function search() {
  page.value = 1
  void load()
}
function enter(group: CameraGroup) {
  if (parent.value) ancestors.value.push(parent.value)
  parent.value = group
  name.value = ''
  search()
}
function up() {
  parent.value = ancestors.value.pop() ?? null
  name.value = ''
  search()
}
function openCreate() {
  detailSequence++
  editing.value = null
  formError.value = null
  impact.value = null
  Object.assign(form, {
    name: '',
    parentId: parent.value?.groupId ?? null,
    sortOrder: 0,
    remark: '',
  })
  dialog.value = true
}
async function openEdit(group: CameraGroup) {
  const sequence = ++detailSequence,
    active = captureScope()
  error.value = null
  try {
    const result = await api.getGroup(group.groupId)
    if (!active() || sequence !== detailSequence) return
    editing.value = result
    impact.value = null
    formError.value = null
    Object.assign(form, {
      name: result.name,
      parentId: result.parentId,
      sortOrder: result.sortOrder,
      remark: result.remark ?? '',
    })
    dialog.value = true
  } catch (cause) {
    if (active() && sequence === detailSequence) error.value = cause
  }
}
async function save() {
  if (saving.value || !canManage.value) return
  if (!form.name.trim()) {
    formError.value = validationError('请填写分组名称。')
    return
  }
  const active = captureScope()
  saving.value = true
  formError.value = null
  try {
    if (editing.value && editing.value.parentId !== form.parentId && !impact.value) {
      const result = await api.previewGroupMove(editing.value, form.parentId)
      if (active()) impact.value = result
      return
    }
    const input = {
      name: form.name.trim(),
      parentId: form.parentId,
      sortOrder: form.sortOrder,
      remark: form.remark.trim() || null,
    }
    if (editing.value)
      await api.updateGroup(editing.value.groupId, {
        ...input,
        version: editing.value.version,
        ...(impact.value ? { confirmation: impact.value.confirmation } : {}),
      })
    else await api.createGroup(input)
    if (!active()) return
    dialog.value = false
    await load()
  } catch (cause) {
    if (active()) {
      formError.value = cause
      impact.value = null
    }
  } finally {
    saving.value = false
  }
}
async function remove(group: CameraGroup) {
  if (saving.value) return
  const active = captureScope()
  saving.value = true
  try {
    await ElMessageBox.confirm(
      `删除分组“${group.name}”？有子组、相机或授权引用时不能删除。`,
      '确认删除',
      { type: 'warning' },
    )
    if (!active()) return
    await api.deleteGroup(group)
    if (active()) await load()
  } catch (cause) {
    if (active() && cause !== 'cancel' && cause !== 'close') error.value = cause
  } finally {
    saving.value = false
  }
}
onMounted(load)
</script>

<template>
  <div class="content-page">
    <ElForm class="search-form" inline @submit.prevent="search"
      ><ElFormItem label="分组名称"
        ><ElInput
          v-model="name"
          class="search-field"
          placeholder="搜索分组名称"
          clearable
          maxlength="64" /></ElFormItem
      ><ElFormItem
        ><ElButton native-type="submit" type="primary" :loading="loading">查询</ElButton
        ><ElButton @click="resetSearch">重置</ElButton></ElFormItem
      ></ElForm
    >
    <RequestError :error="error" />
    <nav class="group-location" aria-label="分组浏览位置">
      <div>
        <span class="group-location-label">当前分组</span>
        <strong>{{ parent?.name || '根目录' }}</strong>
      </div>
      <ElButton v-if="parent" @click="up">返回</ElButton>
    </nav>
    <TablePanel>
      <template #actions
        ><ElButton v-if="canManage" type="primary" :disabled="saving" @click="openCreate"
          >新建分组</ElButton
        ></template
      >
      <template #tools><ElButton :loading="loading" @click="load">刷新</ElButton></template>
      <ElTable
        v-loading="loading"
        :data="rows"
        row-key="groupId"
        border
        empty-text="当前层级暂无分组"
      >
        <ElTableColumn prop="name" label="分组名称" min-width="240" show-overflow-tooltip />
        <ElTableColumn prop="visibleCameraCount" label="可见相机数" min-width="140" />
        <ElTableColumn prop="sortOrder" label="排序" min-width="100" />
        <ElTableColumn
          label="操作"
          width="220"
          fixed="right"
          class-name="table-actions-column"
          :resizable="false"
          ><template #default="{ row }"
            ><TableActions compact
              ><ElButton link type="primary" @click="enter(asGroup(row))">查看下级</ElButton
              ><ElButton
                v-if="canManage"
                link
                type="primary"
                :disabled="saving"
                @click="openEdit(asGroup(row))"
                >编辑</ElButton
              ><ElButton
                v-if="canManage"
                link
                type="danger"
                :disabled="saving"
                @click="remove(asGroup(row))"
                >删除</ElButton
              ></TableActions
            ></template
          ></ElTableColumn
        >
      </ElTable>
      <template #footer
        ><ElPagination
          v-model:current-page="page"
          :page-size="20"
          :total="total"
          layout="total, prev, pager, next"
          @current-change="load"
      /></template>
    </TablePanel>
    <ElDialog
      class="management-dialog"
      v-model="dialog"
      :title="editing ? '编辑视频分组' : '新建视频分组'"
      width="600px"
      destroy-on-close
      :close-on-click-modal="!saving"
      :close-on-press-escape="!saving"
      :show-close="!saving"
    >
      <RequestError :error="formError" /><ImpactSummary v-if="impact" :impact="impact" />
      <ElForm label-width="100px" :disabled="saving || !!impact" @submit.prevent="save">
        <ElFormItem label="分组名称" required
          ><ElInput v-model="form.name" maxlength="64"
        /></ElFormItem>
        <ElFormItem label="上级分组"
          ><GroupSelect v-model="form.parentId" :exclude-id="editing?.groupId" clearable /><span
            >留空表示根目录</span
          ></ElFormItem
        >
        <ElFormItem label="排序"
          ><ElInputNumber v-model="form.sortOrder" :min="0" :max="2147483647"
        /></ElFormItem>
        <ElFormItem label="备注"
          ><ElInput v-model="form.remark" type="textarea" maxlength="500"
        /></ElFormItem>
      </ElForm>
      <template #footer
        ><ElButton v-if="impact" :disabled="saving" @click="impact = null">重新编辑</ElButton
        ><ElButton :disabled="saving" @click="dialog = false">取消</ElButton
        ><ElButton type="primary" :loading="saving" @click="save">{{
          impact ? '确认影响并保存' : '保存'
        }}</ElButton></template
      >
    </ElDialog>
  </div>
</template>
<style scoped>
.group-location {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 16px 20px;
  margin-bottom: 16px;
  border: 1px solid var(--border-subtle);
  border-radius: 4px;
  background: var(--el-bg-color);
}
.group-location > div {
  min-width: 0;
  overflow-wrap: anywhere;
}
.group-location-label {
  margin-right: 12px;
  font-size: 13px;
  color: var(--text-secondary);
}
.group-location strong {
  font-size: 15px;
}
.group-location > .el-button {
  flex-shrink: 0;
}
</style>
