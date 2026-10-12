<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { ElDialog, ElMessage, ElMessageBox } from "element-plus";
import { request } from "./api";

interface Definition {
  key: string;
  version: string;
  kind: string;
  platform: string;
  image: string;
  digest: string;
  recipeRevision: string;
}
interface Target {
  id: string;
  name: string;
  address: string;
  observation: { dockerVersion?: string; composeVersion?: string; platform?: string; pendingJobId?: string; dockerAvailableBytes?: number; pluginCatalogHash?: string };
  observedAt: string;
}
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
interface Plan { id: string; targetId: string; definition: Definition; expiresAt: string }
interface Job { id: string; status: string; phase: string; errorCode: string; traceId: string; updatedAt: string; targetAckPending: boolean }

const emit = defineEmits<{ error: [unknown] }>();
const targets = ref<Target[]>([]);
const definitions = ref<Definition[]>([]);
const installations = ref<Installation[]>([]);
const targetId = ref("");
const busy = ref(false);
const loading = ref(false);
const plan = ref<Plan | null>(null);
const planKey = ref("");
const selectedId = ref("");
const detailJob = ref<Job | null>(null);
const jobLogs = ref("");
const dialog = ref(false);
const currentTarget = computed(() => targets.value.find(t => t.id === targetId.value));
const selected = computed(() => installations.value.find(i => i.id === selectedId.value));
const labels: Record<string, string> = { INSTALLING: "安装中", REMOVING: "卸载中", READY: "可用", FAILED: "异常", UNKNOWN: "未知", QUEUED: "排队中", RUNNING: "执行中", SUCCEEDED: "成功" };
const phases: Record<string, string> = { PREFLIGHT: "预检", TARGET_EXECUTION: "目标执行", PULLING: "拉取镜像", VERIFYING: "校验摘要", VERSION_CHECK: "探测版本", RECORDED: "完成登记", REMOVING: "卸载", REMOVED: "已卸载", PLUGIN_ERROR: "插件异常", NOT_STARTED: "未开始", INTENT_RECORDED: "等待核对", CANCELLED: "已取消" };
const time = (s?: string) => s ? new Date(s).toLocaleString("zh-CN", { timeZone: "Asia/Shanghai", hour12: false }) : "—";
const name = (d: Definition) => d.kind === "JAVA_RUNTIME" ? "Java 运行环境" : "Maven 构建环境";
const installation = (d: Definition) => installations.value.find(i => i.targetId === targetId.value && i.pluginKey === d.key && i.pluginVersion === d.version && i.imageDigest === d.digest);
const ready = computed(() => {
  const t = currentTarget.value;
  return !!t?.observation.dockerVersion && !!t.observation.composeVersion && !!t.observation.pluginCatalogHash && t.observation.platform === "linux/amd64" && !t.observation.pendingJobId && Date.now() - Date.parse(t.observedAt) < 60000;
});
let timer: ReturnType<typeof setTimeout> | undefined;
let disposed = false;

