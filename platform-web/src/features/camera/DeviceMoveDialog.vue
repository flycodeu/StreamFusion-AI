<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElButton, ElDialog, vLoading } from 'element-plus'
import {
  getGroupTree,
  getDevicePlacements,
  previewDeviceMove,
  moveDevice,
} from '../../api/camera-groups/api'
import type { CameraGroup } from '../../api/camera-groups/types'
import type { CameraDeviceGroup } from '../../api/camera/types'
import GroupTree from '../camera-groups/GroupTree.vue'
import ImpactSummary from '../camera-groups/ImpactSummary.vue'
import RequestError from '../../components/feedback/RequestError.vue'
import { usePageScope } from '../../composables/usePageScope'
import { formError } from './form'
import { groupTree } from '../camera-groups/tree'

const props = defineProps<{ device: CameraDeviceGroup }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const groups = ref<CameraGroup[]>([]),
  target = ref<string | null>(null),
  loading = ref(false),
  saving = ref(false),
  error = ref<unknown>(null)
const preview = ref<Awaited<ReturnType<typeof previewDeviceMove>> | null>(null)
const placements = ref<Awaited<ReturnType<typeof getDevicePlacements>>>([])
const ready = ref(false)
const currentPlacements = computed(() => preview.value?.placements ?? placements.value)
const targetPath = computed(() => {
  const find = (nodes: ReturnType<typeof groupTree>): string | null => {
    for (const node of nodes) {
      if (node.groupId === target.value) return node.path
      const child = find(node.children)
      if (child) return child
    }
    return null
  }
  return find(groupTree(groups.value))
})
const unchanged = computed(
  () => placements.value.length === 1 && placements.value[0]?.groupId === target.value,
)
const captureScope = usePageScope()
async function load() {
  const active = captureScope()
  loading.value = true
  ready.value = false
  error.value = null
  try {
    const [result, origins] = await Promise.all([
      getGroupTree(),
      getDevicePlacements(props.device.groupKey),
    ])
    if (active()) {
      groups.value = result
      placements.value = origins
      target.value = origins.length === 1 ? origins[0]!.groupId : null
      preview.value = null
      ready.value = true
    }
  } catch (cause) {
    if (active()) error.value = cause
  } finally {
    if (active()) loading.value = false
  }
}
async function save() {
  if (saving.value || loading.value || !ready.value || unchanged.value) return
  if (!target.value) {
    error.value = formError('请选择目标分组。')
    return
  }
  const active = captureScope()
  saving.value = true
  error.value = null
  try {
    if (!preview.value) {
      const result = await previewDeviceMove(props.device.groupKey, target.value)
      if (active()) preview.value = result
      return
    }
    await moveDevice(props.device.groupKey, target.value, preview.value.impact.confirmation)
    if (active()) emit('saved')
  } catch (cause) {
    if (active()) {
      error.value = cause
      preview.value = null
    }
  } finally {
    if (active()) saving.value = false
  }
}
onMounted(load)
</script>
<template>
  <ElDialog
    :model-value="true"
    title="移动分组"
    class="management-dialog"
    width="640px"
    :close-on-click-modal="!saving"
    :close-on-press-escape="!saving"
    :show-close="!saving"
    @close="emit('close')"
  >
    <div class="move-device-name">{{ device.name }}</div>
    <p class="move-note">整台相机的全部通道一起移动。</p>
    <RequestError :error="error" />
    <section v-if="ready" class="move-current">
      <h3>当前分组</h3>
      <dl class="move-origins">
        <div v-for="place in currentPlacements" :key="place.groupId ?? 'pending'">
          <dt>{{ place.groupPath }}</dt>
          <dd>{{ place.channelCount }} 个通道</dd>
        </div>
      </dl>
    </section>
    <div v-if="!preview" v-loading="loading" class="move-target">
      <h3>选择目标分组</h3>
      <GroupTree
        v-if="ready"
        v-model="target"
        :groups="groups"
        :show-all="false"
        :counts="false"
        selection
        :placements="placements"
        :disabled="saving || loading"
      />
      <p class="move-target-path">
        目标：{{ targetPath || '尚未选择' }}<span v-if="unchanged">（与当前分组相同）</span>
      </p>
    </div>
    <ImpactSummary v-else :impact="preview.impact" />
    <ElButton v-if="error" link :disabled="saving || loading" @click="load"
      >重新读取分组归属</ElButton
    >
    <template #footer>
      <ElButton v-if="preview" :disabled="saving" @click="preview = null">重新选择</ElButton>
      <ElButton :disabled="saving" @click="emit('close')">取消</ElButton>
      <ElButton
        type="primary"
        :loading="saving"
        :disabled="loading || !ready || !target || unchanged"
        @click="save"
        >{{ preview ? '确认移动全部通道' : '下一步：确认影响' }}</ElButton
      >
    </template>
  </ElDialog>
</template>
<style scoped>
.move-device-name {
  font-size: 16px;
  font-weight: 600;
  overflow-wrap: anywhere;
}
.move-note {
  color: var(--text-secondary);
}
.move-current {
  padding: 14px 16px;
  margin: 16px 0 20px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 6px;
  background: var(--el-fill-color-light);
}
.move-current h3,
.move-target h3 {
  font-size: 14px;
  margin: 0 0 12px;
}
.move-origins {
  margin: 0;
}
.move-target :deep(.el-tree) {
  max-height: 34vh;
  overflow: auto;
}
.move-target-path {
  line-height: 1.6;
  overflow-wrap: anywhere;
  color: var(--el-color-primary);
}
.move-origins > div {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  margin: 10px 0;
}
.move-origins dt {
  overflow-wrap: anywhere;
}
.move-origins dd {
  flex-shrink: 0;
  margin: 0;
}
</style>
