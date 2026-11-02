<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { ElDialog } from "element-plus";
import { ApiError, request } from "./api";
import {
  automaticPreparationUpdate,
  type AutomaticPreparationUpdate,
  type PreparationUpdateCheck,
} from "./preparationUpdate";
import { hostTime, type Host } from "./hosts";
import type {
  InstallationMaterial,
  ServiceRecipe,
} from "./serviceInstallation";
import {
  softwareJobStatusLabel,
  softwareJobErrorMessages,
  softwareJobDiagnostic,
  type SoftwareJob,
} from "./software";
import ErrorHelpLink from "./ErrorHelpLink.vue";
import HostPreparationPanel from "./HostPreparationPanel.vue";
import NodeRegistryPanel from "./NodeRegistryPanel.vue";
import NodeVersionDialog from "./NodeVersionDialog.vue";
import {
  nodeInitialPlanMatches,
  type NodeInitialIntent,
  nodeActionLabels,
  nodeJobPending,
  nodeMaterials,
  nodeTerminalCommand,
  nodeVersionRow,
  nodeVersionValue,
  type NodeAction,
  type NodeCatalog,
  type NodeDiscovery,
  type NodeDraft,
  type NodeEnvironment,
  type NodeObservation,
  type NodePlan,
  type NodeRegistry,
} from "./nodeEnvironment";

const props = defineProps<{ host: Host; initialEnvironmentId?: string }>();
const emit = defineEmits<{ changed: []; closeState: [state: { blocked: boolean; dirty: boolean }] }>();
const registryCloseState = ref({ busy: false, dirty: false });
const environments = ref<NodeEnvironment[]>([]);
const selectedId = ref("");
const environment = ref<NodeEnvironment>();
const catalog = ref<NodeCatalog>({ node: [], tools: [] });
const materials = ref<InstallationMaterial[]>([]);
const busy = ref(false);
const loading = ref(false);
const error = ref<{
  message: string;
  code: string;
  traceId: string;
  at: string;
}>();
const draft = ref<NodeDraft>();
const plan = ref<NodePlan>();
const initialIntent = ref<NodeInitialIntent>();
const continuationRetry = ref<{ path: string; key: string }>();
let continuingInitial = false;
const attempted = ref(false);
const submitKey = ref("");
const submission = ref<{ path: string; body: unknown }>();
const jobs = ref<SoftwareJob[]>([]);
const job = ref<SoftwareJob>();
const logs = ref("");
const logsJobId = ref("");
watch(selectedId, () => {
  logs.value = "";
  logsJobId.value = "";
});
const tab = ref("versions");
const discoveryOpen = ref(false);
const discoveryAccount = ref("");
const discoveryPath = ref("");
const discovery = ref<NodeDiscovery>();
const terminalOpen = ref(false);
const preparationOpen = ref(false);
const preparationBusy = ref(false);
const automaticUpdate = ref<AutomaticPreparationUpdate>();
const currentHost = ref(props.host);
let revision = 0;
let disposed = false;
let pollFailures = 0;
let timer: ReturnType<typeof setTimeout> | undefined;
const active = computed(() =>
  environments.value.filter((item) => item.managementMode !== "RELEASED"),
);
const desiredDefault = computed(
  () =>
    environment.value?.versions.find(
      (v) => v.id === environment.value?.desiredDefaultSoftwareId,
    )?.softwareVersion || "未设置",
);
const versions = computed(() => environment.value?.observation?.versions || []);
const pending = computed(
  () =>
    !!initialIntent.value ||
    !!continuationRetry.value ||
    nodeJobPending(job.value),
);
const writable = computed(
  () =>
    environment.value?.managementMode === "MANAGED" &&
    !currentHost.value.maintenance &&
    !pending.value,
);
watch(() => [busy.value, attempted.value, pending.value, !!draft.value, discoveryOpen.value, preparationOpen.value, terminalOpen.value, registryCloseState.value], () => emit("closeState", {
  blocked: busy.value || pending.value || !!draft.value || discoveryOpen.value || preparationOpen.value || terminalOpen.value || registryCloseState.value.busy,
  dirty: registryCloseState.value.dirty,
}), { immediate: true, deep: true });
function selectRegistry(entry: NodeRegistry) {
  openAction("SET_NPM_REGISTRY");
  draft.value!.registryId = entry.id;
  draft.value!.expectedRegistryVersion = entry.version;
}
const toolsKey = computed(() =>
  !draft.value
    ? ""
    : draft.value.action.includes("PNPM")
      ? "pnpm"
      : draft.value.action === "UPDATE_NPM"
        ? "npm"
        : "nvm",
);
const choices = computed(() =>
  draft.value?.action === "INSTALL_NODE"
    ? catalog.value.node
    : catalog.value.tools.filter((item) => item.key === toolsKey.value),
);
const recipe = computed(
  () =>
    choices.value.find(
      (item) =>
        nodeVersionValue(item.version) ===
        nodeVersionValue(
          draft.value?.action === "INSTALL_NODE"
            ? draft.value.nodeVersion
            : draft.value?.packageVersion || "",
        ),
    ) || choices.value[0],
);
const initialRecipe = computed(() =>
  catalog.value.node.find((r) => r.version === draft.value?.initialNodeVersion),
);
const initialMaterials = computed(() =>
  nodeMaterials(materials.value, initialRecipe.value),
);
const matchingMaterials = computed(() =>
  nodeMaterials(materials.value, recipe.value),
);
const downloadable = (action: NodeAction) =>
  [
    "INITIALIZE",
    "INSTALL_NODE",
    "UPDATE_NPM",
    "INSTALL_PNPM",
    "UPDATE_PNPM",
    "UPDATE_NVM",
  ].includes(action);
