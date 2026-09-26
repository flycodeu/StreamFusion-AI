import { describe, expect, it, vi } from 'vitest'
import type { MenuRoute } from '../../api/auth/types'
import { buildMenuRoutes, fixedPaths, validMenuPath } from './routes'
import { createPageResolver } from './views'
import type { PageLoader } from './views'

const cameraPage: PageLoader = async () => ({ default: { name: 'CameraManage' } })
const resolve = createPageResolver({ '../../views/camera/manage.vue': cameraPage })

function page(values: Partial<MenuRoute> = {}): MenuRoute {
  return {
    id: '100',
    name: '相机管理',
    type: 'PAGE',
    icon: 'camera',
    visible: true,
    routeName: 'camera:manage',
    path: '/camera/manage',
    componentKey: null,
    moduleKey: 'camera',
    children: [],
    ...values,
  }
}

function directory(children: MenuRoute[], values: Partial<MenuRoute> = {}): MenuRoute {
  return page({
    id: '10',
    name: '设备管理',
    type: 'DIRECTORY',
    routeName: null,
    path: null,
    moduleKey: null,
    children,
    ...values,
  })
}

describe('backend menu route assembly', () => {
  it.each(['/public', '/Public/file', '/node_modules', '/NODE_MODULES/vue'])(
    'rejects reserved static root %s',
    (path) => {
      expect(validMenuPath(path)).toBe(false)
    },
  )
  it('creates a new module route from its path and key with no frontend registration', () => {
    const result = buildMenuRoutes([directory([page()])], ['camera'], resolve)
    expect(result.records).toHaveLength(1)
    expect(result.records[0]).toMatchObject({
      path: '/camera/manage',
      name: 'page:camera:manage',
      sensitive: true,
      component: cameraPage,
      meta: {
        menuId: '100',
        moduleKey: 'camera',
        breadcrumbs: ['设备管理', '相机管理'],
        componentPath: '/camera/manage',
        configurationError: false,
      },
    })
    expect(result.navigation).toEqual([
      {
        id: '10',
        title: '设备管理',
        icon: 'camera',
        children: [
          { id: '100', title: '相机管理', icon: 'camera', path: '/camera/manage', children: [] },
        ],
      },
    ])
  })

  it('resolves the component without requiring the URL to match the filename', () => {
    const departmentResolve = createPageResolver({
      '../../views/system/Department.vue': cameraPage,
    })
    const result = buildMenuRoutes(
      [
        page({
          path: '/system/departments',
          routeName: 'departments',
          componentKey: '/system/Department',
          moduleKey: 'departments',
        }),
      ],
      ['departments'],
      departmentResolve,
    )
    expect(result.records[0]).toMatchObject({
      path: '/system/departments',
      component: cameraPage,
      meta: { componentPath: '/system/Department', configurationError: false },
    })
  })

  it.each([
    ...fixedPaths,
    '/LOGIN',
    '/login/child',
    '/home/child',
    '/profile/child',
    '/api/camera',
    '/auth/me',
    '/actuator/health',
    '/assets/icon',
    '/src/main',
    '//example.invalid/manage',
    'https://example.invalid/manage',
    '/camera/../manage',
    '/camera/%2e%2e/manage',
    '/camera/%252fmanage',
    '/camera/\\manage',
    '/camera/manage?all=true',
    '/camera/manage#details',
    '/camera/manage.vue',
    '/camera//manage',
    '/camera/manage/',
    `/camera/${'a'.repeat(201)}`,
  ])('cannot replace a fixed route or register an unsafe path: %s', (path) => {
    expect(validMenuPath(path)).toBe(false)
    expect(buildMenuRoutes([page({ path })], ['camera'], resolve)).toEqual({
      records: [],
      navigation: [],
    })
  })

  it('retains authorized hidden page routes while excluding their navigation entries', () => {
    const result = buildMenuRoutes([page({ visible: false })], ['camera'], resolve)
    expect(result.records).toHaveLength(1)
    expect(result.navigation).toEqual([])
  })

  it('retains routes under hidden directories and removes their whole navigation branch', () => {
    const result = buildMenuRoutes(
      [directory([directory([page()], { id: '11', name: '相机' })], { visible: false })],
      ['camera'],
      resolve,
    )
    expect(result.records).toHaveLength(1)
    expect(result.records[0]?.meta?.breadcrumbs).toEqual(['设备管理', '相机', '相机管理'])
    expect(result.navigation).toEqual([])
  })

  it.each([null, '', 'Camera', 'users'])(
    'omits a page without its exact module grant: %s',
    (key) => {
      const resolver = vi.fn(resolve)
      expect(
        buildMenuRoutes([directory([page({ moduleKey: key })])], ['camera'], resolver),
      ).toEqual({
        records: [],
        navigation: [],
      })
      expect(resolver).not.toHaveBeenCalled()
    },
  )

  it('removes authorized-looking menu entries when the session has no module grants', () => {
    expect(buildMenuRoutes([page()], [], resolve)).toEqual({ records: [], navigation: [] })
  })

  it.each([null, '', '123camera', 'camera.manage', 'camera/manage', `A${'a'.repeat(64)}`])(
    'rejects an invalid or missing unique route key: %s',
    (routeName) => {
      expect(buildMenuRoutes([page({ routeName })], ['camera'], resolve)).toEqual({
        records: [],
        navigation: [],
      })
    },
  )

  it.each([
    { id: '101', path: '/camera/other', routeName: 'CAMERA:MANAGE' },
    { id: '101', path: '/CAMERA/MANAGE', routeName: 'camera:other' },
  ])('keeps the first route when key or URL collides case-insensitively', (duplicate) => {
    const result = buildMenuRoutes([page(), directory([page(duplicate)])], ['camera'], resolve)
    expect(result.records).toHaveLength(1)
    expect(result.records[0]?.meta?.menuId).toBe('100')
    expect(result.navigation).toHaveLength(1)
    expect(result.navigation[0]?.id).toBe('100')
  })

  it('does not let an unauthorized entry reserve a legitimate key or URL', () => {
    const result = buildMenuRoutes(
      [page({ id: '99', moduleKey: 'users' }), page()],
      ['camera'],
      resolve,
    )
    expect(result.records).toHaveLength(1)
    expect(result.records[0]?.meta?.menuId).toBe('100')
  })

  it.each(['/camera/missing', '/camera/../private'])(
    'keeps a configured but unresolved page visible as a configuration error: %s',
    (componentKey) => {
      const result = buildMenuRoutes([page({ componentKey })], ['camera'], resolve)
      expect(result.records).toHaveLength(1)
      expect(result.records[0]?.meta?.configurationError).toBe(true)
      expect(result.records[0]?.component).toBeTypeOf('function')
      expect(result.records[0]?.component).not.toBe(cameraPage)
      expect(result.navigation[0]?.path).toBe('/camera/manage')
    },
  )
})
