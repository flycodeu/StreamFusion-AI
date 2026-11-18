<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { ElDialog, ElDropdown, ElDropdownMenu, ElDropdownItem, ElMessage, ElMessageBox } from "element-plus";
import { request } from "./api";
import OpsPagination from "./OpsPagination.vue";
import { usePagination } from "./pagination";
import { requestAllPages } from "./listPages";
import { findErrorReference } from "./errorReference";
import SoftwareIcon from "./SoftwareIcon.vue";
import ServiceInstallDialog from "./ServiceInstallDialog.vue";
import {
  serviceDescriptions,
  recipeCatalogKey,
  isTool,
  type InstallationMaterial,
  type ServiceRecipe,
} from "./serviceInstallation";
import { hostObservationExpired, type Host } from "./hosts";
import { softwareHasInstallationRecord, softwareIsDockerEngine, softwareState, type SoftwareInstance } from "./software";
import { catalogDefaultRecipe, catalogInstances, catalogInstallState, catalogPending, catalogPendingRecords, catalogVersionMatches, installedPythonPaths } from "./softwareCatalog";
import "./software-recipes.css";

interface Installation {
  id: string;
  targetId: string;
  pluginKey: string;
  pluginVersion: string;
  status: string;
  referenceCount: number;
  imageDigest: string;
  imageId: string;
  observedVersion: string;
  observedAt: string;
  lastJobId: string;
  errorCode: string;
}
interface Job {
  id: string;
  status: string;
  phase: string;
  errorCode: string;
  traceId: string;
  updatedAt: string;
  targetAckPending: boolean;
}

const props = defineProps<{ hostId: string; initialRecipeKey?: string }>();
const emit = defineEmits<{ error: [unknown]; preparation: [string]; openInstance: [string]; register: [hostId: string, softwareType: string]; node: [hostId: string]; git: [hostId: string]; logs: [jobId: string] }>();
const targets = ref<Host[]>([]);
const serviceDefinitions = ref<ServiceRecipe[]>([]);
const officialDefinitions = ref<ServiceRecipe[]>([]);
const serviceRecipe = ref<ServiceRecipe>();
const serviceRecord = ref<SoftwareInstance>();
const installations = ref<Installation[]>([]);
const softwareInstances = ref<SoftwareInstance[]>([]);
const dockerRecipe = computed(() => serviceDefinitions.value.find((item) => item.key === "docker"));
const dockerInstance = computed(() => softwareInstances.value.find((item) => item.hostId === targetId.value &&
  item.managementMode !== "RELEASED" && softwareIsDockerEngine(item)));
const targetId = ref("");
const pythonPaths = computed(() => installedPythonPaths(softwareInstances.value, targetId.value));
const installedGit = computed(() => softwareInstances.value.find((item) => item.hostId === targetId.value &&
  item.softwareType === "GIT" && item.managementMode !== "RELEASED" &&
  (['READY', 'INSTALLING', 'UNKNOWN'].includes(item.installationStatus || '') || item.actualState === 'INSTALLED')));
