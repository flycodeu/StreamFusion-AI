import type { Lifecycle } from '../../api/camera/types'
import type { AccessAdapter } from '../../api/camera-access/types'
import { labelOf } from '../../utils/display'

export const lifecycleLabels = {
  PENDING_ASSIGNMENT: '待归档',
  ENABLED: '已启用',
  DISABLED: '已停用',
} as const satisfies Record<Lifecycle, string>
export const lifecycleLabel = (value: string) => labelOf(lifecycleLabels, value)

const brandAliases: Readonly<Record<string, string>> = {
  HIKVISION: '海康',
  HIK_PLATFORM: '海康',
  DAHUA: '大华',
}
function brandLabel(value: string | null | undefined): string {
  const text = value?.trim() ?? ''
  const key = text.toUpperCase()
  if (Object.hasOwn(brandAliases, key)) return brandAliases[key]!
  if (key.includes('HIKVISION')) return brandAliases.HIKVISION!
  if (key.includes('DAHUA')) return brandAliases.DAHUA!
  return text
}
export function brandOptions(adapters: AccessAdapter[]): string[] {
  return [
    ...new Set(
      adapters.flatMap((adapter) =>
        adapter.category === 'DEVICE' && Object.hasOwn(brandAliases, adapter.type)
          ? [brandAliases[adapter.type]!]
          : [],
      ),
    ),
  ]
}
export function sourceBrandLabel(camera: {
  sourceType: string | null
  sourceDisplayName: string
  manufacturer?: string | null
  vendorHint?: string | null
}): string {
  // Display aliases never replace the saved vendor hint, observed manufacturer or protocol.
  return (
    brandLabel(camera.vendorHint) ||
    (camera.sourceType && Object.hasOwn(brandAliases, camera.sourceType.toUpperCase())
      ? brandAliases[camera.sourceType.toUpperCase()]
      : '') ||
    brandLabel(camera.manufacturer) ||
    '未识别品牌'
  )
}
