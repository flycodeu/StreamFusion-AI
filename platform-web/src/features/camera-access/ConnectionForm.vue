<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import {
  ElButton,
  ElCheckbox,
  ElForm,
  ElFormItem,
  ElInput,
  ElInputNumber,
  ElOption,
  ElSelect,
} from 'element-plus'
import type { AccessMethod, AccessOptions } from '../../api/camera-access/types'
import * as sourceApi from '../../api/camera-sources/api'
import SourceEditor from '../camera-sources/SourceEditor.vue'
import type { Source } from '../../api/camera-sources/types'
import { usePageScope } from '../../composables/usePageScope'
import RequestError from '../../components/feedback/RequestError.vue'
import { methodLabels } from './form'
import type { ConnectionDraft } from './form'

const props = defineProps<{ options: AccessOptions; disabled: boolean }>()
const emit = defineEmits<{ editing: [value: boolean] }>()
const model = defineModel<ConnectionDraft>({ required: true })
const editingSource = ref(false)
watch(editingSource, (value) => emit('editing', value))
const descriptor = computed(() =>
  props.options.adapters?.find((item) => item.type === model.value.method),
)
const platformMode = computed(() =>
  props.options.methods.some((method) =>
    props.options.adapters?.some(
      (adapter) => adapter.type === method && adapter.category === 'PLATFORM',
    ),
  ),
)
const platformPresets = computed(
  () =>
    props.options.platformPresets ??
    props.options.adapters
      .filter((adapter) => adapter.category === 'PLATFORM')
      .map((adapter) => ({
        type: adapter.type,
        label: adapter.label,
        apiFamily: '目录接口',
        available: true,
        status: '',
      })),
)
const selectedPreset = computed(() =>
  platformPresets.value.find((item) => item.type === model.value.method),
)
const unavailable = computed(() => platformMode.value && selectedPreset.value?.available === false)
const isRtsp = computed(
  () => descriptor.value?.inputKind === 'RTSP_URL' || model.value.method === 'RTSP',
)
const isPlatform = computed(
  () => descriptor.value?.inputKind === 'PLATFORM_APPKEY' || model.value.method === 'HIK_PLATFORM',
)
const label = (method: string) =>
  props.options.adapters?.find((item) => item.type === method)?.label ??
  methodLabels[method] ??
  method
const sources = ref<Source[]>([]),
  selectedSource = ref<Source | null>(null),
  searching = ref(false),
  loadingDetail = ref(false),
  error = ref<unknown>(null),
  sourceTotal = ref(0)
const captureScope = usePageScope()
const loading = computed(() => searching.value || loadingDetail.value)
let searchSequence = 0,
  detailSequence = 0
function mergeSelected(items: Source[]) {
  const selected = selectedSource.value
  if (!selected || selected.sourceId !== model.value.sourceId) return items
  return [selected, ...items.filter((item) => item.sourceId !== selected.sourceId)]
}
function changeMethod(method: AccessMethod) {
  if (method === model.value.method) return
  const retained =
    selectedSource.value?.connectionCategory === 'DEVICE' &&
    !selectedSource.value.adapterType &&
    (method === 'AUTO' ||
      props.options.adapters?.some((item) => item.type === method && item.category === 'DEVICE'))
      ? selectedSource.value
      : null
  model.value.method = method
  if (!retained) {
    model.value.sourceId = null
    model.value.sourceVersion = null
    selectedSource.value = null
  }
  model.value.username = ''
  model.value.password = ''
  model.value.rtspUrls = ['']
  sources.value = retained ? [retained] : []
  searchSequence++
  detailSequence++
  searching.value = false
  loadingDetail.value = false
  if (!retained && model.value.reuse) void searchSources()
}
async function searchSources(name = '') {
  if (unavailable.value) return
  const current = ++searchSequence,
    active = captureScope()
  searching.value = true
  error.value = null
  try {
    const result = await sourceApi.getSources({
      page: 1,
      size: 100,
      name: name.trim() || undefined,
      enabled: true,
      adapterType: model.value.method === 'AUTO' ? undefined : model.value.method,
    })
    if (!active() || current !== searchSequence) return
    sources.value = mergeSelected(
      result.items.filter(
        (source) =>
          ((source.adapterType &&
            props.options.methods.includes(source.adapterType as AccessMethod)) ||
            (model.value.method === 'AUTO' && source.connectionCategory === 'DEVICE')) &&
          (model.value.method !== 'AUTO' || source.adapterType !== 'RTSP'),
      ),
    )
    sourceTotal.value = result.total
  } catch (cause) {
    if (active() && current === searchSequence) error.value = cause
  } finally {
    if (active() && current === searchSequence) searching.value = false
  }
}
async function selectSource() {
  error.value = null
  const current = ++detailSequence,
    active = captureScope()
  model.value.sourceVersion = null
  selectedSource.value = null
  if (!model.value.sourceId) {
    loadingDetail.value = false
    return
  }
  loadingDetail.value = true
  try {
    const source = await sourceApi.getSource(model.value.sourceId)
    if (!active() || current !== detailSequence) return
    model.value.sourceVersion = source.version
    selectedSource.value = source
    sources.value = mergeSelected(sources.value)
    model.value.networkPolicyKey = source.networkPolicyKey ?? model.value.networkPolicyKey
  } catch (cause) {
    if (active() && current === detailSequence) error.value = cause
  } finally {
    if (active() && current === detailSequence) loadingDetail.value = false
  }
}
async function sourceSaved() {
  editingSource.value = false
  await selectSource()
}
onMounted(async () => {
  if (model.value.reuse) {
    await searchSources()
    await selectSource()
  }
})
watch(
  () => model.value.reuse,
  (reuse) => {
    model.value.sourceId = null
    model.value.sourceVersion = null
    selectedSource.value = null
    searchSequence++
    detailSequence++
    searching.value = false
    loadingDetail.value = false
    if (reuse) void searchSources()
  },
)
</script>

