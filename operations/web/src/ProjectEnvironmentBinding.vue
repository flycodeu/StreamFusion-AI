<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { ElDialog, ElMessage } from "element-plus";
import { ApiError, request } from "./api";
import type { ApplicationProject, ProjectEnvironment, ProjectInstanceSource, ProjectServiceRegistration } from "./applicationProjects";
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
const emit = defineEmits<{ close: []; "manage-targets": [ProjectInstanceSource]; saved: [ApplicationProject]; error: [unknown] }>();
const lifetime = new AbortController();
const projectVersion = props.project.version;
const originalRuntime = props.registration?.runtimeSoftwareId || "";
const existingEnvironments = props.project.environments?.filter(item => item.deployments.some(row => row.moduleId === props.moduleId)) || [];
const environmentName = ref(props.environment?.name || (existingEnvironments.length ? "" : "test"));
const hostId = ref(props.registration?.hostId || "");
const originalTarget = props.registration?.managedInstanceId || "";
const managedId = ref(originalTarget);
const removing = ref(false);
const runtimeId = ref(originalRuntime);
interface ManagedTarget { id: string; name: string; targetId: string; config: { environment: string; hostPort: number } }
const managedTargets = ref<ManagedTarget[]>([]);
const loadingTargets = ref(true), targetError = ref("");
const targetOptions = computed(() => managedTargets.value.filter(item =>
  item.targetId === hostId.value && item.config.environment.toLowerCase() === environmentName.value.trim().toLowerCase(),
));
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
watch(hostId, () => { runtimeId.value = ""; managedId.value = ""; });
watch(environmentName, () => { managedId.value = ""; });

function describe(error: unknown, fallback: string) {
  if (error instanceof ApiError && error.code === "AUTH_REQUIRED") emit("error", error);
  return error instanceof ApiError
    ? [error.message, error.code, error.traceId].filter(Boolean).join(" · ")
    : fallback;
}

onMounted(async () => {
  await Promise.all([
    request<Host[]>("/hosts", "GET", undefined, lifetime.signal)
      .then(items => { hosts.value = items; if (!props.registration && items.length === 1) hostId.value = items[0].id; })
      .catch(error => { if (!lifetime.signal.aborted) hostError.value = describe(error, "无法读取主机"); })
      .finally(() => { loadingHosts.value = false; }),
    requestAllPages<ManagedTarget>("/instances", lifetime.signal)
      .then(items => { managedTargets.value = items; })
      .catch(error => { if (!lifetime.signal.aborted) targetError.value = describe(error, "无法读取上线目标，请重新打开运行配置"); })
      .finally(() => { loadingTargets.value = false; }),
    requestAllPages<SoftwareInstance>("/software-instances", lifetime.signal)
      .then(items => { software.value = items; })
      .catch(error => { if (!lifetime.signal.aborted) softwareError.value = describe(error, "无法读取已识别的软件"); })
      .finally(() => { loadingSoftware.value = false; }),
  ]);
});
onUnmounted(() => lifetime.abort());

async function save() {
  if (!module.value || props.project.archived || saving.value || !environmentName.value.trim()) return;
  saving.value = true;
  formError.value = "";
  const registrationId = props.registration?.id;
  try {
    const saved = await request<ApplicationProject>(`/application-projects/${props.project.id}/service-instances`, "POST", {
      ...(registrationId ? { id: registrationId } : {}),
      version: projectVersion, moduleId: props.moduleId,
      environment: environmentName.value.trim(), hostId: hostId.value, recordedVersion: props.registration?.recordedVersion || "",
      ...(managedId.value === originalTarget ? {} : { managedInstanceId: managedId.value }),
      ...(runtimeId.value === originalRuntime ? {} : { runtimeSoftwareId: runtimeId.value }),
    }, lifetime.signal);
    if (lifetime.signal.aborted) return;
    ElMessage.success("运行配置已保存");
    emit("saved", saved);
    emit("close");
  } catch (error) {
    if (!lifetime.signal.aborted) formError.value = describe(error, "运行配置保存失败，请重试");
  } finally {
    saving.value = false;
  }
}
async function removeEnvironment() {
  if (!props.registration || props.project.archived || originalRuntime || !removing.value || saving.value) return;
  saving.value = true;
  formError.value = "";
  try {
    const saved = await request<ApplicationProject>(`/application-projects/${props.project.id}/service-instances/${props.registration.id}`, "DELETE", { version: projectVersion }, lifetime.signal);
    if (lifetime.signal.aborted) return;
    emit("saved", saved);
    emit("close");
    ElMessage.success("运行环境已移除");
  } catch (error) {
    if (!lifetime.signal.aborted) formError.value = describe(error, "运行环境移除失败");
  } finally { saving.value = false; }
}
</script>

