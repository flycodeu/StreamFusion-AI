import type { MenuNode } from '../../api/menus/types'
import { labelOf } from '../../utils/display'

export const menuTypeLabels = { DIRECTORY: '目录', PAGE: '页面' } as const satisfies Record<
  MenuNode['type'],
  string
>
export const menuTypeLabel = (value: string) => labelOf(menuTypeLabels, value)
