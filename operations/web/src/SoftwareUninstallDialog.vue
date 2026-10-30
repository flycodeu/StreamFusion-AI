<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { ElDialog } from "element-plus";
import { ApiError, request } from "./api";
import { hostTime } from "./hosts";
import { softwareIsDockerEngine, softwareUninstallAvailable, type SoftwareInstance, type SoftwareMutationResult } from "./software";

const props = defineProps<{ open: boolean; item?: SoftwareInstance; dependentContainers: string[] }>();
const emit = defineEmits<{ close: []; submitted: [SoftwareMutationResult] }>();
const deleteData = ref(false);
const confirmationName = ref("");
const busy = ref(false);
const attempted = ref(false);
const key = ref("");
const expectedVersion = ref("");
const error = ref<{ message: string; code: string; at: string; traceId: string }>();
const directoryRows = computed(() => [
  { label: "实例根目录", path: props.item?.installationPaths?.root },
  { label: "数据目录", path: props.item?.installationPaths?.data },
  { label: "配置目录", path: props.item?.installationPaths?.config },
  { label: "实例日志目录", path: props.item?.installationPaths?.logs },
]);
const pathsReady = computed(() => directoryRows.value.every((row) =>
  !!row.path && row.path.startsWith("/") && row.path !== "/"));
watch(() => props.item?.id, () => {
  deleteData.value = false;
  confirmationName.value = "";
  attempted.value = false;
  key.value = crypto.randomUUID();
  expectedVersion.value = props.item?.version || "";
  error.value = undefined;
}, { immediate: true });
const canSubmit = computed(() => !!props.item && softwareUninstallAvailable(props.item) &&
  (!deleteData.value || (pathsReady.value && confirmationName.value === props.item.name)));
async function submit() {
  if (busy.value || !canSubmit.value || !props.item) return;
  busy.value = true;
  error.value = undefined;
  if (!attempted.value) expectedVersion.value = props.item.version;
  attempted.value = true;
  try {
    const result = await request<SoftwareMutationResult>(`/software-instances/${props.item.id}/actions`, "POST", {
      action: "UNINSTALL", expectedVersion: expectedVersion.value, idempotencyKey: key.value,
      confirmed: true, deleteData: deleteData.value,
      ...(deleteData.value ? { confirmationName: confirmationName.value } : {}),
    });
    attempted.value = false;
    key.value = crypto.randomUUID();
    emit("submitted", result);
    emit("close");
  } catch (cause) {
    error.value = {
      message: cause instanceof Error ? cause.message : "卸载提交尚未确认，请重试核对原作业",
      code: cause instanceof ApiError ? cause.code : "REQUEST_NOT_CONFIRMED",
      at: cause instanceof ApiError ? cause.timestamp : new Date().toISOString(),
      traceId: cause instanceof ApiError ? cause.traceId : "",
    };
  } finally { busy.value = false; }
}
</script>

