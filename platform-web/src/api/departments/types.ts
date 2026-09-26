import { id, integer, list, object, string } from '../parse'

export interface Department {
  id: string
  parentId: string | null
  name: string
  sortOrder: number
  memberCount: number
  version: string
  children: Department[]
}

export interface DepartmentOption {
  id: string
  parentId: string | null
  name: string
}

export function parseDepartmentOption(value: unknown): DepartmentOption {
  const row = object(value)
  return {
    id: id(row.id),
    parentId: row.parentId == null ? null : id(row.parentId),
    name: string(row.name),
  }
}

export function parseDepartment(value: unknown, depth = 0): Department {
  if (depth > 8) throw new Error('depth')
  const row = object(value)
  return {
    ...parseDepartmentOption(row),
    sortOrder: integer(row.sortOrder),
    memberCount: integer(row.memberCount),
    version: id(row.version),
    children: list(row.children, (child) => parseDepartment(child, depth + 1)),
  }
}

export interface UserDepartments {
  userId: string
  version: string
  departments: { id: string; name: string }[]
}

export function parseUserDepartments(value: unknown): UserDepartments {
  const row = object(value)
  return {
    userId: id(row.userId),
    version: id(row.version),
    departments: list(row.departments, (entry) => {
      const department = object(entry)
      return { id: id(department.id), name: string(department.name) }
    }),
  }
}
