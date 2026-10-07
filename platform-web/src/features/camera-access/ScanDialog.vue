<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  ElAlert,
  ElButton,
  ElCheckbox,
  ElDialog,
  ElForm,
  ElFormItem,
  ElInput,
  ElOption,
  ElSelect,
  ElTable,
  ElTableColumn,
} from 'element-plus'
import * as api from '../../api/camera-access/api'
import { createCamera } from '../../api/camera/api'
import type { AccessJob, AccessOptions, ScanInput } from '../../api/camera-access/types'
import type { ManualCameraCreate } from '../../api/camera/types'
import { usePageScope } from '../../composables/usePageScope'
import { sessionState } from '../../session/state'
import { accessWarningLabel } from './form'
import RequestError from '../../components/feedback/RequestError.vue'
import GroupSelect from '../camera-groups/GroupSelect.vue'
import DeviceConnectionFields from '../camera/DeviceConnectionFields.vue'
import PlacementContent from '../camera/PlacementContent.vue'
import { newManualCamera, manualCameraError, manualCameraInput } from '../camera/manual'
import { formError, isRequestRejected, useCreateRequest } from '../camera/form'

interface ScanRow {
  candidateId: string
  host: string
  openPorts: number[]
  port: number
  name: string
  selected: boolean
  cameraId: string | null
  assigned: boolean
  pending: (Omit<ManualCameraCreate, 'clientRequestId'> & { clientRequestId: string }) | null
  error: unknown
}
const emit = defineEmits<{ close: []; saved: [cameraIds: string[]] }>()
const props = withDefaults(defineProps<{ storageReady?: boolean }>(), { storageReady: true })
const captureScope = usePageScope(),
  options = ref<AccessOptions | null>(null),
  job = ref<AccessJob | null>(null),
  jobId = ref<string | null>(null)
const error = ref<unknown>(null),
  busy = ref(false),
  saving = ref(false),
  rows = ref<ScanRow[]>([]),
  groupId = ref<string | null>(null),
  placementId = ref<string | null>(null)
const rangeMode = ref('cidr'),
  cidr = ref(''),
  startAddress = ref(''),
  endAddress = ref(''),
  ports = ref('80,443'),
  networkPolicyKey = ref(''),
  form = ref(newManualCamera())
const creation = useCreateRequest<Omit<ScanInput, 'clientRequestId'>>()
const running = computed(
  () => !!jobId.value && (!job.value || ['QUEUED', 'RUNNING'].includes(job.value.status)),
)
const frozen = computed(() => !!creation.pending.value)
const savedIds = computed(() => rows.value.flatMap((row) => (row.cameraId ? [row.cameraId] : [])))
const paused = ref(false)
let timer: ReturnType<typeof globalThis.setTimeout> | undefined,
  sequence = 0,
  reads = 0,
  ownerEpoch = sessionState.epoch
