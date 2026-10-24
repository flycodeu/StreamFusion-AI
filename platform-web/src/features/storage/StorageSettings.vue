<script setup lang="ts">
import { reactive, ref } from 'vue'
import {
  ElButton,
  ElTable,
  ElTableColumn,
  ElDescriptions,
  ElDescriptionsItem,
  ElDialog,
  ElForm,
  ElFormItem,
  ElInput,
  ElMessageBox,
} from 'element-plus'
import * as api from '../../api/storage/api'
import type { StorageInstance, StorageScope } from '../../api/storage/types'
import RequestError from '../../components/feedback/RequestError.vue'
import StorageHistoryPolicy from './StorageHistoryPolicy.vue'
const props = defineProps<{ instances: StorageInstance[]; instance: StorageInstance | null }>()
const emit = defineEmits<{
  edit: [row: StorageInstance | null]
  select: [id: string]
  updated: []
}>()
const policyOpen = ref(false),
  globalPolicyOpen = ref(false)
const error = ref<unknown>(null),
  adding = ref(false),
  busy = ref(false)
const form = reactive({ bucket: '', prefix: '', displayName: '', probePrefix: '' })
function add() {
  Object.assign(form, { bucket: '', prefix: '', displayName: '', probePrefix: '' })
  error.value = null
  adding.value = true
}
async function saveDirectory() {
  if (!props.instance || busy.value) return
  busy.value = true
  error.value = null
  try {
    await api.addScope(props.instance.instanceId, {
      ...form,
      instanceVersion: props.instance.version,
      idempotencyKey: crypto.randomUUID(),
    })
    adding.value = false
    emit('updated')
  } catch (e) {
    error.value = e
  } finally {
    busy.value = false
  }
}
async function removeDirectory(row: StorageScope) {
  try {
    await ElMessageBox.confirm(
      `移除存储入口“${row.displayName || row.bucket}”？此操作保留远端文件。`,
      '移除存储入口',
      {
        type: 'warning',
        confirmButtonClass: 'el-button--danger',
        confirmButtonText: '移除登记',
        cancelButtonText: '取消',
      },
    )
    await api.removeScope(row.scopeId, row.version)
    emit('updated')
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') error.value = e
  }
}
async function removeInstance(row: StorageInstance) {
  try {
    await ElMessageBox.confirm(
      `移除“${row.name}”的平台登记？此操作保留 SeaweedFS 服务和文件。`,
      '移除实例',
      {
        type: 'warning',
        confirmButtonClass: 'el-button--danger',
        confirmButtonText: '移除登记',
        cancelButtonText: '取消',
      },
    )
    await api.removeInstance(row.instanceId, row.version)
    emit('updated')
  } catch (e) {
    if (e !== 'cancel' && e !== 'close') error.value = e
  }
}
const asInstance = (row: unknown) => row as StorageInstance
const asScope = (row: unknown) => row as StorageScope
</script>
<template>
  <section class="settings-page">
    <section class="settings-card">
      <div class="section-heading">
        <h2>存储实例</h2>
        <ElButton type="primary" @click="emit('edit', null)">新增实例</ElButton>
      </div>
      <RequestError v-if="error && !adding" :error="error" />
      <ElTable :data="instances" row-key="instanceId" empty-text="尚未配置存储实例"
        ><ElTableColumn label="名称" min-width="170"
          ><template #default="{ row }"
            ><ElButton
              link
              :type="row.instanceId === instance?.instanceId ? 'primary' : undefined"
              @click="emit('select', row.instanceId)"
              >{{ row.name }}</ElButton
            ></template
          ></ElTableColumn
        ><ElTableColumn
          prop="endpoint"
          label="S3 地址"
          min-width="210"
          show-overflow-tooltip
        /><ElTableColumn label="操作" width="180"
          ><template #default="{ row }"
            ><ElButton link type="primary" @click="emit('edit', asInstance(row))">编辑</ElButton
            ><ElButton link type="danger" @click="removeInstance(asInstance(row))"
              >移除登记</ElButton
            ></template
          ></ElTableColumn
        ></ElTable
      >
      <template v-if="instance">
        <div class="section-heading connection-heading">
          <h3>{{ instance.name }}</h3>
          <ElButton @click="emit('edit', instance)">编辑接入</ElButton>
        </div>
        <ElDescriptions :column="1"
          ><ElDescriptionsItem label="S3 地址">{{ instance.endpoint }}</ElDescriptionsItem
          ><ElDescriptionsItem label="签名区域">{{ instance.region }}</ElDescriptionsItem
          ><ElDescriptionsItem label="访问密钥">{{
            instance.credentialConfigured ? '已配置' : '未配置'
          }}</ElDescriptionsItem
          ><ElDescriptionsItem label="原生管理"
            ><a
              v-if="instance.managementUrl"
              :href="instance.managementUrl"
              target="_blank"
              rel="noopener noreferrer"
              >打开 SeaweedFS 管理 ↗</a
            ><span v-else>未配置</span></ElDescriptionsItem
          ></ElDescriptions
        >
      </template>
    </section>
    <section v-if="instance" class="settings-card">
      <div class="section-heading directory-heading">
        <h3>存储目录</h3>
        <ElButton type="primary" @click="add">添加目录入口</ElButton>
      </div>
      <ElTable :data="instance.scopes" row-key="scopeId" empty-text="尚未配置存储目录"
        ><ElTableColumn label="名称" min-width="150"
          ><template #default="{ row }">{{
            row.displayName || row.bucket
          }}</template></ElTableColumn
        ><ElTableColumn prop="bucket" label="Bucket" min-width="170" /><ElTableColumn
          label="根目录"
          min-width="160"
          ><template #default="{ row }">{{ row.prefix || '/' }}</template></ElTableColumn
        ><ElTableColumn label="操作" width="130"
          ><template #default="{ row }"
            ><ElButton link type="danger" @click="removeDirectory(asScope(row))"
              >移除登记</ElButton
            ></template
          ></ElTableColumn
        ></ElTable
      >
    </section>
    <section v-if="instance" class="settings-card policy-card">
      <div>
        <h3>记录清理</h3>
        <span class="secondary">操作日志与文件操作元数据</span>
      </div>
      <div class="policy-actions">
        <ElButton @click="globalPolicyOpen = true">全局策略</ElButton
        ><ElButton type="primary" @click="policyOpen = true">当前图床策略</ElButton>
      </div>
    </section>
    <StorageHistoryPolicy v-model="globalPolicyOpen" />
    <StorageHistoryPolicy v-if="instance" v-model="policyOpen" :instance-id="instance.instanceId" />
    <ElDialog
      v-model="adding"
      class="management-dialog"
      title="添加存储目录"
      width="min(540px,94vw)"
      align-center
      :close-on-click-modal="false"
      :show-close="!busy"
      :close-on-press-escape="!busy"
      ><RequestError v-if="error" :error="error" /><ElForm
        label-position="top"
        :disabled="busy"
        @submit.prevent="saveDirectory"
        ><ElFormItem label="显示名称"
          ><ElInput v-model="form.displayName" maxlength="64" /></ElFormItem
        ><ElFormItem label="已有 Bucket" required
          ><ElInput v-model="form.bucket" maxlength="63" /></ElFormItem
        ><ElFormItem label="根目录"
          ><ElInput
            v-model="form.prefix"
            placeholder="留空为整个 Bucket，子目录以 / 结尾"
            maxlength="1024" /></ElFormItem></ElForm
      ><template #footer
        ><ElButton :disabled="busy" @click="adding = false">取消</ElButton
        ><ElButton type="primary" :loading="busy" @click="saveDirectory">保存</ElButton></template
      ></ElDialog
    >
  </section>
