<script setup lang="ts">
import { computed, onDeactivated, onUnmounted, ref, watch } from "vue";
import { ApiError, request } from "./api";
import { usePagination } from "./pagination";
import type { ListPage } from "./listPages";
import {
  automaticPreparationUpdate,
  type AutomaticPreparationUpdate,
  type PreparationUpdateCheck,
} from "./preparationUpdate";
import { hostTime, type Host } from "./hosts";
import type { InstallationMaterial } from "./serviceInstallation";
import type { SoftwareJob } from "./software";
import ErrorHelpLink from "./ErrorHelpLink.vue";
import NodeEnvironmentContent from "./NodeEnvironmentContent.vue";
import NodeEnvironmentDialogs from "./NodeEnvironmentDialogs.vue";
import {
  nodeInitialPlanMatches,
  type NodeInitialIntent,
  nodeJobPending,
  nodeMaterials,
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
import "./node-environment.css";

const props = defineProps<{ host: Host; initialEnvironmentId?: string }>();
const emit = defineEmits<{ changed: []; closeState: [state: { blocked: boolean; dirty: boolean; hostChangeBlocked: boolean }] }>();
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
const actionOpen = ref(false);
const plan = ref<NodePlan>();
const initialIntent = ref<NodeInitialIntent>();
const continuationRetry = ref<{ path: string; key: string }>();
let continuingInitial = false;
const attempted = ref(false);
const submitKey = ref("");
const submission = ref<{ path: string; body: unknown }>();
const jobs = ref<SoftwareJob[]>([]);
const historyPage = ref(1), historyPageSize = ref(10), historyTotal = ref(0);
let historyRevision = 0;
const job = ref<SoftwareJob>();
const logs = ref("");
const logsJobId = ref("");
watch(selectedId, () => {
  logs.value = "";
  logsJobId.value = "";
  historyPage.value = 1;
  historyTotal.value = 0;
  jobs.value = [];
  historyRevision++;
}, { flush: "sync" });
const tab = ref("versions");
const discoveryOpen = ref(false);
const discoveryAccount = ref("");
const discoveryPath = ref("");
const discovery = ref<NodeDiscovery>();
const terminalOpen = ref(false);
const preparationOpen = ref(false);
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
const { page: versionPage, pageSize: versionPageSize, total: versionTotal, pageItems: pagedVersions } = usePagination(
  () => versions.value, [selectedId],
);
const readyVersions = computed(() => environment.value?.versions.filter(item => item.installationStatus === "READY") || []);
const { page: sourcePage, pageSize: sourcePageSize, total: sourceTotal, pageItems: pagedSources } = usePagination(
  () => readyVersions.value, [selectedId],
);
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
watch(() => [busy.value, attempted.value, pending.value, initialIntent.value, continuationRetry.value, !!draft.value, discoveryOpen.value, preparationOpen.value, terminalOpen.value, registryCloseState.value], () => emit("closeState", {
  blocked: false,
  dirty: !!draft.value || registryCloseState.value.dirty,
  hostChangeBlocked: !!initialIntent.value || !!continuationRetry.value,
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
  const [detail] = await Promise.all([
    request<NodeEnvironment>("/node-environments/" + id),
    loadHistory(historyPage.value, historyPageSize.value, id),
  ]);
  if (token !== revision || disposed || id !== selectedId.value) return;
  environment.value = detail;
  if (detail.lastJobId) {
    const latestJob = await request<SoftwareJob>("/jobs/" + detail.lastJobId);
    if (token === revision && !disposed && id === selectedId.value)
      job.value = latestJob;
  } else job.value = undefined;
  await continueInitialInstallation();
}
async function loadHistory(page = historyPage.value, pageSize = historyPageSize.value, id = selectedId.value) {
  if (!id) return;
  const token = revision, generation = ++historyRevision;
  const read = (value: number) => request<ListPage<SoftwareJob>>(`/node-environments/${id}/jobs?page=${value}&pageSize=${pageSize}`);
  let result = await read(page);
  if (token !== revision || disposed || generation !== historyRevision || id !== selectedId.value) return;
  const lastPage = Math.max(1, Math.ceil(result.total / pageSize));
  if (page > lastPage) { page = lastPage; result = await read(page); }
  if (token !== revision || disposed || generation !== historyRevision || id !== selectedId.value) return;
  jobs.value = result.items;
  historyTotal.value = result.total;
  historyPage.value = page;
  historyPageSize.value = pageSize;
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
  actionOpen.value = true;
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
function closeAction() {
  actionOpen.value = false;
  preparationOpen.value = false;
  if (!busy.value && !attempted.value) {
    draft.value = undefined;
    plan.value = undefined;
    automaticUpdate.value = undefined;
  }
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
      if (token === revision && !disposed && draft.value === d) plan.value = result;
    } catch (cause) {
      if (token !== revision || disposed || draft.value !== d) return;
      if (allowAutomaticUpdate && currentHost.value.kind === "SSH" && cause instanceof ApiError && ["NODE_CAPABILITY_REQUIRED", "NODE_TERMINAL_CAPABILITY_REQUIRED", "NODE_REGISTRY_CAPABILITY_REQUIRED", "EXECUTOR_UPGRADE_REQUIRED"].includes(cause.code)) {
        const checked = await request<PreparationUpdateCheck>(
          `/hosts/${currentHost.value.id}/preparation?checkUpdate=1`,
        );
        if (token !== revision || disposed || draft.value !== d) return;
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
    actionOpen.value = false;
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
onDeactivated(() => {
  closeAction();
  discoveryOpen.value = false;
  terminalOpen.value = false;
});
poll();
</script>

<template>
  <section class="node-environments" aria-label="Node.js 环境管理">
    <header class="node-header">
      <div class="actions">
        <button v-if="draft && !actionOpen" class="secondary" @click="actionOpen = true">查看当前操作</button>
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
    <NodeEnvironmentContent
      v-if="environment"
      v-model:tab="tab"
      v-model:version-page="versionPage"
      v-model:version-page-size="versionPageSize"
      v-model:source-page="sourcePage"
      v-model:source-page-size="sourcePageSize"
      :environment="environment"
      :desired-default="desiredDefault"
      :busy="busy"
      :writable="writable"
      :pending="pending"
      :versions="versions"
      :paged-versions="pagedVersions"
      :version-total="versionTotal"
      :paged-sources="pagedSources"
      :source-total="sourceTotal"
      :jobs="jobs"
      :history-page="historyPage"
      :history-page-size="historyPageSize"
      :history-total="historyTotal"
      :logs-job-id="logsJobId"
      :logs="logs"
      :job="job"
      @open-action="openAction"
      @open-terminal="terminalOpen = true"
      @select-registry="selectRegistry"
      @registry-close-state="registryCloseState = $event"
      @show-job="showJob"
      @change-history-page="act(() => loadHistory($event))"
      @change-history-page-size="act(() => loadHistory(1, $event))"
      @job-action="jobAction"
    />
    <NodeEnvironmentDialogs
      v-model:preparation-open="preparationOpen"
      v-model:discovery-open="discoveryOpen"
      v-model:discovery-account="discoveryAccount"
      v-model:discovery-path="discoveryPath"
      v-model:terminal-open="terminalOpen"
      :draft="draft"
      :action-open="actionOpen"
      :environment="environment"
      :choices="choices"
      :initial-choices="catalog.node"
      :initial-recipe="initialRecipe"
      :initial-materials="initialMaterials"
      :matching-materials="matchingMaterials"
      :recipe="recipe"
      :plan="plan"
      :busy="busy"
      :attempted="attempted"
      :error="error"
      :current-host="currentHost"
      :automatic-update="automaticUpdate"
      :discovery="discovery"
      @close-action="closeAction"
      @changed-draft="changedDraft"
      @preflight="preflight"
      @submit="submit"
      @prepare="
        automaticUpdate = undefined;
        preparationOpen = true;
      "
      @material-ready="materialReady"
      @initial-material-ready="initialMaterialReady"
      @material-busy="busy = $event"
      @updated-preparation="updatedPreparation"
      @discover="discover"
      @register="register"
    />
  </section>
</template>
