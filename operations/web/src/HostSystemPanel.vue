<script setup lang="ts">
import { onUnmounted, ref, watch } from "vue";
import { ElMessage } from "element-plus";
import { ApiError, request } from "./api";
import type { Host } from "./hosts";
import {
  metricSnapshot,
  type HostObservation,
  type HostMetricSnapshot,
} from "./hostMetrics";
import HostResourceDetails from "./HostResourceDetails.vue";

const props = defineProps<{ host: Host }>();
const emit = defineEmits<{ error: [unknown] }>();
const current = ref<HostMetricSnapshot>();
const readError = ref("");
const loading = ref(false),
  detecting = ref(false),
  monitoring = ref(false);
let generation = 0;
let timer: ReturnType<typeof setTimeout> | undefined;
let pending: AbortController | undefined;
let disposed = false;

async function load(revision = generation) {
  if (revision !== generation || disposed || loading.value) return;
  const hostId = props.host.id;
  loading.value = true;
  const controller = new AbortController();
  pending = controller;
  try {
    const observation = await request<HostObservation>(
      `/hosts/${hostId}/metrics`,
      "GET",
      undefined,
      controller.signal,
    );
    if (!disposed && revision === generation && observation.hostId === hostId) {
      current.value = metricSnapshot(observation);
      readError.value = "";
    }
  } catch (error) {
    if (!disposed && revision === generation && !controller.signal.aborted) {
      if (error instanceof ApiError && error.status === 401)
        emit("error", error);
      else readError.value = "暂时无法读取主机信息，请稍后重试";
    }
  } finally {
    if (revision === generation) {
      loading.value = false;
      pending = undefined;
    }
  }
}
function poll(revision: number) {
  timer = setTimeout(async () => {
    if (!document.hidden) await load(revision);
    if (!disposed && revision === generation) poll(revision);
  }, 5000);
}
watch(
  () => props.host.id,
  async () => {
    const revision = ++generation;
    if (timer) clearTimeout(timer);
    pending?.abort();
    current.value = undefined;
    readError.value = "";
    loading.value = detecting.value = monitoring.value = false;
    await load(revision);
    if (!disposed && revision === generation) poll(revision);
  },
  { immediate: true },
);

async function detect() {
  if (detecting.value) return;
  const hostId = props.host.id,
    revision = generation;
  detecting.value = true;
  try {
    await request("/observation-batches", "POST", {
      kind: "HOST",
      hostId,
      idempotencyKey: crypto.randomUUID(),
    });
    if (!disposed && revision === generation)
      ElMessage.success("已加入后台检测");
  } catch (error) {
    if (!disposed && revision === generation) emit("error", error);
  } finally {
    if (revision === generation) detecting.value = false;
  }
}
async function toggleMonitoring() {
  if (monitoring.value || !current.value) return;
  const host = props.host,
    revision = ++generation;
  pending?.abort();
  loading.value = false;
  if (timer) clearTimeout(timer);
  monitoring.value = true;
  try {
    const result = await request<{ enabled: boolean }>(
      `/hosts/${host.id}/monitoring`,
      "PATCH",
      {
        enabled: current.value.enabled === false,
        expectedVersion: host.version,
      },
    );
    if (!disposed && revision === generation && current.value) {
      current.value = { ...current.value, enabled: result.enabled };
      await load(revision);
    }
  } catch (error) {
    if (!disposed && revision === generation) emit("error", error);
  } finally {
    if (!disposed && revision === generation) {
      monitoring.value = false;
      poll(revision);
    }
  }
}
onUnmounted(() => {
  disposed = true;
  generation++;
  pending?.abort();
  if (timer) clearTimeout(timer);
});
</script>
<template>
  <HostResourceDetails
    :host="host"
    :metrics="current"
    :loading="loading"
    :detecting="detecting"
    :monitoring="monitoring"
    :read-error="readError"
    @detect="detect"
    @monitoring="toggleMonitoring"
  />
</template>
