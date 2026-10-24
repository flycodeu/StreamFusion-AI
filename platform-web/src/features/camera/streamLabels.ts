import type { UsageHint } from '../../api/camera/types'

/** Shared display names only; stream identities and selection rules stay unchanged. */
export const usageLabels = {
  MAIN: '主码流',
  SUB: '子码流',
  THIRD: '第三码流',
  CUSTOM: '自定义',
  UNKNOWN: '未指定',
} as const satisfies Record<UsageHint, string>
export const usageOptions = Object.entries(usageLabels).map(([value, label]) => ({
  value: value as UsageHint,
  label,
}))
export function usageLabel(value: string): string {
  return Object.hasOwn(usageLabels, value) ? usageLabels[value as UsageHint] : value
}

interface StreamLabelProfile {
  streamProfileId?: string
  label?: string | null
  usageHint?: string | null
  classification?: { usageHint: string; origin?: string }
}
export function effectiveUsage(profile: StreamLabelProfile): string {
  return profile.classification?.usageHint ?? profile.usageHint ?? 'UNKNOWN'
}

const nameUsages: Record<string, UsageHint> = {
  main: 'MAIN',
  mainstream: 'MAIN',
  主码流: 'MAIN',
  sub: 'SUB',
  substream: 'SUB',
  substream1: 'SUB',
  子码流: 'SUB',
  辅码流: 'SUB',
  third: 'THIRD',
  thirdstream: 'THIRD',
  substream2: 'THIRD',
  第三码流: 'THIRD',
}
function baseLabel(profile: StreamLabelProfile, cameraName?: string): string {
  const name = profile.label?.trim() ?? ''
  const role = effectiveUsage(profile)
  if (role === 'CUSTOM') return name || usageLabel(role)
  const knownRole = role === 'MAIN' || role === 'SUB' || role === 'THIRD'
  const deviceName = /^mediaprofile_channel[0-9]+_(mainstream|substream1|substream2)$/i.exec(
    name,
  )?.[1]
  const key = (deviceName ?? name).replace(/[\s_-]/g, '').toLowerCase()
  const alias = Object.hasOwn(nameUsages, key) ? nameUsages[key] : undefined
  const generated = /^(media)?profile[_\s-]/i.test(name)
  if (knownRole && (alias || generated || name === cameraName)) return usageLabel(role)
  if (alias) return usageLabel(alias)
  return name || usageLabel(role)
}

export function streamLabel(
  profile: StreamLabelProfile,
  profiles: readonly StreamLabelProfile[] = [],
  cameraName?: string,
): string {
  const label = baseLabel(profile, cameraName)
  const duplicates = profiles.filter((item) => baseLabel(item, cameraName) === label)
  if (duplicates.length < 2) return label
  const index = duplicates.findIndex(
    (item) =>
      item === profile ||
      (profile.streamProfileId != null && item.streamProfileId === profile.streamProfileId),
  )
  return index < 0 ? label : `${label} ${index + 1}`
}
