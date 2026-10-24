import type { Blocker } from '../../api/access-query/types'
import { labelOf } from '../../utils/display'

const reasons = {
  ROLE_DISABLED: '角色已停用',
  PAGE_DISABLED: '页面已停用',
  ANCESTOR_DISABLED: '上级目录已停用',
  INVALID_MENU_TREE: '菜单层级异常',
  MODULE_UNREGISTERED: '后端模块未注册',
  NO_ACTIVE_ROLE: '授予角色均已停用',
  USER_DISABLED: '账号已停用',
  PASSWORD_CHANGE_REQUIRED: '需先完成改密',
  NO_PAGE_GRANT: '未分配此页面',
}
export const reasonText = (reason: Blocker) => `${labelOf(reasons, reason.code)}：${reason.name}`
export const eligibilityText = (effective: boolean, eligible: boolean, sourceCount = 1) =>
  eligible ? '有效' : sourceCount === 0 ? '未分配此页面' : effective ? '账号受限' : '已分配但未生效'