<template>
  <ElDialog :model-value="open" title="卸载软件" width="min(540px,94vw)" align-center
    :close-on-click-modal="!busy" :close-on-press-escape="!busy" :show-close="!busy" @close="emit('close')">
    <div v-if="item" class="uninstall-form">
      <p>卸载 <strong>{{ item.name }}</strong>？程序或受管容器将被移除，服务会停止。</p>
      <p class="muted">安装记录和执行历史会保留，之后可在“全部登记”中查看。</p>
      <div v-if="softwareIsDockerEngine(item)" class="uninstall-warning">
        Docker Engine 是主机容器运行引擎，停止或移除它会影响容器服务。
        <p v-if="dependentContainers.length">此主机已登记 {{ dependentContainers.length }} 个容器：{{ dependentContainers.join("、") }}。请先处理依赖；服务器上未登记的容器也可能受影响。</p>
        <p>Docker Engine 的容器数据使用下方数据目录。</p>
      </div>
      <label class="delete-choice"><input v-model="deleteData" type="checkbox" :disabled="busy || attempted" />同时删除此实例的数据</label>
      <p class="data-summary">{{ deleteData ? "会删除此实例专属的数据、配置和日志目录，无法通过界面恢复。" : "默认保留此实例的数据、配置和日志目录。" }}</p>
      <section v-if="pathsReady" class="uninstall-paths" aria-label="卸载目录范围">
        <h3>{{ deleteData ? "将删除的实例目录" : "保留的实例目录" }}</h3>
        <dl><template v-for="directory in directoryRows" :key="directory.label">
          <dt>{{ directory.label }}</dt><dd><code>{{ directory.path }}</code></dd>
        </template></dl>
      </section>
      <p v-else class="uninstall-warning" role="alert">未取得完整安装目录，暂不能删除数据。请取消并刷新软件详情，重新检查后再操作；仍可选择卸载并保留数据。</p>
      <section v-if="softwareIsDockerEngine(item)" class="uninstall-paths" aria-label="Docker 系统目录处理">
        <h3>Docker 系统目录</h3>
        <dl>
          <dt>主机配置</dt><dd><code>/etc/docker/daemon.json</code><span>{{ deleteData ? "仅在内容仍与本实例安装回执一致时删除；不一致时报告错误。" : "保留。" }}</span></dd>
          <dt>containerd 数据</dt><dd><code>/var/lib/containerd</code><span>始终保留。</span></dd>
        </dl>
      </section>
      <label v-if="deleteData" class="name-confirm">输入实例名称“{{ item.name }}”确认删除
        <input v-model="confirmationName" aria-label="确认删除的实例名称" :disabled="busy || attempted" autocomplete="off" />
      </label>
      <div v-if="error" class="uninstall-warning" role="alert">
        <strong>{{ error.message }}</strong>
        <small>{{ error.code }} · {{ hostTime(error.at) }} · {{ item.name }}</small>
        <details v-if="error.traceId"><summary>诊断信息</summary><code>{{ error.traceId }}</code></details>
        <p>重试会核对同一卸载请求。也可关闭后查看该实例的执行记录。</p>
      </div>
    </div>
    <template #footer>
      <button class="secondary" :disabled="busy" @click="emit('close')">取消</button>
      <button class="primary danger" :disabled="busy || !canSubmit" @click="submit">{{ busy ? "提交中…" : attempted ? "重试并核对原请求" : deleteData ? "卸载并删除数据" : "卸载，保留数据" }}</button>
    </template>
  </ElDialog>
</template>

<style scoped>
.uninstall-form { display: grid; gap: 15px; font-size: 13px; line-height: 1.8; max-height: 65vh; overflow-y: auto; }
p { margin: 0; overflow-wrap: anywhere; }
.muted, .data-summary { color: var(--muted); font-size: 12px; }
.delete-choice { display: flex; align-items: center; gap: 9px; cursor: pointer; }
.delete-choice input { width: 16px; height: 16px; margin: 0; accent-color: var(--danger); }
.name-confirm { display: grid; gap: 8px; font-size: 12px; }
.name-confirm input { margin: 0; }
.uninstall-warning { padding: 12px 14px; border: 1px solid #ead5ae; border-radius: 5px; background: #fff9ef; color: #946619; font-size: 12px; }
.uninstall-warning p, small { display: block; margin-top: 7px; }
.uninstall-paths { border: 1px solid var(--border); border-radius: 5px; padding: 12px; }
.uninstall-paths h3 { margin: 0 0 10px; font-size: 12px; }
.uninstall-paths dl { display: grid; grid-template-columns: 90px minmax(0, 1fr); gap: 8px 12px; margin: 0; }
.uninstall-paths dt, .uninstall-paths dd { margin: 0; font-size: 12px; }
.uninstall-paths dt, .uninstall-paths span { color: var(--muted); }
.uninstall-paths span { display: block; margin-top: 4px; }
code { font: 11px/1.7 var(--mono); overflow-wrap: anywhere; }
summary { cursor: pointer; }
.primary { margin: 0 0 0 10px; width: auto; }
.primary.danger { color: #fff; background: var(--error); border-color: var(--error); }
.primary.danger:hover:not(:disabled) { background: #b91c1c; border-color: #b91c1c; }
</style>