function report(e: unknown) {
  error.value = {
    message: e instanceof Error ? e.message : "请求未完成，请重试",
    code: e instanceof ApiError ? e.code : "REQUEST_NOT_CONFIRMED",
    traceId: e instanceof ApiError ? e.traceId : "",
    at: e instanceof ApiError ? e.timestamp : new Date().toISOString(),
  };
}
async function act(work: () => Promise<void>) {
  if (busy.value) return;
  const token = revision;
  busy.value = true;
  error.value = undefined;
  try {
    await work();
  } catch (e) {
    if (token === revision && !disposed) report(e);
  } finally {
    if (token === revision) busy.value = false;
  }
}
async function loadDetail(id = selectedId.value) {
  const token = revision;
  if (!id) {
    environment.value = undefined;
    jobs.value = [];
    job.value = undefined;
    return;
  }
  const [detail, history] = await Promise.all([
    request<NodeEnvironment>("/node-environments/" + id),
    request<SoftwareJob[]>("/node-environments/" + id + "/jobs"),
  ]);
  if (token !== revision || disposed || id !== selectedId.value) return;
  environment.value = detail;
  jobs.value = history;
  if (detail.lastJobId) {
    const latestJob = await request<SoftwareJob>("/jobs/" + detail.lastJobId);
    if (token === revision && !disposed && id === selectedId.value)
      job.value = latestJob;
  } else job.value = undefined;
  await continueInitialInstallation();
}
async function continueInitialInstallation() {
  const intent = initialIntent.value,
    env = environment.value,
    latest = job.value;
  if (
    !intent ||
    !env ||
    !latest ||
    latest.id !== intent.initializationJobId ||
    continuingInitial
  )
    return;
  if (["UNKNOWN", "FAILED", "CANCELLED"].includes(latest.status)) {
    initialIntent.value = undefined;
    report(
      new Error("环境准备结果未确认，已停止后续安装；请核对操作记录后继续安装"),
    );
    return;
  }
  if (latest.status !== "SUCCEEDED" || latest.targetAckPending) return;
  continuingInitial = true;
  const token = revision;
  try {
    const r = intent.definition;
    const next = await request<NodePlan>(
      "/hosts/" + intent.hostId + "/software-install-plans",
      "POST",
      {
        expectedHostVersion: intent.hostVersion,
        recipeKey: r.key,
        recipeVersion: r.version,
        recipeRevision: r.revision,
        config: {
          name: "Node.js " + r.version,
          runtimeMode: "NATIVE",
          memoryMiB: 512,
        },
        source: {
          type: intent.source.type,
          ...(intent.source.materialId
            ? { materialId: intent.source.materialId }
            : {}),
        },
        node: {
          environmentId: env.id,
          environmentVersion: env.version,
          makeDefault: intent.makeDefault,
          connectTerminal: true,
        },
      },
    );
    if (token !== revision || disposed) return;
    if (!nodeInitialPlanMatches(intent, next, env, currentHost.value))
      throw new Error(
        "初始化已完成，但 Node 计划与确认内容不一致；请重新确认安装",
      );
    initialIntent.value = undefined;
    // The initial dialog already confirmed this exact version. Reuse ordinary jobs; never resume this chain after a page reload.
    continuationRetry.value = {
      path: "/software-install-plans/" + next.id + "/submit",
      key: intent.submitKey,
    };
    const result = await request<{ jobId: string }>(
      continuationRetry.value.path,
      "POST",
      { idempotencyKey: intent.submitKey },
    );
    continuationRetry.value = undefined;
    if (token !== revision || disposed) return;
    job.value = await request<SoftwareJob>("/jobs/" + result.jobId);
    await refresh();
    emit("changed");
  } catch (e) {
    if (token === revision && !disposed) {
      initialIntent.value = undefined;
      report(e);
    }
  } finally {
    continuingInitial = false;
  }
}
async function retryContinuation() {
  await act(async () => {
    const original = continuationRetry.value;
    if (!original) return;
    await request(original.path, "POST", { idempotencyKey: original.key });
    continuationRetry.value = undefined;
    await refresh();
  });
}
async function refresh() {
  const token = revision,
    hostId = currentHost.value.id;
  loading.value = true;
  try {
    const [list, definitions, files] = await Promise.all([
      request<NodeEnvironment[]>("/hosts/" + hostId + "/node-environments"),
      request<NodeCatalog>("/node-environment-recipes"),
      request<InstallationMaterial[]>("/installation-materials"),
    ]);
    if (token !== revision || disposed) return;
    environments.value = list;
    catalog.value = definitions;
    materials.value = files;
    if (!list.some((item) => item.id === selectedId.value))
      selectedId.value =
        list.find((item) => item.managementMode !== "RELEASED")?.id || "";
    await loadDetail();
  } finally {
    if (token === revision) loading.value = false;
  }
}
function openAction(action: NodeAction, nodeVersion = "") {
  const key = action.includes("PNPM")
    ? "pnpm"
    : action === "UPDATE_NPM"
      ? "npm"
      : "nvm";
  draft.value = {
    action,
    name: "Node.js 环境",
    account:
      currentHost.value.sshUser === "root"
        ? ""
        : currentHost.value.sshUser || "",
    nodeVersion: nodeVersion || catalog.value.node[0]?.version || "",
    initialNodeVersion: catalog.value.node[0]?.version || "",
    initialNodeMaterialId: "",
    packageVersion:
      catalog.value.tools.find((item) => item.key === key)?.version || "",
    source: "OFFICIAL",
    materialId: "",
    makeDefault:
      action === "INITIALIZE" ||
      (action === "INSTALL_NODE" && !versions.value.length),
    deleteData: false,
    clearNodeDefault: false,
    confirmationName: "",
  };
  if (
    action === "INITIALIZE" ||
    action === "UPDATE_NVM" ||
    action === "CONNECT_TERMINAL" ||
    action === "SET_NPM_REGISTRY" ||
    action === "REMOVE_ENVIRONMENT" ||
    action === "RELEASE"
  )
    draft.value.nodeVersion = "";
  plan.value = undefined;
  attempted.value = false;
  submission.value = undefined;
  submitKey.value = crypto.randomUUID();
  error.value = undefined;
}
function initialMaterialReady(item: InstallationMaterial) {
  materialReady(item);
  if (draft.value) {
    draft.value.initialNodeMaterialId = item.id;
    draft.value.materialId = matchingMaterials.value[0]?.id || "";
  }
}
watch(initialRecipe, () => {
  if (draft.value && !attempted.value) {
    draft.value.initialNodeMaterialId = initialMaterials.value[0]?.id || "";
    plan.value = undefined;
  }
});
function materialReady(item: InstallationMaterial) {
  materials.value = [...materials.value.filter((m) => m.id !== item.id), item];
  if (draft.value) draft.value.materialId = item.id;
  changedDraft();
}
function changedDraft() {
  if (!attempted.value) {
    plan.value = undefined;
    error.value = undefined;
  }
}
watch(recipe, () => {
  if (draft.value && !attempted.value) {
    draft.value.materialId = matchingMaterials.value[0]?.id || "";
    plan.value = undefined;
  }
});
function source() {
  return {
    type: draft.value!.source,
    ...(draft.value!.source === "ARCHIVE"
      ? { materialId: draft.value!.materialId }
      : {}),
  };
}
function validateDraft() {
  const d = draft.value!;
  if (
    d.action === "INITIALIZE" &&
    (!initialRecipe.value ||
      (d.source === "ARCHIVE" &&
        !initialMaterials.value.some((m) => m.id === d.initialNodeMaterialId)))
  )
    throw new Error("请选择 Node 精确版本及对应离线材料");
  if (
    d.action !== "INITIALIZE" &&
    d.action !== "RELEASE" &&
    environment.value?.managementMode !== "MANAGED"
  )
    throw new Error("自行安装的环境仅支持检测和解除登记");
  if (
    d.action === "INITIALIZE" &&
    (!d.name.trim() || !d.account.trim() || d.account === "root")
  )
    throw new Error("请填写环境名称及服务器已有的非 root 账号");
  if (downloadable(d.action) && !recipe.value)
    throw new Error("当前没有可用的固定安装版本");
  if (
    downloadable(d.action) &&
    d.source === "ARCHIVE" &&
    !matchingMaterials.value.some((item) => item.id === d.materialId)
  )
    throw new Error("请选择此版本已核验的离线材料");
}
async function preflight() {
  await runPreflight(true);
}
async function runPreflight(allowAutomaticUpdate: boolean) {
  await act(async () => {
    validateDraft();
    const token = revision,
      d = draft.value!,
      env = environment.value;
    try {
      let result: NodePlan;
      if (d.action === "INSTALL_NODE") {
        if (!env || !recipe.value)
          throw new Error("请先选择已准备好的 Node 环境");
        const r = recipe.value;
        result = await request<NodePlan>(
          "/hosts/" + currentHost.value.id + "/software-install-plans",
          "POST",
          {
            expectedHostVersion: currentHost.value.version,
            recipeKey: r.key,
            recipeVersion: r.version,
            recipeRevision: r.revision,
            config: {
              name: "Node.js " + r.version,
              runtimeMode: "NATIVE",
              memoryMiB: 512,
            },
            source: source(),
            node: {
              environmentId: env.id,
              environmentVersion: env.version,
              makeDefault: d.makeDefault,
              connectTerminal: true,
            },
          },
        );
      } else {
        result = await request<NodePlan>(
          "/hosts/" + currentHost.value.id + "/node-environment-plans",
          "POST",
          {
            expectedHostVersion: currentHost.value.version,
            ...(d.action === "INITIALIZE"
              ? { name: d.name.trim(), account: d.account.trim() }
              : {
                  environmentId: env!.id,
                  expectedEnvironmentVersion: env!.version,
                }),
            action: d.action,
            nodeVersion: d.nodeVersion || undefined,
            packageVersion: [
              "UPDATE_NPM",
              "INSTALL_PNPM",
              "UPDATE_PNPM",
            ].includes(d.action)
              ? d.packageVersion
              : undefined,
            source: ["CONNECT_TERMINAL", "SET_NPM_REGISTRY"].includes(d.action) ? undefined : source(),
            registryId: d.registryId,
            expectedRegistryVersion: d.expectedRegistryVersion,
            deleteData: d.deleteData,
            confirmationName: d.confirmationName,
          },
        );
      }
      if (token === revision && !disposed) plan.value = result;
    } catch (cause) {
      if (token !== revision || disposed) return;
      if (allowAutomaticUpdate && currentHost.value.kind === "SSH" && cause instanceof ApiError && ["NODE_CAPABILITY_REQUIRED", "NODE_TERMINAL_CAPABILITY_REQUIRED", "NODE_REGISTRY_CAPABILITY_REQUIRED", "EXECUTOR_UPGRADE_REQUIRED"].includes(cause.code)) {
        const checked = await request<PreparationUpdateCheck>(
          `/hosts/${currentHost.value.id}/preparation?checkUpdate=1`,
        );
        if (token !== revision || disposed) return;
        if (
          checked.hostId !== currentHost.value.id ||
          checked.expectedHostVersion !== currentHost.value.version
        )
          throw new ApiError(
            "主机配置已变化，请重新选择主机",
            "VERSION_CONFLICT",
            "",
            new Date().toISOString(),
          );
        if (checked.update?.status === "AVAILABLE") {
          const update = automaticPreparationUpdate(checked);
          if (!update) throw new Error("组件更新信息不完整，请重新预检");
          automaticUpdate.value = update;
          preparationOpen.value = true;
          return;
        }
        if (checked.update && checked.update.status !== "CURRENT")
          throw new ApiError(
            "接入组件需要核对，请查看主机安装准备",
            checked.update.code || "EXECUTOR_UPGRADE_REQUIRED",
            "",
            new Date().toISOString(),
          );
      }
      throw cause;
    }
  });
}
async function submit() {
  await act(async () => {
    const d = draft.value!,
      env = environment.value,
      token = revision;
    if (!submission.value) validateDraft();
    if (!submission.value && d.action === "RELEASE") {
      if (d.confirmationName !== env?.name)
        throw new Error("请输入完整环境名称确认");
      submission.value = {
        path: "/node-environments/" + env.id + "/release",
        body: {
          expectedVersion: env.version,
          confirmed: true,
          confirmationName: d.confirmationName,
        },
      };
    } else if (!submission.value && d.action === "UNINSTALL_NODE") {
      const row = nodeVersionRow(env, d.nodeVersion);
      if (!row) throw new Error("软件记录已变化，请重新检测");
      if (
        d.nodeVersion === env?.observedDefaultVersion &&
        ((env.observation?.versions.length || 0) > 1 || !d.clearNodeDefault)
      )
        throw new Error("请先切换默认版本；最后一个版本需明确清除默认值");
      if (
        d.deleteData &&
        (!row.nodeDataPaths?.length ||
          row.nodeDataPaths.some(
            (p) => !p.startsWith(env!.rootPath + "/") || p.includes("/../"),
          ))
      )
        throw new Error("未确认清理目录，请重新检测");
      if (d.confirmationName !== row.name)
        throw new Error("请输入完整软件名称确认");
      submission.value = {
        path: "/software-instances/" + row.id + "/actions",
        body: {
          action: "UNINSTALL",
          expectedVersion: row.version,
          idempotencyKey: submitKey.value,
          confirmed: true,
          deleteData: d.deleteData,
          clearNodeDefault: d.clearNodeDefault,
          confirmationName: row.name,
        },
      };
    } else if (!submission.value) {
      if (!plan.value || Date.parse(plan.value.expiresAt) <= Date.now())
        throw new Error("计划已过期，请重新检查");
      submission.value = {
        path:
          (d.action === "INSTALL_NODE"
            ? "/software-install-plans/"
            : "/node-environment-plans/") +
          plan.value.id +
          "/submit",
        body: {
          idempotencyKey: submitKey.value,
          ...(d.action !== "INSTALL_NODE" ? { confirmed: true } : {}),
        },
      };
    }
    if (!attempted.value && d.action === "INITIALIZE") {
      const context = plan.value?.change?.context,
        definition = initialRecipe.value,
        material = initialMaterials.value.find(
          (m) => m.id === d.initialNodeMaterialId,
        );
      if (!context || !definition || !plan.value?.environmentId)
        throw new Error("初始化预检缺少账号与目录身份，请重新检查");
      initialIntent.value = JSON.parse(
        JSON.stringify({
          hostId: currentHost.value.id,
          hostVersion: currentHost.value.version,
          environmentId: plan.value.environmentId,
          context,
          definition,
          source:
            d.source === "OFFICIAL"
              ? { type: "OFFICIAL" }
              : {
                  type: "ARCHIVE",
                  materialId: material!.id,
                  sha256: material!.sha256,
                  byteSize: material!.byteSize,
                },
          makeDefault: d.makeDefault,
          expiresAt: plan.value.expiresAt,
          initializationJobId: "",
          submitKey: crypto.randomUUID(),
        }),
      );
    }
    attempted.value = true;
    let result: {
      jobId?: string;
      environmentId?: string;
    } | null;
    try {
      result = await request<typeof result>(submission.value.path, "POST", submission.value.body);
    } catch (cause) {
      // This endpoint returns these conflicts before creating a job. Transport
      // errors and other endpoints retain the original request for reconciliation.
      if (token === revision && !disposed &&
          submission.value.path.startsWith("/node-environment-plans/") &&
          cause instanceof ApiError && cause.status === 409 &&
          ["NODE_PLAN_STALE", "HOST_CHANGED", "NODE_ENVIRONMENT_CHANGED",
           "NODE_ENVIRONMENT_DRIFT", "NODE_REGISTRY_CHANGED", "NODE_REGISTRY_ACTOR_CHANGED",
           "NODE_MATERIAL_MISSING", "NODE_MATERIAL_MISMATCH", "OPERATION_LOCKED", "QUEUE_FULL",
           "NODE_VERSION_EXISTS", "NODE_VERSION_NOT_MANAGED", "NODE_ENVIRONMENT_NOT_EMPTY"].includes(cause.code)) {
        attempted.value = false;
        submission.value = undefined;
        plan.value = undefined;
        initialIntent.value = undefined;
        submitKey.value = crypto.randomUUID();
      }
      throw cause;
    }
    if (token !== revision || disposed) return;
    if (initialIntent.value && result?.jobId)
      initialIntent.value.initializationJobId = result.jobId;
    if (result?.environmentId) selectedId.value = result.environmentId;
    draft.value = undefined;
    plan.value = undefined;
    await refresh();

    emit("changed");
  });
}
async function observe() {
  pollFailures = 0;
  await act(async () => {
    const env = environment.value;
    if (!env) return;
    await request("/node-environments/" + env.id + "/observe", "POST", {
      expectedVersion: env.version,
    });
    await refresh();
  });
}
function openDiscovery() {
  discoveryOpen.value = true;
  discoveryAccount.value =
    currentHost.value.sshUser === "root" ? "" : currentHost.value.sshUser || "";
  discoveryPath.value = "";
  discovery.value = undefined;
  error.value = undefined;
}
async function discover() {
  await act(async () => {
    const token = revision;
    const result = await request<NodeDiscovery>(
      "/hosts/" + currentHost.value.id + "/node-environments/discover",
      "POST",
      {
        expectedHostVersion: currentHost.value.version,
        account: discoveryAccount.value.trim(),
        rootPath: discoveryPath.value.trim() || undefined,
      },
    );
    if (token === revision) discovery.value = result;
  });
}
async function register(candidate: NodeObservation) {
  await act(async () => {
    const token = revision;
    const result = await request<NodeEnvironment>(
      "/hosts/" + currentHost.value.id + "/node-environments/register",
      "POST",
      {
        expectedHostVersion: currentHost.value.version,
        name: candidate.account + " 的 Node 环境",
        candidate,
      },
    );
    if (token !== revision) return;
    selectedId.value = result.id;
    discoveryOpen.value = false;
    await refresh();
    emit("changed");
  });
}
async function jobAction(action: "reconcile" | "cancel") {
  await act(async () => {
    if (!job.value) return;
    await request("/jobs/" + job.value.id + "/" + action, "POST", {});
    await refresh();
  });
}
async function showJob(item: SoftwareJob) {
  await act(async () => {
    const token = revision;
    const result = await request<{ text: string }>(
      "/jobs/" + item.id + "/logs",
    );
    if (token === revision) {
      logsJobId.value = item.id;
      logs.value = result.text || "暂无日志";
      tab.value = "history";
    }
  });
}
async function updatedPreparation() {
  const token = revision,
    hostId = currentHost.value.id;
  let ready = false;
  await act(async () => {
    const hosts = await request<Host[]>("/hosts");
    if (token !== revision || disposed) return;
    const host = hosts.find((h) => h.id === hostId);
    if (!host || host.capabilities?.mutation.status !== "AVAILABLE") return;
    currentHost.value = host;
    await refresh();
    if (token !== revision || disposed) return;
    preparationOpen.value = false;
    ready = true;
  });
  if (ready && draft.value && !attempted.value) await runPreflight(false);
}
function poll() {
  timer = setTimeout(async () => {
    if (
      !disposed &&
      !document.hidden &&
      !busy.value &&
      pending.value &&
      pollFailures < 3
    ) {
      try {
        await loadDetail();
        pollFailures = 0;
      } catch (e) {
        pollFailures++;
        report(e);
      }
    }
    if (!disposed) poll();
  }, 4000);
}
watch(
  () => props.host.id,
  () => {
    revision++;
    automaticUpdate.value = undefined;
    preparationOpen.value = false;
    initialIntent.value = undefined;
    continuationRetry.value = undefined;
    currentHost.value = props.host;
    selectedId.value = props.initialEnvironmentId || "";
    environment.value = undefined;
    draft.value = undefined;
    discoveryOpen.value = false;
    job.value = undefined;
    busy.value = false;
    void act(refresh);
  },
  { immediate: true },
);
watch(
  () => props.initialEnvironmentId,
  (id) => {
    if (id && id !== selectedId.value && !pending.value) {
      selectedId.value = id;
      void act(() => loadDetail());
    }
  },
);
watch(
  () => props.host.version,
  () => {
    currentHost.value = props.host;
  },
);
onUnmounted(() => {
  disposed = true;
  revision++;
  if (timer) clearTimeout(timer);
});
poll();
</script>