async function refresh() {
  if (loading.value) return;
  loading.value = true;
  try {
    const [ts, ds, rows] = await Promise.all([request<Target[]>("/targets"), request<Definition[]>("/plugins"), request<Installation[]>("/plugin-installations")]);
    targets.value = ts;
    definitions.value = ds;
    installations.value = rows;
    if (!targetId.value && ts[0]) targetId.value = ts[0].id;
    if (dialog.value && selected.value?.lastJobId) detailJob.value = await request<Job>("/jobs/" + selected.value.lastJobId);
  } finally { loading.value = false; }
}
async function act(fn: () => Promise<void>) {
  if (busy.value) return;
  busy.value = true;
  try { await fn(); } catch (e) { emit("error", e); } finally { busy.value = false; }
}
async function probe() {
  await act(async () => { await request("/targets/" + targetId.value + "/probes", "POST"); await refresh(); });
}
async function preview(d: Definition) {
  await act(async () => {
    plan.value = await request<Plan>("/targets/" + targetId.value + "/plugin-install-plans", "POST", { pluginKey: d.key, version: d.version, recipeRevision: d.recipeRevision });
    planKey.value = crypto.randomUUID();
  });
}
async function submit() {
  const p = plan.value;
  if (!p) return;
  await act(async () => {
    const result = await request<{ jobId: string; pluginInstallationId: string }>("/plugin-install-plans/" + p.id + "/submit", "POST", { idempotencyKey: planKey.value });
    plan.value = null;
    selectedId.value = result.pluginInstallationId;
    detailJob.value = await request<Job>("/jobs/" + result.jobId);
    dialog.value = true;
    await refresh();
  });
}
async function details(i: Installation) {
  selectedId.value = i.id;
  detailJob.value = null;
  jobLogs.value = "";
  dialog.value = true;
  await act(async () => { detailJob.value = await request<Job>("/jobs/" + i.lastJobId); });
}
async function verify(i: Installation) {
  await act(async () => {
    const result = await request<{ status: string; errorCode: string }>("/plugin-installations/" + i.id + "/verify", "POST");
    await refresh();
    if (result.status === "READY") ElMessage.success("环境校验通过");
    else ElMessage.error(result.errorCode || "环境校验失败，请查看详情");
  });
}
async function remove(i: Installation) {
  try { await ElMessageBox.confirm("卸载该版本环境？服务器原有的镜像会保留。", "卸载环境", { confirmButtonText: "卸载", cancelButtonText: "取消", type: "warning" }); } catch { return; }
  await act(async () => {
    const result = await request<{ jobId: string }>("/plugin-installations/" + i.id + "/remove", "POST", { idempotencyKey: crypto.randomUUID() });
    detailJob.value = await request<Job>("/jobs/" + result.jobId);
    selectedId.value = i.id;
    dialog.value = true;
    await refresh();
  });
}
async function reconcile() {
  const j = detailJob.value;
  if (!j) return;
  await act(async () => { await request("/jobs/" + j.id + "/reconcile", "POST"); detailJob.value = await request<Job>("/jobs/" + j.id); await refresh(); });
}
async function loadLogs() {
  const j = detailJob.value;
  if (!j) return;
  await act(async () => { jobLogs.value = (await request<{ text: string }>("/jobs/" + j.id + "/logs")).text; });
}
async function cancelJob() {
  const j = detailJob.value;
  if (!j) return;
  await act(async () => { await request("/jobs/" + j.id + "/cancel", "POST"); await refresh(); detailJob.value = await request<Job>("/jobs/" + j.id); });
}
function poll() {
  if (disposed) return;
  timer = setTimeout(async () => {
    try { if (!document.hidden && !busy.value) { await refresh(); if (dialog.value && detailJob.value) detailJob.value = await request<Job>("/jobs/" + detailJob.value.id); } }
    catch (e) { emit("error", e); }
    poll();
  }, 4000);
}
onMounted(async () => { await act(refresh); poll(); });
onUnmounted(() => { disposed = true; if (timer) clearTimeout(timer); });
</script>

