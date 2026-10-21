<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { ElDialog, ElMessage, ElMessageBox } from "element-plus";
import { request } from "./api";
import SoftwareIcon from "./SoftwareIcon.vue";
import ServiceInstallDialog from "./ServiceInstallDialog.vue";
import {
  serviceDescriptions,
  isTool,
  type ServiceRecipe,
} from "./serviceInstallation";
import { hostObservationExpired, type Host } from "./hosts";

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

const props = defineProps<{ hostId: string }>();
const emit = defineEmits<{ error: [unknown]; preparation: [string] }>();
const targets = ref<Host[]>([]);
const serviceDefinitions = ref<ServiceRecipe[]>([]);
const serviceRecipe = ref<ServiceRecipe>();
const installations = ref<Installation[]>([]);
const targetId = ref("");
const search = ref("");
const category = ref("ALL");
const selectedVersions = ref<Record<string, string>>({});
const categories = [
  { key: "ALL", label: "全部软件" },
  { key: "SERVICES", label: "服务软件" },
  { key: "JAVA_RUNTIME", label: "运行环境" },
  { key: "MAVEN_JDK_BUILD", label: "构建工具" },
  { key: "MEDIA_TOOLS", label: "媒体工具" },
  { key: "PROGRAMMING", label: "编程软件" },
];
function recipeSubtitle(d: ServiceRecipe) {
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
      : "Web 与反向代理";
}
const catalogEntries = computed(() => {
  const term = search.value.trim().toLowerCase();
  const groups = new Map<string, ServiceRecipe[]>();
  for (const definition of serviceDefinitions.value) {
    if (category.value === "SERVICES" && isTool(definition)) continue;
    if (
      category.value === "JAVA_RUNTIME" &&
      !["java", "python"].includes(definition.key)
    )
      continue;
    if (category.value === "MAVEN_JDK_BUILD" && definition.key !== "maven")
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
    const versions = groups.get(definition.key) || [];
    versions.push(definition);
    groups.set(definition.key, versions);
  }
  return Array.from(groups.values(), (versions) => ({
    definition:
      versions.find((d) => d.version === selectedVersions.value[d.key]) ||
      versions[0]!,
    versions,
  }));
});
const legacyInstallations = computed(() =>
  installations.value.filter((item) => item.targetId === targetId.value),
);
const legacyName = (item: Installation) =>
  item.pluginKey === "java-runtime" ? "Java" : "Maven";
