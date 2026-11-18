<script setup lang="ts">
import { ElDialog } from "element-plus";
import ErrorHelpLink from "./ErrorHelpLink.vue";
import { hostTime, type Host } from "./hosts";

export interface FirewallRule {
  id: string;
  action: "allow" | "deny";
  protocol: string;
  source: string;
  start: number;
  end: number;
}

export interface FirewallPlan {
  id: string;
  expiresAt: string;
  change: { operation: string; rule: FirewallRule };
  snapshot: { status: string };
}

defineProps<{
  dialog: boolean;
  operation: string;
  actions: Record<string, string>;
  states: Record<string, string>;
  busy: boolean;
  plan?: FirewallPlan;
  rule: FirewallRule;
  range: boolean;
  sourceMode: string;
  sourceText: string;
  host?: Host;
  submissionAttempted: boolean;
  expired: boolean;
  error: string;
  errorDetails?: { code: string; timestamp: string; traceId: string };
}>();

const emit = defineEmits<{
  "update:dialog": [value: boolean];
  "update:range": [value: boolean];
  "update:sourceMode": [value: string];
  "update:sourceText": [value: string];
  clearPlan: [];
  preflight: [];
  submit: [];
}>();
</script>

<template>
  <ElDialog
    :model-value="dialog"
    :title="actions[operation]"
    width="min(560px,94vw)"
    align-center
    :close-on-click-modal="!busy"
    :show-close="!busy"
    @update:model-value="emit('update:dialog', $event)"
  >
    <div class="port-form">
      <template v-if="!plan && operation === 'ADD'">
        <div class="port-form-row">
          <label
            >策略<select v-model="rule.action" aria-label="访问策略">
              <option value="allow">允许访问</option>
              <option value="deny">禁止新连接</option>
            </select></label
          >
          <label
            >协议<select v-model="rule.protocol" aria-label="端口协议">
              <option value="tcp">TCP</option>
              <option value="udp">UDP</option>
            </select></label
          >
        </div>
        <div class="port-form-row">
          <label
            >端口<input
              v-model.number="rule.start"
              type="number"
              min="1"
              max="65535"
              aria-label="端口"
          /></label>
          <label v-if="range"
            >结束端口<input
              v-model.number="rule.end"
              type="number"
              min="1"
              max="65535"
              aria-label="结束端口"
          /></label>
        </div>
        <label class="range-toggle"
          ><input
            :checked="range"
            type="checkbox"
            @change="emit('update:range', ($event.target as HTMLInputElement).checked)"
          />连续端口范围</label
        >
        <label
          >访问来源<select
            :value="sourceMode"
            aria-label="访问来源"
            @change="emit('update:sourceMode', ($event.target as HTMLSelectElement).value)"
          >
            <option value="any4">任意 IPv4 来源</option>
            <option value="any6">任意 IPv6 来源</option>
            <option value="custom">指定 IP / 网段</option>
          </select></label
        >
        <label v-if="sourceMode === 'custom'"
          >IP / CIDR<input
            :value="sourceText"
            aria-label="来源 IP 或网段"
            placeholder="例如 192.168.1.0/24"
            @input="emit('update:sourceText', ($event.target as HTMLInputElement).value)"
        /></label>
      </template>
      <template v-else-if="!plan">
        <p>
          {{
            operation === "DELETE"
              ? "撤销此规则后，是否可访问仍取决于其他规则和默认策略。"
              : operation === "ENABLE"
                ? "启用后按照已有规则过滤流量。先为检测到的管理来源放行实际 SSH 端口。"
                : "停用后，UFW 将不再过滤入站访问，现有规则保留。"
          }}
        </p>
        <p v-if="operation !== 'DELETE'" class="port-warning">
          提交后需在 120 秒内确认保持；失联时目标尝试恢复原状态。
        </p>
      </template>
      <template v-if="plan">
        <dl class="port-plan">
          <dt>主机</dt>
          <dd>{{ host?.name }}</dd>
          <dt>操作</dt>
          <dd>{{ actions[plan.change.operation] }}</dd>
          <template v-if="['ADD', 'DELETE'].includes(operation)">
            <dt>策略 / 端口</dt>
            <dd>
              {{ plan.change.rule.action === "allow" ? "允许" : "禁止" }} ·
              {{ plan.change.rule.protocol.toUpperCase() }}
              {{ plan.change.rule.start }}–{{ plan.change.rule.end }}
            </dd>
            <dt>来源</dt>
            <dd>
              <code>{{ plan.change.rule.source }}</code>
            </dd>
          </template>
          <dt>当前防火墙</dt>
          <dd>{{ states[plan.snapshot.status] }}</dd>
          <dt>计划有效期</dt>
          <dd>{{ hostTime(plan.expiresAt) }}</dd>
        </dl>
        <p v-if="submissionAttempted" class="port-warning">
          已尝试提交；响应未确认时，使用原标识重试并找回作业。
        </p>
      </template>
      <p v-if="error" role="alert" class="port-error">{{ error }}</p>
      <details v-if="errorDetails" class="port-error">
        <summary>诊断信息</summary>
        <ErrorHelpLink :code="errorDetails.code" />
        <code>{{ errorDetails.code }} · {{ hostTime(errorDetails.timestamp) }}</code>
        <code>{{ errorDetails.traceId }}</code>
      </details>
    </div>
    <template #footer>
      <div class="port-actions">
        <button class="secondary" :disabled="busy" @click="emit('update:dialog', false)">
          取消
        </button>
        <button
          v-if="plan"
          class="quiet"
          :disabled="busy || submissionAttempted"
          @click="emit('clearPlan')"
        >
          返回修改
        </button>
        <button
          v-if="!plan"
          class="primary"
          :disabled="busy"
          @click="emit('preflight')"
        >
          预检规则
        </button>
        <button
          v-else
          class="primary"
          :disabled="busy || (expired && !submissionAttempted)"
          @click="emit('submit')"
        >
          {{
            submissionAttempted
              ? "重试并找回作业"
              : expired
                ? "计划已过期"
                : "确认执行"
          }}
        </button>
      </div>
    </template>
  </ElDialog>
</template>
