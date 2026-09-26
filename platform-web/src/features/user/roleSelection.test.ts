import { describe, expect, it } from 'vitest'
import type { RoleOption } from '../../api/roles/types'
import {
  filterRoleChoices,
  protectedRoleSelection,
  roleChoices,
  updateVisibleRoleSelection,
} from './roleSelection'

const admin: RoleOption = { id: '1', name: '超级管理员', code: 'SUPER_ADMIN' }
const operator: RoleOption = { id: '2', name: '运维人员', code: 'OPERATOR' }
const reviewer: RoleOption = { id: '3', name: '审核人员', code: 'REVIEWER' }
const legacy: RoleOption = { id: '4', name: '历史角色', code: 'LEGACY' }

describe('user role assignment selection', () => {
  it('merges assignable and already assigned roles without dropping unavailable bindings', () => {
    const choices = roleChoices(
      [operator, reviewer],
      [legacy, { ...operator, name: '旧名称' }],
      false,
    )
    expect(choices).toEqual([
      { ...legacy, available: false, locked: false },
      { ...operator, available: true, locked: false },
      { ...reviewer, available: true, locked: false },
    ])
  })

  it('searches role names and codes without changing the original choices', () => {
    const choices = roleChoices([operator, reviewer], [legacy], false)
    expect(filterRoleChoices(choices, '  运维 ')).toEqual([choices[1]])
    expect(filterRoleChoices(choices, ' reviewER ')).toEqual([choices[2]])
    expect(filterRoleChoices(choices, ' ')).toEqual(choices)
    expect(filterRoleChoices(choices, 'missing')).toEqual([])
    expect(choices).toHaveLength(3)
  })

  it('preserves hidden selections when selecting or clearing filtered rows', () => {
    const choices = roleChoices([operator, reviewer], [legacy], false)
    const visible = filterRoleChoices(choices, 'OPERATOR')
    const selected = updateVisibleRoleSelection([legacy.id, reviewer.id], visible, [operator.id])
    expect(selected).toEqual([legacy.id, reviewer.id, operator.id])
    expect(updateVisibleRoleSelection(selected, visible, [])).toEqual([legacy.id, reviewer.id])
  })

  it('selects only current search results and ignores ids outside those results', () => {
    const choices = roleChoices([operator, reviewer], [], false)
    const visible = filterRoleChoices(choices, 'OPERATOR')
    expect(updateVisibleRoleSelection([], visible, [operator.id, reviewer.id, 'unknown'])).toEqual([
      operator.id,
    ])
  })

  it('keeps own existing super administrator binding when clearing all visible selections', () => {
    const choices = roleChoices([admin, operator], [admin, operator], true)
    expect(choices[0]?.locked).toBe(true)
    expect(updateVisibleRoleSelection([admin.id, operator.id], choices, [])).toEqual([admin.id])
    expect(protectedRoleSelection([], choices)).toEqual([admin.id])
  })

  it('does not lock another user or invent an unassigned super administrator binding', () => {
    const otherUserChoices = roleChoices([admin, operator], [admin], false)
    expect(protectedRoleSelection([], otherUserChoices)).toEqual([])
    const unassignedChoices = roleChoices([admin, operator], [operator], true)
    expect(unassignedChoices.find((role) => role.id === admin.id)?.locked).toBe(false)
    expect(protectedRoleSelection([], unassignedChoices)).toEqual([])
  })

  it('allows an unavailable existing role to remain selected or be removed', () => {
    const choices = roleChoices([operator], [legacy], false)
    expect(protectedRoleSelection([legacy.id], choices)).toEqual([legacy.id])
    expect(
      updateVisibleRoleSelection([legacy.id], filterRoleChoices(choices, 'LEGACY'), []),
    ).toEqual([])
  })

  it('normalizes duplicate selections and excludes unknown ids before saving', () => {
    const choices = roleChoices([admin, operator], [admin], true)
    expect(protectedRoleSelection([operator.id, operator.id, 'unknown'], choices)).toEqual([
      operator.id,
      admin.id,
    ])
  })
})