const busy = ref(false);
const loading = ref(false);
const selectedId = ref("");
const detailJob = ref<Job | null>(null);
const jobLogs = ref("");
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
    const [ts, rows, services] = await Promise.all([
      request<Host[]>("/hosts"),
      request<Installation[]>("/plugin-installations"),
      request<ServiceRecipe[]>("/software-recipes"),
    ]);
    if (disposed) return;
    targets.value = ts;
    installations.value = rows;
    serviceDefinitions.value = services;
    if (!targetId.value && ts.length)
      targetId.value = ts.some((t) => t.id === props.hostId)
        ? props.hostId
        : ts[0]!.id;
    if (dialog.value && selected.value?.lastJobId)
      detailJob.value = await request<Job>("/jobs/" + selected.value.lastJobId);
  } finally {
    loading.value = false;
  }
}
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
  jobLogs.value = "";
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
    else ElMessage.error(result.errorCode || "环境校验失败，请查看详情");
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
async function loadLogs() {
  const j = detailJob.value;
  if (!j) return;
  await act(async () => {
    jobLogs.value = (
      await request<{ text: string }>("/jobs/" + j.id + "/logs")
    ).text;
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
        <select v-model="targetId" aria-label="目标主机" :disabled="busy">
          <option v-if="!targets.length" value="">暂无主机</option>
          <option v-for="t in targets" :key="t.id" :value="t.id">
            {{ t.name }} · {{ t.address || "本机" }}
          </option>
        </select>
      </div>
      <span class="target-capability" :class="{ available: ready }"
        ><i></i>{{ ready ? "变更执行已就绪" : "待检查 / 未就绪" }}</span
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
          <dd>{{ currentTarget.capabilities?.platform || "待检查" }}</dd>
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
    </div>
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
    <p v-else-if="!ready" class="target-hint">
      {{
        currentTarget?.maintenance
          ? "主机维护中，暂不接受新的安装操作。"
          : currentTarget && !hostObservationExpired(currentTarget, Date.now())
            ? currentTarget.capabilities?.mutation.status !== "AVAILABLE"
              ? currentTarget.capabilities?.mutation.message
              : currentTarget.capabilities.docker.message
            : "安装前请检查主机能力。"
      }}
    </p>
    <div class="catalog-count" aria-live="polite">
      {{ catalogEntries.length }} 项软件<span>服务与工具</span>
    </div>
    <div v-if="!catalogEntries.length && !loading" class="empty">
      没有匹配的软件
    </div>
    <div v-else class="plugin-grid">
      <article
        v-for="{ definition, versions } in catalogEntries"
        :key="definition.key"
        class="plugin-card"
      >
        <div class="plugin-heading">
          <SoftwareIcon :type="definition.key" />
          <div class="plugin-title">
            <h3>{{ definition.name }}</h3>
            <span>{{ recipeSubtitle(definition) }}</span>
          </div>
        </div>
        <p class="plugin-description">
          {{ serviceDescriptions[definition.key] || definition.name }}
        </p>
        <div class="plugin-meta">
          <select
            v-if="versions.length > 1"
            :value="definition.version"
            :aria-label="`${definition.name}版本`"
            class="version-select"
            @change="
              selectedVersions[definition.key] = (
                $event.target as HTMLSelectElement
              ).value
            "
          >
            <option
              v-for="version in versions"
              :key="version.version"
              :value="version.version"
            >
              {{ version.version }}
            </option></select
          ><span v-else class="version-label">{{ definition.version }}</span
          ><span>{{ definition.platform }}</span>
        </div>
        <footer>
          <span class="recipe-kind"
            >{{
              definition.runtimeModes
                .map((mode) => (mode === "NATIVE" ? "原生" : "Docker"))
                .join(" / ")
            }}
            · 在线 / 离线</span
          >
          <div class="recipe-actions">
            <a
              class="official-link"
              :href="definition.officialUrl"
              target="_blank"
              rel="noopener noreferrer"
              >官方说明</a
            ><button
              class="primary"
              :disabled="busy || !currentTarget || currentTarget.maintenance"
              @click="serviceRecipe = definition"
            >
              安装
            </button>
          </div>
        </footer>
      </article>
    </div>
    <details v-if="legacyInstallations.length" class="legacy-installations">
      <summary>已有工具镜像（{{ legacyInstallations.length }}）</summary>
      <p class="muted">原发布流程使用的环境记录，保持现有引用与维护。</p>
      <div
        v-for="item in legacyInstallations"
        :key="item.id"
        class="legacy-row"
      >
        <strong>{{ legacyName(item) }} · {{ item.pluginVersion }}</strong
        ><span>{{ labels[item.status] || item.status }}</span>
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
    </details>
  </section>
  <ServiceInstallDialog
    :open="!!serviceRecipe"
    :recipe="serviceRecipe"
    :host="currentTarget"
    @close="serviceRecipe = undefined"
    @submitted="act(refresh)"
    @error="emit('error', $event)"
    @preparation="emit('preparation', $event)"
  />
  <ElDialog v-model="dialog" title="环境详情" width="min(660px,94vw)">
    <div class="dialog-form">
      <dl class="plugin-detail">
        <dt>环境状态</dt>
        <dd>
          {{ labels[selected?.status || detailJob?.status || ""] || "—" }}
        </dd>
        <dt>作业阶段</dt>
        <dd>{{ phases[detailJob?.phase || ""] || detailJob?.phase || "—" }}</dd>
        <dt>错误码</dt>
        <dd>{{ selected?.errorCode || detailJob?.errorCode || "—" }}</dd>
        <dt>观察时间</dt>
        <dd>{{ time(selected?.observedAt || detailJob?.updatedAt) }}</dd>
        <dt>作业</dt>
        <dd>
          <code>{{ detailJob?.id }}</code>
        </dd>
        <dt>追踪</dt>
        <dd>
          <code>{{ detailJob?.traceId }}</code>
        </dd>
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
      <details
        v-if="
          detailJob &&
          detailJob.status !== 'QUEUED' &&
          detailJob.phase !== 'CANCELLED'
        "
      >
        <summary>执行日志</summary>
        <button class="quiet" :disabled="busy" @click="loadLogs">
          读取目标日志
        </button>
        <pre v-if="jobLogs" class="version-output">{{ jobLogs }}</pre>
      </details>
    </div>
  </ElDialog>
</template>

<style scoped>
.legacy-installations {
  margin-top: 28px;
  border-top: 1px solid var(--border);
  padding-top: 20px;
  font-size: 12px;
}
.legacy-installations summary {
  cursor: pointer;
  color: var(--muted);
}
.legacy-row {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
  padding: 12px 0;
  border-bottom: 1px solid var(--border);
}
.legacy-row strong {
  flex: 1;
  min-width: 150px;
  font-weight: 500;
}
.legacy-row > span {
  color: var(--muted);
}

.catalog-target {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
  background: #f8fafd;
  border: 1px solid var(--border);
  padding: 14px 18px;
  border-radius: 7px;
}
.target-choice {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}
.target-label {
  font-size: 12px;
  color: var(--muted);
  white-space: nowrap;
}
.target-choice select {
  width: 250px;
  height: 36px;
  margin: 0;
  padding: 6px 10px;
  font-size: 13px;
}
.target-capability {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--muted);
}
.target-capability i {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}
.target-capability.available {
  color: var(--success);
}
.target-facts {
  margin-left: auto;
  font-size: 12px;
}
.target-facts[open] {
  flex-basis: 100%;
  margin-left: 0;
}
.target-facts dl {
  display: grid;
  grid-template-columns: 85px minmax(0, 1fr);
  gap: 12px;
  margin: 16px 0 2px;
}
.target-facts dt {
  color: var(--muted);
}
.target-facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.catalog-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  flex-wrap: wrap;
  margin: 26px 0 0;
}
.catalog-categories {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}
.catalog-categories button {
  border: 0;
  padding: 8px 13px;
  border-radius: 5px;
  background: transparent;
  color: var(--muted);
  font-size: 13px;
}
.catalog-categories button.selected {
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 600;
}
.catalog-search {
  display: flex;
  align-items: center;
  gap: 10px;
}
.catalog-search input {
  margin: 0;
  width: 220px;
  height: 36px;
  font-size: 13px;
}
.catalog-search button {
  height: 36px;
}
.catalog-count {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 22px 0 14px;
  color: var(--muted);
  font-size: 12px;
}
.catalog-count span {
  margin-left: auto;
  color: #8a96a7;
}
.target-hint {
  margin: 14px 0 0;
  padding: 10px 14px;
  border-left: 2px solid #d4ae69;
  background: #fffbf2;
  color: #8a6b35;
  font-size: 12px;
  line-height: 1.7;
}
.plugin-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 18px;
}
.plugin-card {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--border);
  background: var(--surface);
  border-radius: 8px;
  padding: 22px 22px 0;
  min-width: 0;
  transition:
    box-shadow 150ms,
    border-color 150ms;
}
.plugin-card:hover {
  border-color: #bfcfe2;
  box-shadow: 0 5px 18px #20395808;
}
.plugin-heading {
  display: flex;
  align-items: center;
  gap: 14px;
}
.plugin-title {
  flex: 1;
  min-width: 0;
}
.plugin-title h3 {
  margin: 0 0 6px;
  font:
    600 19px "Bahnschrift",
    "Microsoft YaHei UI",
    sans-serif;
  color: #2d3b4e;
}
.plugin-title > span {
  font-size: 12px;
  color: var(--muted);
}
.plugin-heading .state-tag {
  flex-shrink: 0;
}
.plugin-description {
  color: #7a8798;
  font-size: 12px;
  line-height: 1.8;
  margin: 20px 0 16px;
  min-height: 22px;
}
.plugin-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 10px;
  color: #8a96a6;
  font-size: 11px;
  min-height: 30px;
  margin-bottom: 20px;
}
.version-select {
  width: auto;
  max-width: 100%;
  min-height: 30px;
  margin: 0;
  padding: 4px 26px 4px 8px;
  font: 12px var(--mono);
  color: #596b82;
  background-color: #f5f7fa;
  border-color: #e5eaf0;
}
.version-label {
  font: 12px var(--mono);
  color: #596b82;
  background: #f5f7fa;
  border-radius: 4px;
  padding: 5px 8px;
}
.plugin-card footer {
  border-top: 1px solid #eff1f5;
  margin: auto -22px 0;
  padding: 12px 20px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  min-height: 60px;
}
.recipe-kind {
  color: #8a95a4;
  font-size: 11px;
}
.recipe-actions {
  display: flex;
  gap: 5px;
  align-items: center;
  flex-wrap: wrap;
  justify-content: flex-end;
}
.recipe-actions .primary {
  min-height: 32px;
  padding: 5px 18px;
}
.official-link {
  color: #6e8299;
  text-decoration: none;
  font-size: 12px;
  padding: 5px 7px;
}
.official-link:hover {
  color: var(--accent);
}
.error-code {
  color: #b74c49;
  font:
    12px Consolas,
    monospace;
  overflow-wrap: anywhere;
}
.plugin-detail {
  display: grid;
  grid-template-columns: 90px minmax(0, 1fr);
  gap: 14px;
  margin: 0;
  font-size: 13px;
}
.plugin-detail dt {
  color: #6b8095;
}
.plugin-detail dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.version-output {
  background: #edf4fa;
  color: #31516c;
  padding: 14px;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font:
    12px/1.7 Consolas,
    monospace;
  max-height: 250px;
  overflow: auto;
}
.job-notice {
  background: #edf4fa;
  color: #31516c;
  padding: 12px;
  display: flex;
  align-items: center;
  gap: 14px;
  justify-content: space-between;
  font-size: 13px;
  flex-wrap: wrap;
}
details summary {
  cursor: pointer;
  color: #507596;
  font-size: 13px;
}
details code {
  display: block;
  margin-top: 12px;
  overflow-wrap: anywhere;
  font-size: 12px;
}
h4 {
  font-size: 13px;
  font-weight: 500;
  margin-bottom: 10px;
}
@media (max-width: 1250px) {
  .plugin-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 650px) {
  .catalog-target {
    gap: 12px;
    padding: 14px;
  }
  .target-choice {
    flex-basis: 100%;
  }
  .target-choice select {
    width: 0;
    flex: 1;
  }
  .target-facts {
    margin-left: 0;
  }
  .catalog-toolbar {
    gap: 14px;
    margin-top: 20px;
  }
  .catalog-search {
    width: 100%;
  }
  .catalog-search input {
    flex: 1;
    width: 0;
    min-width: 0;
  }
  .plugin-grid {
    grid-template-columns: 1fr;
    gap: 14px;
  }
  .plugin-card {
    padding: 20px 20px 0;
  }
  .plugin-card footer {
    margin-left: -20px;
    margin-right: -20px;
  }
}
</style>