<template>
  <div class="connection-form">
    <template v-if="!editingSource">
      <div
        v-if="!platformMode && options.methods.length > 1"
        class="method-grid"
        aria-label="接入方式"
      >
        <button
          v-for="method in options.methods"
          :key="method"
          type="button"
          class="method-choice"
          :class="{ selected: model.method === method }"
          :aria-pressed="model.method === method"
          :disabled="disabled || loading"
          @click="changeMethod(method)"
        >
          {{ label(method) }}
        </button>
      </div>
      <div v-if="platformMode" class="platform-selector">
        <label for="camera-platform-product">平台产品</label>
        <ElSelect
          id="camera-platform-product"
          :model-value="model.method"
          :disabled="disabled || loading"
          @change="changeMethod"
        >
          <ElOption
            v-for="preset in platformPresets"
            :key="preset.type"
            :value="preset.type"
            :label="preset.label"
          />
        </ElSelect>
        <p v-if="selectedPreset" class="platform-description">
          {{ selectedPreset.apiFamily }} · {{ selectedPreset.status }}
        </p>
      </div>
      <ElForm v-if="!unavailable" label-width="110px" :disabled="disabled" @submit.prevent>
        <ElFormItem label="连接配置"
          ><ElCheckbox v-model="model.reuse">使用已保存连接</ElCheckbox></ElFormItem
        >
        <template v-if="model.reuse">
          <ElFormItem label="已保存连接" required>
            <ElSelect
              v-model="model.sourceId"
              remote
              filterable
              :remote-method="searchSources"
              :loading="loading"
              placeholder="输入连接名称搜索"
              @change="selectSource"
            >
              <ElOption
                v-for="source in sources"
                :key="source.sourceId"
                :value="source.sourceId"
                :label="source.name"
              />
            </ElSelect>
          </ElFormItem>
          <p v-if="sourceTotal > 100" class="field-note">
            匹配超过 100 项，请输入更完整的连接名称。
          </p>
          <ElButton
            v-if="model.sourceId"
            link
            type="primary"
            :disabled="disabled || loading"
            @click="editingSource = true"
            >连接设置</ElButton
          >
          <RequestError :error="error" />
          <ElFormItem v-if="options.networkPolicies.length > 1" label="接入网络"
            ><ElSelect v-model="model.networkPolicyKey"
              ><ElOption
                v-for="policy in options.networkPolicies"
                :key="policy.key"
                :value="policy.key"
                :label="policy.name" /></ElSelect
          ></ElFormItem>
        </template>
        <template v-if="isRtsp">
          <ElFormItem label="相机名称" required
            ><ElInput v-model="model.name" maxlength="100" placeholder="例如：高炉东侧"
          /></ElFormItem>
          <ElFormItem
            v-for="(_, index) in model.rtspUrls"
            :key="index"
            :label="index ? `其他码流 ${index}` : 'RTSP 地址'"
            required
          >
            <div class="url-row">
              <ElInput
                v-model="model.rtspUrls[index]"
                type="password"
                show-password
                autocomplete="off"
                maxlength="8192"
                placeholder="rtsp://192.168.1.10:554/实际视频路径"
              /><ElButton
                v-if="index > 0"
                :disabled="disabled"
                @click="model.rtspUrls.splice(index, 1)"
                >移除</ElButton
              >
            </div>
          </ElFormItem>
          <ElFormItem
            ><ElButton
              :disabled="disabled || model.rtspUrls.length >= 8"
              @click="model.rtspUrls.push('')"
              >添加其他码流地址</ElButton
            ></ElFormItem
          >
        </template>
        <template v-else-if="!model.reuse">
          <ElFormItem :label="isPlatform ? '平台地址' : '设备地址'" required
            ><ElInput
              v-model="model.host"
              maxlength="253"
              placeholder="IP 或域名，例如 192.168.1.10"
          /></ElFormItem>
          <ElFormItem label="服务端口" required
            ><ElInputNumber v-model="model.port" :min="1" :max="65535"
          /></ElFormItem>
        </template>
        <template v-if="!model.reuse">
          <ElFormItem :label="isPlatform ? 'AppKey' : '用户名'" :required="isPlatform"
            ><ElInput
              v-model="model.username"
              autocomplete="off"
              maxlength="128"
              :placeholder="isRtsp ? '地址未包含账号时，可单独填写' : '设备或平台提供的账号'"
          /></ElFormItem>
          <ElFormItem :label="isPlatform ? 'AppSecret' : '密码'" :required="isPlatform"
            ><ElInput
              v-model="model.password"
              type="password"
              show-password
              autocomplete="new-password"
              maxlength="512"
              placeholder="不会回显已保存密码"
          /></ElFormItem>
          <details class="advanced-settings">
            <summary>高级设置</summary>
            <ElFormItem v-if="!isRtsp" label="连接协议"
              ><ElSelect v-model="model.scheme"
                ><ElOption label="HTTP" value="http" /><ElOption
                  label="HTTPS（验证证书）"
                  value="https" /></ElSelect
            ></ElFormItem>
            <ElFormItem v-if="!isRtsp && !isPlatform" label="RTSP 端口"
              ><ElInputNumber v-model="model.rtspPort" :min="1" :max="65535"
            /></ElFormItem>
            <ElFormItem v-if="!isRtsp" label="连接名称"
              ><ElInput v-model="model.name" maxlength="100" placeholder="留空则使用设备地址"
            /></ElFormItem>
            <ElFormItem v-if="options.networkPolicies.length > 1" label="接入网络"
              ><ElSelect v-model="model.networkPolicyKey" placeholder="选择设备所在网络"
                ><ElOption
                  v-for="policy in options.networkPolicies"
                  :key="policy.key"
                  :value="policy.key"
                  :label="policy.name" /></ElSelect
            ></ElFormItem>
          </details>
        </template>
      </ElForm>
    </template>
    <SourceEditor
      embedded
      v-if="editingSource && model.sourceId"
      :source-id="model.sourceId"
      @close="editingSource = false"
      @saved="sourceSaved"
    />
  </div>
