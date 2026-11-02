<script setup lang="ts">
import { onUnmounted, ref } from "vue";
import { request, upload } from "./api";
import {
  materialNextAction,
  type InstallationMaterial,
  type ServiceRecipe,
} from "./serviceInstallation";

const props = defineProps<{ recipe: ServiceRecipe; disabled: boolean }>();
const emit = defineEmits<{ ready: [InstallationMaterial]; busy: [boolean] }>();
const file = ref<File>();
const sha = ref("");
const busy = ref(false);
const attempted = ref(false);
const progress = ref("");
const error = ref("");
const reimport = ref(false);
let key = crypto.randomUUID();
let mode: "download" | "upload" | undefined;
let controller: AbortController | undefined;

function selectFile(event: Event) {
  file.value = (event.target as HTMLInputElement).files?.[0];
  key = crypto.randomUUID();
}
async function prepare(selectedMode: "download" | "upload") {
  if (busy.value || props.disabled) return;
  if (
    selectedMode === "upload" &&
    (!file.value ||
      file.value.size < 1 ||
      file.value.size > 2 * 1024 ** 3 ||
      !/^[a-f\d]{64}$/i.test(sha.value.trim()))
  ) {
    error.value = "请选择不超过 2 GiB 的归档并填写文件的 64 位 SHA-256";
    return;
  }
  if (attempted.value && mode !== selectedMode) return;
  mode = selectedMode;
  attempted.value = true;
  busy.value = true;
  emit("busy", true);
  error.value = "";
  controller = new AbortController();
  const signal = controller.signal;
  const recipe = props.recipe;
  try {
    progress.value =
      selectedMode === "download" ? "正在获取官方材料" : "正在登记归档";
    let material = await request<InstallationMaterial>(
      selectedMode === "download"
        ? "/installation-materials/official"
        : "/installation-materials",
      "POST",
      {
        recipeKey: recipe.key,
        recipeVersion: recipe.version,
        recipeRevision: recipe.revision,
        idempotencyKey: key,
        ...(selectedMode === "upload"
          ? {
              runtimeMode: "NATIVE",
              name: file.value!.name,
              sha256: sha.value.trim().toLowerCase(),
              byteSize: file.value!.size,
            }
          : {}),
      },
      signal,
    );
    const deadline = Date.now() + 900000;
    while (!material.available) {
      signal.throwIfAborted();
      if (Date.now() >= deadline)
        throw new Error("材料尚未确认，请重试核对同一记录");
      const next = materialNextAction(material);
      if (next === "REIMPORT") {
        reimport.value = true;
        throw new Error(
          "原材料文件已缺失，可明确重新导入为新记录；历史记录保留",
        );
      }
      if (selectedMode === "upload" && next === "UPLOAD") {
        progress.value = "正在上传归档";
        await upload(
          `/installation-materials/${material.id}/content`,
          file.value!,
          signal,
        );
        material = { ...material, status: "UPLOADED" };
      } else if (selectedMode === "upload" && next === "CONFIRM") {
        progress.value = "正在校验文件与固定配方";
        material = await request<InstallationMaterial>(
          `/installation-materials/${material.id}/confirm`,
          "POST",
          {},
          signal,
        );
        if (material.status === "UPLOADED")
          throw new Error("材料校验尚未确认，请重试核对原记录");
      } else if (
        next === "WAIT" ||
        material.status === "DOWNLOADING" ||
        (selectedMode === "download" && material.status === "UPLOADED")
      ) {
        progress.value = "等待原材料操作结果";
        await new Promise<void>((resolve, reject) => {
          const abort = () => {
            clearTimeout(timer);
            reject(new Error("材料操作已中断"));
          };
          const timer = setTimeout(() => {
            signal.removeEventListener("abort", abort);
            resolve();
          }, 1000);
          signal.addEventListener("abort", abort, { once: true });
        });
        const all = await request<InstallationMaterial[]>(
          "/installation-materials",
          "GET",
          undefined,
          signal,
        );
        const found = all.find((item) => item.id === material.id);
        if (!found) throw new Error("原材料记录不可读取，请刷新后核对");
        material = found;
      } else {
        throw new Error(
          `${material.errorCode || material.status}：材料未就绪，请核对原记录`,
        );
      }
    }
    progress.value = "材料已核验，后续安装可直接复用";
    emit("ready", material);
  } catch (e) {
    if (!signal.aborted)
      error.value = e instanceof Error ? e.message : "材料处理未完成";
  } finally {
    busy.value = false;
    emit("busy", false);
  }
}
function importAgain() {
  key = crypto.randomUUID();
  attempted.value = false;
  reimport.value = false;
}
onUnmounted(() => controller?.abort());
</script>

<template>
  <details class="material-import">
    <summary>补充离线材料</summary>
    <p>下载入库或上传归档，后续可直接复用。</p>
    <button
      type="button"
      class="secondary"
      :disabled="disabled || busy || (attempted && mode !== 'download')"
      @click="prepare('download')"
    >
      {{
        attempted && mode === "download"
          ? "核对原下载结果"
          : "由 Ops 获取官方材料"
      }}
    </button>
    <label
      >已有安装归档<input
        type="file"
        :disabled="disabled || busy || attempted"
        @change="selectFile"
    /></label>
    <label
      >文件 SHA-256<input
        v-model="sha"
        aria-label="Node 材料 SHA-256"
        :disabled="disabled || busy || attempted"
    /></label>
    <button
      type="button"
      class="secondary"
      :disabled="disabled || busy || (attempted && mode !== 'upload')"
      @click="prepare('upload')"
    >
      {{ attempted && mode === "upload" ? "核对原上传结果" : "上传并校验" }}
    </button>
    <button
      v-if="reimport"
      type="button"
      class="secondary"
      :disabled="busy"
      @click="importAgain"
    >
      重新导入为新材料
    </button>
    <p v-if="progress" role="status">{{ progress }}</p>
    <p v-if="error" role="alert" class="error">{{ error }}</p>
  </details>
</template>

<style scoped>
.material-import {
  font-size: 12px;
  line-height: 1.8;
}
.material-import > * {
  margin-top: 12px;
}
summary {
  cursor: pointer;
}
label {
  display: grid;
  gap: 6px;
}
input {
  min-width: 0;
  padding: 8px;
  border: 1px solid var(--border);
  border-radius: 4px;
}
p {
  color: var(--muted);
}
.error {
  color: var(--danger);
}
</style>