function recipeRecord(definition: ServiceRecipe, pendingOnly = false) {
  if (pendingOnly) return catalogPending(softwareInstances.value, targetId.value, definition);
  const history = softwareInstances.value.filter(item => item.hostId === targetId.value &&
    softwareHasInstallationRecord(item) && item.installation?.definition &&
    recipeCatalogKey(item.installation.definition) === recipeCatalogKey(definition))
    .sort((a, b) => b.createdAt.localeCompare(a.createdAt));
  return history.find(item => catalogVersionMatches(item, definition)) || history[0];
}
const nodeInstances = computed(() => catalogInstances(softwareInstances.value, targetId.value, "node"));
const nodeVersionCount = computed(() => new Set(nodeInstances.value.map(item => item.softwareVersion || item.observation?.version).filter(Boolean)).size);
const installState = (definition: ServiceRecipe) => catalogInstallState(softwareInstances.value, targetId.value, definition);
function manageDefinition(definition: ServiceRecipe) {
  const state = installState(definition);
  const item = state.matching[0] || state.instances[0];
  if (item) emit("openInstance", item.id);
}
function openDefinition(definition: ServiceRecipe) {
  const pending = recipeRecord(definition, true);
  if (pending) openInstallationRecord(pending.id);
  else if (definition.key === "git" && installedGit.value) emit("openInstance", installedGit.value.id);
  else { serviceRecord.value = undefined; serviceRecipe.value = definition; }
}
function openInstallationRecord(id: string) {
  const item = softwareInstances.value.find(record => record.id === id);
  if (!item?.installation?.definition) return;
  serviceRecord.value = item;
  serviceRecipe.value = item.installation.definition;
}
function openInstallationHistory(item?: SoftwareInstance) {
  if (!item?.installationJobId) return;
  if (item.installationStatus === "FAILED") openInstallationRecord(item.id);
  else showJobLogs(item.installationJobId);
}
const pendingRecords = (key: string) => catalogPendingRecords(softwareInstances.value, targetId.value, key);
function showJobLogs(jobId: string) {
  dialog.value = false;
  serviceRecipe.value = undefined;
  serviceRecord.value = undefined;
  emit("logs", jobId);
}
const search = ref("");
const category = ref("ALL");
const selectedVersions = ref<Record<string, string>>({});
const categories = [
  { key: "ALL", label: "全部软件" },
  { key: "SERVICES", label: "服务软件" },
  { key: "ANNOTATION", label: "标注平台" },
  { key: "ENGINE", label: "容器引擎" },
  { key: "JAVA_RUNTIME", label: "运行环境" },
  { key: "MAVEN_JDK_BUILD", label: "构建工具" },
  { key: "MEDIA_TOOLS", label: "媒体工具" },
  { key: "PROGRAMMING", label: "编程软件" },
  { key: "CUSTOM", label: "自定义软件" },
];
function recipeSubtitle(d: ServiceRecipe) {
  if (d.category === "ANNOTATION") return "数据标注平台";
  if (d.commands && !d.commands.builtin) return { TOOL: "程序工具", SYSTEMD: "系统服务", DOCKER: "容器服务", SERVICE: "服务" }[d.commands.kind];
  if (d.key === "git") return "版本控制";
  if (d.key === "docker") return "主机容器运行引擎";
  if (d.category === "PROGRAMMING") return "编程软件";
  if (["java", "python"].includes(d.key)) return "应用运行环境";
  if (d.key === "maven") return "项目构建工具";
  if (d.key === "ffmpeg") return "媒体处理工具";
  if (["mediamtx", "zlm"].includes(d.key)) return "多协议媒体服务";
  if (d.key === "seaweedfs") return "S3 对象存储";
  return d.key === "mysql"
    ? "关系数据库"
    : d.key === "redis"
      ? "内存数据服务"
      : d.key === "nginx" ? "Web 与反向代理" : isTool(d) ? "程序工具" : "服务软件";
}
const catalogEntries = computed(() => {
  const term = search.value.trim().toLowerCase();
  const groups = new Map<string, ServiceRecipe[]>();
  for (const definition of serviceDefinitions.value) {
    if (definition.key === "node") continue;
    if (category.value === "CUSTOM" && (!definition.commands || definition.commands.builtin)) continue;
    if (category.value === "ANNOTATION" && definition.category !== "ANNOTATION") continue;
    if (category.value === "ENGINE" && definition.key !== "docker") continue;
    if (category.value === "SERVICES" && definition.key === "docker") continue;
    if (category.value === "SERVICES" && isTool(definition)) continue;
    if (
      category.value === "JAVA_RUNTIME" &&
      !["java", "python"].includes(definition.key)
    )
      continue;
    if (category.value === "MAVEN_JDK_BUILD" && !["maven", "git"].includes(definition.key) && definition.category !== "BUILD_TOOLS")
      continue;
    if (
      ["MEDIA_TOOLS", "PROGRAMMING"].includes(category.value) &&
      definition.category !== category.value
    )
      continue;
    if (
      term &&
      !`${definition.name} ${definition.version} ${definition.image}`
        .toLowerCase()
        .includes(term)
    )
      continue;
    const groupKey = recipeCatalogKey(definition);
    const versions = groups.get(groupKey) || [];
    versions.push(definition);
    groups.set(groupKey, versions);
  }
  return Array.from(groups, ([key, versions]) => ({
    key,
    name: (versions.find(item => item.key === key && (!item.commands || item.commands.builtin)) || versions[0]!).name,
    definition: catalogDefaultRecipe(versions, selectedVersions.value[key])!,
    versions,
    versionChoices: Array.from(new Set(versions.map(item => item.version))),
  }));
});
const showNode = computed(() => ["ALL", "JAVA_RUNTIME"].includes(category.value) && (!search.value.trim() || /node|npm|pnpm|nvm|运行环境/i.test(search.value)));
const catalogItems = computed(() => [
  ...(showNode.value ? [{ kind: "node" as const }] : []),
  ...catalogEntries.value.map(entry => ({ kind: "recipe" as const, entry })),
]);
const { page, pageSize, total, pageItems: pagedCatalog } = usePagination(
  () => catalogItems.value, [search, category, targetId],
);
const nodeOnPage = computed(() => pagedCatalog.value.some(item => item.kind === "node"));
const pagedCatalogEntries = computed(() => pagedCatalog.value.flatMap(item => item.kind === "recipe" ? [item.entry] : []));
const legacyInstallations = computed(() =>
  installations.value.filter((item) => item.targetId === targetId.value),
);
const { page: legacyPage, pageSize: legacyPageSize, total: legacyTotal, pageItems: pagedLegacy } = usePagination(
  () => legacyInstallations.value, [targetId],
);
const legacyName = (item: Installation) =>
  item.pluginKey === "java-runtime" ? "Java" : "Maven";
