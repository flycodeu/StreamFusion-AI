<script setup lang="ts">
import { ElButton, ElEmpty, ElOption, ElSelect, ElTable, ElTableColumn, ElTag } from "element-plus";
import OpsPagination from "./OpsPagination.vue";
import { isProjectJob } from "./jobs";

export interface WorkspaceJobItem {
  id: string;
  kind: string;
  subjectName?: string;
  status: string;
  phase: string;
  errorCode: string;
  targetAckPending: boolean;
  traceId: string;
  createdAt: string;
  updatedAt: string;
  result: Record<string, unknown>;
}

defineProps<{
  busy: boolean;
  jobs: WorkspaceJobItem[];
  jobStatusFilter: string;
  jobKindFilter: string;
  statusLabels: Record<string, string>;
  jobKindLabels: Record<string, string>;
  phaseLabels: Record<string, string>;
  jobPage: number;
  jobPageSize: number;
  jobNextCursor: string;
  jobsLoading: boolean;
  jobStatus: (job: WorkspaceJobItem) => string;
  time: (value: string) => string;
  short: (value: string) => string;
}>();

const emit = defineEmits<{
  "update:jobStatusFilter": [value: string];
  "update:jobKindFilter": [value: string];
  refresh: [];
  openDetail: [job: WorkspaceJobItem];
  cancelJob: [job: WorkspaceJobItem];
  reconcile: [job: WorkspaceJobItem, recover?: boolean];
  changePage: [page: number];
  changeSize: [size: number];
}>();
</script>

<template>
  <div class="ops-el-toolbar" style="margin-bottom: 14px">
    <h2>作业记录</h2>
    <div class="ops-el-toolbar-group">
      <ElSelect
        :model-value="jobStatusFilter"
        clearable
        :value-on-clear="''"
        placeholder="全部状态"
        style="width: 140px"
        @update:model-value="emit('update:jobStatusFilter', $event ?? '')"
      >
        <ElOption label="全部状态" value="" />
        <ElOption
          v-for="(label, code) in statusLabels"
          :key="code"
          :label="label"
          :value="code"
        />
      </ElSelect>
      <ElSelect
        :model-value="jobKindFilter"
        clearable
        :value-on-clear="''"
        placeholder="全部类型"
        style="width: 160px"
        @update:model-value="emit('update:jobKindFilter', $event ?? '')"
      >
        <ElOption label="全部类型" value="" />
        <ElOption
          v-for="(label, code) in jobKindLabels"
          :key="code"
          :label="label"
          :value="code"
        />
      </ElSelect>
      <ElButton :disabled="busy" :loading="jobsLoading" @click="emit('refresh')">
        刷新
      </ElButton>
    </div>
  </div>
  <ElTable :data="jobs" border stripe style="width: 100%">
    <ElTableColumn label="时间 / 作业" min-width="170">
      <template #default="{ row }">
        <div>{{ time(row.createdAt) }}</div>
        <small class="muted">{{ short(row.id) }}</small>
      </template>
    </ElTableColumn>
    <ElTableColumn label="类型" min-width="150" show-overflow-tooltip>
      <template #default="{ row }">
        {{ row.kind === "HOST_PREPARE" ? row.subjectName || "主机接入" : jobKindLabels[row.kind] || "其他操作" }}
      </template>
    </ElTableColumn>
    <ElTableColumn label="阶段" min-width="140">
      <template #default="{ row }">
        <div>{{ phaseLabels[row.phase] || "阶段待确认" }}</div>
        <small v-if="row.targetAckPending" class="muted">目标确认收尾中</small>
      </template>
    </ElTableColumn>
    <ElTableColumn label="状态" width="110" align="center">
      <template #default="{ row }">
        <ElTag
          size="small"
          :type="row.status === 'SUCCEEDED' ? 'success' : jobStatus(row as WorkspaceJobItem) === 'FAILED' ? 'danger' : 'info'"
        >
          {{ statusLabels[jobStatus(row as WorkspaceJobItem)] || "状态待确认" }}
        </ElTag>
      </template>
    </ElTableColumn>
    <ElTableColumn label="操作" width="210" fixed="right">
      <template #default="{ row }">
        <div class="ops-el-toolbar-group" style="gap: 8px">
          <ElButton link type="primary" size="small" @click="emit('openDetail', row as WorkspaceJobItem)">
            详情
          </ElButton>
          <ElButton
            v-if="!isProjectJob(row.kind) && row.status === 'QUEUED' && row.phase !== 'UPLOADING' && !row.targetAckPending"
            link
            size="small"
            :disabled="busy"
            @click="emit('cancelJob', row as WorkspaceJobItem)"
          >
            取消排队
          </ElButton>
          <ElButton
            v-if="!isProjectJob(row.kind) && (row.status === 'UNKNOWN' || row.targetAckPending)"
            link
            type="primary"
            size="small"
            :disabled="busy"
            @click="emit('reconcile', row as WorkspaceJobItem)"
          >
            核对
          </ElButton>
          <ElButton
            v-if="row.status === 'UNKNOWN' && ['PUBLISH', 'ROLLBACK'].includes(row.kind)"
            link
            type="danger"
            size="small"
            :disabled="busy"
            @click="emit('reconcile', row as WorkspaceJobItem, true)"
          >
            恢复
          </ElButton>
        </div>
      </template>
    </ElTableColumn>
    <template #empty>
      <ElEmpty description="暂无作业记录" />
    </template>
  </ElTable>
  <OpsPagination
    :page="jobPage"
    :page-size="jobPageSize"
    :has-next="!!jobNextCursor"
    :disabled="jobsLoading"
    label="作业分页"
    @update:page="emit('changePage', $event)"
    @update:page-size="emit('changeSize', $event)"
  />
</template>
