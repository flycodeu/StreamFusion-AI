<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import {
  ElAlert,
  ElButton,
  ElDropdown,
  ElDropdownItem,
  ElDropdownMenu,
  ElInput,
  ElMessage,
  ElTag,
} from "element-plus";
import { request } from "./api";
import type { SoftwareInstance } from "./software";

interface MavenSettingsPreset {
  key: string;
  name: string;
  summary: string;
  content: string;
}

interface MavenSettingsResponse {
  path: string;
  scope: "DOCKER" | "NATIVE" | "OFFICIAL";
  exists: boolean;
  content: string;
  revision: string;
  hasSecret: boolean;
  presets?: MavenSettingsPreset[];
}

const props = defineProps<{
  instance: SoftwareInstance;
  active?: boolean;
  disabled?: boolean;
}>();

const emit = defineEmits<{
  closeState: [{ blocked: boolean; dirty: boolean }];
  saved: [];
}>();

const loading = ref(false);
const saving = ref(false);
const error = ref("");
const filePath = ref("");
const scope = ref<"DOCKER" | "NATIVE" | "OFFICIAL">("DOCKER");
const exists = ref(false);
const revision = ref("ABSENT");
const content = ref("");
const baseline = ref("");
const presets = ref<MavenSettingsPreset[]>([]);
let abort: AbortController | undefined;

const dirty = computed(() => !loading.value && content.value !== baseline.value);
const blocked = computed(() => loading.value || saving.value);

watch(
  [blocked, dirty],
  () => emit("closeState", { blocked: blocked.value, dirty: dirty.value }),
  { immediate: true },
);

const scopeLabel = computed(() => {
  if (scope.value === "NATIVE") return "原生独立安装";
  if (scope.value === "OFFICIAL") return "系统官方包";
  return "Docker 受管配置";
});

const scopeHint = computed(() => {
  if (scope.value === "NATIVE") {
    return "将写入该 Maven 安装目录下的 conf/settings.xml，对本机 mvn 命令立即生效。";
  }
  if (scope.value === "OFFICIAL") {
    return "将写入系统 /etc/maven/settings.xml，对本机官方 Maven 全局生效。";
  }
  return "将保存至该实例配置目录并同步托管文件，sf-docker mvn 与后续容器构建会自动只读挂载生效。";
});

async function load() {
  abort?.abort();
  abort = new AbortController();
  loading.value = true;
  error.value = "";
  try {
    const res = await request<MavenSettingsResponse>(
      `/software-instances/${props.instance.id}/maven-settings`,
      "GET",
      undefined,
      abort.signal,
    );
    filePath.value = res.path || "";
    scope.value = res.scope || "DOCKER";
    exists.value = !!res.exists;
    revision.value = res.revision || "ABSENT";
    presets.value = res.presets || [];
    const initialBody =
      res.exists && res.content
        ? res.content
        : res.presets?.[0]?.content || "";
    content.value = initialBody;
    baseline.value = res.exists ? initialBody : "";
  } catch (cause) {
    if (abort.signal.aborted) return;
    error.value =
      cause instanceof Error ? cause.message : "读取 Maven settings.xml 失败";
  } finally {
    loading.value = false;
  }
}

function applyPreset(key: string) {
  const preset = presets.value.find((item) => item.key === key);
  if (!preset) return;
  content.value = preset.content;
  ElMessage.info(`已填入「${preset.name}」模板，点击「保存并生效」写入服务器`);
}

async function save() {
  if (saving.value || props.disabled) return;
  saving.value = true;
  error.value = "";
  try {
    const res = await request<{
      path: string;
      scope: "DOCKER" | "NATIVE" | "OFFICIAL";
      exists: boolean;
      content: string;
      revision: string;
      message?: string;
    }>(
      `/software-instances/${props.instance.id}/maven-settings`,
      "PUT",
      {
        content: content.value,
        expectedRevision: revision.value || "ABSENT",
      },
    );
    filePath.value = res.path || filePath.value;
    exists.value = true;
    revision.value = res.revision;
    content.value = res.content;
    baseline.value = res.content;
    ElMessage.success(res.message || "Maven settings.xml 已保存");
    emit("saved");
  } catch (cause) {
    error.value =
      cause instanceof Error ? cause.message : "保存 Maven settings.xml 失败";
  } finally {
    saving.value = false;
  }
}

watch(
  () => [props.instance.id, props.active],
  ([id, active]) => {
    if (id && active !== false) void load();
  },
  { immediate: true },
);

onUnmounted(() => {
  abort?.abort();
});
</script>

<template>
  <div class="maven-settings-panel">
    <ElAlert
      v-if="error"
      type="error"
      :title="error"
      :closable="false"
      show-icon
    />
    <ElAlert
      v-else
      type="info"
      :title="scopeHint"
      :closable="false"
      show-icon
    />

    <div class="maven-meta-bar">
      <div class="maven-meta-left">
        <ElTag size="small" effect="plain">{{ scopeLabel }}</ElTag>
        <code v-if="filePath" class="maven-path">{{ filePath }}</code>
        <ElTag v-if="!exists && !loading" size="small" type="warning">
          服务器尚未创建（保存后自动生成）
        </ElTag>
        <ElTag v-else-if="revision && revision !== 'ABSENT'" size="small" type="info">
          版本 {{ revision.slice(0, 8) }}
        </ElTag>
      </div>
      <div class="maven-meta-actions">
        <ElDropdown
          v-if="presets.length"
          trigger="click"
          :disabled="blocked || disabled"
          @command="applyPreset"
        >
          <ElButton size="small" :disabled="blocked || disabled">
            套用镜像模板 ▾
          </ElButton>
          <template #dropdown>
            <ElDropdownMenu>
              <ElDropdownItem
                v-for="preset in presets"
                :key="preset.key"
                :command="preset.key"
              >
                {{ preset.name }}（{{ preset.summary }}）
              </ElDropdownItem>
            </ElDropdownMenu>
          </template>
        </ElDropdown>
        <ElButton size="small" :loading="loading" :disabled="saving" @click="load">
          刷新
        </ElButton>
        <ElButton
          type="primary"
          size="small"
          :loading="saving"
          :disabled="loading || disabled || !content.trim()"
          @click="save"
        >
          保存并生效
        </ElButton>
      </div>
    </div>

    <ElInput
      v-model="content"
      type="textarea"
      :rows="18"
      class="maven-xml-editor"
      placeholder="请输入 Maven settings.xml 内容"
      :disabled="loading || saving"
      spellcheck="false"
    />
  </div>
</template>

<style scoped>
.maven-settings-panel {
  display: grid;
  gap: 12px;
}
.maven-meta-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}
.maven-meta-left {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.maven-path {
  font: 12px/1.4 var(--mono);
  word-break: break-all;
  color: #334155;
}
.maven-meta-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
.maven-xml-editor :deep(textarea) {
  font-family: var(--mono);
  font-size: 12px;
  line-height: 1.55;
  white-space: pre;
}
</style>
