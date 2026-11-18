<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { ElMessage } from "element-plus";
import { request } from "./api";
import { requestAllPages } from "./listPages";
import OpsPagination from "./OpsPagination.vue";
import { usePagination } from "./pagination";
import { hostTime, type Host } from "./hosts";
import {
  softwareIsHistoryOnly, softwareRuntimeLabels, softwareState, softwareTypeLabel,
  softwareVersionText, type SoftwareInstance,
} from "./software";

const emit = defineEmits<{ detail: [string] }>();
const rows = ref<SoftwareInstance[]>([]);
const hosts = ref<Host[]>([]);
const query = ref(""), hostId = ref(""), expanded = ref("");
const loading = ref(false), loaded = ref(false);
const refreshFailed = ref(false);
const abort = new AbortController();

function currentObservation(item: SoftwareInstance) {
  return item.freshness === "FRESH" && (!item.latestAttempt?.code || ["READY", "NOT_CHECKED"].includes(item.latestAttempt.code));
}
function locations(item: SoftwareInstance) {
  const saved = item.installationPaths, official = item.installation?.official?.paths, observed = item.observation;
  const recorded = "安装记录";
  const actual = currentObservation(item) ? "主机实测" : "上次检测";
  const binary = observed?.executablePath || item.installationEvidence?.binaryPath || official?.binary;
  const values = [
    { label: "安装目录", value: saved?.root === "/" ? undefined : saved?.root, source: recorded },
    { label: "程序路径", value: binary, source: observed?.executablePath ? actual : recorded },
    { label: "程序目录", value: binary ? undefined : saved?.bin, source: recorded },
    { label: "配置路径", value: official?.config || saved?.config, source: recorded },
    { label: "数据路径", value: official?.data || saved?.data, source: recorded },
    { label: "日志目录", value: official?.logs || saved?.logs, source: recorded },
    { label: "服务文件", value: observed?.serviceFile, source: actual },
    { label: "服务日志", value: observed?.logPath, source: actual },
    { label: "Docker 数据根目录", value: observed?.dockerRootDir, source: actual },
  ];
  return values.filter((entry): entry is { label: string; value: string; source: string } => !!entry.value)
    .filter((entry, index, all) => all.findIndex((other) => other.value === entry.value && other.label === entry.label) === index);
}
const hostById = computed(() => new Map(hosts.value.map((host) => [host.id, host])));
const records = computed(() => rows.value.map((item) => {
  const host = hostById.value.get(item.hostId);
  const configuredPorts = [...new Set([item.installation?.config?.hostPort, ...Object.values(item.installation?.config?.ports || {})].filter((port): port is number => !!port))];
  const mapped = item.observation?.portMappings || [];
  const observedPorts = mapped.length ? mapped : item.observation?.listeningPorts || [];
  const endpoint = observedPorts.length ? observedPorts : configuredPorts.map((port) => `${port}（安装记录）`);
  const paths = locations(item);
  const mountPaths = item.observation?.mounts?.flatMap((mount) => [mount.source, mount.destination, mount.volume]) || [];
  const locator = item.runtimeKind === "DOCKER" ? item.observation?.containerName || item.locator.slice(0, 12) : item.locator;
  return {
    item, host, endpoint, paths, locator, portLabel: mapped.length ? "宿主机 → 容器端口" : observedPorts.length ? "监听端口" : "登记端口",
    search: [item.name, softwareTypeLabel(item), host?.name, host?.address, locator,
      ...endpoint, ...configuredPorts, ...paths.map((entry) => entry.value), ...mountPaths].join(" ").toLocaleLowerCase(),
  };
}));
const visible = computed(() => records.value.filter(({ item, search }) =>
  (!hostId.value || item.hostId === hostId.value) && search.includes(query.value.trim().toLocaleLowerCase()),
));
const { page, pageSize, total, pageItems } = usePagination(() => visible.value, [query, hostId]);

