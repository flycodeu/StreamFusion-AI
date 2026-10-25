<script setup lang="ts">
import { computed } from "vue";
import {
  isTool,
  servicePortAddress,
  servicePortValue,
  visibleServicePorts,
  type ServicePort,
  type ServiceConfiguration,
  type ServiceRecipe,
} from "./serviceInstallation";

const props = defineProps<{
  recipe: ServiceRecipe;
  config: ServiceConfiguration;
  disabled?: boolean;
  storageEditable?: boolean;
  projectRoot?: string;
}>();
const ports = computed(() =>
  visibleServicePorts(props.recipe, props.config.runtimeMode),
);
const primaryLabel = computed(
  () =>
    props.recipe.ports?.find((port) => port.key === "main")?.label ||
    (props.recipe.key === "seaweedfs" ? "S3 访问端口" : "主访问端口"),
);
const primaryContainerPort = computed(
  () =>
    props.recipe.ports?.find((port) => port.key === "main")?.containerPort ||
    props.recipe.port,
);
const requiredPorts = computed(() =>
  ports.value.filter((port) => port.required),
);
type OptionalProtocol = { key: string; label: string; ports: ServicePort[] };
const optionalProtocols = computed<OptionalProtocol[]>(() => {
  const groups: OptionalProtocol[] = [];
  for (const port of ports.value.filter((port) => !port.required)) {
    const key =
      props.recipe.key === "zlm" && ["rtpTcp", "rtpUdp"].includes(port.key)
        ? "rtp"
        : props.recipe.key === "mediamtx" &&
            ["webrtc", "webrtcUdp"].includes(port.key)
          ? "webrtc"
          : port.key;
    const group = groups.find((item) => item.key === key);
    if (group) group.ports.push(port);
    else
      groups.push({
        key,
        label: key === "rtp" ? "RTP" : key === "webrtc" ? "WebRTC" : port.label,
        ports: [port],
      });
  }
  return groups;
});
function protocolEnabled(group: OptionalProtocol) {
  return group.ports.some(
    (port) => servicePortValue(props.recipe, props.config, port.key) !== 0,
  );
}
function toggleProtocol(group: OptionalProtocol, event: Event) {
  const enabled = (event.target as HTMLInputElement).checked;
  for (const port of group.ports)
    props.config.ports[port.key] = enabled ? port.defaultPort : 0;
}
function changePort(key: string, event: Event) {
  const input = (event.target as HTMLInputElement).value;
  const value = input === "" ? Number.NaN : Number(input);
  props.config.ports[key] = value;
  if (props.recipe.key === "zlm" && ["rtpTcp", "rtpUdp"].includes(key)) {
    props.config.ports.rtpTcp = value;
    props.config.ports.rtpUdp = value;
  }
}
function portInputValue(key: string) {
  const value = servicePortValue(props.recipe, props.config, key);
  return Number.isFinite(value) ? value : "";
}
</script>

