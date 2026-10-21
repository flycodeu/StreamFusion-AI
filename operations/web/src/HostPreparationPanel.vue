<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { ApiError, request } from "./api";
import { hostTime, type Host } from "./hosts";
interface HostPreparation {
  hostId: string;
  expectedHostVersion: string;
  controllerId: string;
  targetId: string;
  installRoot: string;
  account: string;
  platform: string;
  package: {
    available: boolean;
    code: string;
    message: string;
    fileName: string;
    sha256: string;
    byteSize: number;
    downloadUrl?: string;
  };
  commands: {
    preview: string;
    apply: string;
    upgrade: string;
    authorize?: string;
  };
  authorization: { scope: string; command: string };
  steps: {
    key: string;
    label: string;
    status: "AVAILABLE" | "UNAVAILABLE" | "UNKNOWN";
    code: string;
    message: string;
    observedAt?: string;
  }[];
}
const props = defineProps<{ host: Host }>();
const emit = defineEmits<{ checked: [] }>();
const data = ref<HostPreparation>();
const busy = ref(false);
const error = ref<{
  message: string;
  code: string;
  at: string;
  traceId: string;
}>();
const copied = ref("");
const commandEntries = computed(() =>
  [
    { key: "preview", label: "预览变更" },
    { key: "apply", label: "首次准备" },
    { key: "upgrade", label: "已有执行器升级" },
    { key: "authorize", label: "修复受限授权" },
  ].filter(
    (entry) => entry.key !== "authorize" || !!data.value?.commands.authorize,
  ),
);
let generation = 0;
function report(cause: unknown) {
  error.value = {
    message: cause instanceof Error ? cause.message : "安装准备未完成",
    code: cause instanceof ApiError ? cause.code : "REQUEST_NOT_CONFIRMED",
    at: cause instanceof ApiError ? cause.timestamp : new Date().toISOString(),
    traceId: cause instanceof ApiError ? cause.traceId : "",
  };
}
async function load() {
  const revision = ++generation;
  busy.value = true;
  error.value = undefined;
  try {
    const value = await request<HostPreparation>(
      `/hosts/${props.host.id}/preparation`,
    );
    if (revision === generation) data.value = value;
  } catch (cause) {
    if (revision === generation) report(cause);
  } finally {
    if (revision === generation) busy.value = false;
  }
}
watch(
  [() => props.host.id, () => props.host.version, () => props.host.observedAt],
  () => {
    data.value = undefined;
    void load();
  },
  { immediate: true },
);
async function copy(label: string, text: string) {
  try {
    await navigator.clipboard.writeText(text);
    copied.value = label;
  } catch (cause) {
    report(cause);
  }
}
async function download() {
  if (
    !data.value?.package.available ||
    !data.value.package.downloadUrl ||
    busy.value
  )
    return;
  busy.value = true;
  error.value = undefined;
  try {
    const target = new URL(data.value.package.downloadUrl, location.origin);
    if (
      target.origin !== location.origin ||
      target.pathname !==
        `/ops/api/v1/hosts/${props.host.id}/preparation/download`
    )
      throw new Error("安装准备包地址不匹配当前主机，请刷新准备信息");
    const response = await fetch(target, {
      credentials: "same-origin",
      signal: AbortSignal.timeout(60000),
    });
    if (!response.ok) {
      const body = await response.json();
      throw new ApiError(
        body.msg || "准备包下载未完成",
        body.code,
        body.traceId,
        body.timestamp,
      );
    }
    const blob = await response.blob();
    if (!blob.size) throw new Error("准备包内容为空，请检查发行包");
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = data.value.package.fileName.replace(/[\\/]/g, "_");
    anchor.click();
    URL.revokeObjectURL(url);
  } catch (cause) {
    report(cause);
  } finally {
    busy.value = false;
  }
}
async function check() {
  if (busy.value) return;
  busy.value = true;
  error.value = undefined;
  try {
    await request(`/hosts/${props.host.id}/checks`, "POST", {});
    emit("checked");
    await load();
  } catch (cause) {
    report(cause);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="host-preparation">
    <header>
      <div>
        <h3>准备安装环境</h3>
        <p>按当前主机生成固定执行器与受限授权指引。</p>
      </div>
      <button class="secondary" :disabled="busy" @click="load">刷新</button>
    </header>
    <div v-if="error" class="preparation-error" role="alert">
      <strong>{{ error.message }}</strong
      ><small>{{ error.code }} · {{ hostTime(error.at) }}</small
      ><code v-if="error.traceId">traceId={{ error.traceId }}</code>
    </div>
    <p v-if="!data && busy" class="muted">读取安装准备信息…</p>
    <template v-if="data">
      <div class="preparation-steps">
        <article v-for="step in data.steps" :key="step.key">
          <header>
            <strong>{{ step.label }}</strong
            ><span
              class="state-tag"
              :class="
                step.status === 'AVAILABLE'
                  ? 'state-success'
                  : step.status === 'UNAVAILABLE'
                    ? 'state-error'
                    : 'state-muted'
              "
              >{{
                step.status === "AVAILABLE"
                  ? "已就绪"
                  : step.status === "UNAVAILABLE"
                    ? "待处理"
                    : "待检查"
              }}</span
            >
          </header>
          <p>{{ step.message }}</p>
          <details>
            <summary>检查依据</summary>
            <code>{{ step.code }}</code
            ><small v-if="step.observedAt">{{
              hostTime(step.observedAt)
            }}</small>
          </details>
        </article>
      </div>
      <section class="preparation-package">
        <header>
          <strong>主机专用准备包</strong
          ><button
            class="primary"
            :disabled="busy || !data.package.available"
            @click="download"
          >
            下载准备包
          </button>
        </header>
        <p>{{ data.package.message }}</p>
        <code v-if="data.package.code">{{ data.package.code }}</code>
        <dl>
          <dt>执行账号</dt>
          <dd>{{ data.account }}</dd>
          <dt>项目目录</dt>
          <dd>
            <code>{{ data.installRoot }}</code>
          </dd>
          <template v-if="data.package.available"
            ><dt>包文件</dt>
            <dd>{{ data.package.fileName }}</dd>
            <dt>执行器大小</dt>
            <dd>{{ (data.package.byteSize / 1024 / 1024).toFixed(1) }} MiB</dd>
            <dt>执行器 SHA-256</dt>
            <dd>
              <code>{{ data.package.sha256 }}</code>
            </dd></template
          >
        </dl>
      </section>
      <section class="preparation-commands">
        <h3>在目标服务器执行</h3>
        <p>
          上传并解压准备包后，先预览，再由服务器管理员执行准备。完成后重新检查主机能力。
        </p>
        <div
          v-for="{ key, label } in commandEntries"
          :key="key"
          class="command-block"
        >
          <header>
            <strong>{{ label }}</strong
            ><button
              class="quiet"
              :disabled="!data.commands[key as keyof typeof data.commands]"
              @click="
                copy(
                  label,
                  data.commands[key as keyof typeof data.commands] || '',
                )
              "
            >
              {{ copied === label ? "已复制" : "复制命令" }}
            </button>
          </header>
          <pre>{{
            data.commands[key as keyof typeof data.commands] ||
            "当前无需执行此步骤"
          }}</pre>
        </div>
        <p class="authorization-scope">
          授权范围：{{
            data.authorization.scope
          }}。服务程序和数据按各自生命周期运行。
        </p>
        <details>
          <summary>授权与目标标识</summary>
          <code>{{ data.authorization.command }}</code
          ><code>controllerId={{ data.controllerId }}</code
          ><code>targetId={{ data.targetId }}</code>
        </details>
      </section>
      <button
        class="secondary check-preparation"
        :disabled="busy"
        @click="check"
      >
        {{ busy ? "检查中…" : "重新检查主机能力" }}
      </button>
    </template>
  </div>
</template>

<style scoped>
.host-preparation {
  display: grid;
  gap: 24px;
  padding-top: 12px;
  min-width: 0;
}
.host-preparation > header,
.preparation-package > header,
.command-block > header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 14px;
}
.host-preparation h3 {
  font-size: 14px;
  margin: 0;
}
.host-preparation p {
  font-size: 12px;
  line-height: 1.8;
  color: var(--muted);
  margin: 8px 0 0;
}
.host-preparation small {
  font-size: 11px;
  color: var(--muted);
}
.preparation-steps {
  display: grid;
  grid-template-columns: 1fr 1fr;
  border: 1px solid var(--border);
  border-radius: 6px;
  overflow: hidden;
}
.preparation-steps article {
  padding: 16px;
  min-width: 0;
}
.preparation-steps article:nth-child(odd) {
  border-right: 1px solid var(--border);
}
.preparation-steps article:nth-child(-n + 2) {
  border-bottom: 1px solid var(--border);
}
.preparation-steps article header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  font-size: 12px;
}
.host-preparation details {
  font-size: 11px;
  margin-top: 12px;
  color: var(--muted);
}
.host-preparation details summary {
  cursor: pointer;
}
.host-preparation code {
  font: 11px/1.8 var(--mono);
  overflow-wrap: anywhere;
  display: block;
}
.host-preparation details code {
  margin-top: 10px;
}
.preparation-package,
.preparation-commands {
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 18px;
  min-width: 0;
}
.preparation-package > header strong {
  font-size: 13px;
}
.preparation-package dl {
  display: grid;
  grid-template-columns: 75px minmax(0, 1fr);
  gap: 12px;
  font-size: 12px;
  margin: 18px 0 0;
}
.preparation-package dt {
  color: var(--muted);
}
.preparation-package dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.command-block {
  margin-top: 18px;
}
.command-block header strong {
  font-size: 12px;
  font-weight: 500;
}
.command-block pre {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  background: #f6f8fb;
  border: 1px solid var(--border);
  border-radius: 5px;
  padding: 12px;
  font: 11px/1.9 var(--mono);
  margin: 6px 0 0;
}
.preparation-error {
  display: grid;
  gap: 9px;
  padding: 14px;
  background: #fff5f3;
  border: 1px solid #efd2c9;
  border-radius: 6px;
  font-size: 12px;
}
.check-preparation {
  justify-self: start;
}
.authorization-scope {
  padding-top: 8px;
}
@media (max-width: 550px) {
  .preparation-steps {
    grid-template-columns: 1fr;
  }
  .preparation-steps article {
    border-right: 0 !important;
    border-bottom: 1px solid var(--border);
  }
  .preparation-steps article:last-child {
    border-bottom: 0;
  }
  .preparation-package {
    padding: 14px;
  }
}
</style>
