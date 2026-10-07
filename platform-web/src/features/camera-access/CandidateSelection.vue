<script setup lang="ts">
import { computed, ref } from 'vue'
import {
  ElButton,
  ElCheckbox,
  ElFormItem,
  ElOption,
  ElPagination,
  ElSelect,
  ElTag,
} from 'element-plus'
import type {
  AccessCandidate,
  CandidateSelection,
  CandidateProfile,
} from '../../api/camera-access/types'
import { initialSelections } from './form'
import { usageLabel } from '../camera/form'

const props = defineProps<{ candidates: AccessCandidate[]; disabled: boolean; single?: boolean }>()
const model = defineModel<CandidateSelection[]>({ required: true })
const page = ref(1)
const visible = computed(() => props.candidates.slice((page.value - 1) * 12, page.value * 12))
function selected(candidateId: string) {
  return model.value.find((item) => item.candidateId === candidateId)
}
function toggleCandidate(candidate: AccessCandidate, checked: boolean) {
  model.value = model.value.filter((item) => item.candidateId !== candidate.candidateId)
  if (checked)
    model.value = [...(props.single ? [] : model.value), ...initialSelections([candidate])]
}
function toggleProfile(candidate: AccessCandidate, profile: CandidateProfile, checked: boolean) {
  const selection = selected(candidate.candidateId)
  if (!selection) return
  selection.profileIds = checked
    ? [...selection.profileIds, profile.profileId]
    : selection.profileIds.filter((value) => value !== profile.profileId)
  if (!selection.profileIds.includes(selection.defaultProfileId ?? ''))
    selection.defaultProfileId = selection.profileIds[0]
  model.value = [...model.value]
}
</script>

<template>
  <div class="candidate-selection">
    <div class="selection-toolbar">
      <strong>读取到 {{ candidates.length }} 个通道 · 已选 {{ model.length }} 个</strong>
      <div>
        <ElButton v-if="!single" :disabled="disabled" @click="model = initialSelections(candidates)"
          >选择可导入通道</ElButton
        ><ElButton :disabled="disabled" @click="model = []">清空选择</ElButton>
      </div>
    </div>
    <p v-if="!candidates.length" class="empty-state">本页无相机通道。</p>
    <section v-for="candidate in visible" :key="candidate.candidateId" class="candidate-row">
      <header>
        <ElCheckbox
          :model-value="!!selected(candidate.candidateId)"
          :disabled="disabled || candidate.mappingRequired"
          @update:model-value="toggleCandidate(candidate, !!$event)"
          >{{ candidate.name }}</ElCheckbox
        ><ElTag v-if="candidate.mappingRequired" type="warning">通道映射待确认，暂不能导入</ElTag
        ><ElTag v-else-if="!candidate.profiles.length" type="info">暂无码流资料</ElTag
        ><span v-else>{{ candidate.profiles.length }} 路码流</span>
      </header>
      <template v-if="selected(candidate.candidateId)">
        <div v-for="profile in candidate.profiles" :key="profile.profileId" class="profile-row">
          <ElCheckbox
            :model-value="selected(candidate.candidateId)!.profileIds.includes(profile.profileId)"
            :disabled="disabled"
            @update:model-value="toggleProfile(candidate, profile, !!$event)"
            >{{ profile.name || usageLabel(profile.usageHint) }}</ElCheckbox
          >
          <span class="profile-parameters"
            >{{ usageLabel(profile.usageHint) }} · {{ profile.videoCodec || '编码未获取' }} ·
            {{
              profile.width && profile.height
                ? `${profile.width} × ${profile.height}`
                : '分辨率未获取'
            }}
            · {{ profile.frameRate == null ? '帧率未获取' : `${profile.frameRate} fps` }} ·
            {{ profile.bitrateKbps == null ? '码率未获取' : `${profile.bitrateKbps} kbps` }}</span
          >
        </div>
        <ElFormItem
          v-if="selected(candidate.candidateId)!.profileIds.length"
          label="默认码流"
          class="default-profile"
          ><ElSelect
            v-model="selected(candidate.candidateId)!.defaultProfileId"
            clearable
            :disabled="disabled"
            placeholder="可留空，暂不设置默认码流"
            ><ElOption
              v-for="profile in candidate.profiles.filter((item) =>
                selected(candidate.candidateId)!.profileIds.includes(item.profileId),
              )"
              :key="profile.profileId"
              :value="profile.profileId"
              :label="profile.name || usageLabel(profile.usageHint)" /></ElSelect
        ></ElFormItem>
      </template>
    </section>
    <ElPagination
      v-if="candidates.length > 12"
      v-model:current-page="page"
      :page-size="12"
      :total="candidates.length"
      layout="total, prev, pager, next"
    />
  </div>
</template>

<style scoped>
.selection-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 16px;
  flex-wrap: wrap;
}
.candidate-row {
  padding: 14px 16px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 5px;
  margin-bottom: 12px;
}
.candidate-row header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.candidate-row header > span,
.profile-parameters,
.empty-state {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.profile-row {
  display: flex;
  align-items: center;
  gap: 16px;
  padding-left: 22px;
  flex-wrap: wrap;
}
.default-profile {
  margin: 10px 0 0 22px;
  max-width: 370px;
}
.invalid-selection {
  color: var(--el-color-danger);
  margin: 8px 0 0 22px;
}
</style>
