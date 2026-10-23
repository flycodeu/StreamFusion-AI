<script setup lang="ts">
import { ref, watch } from 'vue'
import { ElButton, ElMessage } from 'element-plus'
import JsonTree from './JsonTree.vue'

const props = defineProps<{ value: unknown }>()
const expanded = ref(true),
  revision = ref(0)
function toggle() {
  expanded.value = !expanded.value
  revision.value++
}
watch(
  () => props.value,
  () => {
    expanded.value = true
    revision.value++
  },
)
async function copy() {
  try {
    await globalThis.navigator.clipboard.writeText(JSON.stringify(props.value, null, 2) ?? 'null')
    ElMessage.success('JSON 已复制')
  } catch {
    ElMessage.error('复制失败，请检查浏览器剪贴板权限')
  }
}
</script>

<template>
  <section class="json-inspector" aria-label="格式化 JSON">
    <header>
      <span>JSON</span>
      <div>
        <ElButton text size="small" @click="toggle">{{
          expanded ? '收起层级' : '展开层级'
        }}</ElButton
        ><ElButton text size="small" @click="copy">复制 JSON</ElButton>
      </div>
    </header>
    <div class="json-body" tabindex="0">
      <JsonTree :key="revision" :value="value" :expanded="expanded" />
    </div>
  </section>
</template>

<style scoped>
.json-inspector {
  border: 1px solid #e2e8f0;
  border-radius: 10px;
  overflow: hidden;
}
header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 14px;
  border-bottom: 1px solid #e2e8f0;
  background: #f8fafc;
}
header > span {
  color: #64748b;
  font:
    600 12px ui-monospace,
    Consolas,
    monospace;
  letter-spacing: 0.08em;
}
.json-body {
  max-height: 58vh;
  overflow: auto;
  padding: 18px 24px;
  background: #fff;
  font:
    13px ui-monospace,
    Consolas,
    monospace;
  tab-size: 2;
}
@media (max-width: 600px) {
  .json-body {
    padding: 12px;
    font-size: 12px;
  }
}
</style>
