<script setup lang="ts">
import { reactive, ref } from 'vue'
import { ElButton, ElDialog, ElForm, ElFormItem, ElInput, ElInputNumber } from 'element-plus'
import * as api from '../../api/camera-groups/api'
import type { CameraGroup, Impact } from '../../api/camera-groups/types'
import RequestError from '../../components/feedback/RequestError.vue'
import GroupSelect from './GroupSelect.vue'
import ImpactSummary from './ImpactSummary.vue'
import { usePageScope } from '../../composables/usePageScope'
import { formError } from '../camera/form'

const props = defineProps<{ group: CameraGroup | null; parentId: string | null }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const captureScope = usePageScope(),
  saving = ref(false),
  error = ref<unknown>(null),
  impact = ref<Impact | null>(null)
const form = reactive({
  name: props.group?.name ?? '',
  parentId: props.group?.parentId ?? props.parentId,
  sortOrder: props.group?.sortOrder ?? 0,
  remark: props.group?.remark ?? '',
})
async function save() {
  if (saving.value) return
  if (!form.name.trim()) {
    error.value = formError('请填写分组名称。')
    return
  }
  const active = captureScope()
  saving.value = true
  error.value = null
  try {
    if (props.group && props.group.parentId !== form.parentId && !impact.value) {
      const result = await api.previewGroupMove(props.group, form.parentId)
      if (active()) impact.value = result
      return
    }
    const input = { ...form, name: form.name.trim(), remark: form.remark.trim() || null }
    if (props.group)
      await api.updateGroup(props.group.groupId, {
        ...input,
        version: props.group.version,
        ...(impact.value ? { confirmation: impact.value.confirmation } : {}),
      })
    else await api.createGroup(input)
    if (active()) emit('saved')
  } catch (cause) {
    if (active()) {
      error.value = cause
      impact.value = null
    }
  } finally {
    if (active()) saving.value = false
  }
}
</script>
<template>
  <ElDialog
    :model-value="true"
    class="management-dialog"
    :title="group ? '编辑分组' : '新建分组'"
    width="600px"
    :close-on-click-modal="!saving"
    :close-on-press-escape="!saving"
    :show-close="!saving"
    @close="emit('close')"
  >
    <RequestError :error="error" /><ImpactSummary v-if="impact" :impact="impact" />
    <ElForm label-width="90px" :disabled="saving || !!impact" @submit.prevent="save">
      <ElFormItem label="分组名称" required
        ><ElInput v-model="form.name" maxlength="64"
      /></ElFormItem>
      <ElFormItem label="上级分组"
        ><GroupSelect
          v-model="form.parentId"
          :exclude-id="group?.groupId"
          clearable
          :disabled="saving || !!impact"
        /><span>留空表示根目录</span></ElFormItem
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
      ><ElButton :disabled="saving" @click="emit('close')">取消</ElButton
      ><ElButton type="primary" :loading="saving" @click="save">{{
        impact ? '确认影响并保存' : '保存'
      }}</ElButton></template
    >
  </ElDialog>
</template>
