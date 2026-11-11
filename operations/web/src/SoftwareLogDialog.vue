<script setup lang="ts">
import { ElDialog } from "element-plus";
import SoftwareLogViewer from "./SoftwareLogViewer.vue";
import type { LogSelection } from "./jobs";
defineProps<{ selection?: LogSelection }>();
const emit = defineEmits<{ close: [] }>();
</script>

<template>
  <ElDialog :model-value="!!selection" title="执行日志" class="software-log-dialog" width="min(1040px, 96vw)" align-center destroy-on-close append-to-body @close="emit('close')">
    <SoftwareLogViewer v-if="selection" :job-id="selection.jobId" :software-id="selection.softwareId" :title="selection.title" />
  </ElDialog>
</template>

<style>
.software-log-dialog { display: flex; flex-direction: column; height: min(760px, calc(100dvh - 32px)); }
.software-log-dialog .el-dialog__header { flex-shrink: 0; }
.software-log-dialog .el-dialog__body { display: flex; flex: 1; min-height: 0; overflow: hidden; }
.software-log-dialog .software-log-viewer { flex: 1; }
.software-log-dialog .execution-output { min-height: 0; max-height: none; }
</style>
