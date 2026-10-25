<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { ElDrawer } from "element-plus";
import { request } from "./api";
import type { Host } from "./hosts";
import {
  softwareTypes,
  type SoftwareInstance,
  type SoftwareRuntime,
  type SoftwareType,
  type SoftwareRegistrationDraft,
} from "./software";

const props = defineProps<{
  open: boolean;
  hosts: Host[];
  hostId: string;
  prefill?: SoftwareRegistrationDraft;
}>();
const emit = defineEmits<{
  close: [];
  saved: [SoftwareInstance];
  error: [unknown];
}>();
const busy = ref(false);
const emptyDraft = (): SoftwareRegistrationDraft => ({
  name: "",
  hostId: "",
  softwareType: "ZLM" as SoftwareType,
  runtimeKind: "SYSTEMD" as SoftwareRuntime,
  locator: "",
  softwareVersion: "",
});
const draft = ref(emptyDraft());
const toolTypes: SoftwareType[] = ["JAVA", "PYTHON", "FFMPEG"];
const tools: Partial<Record<SoftwareType, string>> = {
  JAVA: "java",
  PYTHON: "python",
  FFMPEG: "ffmpeg",
};
const availableTypes = computed(() =>
  Object.entries(softwareTypes).filter(
    ([key]) =>
      draft.value.runtimeKind !== "TOOL" ||
      toolTypes.includes(key as SoftwareType),
  ),
);
const selectedHost = computed(() =>
  props.hosts.find((host) => host.id === draft.value.hostId),
);
const valid = computed(() => {
  const item = draft.value;
  return (
    !!item.name.trim() &&
    !!selectedHost.value &&
    !selectedHost.value.maintenance &&
    !!item.locator.trim() &&
    (item.runtimeKind !== "DOCKER" ||
      /^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$/.test(item.locator.trim())) &&
    (item.runtimeKind !== "SYSTEMD" ||
      /^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,95}\.service$/.test(item.locator.trim()))
  );
});
watch(
  () => props.open,
  (open) => {
    if (open)
      draft.value = {
        ...emptyDraft(),
        hostId: props.hostId || props.hosts[0]?.id || "",
        ...props.prefill,
      };
  },
);
watch(
  () => draft.value.runtimeKind,
  (kind, previous) => {
    if (
      props.prefill &&
      draft.value.locator === props.prefill.locator &&
      kind === props.prefill.runtimeKind &&
      previous !== kind
    )
      return;
    draft.value.locator = "";
    if (kind === "TOOL") {
      if (!toolTypes.includes(draft.value.softwareType))
        draft.value.softwareType = "JAVA";
      draft.value.locator = tools[draft.value.softwareType] || "java";
    }
  },
);
watch(
  () => draft.value.softwareType,
  (type) => {
    if (draft.value.runtimeKind === "TOOL")
      draft.value.locator = tools[type] || "java";
  },
);
async function save() {
  if (busy.value || !valid.value) return;
  busy.value = true;
  try {
    const item = await request<SoftwareInstance>(
      "/software-instances",
      "POST",
      {
        ...draft.value,
        name: draft.value.name.trim(),
        locator: draft.value.locator.trim(),
        softwareVersion: draft.value.softwareVersion.trim(),
      },
    );
    emit("saved", item);
    emit("close");
  } catch (error) {
    emit("error", error);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <ElDrawer
    :model-value="open"
    title="登记已有软件"
    size="min(600px,100vw)"
    destroy-on-close
    :close-on-click-modal="!busy"
    :close-on-press-escape="!busy"
    :show-close="!busy"
    @close="emit('close')"
  >
    <form
      id="software-registration-form"
      class="software-registration"
      @submit.prevent="save"
    >
      <label
        >软件名称<input
          v-model="draft.name"
          aria-label="软件名称"
          maxlength="100"
          required
          placeholder="例如：视频服务 A"
          :disabled="busy"
      /></label>
      <label
        >所在主机<select
          v-model="draft.hostId"
          aria-label="所在主机"
          required
          :disabled="busy"
        >
          <option value="" disabled>选择主机</option>
          <option
            v-for="host in hosts"
            :key="host.id"
            :value="host.id"
            :disabled="host.maintenance"
          >
            {{ host.name }} · {{ host.kind === "LOCAL" ? "本机" : host.address
            }}{{ host.maintenance ? "（维护中）" : "" }}
          </option>
        </select></label
      >
      <div class="software-form-row">
        <label
          >运行方式<select
            v-model="draft.runtimeKind"
            aria-label="运行方式"
            :disabled="busy"
          >
            <option value="SYSTEMD">systemd 服务</option>
            <option value="DOCKER">Docker 容器</option>
            <option value="TOOL">已有工具</option>
          </select></label
        >
        <label
          >软件类型<select
            v-model="draft.softwareType"
            aria-label="软件类型"
            :disabled="busy"
          >
            <option
              v-for="[key, label] in availableTypes"
              :key="key"
              :value="key"
            >
              {{ label }}
            </option>
          </select></label
        >
      </div>
      <label v-if="draft.runtimeKind === 'SYSTEMD'"
        >systemd unit<input
          v-model="draft.locator"
          aria-label="systemd unit"
          required
          placeholder="例如：zlm.service"
          :disabled="busy"
          spellcheck="false"
      /></label>
      <label v-else-if="draft.runtimeKind === 'DOCKER'"
        >容器名称 / ID<input
          v-model="draft.locator"
          aria-label="容器名称 / ID"
          required
          maxlength="128"
          placeholder="例如：zlm-server"
          :disabled="busy"
          spellcheck="false"
      /></label>
      <div v-else class="tool-command">
        <span>固定工具</span><code>{{ draft.locator }}</code>
      </div>
      <details class="software-advanced">
        <summary>补充信息</summary>
        <label
          >标记版本<input
            v-model="draft.softwareVersion"
            aria-label="标记版本"
            maxlength="100"
            placeholder="可选，实际版本由检查结果确认"
            :disabled="busy"
        /></label>
      </details>
    </form>
    <template #footer>
      <footer>
        <button
          type="button"
          class="secondary"
          :disabled="busy"
          @click="emit('close')"
        >
          取消</button
        ><button
          class="primary"
          form="software-registration-form"
          :disabled="busy || !valid"
        >
          {{ busy ? "登记中…" : "只读登记" }}
        </button>
      </footer>
    </template>
  </ElDrawer>
</template>

<style scoped>
.software-registration {
  display: grid;
  gap: 20px;
}
.software-form-row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}
.software-form-row > label {
  min-width: 0;
}
.software-advanced {
  padding-top: 2px;
}
.software-advanced summary {
  font-size: 13px;
  color: var(--muted);
  cursor: pointer;
}
.software-advanced label {
  display: block;
  margin-top: 16px;
}
.tool-command {
  display: grid;
  gap: 10px;
  font-size: 13px;
  color: var(--muted);
}
.tool-command code {
  color: var(--text);
  font: 12px var(--mono);
}
.registration-hint {
  margin: 0;
  font-size: 12px;
  color: var(--muted);
  line-height: 1.7;
}
footer {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: 10px;
  padding-top: 0;
}
footer .primary {
  width: auto;
  margin: 0;
}
@media (max-width: 480px) {
  .software-form-row {
    grid-template-columns: 1fr;
  }
}
</style>
