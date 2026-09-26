export const avatarCatalog = [
  { key: 'avatar-01', label: '头像一', background: '#e8f0fb', color: '#3973b9' },
  { key: 'avatar-02', label: '头像二', background: '#e5f2ee', color: '#34806b' },
  { key: 'avatar-03', label: '头像三', background: '#edf0f5', color: '#65738d' },
  { key: 'avatar-04', label: '头像四', background: '#fff0de', color: '#aa7430' },
  { key: 'avatar-05', label: '头像五', background: '#e2f1f6', color: '#367d98' },
  { key: 'avatar-06', label: '头像六', background: '#f6e9e6', color: '#aa6557' },
] as const

export function resolveAvatar(key?: string | null) {
  return avatarCatalog.find((avatar) => avatar.key === key) ?? null
}
