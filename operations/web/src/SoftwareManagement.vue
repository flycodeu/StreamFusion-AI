<script setup lang="ts">
import { computed, onActivated, onDeactivated, onMounted, onUnmounted, ref, watch } from "vue";
import {
  ElButton,
  ElDescriptions,
  ElDescriptionsItem,
  ElDrawer,
  ElDropdown,
  ElDropdownItem,
  ElDropdownMenu,
  ElEmpty,
  ElInput,
  ElMessage,
  ElMessageBox,
  ElOption,
  ElPagination,
  ElRadioButton,
  ElRadioGroup,
  ElSelect,
  ElTable,
  ElTableColumn,
  ElTag,
} from "element-plus";
import { request } from "./api";
import OpsPagination from "./OpsPagination.vue";
import { useGridPagination, usePagination } from "./pagination";
import { requestAllPages } from "./listPages";
import { hostTime, type Host } from "./hosts";
import ObservationBatchPanel from "./ObservationBatchPanel.vue";
import type { ObservationBatch } from "./observation";
import SoftwareIcon from "./SoftwareIcon.vue";
import SoftwareRecipes from "./SoftwareRecipes.vue";
import NodeEnvironmentPanel from "./NodeEnvironmentPanel.vue";
import GitManagementPanel from "./GitManagementPanel.vue";
import SoftwareDetailsDialog from "./SoftwareDetailsDialog.vue";
import SoftwareWebAccess from "./SoftwareWebAccess.vue";
import SoftwareUninstallDialog from "./SoftwareUninstallDialog.vue";
import SoftwareRegistrationDialog from "./SoftwareRegistrationDialog.vue";
import SoftwareDiscoveryDialog from "./SoftwareDiscoveryDialog.vue";
import SoftwareConfigurationDialog from "./SoftwareConfigurationDialog.vue";
import { softwareConfigurationAvailable } from "./softwareManagement";
import {
  softwareModes,
  softwareRuntimeLabels,
  softwareState,
  softwareIsTool,
  softwareIsDockerEngine,
  softwareInstallationReady,
  softwareLifecycleAvailable,
  softwareTypeLabel,
  softwareVersionSummary,
  softwareHasInstallationRecord,
  softwareIsHistoryOnly,
  softwareUninstallAvailable,
  softwareVersionText,
  softwareVersionWithoutDigest,
  cleanObservedVersionText,
  type SoftwareInstance,
  type SoftwareJob,
  type SoftwareMutationResult,
  type SoftwareAction,
  type SoftwareRegistrationDraft,
} from "./software";

const emit = defineEmits<{
  error: [unknown];
  preparation: [string];
  navigate: [view: "manage" | "install"];
  logs: [target: { jobId?: string; softwareId?: string }];
  selectionOpened: [string];
}>();
const props = withDefaults(defineProps<{ view?: "manage" | "install"; dockerHostId?: string; installHostId?: string; selectedSoftwareId?: string; installSearch?: string }>(), {
  view: "manage",
});
const rows = ref<SoftwareInstance[]>([]);
const isActive = ref(true);
const hosts = ref<Host[]>([]);
const hostId = ref("");
const nodeEnvironmentId = ref("");
const nodeHostId = ref("");
const nodeOpen = ref(false);
const gitHostId = ref("");
const gitOpen = ref(false);
const gitCloseState = ref({ blocked: false, dirty: false });
const gitHost = computed(() => hosts.value.find((host) => host.id === gitHostId.value));
async function openGit(targetId: string) {
  await act(async () => {
    hosts.value = await request<Host[]>("/hosts");
    if (disposed || !isActive.value) return;
    gitHostId.value = targetId;
    gitOpen.value = true;
  });
}
async function closeGit(done: () => void) {
  if (gitCloseState.value.blocked) return;
  if (gitCloseState.value.dirty) {
    try { await ElMessageBox.confirm("放弃未保存的 Git 配置？", "关闭窗口", { confirmButtonText: "放弃修改", cancelButtonText: "继续编辑" }); } catch { return; }
  }
  done();
}
const nodeCloseState = ref({ blocked: false, dirty: false, hostChangeBlocked: false });
async function closeNode(done: () => void) {
  if (nodeCloseState.value.blocked) return;
  // The mounted panel retains its draft when this drawer is hidden.
  done();
}
const nodeHost = computed(() => hosts.value.find((host) => host.id === nodeHostId.value));
async function openNode(targetId: string, environmentId = "") {
  if (targetId !== nodeHostId.value && nodeCloseState.value.hostChangeBlocked) {
    ElMessage.info("当前主机正在完成 Node 安装，请完成或核对原操作后再切换主机");
    return;
  }
  await act(async () => {
    const latestHosts = await request<Host[]>("/hosts");
    if (disposed || !isActive.value) return;
    hosts.value = latestHosts;
    nodeHostId.value = targetId;
    nodeEnvironmentId.value = environmentId;
    nodeOpen.value = true;
  });
}
async function discoverInstalled(targetId: string) {
  await act(async () => {
    const latestHosts = await request<Host[]>("/hosts");
    if (disposed || !isActive.value) return;
    hosts.value = latestHosts;
    hostId.value = targetId;
    emit("navigate", "manage");
    discovery.value = true;
  });
}
watch(() => props.dockerHostId, (id) => {
  if (id) hostId.value = id;
}, { immediate: true });
const query = ref("");
const mode = ref("ACTIVE");
const loading = ref(false);
const batch = ref<ObservationBatch>();
const detectionScope = ref("FILTERED");
const detectionCount = computed(
  () =>
    (detectionScope.value === "ALL" ? rows.value : visible.value).filter(
      (i) => i.managementMode !== "RELEASED" && softwareLifecycleAvailable(i),
    ).length,
);
const busy = ref(false);
const observing = ref(false);
const registration = ref(false);
const discovery = ref(false);
const uninstallItem = ref<SoftwareInstance>();
const uninstallOpen = ref(false);
const dependentContainers = (item?: SoftwareInstance) => rows.value.filter((other) =>
  other.hostId === item?.hostId && other.runtimeKind === "DOCKER" && other.managementMode !== "RELEASED",
).map((other) => other.name);
const registrationPrefill = ref<SoftwareRegistrationDraft>();
const configurationItem = ref<SoftwareInstance>(), queuedConfiguration = ref<SoftwareInstance>();
function editConfiguration(item: SoftwareInstance) {
  if (detailId.value) { queuedConfiguration.value = item; selectDetail(""); }
  else configurationItem.value = item;
}
function detailClosed() { if (queuedConfiguration.value) { configurationItem.value = queuedConfiguration.value; queuedConfiguration.value = undefined; } }
const detailId = ref("");
const detailInitialTab = ref("overview");
const job = ref<SoftwareJob>();
let disposed = false;
let timer: ReturnType<typeof setTimeout> | undefined;
let refreshing: Promise<void> | undefined;
let generation = 0;
let detailRevision = 0;
let batchGeneration = 0;

