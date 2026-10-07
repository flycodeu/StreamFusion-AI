<script setup lang="ts">
import { ElFormItem, ElInput, ElInputNumber, ElOption, ElSelect } from 'element-plus'
import type { ManualCameraDraft } from './manual'
import type { AccessAdapter } from '../../api/camera-access/types'
defineProps<{ hideHost?: boolean; adapters?: AccessAdapter[] }>()
const model = defineModel<ManualCameraDraft>({ required: true })
</script>
<template>
  <ElFormItem label="连接方式">
    <ElSelect v-model="model.adapterType">
      <ElOption label="暂不指定" value="" /><ElOption
        v-for="adapter in adapters?.filter((item) => item.category === 'DEVICE')"
        :key="adapter.type"
        :label="adapter.label"
        :value="adapter.type"
      />
    </ElSelect>
  </ElFormItem>
  <template v-if="!hideHost">
    <ElFormItem label="设备地址" required
      ><ElInput v-model="model.host" maxlength="253" placeholder="IP 或域名"
    /></ElFormItem>
    <ElFormItem label="管理端口" required
      ><ElInputNumber v-model="model.port" :min="1" :max="65535"
    /></ElFormItem>
  </template>
  <ElFormItem label="设备账号"
    ><ElInput v-model="model.username" autocomplete="off" maxlength="128" placeholder="可选"
  /></ElFormItem>
  <ElFormItem label="设备密码"
    ><ElInput
      v-model="model.password"
      type="password"
      show-password
      autocomplete="new-password"
      maxlength="512"
  /></ElFormItem>
  <details class="connection-more">
    <summary>其他连接资料</summary>
    <ElFormItem label="连接协议"
      ><ElSelect v-model="model.scheme"
        ><ElOption label="HTTP" value="http" /><ElOption label="HTTPS" value="https" /></ElSelect
    ></ElFormItem>
    <ElFormItem label="品牌备注"
      ><ElInput v-model="model.vendorHint" maxlength="100" placeholder="人工填写的品牌"
    /></ElFormItem>
  </details>
</template>
<style scoped>
.connection-more {
  margin: 8px 0 16px;
}
.connection-more summary {
  cursor: pointer;
  color: var(--el-text-color-secondary);
  margin-bottom: 14px;
}
</style>
