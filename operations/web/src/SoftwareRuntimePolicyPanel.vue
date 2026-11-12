<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { ApiError, request } from "./api";
import { hostTime } from "./hosts";
import RuntimePolicyFields from "./RuntimePolicyFields.vue";
import { runtimePolicyError, runtimePolicySummary, runtimePolicyStopConfirmed, type RuntimePolicy } from "./runtimePolicy";
import type { SoftwareInstance, SoftwareMutationResult } from "./software";

const props = defineProps<{ detail: SoftwareInstance; active: boolean; disabled?: boolean }>();
const emit = defineEmits<{ observe: []; preparation: []; submitted: [SoftwareMutationResult]; closeState: [{ blocked: boolean; dirty: boolean }] }>();
const policy = ref<RuntimePolicy>(), baseline = ref(""), busy = ref(false), error = ref("");
const attempted = ref(false), accepted = ref(false);
let idempotencyKey = "";
let subjectId = "";
let intent: { action: string; runtimePolicy: RuntimePolicy; expectedVersion: string; idempotencyKey: string; confirmed: true } | undefined;
const state = computed(() => props.detail.observation?.runtimePolicy);
const kind = computed(() => props.detail.runtimeKind === "DOCKER" ? "DOCKER" : "SYSTEMD");
const heldConfirmed = computed(() => props.detail.freshness === "FRESH" && runtimePolicyStopConfirmed(state.value, props.detail.actualState === "STOPPED", kind.value));
const dirty = computed(() => !!policy.value && JSON.stringify(policy.value) !== baseline.value);
const observationConfirmed = computed(() => !props.detail.errorCode && (!props.detail.latestAttempt?.code || props.detail.latestAttempt.code === "READY"));
const requiresUpdate = computed(() => props.detail.managementMode === "MANAGED" && (!state.value && observationConfirmed.value && props.detail.freshness === "FRESH" && !!props.detail.observation?.observedAt || ["EXECUTOR_UPGRADE_REQUIRED", "EXECUTOR_SOFTWARE_UNSUPPORTED"].includes(props.detail.errorCode || "")));
const canEdit = computed(() => props.detail.managementMode === "MANAGED" && state.value?.editable && !!state.value.actual && props.detail.freshness === "FRESH" && observationConfirmed.value);
const policyReason = computed(() => state.value?.reason && /[\u4e00-\u9fff]/.test(state.value.reason) ? state.value.reason : state.value?.actual ? "当前服务不支持修改运行策略，请检查服务单元和安装权限" : "运行策略读取未完成，请检查服务配置与读取权限");
watch([() => props.detail.id, () => state.value], () => {
  if (subjectId === props.detail.id && dirty.value && !accepted.value) return;
  subjectId = props.detail.id;
  const current = state.value?.configured || state.value?.actual;
  policy.value = current && { ...current };
  baseline.value = JSON.stringify(policy.value);
  attempted.value = false; accepted.value = false; error.value = ""; idempotencyKey = ""; intent = undefined;
}, { immediate: true });
watch([busy, dirty], () => emit("closeState", { blocked: busy.value, dirty: dirty.value }), { immediate: true });
let readRequested = false;
watch([() => props.active, () => props.disabled], ([active, disabled]) => {
  if (!active) readRequested = false;
  else if (!disabled && !readRequested && (!state.value?.actual || props.detail.freshness !== "FRESH" || !observationConfirmed.value)) {
    readRequested = true;
    emit("observe");
  }
}, { immediate: true });
function changed(value?: RuntimePolicy) {
  if (attempted.value) return;
  policy.value = value; accepted.value = false; error.value = ""; idempotencyKey = "";
}
async function save() {
  if (!policy.value || !canEdit.value || props.disabled || busy.value || accepted.value) return;
  error.value = runtimePolicyError(policy.value, kind.value);
  if (error.value) return;
  idempotencyKey ||= crypto.randomUUID();
  intent ||= { action: "RUNTIME_POLICY", runtimePolicy: { ...policy.value }, expectedVersion: props.detail.version, idempotencyKey, confirmed: true };
  attempted.value = true; busy.value = true;
  try {
    const receipt = await request<SoftwareMutationResult>(`/software-instances/${props.detail.id}/actions`, "POST", intent);
    accepted.value = true;
    baseline.value = JSON.stringify(policy.value);
    emit("submitted", receipt);
  } catch (cause) {
    if (cause instanceof ApiError && cause.status && cause.status < 500) { attempted.value = false; idempotencyKey = ""; intent = undefined; }
    error.value = cause instanceof Error && /[\u4e00-\u9fff]/.test(cause.message) ? cause.message : "提交结果尚未确认，请核对执行记录，或用同一请求重试";
  } finally { busy.value = false; }
}
</script>

<template>
  <section class="software-runtime-policy">
    <header><strong>运行策略</strong><button class="quiet" :disabled="disabled || busy" @click="emit('observe')">读取当前策略</button></header>
    <template v-if="state?.actual">
      <p class="policy-state">{{ state.held ? heldConfirmed ? '已保持停止，下次启动恢复配置策略' : '停止结果待核对' : runtimePolicySummary(state.actual, kind) }}</p>
      <small v-if="state.observedAt" class="muted">核对于 {{ hostTime(state.observedAt) }}</small>
    </template>
    <p v-else class="muted">{{ requiresUpdate ? '当前接入组件尚未提供运行策略，请更新后重新读取' : '尚未读取运行策略' }}</p>
    <button v-if="detail.managementMode === 'MANAGED' && (!state?.actual || requiresUpdate)" class="secondary preparation-link" :disabled="disabled || busy" @click="emit('preparation')">{{ requiresUpdate ? '更新接入组件' : '主机安装准备' }}</button>
    <RuntimePolicyFields v-if="policy" :model-value="policy" :kind="kind" :stop-timeout-editable="kind !== 'DOCKER'" :disabled="disabled || busy || attempted || !canEdit" @update:model-value="changed" />
    <p v-if="detail.managementMode !== 'MANAGED'" class="muted">接管软件后可修改运行策略</p>
    <p v-else-if="state && !state.editable" class="muted">{{ policyReason }}</p>
    <p v-else-if="state?.actual && (detail.freshness !== 'FRESH' || !observationConfirmed)" class="muted">请先读取当前策略</p>
    <p v-if="error" class="policy-error" role="alert">{{ error }}</p>
    <footer v-if="canEdit"><span v-if="accepted" class="muted">已提交，进度可在执行记录查看</span><span class="spacer"></span><button class="primary" :disabled="disabled || busy || !dirty || accepted" @click="save">{{ busy ? '正在提交…' : attempted && !accepted ? '重试原请求' : '应用运行策略' }}</button></footer>
  </section>
</template>

<style scoped>
.software-runtime-policy{display:grid;gap:16px;margin:0 0 24px;}
header,footer{display:flex;align-items:center;gap:12px;}
header{justify-content:space-between;}
.policy-state{margin:0;font-size:14px;}
.muted{margin:0;color:var(--muted);font-size:12px;}
.policy-error{margin:0;color:var(--danger,#c2413b);font-size:13px;}
.spacer{flex:1;}
.preparation-link{justify-self:start;}
</style>