function currentDetail(id: string, revision: number) {
  return !disposed && detailId.value === id && detailRevision === revision;
}

function selectDetail(id: string, tab = "overview") {
  detailRevision += 1;
  detailInitialTab.value = tab;
  detailId.value = id;
  job.value = undefined;
  return detailRevision;
}

const visible = computed(() => {
  const term = query.value.trim().toLocaleLowerCase();
  return rows.value.filter(
    (item) =>
      !softwareIsHistoryOnly(item) &&
      (!hostId.value || item.hostId === hostId.value) &&
      (mode.value === "ALL" ||
        (mode.value === "ACTIVE"
          ? item.managementMode !== "RELEASED"
          : item.managementMode === mode.value)) &&
      (!term ||
        `${item.name} ${softwareTypeLabel(item)} ${item.locator}`
          .toLocaleLowerCase()
          .includes(term)),
  );
});
const { grid: softwareGrid, page, pageSize, total, pageSizeOptions, pageItems: pagedSoftware } = useGridPagination(
  () => visible.value, [query, hostId, mode],
);
const displayMode = ref<"table" | "card">("table");
const expandedRowId = ref("");
const { page: tablePage, pageSize: tablePageSize, total: tableTotal, pageItems: tableRows } = usePagination(
  () => visible.value, [query, hostId, mode], 20,
);
const softwareCards = computed(() =>
  pagedSoftware.value.map((item) => ({ item, version: softwareVersionSummary(item) })),
);
function tableVersionText(item: SoftwareInstance): string {
  const raw = item.observation?.version || item.softwareVersion;
  const clean = softwareVersionText(softwareVersionWithoutDigest(raw));
  return clean || item.installation?.definition?.version || "";
}
function stateTagType(tone: string): "success" | "danger" | "warning" | "info" {
  if (tone === "state-success") return "success";
  if (tone === "state-error") return "danger";
  if (tone === "state-warning") return "warning";
  return "info";
}
function hasMoreActions(item: SoftwareInstance): boolean {
  return (
    (rowCanChange(item) && (item.observation?.state || item.actualState) === "RUNNING") ||
    (item.managementMode === "OBSERVE" && service(item)) ||
    softwareHasInstallationRecord(item) ||
    softwareUninstallAvailable(item)
  );
}
function runBatchCommand(cmd: string | number | Record<string, unknown>) {
  if (cmd === "detect-filtered") {
    detectionScope.value = "FILTERED";
    void detectAll();
  } else if (cmd === "detect-all") {
    detectionScope.value = "ALL";
    void detectAll();
  } else if (cmd === "discover") {
    discovery.value = true;
  }
}
function runRowCommand(item: SoftwareInstance, cmd: string | number | Record<string, unknown>) {
  if (cmd === "stop") void changeState(item, "STOP");
  else if (cmd === "adopt") void adopt(item);
  else if (cmd === "logs") emit("logs", { jobId: item.installationJobId || item.lastJobId });
  else if (cmd === "uninstall") {
    uninstallItem.value = item;
    uninstallOpen.value = true;
  }
}
function rowCanChange(item: SoftwareInstance) {
  return (
    softwareLifecycleAvailable(item) &&
    item.managementMode === "MANAGED" &&
    service(item) &&
    !hosts.value.find((host) => host.id === item.hostId)?.maintenance
  );
}
function itemHost(item: SoftwareInstance) {
  return hosts.value.find((h) => h.id === item.hostId);
}
function itemLocator(item: SoftwareInstance) {
  return item.runtimeKind === "DOCKER"
    ? item.observation?.containerName || item.locator.slice(0, 16)
    : item.locator;
}
function itemPortsSummary(item: SoftwareInstance): string[] {
  const mapped = item.observation?.portMappings || [];
  if (mapped.length) return mapped;
  const listening = item.observation?.listeningPorts || [];
  if (listening.length) return listening;
  const cfg = item.installation?.config;
  const ports = [
    ...(cfg?.hostPort ? [cfg.hostPort] : []),
    ...Object.values(cfg?.ports || {}).filter((p): p is number => !!p),
  ];
  return Array.from(new Set(ports)).map((p) => `${p}（配置）`);
}
function itemPaths(item: SoftwareInstance) {
  const saved = item.installationPaths;
  const official = item.installation?.official?.paths;
  const observed = item.observation;
  const binary =
    observed?.executablePath ||
    item.installationEvidence?.binaryPath ||
    official?.binary;
  const values = [
    { label: "安装根目录", value: saved?.root === "/" ? "" : saved?.root },
    { label: "配置路径", value: official?.config || saved?.config },
    { label: "数据目录", value: official?.data || saved?.data },
    { label: "日志目录", value: official?.logs || saved?.logs },
    { label: "程序路径", value: binary },
    { label: "程序目录", value: binary ? "" : saved?.bin },
    { label: "服务单元", value: observed?.serviceFile },
    { label: "Docker Root", value: observed?.dockerRootDir },
    { label: "服务日志", value: observed?.logPath },
  ];
  return values.filter((e): e is { label: string; value: string } => !!e.value);
}
async function copyPathValue(val: string) {
  try {
    await navigator.clipboard.writeText(val);
    ElMessage.success("已复制路径");
  } catch {
    ElMessage.info("请手动选中复制");
  }
}
const detail = computed(() =>
  rows.value.find((item) => item.id === detailId.value),
);
const detailHost = computed(() =>
  hosts.value.find((item) => item.id === detail.value?.hostId),
);
const pendingJob = computed(
  () =>
    (!!detail.value?.lastJobId && job.value?.id !== detail.value.lastJobId) ||
    (!!job.value &&
      (["QUEUED", "RUNNING", "UNKNOWN"].includes(job.value.status) ||
        job.value.targetAckPending)),
);
const active = (item: SoftwareInstance) => item.managementMode !== "RELEASED";
const service = (item: SoftwareInstance) => !softwareIsTool(item);
const canAdopt = (item: SoftwareInstance) =>
  softwareInstallationReady(item) &&
  item.managementMode === "OBSERVE" &&
  service(item) &&
  item.freshness === "FRESH" &&
  !item.errorCode &&
  !!item.observation?.identityHash &&
  !["UNKNOWN", "MISSING"].includes(item.actualState) &&
  !hosts.value.find((host) => host.id === item.hostId)?.maintenance;