async function refresh() {
  if (loading.value) return;
  loading.value = true; refreshFailed.value = false;
  try {
    const [software, targets] = await Promise.all([
      requestAllPages<SoftwareInstance>("/software-instances", abort.signal),
      request<Host[]>("/hosts", "GET", undefined, abort.signal),
    ]);
    rows.value = software.filter((item) => item.managementMode !== "RELEASED" && item.actualState !== "REMOVED" && !softwareIsHistoryOnly(item));
    hosts.value = targets;
    loaded.value = true;
  } catch {
    if (!abort.signal.aborted) refreshFailed.value = true;
  } finally {
    loading.value = false;
  }
}
function logSource(item: SoftwareInstance) {
  const source = item.observation?.logSource || "";
  if (source === "journal") return "systemd 日志";
  if (source === "file") return "文件";
  if (source === "docker:none") return "Docker 日志已禁用";
  return source.startsWith("docker:") ? `Docker 日志驱动：${source.slice(7)}` : "";
}
async function copy(value: string) {
  try { await navigator.clipboard.writeText(value); ElMessage.success("已复制"); }
  catch { ElMessage.info("请选中路径手动复制"); }
}
onMounted(refresh);
onUnmounted(() => abort.abort());
</script>

<template>
  <section class="content panel-card software-directory" aria-label="软件目录">
    <div class="directory-toolbar">
      <label>主机 <select v-model="hostId" aria-label="主机"><option value="">全部主机</option><option v-for="host in hosts" :key="host.id" :value="host.id">{{ host.name }} · {{ host.address || "本机" }}</option></select></label>
      <input v-model="query" type="search" aria-label="搜索软件目录" placeholder="软件、路径、服务或端口" />
      <button class="secondary" :disabled="loading" @click="refresh">{{ loading ? "读取中…" : "刷新" }}</button>
    </div>
    <p v-if="refreshFailed && loaded" class="directory-refresh-error" role="status">刷新失败，仍显示上次读取的目录。</p>
    <p v-if="!pageItems.length" class="empty">{{ loading ? "正在读取软件目录…" : loaded ? "没有匹配的软件" : "软件目录读取失败，请重试" }}</p>
    <div v-else class="table-scroll">
      <table>
        <thead><tr><th scope="col">软件</th><th scope="col">主机</th><th scope="col">运行对象</th><th scope="col">状态</th><th scope="col">端口</th><th scope="col">操作</th></tr></thead>
        <tbody>
          <template v-for="record in pageItems" :key="record.item.id">
            <tr class="directory-row">
              <td><strong>{{ record.item.name }}</strong><small>{{ softwareTypeLabel(record.item) }}<template v-if="record.item.runtimeKind !== 'DOCKER' && (record.item.observation?.version || record.item.softwareVersion)"> · {{ softwareVersionText(record.item.observation?.version || record.item.softwareVersion) }}</template></small></td>
              <td>{{ record.host?.name || record.item.hostName || "—" }}<small v-if="record.host?.address">{{ record.host.address }}</small></td>
              <td><code :title="record.item.locator">{{ record.locator }}</code><small>{{ softwareRuntimeLabels[record.item.runtimeKind] }}</small></td>
              <td><span class="state-tag" :class="softwareState(record.item).tone">{{ softwareState(record.item).label }}</span></td>
              <td><template v-if="record.endpoint.length"><code>{{ record.endpoint[0] }}</code><small v-if="record.endpoint.length > 1">另有 {{ record.endpoint.length - 1 }} 项</small></template><span v-else class="muted">{{ record.item.runtimeKind === "TOOL" ? "—" : "未确认" }}</span></td>
              <td><div class="directory-actions">
                <button class="quiet" :aria-expanded="expanded === record.item.id" @click="expanded = expanded === record.item.id ? '' : record.item.id">{{ expanded === record.item.id ? "收起" : "查看位置" }}</button>
                <button class="quiet" @click="emit('detail', record.item.id)">详情</button>
              </div></td>
            </tr>
            <tr v-if="expanded === record.item.id" class="directory-expanded"><td colspan="6">
              <dl class="location-list">
                <template v-for="entry in record.paths" :key="`${entry.label}:${entry.value}`">
                  <dt>{{ entry.label }}</dt><dd><code>{{ entry.value }}</code><small>{{ entry.source }}</small><button class="quiet" @click="copy(entry.value)">复制</button></dd>
                </template>
                <template v-if="record.item.runtimeKind === 'DOCKER' && record.item.observation?.version">
                  <dt>镜像</dt><dd><code>{{ record.item.observation.version }}</code><button class="quiet" @click="copy(record.item.observation.version)">复制</button></dd>
                </template>
                <template v-if="logSource(record.item)"><dt>日志来源</dt><dd>{{ logSource(record.item) }}</dd></template>
                <template v-if="record.endpoint.length"><dt>{{ record.portLabel }}</dt><dd><code>{{ record.endpoint.join("，") }}</code></dd></template>
                <template v-for="(mount, index) in record.item.observation?.mounts" :key="index">
                  <dt>{{ mount.volume ? "数据卷" : "挂载" }}</dt>
                  <dd><code>{{ mount.volume || mount.source || "—" }} → {{ mount.destination }}</code><small>{{ mount.readOnly ? "只读" : "读写" }}</small><button v-if="mount.source" class="quiet" @click="copy(mount.source)">复制宿主路径</button><code v-if="mount.volume && mount.source" class="volume-path">{{ mount.source }}</code></dd>
                </template>
              </dl>
              <p v-if="record.item.observation?.locationStatus === 'UNAVAILABLE'" class="muted">位置读取未完成，可在详情中检查主机连接和读取权限。</p>
              <p v-else-if="!record.paths.length && !record.item.observation?.mounts?.length" class="muted">尚无已确认的软件位置。</p>
              <p v-if="record.item.observation?.observedAt" class="muted directory-observed">{{ currentObservation(record.item) ? "检测于" : "上次检测于" }} {{ hostTime(record.item.observation.observedAt) }}</p>
            </td></tr>
          </template>
        </tbody>
      </table>
    </div>
    <OpsPagination v-if="total" v-model:page="page" v-model:page-size="pageSize" :total="total" label="软件目录分页" />
  </section>
