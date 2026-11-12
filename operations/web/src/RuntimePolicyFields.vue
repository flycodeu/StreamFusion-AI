<script setup lang="ts">
import { computed } from "vue";
import { defaultRuntimePolicy, normalizeRuntimePolicy, type PolicyRuntime, type RuntimePolicy } from "./runtimePolicy";

const props = defineProps<{ modelValue?: RuntimePolicy; kind: PolicyRuntime; disabled?: boolean; optional?: boolean; inheritLabel?: string; stopTimeoutEditable?: boolean }>();
const emit = defineEmits<{ "update:modelValue": [RuntimePolicy | undefined] }>();
const policy = computed(() => props.modelValue || defaultRuntimePolicy(props.kind));
function change(values: Partial<RuntimePolicy>) {
  emit("update:modelValue", normalizeRuntimePolicy({ ...policy.value, ...values }, props.kind));
}
function number(event: Event) { return Number((event.target as HTMLInputElement).value); }
</script>

<template>
  <fieldset class="runtime-policy-fields" :disabled="disabled">
    <legend>运行策略</legend>
    <label v-if="optional" class="policy-mode">
      配置方式
      <select :value="modelValue ? 'CUSTOM' : ''" aria-label="运行策略配置方式" @change="emit('update:modelValue', ($event.target as HTMLSelectElement).value ? defaultRuntimePolicy(kind) : undefined)">
        <option value="">{{ inheritLabel || '保持当前配置' }}</option>
        <option value="CUSTOM">自定义策略</option>
      </select>
    </label>
    <template v-if="!optional || modelValue">
      <div class="policy-grid">
        <label v-if="kind === 'SYSTEMD'" class="policy-toggle"><input type="checkbox" :checked="policy.autoStart" aria-label="开机自动启动" @change="change({ autoStart: ($event.target as HTMLInputElement).checked })" />开机自动启动</label>
        <label>
          退出恢复
          <select :value="policy.restart" aria-label="退出恢复" @change="change({ restart: ($event.target as HTMLSelectElement).value as RuntimePolicy['restart'] })">
            <option value="no">不自动重启</option>
            <option value="on-failure">异常退出后重启</option>
            <option value="always">{{ kind === 'DOCKER' ? '退出后重启，随 Docker 启动' : '退出后自动重启' }}</option>
            <option v-if="kind === 'DOCKER'" value="unless-stopped">自动恢复，保留手动停止</option>
          </select>
        </label>
      </div>
      <p v-if="kind === 'DOCKER'" class="policy-hint">{{ policy.autoStart ? '随 Docker 自动启动' : 'Docker 重启后不自动启动' }}</p>
      <details class="policy-advanced">
        <summary>高级设置</summary>
        <div class="policy-grid">
          <label v-if="kind === 'SYSTEMD'">重启间隔（秒）<input :value="policy.restartDelaySeconds" type="number" min="0" max="300" step="any" placeholder="沿用当前值" aria-label="重启间隔（秒）" @change="change({ restartDelaySeconds: number($event) || undefined })" /></label>
          <label v-if="kind === 'DOCKER' && policy.restart === 'on-failure'">最多重试次数<input :value="policy.maxRetries || 0" type="number" min="0" max="100" aria-label="最多重试次数" @change="change({ maxRetries: number($event) })" /><small>0 表示不限制</small></label>
          <label>停止等待（秒）<input :value="policy.stopTimeoutSeconds" :disabled="stopTimeoutEditable === false" type="number" min="1" max="600" placeholder="沿用当前值" aria-label="停止等待（秒）" @change="change({ stopTimeoutSeconds: number($event) || undefined })" /></label>
        </div>
      </details>
    </template>
  </fieldset>
</template>

<style scoped>
.runtime-policy-fields{min-width:0;margin:0;padding:18px;border:1px solid var(--border);border-radius:8px;display:grid;gap:16px;}
legend{padding:0 6px;font-weight:600;}
.policy-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px;align-items:start;}
label{display:grid;gap:8px;font-size:13px;min-width:0;}
input,select{width:100%;min-width:0;box-sizing:border-box;}
.policy-toggle{display:flex;align-items:center;gap:8px;min-height:38px;align-self:end;}
.policy-toggle input{width:auto;}
.policy-hint,small{margin:0;color:var(--muted);font-size:12px;}
.policy-advanced summary{cursor:pointer;color:var(--muted);font-size:13px;}
.policy-advanced .policy-grid{margin-top:14px;}
@media(max-width:600px){.policy-grid{grid-template-columns:minmax(0,1fr);}}
</style>