const canChange = (item: SoftwareInstance) =>
  softwareLifecycleAvailable(item) &&
  item.managementMode === "MANAGED" &&
  service(item) &&
  !hosts.value.find((host) => host.id === item.hostId)?.maintenance &&
  !pendingJob.value;

function refresh(): Promise<void> {
  if (disposed) return Promise.resolve();
  if (!refreshing) refreshing = loadSnapshot().finally(() => { refreshing = undefined; });
  return refreshing;
}
async function refreshView() {
  try { await refresh(); }
  catch (error) { if (!disposed && isActive.value) emit("error", error); }
}
async function loadSnapshot() {
  if (disposed || loading.value) return;
  loading.value = true;
  const currentGeneration = generation;
  const currentBatchGeneration = batchGeneration;
  const revision = detailRevision;
  try {
    const [items, targetHosts] = await Promise.all([
      requestAllPages<SoftwareInstance>("/software-instances"),
      request<Host[]>("/hosts"),
    ]);
    if (disposed) return;
    if (currentGeneration === generation) rows.value = items;
    hosts.value = targetHosts;
    const latestBatch = await request<ObservationBatch | null>(
      "/observation-batches",
    );
    if (!disposed && currentBatchGeneration === batchGeneration)
      batch.value = latestBatch || undefined;
    const selected = detail.value;
    if (selected?.lastJobId && currentDetail(selected.id, revision)) {
      const refreshedJob = await request<SoftwareJob>(
        "/jobs/" + selected.lastJobId,
      );
      if (currentDetail(selected.id, revision) && detail.value?.lastJobId === selected.lastJobId)
        job.value = refreshedJob;
    }
  } finally {
    loading.value = false;
  }
}
async function act(action: () => Promise<void>) {
  if (disposed || busy.value) return;
  busy.value = true;
  try {
    await action();
  } catch (error) {
    if (!disposed) emit("error", error);
  } finally {
    busy.value = false;
  }
}
function update(item: SoftwareInstance) {
  generation += 1;
  rows.value = rows.value.some((row) => row.id === item.id)
    ? rows.value.map((row) => (row.id === item.id ? item : row))
    : [...rows.value, item];
}
async function showDetails(item: SoftwareInstance, tab = "overview") {
  if(item.softwareType === "NODE") { await openNode(item.hostId, item.nodeEnvironmentId || ""); return; }
  const revision = selectDetail(item.id, tab);
  if (!item.lastJobId) return;
  try {
    const selectedJob = await request<SoftwareJob>("/jobs/" + item.lastJobId);
    if (currentDetail(item.id, revision) && detail.value?.lastJobId === item.lastJobId)
      job.value = selectedJob;
  } catch (error) {
    if (currentDetail(item.id, revision) && detail.value?.lastJobId === item.lastJobId)
      emit("error", error);
  }
}
async function saved(item: SoftwareInstance) {
  update(item);
  emit("navigate", "manage");
  mode.value = "ACTIVE";
  query.value = "";
  hostId.value = item.hostId;
  await showDetails(item);
  ElMessage.success("已登记为只读软件");
}
async function openInstance(id: string, requested = false) {
  const item = await request<SoftwareInstance>(`/software-instances/${id}`);
  if (disposed || requested && (!isActive.value || props.selectedSoftwareId !== id)) return;
  update(item);
  hostId.value = item.hostId;
  mode.value = "ACTIVE";
  query.value = "";
  emit("navigate", "manage");
  await showDetails(item);
}
function openRegistration(prefill?: SoftwareRegistrationDraft) {
  registrationPrefill.value = prefill;
  discovery.value = false;
  registration.value = true;
}
async function detectAll() {
  batchGeneration += 1;
  await act(async () => {
    const result = await request<{ id: string }>(
      "/observation-batches",
      "POST",
      {
        kind: "SOFTWARE",
        hostId: detectionScope.value === "ALL" ? "" : hostId.value,
        query: detectionScope.value === "ALL" ? "" : query.value,
        mode: detectionScope.value === "ALL" ? "ACTIVE" : mode.value,
        idempotencyKey: crypto.randomUUID(),
      },
    );
    const resultBatch = await request<ObservationBatch>(
      "/observation-batches/" + result.id,
    );
    if (!disposed) batch.value = resultBatch;
  });
}
async function cancelDetection() {
  if (!batch.value) return;
  batchGeneration += 1;
  await act(async () => {
    const result = await request<ObservationBatch>(
      "/observation-batches/" + batch.value!.id + "/cancel",
      "POST",
      {},
    );
    if (!disposed) batch.value = result;
  });
}
async function observe(item: SoftwareInstance) {
  if (busy.value || !softwareLifecycleAvailable(item)) return;
  if (detailId.value !== item.id) selectDetail(item.id);
  observing.value = true;
  try {
    await act(async () => {
      const revision = generation;
      const updated = await request<SoftwareInstance>(
        `/software-instances/${item.id}/observe`,
        "POST",
        {},
      );
      if (!disposed && revision === generation) update(updated);
    });
  } finally { observing.value = false; }
}
async function mutationDone(result: SoftwareMutationResult, revision: number) {
  generation += 1;
  const acceptedJob = result.jobId
    ? await request<SoftwareJob>("/jobs/" + result.jobId)
    : undefined;
  if (currentDetail(result.softwareInstanceId, revision)) {
    detailRevision += 1;
    job.value = acceptedJob;
  }
  // Let a pre-submission read settle before fetching the committed snapshot.
  if (refreshing) await refreshing.catch(() => {});
  await refresh();
}
async function configurationSubmitted(receipt: SoftwareMutationResult) {
  if (disposed) return;
  const revision = detailRevision;
  try { await mutationDone(receipt, revision); }
  catch (error) { if (!disposed) emit("error", error); }
}
async function adopt(item: SoftwareInstance) {
  try {
    await ElMessageBox.confirm(
      `接管“${item.name}”的 ${softwareRuntimeLabels[item.runtimeKind]}（${item.locator}）？此后 Ops 可启停该对象。配置和业务数据保留。`,
      "接管生命周期",
      {
        confirmButtonText: "确认接管",
        cancelButtonText: "取消",
        customClass: "software-confirm",
        type: "warning",
      },
    );
  } catch {
    return;
  }
  if (busy.value) return;
  const revision = selectDetail(item.id);
  await act(async () =>
    mutationDone(
      await request<SoftwareMutationResult>(
        `/software-instances/${item.id}/adopt`,
        "POST",
        {
          expectedVersion: item.version,
          identityHash: item.observation?.identityHash,
          confirmed: true,
          idempotencyKey: crypto.randomUUID(),
        },
      ),
      revision,
    ),
  );
}
async function changeState(item: SoftwareInstance, action: SoftwareAction) {
  if (action === "START" && !softwareInstallationReady(item)) return;
  if (["STOP", "RESTART", "PAUSE"].includes(action)) {
    const verb = { STOP: "停止", RESTART: "重启", PAUSE: "暂停" }[action as "STOP" | "RESTART" | "PAUSE"];
    try {
      await ElMessageBox.confirm(
        softwareIsDockerEngine(item)
          ? `${verb}主机 Docker Engine“${item.name}”？此主机已登记 ${dependentContainers(item).length} 个容器${dependentContainers(item).length ? `（${dependentContainers(item).join("、")}）` : ""}。容器服务可能中断，未登记容器也可能受影响。`
          : action === "STOP" ? `停止“${item.name}”并保持停止？再次启动会恢复原运行策略。` : `${verb}“${item.name}”？使用该服务的业务可能受到影响。`,
        `${verb}服务`,
        {
          confirmButtonText: `${verb}服务`,
          cancelButtonText: "取消",
          type: "warning",
        },
      );
    } catch {
      return;
    }
  }
  if (busy.value) return;
  const revision = selectDetail(item.id);
  await act(async () =>
    mutationDone(
      await request<SoftwareMutationResult>(
        `/software-instances/${item.id}/actions`,
        "POST",
        {
          expectedVersion: item.version,
          action,
          idempotencyKey: crypto.randomUUID(),
        },
      ),
      revision,
    ),
  );
}
async function unmanage(item: SoftwareInstance) {
  const readOnly = item.managementMode === "OBSERVE";
  const title = readOnly ? "移除登记" : "解除管理";
  try {
    await ElMessageBox.confirm(
      readOnly ? `将“${item.name}”移出软件列表？` : `解除“${item.name}”的管理？服务保持当前运行状态，远端文件和数据保留。`,
      title,
      {
        confirmButtonText: title,
        cancelButtonText: "取消",
        type: "warning",
      },
    );
  } catch {
    return;
  }
  if (busy.value) return;
  const revision = selectDetail(item.id);
  await act(async () => {
    await mutationDone(
      await request<SoftwareMutationResult>(
        `/software-instances/${item.id}/unmanage`,
        "POST",
        {
          expectedVersion: item.version,
          idempotencyKey: crypto.randomUUID(),
        },
      ),
      revision,
    );
    ElMessage.success(
      readOnly ? "已移除登记" : "解除管理已提交",
    );
  });
}
function readLogs(source: "software" | "job") {
  const item = detail.value;
  if (!item) return;
  if (source === "software" && !softwareLifecycleAvailable(item)) return;
  const target = source === "job" ? { jobId: job.value?.id } : { softwareId: item.id };
  if (source === "job" && !target.jobId) return;
  selectDetail("");
  emit("logs", target);
}
async function reconcile() {
  if (!job.value) return;
  await act(async () => {
    await request("/jobs/" + job.value!.id + "/reconcile", "POST");
    await refresh();
  });
}
async function cancel() {
  if (!job.value) return;
  await act(async () => {
    await request("/jobs/" + job.value!.id + "/cancel", "POST");
    await refresh();
  });
}
function poll() {
  if (disposed || !isActive.value || timer) return;
  timer = setTimeout(async () => {
    timer = undefined;
    if (disposed || !isActive.value) return;
    if (
      !document.hidden &&
      !busy.value &&
      !registration.value &&
      !discovery.value &&
      props.view === "manage"
    )
      await refreshView();
    poll();
  }, 5000);
}
watch(() => props.view, (view) => {
  if (isActive.value && view === "manage") void refreshView();
});
watch(() => props.selectedSoftwareId, async (id) => {
  if (!id) return;
  try { await openInstance(id, true); }
  catch (error) { if (!disposed && props.selectedSoftwareId === id) emit("error", error); }
  finally { emit("selectionOpened", id); }
}, { immediate: true });
onMounted(() => { void refreshView(); });
onActivated(() => {
  const resumed = !isActive.value;
  isActive.value = true;
  if (resumed) void refreshView();
  poll();
});
onDeactivated(() => {
  isActive.value = false;
  if (timer) clearTimeout(timer);
  timer = undefined;
  nodeOpen.value = false;
  gitOpen.value = false;
  registration.value = false;
  discovery.value = false;
  uninstallOpen.value = false;
  configurationItem.value = undefined;
  queuedConfiguration.value = undefined;
  selectDetail("");
});
onUnmounted(() => {
  disposed = true;
  if (timer) clearTimeout(timer);
});
</script>