</template>
<style scoped>
.settings-page {
  padding: 20px 24px;
  display: grid;
  gap: 16px;
  background: #f8fafc;
  border-radius: 12px;
}
.section-heading {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  align-items: center;
  flex-wrap: wrap;
  margin-bottom: 14px;
}
h2 {
  margin: 0;
  font-size: 17px;
  font-weight: 700;
  color: #0f172a;
}
h3 {
  margin: 0;
  font-size: 15px;
  font-weight: 700;
  color: #0f172a;
}
.connection-heading {
  margin-top: 28px;
}
.directory-heading {
  margin-top: 0;
}
.settings-card {
  padding: 20px 22px;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #ffffff;
  min-width: 0;
}
.settings-card :deep(.el-table__header .el-table__cell) {
  background: #f8fafc;
  color: #475569;
  font-weight: 600;
}
.settings-card :deep(.el-table__cell) {
  padding: 12px 0;
}
.settings-card :deep(.el-descriptions__cell) {
  padding: 10px 0;
}
.policy-card {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  justify-content: space-between;
  gap: 16px;
}
.policy-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}
.policy-actions .el-button {
  margin-left: 0;
}
.secondary {
  display: block;
  color: #64748b;
  font-size: 12.5px;
  margin-top: 4px;
}
a {
  color: var(--el-color-primary);
  text-decoration: none;
  font-weight: 500;
}
a:hover {
  text-decoration: underline;
}
@media (max-width: 700px) {
  .settings-page {
    padding: 16px;
  }
}
</style>
