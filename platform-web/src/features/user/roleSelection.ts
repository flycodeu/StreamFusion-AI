import type { RoleOption } from '../../api/roles/types'

export interface RoleChoice extends RoleOption {
  available: boolean
  locked: boolean
}

export function roleChoices(
  options: readonly RoleOption[],
  assigned: readonly RoleOption[],
  protectOwnSuperAdmin: boolean,
): RoleChoice[] {
  const available = new Set(options.map((role) => role.id))
  const assignedIds = new Set(assigned.map((role) => role.id))
  const roles = new Map([...assigned, ...options].map((role) => [role.id, role]))
  return [...roles.values()].map((role) => ({
    ...role,
    available: available.has(role.id),
    locked: protectOwnSuperAdmin && role.code === 'SUPER_ADMIN' && assignedIds.has(role.id),
  }))
}

export function protectedRoleSelection(
  ids: readonly string[],
  roles: readonly RoleChoice[],
): string[] {
  const known = new Set(roles.map((role) => role.id))
  return [
    ...new Set([
      ...ids.filter((id) => known.has(id)),
      ...roles.filter((role) => role.locked).map((role) => role.id),
    ]),
  ]
}
