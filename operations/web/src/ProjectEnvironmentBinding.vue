<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { ElDialog, ElMessage } from "element-plus";
import { ApiError, request } from "./api";
import type { ApplicationProject, ProjectEnvironment, ProjectServiceRegistration } from "./applicationProjects";
import { isProjectBuildService } from "./applicationProjects";
import type { Host } from "./hosts";
import { requestAllPages } from "./listPages";
import type { SoftwareInstance } from "./software";

const props = defineProps<{
  project: ApplicationProject;
  moduleId: string;
  environment?: ProjectEnvironment;
  registration?: ProjectServiceRegistration;
}>();
const emit = defineEmits<{ close: []; saved: [ApplicationProject]; error: [unknown] }>();
const lifetime = new AbortController();
const projectVersion = props.project.version;
const originalRuntime = props.registration?.runtimeSoftwareId || "";
const environmentName = ref(props.environment?.name || "");
const hostId = ref(props.registration?.hostId || "");
const recordedVersion = ref(props.registration?.recordedVersion || "");
const runtimeId = ref(originalRuntime);
const hosts = ref<Host[]>([]);
const software = ref<SoftwareInstance[]>([]);
const loadingHosts = ref(true), loadingSoftware = ref(true), saving = ref(false);
const hostError = ref(""), softwareError = ref(""), formError = ref("");
const module = computed(() => props.project.modules.find(item => item.id === props.moduleId));
const runtimeOptions = computed(() => software.value.filter(item =>
  item.hostId === hostId.value && ["SYSTEMD", "DOCKER"].includes(item.runtimeKind) &&
  !isProjectBuildService(module.value?.name || "", item) &&
  item.managementMode !== "RELEASED" && item.actualState !== "REMOVED" &&
  item.installationStatus !== "UNINSTALLED" && !!item.observation?.identityHash,
));
const originalBuildService = computed(() => software.value.some(item => item.id === originalRuntime && isProjectBuildService(module.value?.name || "", item)));
watch(hostId, () => { runtimeId.value = ""; });

function describe(error: unknown, fallback: string) {
  if (error instanceof ApiError && error.code === "AUTH_REQUIRED") emit("error", error);
  return error instanceof ApiError
    ? [error.message, error.code, error.traceId].filter(Boolean).join(" · ")
    : fallback;
}

onMounted(async () => {
  await Promise.all([
    request<Host[]>("/hosts", "GET", undefined, lifetime.signal)
      .then(items => { hosts.value = items; })
      .catch(error => { if (!lifetime.signal.aborted) hostError.value = describe(error, "无法读取主机"); })
      .finally(() => { loadingHosts.value = false; }),
    requestAllPages<SoftwareInstance>("/software-instances", lifetime.signal)
      .then(items => { software.value = items; })
      .catch(error => { if (!lifetime.signal.aborted) softwareError.value = describe(error, "无法读取已识别的软件"); })
      .finally(() => { loadingSoftware.value = false; }),
  ]);
});
onUnmounted(() => lifetime.abort());

async function save() {
  if (!module.value || saving.value || !environmentName.value.trim()) return;
  saving.value = true;
  formError.value = "";
  const registrationId = props.registration?.id;
  try {
    const saved = await request<ApplicationProject>(`/application-projects/${props.project.id}/service-instances`, "POST", {
      ...(registrationId ? { id: registrationId } : {}),
      version: projectVersion, moduleId: props.moduleId,
      environment: environmentName.value.trim(), hostId: hostId.value, recordedVersion: recordedVersion.value.trim(),
      ...(runtimeId.value === originalRuntime ? {} : { runtimeSoftwareId: runtimeId.value }),
    }, lifetime.signal);
    if (lifetime.signal.aborted) return;
    ElMessage.success(registrationId ? "环境绑定已更新" : "实例已创建");
    emit("saved", saved);
    emit("close");
  } catch (error) {
    if (!lifetime.signal.aborted) formError.value = describe(error, "保存环境绑定失败，请重试");
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <ElDialog :model-value="true" :title="registration ? '编辑环境绑定' : '绑定环境'" width="min(560px, 94vw)" append-to-body
    :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving" @close="emit('close')">
    <form class="binding-form" @submit.prevent="save">
      <p class="binding-module">{{ project.name }} / <strong>{{ module?.name }}</strong></p>
      <div class="binding-fields">
        <label>环境<input v-model="environmentName" list="known-binding-environments" :readonly="!!registration" :disabled="saving" maxlength="60" required placeholder="例如 test" />
          <datalist id="known-binding-environments"><option v-for="item in project.environments" :key="item.id" :value="item.name" /></datalist>
        </label>
        <label>目标主机<select v-model="hostId" aria-label="目标主机" :disabled="saving || loadingHosts || !!originalRuntime || !!hostError" :required="!registration">
          <option value="" :disabled="!registration || !!hostId">{{ loadingHosts ? "正在读取主机…" : "请选择主机" }}</option>
          <option v-if="hostId && !hosts.some(item => item.id === hostId)" :value="hostId">原绑定主机</option>
          <option v-for="host in hosts" :key="host.id" :value="host.id">{{ host.name }}</option>
        </select></label>
      </div>
      <label>登记版本<input v-model="recordedVersion" :disabled="saving" maxlength="100" placeholder="可选，例如 1.2.0 或构建 #42" /></label>
      <label>运行对象<select v-model="runtimeId" aria-label="运行对象" :disabled="saving || !hostId || loadingSoftware || !!softwareError">
        <option value="">暂不关联</option>
        <option v-if="originalRuntime && !runtimeOptions.some(item => item.id === originalRuntime)" :value="originalRuntime">{{ originalBuildService ? "Jenkins（原关联的构建服务）" : "原关联对象" }}</option>
        <option v-for="item in runtimeOptions" :key="item.id" :value="item.id">{{ item.name }} · {{ item.locator }}</option>
      </select></label>
      <p v-if="originalBuildService && runtimeId === originalRuntime" class="muted">请选择实际运行该模块的容器或 systemd 服务。尚未部署时可选「暂不关联」。</p>
      <p v-else-if="originalRuntime" class="muted">更换主机前，先设为「暂不关联」并保存。</p>
      <p v-else-if="hostId && !loadingSoftware && !softwareError && !runtimeOptions.length" class="muted">该主机暂无已识别的 Docker 容器或 systemd 服务。识别后可关联并查看运行日志。</p>
      <p v-if="hostError || softwareError || formError" class="error" role="alert">{{ [hostError, softwareError, formError].filter(Boolean).join(" · ") }}</p>
      <div class="dialog-footer">
        <button type="button" class="secondary" :disabled="saving" @click="emit('close')">取消</button>
        <button type="submit" class="primary" :disabled="saving || loadingHosts || !module || (!registration && (!hostId || !!hostError))">{{ saving ? "保存中…" : "保存绑定" }}</button>
      </div>
    </form>
  </ElDialog>
</template>

<style scoped>
.binding-form { display: grid; gap: 16px; }
.binding-form p { margin: 0; }
.binding-form label { display: grid; gap: 6px; min-width: 0; }
.binding-form input, .binding-form select { min-width: 0; width: 100%; }
.binding-module { color: var(--muted); overflow-wrap: anywhere; }
.binding-module strong { color: var(--text); }
.binding-fields { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
@media (max-width: 550px) {
  .binding-fields { grid-template-columns: minmax(0, 1fr); }
}
</style>
