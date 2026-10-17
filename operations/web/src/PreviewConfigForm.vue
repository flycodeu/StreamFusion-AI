<script setup lang="ts">
import { computed } from "vue";
import type { PreviewConfig } from "./preview";
const props = defineProps<{ modelValue: PreviewConfig; deployed: boolean }>();
const emit = defineEmits<{ "update:modelValue": [PreviewConfig] }>();
function update(key: keyof PreviewConfig, value: unknown) {
  emit("update:modelValue", { ...props.modelValue, [key]: value });
}
const targets = computed({
  get: () => props.modelValue.allowedTargets.join("\n"),
  set: (value: string) =>
    update("allowedTargets", value.split(/\s+/).filter(Boolean)),
});
const fields: {
  key: keyof PreviewConfig;
  label: string;
  min: number;
  max: number;
}[] = [
  { key: "maxSources", label: "源流上限", min: 1, max: 64 },
  { key: "maxViewers", label: "观看上限", min: 1, max: 64 },
  { key: "maxTranscodes", label: "共享转码上限", min: 0, max: 64 },
  { key: "maxStarting", label: "同时启动上限", min: 1, max: 4 },
  { key: "ingressKbps", label: "入站预算 kbps", min: 1, max: 100000000 },
  { key: "egressKbps", label: "出口预算 kbps", min: 1, max: 100000000 },
  { key: "sourceUpperKbps", label: "每源码率上界 kbps", min: 1, max: 1000000 },
  {
    key: "outputUpperKbps",
    label: "每观看码率上界 kbps",
    min: 1,
    max: 1000000,
  },
];
</script>
<template>
  <div class="preview-config">
    <div class="form-grid">
      <label
        >节点编号<input
          :value="modelValue.nodeId"
          :disabled="deployed"
          required
          pattern="[A-Za-z0-9_-]{1,64}"
          @input="update('nodeId', ($event.target as HTMLInputElement).value)"
      /></label>
      <label
        >控制域名或 IP<input
          :value="modelValue.controlHost"
          :disabled="deployed"
          required
          @input="
            update('controlHost', ($event.target as HTMLInputElement).value)
          "
      /></label>
      <label
        >浏览器可达的媒体 IPv4<input
          :value="modelValue.mediaAddress"
          :disabled="deployed"
          required
          @input="
            update('mediaAddress', ($event.target as HTMLInputElement).value)
          "
      /></label>
      <label
        >CPU 核预算<input
          :value="modelValue.cpus"
          type="number"
          min="1"
          max="64"
          required
          @input="
            update('cpus', Number(($event.target as HTMLInputElement).value))
          "
      /></label>
    </div>
    <label
      >允许连接的摄像机端点<textarea
        v-model="targets"
        rows="3"
        required
        placeholder="192.168.10.20:554&#10;192.168.10.21:554"
      />
    </label>
    <small
      >每行一个 IP:RTSP 端口。控制端口固定 9443/TCP，媒体端口固定
      18000/TCP+UDP。</small
    >
    <details>
      <summary>容量预算 · 默认每节点 64 路</summary>
      <div class="form-grid">
        <label v-for="field in fields" :key="field.key"
          >{{ field.label }}
          <input
            :value="modelValue[field.key]"
            type="number"
            :min="field.min"
            :max="field.max"
            required
            @input="
              update(
                field.key,
                Number(($event.target as HTMLInputElement).value),
              )
            "
        /></label>
      </div>
      <small>预算修改随下一次排空发布生效；实际容量需在目标机器验证。</small>
    </details>
  </div>
</template>
<style scoped>
.preview-config {
  display: grid;
  gap: 16px;
}
.form-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
}
.preview-config textarea {
  box-sizing: border-box;
  width: 100%;
  font: inherit;
  padding: 9px;
  resize: vertical;
  border: 1px solid var(--line, #d8dee7);
  border-radius: 4px;
}
.preview-config details .form-grid {
  margin-top: 12px;
}
@media (max-width: 600px) {
  .form-grid {
    grid-template-columns: 1fr;
  }
}
</style>