</template>

<style scoped>
.software-directory { min-width: 0; }
.directory-toolbar { display: flex; align-items: center; gap: 12px; margin-bottom: 14px; }
.directory-toolbar label { display: flex; align-items: center; gap: 8px; white-space: nowrap; color: var(--muted); font-size: 13px; }
.directory-toolbar input { flex: 1; min-width: 0; }
.directory-toolbar select { max-width: 240px; }
.directory-toolbar button { margin-left: auto; }
.directory-toolbar input, .directory-toolbar select { min-height: 34px; padding: 6px 10px; border: 1px solid var(--border); border-radius: 6px; background: var(--surface); }
.directory-toolbar .secondary, td button { white-space: nowrap; }
.directory-row td { white-space: normal; overflow-wrap: anywhere; }
.directory-row td > code { overflow-wrap: anywhere; }
.directory-actions { display: flex; flex-wrap: wrap; gap: 4px; min-width: 104px; }
.directory-actions .quiet { margin: 0; }
.directory-refresh-error { margin: 0 0 12px; color: var(--error); font-size: 13px; }
td strong { font-weight: 600; }
td small { display: block; margin-top: 3px; font-size: 12px; color: var(--muted); }
.directory-expanded td { white-space: normal; background: #f8fafc; }
.location-list { display: grid; grid-template-columns: auto minmax(0, 1fr); gap: 8px 16px; margin: 0; }
.location-list dt { color: var(--muted); padding-top: 2px; }
.location-list dd { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 10px; min-width: 0; margin: 0; overflow-wrap: anywhere; }
.location-list dd .quiet { padding: 2px 6px; margin: 0; font-size: 12px; }
.volume-path { flex-basis: 100%; }
.directory-observed { margin: 10px 0 0; font-size: 12px; }
@media (max-width: 710px) {
  .directory-toolbar { flex-wrap: wrap; }
  .directory-toolbar label { width: 100%; }
  .directory-toolbar select { max-width: 100%; flex: 1; min-width: 0; }
  .table-scroll { overflow: visible; }
  table { min-width: 0; display: block; }
  thead { position: absolute; width: 1px; height: 1px; margin: -1px; overflow: hidden; clip-path: inset(50%); }
  tbody { display: block; }
  .directory-row { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 8px 12px; padding: 12px; border-top: 1px solid var(--border); }
  .directory-row:first-child { border-top: 0; }
  .directory-row td, .directory-expanded td { padding: 0; border: 0; min-width: 0; }
  .directory-row td:nth-child(6) { grid-column: 1 / -1; }
  .directory-row td:nth-child(3) { grid-column: 1 / -1; }
  .directory-row td:nth-child(3)::before { content: "运行对象"; display: block; color: var(--muted); font-size: 12px; margin-bottom: 3px; }
  .directory-row td:nth-child(5)::before { content: "端口"; display: block; color: var(--muted); font-size: 12px; margin-bottom: 3px; }
  .directory-expanded, .directory-expanded td { display: block; }
  .directory-expanded td { padding: 12px; }
  .location-list { grid-template-columns: minmax(0,1fr); gap: 4px; }
  .location-list dd { margin-bottom: 8px; }
}
</style>