</template>

<style scoped>
.platform-selector {
  display: grid;
  grid-template-columns: 98px minmax(0, 1fr);
  gap: 10px 12px;
  align-items: center;
  margin-bottom: 20px;
}
.platform-selector > label {
  text-align: right;
}
.platform-description {
  grid-column: 2;
  margin: 0;
  color: var(--text-secondary);
  font-size: 13px;
  line-height: 1.5;
}
.method-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
  margin-bottom: 24px;
}
.method-choice {
  min-height: 46px;
  padding: 10px;
  color: var(--el-text-color-regular);
  background: var(--el-fill-color-blank);
  border: 1px solid var(--el-border-color);
  border-radius: 5px;
  cursor: pointer;
  font: inherit;
}
.method-choice.selected {
  color: var(--el-color-primary);
  border-color: var(--el-color-primary);
  background: var(--el-color-primary-light-9);
  font-weight: 600;
}
.method-choice:focus-visible {
  outline: 2px solid var(--el-color-primary);
  outline-offset: 2px;
}
.method-choice:disabled {
  cursor: not-allowed;
  opacity: 0.65;
}
.url-row {
  display: flex;
  gap: 8px;
  width: 100%;
}
.field-note {
  color: var(--el-text-color-secondary);
  font-size: 13px;
  line-height: 1.6;
  margin: 0 0 16px 110px;
}
.advanced-settings {
  border-top: 1px solid var(--el-border-color-lighter);
  padding-top: 12px;
}
.advanced-settings summary {
  cursor: pointer;
  color: var(--el-text-color-secondary);
  margin-bottom: 16px;
}
@media (max-width: 640px) {
  .method-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .field-note {
    margin-left: 0;
  }
}
</style>
