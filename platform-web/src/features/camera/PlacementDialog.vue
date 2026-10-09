<script setup lang="ts">
import { ref } from 'vue'
import { ElDialog } from 'element-plus'
import PlacementContent from './PlacementContent.vue'

defineProps<{ cameraId: string; action: 'move' | 'enable' | 'disable'; initialGroupId?: string }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const saving = ref(false)
</script>
<template>
  <ElDialog
    append-to-body
    class="management-dialog"
    :model-value="true"
    :title="action === 'move' ? '调整相机分组' : action === 'enable' ? '启用相机' : '停用相机'"
    width="600px"
    :close-on-click-modal="!saving"
    :close-on-press-escape="!saving"
    :show-close="!saving"
    @update:model-value="emit('close')"
  >
    <PlacementContent
      :camera-id="cameraId"
      :action="action"
      :initial-group-id="initialGroupId"
      @busy="saving = $event"
      @close="emit('close')"
      @saved="emit('saved')"
    />
  </ElDialog>
</template>