<template>
  <div class="network-fields">
    <template v-if="!isTool(recipe)">
      <div class="form-row">
        <label
          >监听地址<input
            v-model="config.bindAddress"
            aria-label="宿主机监听地址"
            :disabled="disabled"
            spellcheck="false"
            placeholder="服务器内网 IPv4"
        /></label>
        <label
          >{{ primaryLabel
          }}<input
            v-model.number="config.hostPort"
            type="number"
            aria-label="宿主机访问端口"
            min="1"
            max="65535"
            :disabled="disabled"
        /></label>
      </div>
      <div class="port-preview">
        <span
          >{{ config.bindAddress || "服务器IP" }}:{{
            config.hostPort || "端口"
          }}</span
        >
        <template v-if="config.runtimeMode === 'DOCKER'"
          ><b aria-hidden="true">→</b
          ><span>容器 {{ primaryContainerPort }}/tcp</span></template
        >
        <span v-else>原生监听</span>
      </div>
      <p v-if="config.bindAddress.startsWith('127.')" class="install-note">
        当前仅允许服务器本机访问；跨机使用请填写内网地址。
      </p>
      <div v-if="requiredPorts.length" class="required-ports">
        <h3>必要组件端口</h3>
        <div class="form-row">
          <label v-for="port in requiredPorts" :key="port.key"
            >{{ port.label }} · {{ port.protocol.toUpperCase()
            }}<input
              :value="portInputValue(port.key)"
              type="number"
              :aria-label="port.label + '端口'"
              min="1"
              max="65535"
              :disabled="disabled"
              :placeholder="String(port.defaultPort)"
              @input="changePort(port.key, $event)"
            /><small
              >绑定 {{ servicePortAddress(recipe, config, port.key) }}</small
            ></label
          >
        </div>
        <p
          v-if="recipe.key === 'seaweedfs' && config.runtimeMode === 'NATIVE'"
          class="install-note grpc-notice"
        >
          原生 S3 同时监听内部 gRPC（不使用 S3
          密钥鉴权），请由管理员限制该端口的访问来源。
        </p>
      </div>
      <details v-if="optionalProtocols.length" class="additional-ports">
        <summary>可选协议（{{ optionalProtocols.length }}）</summary>
        <div class="optional-protocols">
          <div
            v-for="group in optionalProtocols"
            :key="group.key"
            class="protocol-option"
          >
            <label class="protocol-toggle"
              ><input
                type="checkbox"
                :checked="protocolEnabled(group)"
                :disabled="disabled"
                :aria-label="'启用 ' + group.label"
                @change="toggleProtocol(group, $event)"
              /><span>{{ group.label }}</span></label
            >
            <div v-if="protocolEnabled(group)" class="form-row protocol-ports">
              <label
                v-for="port in group.key === 'rtp'
                  ? group.ports.slice(0, 1)
                  : group.ports"
                :key="port.key"
                >{{ group.key === "rtp" ? "RTP 端口 · TCP / UDP" : port.label
                }}<input
                  :value="portInputValue(port.key)"
                  type="number"
                  :aria-label="port.label + '端口'"
                  min="1"
                  max="65535"
                  :disabled="disabled"
                  @input="changePort(port.key, $event)"
                /><small v-if="group.key !== 'rtp'"
                  >{{ port.protocol }} ·
                  {{ servicePortAddress(recipe, config, port.key) }}</small
                ></label
              >
            </div>
          </div>
        </div>
      </details>
    </template>
    <p v-else class="install-note">
      这是按需使用的程序工具，不监听端口，也无需保持后台进程。
    </p>
    <div class="storage-section">
      <h3>程序与存储</h3>
      <label v-if="storageEditable"
        >存储根目录 <small>可选</small
        ><input
          v-model="config.storageRoot"
          aria-label="存储根目录"
          :disabled="disabled"
          spellcheck="false"
          placeholder="默认使用主机项目目录"
          maxlength="240"
      /></label>
      <template v-else
        ><label
          >已登记存储根目录<code class="path-value">{{
            config.storageRoot || projectRoot || "使用主机项目目录"
          }}</code></label
        ></template
      >
    </div>
    <details v-if="!isTool(recipe)">
      <summary>资源设置</summary>
      <label
        >内存上限（MiB）<input
          v-model.number="config.memoryMiB"
          type="number"
          aria-label="内存上限（MiB）"
          min="128"
          max="16384"
          :disabled="disabled"
      /></label>
    </details>
  </div>
</template>

<style scoped src="./service-installation.css"></style>
<style scoped>
.network-fields,
.storage-section,
.required-ports {
  display: grid;
  gap: 18px;
  min-width: 0;
}
.storage-section {
  border-top: 1px solid var(--border);
  padding-top: 20px;
}
.storage-section h3,
.required-ports h3 {
  margin: 0;
  font-size: 13px;
}
.optional-protocols {
  display: grid;
  gap: 12px;
  margin-top: 16px;
}
.protocol-option {
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 12px;
}
.network-fields .protocol-toggle {
  display: flex;
  align-items: center;
  gap: 9px;
  color: var(--text);
  cursor: pointer;
}
.network-fields .protocol-toggle input {
  width: 15px;
  height: 15px;
  margin: 0;
  accent-color: var(--accent);
}
.protocol-ports {
  margin-top: 12px;
}
.grpc-notice {
  margin: 0;
}
.network-fields label {
  display: grid;
  gap: 8px;
  color: #53657a;
  font-size: 12px;
  min-width: 0;
}
.network-fields input {
  margin: 0;
  width: 100%;
  height: 38px;
  min-width: 0;
  padding: 8px 10px;
}
.network-fields small {
  color: var(--muted);
  font-size: 11px;
}
</style>