<template>
  <section class="content plugin-center" aria-label="插件中心">
    <div class="section-head">
      <h2>环境插件</h2>
      <button class="secondary" :disabled="busy || loading" @click="act(refresh)">刷新</button>
    </div>
    <div class="target-bar">
      <label>目标主机<select v-model="targetId" aria-label="目标主机" :disabled="busy"><option v-for="t in targets" :key="t.id" :value="t.id">{{ t.name }} · {{ t.address }}</option></select></label>
      <div class="capability"><span>Docker</span><strong>{{ currentTarget?.observation.dockerVersion || "待检查" }}</strong></div>
      <div class="capability"><span>Compose</span><strong>{{ currentTarget?.observation.composeVersion || "待检查" }}</strong></div>
      <div v-if="currentTarget?.observation.dockerAvailableBytes !== undefined" class="capability"><span>镜像盘可用空间</span><strong>{{ (currentTarget.observation.dockerAvailableBytes / 1073741824).toFixed(1) }} GiB</strong></div>
      <button class="secondary" :disabled="busy || !targetId" @click="probe">检查目标</button>
    </div>
    <div v-if="!targets.length" class="empty">请先在“主机与依赖”登记 Linux 目标</div>
    <p v-else-if="!ready" class="target-hint">{{ currentTarget?.observation.pendingJobId ? "目标有未收尾作业，请在作业详情中核对。" : currentTarget?.observation.dockerVersion && !currentTarget.observation.pluginCatalogHash ? "目标执行器需要更新，才能安装环境插件。" : "安装前请检查目标，确认 Docker 与 Compose 已就绪。" }}</p>
    <div v-if="targets.length" class="plugin-grid">
      <article v-for="d in definitions" :key="d.key + d.version" class="plugin-card">
        <div class="plugin-heading"><div><span class="category">{{ d.kind === 'JAVA_RUNTIME' ? '运行环境' : '构建环境' }}</span><h3>{{ name(d) }}</h3></div><span class="state-tag" :class="installation(d)?.status === 'READY' ? 'state-success' : installation(d)?.status === 'FAILED' ? 'state-error' : 'state-muted'">{{ installation(d)?.referenceCount ? '被引用' : labels[installation(d)?.status || ''] || '未安装' }}</span></div>
        <strong class="plugin-version">{{ d.version.replace('-jre', '').replace('-jdk-', ' / JDK ') }}<small v-if="d.kind === 'JAVA_RUNTIME'">JRE</small></strong>
        <div class="plugin-meta"><span>{{ d.platform }}</span><span>Docker 镜像</span></div>
        <p v-if="installation(d)?.errorCode" class="error-code">{{ installation(d)?.errorCode }}</p>
        <footer>
          <button v-if="!installation(d) || installation(d)?.status === 'FAILED'" class="primary" :disabled="busy || !ready" @click="preview(d)">{{ installation(d) ? '重新安装' : '安装' }}</button>
          <template v-if="installation(d)">
            <button class="quiet" :disabled="busy" @click="details(installation(d)!)">详情</button>
            <button v-if="['READY','FAILED','UNKNOWN'].includes(installation(d)?.status || '')" class="quiet" :disabled="busy" @click="verify(installation(d)!)">校验</button>
            <button v-if="['READY','FAILED'].includes(installation(d)?.status || '')" class="quiet danger" :disabled="busy || !!installation(d)?.referenceCount" @click="remove(installation(d)!)">卸载</button>
          </template>
        </footer>
      </article>
    </div>
  </section>
  <ElDialog :model-value="!!plan" title="安装计划" width="min(560px,94vw)" @close="plan = null">
    <div v-if="plan" class="dialog-form">
      <dl class="plugin-detail"><dt>目标</dt><dd>{{ targets.find(t => t.id === plan?.targetId)?.name }}</dd><dt>环境</dt><dd>{{ name(plan.definition) }} · {{ plan.definition.version }}</dd><dt>安装方式</dt><dd>Docker 镜像</dd><dt>有效期</dt><dd>{{ time(plan.expiresAt) }}</dd></dl>
      <details><summary>镜像身份</summary><code>{{ plan.definition.image }}@{{ plan.definition.digest }}</code></details>
      <button class="primary" :disabled="busy" @click="submit">确认安装</button>
    </div>
  </ElDialog>
  <ElDialog v-model="dialog" title="环境详情" width="min(660px,94vw)">
    <div class="dialog-form">
      <dl class="plugin-detail"><dt>环境状态</dt><dd>{{ labels[selected?.status || detailJob?.status || ''] || '—' }}</dd><dt>作业阶段</dt><dd>{{ phases[detailJob?.phase || ''] || detailJob?.phase || '—' }}</dd><dt>错误码</dt><dd>{{ selected?.errorCode || detailJob?.errorCode || '—' }}</dd><dt>观察时间</dt><dd>{{ time(selected?.observedAt || detailJob?.updatedAt) }}</dd><dt>作业</dt><dd><code>{{ detailJob?.id }}</code></dd><dt>追踪</dt><dd><code>{{ detailJob?.traceId }}</code></dd><dt>引用数</dt><dd>{{ selected?.referenceCount || 0 }}</dd></dl>
      <div v-if="detailJob?.status === 'UNKNOWN' || detailJob?.targetAckPending" class="job-notice"><span>{{ detailJob.targetAckPending ? '正在等待目标确认收尾' : '目标结果未确认，相关变更已锁定' }}</span><button class="secondary" :disabled="busy" @click="reconcile">核对原作业</button></div>
      <button v-if="detailJob?.status === 'QUEUED'" class="secondary" :disabled="busy" @click="cancelJob">取消排队</button>
      <div v-if="selected?.observedVersion"><h4>实际工具版本</h4><pre class="version-output">{{ selected.observedVersion }}</pre></div>
      <details v-if="selected"><summary>镜像身份</summary><code>{{ selected.imageDigest }}</code><code>{{ selected.imageId || '尚未确认' }}</code></details>
      <details v-if="detailJob && detailJob.status !== 'QUEUED' && detailJob.phase !== 'CANCELLED'"><summary>执行日志</summary><button class="quiet" :disabled="busy" @click="loadLogs">读取目标日志</button><pre v-if="jobLogs" class="version-output">{{ jobLogs }}</pre></details>
    </div>
  </ElDialog>
