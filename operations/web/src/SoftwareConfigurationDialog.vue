<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { ElDialog, ElMessageBox } from "element-plus";
import { request } from "./api";
import type { Host } from "./hosts";
import type { SoftwareInstance, SoftwareMutationResult } from "./software";
import { builtinConfigurationAvailable, configurationContext, hasConfiguration, managementOperations, type ManagementContext } from "./softwareManagement";
import SoftwareManagementPanel from "./SoftwareManagementPanel.vue";
import ServiceConfigurationPanel from "./ServiceConfigurationPanel.vue";
const props = defineProps<{ item?: SoftwareInstance; host?: Host }>();
const emit = defineEmits<{ close: []; submitted: [SoftwareMutationResult]; logs: [string]; preparation: [string] }>();
const detail = ref<SoftwareInstance>(), context = ref<ManagementContext>();
const loading = ref(false), error = ref("");
const mode = ref<"configuration" | "migration">("configuration");
const closeState = ref<{ blocked: boolean; dirty: boolean; pending?: boolean }>({ blocked: false, dirty: false });
const managed = computed(() => !!detail.value && !!context.value && hasConfiguration(context.value, detail.value));
const legacy = computed(() => !!detail.value && builtinConfigurationAvailable(detail.value));
const migrationAvailable = computed(() => {
  if (!managed.value || !detail.value || !context.value) return false;
  const operations = managementOperations(context.value, detail.value);
  return operations.some(operation => operation.key === "migrate-data" && !operation.readOnly) && operations.some(operation => operation.key === "check-data-migration" && operation.readOnly);
});
let revision = 0, abort: AbortController | undefined;
async function load() {
  const id = props.item?.id, current = ++revision;
  abort?.abort(); detail.value = undefined; context.value = undefined; error.value = ""; mode.value = "configuration";
  closeState.value = { blocked: false, dirty: false };
  if (!id) { loading.value = false; return; }
  const controller = new AbortController(); abort = controller; loading.value = true;
  try {
    const item = await request<SoftwareInstance>(`/software-instances/${id}`, "GET", undefined, controller.signal);
    if (current !== revision || props.item?.id !== id) return;
    detail.value = item;
    try {
      const result = await request<ManagementContext>(`/software-instances/${id}/management`, "GET", undefined, controller.signal);
      if (current !== revision || props.item?.id !== id) return;
      context.value = configurationContext(result, item);
    } catch (cause) { if (!builtinConfigurationAvailable(item)) throw cause; }
    if (current !== revision || props.item?.id !== id) return;
    if (!managed.value && !legacy.value) error.value = "当前软件模板未提供可编辑配置";
  } catch (cause) { if (current === revision && !controller.signal.aborted) error.value = cause instanceof Error ? cause.message : "读取配置失败"; }
  finally { if (current === revision) loading.value = false; }
}
async function confirmDiscard() {
  if (!closeState.value.dirty) return true;
  try { await ElMessageBox.confirm("放弃未保存的配置？", "关闭配置", { confirmButtonText: "放弃修改", cancelButtonText: "继续编辑" }); return true; } catch { return false; }
}
async function close(done?: () => void) {
  const current = revision;
  if (closeState.value.blocked || !(await confirmDiscard()) || current !== revision) return;
  revision++; abort?.abort(); loading.value = false;
  if (done) done(); else emit("close");
}
async function switchMode() {
  const requestedRevision = revision;
  if (closeState.value.blocked || closeState.value.pending || !(await confirmDiscard()) || requestedRevision !== revision) return;
  const id = detail.value?.id, current = revision;
  if (!id) return;
  const controller = new AbortController(); abort?.abort(); abort = controller; loading.value = true;
  try {
    const latest = await request<ManagementContext>(`/software-instances/${id}/management`, "GET", undefined, controller.signal);
    if (current !== revision || props.item?.id !== id || !detail.value) return;
    context.value = configurationContext(latest, detail.value);
    mode.value = mode.value === "configuration" ? "migration" : "configuration";
    closeState.value = { blocked: false, dirty: false };
  } catch (cause) { if (current === revision && !controller.signal.aborted) error.value = cause instanceof Error ? cause.message : "读取配置失败"; }
  finally { if (current === revision) loading.value = false; }
}
async function completed() {
  const id = detail.value?.id, current = revision;
  if (!id) return;
  try { const latest = await request<SoftwareInstance>(`/software-instances/${id}`); if (current === revision && props.item?.id === id) detail.value = latest; }
  catch (cause) { if (current === revision) error.value = cause instanceof Error ? cause.message : "状态刷新失败"; }
}
async function navigate(kind: "logs" | "preparation", id: string) {
  if (closeState.value.blocked || !(await confirmDiscard())) return;
  emit("close");
  if (kind === "logs") emit("logs", id); else emit("preparation", id);
}
watch(() => props.item?.id, load, { immediate: true });
onUnmounted(() => { revision++; abort?.abort(); });
</script>

<template>
  <ElDialog :model-value="!!item" :title="`${mode === 'migration' ? '迁移数据' : '编辑配置'} · ${item?.name || ''}`" width="min(920px, calc(100vw - 32px))" class="software-configuration-dialog" align-center append-to-body destroy-on-close :before-close="close" :close-on-click-modal="!closeState.blocked" :close-on-press-escape="!closeState.blocked" :show-close="!closeState.blocked" @close="emit('close')">
    <p v-if="loading" role="status">正在读取软件配置…</p>
    <div v-else-if="error" class="configuration-error" role="alert">{{ error }} <button class="quiet" @click="load">重新读取</button></div>
    <template v-else-if="detail">
      <div v-if="migrationAvailable" class="configuration-navigation"><button class="quiet" :disabled="closeState.blocked || closeState.pending" @click="switchMode">{{ mode === 'configuration' ? '迁移数据' : '返回配置' }}</button></div>
      <SoftwareManagementPanel v-if="managed && context" :key="`${detail.id}:${mode}`" :detail="detail" :initial-context="context" :presentation="mode" :active="true" :disabled="!!host?.maintenance" @close-state="closeState = $event" @submitted="emit('submitted', $event)" @completed="completed" @logs="navigate('logs', $event)" @preparation="navigate('preparation', detail.hostId)" />
      <ServiceConfigurationPanel v-else-if="legacy" :detail="detail" :host="host" :disabled="!!host?.maintenance" direct-edit @close-state="closeState = $event" @submitted="emit('submitted', $event)" @cancel="close()" @logs="navigate('logs', $event)" @preparation="navigate('preparation', $event)" />
    </template>
  </ElDialog>
</template>

<style scoped>
.configuration-navigation { display:flex; justify-content:flex-end; margin-bottom:16px; }
.configuration-error { padding:14px; color:var(--error); background:#fff6f4; border-radius:6px; font-size:13px; }
</style>
<style>
.software-configuration-dialog.el-dialog { display:flex; flex-direction:column; max-height:calc(100dvh - 32px); border-radius:10px; }
.software-configuration-dialog .el-dialog__body { min-height:0; overflow-y:auto; overflow-x:hidden; padding-top:16px; }
@media(max-width:520px) { .software-configuration-dialog.el-dialog { width:calc(100vw - 16px)!important; max-height:calc(100dvh - 16px); } }
</style>