<template>
  <section class="content software-management" :aria-label="view === 'install' ? '软件安装' : '软件管理'">
    <SoftwareRecipes
      v-if="isActive && view === 'install'"
      :host-id="dockerHostId || installHostId || hostId"
      :initial-recipe-key="dockerHostId ? 'docker' : undefined"
      :initial-search="installSearch"
      @open-instance="(id) => openInstance(id).catch((error) => emit('error', error))"
      @logs="(jobId) => emit('logs', { jobId })"
      @error="emit('error', $event)"
      @preparation="emit('preparation', $event)"
      @register="discoverInstalled"
      @node="openNode"
      @git="openGit"
    />
    <div v-if="view === 'manage'" class="software-instances" aria-label="已安装软件">
      <div class="ops-el-toolbar">
        <div class="ops-el-toolbar-group">
          <ElInput
            v-model="query"
            clearable
            placeholder="搜索软件名称、类型或标识"
            style="width: 230px"
          />
          <ElSelect
            v-model="hostId"
            clearable
            :value-on-clear="''"
            placeholder="全部主机"
            style="width: 165px"
          >
            <ElOption label="全部主机" value="" />
            <ElOption
              v-for="host in hosts"
              :key="host.id"
              :label="host.name"
              :value="host.id"
            />
          </ElSelect>
          <ElSelect v-model="mode" style="width: 150px">
            <ElOption label="活跃实例" value="ACTIVE" />
            <ElOption label="平台托管" value="MANAGED" />
            <ElOption label="主机发现（只读）" value="OBSERVE" />
            <ElOption label="已解除" value="RELEASED" />
            <ElOption label="全部登记" value="ALL" />
          </ElSelect>
        </div>
        <div class="ops-el-toolbar-group">
          <ElRadioGroup v-model="displayMode" size="small">
            <ElRadioButton value="table">列表</ElRadioButton>
            <ElRadioButton value="card">卡片</ElRadioButton>
          </ElRadioGroup>
          <ElDropdown trigger="click" @command="runBatchCommand">
            <ElButton :disabled="busy || (!hosts.length && !detectionCount)">
              批量与发现 ▾
            </ElButton>
            <template #dropdown>
              <ElDropdownMenu>
                <ElDropdownItem
                  command="detect-filtered"
                  :disabled="!detectionCount"
                >
                  一键检测当前筛选（{{ detectionCount }}）
                </ElDropdownItem>
                <ElDropdownItem command="detect-all" :disabled="!rows.length">
                  一键检测全部活跃软件
                </ElDropdownItem>
                <ElDropdownItem
                  command="discover"
                  divided
                  :disabled="!hosts.length"
                >
                  自动识别主机软件
                </ElDropdownItem>
              </ElDropdownMenu>
            </template>
          </ElDropdown>
          <ObservationBatchPanel
            :batch="batch"
            :busy="busy"
            @cancel="cancelDetection"
          />
          <ElButton
            :loading="loading"
            :disabled="busy"
            @click="refreshView"
          >
            刷新
          </ElButton>
          <ElButton
            type="primary"
            :disabled="busy || !hosts.length"
            @click="openRegistration()"
          >
            登记已有
          </ElButton>
        </div>
      </div>
      <div v-if="!rows.length" class="software-empty-box">
        <ElEmpty
          :description="
            loading
              ? '正在加载软件实例…'
              : hosts.length
                ? '尚未登记或安装任何软件'
                : '请先在服务器管理添加目标节点'
          "
        >
          <template v-if="!loading" #default>
            <div class="ops-el-toolbar-group" style="justify-content: center">
              <ElButton
                type="primary"
                :disabled="!hosts.length"
                @click="openRegistration()"
              >
                登记已有
              </ElButton>
              <ElButton @click="emit('navigate', 'install')">安装软件</ElButton>
            </div>
          </template>
        </ElEmpty>
      </div>
      <template v-else-if="displayMode === 'table'">
        <ElTable
          :data="tableRows"
          row-key="id"
          border
          stripe
          style="width: 100%; margin-top: 16px"
        >
          <ElTableColumn type="expand" width="44">
            <template #default="{ row }">
              <div class="expand-box">
                <ElDescriptions :column="1" border size="small" class="expand-descriptions">
                  <ElDescriptionsItem
                    v-if="row.observation?.version || row.softwareVersion"
                    label="版本 / 镜像身份"
                  >
                    <div class="expand-value-row">
                      <code>{{ cleanObservedVersionText(row.observation?.version || row.softwareVersion) }}</code>
                    </div>
                  </ElDescriptionsItem>
                  <ElDescriptionsItem
                    v-for="entry in itemPaths(row as SoftwareInstance)"
                    :key="`${entry.label}:${entry.value}`"
                    :label="entry.label"
                  >
                    <div class="expand-value-row">
                      <code>{{ entry.value }}</code>
                      <ElButton
                        link
                        type="primary"
                        size="small"
                        @click="copyPathValue(entry.value)"
                      >
                        复制
                      </ElButton>
                    </div>
                  </ElDescriptionsItem>
                  <ElDescriptionsItem
                    v-if="itemPortsSummary(row as SoftwareInstance).length"
                    label="端口与映射"
                  >
                    <div class="expand-value-row">
                      <code>{{ itemPortsSummary(row as SoftwareInstance).join("，") }}</code>
                    </div>
                  </ElDescriptionsItem>
                  <ElDescriptionsItem
                    v-for="(mount, mIdx) in row.observation?.mounts"
                    :key="mIdx"
                    :label="mount.volume ? '容器卷' : '路径挂载'"
                  >
                    <div class="expand-value-row">
                      <code>{{ mount.volume || mount.source || "—" }} → {{ mount.destination }}</code>
                      <ElTag size="small" type="info">
                        {{ mount.readOnly ? "只读" : "读写" }}
                      </ElTag>
                      <ElButton
                        v-if="mount.source"
                        link
                        type="primary"
                        size="small"
                        @click="copyPathValue(mount.source)"
                      >
                        复制宿主路径
                      </ElButton>
                    </div>
                  </ElDescriptionsItem>
                </ElDescriptions>
                <div class="expand-foot muted">
                  <span v-if="!itemPaths(row as SoftwareInstance).length && !row.observation?.mounts?.length && !itemPortsSummary(row as SoftwareInstance).length">
                    暂无已采集的文件路径与挂载目录，可点击「详情 → 检查状态」采集完整目录。
                  </span>
                  <span v-if="row.observation?.observedAt">
                    最近检查于 {{ hostTime(row.observation.observedAt) }}
                  </span>
                </div>
              </div>
            </template>
          </ElTableColumn>
          <ElTableColumn label="软件名称" min-width="210" show-overflow-tooltip>
            <template #default="{ row }">
              <div class="software-table-identity">
                <SoftwareIcon :type="row.installation?.definition?.catalogKey || row.softwareType" />
                <div class="software-table-name">
                  <strong>{{ row.name }}</strong>
                  <span class="muted">
                    {{ softwareTypeLabel(row as SoftwareInstance) }}
                    <template v-if="tableVersionText(row as SoftwareInstance)">
                      · {{ tableVersionText(row as SoftwareInstance) }}
                    </template>
                  </span>
                </div>
              </div>
            </template>
          </ElTableColumn>
          <ElTableColumn label="所属服务器" width="160" show-overflow-tooltip>
            <template #default="{ row }">
              <div class="software-table-host">
                <strong>{{ row.hostName || itemHost(row as SoftwareInstance)?.name || row.hostId }}</strong>
                <span v-if="itemHost(row as SoftwareInstance)?.address" class="muted">{{ itemHost(row as SoftwareInstance)!.address }}</span>
              </div>
            </template>
          </ElTableColumn>
          <ElTableColumn label="运行方式" width="200" show-overflow-tooltip>
            <template #default="{ row }">
              <div class="software-table-runtime">
                <div class="runtime-tags">
                  <ElTag size="small" effect="plain">{{ softwareRuntimeLabels[(row as SoftwareInstance).runtimeKind] }}</ElTag>
                  <ElTag size="small" type="info">{{ softwareModes[(row as SoftwareInstance).managementMode] }}</ElTag>
                </div>
                <span class="locator-ellipsis" :title="row.observation?.containerName || row.locator">
                  {{ itemLocator(row as SoftwareInstance) }}
                </span>
              </div>
            </template>
          </ElTableColumn>
          <ElTableColumn label="状态" width="96" align="center">
            <template #default="{ row }">
              <ElTag :type="stateTagType(softwareState(row as SoftwareInstance).tone)" size="small">
                {{ softwareState(row as SoftwareInstance).label }}
              </ElTag>
            </template>
          </ElTableColumn>
          <ElTableColumn label="端口 / 入口" min-width="180" show-overflow-tooltip>
            <template #default="{ row }">
              <div class="software-table-ports">
                <SoftwareWebAccess :item="(row as SoftwareInstance)" :host="itemHost(row as SoftwareInstance)" compact />
                <template v-if="itemPortsSummary(row as SoftwareInstance).length">
                  <code class="port-chip">{{ itemPortsSummary(row as SoftwareInstance)[0] }}</code>
                  <ElTag v-if="itemPortsSummary(row as SoftwareInstance).length > 1" size="small" type="info">
                    +{{ itemPortsSummary(row as SoftwareInstance).length - 1 }}
                  </ElTag>
                </template>
                <span v-else-if="!row.installation?.definition?.web" class="muted">
                  {{ row.runtimeKind === "TOOL" ? "—" : "未确认" }}
                </span>
              </div>
            </template>
          </ElTableColumn>
          <ElTableColumn label="操作" width="185" fixed="right">
            <template #default="{ row }">
              <div class="software-op-row">
                <ElButton
                  link
                  type="primary"
                  size="small"
                  :disabled="busy"
                  @click="showDetails(row as SoftwareInstance)"
                >
                  详情
                </ElButton>
                <ElButton
                  v-if="row.softwareType === 'MAVEN' && row.managementMode === 'MANAGED' && row.installationStatus === 'READY'"
                  link
                  type="primary"
                  size="small"
                  :disabled="busy || !!itemHost(row as SoftwareInstance)?.maintenance"
                  @click="showDetails(row as SoftwareInstance, 'maven-config')"
                >
                  Maven 配置
                </ElButton>
                <ElButton
                  v-if="rowCanChange(row as SoftwareInstance) && ['STOPPED', 'FAILED', 'MISSING'].includes(row.observation?.state || row.actualState)"
                  link
                  type="success"
                  size="small"
                  :disabled="busy || !softwareInstallationReady(row as SoftwareInstance)"
                  @click="changeState(row as SoftwareInstance, 'START')"
                >
                  启动
                </ElButton>
                <ElButton
                  v-else-if="rowCanChange(row as SoftwareInstance) && (row.observation?.state || row.actualState) === 'RUNNING'"
                  link
                  type="primary"
                  size="small"
                  :disabled="busy"
                  @click="changeState(row as SoftwareInstance, 'RESTART')"
                >
                  重启
                </ElButton>
                <ElDropdown
                  v-if="hasMoreActions(row as SoftwareInstance)"
                  trigger="click"
                  @command="runRowCommand(row as SoftwareInstance, $event)"
                >
                  <ElButton link size="small" :disabled="busy">更多 ▾</ElButton>
                  <template #dropdown>
                    <ElDropdownMenu>
                      <ElDropdownItem
                        v-if="rowCanChange(row as SoftwareInstance) && (row.observation?.state || row.actualState) === 'RUNNING'"
                        command="stop"
                      >
                        停止
                      </ElDropdownItem>
                      <ElDropdownItem
                        v-if="row.managementMode === 'OBSERVE' && service(row as SoftwareInstance)"
                        command="adopt"
                        :disabled="!canAdopt(row as SoftwareInstance)"
                      >
                        接管管理
                      </ElDropdownItem>
                      <ElDropdownItem
                        v-if="softwareHasInstallationRecord(row as SoftwareInstance)"
                        command="logs"
                      >
                        安装日志
                      </ElDropdownItem>
                      <ElDropdownItem
                        v-if="softwareUninstallAvailable(row as SoftwareInstance)"
                        command="uninstall"
                        divided
                        :disabled="!!itemHost(row as SoftwareInstance)?.maintenance"
                      >
                        卸载软件
                      </ElDropdownItem>
                    </ElDropdownMenu>
                  </template>
                </ElDropdown>
              </div>
            </template>
          </ElTableColumn>
          <template #empty>
            <ElEmpty description="没有匹配的软件" />
          </template>
        </ElTable>
        <div v-if="tableTotal" class="ops-el-pager">
          <ElPagination
            v-model:current-page="tablePage"
            v-model:page-size="tablePageSize"
            :page-sizes="[10, 20, 50]"
            :total="tableTotal"
            background
            layout="total, sizes, prev, pager, next"
          />
        </div>
      </template>
      <template v-else>
      <div ref="softwareGrid" class="software-cards">
        <article
          v-for="{ item, version } in softwareCards"
          :key="item.id"
          class="software-card"
          :aria-label="item.name"
        >
          <header class="software-card-head">
            <span class="state-tag" :class="softwareState(item).tone">
              {{ softwareState(item).label }}
            </span>
            <span class="management-label">
              {{ softwareModes[item.managementMode] }}
            </span>
          </header>
          <div class="software-identity">
            <SoftwareIcon :type="item.installation?.definition?.catalogKey || item.softwareType" />
            <div>
              <h3>{{ item.name }}</h3>
              <span>
                {{ softwareTypeLabel(item) }} ·
                {{ softwareRuntimeLabels[item.runtimeKind] }}
              </span>
            </div>
          </div>
          <dl class="card-facts">
            <dt>所在主机</dt>
            <dd>
              {{
                item.hostName ||
                hosts.find((host) => host.id === item.hostId)?.name ||
                item.hostId
              }}
            </dd>
            <dt>{{ version.label }}</dt>
            <dd class="card-version" :title="version.value">{{ version.value }}</dd>
          </dl>
          <footer class="software-card-footer">
            <div class="software-row-actions">
              <button
                v-if="active(item)"
                class="quiet"
                :disabled="busy || !softwareLifecycleAvailable(item)"
                @click="observe(item)"
              >
                检查
              </button>
              <button class="quiet" :disabled="busy" @click="showDetails(item)">
                详情
              </button>
              <button
                v-if="item.softwareType === 'MAVEN' && item.managementMode === 'MANAGED' && item.installationStatus === 'READY'"
                class="quiet"
                :disabled="busy || !!hosts.find(host => host.id === item.hostId)?.maintenance"
                @click="showDetails(item, 'maven-config')"
              >
                Maven 配置
              </button>
              <SoftwareWebAccess :item="item" :host="hosts.find(host => host.id === item.hostId)" compact />
              <button v-if="softwareConfigurationAvailable(item)" class="quiet" :disabled="busy || !!hosts.find(host => host.id === item.hostId)?.maintenance" @click="editConfiguration(item)">编辑配置</button>
              <button v-if="softwareHasInstallationRecord(item)" class="quiet" :disabled="busy" @click="emit('logs', { jobId: item.installationJobId || item.lastJobId })">安装日志</button>
              <button v-if="softwareUninstallAvailable(item)" class="quiet danger" :disabled="busy || !!hosts.find(host => host.id === item.hostId)?.maintenance" @click="uninstallItem = item; uninstallOpen = true">卸载</button>
              <button
                v-if="item.managementMode === 'OBSERVE' && service(item)"
                class="quiet"
                :disabled="busy || !canAdopt(item)"
                @click="adopt(item)"
              >
                接管
              </button>
            </div>
          </footer>
        </article>
      </div>
      <OpsPagination v-if="total" v-model:page="page" v-model:page-size="pageSize" :size-options="pageSizeOptions" :total="total" label="软件管理分页" />
      </template>
    </div>
  </section>
  <ElDrawer v-model="nodeOpen" title="Node.js" size="min(1080px, 100vw)" append-to-body :destroy-on-close="false" :close-on-click-modal="!nodeCloseState.blocked" :close-on-press-escape="!nodeCloseState.blocked" :show-close="!nodeCloseState.blocked" :before-close="closeNode">
    <p v-if="nodeHost" class="node-host-label">{{nodeHost.name}} · {{nodeHost.address || '本机'}}</p>
    <NodeEnvironmentPanel v-if="nodeHost" :host="nodeHost" :initial-environment-id="nodeEnvironmentId" @changed="act(refresh)" @close-state="nodeCloseState = $event" />
    <p v-else class="empty">此主机已不可用，请重新选择。</p>
  </ElDrawer>
  <ElDrawer v-model="gitOpen" title="Git 账号配置" size="min(960px, 100vw)" destroy-on-close :close-on-click-modal="!gitCloseState.blocked" :close-on-press-escape="!gitCloseState.blocked" :show-close="!gitCloseState.blocked" :before-close="closeGit">
    <p v-if="gitHost" class="node-host-label">{{ gitHost.name }} · {{ gitHost.address || '本机' }}</p>
    <GitManagementPanel v-if="gitOpen && gitHost" :host="gitHost" @close-state="gitCloseState = $event" @changed="act(refresh)" />
    <p v-else class="empty">此主机已不可用，请重新选择。</p>
  </ElDrawer>
  <SoftwareRegistrationDialog
    :open="registration"
    :hosts="hosts"
    :host-id="hostId"
    :prefill="registrationPrefill"
    @close="registration = false"
    @saved="saved"
    @error="emit('error', $event)"
  />
  <SoftwareDiscoveryDialog
    :open="discovery"
    :hosts="hosts"
    :host-id="hostId"
    @close="discovery = false"
    @register="openRegistration"
    @error="emit('error', $event)"
  />
  <SoftwareDetailsDialog
    :detail="detail"
    :initial-tab="detailInitialTab"
    :host="detailHost"
    :busy="busy"
    :observing="observing"
    :job="job"
    :pending-job="pendingJob"
    :can-adopt="!!detail && canAdopt(detail)"
    :can-change="!!detail && canChange(detail)"
    @close="selectDetail('')"
    @observe="observe"
    @adopt="adopt"
    @unmanage="unmanage"
    @uninstall="(item) => { uninstallItem = item; uninstallOpen = true; }"
    @change-state="changeState"
    @read-logs="readLogs"
    @operation-logs="id => { selectDetail(''); emit('logs', { jobId: id }); }"
    @reconcile="reconcile"
    @cancel="cancel"
    @configuration-submitted="configurationSubmitted"
    @changed="act(refresh)"
    @preparation="emit('preparation', $event)"
    @configure="editConfiguration"
    @closed="detailClosed"
  />
  <SoftwareConfigurationDialog :item="configurationItem" :host="hosts.find(host => host.id === configurationItem?.hostId)" @close="configurationItem = undefined" @submitted="configurationSubmitted" @logs="id => emit('logs', { jobId: id })" @preparation="emit('preparation', $event)" />
  <SoftwareUninstallDialog :open="uninstallOpen" :item="uninstallItem"
    :dependent-containers="dependentContainers(uninstallItem)"
    @close="uninstallOpen = false" @submitted="configurationSubmitted" />

