import { boolean, id, integer, list, object, optionalString, string } from '../parse'

export interface UserProfile {
  id: string
  username: string
  nickname: string | null
  avatarKey: string | null
  phone: string | null
  email: string | null
  gender: number
  status: number
  mustChangePassword: boolean
  version: string
}

export interface MenuRoute {
  id: string
  name: string
  type: 'DIRECTORY' | 'PAGE'
  icon: string | null
  visible: boolean
  routeName: string | null
  path: string | null
  componentKey: string | null
  moduleKey: string | null
  children: MenuRoute[]
}

export interface AuthUser {
  user: UserProfile
  modules: string[]
  roles: { id: string; code: string; name: string }[]
  routes: MenuRoute[]
  isSuperAdmin: boolean
}

export function parseProfile(value: unknown): UserProfile {
  const row = object(value)
  return {
    id: id(row.id),
    username: string(row.username),
    nickname: optionalString(row.nickname),
    avatarKey: optionalString(row.avatarKey),
    phone: optionalString(row.phone),
    email: optionalString(row.email),
    gender: integer(row.gender),
    status: integer(row.status),
    mustChangePassword: boolean(row.mustChangePassword),
    version: id(row.version),
  }
}

function parseRoute(value: unknown, depth = 0): MenuRoute {
  if (depth > 5) throw new Error('route depth')
  const row = object(value)
  const type = string(row.type)
  if (type !== 'DIRECTORY' && type !== 'PAGE') throw new Error('route type')
  return {
    id: id(row.id),
    name: string(row.name),
    type,
    icon: optionalString(row.icon),
    visible: boolean(row.visible),
    routeName: optionalString(row.routeName),
    path: optionalString(row.path),
    componentKey: optionalString(row.componentKey),
    moduleKey: optionalString(row.moduleKey),
    children:
      row.children == null ? [] : list(row.children, (child) => parseRoute(child, depth + 1)),
  }
}

export function parseAuthUser(value: unknown): AuthUser {
  const row = object(value)
  return {
    user: parseProfile(row.user),
    modules: list(row.modules, string),
    roles: list(row.roles, (entry) => {
      const role = object(entry)
      return { id: id(role.id), code: string(role.code), name: string(role.name) }
    }),
    routes: parseMenuRoutes(row.routes),
    isSuperAdmin: boolean(row.isSuperAdmin),
  }
}

export function parseMenuRoutes(value: unknown): MenuRoute[] {
  return list(value, parseRoute)
}