<template>
  <section class="node-environments" aria-label="Node.js 环境管理">
    <header class="node-header">
      <div class="actions">
        <button class="secondary" :disabled="busy" @click="openDiscovery">
          检测已有安装</button
        ><button
          class="primary"
          :disabled="busy || pending || currentHost.maintenance"
          @click="openAction('INITIALIZE')"
        >
          新建 Node 环境
        </button>
      </div>
    </header>
    <p v-if="error && !draft && !discoveryOpen" role="alert" class="error">
      {{ error.message }}
      <button
        v-if="
          ['NODE_CAPABILITY_REQUIRED', 'NODE_TERMINAL_CAPABILITY_REQUIRED', 'NODE_REGISTRY_CAPABILITY_REQUIRED', 'EXECUTOR_UPGRADE_REQUIRED'].includes(
            error.code,
          )
        "
        class="quiet"
        @click="
          automaticUpdate = undefined;
          preparationOpen = true;
        "
      >
        更新接入组件
      </button>
    </p>
    <details v-if="error && !draft && !discoveryOpen">
      <summary>诊断信息</summary>
      <ErrorHelpLink :code="error.code" />
      <code
        >{{ error.code }} · {{ hostTime(error.at) }} · {{ error.traceId }} ·
        {{ currentHost.id }}</code
      >
    </details>
    <div v-if="active.length" class="environment-picker">
      <label
        >当前环境<select
          v-model="selectedId"
          aria-label="当前 Node 环境"
          :disabled="busy"
          @change="act(() => loadDetail())"
        >
          <option v-for="item in active" :key="item.id" :value="item.id">
            {{ item.name }} · {{ item.account
            }}{{ item.managementMode === "OBSERVE" ? " · 只读登记" : "" }}
          </option>
        </select></label
      ><button class="secondary" :disabled="busy || loading" @click="observe">
        检测环境
      </button>
    </div>
    <p v-else class="empty">
      {{ loading ? "正在读取环境…" : "暂无 Node 环境" }}
    </p>
    <p v-if="continuationRetry" class="hint">
      Node 提交结果尚未确认。<button
        class="secondary"
        :disabled="busy"
        @click="retryContinuation"
      >
        核对 Node 提交结果
      </button>
    </p>
    <template v-if="environment">
      <div class="environment-summary">
        <div>
          <span>执行账号</span><strong>{{ environment.account }}</strong>
        </div>
        <div>
          <span>实际默认</span
          ><strong>{{ environment.observedDefaultVersion || "未设置" }}</strong>
          <span>期望：{{ desiredDefault }}</span>
        </div>
        <div>
          <span>管理方式</span
          ><strong
            >{{
              environment.managementMode === "MANAGED" ? "Ops 管理" : "只读登记"
            }}
            ·
            {{ environment.managerType === "NVM" ? "nvm" : "系统安装" }}</strong
          >
        </div>
        <div>
          <span>上次检查</span
          ><strong>{{ hostTime(environment.observedAt) }}</strong>
        </div>
      </div>
      <div v-if="environment.managerType === 'NVM'" class="terminal-access">
        <span>{{
          environment.observation?.terminal?.status === "CONNECTED"
            ? "Bash 已接入"
            : environment.observation?.terminal?.status === "CONFLICT"
              ? "Bash 配置冲突"
              : environment.observation?.terminal?.status === "UNSUPPORTED"
                ? "当前终端不支持自动接入"
                : environment.observation?.terminal?.status === "NOT_CONNECTED" ? "Bash 未接入" : "终端接入待检查"
        }}</span>
        <button
          v-if="environment.managementMode === 'MANAGED' && environment.observation?.terminal?.status !== 'CONNECTED'"
          class="secondary"
          :disabled="busy || !writable"
          @click="openAction('CONNECT_TERMINAL')"
        >
          接入终端
        </button>
        <span v-if="!environment.observedDefaultVersion && environment.observation?.terminal?.status === 'CONNECTED'" class="hint"
          >选择默认版本后，新终端可使用 node/npm</span
        >
        <span
          v-else-if="environment.observation?.terminal?.status === 'CONNECTED'"
          class="hint"
          >重新登录即可使用 node/npm</span
        >
      </div>
      <details v-if="environment.errorCode" class="hint">
        <summary>检测未完成，显示上次结果</summary>
        <ErrorHelpLink :code="environment.errorCode" /><code>{{
          environment.errorCode
        }}</code>
        <p>{{ hostTime(environment.observedAt) }} · {{ environment.id }}</p>
      </details>
      <div class="node-tabs" role="tablist" aria-label="Node 环境详情">
        <button
          v-for="entry in [
            { key: 'versions', name: '版本列表' },
            { key: 'sources', name: '来源与目录' },
            { key: 'registry', name: 'npm 镜像' },
            { key: 'history', name: '操作记录' },
          ]"
          :key="entry.key"
          role="tab"
          :aria-selected="tab === entry.key"
          :class="{ active: tab === entry.key }"
          @click="tab = entry.key"
        >
          {{ entry.name }}
        </button>
      </div>
      <section v-if="tab === 'versions'" class="version-list">
        <div class="list-actions">
          <button
            class="primary"
            :disabled="busy || !writable"
            @click="openAction('INSTALL_NODE')"
          >
            安装 Node 版本
          </button>
        </div>
        <article
          v-for="item in versions"
          :key="item.version"
          class="node-version"
        >
          <div class="version-title">
            <strong>{{ item.version }}</strong
            ><span
              v-if="item.version === environment.observedDefaultVersion"
              class="default-badge"
              >默认</span
            >
          </div>
          <div class="package-versions">
            <span
              >npm <b>{{ item.npmVersion || "未确认" }}</b></span
            ><span
              >pnpm <b>{{ item.pnpmVersion || "未安装" }}</b></span
            >
          </div>
          <code>{{ item.binaryPath }}</code>
          <div class="actions">
            <button
              class="quiet"
              :disabled="
                busy ||
                !writable ||
                item.version === environment.observedDefaultVersion
              "
              @click="openAction('SET_DEFAULT', item.version)"
            >
              设为默认</button
            ><button
              class="quiet"
              :disabled="busy || !writable"
              @click="openAction('UPDATE_NPM', item.version)"
            >
              更新 npm</button
            ><button
              class="quiet"
              :disabled="busy || !writable"
              @click="
                openAction(
                  item.pnpmVersion ? 'UPDATE_PNPM' : 'INSTALL_PNPM',
                  item.version,
                )
              "
            >
              {{ item.pnpmVersion ? "更新 pnpm" : "安装 pnpm" }}</button
            ><button
              v-if="item.pnpmVersion"
              class="quiet"
              :disabled="busy || !writable"
              @click="openAction('REMOVE_PNPM', item.version)"
            >
              移除 pnpm</button
            ><button
              class="quiet danger"
              :disabled="
                busy || !writable || !nodeVersionRow(environment, item.version)
              "
              @click="openAction('UNINSTALL_NODE', item.version)"
            >
              卸载版本
            </button>
          </div>
          <details v-if="item.globalPackages?.length">
            <summary>全局工具（{{ item.globalPackages.length }}）</summary>
            <p v-for="pkg in item.globalPackages" :key="pkg.name">
              <code>{{ pkg.name }}</code> {{ pkg.version }}
            </p>
          </details>
        </article>
        <p v-if="!versions.length" class="empty">
          {{
            environment.managementMode === "MANAGED"
              ? "尚未安装 Node 版本。"
              : "未检测到可用版本。"
          }}
        </p>
      </section>
      <section v-else-if="tab === 'sources'" class="environment-details">
        <details
          v-for="item in environment.versions.filter(
            (v) => v.installationStatus === 'READY',
          )"
          :key="item.id"
        >
          <summary>
            {{ item.name }} ·
            {{
              item.installation?.source?.type === "OFFICIAL"
                ? "官方在线安装"
                : item.installation?.source?.type === "ARCHIVE"
                  ? "Ops 离线安装"
                  : "自行安装，来源未确认"
            }}
          </summary>
          <p v-if="item.installation?.source?.sha256">
            <code>归档 SHA-256 {{ item.installation.source.sha256 }}</code>
          </p>
          <div
            v-for="part in item.installation?.definition?.native?.parts || []"
            :key="part.name"
            class="source-file"
          >
            <strong>{{ part.name }}</strong
            ><a :href="part.url" target="_blank" rel="noopener noreferrer">{{
              part.url
            }}</a
            ><code>SHA-256 {{ part.sha256 }}</code>
          </div>
        </details>
        <dl>
          <dt>环境目录</dt>
          <dd>
            <code>{{ environment.rootPath }}</code>
          </dd>
          <dt>nvm 版本</dt>
          <dd>{{ environment.nvmVersion || "不适用" }}</dd>
          <dt>所属账号</dt>
          <dd>{{ environment.account }} · UID {{ environment.accountUid }}</dd>
        </dl>
        <div class="actions">
          <button class="secondary" @click="terminalOpen = true">
            终端使用方式</button
          ><button
            v-if="environment.managerType === 'NVM'"
            class="secondary"
            :disabled="busy || !writable"
            @click="openAction('UPDATE_NVM')"
          >
            检查 nvm 更新</button
          ><button
            class="quiet"
            :disabled="busy || pending"
            @click="openAction('RELEASE')"
          >
            解除管理</button
          ><button
            class="quiet danger"
            :disabled="busy || !writable || !!versions.length"
            @click="openAction('REMOVE_ENVIRONMENT')"
          >
            移除空环境
          </button>
        </div>
      </section>
      <NodeRegistryPanel v-else-if="tab === 'registry'" :environment="environment" :disabled="busy || !writable" @select="selectRegistry" @close-state="registryCloseState = $event" />
      <section v-else class="node-history">
        <button
          v-for="item in jobs"
          :key="item.id"
          class="history-row"
          @click="showJob(item)"
        >
          <span>{{ softwareJobStatusLabel(item.status) }}</span
          ><time>{{ hostTime(item.updatedAt) }}</time
          ><span>查看日志</span>
        </button>
        <p v-if="!jobs.length" class="empty">暂无操作记录</p>
        <p v-if="logsJobId" class="hint">作业 {{ logsJobId }}</p>
        <pre v-if="logs">{{ logs }}</pre>
      </section>
      <div v-if="job" class="job-result" role="status">
        <strong>{{ softwareJobStatusLabel(job.status) }}</strong
        ><span>{{
          job.targetAckPending
            ? "正在等待目标确认收尾"
            : job.status === "UNKNOWN"
              ? "请核对原操作结果，避免重复执行"
              : ""
        }}</span
        ><p v-if="softwareJobErrorMessages[job.errorCode]">{{ softwareJobErrorMessages[job.errorCode] }}</p><button
          v-if="job.status === 'UNKNOWN' || job.targetAckPending"
          class="secondary"
          :disabled="busy"
          @click="jobAction('reconcile')"
        >
          检查操作结果</button
        ><button
          v-if="job.status === 'QUEUED'"
          class="secondary"
          :disabled="busy"
          @click="jobAction('cancel')"
        >
          取消排队
        </button>
        <details>
          <summary>诊断信息</summary>
          <ErrorHelpLink v-if="job.errorCode" :code="job.errorCode" />
          <p>
            {{ softwareJobDiagnostic(job.status, job.phase) }} ·
            {{ job.errorCode }} · {{ hostTime(job.updatedAt) }}
          </p>
          <code>{{ job.id }} · {{ job.traceId }}</code>
        </details>
      </div>
    </template>
    <NodeVersionDialog
      v-if="draft"
      :draft="draft"
      :environment="environment"
      :choices="choices"
      :initial-choices="catalog.node"
      :initial-recipe="initialRecipe"
      :initial-materials="initialMaterials"
      :materials="matchingMaterials"
      :recipe="recipe"
      :plan="plan"
      :busy="busy"
      :attempted="attempted"
      :error="error?.message"
      :error-code="error?.code"
      :diagnostic="
        error
          ? [error.code, hostTime(error.at), error.traceId, currentHost.id]
              .filter(Boolean)
              .join(' · ')
          : ''
      "
      :host="currentHost"
      :preparing="preparationOpen"
      :preparation-busy="preparationBusy"
      @close="preparationOpen = false; draft = undefined"
      @back="preparationOpen = false"
      @change="changedDraft"
      @preflight="preflight"
      @submit="submit"
      @prepare="
        automaticUpdate = undefined;
        preparationOpen = true;
      "
      @material="materialReady"
      @initial-material="initialMaterialReady"
      @material-busy="busy = $event"
    >
      <template #preparation>
        <HostPreparationPanel
          v-if="preparationOpen"
          :host="currentHost"
          :automatic-update="automaticUpdate"
          update-only
          @close-blocked="preparationBusy = $event"
          @updated="updatedPreparation"
        />
      </template>
    </NodeVersionDialog>
    <ElDialog
      v-model="discoveryOpen"
      title="检测已有 Node 安装"
      width="min(680px,94vw)"
      :close-on-click-modal="false"
      align-center
      ><div class="discovery-form">
        <label
          >执行账号<input
            v-model="discoveryAccount"
            aria-label="检测 Node 账号"
            :disabled="busy" /></label
        ><label
          >指定目录（可选）<input
            v-model="discoveryPath"
            aria-label="检测 Node 目录"
            placeholder="留空检测标准位置"
            :disabled="busy" /></label
        ><button
          class="primary"
          :disabled="busy || !discoveryAccount.trim()"
          @click="discover"
        >
          {{ busy ? "检测中…" : "开始检测" }}
        </button>
        <p v-if="error" role="alert" class="error">
          {{ error.message }} <ErrorHelpLink :code="error.code" />
        </p>
        <article
          v-for="candidate in discovery?.candidates"
          :key="candidate.rootPath"
          class="candidate"
        >
          <strong>{{ candidate.managerType }} · {{ candidate.account }}</strong
          ><code>{{ candidate.rootPath }}</code>
          <p>
            {{
              candidate.versions.map((item) => item.version).join(" / ") ||
              "未发现 Node 版本"
            }}
          </p>
          <button
            class="secondary"
            :disabled="busy"
            @click="register(candidate)"
          >
            登记此环境
          </button>
        </article>
        <p v-if="discovery && !discovery.candidates.length" class="empty">
          未发现可登记的 Node 环境
        </p>
      </div></ElDialog
    >
    <ElDialog
      v-model="terminalOpen"
      title="终端使用方式"
      width="min(660px,94vw)"
      align-center
      ><p class="hint">
        使用 {{ environment?.account }} 账号执行；只作用于当前终端。
      </p>
      <details>
        <summary>当前终端临时加载</summary>
        <pre v-if="environment">{{ nodeTerminalCommand(environment) }}</pre>
      </details>
    </ElDialog>
    <ElDialog
      v-if="!draft"
      v-model="preparationOpen"
      append-to-body
      :title="automaticUpdate ? '正在准备安装环境' : '更新接入组件'"
      width="min(720px,94vw)"
      align-center
      :close-on-click-modal="false"
      :close-on-press-escape="false"
      ><HostPreparationPanel
        v-if="preparationOpen"
        :host="currentHost"
        :automatic-update="automaticUpdate"
        update-only
        @updated="updatedPreparation"
    /></ElDialog>
  </section>