const busy = ref(false);
const loading = ref(false);
const selectedId = ref("");
const detailJob = ref<Job | null>(null);
const dialog = ref(false);
const currentTarget = computed(() =>
  targets.value.find((t) => t.id === targetId.value),
);
const selected = computed(() =>
  installations.value.find((i) => i.id === selectedId.value),
);
const labels: Record<string, string> = {
  INSTALLING: "安装中",
  REMOVING: "卸载中",
  READY: "镜像就绪",
  FAILED: "异常",
  UNKNOWN: "未知",
  QUEUED: "排队中",
  RUNNING: "执行中",
  SUCCEEDED: "成功",
};
const phases: Record<string, string> = {
  PREFLIGHT: "预检",
  TARGET_EXECUTION: "目标执行",
  PULLING: "拉取镜像",
  VERIFYING: "校验摘要",
  VERSION_CHECK: "探测版本",
  RECORDED: "完成登记",
  REMOVING: "卸载",
  REMOVED: "已卸载",
  PLUGIN_ERROR: "插件异常",
  NOT_STARTED: "未开始",
  INTENT_RECORDED: "等待核对",
  CANCELLED: "已取消",
};
const time = (s?: string) =>
  s
    ? new Date(s).toLocaleString("zh-CN", {
        timeZone: "Asia/Shanghai",
        hour12: false,
      })
    : "—";
const ready = computed(() => {
  const t = currentTarget.value;
  return (
    !!t &&
    !t.maintenance &&
    !hostObservationExpired(t, Date.now()) &&
    t.capabilities?.mutation.status === "AVAILABLE"
  );
});
let timer: ReturnType<typeof setTimeout> | undefined;
let disposed = false;

