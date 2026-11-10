<script setup lang="ts">
import type { ResourceDefinition } from "./softwareManagement";
defineProps<{ resources?: ResourceDefinition[]; preview?: boolean; allowRelease?: boolean }>();
const emit = defineEmits<{ "update:resources": [ResourceDefinition[]] }>();
const types = { PATH_READ: "只读路径", PATH_WRITE: "受管写路径", PORT: "端口", UNIT: "服务单元", CONTAINER: "容器名称" };
</script>

<template>
  <div class="resource-fields">
    <header><slot></slot><button v-if="!preview" type="button" class="quiet" @click="emit('update:resources', [...(resources || []), { key: '', type: 'PATH_WRITE', value: '' }])">添加资源</button></header>
    <details v-for="(resource, index) in resources" :key="index" open>
      <summary>{{ resource.key || '新资源' }}</summary>
      <div class="resource-grid">
        <label>资源名称<input v-model="resource.key" maxlength="48" /></label>
        <label>类型<select v-model="resource.type" aria-label="类型"><option v-for="(label, key) in types" :key="key" :value="key">{{ label }}</option></select></label>
        <label class="wide">目标值<input v-model="resource.value" spellcheck="false" :placeholder="resource.type === 'PORT' ? '${OPS_PARAM_HTTP_PORT}' : '${OPS_INSTALL_ROOT}/config'" /></label>
        <label v-if="resource.type === 'PORT'">协议<select v-model="resource.protocol" aria-label="协议"><option value="TCP">TCP</option><option value="UDP">UDP</option></select></label>
        <label v-if="resource.type === 'PORT'">监听地址<input v-model="resource.bindAddress" placeholder="127.0.0.1" /></label>
        <label v-if="allowRelease && resource.type !== 'PATH_READ'" class="checkbox"><input v-model="resource.release" type="checkbox" />核验后释放该资源</label>
      </div>
      <button v-if="!preview" type="button" class="quiet danger" @click="emit('update:resources', resources!.filter((_, i) => i !== index))">删除资源声明</button>
    </details>
    <p v-if="!resources?.length">暂无额外资源</p>
  </div>
</template>

<style scoped>
header { display: flex; justify-content: space-between; align-items: center; gap: 12px; margin: 14px 0 10px; }
details { margin: 10px 0; padding: 12px; border: 1px solid var(--border); border-radius: var(--control-radius); }
summary { cursor: pointer; font-size: 13px; }
.resource-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; margin-top: 14px; }
label { display: grid; gap: 6px; font-size: 12px; color: var(--muted); }
.wide { grid-column: 1 / -1; }
.checkbox { display: flex; align-items: center; gap: 8px; }.checkbox input { width: 16px; min-height: 16px; }
input,select { width: 100%; min-width: 0; }
p { color: var(--muted); font-size: 12px; }
@media (max-width:600px) { .resource-grid { grid-template-columns: 1fr; } }
</style>
