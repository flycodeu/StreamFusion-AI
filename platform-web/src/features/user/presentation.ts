import { labelOf } from '../../utils/display'

// The backend UserStatus/UserGender own these business codes; unknown responses remain visible.
export const userStatusLabels = { 0: '待改密', 1: '正常', 2: '停用' } as const
export const genderLabels = { 0: '未设置', 1: '男', 2: '女' } as const
export const userStatusOptions = Object.entries(userStatusLabels).map(([value, label]) => ({
  value: Number(value),
  label,
}))
export const genderOptions = Object.entries(genderLabels).map(([value, label]) => ({
  value: Number(value),
  label,
}))
export const userStatusLabel = (value: number) => labelOf(userStatusLabels, value)
export const userStatusTone = (value: number) =>
  value === 0 ? 'warning' : value === 1 ? 'success' : value === 2 ? 'danger' : 'info'