function stop() {
  if (timer) globalThis.clearTimeout(timer)
  timer = undefined
}
function receive(value: AccessJob) {
  job.value = value
  if (!['QUEUED', 'RUNNING'].includes(value.status))
    rows.value = value.hosts.map(
      (host) =>
        rows.value.find((row) => row.candidateId === host.candidateId) ?? {
          ...host,
          port: host.openPorts[0]!,
          name: '',
          selected: false,
          cameraId: null,
          assigned: false,
          pending: null,
          error: null,
        },
    )
}
async function loadOptions() {
  const active = captureScope()
  try {
    const value = await api.getAccessOptions()
    if (active()) {
      options.value = value
      networkPolicyKey.value = value.networkPolicies[0]?.key ?? ''
    }
  } catch (cause) {
    if (active()) error.value = cause
  }
}
async function refresh() {
  if (!jobId.value) return
  stop()
  const active = captureScope(),
    current = sequence
  try {
    const value = await api.getAccessJob(jobId.value)
    if (!active() || current !== sequence) return
    receive(value)
    if (['QUEUED', 'RUNNING'].includes(value.status)) {
      if (++reads < 90)
        timer = globalThis.setTimeout(() => {
          if (active() && current === sequence) void refresh()
        }, 1500)
      else paused.value = true
    }
  } catch (cause) {
    if (active() && current === sequence) {
      error.value = cause
      paused.value = true
    }
  }
}
async function start() {
  if (busy.value || running.value || savedIds.value.length) return
  error.value = null
  const values = [...new Set(ports.value.split(',').map((value) => Number(value.trim())))]
  if (
    !frozen.value &&
    (!networkPolicyKey.value ||
      values.length < 1 ||
      values.length > 4 ||
      values.some((value) => !Number.isInteger(value) || value < 1 || value > 65535) ||
      (rangeMode.value === 'cidr'
        ? !cidr.value.trim()
        : !startAddress.value.trim() || !endAddress.value.trim()))
  ) {
    error.value = formError('请填写批准网络、IPv4 范围和 1～4 个有效端口；最多搜索 128 个地址。')
    return
  }
  const active = captureScope()
  busy.value = true
  try {
    const created = await api.createScanJob(
      creation.capture({
        networkPolicyKey: networkPolicyKey.value,
        ports: values,
        ...(rangeMode.value === 'cidr'
          ? { cidr: cidr.value.trim() }
          : { startAddress: startAddress.value.trim(), endAddress: endAddress.value.trim() }),
      }),
    )
    if (!active()) return
    sequence++
    ownerEpoch = sessionState.epoch
    paused.value = false
    rows.value = []
    job.value = null
    jobId.value = created.jobId
    reads = 0
    creation.reset()
    await refresh()
  } catch (cause) {
    if (active()) {
      error.value = cause
      if (isRequestRejected(cause)) creation.reset()
    }
  } finally {
    if (active()) busy.value = false
  }
}
async function cancel() {
  if (!jobId.value || busy.value) return
  const active = captureScope()
  busy.value = true
  stop()
  try {
    const value = await api.cancelAccessJob(jobId.value)
    if (active()) {
      sequence++
      receive(value)
    }
  } catch (cause) {
    if (active()) error.value = cause
  } finally {
    if (active()) busy.value = false
  }
}
async function saveSelected() {
  if (saving.value || running.value) return
  const selected = rows.value.filter((row) => row.selected && !row.cameraId)
  if (!selected.length) {
    error.value = formError('请勾选需要登记的目标。')
    return
  }
  if (!props.storageReady && selected.some((row) => !row.pending)) {
    error.value = formError(
      '保存连接资料所需的部署密钥尚未配置，请联系系统管理员。搜索结果尚未保存为相机档案。',
    )
    return
  }
  error.value = null
  const active = captureScope()
  saving.value = true
  try {
    for (const row of selected) {
      if (!active()) return
      row.error = null
      const draft = { ...form.value, name: row.name, host: row.host, port: row.port }
      const validation = row.pending ? '' : manualCameraError(draft)
      if (validation) {
        row.error = formError(validation)
        continue
      }
      if (!row.pending)
        row.pending = {
          ...manualCameraInput(draft),
          clientRequestId: `${Date.now()}-${crypto.randomUUID()}`,
        }
      try {
        const result = await createCamera(row.pending)
        if (active()) {
          row.cameraId = result.cameraId
          row.pending = null
        }
      } catch (cause) {
        if (active()) {
          row.error = cause
          if (isRequestRejected(cause)) row.pending = null
        }
      }
    }
    if (active() && groupId.value)
      placementId.value = rows.value.find((row) => row.cameraId && !row.assigned)?.cameraId ?? null
  } finally {
    if (active()) saving.value = false
  }
}
function placed() {
  const current = rows.value.find((row) => row.cameraId === placementId.value)
  if (current) current.assigned = true
  placementId.value = rows.value.find((row) => row.cameraId && !row.assigned)?.cameraId ?? null
}
function close() {
  if (savedIds.value.length) emit('saved', savedIds.value)
  else emit('close')
}
onMounted(loadOptions)
onBeforeUnmount(() => {
  sequence++
  stop()
  creation.reset()
  form.value.username = ''
  form.value.password = ''
  rows.value.forEach((row) => (row.pending = null))
  if (running.value && jobId.value && ownerEpoch === sessionState.epoch)
    void api.cancelAccessJob(jobId.value).catch(() => {
      /* Bounded server jobs also expire. */
    })
})
</script>
<template>
  <ElDialog
    :model-value="true"
    title="指定网段搜索"
    width="min(980px, calc(100vw - 32px))"
    :close-on-click-modal="false"
    :show-close="!busy && !saving && !running && !placementId"
    :close-on-press-escape="!running && !saving && !placementId"
    @update:model-value="close"
  >
    <PlacementContent
      v-if="placementId"
      :key="placementId"
      :camera-id="placementId"
      action="move"
      :initial-group-id="groupId || undefined"
      @close="placementId = null"
      @saved="placed"
    />
    <template v-else>
      <RequestError :error="error" />
      <ElAlert
        v-if="options && !options.networkPolicies.length"
        type="warning"
        :closable="false"
        title="未配置可搜索的网络，请联系管理员。"
      />
      <ElAlert
        v-if="frozen"
        type="warning"
        :closable="false"
        title="搜索请求结果未知，请重试原请求。"
      />
      <ElForm
        label-width="100px"
        :disabled="busy || running || frozen || saving || !!savedIds.length"
      >
        <ElFormItem label="接入网络"
          ><ElSelect v-model="networkPolicyKey"
            ><ElOption
              v-for="policy in options?.networkPolicies"
              :key="policy.key"
              :value="policy.key"
              :label="policy.name" /></ElSelect
        ></ElFormItem>
        <ElFormItem label="范围格式"
          ><ElSelect v-model="rangeMode"
            ><ElOption value="cidr" label="CIDR 网段" /><ElOption
              value="range"
              label="起止 IP" /></ElSelect
        ></ElFormItem>
        <ElFormItem v-if="rangeMode === 'cidr'" label="IPv4 网段"
          ><ElInput v-model="cidr" placeholder="例如 192.168.1.0/25，最多 128 个地址"
        /></ElFormItem>
        <template v-else
          ><ElFormItem label="起始 IP"><ElInput v-model="startAddress" /></ElFormItem
          ><ElFormItem label="结束 IP"><ElInput v-model="endAddress" /></ElFormItem
        ></template>
        <ElFormItem label="端口"
          ><ElInput v-model="ports" placeholder="例如 80,443，最多 4 个"
        /></ElFormItem>
      </ElForm>
      <p v-if="job">已检查 {{ job.scannedTargets }} / {{ job.totalTargets }} 个目标</p>
      <ElAlert
        v-if="job?.diagnostic"
        type="error"
        :closable="false"
        :title="job.diagnostic.actionHint"
        :description="job.diagnostic.reasonCode"
      />
      <ElAlert
        v-for="warning in job?.warnings"
        :key="warning"
        type="warning"
        :closable="false"
        :title="accessWarningLabel(warning)"
      />
      <p v-if="paused">自动查询已暂停。</p>
      <p v-if="job && !running && !rows.length">未发现开放端口，请检查地址范围和端口。</p>
      <template v-if="rows.length">
        <ElTable :data="rows" row-key="candidateId" border>
          <ElTableColumn label="选择" width="66"
            ><template #default="{ row }"
              ><ElCheckbox v-model="row.selected" :disabled="saving || !!row.cameraId" /></template
          ></ElTableColumn>
          <ElTableColumn label="设备地址" prop="host" min-width="145" />
          <ElTableColumn label="管理端口" width="125"
            ><template #default="{ row }"
              ><ElSelect v-model="row.port" :disabled="saving || !!row.cameraId || !!row.pending"
                ><ElOption
                  v-for="port in row.openPorts"
                  :key="port"
                  :value="port"
                  :label="String(port)" /></ElSelect></template
          ></ElTableColumn>
          <ElTableColumn label="相机名称" min-width="190"
            ><template #default="{ row }"
              ><ElInput
                v-model="row.name"
                maxlength="128"
                placeholder="确认是相机后填写名称"
                :disabled="saving || !!row.cameraId || !!row.pending" /></template
          ></ElTableColumn>
          <ElTableColumn label="登记结果" min-width="240"
            ><template #default="{ row }"
              ><span>{{
                row.cameraId
                  ? row.assigned
                    ? '已保存并归档'
                    : '已保存，待归档'
                  : row.pending
                    ? '结果未知，可重试原保存'
                    : '端口可达，未识别设备'
              }}</span
              ><RequestError :error="row.error" /><ElButton
                v-if="row.cameraId && !row.assigned"
                link
                type="primary"
                :disabled="saving"
                @click="placementId = row.cameraId"
                >归档</ElButton
              ></template
            ></ElTableColumn
          >
        </ElTable>
        <ElForm label-width="110px" :disabled="saving">
          <DeviceConnectionFields v-model="form" hide-host :adapters="options?.adapters" />
          <ElFormItem label="视频分组"
            ><GroupSelect v-model="groupId" clearable /><span>保存后确认分组。</span></ElFormItem
          >
        </ElForm>
      </template>
    </template>
    <template v-if="!placementId" #footer>
      <ElButton :disabled="busy || saving || running" @click="close">{{
        savedIds.length ? '完成' : '关闭'
      }}</ElButton>
      <ElButton v-if="running" :loading="busy" @click="cancel">取消搜索</ElButton>
      <ElButton v-if="paused && running" :disabled="busy" @click="refresh">刷新搜索状态</ElButton>
      <ElButton
        v-else-if="!savedIds.length"
        :loading="busy"
        :disabled="!options?.networkPolicies.length || saving"
        @click="start"
        >{{ frozen ? '重试原搜索' : job ? '重新搜索' : '开始搜索' }}</ElButton
      >
      <ElButton
        v-if="rows.length && !running"
        type="primary"
        :loading="saving"
        @click="saveSelected"
        >保存勾选的相机</ElButton
      >
    </template>
  </ElDialog>
</template>
