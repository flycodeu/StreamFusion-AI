<script setup lang="ts">
import { computed, ref, watch } from "vue";
import type { ZLMConfig } from "./zlm";

const model = defineModel<ZLMConfig>({ required: true });
const props = defineProps<{
  credentials: { id: string; revision: string; name: string; kind: string }[];
  deployed: boolean;
}>();
const sourceText = ref(model.value.platformSourceIps.join(", "));
watch(sourceText, (value) => {
  model.value.platformSourceIps = value.split(",").map((x) => x.trim()).filter(Boolean);
});
const credential = computed({
  get: () =>
    model.value.secretCredentialId
      ? `${model.value.secretCredentialId}:${model.value.secretRevision}`
      : "",
  set: (value: string) => {
    const [id = "", revision = ""] = value.split(":");
    model.value.secretCredentialId = id;
    model.value.secretRevision = revision;
  },
});
</script>

<template>
  <div class="form-grid">
    <label>服务地址 IPv4<input v-model="model.mediaAddress" placeholder="10.75.216.216" :disabled="deployed" required /></label>
    <label>API 端口<input v-model.number="model.apiPort" type="number" min="1024" max="65535" :disabled="deployed" required /></label>
    <label>WebRTC 端口<input v-model.number="model.rtcPort" type="number" min="1024" max="65535" :disabled="deployed" required /></label>
    <label>API 密钥凭据<select v-model="credential" required>
      <option value="">请选择 TOKEN 凭据</option>
      <option v-for="item in props.credentials.filter((c) => c.kind === 'TOKEN')" :key="`${item.id}:${item.revision}`" :value="`${item.id}:${item.revision}`">{{ item.name }} r{{ item.revision }}</option>
    </select></label>
    <label>平台出口 IPv4<input v-model="sourceText" placeholder="平台实际出口 IP，多个用逗号分隔" required /></label>
  </div>
  <details>
    <summary>容量预算</summary>
    <div class="form-grid">
      <label>CPU 核数<input v-model.number="model.cpus" type="number" min="1" max="64" required /></label>
      <label>源上限<input v-model.number="model.maxSources" type="number" min="1" max="64" required /></label>
      <label>观看上限<input v-model.number="model.maxViewers" type="number" min="1" max="64" required /></label>
    </div>
    <small>平台须从实际出口地址访问。部署后通过平台连通检查核对来源；此配方不执行转码。</small>
  </details>
</template>
