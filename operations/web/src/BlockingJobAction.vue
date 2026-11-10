<script setup lang="ts">
import { ref, watch } from "vue";
import { ApiError, request } from "./api";
import { softwareJobPhaseLabel, softwareJobStatusLabel } from "./software";

export interface BlockingJob {
  id: string;
  kind: string;
  status: string;
  phase: string;
  targetAckPending: boolean;
}

const props = defineProps<{ job: BlockingJob; disabled?: boolean }>();
const busy = ref(false);
const feedback = ref("");
const checked = ref(false);
watch(() => props.job.id, () => { feedback.value = ""; checked.value = false; });

async function reconcile() {
  const id = props.job.id;
  busy.value = true;
  feedback.value = "";
  try {
    await request(`/jobs/${id}/reconcile`, "POST");
    const result = await request<BlockingJob>(`/jobs/${id}`);
    if (props.job.id !== id) return;
    checked.value = result.status !== "UNKNOWN" && !result.targetAckPending;
    feedback.value = checked.value ? "已核对原作业，可以重试当前操作。" : "原作业仍待确认。";
  } catch (error) {
    if (props.job.id === id)
      feedback.value = error instanceof ApiError ? error.message : "暂时无法核对原作业。";
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="blocking-job">
    <span>{{ job.kind === "SOFTWARE_INSTALL" ? "软件安装" : "主机作业" }} · {{ softwareJobStatusLabel(job.status) }} · {{ softwareJobPhaseLabel(job.phase) }}</span>
    <button v-if="!checked && (job.status === 'UNKNOWN' || job.targetAckPending)" type="button" class="secondary" :disabled="disabled || busy" @click="reconcile">
      {{ busy ? "核对中…" : "核对原作业" }}
    </button>
    <small v-if="feedback" role="status">{{ feedback }}</small>
  </div>
</template>

<style scoped>
.blocking-job { display: flex; align-items: center; flex-wrap: wrap; gap: 8px 12px; font-size: 13px; }
.blocking-job small { flex-basis: 100%; color: var(--muted, #64748b); }
</style>