async function refresh() {
  if (loading.value) return;
  loading.value = true;
  try {
    const [ts, rows, services, materials, official] = await Promise.all([
      request<Host[]>("/hosts"),
      requestAllPages<Installation>("/plugin-installations"),
      request<ServiceRecipe[]>("/software-recipes"),
      request<InstallationMaterial[]>("/installation-materials"),
      request<ServiceRecipe[]>("/software-official-recipes"),
    ]);
    if (disposed) return;
    targets.value = ts;
    installations.value = rows;
    serviceDefinitions.value = services;
    officialDefinitions.value = official;
    if (selectedVersions.value.ytlabel === undefined) {
      const preferred = catalogDefaultRecipe(services.filter(item => recipeCatalogKey(item) === "ytlabel"));
      if (preferred) selectedVersions.value.ytlabel = preferred.version;
    }
    for (const material of materials) {
      if (!material.available || material.origin?.kind !== "BUNDLED") continue;
      const matching = services.find((definition) => definition.key === material.recipeKey &&
        definition.version === material.recipeVersion && definition.revision === material.recipeRevision &&
        definition.runtimeModes.includes(material.runtimeMode));
      if (matching && selectedVersions.value[recipeCatalogKey(matching)] === undefined)
        selectedVersions.value[recipeCatalogKey(matching)] = matching.version;
    }
    for (const definition of services) {
      const key = recipeCatalogKey(definition);
      if (!services.some(item => recipeCatalogKey(item) === key && item.version === selectedVersions.value[key]))
        selectedVersions.value[key] = definition.version;
    }
    if (!targetId.value && ts.length)
      targetId.value = ts.some((t) => t.id === props.hostId)
        ? props.hostId
        : ts[0]!.id;
    const hostId = targetId.value;
    const software = hostId ? await requestAllPages<SoftwareInstance>("/software-instances?hostId=" + encodeURIComponent(hostId)) : [];
    if (!disposed && hostId === targetId.value) softwareInstances.value = software;
    if (dialog.value && selected.value?.lastJobId)
      detailJob.value = await request<Job>("/jobs/" + selected.value.lastJobId);
  } finally {
    loading.value = false;
  }
}
watch(() => [props.initialRecipeKey, props.hostId, serviceDefinitions.value.length], () => {
  if (props.initialRecipeKey && props.hostId && !serviceRecipe.value) {
    const recipe = serviceDefinitions.value.find((item) => item.key === props.initialRecipeKey);
    if (recipe) {
      targetId.value = props.hostId;
      if (!dockerInstance.value && currentTarget.value?.capabilities?.docker.status !== "AVAILABLE") serviceRecipe.value = recipe;
    }
  }
}, { immediate: true });
async function act(fn: () => Promise<void>) {
  if (busy.value) return;
  busy.value = true;
  try {
    await fn();
  } catch (e) {
    emit("error", e);
  } finally {
    busy.value = false;
  }
}
async function probe() {
  await act(async () => {
    await request("/hosts/" + targetId.value + "/checks", "POST");
    await refresh();
  });
}
async function details(i: Installation) {
  selectedId.value = i.id;
  detailJob.value = null;
  dialog.value = true;
  await act(async () => {
    detailJob.value = await request<Job>("/jobs/" + i.lastJobId);
  });
}
async function verify(i: Installation) {
  await act(async () => {
    const result = await request<{ status: string; errorCode: string }>(
      "/plugin-installations/" + i.id + "/verify",
      "POST",
    );
    await refresh();
    if (result.status === "READY") ElMessage.success("工具镜像校验通过");
    else ElMessage.error(findErrorReference(result.errorCode)?.name || "环境校验未通过，请查看执行记录");
  });
}
async function remove(i: Installation) {
  try {
    await ElMessageBox.confirm(
      "卸载该版本环境？服务器原有的镜像会保留。",
      "卸载环境",
      { confirmButtonText: "卸载", cancelButtonText: "取消", type: "warning" },
    );
  } catch {
    return;
  }
  await act(async () => {
    const result = await request<{ jobId: string }>(
      "/plugin-installations/" + i.id + "/remove",
      "POST",
      { idempotencyKey: crypto.randomUUID() },
    );
    detailJob.value = await request<Job>("/jobs/" + result.jobId);
    selectedId.value = i.id;
    dialog.value = true;
    await refresh();
  });
}
async function reconcile() {
  const j = detailJob.value;
  if (!j) return;
  await act(async () => {
    await request("/jobs/" + j.id + "/reconcile", "POST");
    detailJob.value = await request<Job>("/jobs/" + j.id);
    await refresh();
  });
}
async function cancelJob() {
  const j = detailJob.value;
  if (!j) return;
  await act(async () => {
    await request("/jobs/" + j.id + "/cancel", "POST");
    await refresh();
    detailJob.value = await request<Job>("/jobs/" + j.id);
  });
}
function poll() {
  if (disposed) return;
  timer = setTimeout(async () => {
    try {
      if (!document.hidden && !busy.value) {
        await refresh();
        if (dialog.value && detailJob.value)
          detailJob.value = await request<Job>("/jobs/" + detailJob.value.id);
      }
    } catch (e) {
      emit("error", e);
    }
    poll();
  }, 4000);
}
onMounted(async () => {
  await act(refresh);
  poll();
});
onUnmounted(() => {
  disposed = true;
  if (timer) clearTimeout(timer);
});
</script>