</template>

<style scoped>
.node-environments {
  display: grid;
  gap: 22px;
  min-width: 0;
}
.node-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
}
.eyebrow {
  font-size: 11px;
  color: var(--muted);
  letter-spacing: 0.08em;
}
h2 {
  font-size: 24px;
  margin: 7px 0 8px;
  font-weight: 600;
}
.node-header p,
.hint {
  color: var(--muted);
  font-size: 12px;
  line-height: 1.8;
  margin: 0;
}
.actions {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
  align-items: center;
}
.environment-picker {
  display: flex;
  align-items: end;
  gap: 12px;
}
.environment-picker label,
.discovery-form label {
  display: grid;
  gap: 8px;
  font-size: 12px;
}
.environment-picker label {
  flex: 1;
}
select,
input {
  min-width: 0;
  padding: 9px 11px;
  border: 1px solid var(--border);
  border-radius: 5px;
  background: var(--panel, #fff);
  color: var(--text);
  box-sizing: border-box;
}
.terminal-access {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 10px;
  margin: 14px 0;
  font-size: 13px;
}
.environment-summary {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 20px;
  padding: 20px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--panel, #fff);
}
.environment-summary div {
  display: grid;
  gap: 8px;
}
.environment-summary span {
  font-size: 11px;
  color: var(--muted);
}
.environment-summary strong {
  font-size: 13px;
  overflow-wrap: anywhere;
}
.node-tabs {
  display: flex;
  gap: 22px;
  border-bottom: 1px solid var(--border);
}
.node-tabs button {
  border: 0;
  border-bottom: 2px solid transparent;
  background: none;
  padding: 11px 0;
  color: var(--muted);
  cursor: pointer;
}
.node-tabs button.active {
  color: var(--accent);
  border-color: var(--accent);
}
.version-list {
  display: grid;
  gap: 14px;
}
.list-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}
.list-actions p {
  font-size: 12px;
  color: var(--muted);
}
.node-version {
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 20px;
  display: grid;
  gap: 15px;
  background: var(--panel, #fff);
}
.version-title {
  display: flex;
  gap: 12px;
  align-items: center;
}
.version-title strong {
  font: 600 18px var(--mono);
}
.default-badge {
  font-size: 11px;
  background: #eef5ed;
  color: #447048;
  padding: 3px 8px;
  border-radius: 4px;
}
.package-versions {
  display: flex;
  gap: 30px;
  font-size: 12px;
  color: var(--muted);
}
.package-versions b {
  color: var(--text);
  font-weight: 500;
  margin-left: 6px;
}
code,
pre {
  font: 11px/1.8 var(--mono);
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
pre {
  padding: 14px;
  background: #f5f7fa;
  border-radius: 5px;
}
.node-version > code {
  color: var(--muted);
}
.node-version details {
  font-size: 12px;
  border-top: 1px solid var(--border);
  padding-top: 12px;
}
summary {
  cursor: pointer;
  font-size: 12px;
}
.empty {
  padding: 30px;
  color: var(--muted);
  font-size: 13px;
  text-align: center;
  border: 1px dashed var(--border);
  border-radius: 6px;
  line-height: 1.8;
}
.environment-details {
  display: grid;
  gap: 20px;
}
.source-file {
  display: grid;
  gap: 6px;
  margin-top: 14px;
  font-size: 12px;
  overflow-wrap: anywhere;
}
.source-file a {
  color: var(--accent);
}
dl {
  display: grid;
  grid-template-columns: 80px minmax(0, 1fr);
  gap: 16px;
  font-size: 12px;
}
dt {
  color: var(--muted);
}
dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.job-result {
  padding: 18px;
  background: #f6f8fb;
  border: 1px solid var(--border);
  border-radius: 6px;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  font-size: 12px;
}
.job-result details {
  width: 100%;
}
.error,
.danger {
  color: var(--danger, #a74430);
}
.error {
  font-size: 13px;
  line-height: 1.8;
}
.discovery-form {
  display: grid;
  gap: 16px;
}
.candidate {
  border-top: 1px solid var(--border);
  padding-top: 16px;
  display: grid;
  gap: 10px;
}
.candidate p {
  margin: 0;
  font-size: 12px;
}
.history-row {
  width: 100%;
  display: flex;
  justify-content: space-between;
  gap: 12px;
  padding: 15px;
  border: 0;
  border-bottom: 1px solid var(--border);
  background: none;
  color: var(--text);
  text-align: left;
  cursor: pointer;
}
.history-row time {
  color: var(--muted);
  font-size: 12px;
}
@media (max-width: 600px) {
  .node-header {
    align-items: flex-start;
    flex-direction: column;
  }
  .environment-summary {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 18px;
    padding: 16px;
  }
  .list-actions {
    align-items: flex-start;
    flex-direction: column;
  }
  .node-version {
    padding: 16px;
  }
  .actions {
    gap: 8px;
  }
  .node-tabs {
    gap: 20px;
  }
  .history-row {
    flex-wrap: wrap;
  }
}
</style>
