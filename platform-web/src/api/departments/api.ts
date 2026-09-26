import { request } from '../client'
import { empty, list } from '../parse'
import { parseDepartment } from './types'
import type { Department } from './types'

export async function getDepartments(name?: string): Promise<Department[]> {
  return (
    await request({
      path: '/departments',
      method: 'GET',
      params: { name },
      successStatus: 200,
      decode: (v) => list(v, parseDepartment),
    })
  ).data
}

export async function createDepartment(input: {
  parentId: string | null
  name: string
  sortOrder: number
}): Promise<Department> {
  return (
    await request({
      path: '/departments',
      method: 'POST',
      body: input,
      successStatus: 201,
      decode: parseDepartment,
    })
  ).data
}

export async function updateDepartment(input: Department): Promise<Department> {
  return (
    await request({
      path: `/departments/${input.id}`,
      method: 'PUT',
      body: {
        parentId: input.parentId,
        name: input.name,
        sortOrder: input.sortOrder,
        version: input.version,
      },
      successStatus: 200,
      decode: parseDepartment,
    })
  ).data
}

export async function deleteDepartment(input: Department): Promise<void> {
  await request({
    path: `/departments/${input.id}`,
    method: 'DELETE',
    ifMatch: `"${input.version}"`,
    successStatus: 200,
    decode: empty,
  })
}