<template>
  <section class="software-recipes" aria-label="软件目录">
    <div class="catalog-target">
      <div class="target-choice">
        <span class="target-label">安装到</span>
        <select v-model="targetId" aria-label="目标主机" :disabled="busy || loading" @change="act(refresh)">
          <option v-if="!targets.length" value="">暂无主机</option>
          <option v-for="t in targets" :key="t.id" :value="t.id">
            {{ t.name }} · {{ t.address || "本机" }}
          </option>
        </select>
      </div>
      <span class="target-capability" :class="{ available: ready }"
        ><i></i>{{ currentTarget?.maintenance ? "维护中" : ready ? "已就绪" : "安装时检查" }}</span
      >
      <button class="quiet" :disabled="busy || !targetId" @click="probe">
        检查目标
      </button>
      <button
        v-if="currentTarget"
        class="quiet"
        :disabled="busy"
        @click="emit('preparation', currentTarget.id)"
      >
        安装准备
      </button>
      <details v-if="currentTarget" class="target-facts">
        <summary>主机信息</summary>
        <dl>
          <dt>项目目录</dt>
          <dd>
            <code>{{ currentTarget.installRoot }}</code>
          </dd>
          <dt>运行环境</dt>
          <dd>{{ [
            [currentTarget.capabilities?.distribution, currentTarget.capabilities?.distributionVersion].filter(Boolean).join(" "),
            currentTarget.capabilities?.platform,
          ].filter(Boolean).join(" · ") || "待检查" }}</dd>
          <dt>Docker 能力</dt>
          <dd>
            {{
              currentTarget &&
              !hostObservationExpired(currentTarget, Date.now())
                ? currentTarget.capabilities?.docker.status === "AVAILABLE"
                  ? "可用"
                  : currentTarget.capabilities?.docker.status === "UNAVAILABLE"
                    ? "不可用"
                    : "未确认"
                : "待检查"
            }}
          </dd>
        </dl>
      </details>
      <button class="secondary installation-trigger" @click="showJobLogs('')">安装日志</button>
    </div>
    <section v-if="currentTarget" class="docker-engine-bar" aria-label="主机 Docker Engine">
      <SoftwareIcon type="DOCKER" />
      <div>
        <strong>Docker Engine <span>主机容器运行引擎</span></strong>
        <p>{{ dockerInstance ? `已登记 · ${softwareState(dockerInstance).label}` :
          hostObservationExpired(currentTarget, Date.now()) ? '当前状态待检查' :
          currentTarget.capabilities?.docker.status === 'AVAILABLE' ? '外部 Docker 已可用' : '安装容器软件前需准备 Docker Engine' }}</p>
      </div>
      <button v-if="dockerInstance" class="secondary" :disabled="busy" @click="emit('openInstance', dockerInstance.id)">管理 Docker</button>
      <button v-else-if="currentTarget.capabilities?.docker.status !== 'AVAILABLE'" class="secondary" :disabled="busy || !dockerRecipe || currentTarget.maintenance"
        @click="serviceRecord = undefined; serviceRecipe = dockerRecipe">安装 Docker Engine</button>
    </section>
    <div class="catalog-toolbar">
      <div class="catalog-categories" aria-label="软件分类">
        <button
          v-for="item in categories"
          :key="item.key"
          :class="{ selected: category === item.key }"
          :aria-pressed="category === item.key"
          @click="category = item.key"
        >
          {{ item.label }}
        </button>
      </div>
      <div class="catalog-search">
        <input
          v-model="search"
          type="search"
          aria-label="搜索安装软件"
          placeholder="搜索软件或版本"
        /><button
          class="secondary"
          :disabled="busy || loading"
          @click="act(refresh)"
        >
          刷新
        </button>
      </div>
    </div>
    <div v-if="!targets.length" class="empty">请先在“主机管理”添加主机</div>
    <p v-else-if="currentTarget?.maintenance" class="target-hint">主机维护中，暂不接受新的安装操作。</p>
    <div v-if="!catalogEntries.length && !showNode && !loading" class="empty">
      没有匹配的软件
    </div>
    <div v-else class="plugin-grid">
      <article v-if="nodeOnPage" class="plugin-card node-catalog-card">
        <div class="plugin-heading"><SoftwareIcon type="NODE" /><div class="plugin-title"><h3>Node.js</h3><span>多版本运行环境</span></div><span v-if="nodeInstances.length" class="state-tag success catalog-installed">已安装{{ nodeVersionCount ? ` · ${nodeVersionCount} 个版本` : '' }}</span></div>
        <p class="plugin-description">安装 Node.js，管理 npm、pnpm 和默认版本。</p>
        <div class="plugin-meta"><span>官方在线 / Ops 离线 / 自行安装</span></div>
        <footer><span class="recipe-kind">nvm · 多版本</span><button class="primary" :disabled="!currentTarget" @click="emit('node', targetId)">{{ nodeInstances.length ? '版本管理' : '安装与管理' }}</button></footer>
      </article>
      <article
        v-for="{ key, name, definition, versions, versionChoices } in pagedCatalogEntries"
        :key="key"
        class="plugin-card"
      >
        <div class="plugin-heading">
          <SoftwareIcon :type="key" />
          <div class="plugin-title">
            <h3>{{ name }}</h3>
            <span>{{ recipeSubtitle(definition) }}</span>
          </div>
          <span v-if="installState(definition).label" class="state-tag catalog-installed" :class="installState(definition).tone">{{ installState(definition).label }}</span>
        </div>
        <p v-if="serviceDescriptions[key]" class="plugin-description">
          {{ serviceDescriptions[key] }}
        </p>
        <div class="plugin-meta">
          <select v-if="versionChoices.length > 1" v-model="selectedVersions[key]" :aria-label="name + '版本'"><option v-for="version in versionChoices" :key="version" :value="version">{{ version }}{{ catalogInstances(softwareInstances, targetId, key).some(item => catalogVersionMatches(item, { ...definition, version })) ? ' · 已安装' : '' }}</option></select><span v-else>{{ definition.version }}</span>
          <span>{{ definition.platform }}</span>
          <span v-if="definition.commands && !definition.commands.builtin">{{ definition.commands.steps.length }} 个步骤</span>
        </div>
        <footer>
          <span class="recipe-kind"
            >{{
              Array.from(new Set([...versions.flatMap((item) => item.runtimeModes), ...(officialDefinitions.some((item) => recipeCatalogKey(item) === key) ? ["NATIVE"] : [])]))
                .map((mode) => (mode === "NATIVE" ? "原生" : "Docker"))
                .join(" / ")
            }}