<template>
  <ElDialog :model-value="true" title="运行配置" width="min(560px, 94vw)" append-to-body
    :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving" @close="emit('close')">
    <form class="binding-form" @submit.prevent="save">
      <p class="binding-module">{{ project.name }} / <strong>{{ module?.name }}</strong></p>
      <div class="binding-fields">
        <label>环境<input v-model="environmentName" list="known-binding-environments" :readonly="!!registration" :disabled="saving" maxlength="60" required placeholder="例如 test" />
          <datalist id="known-binding-environments"><option v-for="item in project.environments" :key="item.id" :value="item.name" /></datalist>
        </label>
        <label>运行服务器<select v-model="hostId" aria-label="运行服务器" :disabled="saving || loadingHosts || !!originalRuntime || !!hostError" :required="!registration">
          <option value="" :disabled="!registration || !!hostId">{{ loadingHosts ? "正在读取主机…" : "选择运行服务器" }}</option>
          <option v-if="hostId && !hosts.some(item => item.id === hostId)" :value="hostId">原绑定主机</option>
          <option v-for="host in hosts" :key="host.id" :value="host.id">{{ host.name }}</option>
        </select></label>
      </div>
      <label>上线目标<select v-model="managedId" :disabled="saving || !hostId || loadingTargets || !!targetError">
        <option value="">稍后设置</option>
        <option v-if="originalTarget && managedId === originalTarget && !targetOptions.some(item => item.id === originalTarget)" :value="originalTarget">原上线目标（待核对）</option>
        <option v-for="item in targetOptions" :key="item.id" :value="item.id">{{ item.name }} · {{ item.config.hostPort }}</option>
      </select></label>
      <p class="target-help"><span v-if="!targetOptions.length && hostId && !loadingTargets && !targetError">该服务器在此环境下还没有受管实例。</span>
        <button type="button" class="quiet" :disabled="saving" @click="emit('manage-targets', { projectId: project.id, moduleId, serviceInstanceId: registration?.id })">管理受管实例</button></p>
      <label>服务日志来源<select v-model="runtimeId" aria-label="服务日志来源" :disabled="saving || !hostId || loadingSoftware || !!softwareError">
        <option value="">稍后设置</option>
        <option v-if="originalRuntime && !runtimeOptions.some(item => item.id === originalRuntime)" :value="originalRuntime">{{ originalBuildService ? "Jenkins（原关联的构建服务）" : "原关联对象" }}</option>
        <option v-for="item in runtimeOptions" :key="item.id" :value="item.id">{{ item.name }} · {{ item.locator }}</option>
      </select></label>
      <p v-if="originalBuildService && runtimeId === originalRuntime" class="muted">请选择实际运行该模块的容器或 systemd 服务。尚未部署时可选「稍后设置」。</p>
      <p v-else-if="originalRuntime" class="muted">如需更换服务器，先清除日志来源并保存。</p>
      <p v-else-if="hostId && !loadingSoftware && !softwareError && !runtimeOptions.length" class="muted">该主机暂无已识别的 Docker 容器或 systemd 服务。识别后可关联并查看运行日志。</p>
      <p v-if="hostError || softwareError || targetError || formError" class="error" role="alert">{{ [hostError, softwareError, targetError, formError].filter(Boolean).join(" · ") }}</p>
      <div v-if="removing" class="remove-prompt" role="group" aria-label="移除运行环境">
        <span>移除 {{ module?.name }} 的 {{ environmentName }} 运行环境？</span>
        <button class="quiet" type="button" :disabled="saving" @click="removing = false">取消移除</button>
        <button class="quiet danger" type="button" :disabled="saving" @click="removeEnvironment">确认移除</button>
      </div>
      <div v-else class="dialog-footer">
        <button v-if="registration && !originalRuntime" type="button" class="quiet danger remove" :disabled="saving" @click="removing = true">移除环境</button>
        <button type="button" class="secondary" :disabled="saving" @click="emit('close')">取消</button>
        <button type="submit" class="primary" :disabled="saving || loadingHosts || !module || (!registration && (!hostId || !!hostError))">{{ saving ? "保存中…" : "保存配置" }}</button>
      </div>
    </form>
  </ElDialog>
</template>

<style scoped>
.binding-form { display: grid; gap: 16px; }
.binding-form p { margin: 0; }
.target-help,.remove-prompt {display:flex; gap:6px; align-items:center; flex-wrap:wrap; font-size:12px; color:var(--muted);}
.remove {margin-right:auto;}
.binding-form label { display: grid; gap: 6px; min-width: 0; }
.binding-form input, .binding-form select { min-width: 0; width: 100%; }
.binding-module { color: var(--muted); overflow-wrap: anywhere; }
.binding-module strong { color: var(--text); }
.binding-fields { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
@media (max-width: 550px) {
  .binding-fields { grid-template-columns: minmax(0, 1fr); }
}
</style>
