<script setup lang="ts">
import { computed } from 'vue'
import { ElOption, ElSelect } from 'element-plus'
import type { AccessAdapter } from '../../api/camera-access/types'
import { brandOptions } from './form'

const props = defineProps<{ adapters?: AccessAdapter[]; placeholder?: string }>()
const model = defineModel<string>({ required: true })
const options = computed(() => brandOptions(props.adapters ?? []))
</script>
<template>
  <ElSelect
    v-model="model"
    filterable
    allow-create
    default-first-option
    clearable
    value-on-clear=""
    :reserve-keyword="false"
    :placeholder="placeholder || '选择或输入品牌'"
  >
    <ElOption v-for="brand in options" :key="brand" :value="brand" :label="brand" />
  </ElSelect>
</template>
