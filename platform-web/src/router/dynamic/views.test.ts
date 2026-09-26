import { describe, expect, it } from 'vitest'
import { availablePagePaths, createPageResolver, normalizeViewPath, resolvePage } from './views'
import type { PageLoader } from './views'

const cameraPage: PageLoader = async () => ({ default: { name: 'CameraManage' } })
const departmentPage: PageLoader = async () => ({ default: { name: 'Department' } })

describe('file-based business page resolution', () => {
  it('discovers business pages while fixed application pages remain outside the manifest', () => {
    expect(availablePagePaths).toEqual([
      '/monitor/ApiDocs',
      '/monitor/Audit',
      '/monitor/Server',
      '/system/Department',
      '/system/Menu',
      '/system/Role',
      '/system/User',
    ])
    for (const path of [
      '/auth/LoginView',
      '/auth/ChangePasswordView',
      '/home/HomeView',
      '/account/ProfileView',
      '/error/ForbiddenView',
      '/error/UnavailableView',
      '/error/RouteUnavailableView',
    ]) {
      expect(availablePagePaths).not.toContain(path)
      expect(resolvePage(path)).toBeUndefined()
    }
  })
  it('loads a new camera page directly from its file path without a registry entry', async () => {
    const resolve = createPageResolver({
      '../../views/camera/manage.vue': cameraPage,
      '../../views/camera/group/index.vue': departmentPage,
    })

    expect(resolve('/camera/manage')).toBe(cameraPage)
    expect(resolve('/camera/group/index')).toBe(departmentPage)
    expect(await resolve('/camera/manage')?.()).toMatchObject({
      default: { name: 'CameraManage' },
    })
  })

  it.each([
    null,
    '',
    'camera/manage',
    '//camera/manage',
    'https://example.invalid/camera/manage',
    '/camera/../manage',
    '/camera/./manage',
    '/camera/%2e%2e/manage',
    '/camera/%252fmanage',
    '/camera/%5cmanage',
    '/camera/\\manage',
    '/camera//manage',
    '/camera/manage/',
    '/camera/manage.vue',
    '/camera/manage?mode=edit',
    '/camera/manage#section',
    '/camera/manage view',
  ])('rejects a noncanonical component path: %s', (path) => {
    expect(normalizeViewPath(path)).toBeNull()
    expect(
      createPageResolver({ '../../views/camera/manage.vue': cameraPage })(path),
    ).toBeUndefined()
  })

  it('resolves only existing Vue files and preserves case-sensitive filenames', () => {
    const resolve = createPageResolver({
      '../../views/camera/manage.vue': cameraPage,
      '../../components/ColumnPicker.vue': departmentPage,
      '../../views/camera/helper.ts': departmentPage,
      '../../views/camera/../private.vue': departmentPage,
    })
    expect(resolve('/camera/Manage')).toBeUndefined()
    expect(resolve('/camera/missing')).toBeUndefined()
    expect(resolve('/ColumnPicker')).toBeUndefined()
    expect(resolve('/camera/helper')).toBeUndefined()
    expect(resolve('/camera/../private')).toBeUndefined()
  })
})