</template>

<style scoped>
.target-bar { display: flex; align-items: end; gap: 24px; padding: 18px 0 24px; border-bottom: 1px solid #dce5ef; flex-wrap: wrap; }
.target-bar label { flex: 1; min-width: 220px; max-width: 380px; }
.capability { display: grid; gap: 9px; min-width: 100px; font-size: 13px; }
.capability span, .category, .plugin-meta { color: #637d96; font-size: 12px; }
.target-hint { color: #687f95; font-size: 13px; margin: 20px 0 0; }
.plugin-grid { display: grid; grid-template-columns: repeat(auto-fit,minmax(250px,1fr)); gap: 20px; padding-top: 24px; }
.plugin-card { border: 1px solid #d8e4ef; border-top: 3px solid #5f91b9; background: #f9fcff; border-radius: 5px; overflow: hidden; padding: 22px 20px 0; }
.plugin-heading { display: flex; align-items: start; justify-content: space-between; gap: 12px; }
.plugin-heading h3 { margin: 8px 0 0; font-size: 16px; font-weight: 600; color: #284b68; }
.plugin-version { display: flex; align-items: baseline; gap: 10px; font-size: 25px; font-weight: 600; color: #315f83; margin: 28px 0 14px; }
.plugin-version small { font-size: 12px; font-weight: 400; }
.plugin-meta { display: flex; gap: 18px; }
.plugin-card footer { border-top: 1px solid #e1eaf3; margin: 22px -20px 0; padding: 14px 18px; display: flex; align-items: center; gap: 9px; flex-wrap: wrap; min-height: 62px; }
.plugin-card footer button { font-size: 13px; padding: 7px 10px; }
.error-code { color: #b74c49; font: 12px Consolas,monospace; overflow-wrap: anywhere; }
.plugin-detail { display: grid; grid-template-columns: 90px minmax(0,1fr); gap: 14px; margin: 0; font-size: 13px; }
.plugin-detail dt { color: #6b8095; }
.plugin-detail dd { margin: 0; overflow-wrap: anywhere; }
.version-output { background: #edf4fa; color: #31516c; padding: 14px; white-space: pre-wrap; overflow-wrap: anywhere; font: 12px/1.7 Consolas,monospace; max-height: 250px; overflow: auto; }
.job-notice { background: #edf4fa; color: #31516c; padding: 12px; display: flex; align-items: center; gap: 14px; justify-content: space-between; font-size: 13px; flex-wrap: wrap; }
details summary { cursor: pointer; color: #507596; font-size: 13px; }
details code { display: block; margin-top: 12px; overflow-wrap: anywhere; font-size: 12px; }
h4 { font-size: 13px; font-weight: 500; margin-bottom: 10px; }
@media(max-width:600px) { .target-bar { gap: 16px; } .target-bar label { flex: 0 0 100%; min-width: 0; max-width: none; } .plugin-grid { grid-template-columns: 1fr; } }
</style>
