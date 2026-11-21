<script setup lang="ts">
import OpsPagination from "./OpsPagination.vue";

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
  <div class="section-head">
    <h2>作业记录</h2>
    <div class="job-filters">
      <select
        :value="jobStatusFilter"
        aria-label="筛选作业状态"
        @change="emit('update:jobStatusFilter', ($event.target as HTMLSelectElement).value)"
      >
        <option value="">全部状态</option>
        <option
          v-for="(label, code) in statusLabels"
          :key="code"
          :value="code"
        >
          {{ label }}
        </option>
      </select>
      <select
        :value="jobKindFilter"
        aria-label="筛选作业类型"
        @change="emit('update:jobKindFilter', ($event.target as HTMLSelectElement).value)"
      >
        <option value="">全部类型</option>
        <option
          v-for="(label, code) in jobKindLabels"
          :key="code"
          :value="code"
        >
          {{ label }}
        </option>
      </select>
      <button class="secondary" :disabled="busy" @click="emit('refresh')">
        刷新
      </button>
    </div>
  </div>
  <div v-if="!jobs.length" class="empty">暂无作业</div>
  <div v-else class="table-scroll">
    <table>
      <thead>
        <tr>
          <th>时间 / 作业</th>
          <th>类型</th>
          <th>阶段</th>
          <th>状态</th>
          <th>操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="j in jobs" :key="j.id">
          <td>
            {{ time(j.createdAt) }}<small>{{ short(j.id) }}</small>
          </td>
          <td>
            {{ j.kind === "HOST_PREPARE" ? j.subjectName || "主机接入" : jobKindLabels[j.kind] || "其他操作" }}
          </td>
          <td>
            {{ phaseLabels[j.phase] || "阶段待确认"
            }}<small v-if="j.targetAckPending">目标确认收尾中</small>
          </td>
          <td>
            <span
              class="state-tag"
              :class="
                j.status === 'SUCCEEDED'
                  ? 'state-success'
                  : jobStatus(j) === 'FAILED'
                    ? 'state-error'
                    : 'state-muted'
              "
              >{{ statusLabels[jobStatus(j)] || "状态待确认" }}</span
            >
          </td>
          <td>
            <button
              class="quiet"
              @click="emit('openDetail', j)"
            >
              详情</button
            ><button
              v-if="
                j.status === 'QUEUED' &&
                j.phase !== 'UPLOADING' &&
                !j.targetAckPending
              "
              class="quiet"
              :disabled="busy"
              @click="emit('cancelJob', j)"
            >
              取消排队</button
            ><button
              v-if="j.status === 'UNKNOWN' || j.targetAckPending"
              class="quiet"
              :disabled="busy"
              @click="emit('reconcile', j)"
            >
              核对</button
            ><button
              v-if="
                j.status === 'UNKNOWN' &&
                ['PUBLISH', 'ROLLBACK'].includes(j.kind)
              "
              class="quiet danger"
              :disabled="busy"
              @click="emit('reconcile', j, true)"
            >
              恢复
            </button>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
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