</span
          >
          <div class="recipe-actions">
            <ElDropdown v-if="pendingRecords(key).length && (!recipeRecord(definition, true) || pendingRecords(key).length > 1)" trigger="click" @command="openInstallationRecord">
              <button class="quiet">查看进度<span v-if="pendingRecords(key).length > 1">（{{ pendingRecords(key).length }}）</span></button>
              <template #dropdown><ElDropdownMenu><ElDropdownItem v-for="record in pendingRecords(key)" :key="record.id" :command="record.id">{{ record.installation!.definition!.version }} · {{ record.runtimeKind === 'DOCKER' ? 'Docker' : '原生' }} · {{ record.installationStatus === 'UNKNOWN' ? '结果待确认' : '安装中' }}</ElDropdownItem></ElDropdownMenu></template>
            </ElDropdown>
            <button v-if="installState(definition).instances.length && definition.key !== 'git'" class="quiet" @click="manageDefinition(definition)">管理</button>
            <button v-if="recipeRecord(definition)" class="quiet" @click="openInstallationHistory(recipeRecord(definition))">{{ recipeRecord(definition)?.installationStatus === 'FAILED' ? '处理失败安装' : '安装日志' }}</button>
            <button v-if="definition.key === 'git'" class="secondary" :disabled="!currentTarget" @click="emit('git', targetId)">账号配置</button>
            <a
              v-if="definition.officialUrl"
              class="official-link"
              :href="definition.officialUrl"
              target="_blank"
              rel="noopener noreferrer"
              >{{ definition.commands && !definition.commands.builtin ? '来源说明' : '官方说明' }}</a
            ><button
              class="primary"
              :disabled="busy || !currentTarget || currentTarget.maintenance || (definition.key === 'docker' && !recipeRecord(definition, true) && (!!dockerInstance || currentTarget.capabilities?.docker.status === 'AVAILABLE'))"
              @click="openDefinition(definition)"
            >
              {{ recipeRecord(definition, true) ? '查看进度' : definition.key === 'git' && installedGit ? '查看软件' : '安装' }}
            </button>
          </div>
        </footer>
      </article>
    </div>
    <OpsPagination v-model:page="page" v-model:page-size="pageSize" :total="total" label="软件安装分页" />
    <details v-if="legacyInstallations.length" class="legacy-installations">
      <summary>已有工具镜像（{{ legacyInstallations.length }}）</summary>
      <div
        v-for="item in pagedLegacy"
        :key="item.id"
        class="legacy-row"
      >
        <strong>{{ legacyName(item) }} · {{ item.pluginVersion }}</strong
        ><span>{{ labels[item.status] || '状态待确认' }}</span>
        <div class="recipe-actions">
          <button class="quiet" :disabled="busy" @click="details(item)">
            详情</button
          ><button class="quiet" :disabled="busy" @click="verify(item)">
            校验</button
          ><button
            class="quiet danger"
            :disabled="
              busy ||
              !!item.referenceCount ||
              !['READY', 'FAILED'].includes(item.status)
            "
            @click="remove(item)"
          >
            卸载
          </button>
        </div>
      </div>
      <OpsPagination v-model:page="legacyPage" v-model:page-size="legacyPageSize" :total="legacyTotal" label="工具镜像分页" />
    </details>
  </section>
  <ServiceInstallDialog
    :open="!!serviceRecipe"
    :recipe="serviceRecipe"
    :existing="serviceRecord"
    :recipes="serviceDefinitions.filter((item) => serviceRecipe && recipeCatalogKey(item) === recipeCatalogKey(serviceRecipe))"
    :official-recipes="officialDefinitions.filter((item) => serviceRecipe && recipeCatalogKey(item) === recipeCatalogKey(serviceRecipe))"
    :known-python-paths="pythonPaths"
    :docker-recipe="dockerRecipe"
    :docker-official-recipes="officialDefinitions.filter((item) => item.key === 'docker')"
    :host="currentTarget"
    @close="serviceRecord = undefined; serviceRecipe = undefined"
    @logs="showJobLogs"
    @submitted="act(refresh)"
    @error="emit('error', $event)"
    @preparation="emit('preparation', $event)"
    @register="(hostId, softwareType) => { serviceRecipe = undefined; emit('register', hostId, softwareType); }"
  />
  <ElDialog
    v-model="dialog"
    title="环境详情"
    width="min(660px,94vw)"
    align-center
  >
    <div class="dialog-form">
      <dl class="plugin-detail">
        <dt>环境状态</dt>
        <dd>
          {{ labels[selected?.status || detailJob?.status || ""] || "—" }}
        </dd>
        <dt>作业阶段</dt>
        <dd>{{ phases[detailJob?.phase || ""] || "阶段待确认" }}</dd>
        <dt>观察时间</dt>
        <dd>{{ time(selected?.observedAt || detailJob?.updatedAt) }}</dd>
        <dt>引用数</dt>
        <dd>{{ selected?.referenceCount || 0 }}</dd>
      </dl>
      <div
        v-if="detailJob?.status === 'UNKNOWN' || detailJob?.targetAckPending"
        class="job-notice"
      >
        <span>{{
          detailJob.targetAckPending
            ? "正在等待目标确认收尾"
            : "目标结果未确认，相关变更已锁定"
        }}</span
        ><button class="secondary" :disabled="busy" @click="reconcile">
          核对原作业
        </button>
      </div>
      <button
        v-if="detailJob?.status === 'QUEUED'"
        class="secondary"
        :disabled="busy"
        @click="cancelJob"
      >
        取消排队
      </button>
      <div v-if="selected?.observedVersion">
        <h4>实际工具版本</h4>
        <pre class="version-output">{{ selected.observedVersion }}</pre>
      </div>
      <details v-if="selected">
        <summary>镜像身份</summary>
        <code>{{ selected.imageDigest }}</code
        ><code>{{ selected.imageId || "尚未确认" }}</code>
      </details>
      <button v-if="detailJob" class="secondary" @click="showJobLogs(detailJob.id)">查看日志</button>
    </div>
  </ElDialog>
</template>