</template>

<style scoped>
.node-host-label{margin:0 0 20px;color:var(--muted);font-size:12px;}
.software-management {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 24px;
  box-shadow: var(--card-shadow);
}
.software-identity {
  display: flex;
  align-items: center;
  gap: 12px;
}
.software-identity > div {
  min-width: 0;
}
.software-identity :deep(.software-icon) {
  width: 36px;
  height: 36px;
  border-radius: 8px;
  font-size: 17px;
}
.software-identity :deep(svg) {
  width: 30px;
  height: 30px;
}
:global(.software-confirm .el-message-box__message) {
  overflow-wrap: anywhere;
}
.software-toolbar {
  display: flex;
  flex-wrap: wrap;
  gap: 14px;
  align-items: center;
}
.software-filters {
  display: grid;
  flex: 1 1 470px;
  grid-template-columns: minmax(140px, 240px) 132px 132px;
  gap: 10px;
}
.software-toolbar-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  justify-content: flex-end;
  flex-wrap: wrap;
  margin-left: auto;
}
.software-toolbar-actions select {
  width: 120px;
  height: 38px;
  margin: 0;
}
.software-filters input,
.software-filters select {
  width: 100%;
  min-width: 0;
  margin: 0;
  height: 38px;
}
.software-toolbar-actions button {
  min-height: 38px;
}
.software-toolbar-actions .primary,
.software-empty-actions .primary {
  width: auto;
  margin: 0;
}
.software-empty,
.software-cards,
.software-instances > .empty {
  margin-top: 16px;
}
.software-empty {
  display: grid;
  gap: 16px;
  justify-items: center;
  padding: 60px 20px;
}
.software-empty strong {
  color: var(--text);
  font-size: 15px;
  font-weight: 500;
}
.software-empty > span {
  font-size: 13px;
}
.software-empty-actions {
  display: flex;
  gap: 10px;
  margin-top: 6px;
}
.software-cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 310px), 1fr));
  gap: 18px;
}
.software-card {
  display: flex;
  flex-direction: column;
  min-width: 0;
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
  background: var(--surface);
  transition:
    border-color 0.15s ease,
    box-shadow 0.15s ease;
}
.software-card:hover {
  border-color: #cbd5e1;
  box-shadow: 0 2px 8px rgba(15, 23, 42, 0.04);
}
.software-card-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 10px;
  padding: 16px 18px 0;
}
.management-label {
  font-size: 11px;
  color: var(--muted);
}
.software-card .software-identity {
  padding: 22px 18px 18px;
}
.software-identity h3 {
  font-size: 15px;
  font-weight: 600;
  margin: 0 0 7px;
  overflow-wrap: anywhere;
}
.software-identity span {
  font-size: 12px;
  color: var(--muted);
}
.card-facts {
  display: grid;
  grid-template-columns: 84px minmax(0, 1fr);
  gap: 12px;
  margin: 0;
  padding: 0 18px 22px;
  font-size: 12px;
}
.card-facts dt {
  color: var(--muted);
}
.card-facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.card-version {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  font: 11px/1.7 var(--mono);
}
.software-card-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  padding: 10px 12px 10px 18px;
  border-top: 1px solid var(--border);
  margin-top: auto;
  background: #fafbfd;
}
.software-card-footer > span {
  font-size: 11px;
  color: var(--muted);
}
.software-row-actions {
  display: flex;
  flex-wrap: wrap;
}
.software-row-actions .quiet {
  font-size: 12px;
  padding: 5px 7px;
}
.software-empty-box {
  padding: 28px 0;
}
.expand-box {
  padding: 12px 20px;
  display: grid;
  gap: 10px;
  background: #f8fafc;
  max-width: 100%;
  overflow: hidden;
  box-sizing: border-box;
}
.expand-descriptions :deep(.el-descriptions__table) {
  table-layout: fixed;
  width: 100%;
}
.expand-descriptions :deep(td.el-descriptions__label) {
  width: 128px;
}
.expand-descriptions :deep(td.el-descriptions__content) {
  min-width: 0;
  white-space: pre-wrap;
  word-break: break-word;
  overflow-wrap: anywhere;
}
.expand-value-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  min-width: 0;
}
.expand-value-row code {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  word-break: break-word;
  max-width: 100%;
}
.expand-foot {
  display: flex;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
  font-size: 12px;
}
.software-table-identity {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}
.software-table-identity :deep(.software-icon) {
  width: 32px;
  height: 32px;
  border-radius: 7px;
  font-size: 15px;
  flex-shrink: 0;
}
.software-table-identity :deep(svg) {
  width: 26px;
  height: 26px;
}
.software-table-name,
.software-table-host,
.software-table-runtime {
  display: grid;
  gap: 2px;
  min-width: 0;
}
.software-table-name strong,
.software-table-name span,
.software-table-host strong,
.software-table-host span,
.locator-ellipsis {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.software-table-name span,
.software-table-host span {
  font-size: 12px;
}
.runtime-tags {
  display: flex;
  align-items: center;
  gap: 6px;
}
.locator-ellipsis {
  font: 11px/1.4 var(--mono);
  color: var(--muted);
}
.software-table-ports {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}
.port-chip {
  font-size: 12px;
  color: #334155;
}
.software-op-row {
  display: flex;
  align-items: center;
  gap: 8px;
  white-space: nowrap;
}
@media (max-width: 650px) {
  .software-management {
    padding: 16px 14px;
  }
}
</style>
