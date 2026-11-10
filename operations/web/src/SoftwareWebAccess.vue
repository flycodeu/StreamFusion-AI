<script setup lang="ts">
import { computed, ref, watch } from "vue";
import type { Host } from "./hosts";
import type { SoftwareInstance } from "./software";
import { installationWebAccess, softwareWebAccess } from "./webAccess";

const props = withDefaults(defineProps<{ item: SoftwareInstance; host?: Host; compact?: boolean; active?: boolean }>(), { active: true });
const access = computed(() => softwareWebAccess(props.item, props.host));
const preview = ref(false);
const previewURL = ref("");
const configuredURL = computed(() => props.item.installation?.definition && props.item.installation.config
  ? installationWebAccess(props.item.installation.definition, props.item.installation.config, props.host)?.url : "");
const mixedContent = computed(() => typeof location !== "undefined" && location.protocol === "https:" && access.value?.url.startsWith("http:"));
function closePreview() { preview.value = false; previewURL.value = ""; }
function togglePreview() {
  if (preview.value) closePreview();
  else if (access.value?.url) { previewURL.value = access.value.url; preview.value = true; }
}
watch([() => props.active, () => props.item.id, configuredURL], closePreview);
</script>

<template>
  <span v-if="compact && access" class="compact-access">
    <a v-if="access.url" :href="access.url" target="_blank" rel="noopener noreferrer" class="quiet web-enter">进入系统 ↗</a>
    <button v-else type="button" class="quiet" disabled :title="access.reason">进入系统</button>
  </span>
  <section v-else-if="access" class="software-web-access" aria-label="系统访问入口">
    <div class="access-toolbar">
      <a v-if="access.url || previewURL" :href="access.url || previewURL" target="_blank" rel="noopener noreferrer" class="primary web-enter">进入系统 ↗</a>
      <button v-if="preview || access.url && access.embed && !mixedContent" type="button" class="secondary" @click="togglePreview">{{ preview ? '关闭预览' : '内嵌预览' }}</button>
      <code v-if="access.url || previewURL">{{ access.url || previewURL }}</code>
    </div>
    <p v-if="access.reason" class="access-reason">{{ access.reason }}</p>
    <p v-else-if="access.embed && mixedContent" class="access-reason">当前页面使用 HTTPS，请在新窗口打开此 HTTP 系统。</p>
    <iframe v-if="preview && active && previewURL" :src="previewURL" :title="item.name + '预览'" referrerpolicy="no-referrer" sandbox="allow-scripts allow-forms allow-same-origin allow-downloads allow-popups allow-modals" />
  </section>
</template>

<style scoped>
.compact-access { display: inline-flex; align-items: center; }
.web-enter { display: inline-flex; align-items: center; justify-content: center; text-decoration: none; white-space: nowrap; font-size: 12px; }
.web-enter.primary { width: auto; margin: 0; padding: 9px 14px; border-radius: var(--control-radius); color: white; }
.web-enter.quiet { color: var(--accent); }
.software-web-access { display: grid; gap: 16px; min-width: 0; }
.access-toolbar { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; }
.access-toolbar code { flex: 1 1 220px; overflow-wrap: anywhere; color: var(--muted); font: 12px/1.6 var(--mono); }
.access-reason { color: var(--muted); margin: 0; font-size: 13px; }
iframe { border: 1px solid var(--border); border-radius: 6px; width: 100%; height: min(700px, 65vh); min-height: 300px; background: white; }
</style>
