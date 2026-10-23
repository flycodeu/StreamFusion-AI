import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createRenderer, nextTick, ssrContextKey } from 'vue'
import type { StorageInstance, StorageScope, StorageTask } from '../../api/storage/types'
import StorageLibrary from './StorageLibrary.vue'

const mock = vi.hoisted(() => ({
  listObjects: vi.fn(),
  replace: vi.fn(),
  route: { path: '/storage/manage', query: {} as Record<string, string> },
}))
vi.mock('../../api/storage/api', () => ({ listObjects: mock.listObjects }))
vi.mock('vue-router', () => ({
  useRoute: () => mock.route,
  useRouter: () => ({ replace: mock.replace }),
}))
vi.mock('element-plus', () =>
  Object.fromEntries(
    [
      'ElButton',
      'ElCheckbox',
      'ElDropdown',
      'ElDropdownMenu',
      'ElDropdownItem',
      'ElTooltip',
      'ElDialog',
      'ElEmpty',
      'ElIcon',
      'ElMessageBox',
      'ElInput',
      'ElMessage',
      'ElSelect',
      'ElOption',
      'ElTable',
      'ElTableColumn',
      'vLoading',
    ].map((key) => [key, {}]),
  ),
)
vi.mock('../../components/feedback/RequestError.vue', () => ({ default: {} }))
vi.mock('./StorageUpload.vue', () => ({ default: {} }))
vi.mock('./StorageFolderDelete.vue', () => ({ default: {} }))
vi.mock('./StorageFileDelete.vue', () => ({ default: {} }))
vi.mock('./StorageFileDetails.vue', () => ({ default: {} }))
vi.mock('./StorageFolderTree.vue', () => ({ default: {} }))
vi.mock('./StorageThumbnail.vue', () => ({ default: {} }))

const first = { scopeId: '11', prefix: 'images/', version: '0' } as StorageScope
const second = { scopeId: '22', prefix: 'other/', version: '0' } as StorageScope
const instance = { instanceId: '1', version: '0', scopes: [first, second] } as StorageInstance
interface State {
  prefix: string
  scopeId: string
  treeRevision: number
  folderDeletion: { scopeId: string; folder: string } | null
  folderDeletionScope: StorageScope | undefined
  menuEntry: unknown
  treeMenu: (event: unknown, id: string, directory: string) => void
  menuCommand: (action: string) => void
  folderUpdated: (task: StorageTask) => Promise<void>
  refreshFiles: () => void
}
const renderer = createRenderer<Record<string, unknown>, Record<string, unknown>>({
  createElement: () => ({}),
  createText: () => ({}),
  createComment: () => ({}),
  setText() {},
  setElementText() {},
  patchProp() {},
  insert() {},
  remove() {},
  parentNode: () => null,
  nextSibling: () => null,
})
let dispose: () => void = () => {}
async function mount(directory = '') {
  mock.route.query = { instance: '1', scope: '11', directory }
  const app = renderer.createApp({ ...StorageLibrary, render: () => null }, { instance })
  app.provide(ssrContextKey, { modules: new Set<string>() })
  app.mount({})
  dispose = () => app.unmount()
  await Promise.resolve()
  await nextTick()
  return (app._instance as unknown as { setupState: State }).setupState
}
beforeEach(() => {
  vi.resetAllMocks()
  vi.stubGlobal(
    'DOMRect',
    class {
      constructor(
        public x: number,
        public y: number,
      ) {}
    },
  )
  mock.listObjects.mockResolvedValue({
    items: [],
    commonPrefixes: [],
    nextCursor: null,
    hasMore: false,
  })
  mock.replace.mockResolvedValue(undefined)
})
afterEach(() => {
  dispose()
  vi.unstubAllGlobals()
})

describe('folder tree deletion ownership and refresh', () => {
  it('binds a right-clicked directory to its own scope instead of the currently open bucket', async () => {
    const state = await mount()
    state.treeMenu(
      {
        currentTarget: { getBoundingClientRect: () => ({ right: 120, bottom: 80 }) },
        clientX: 100,
        clientY: 70,
      },
      '22',
      '中文目录/',
    )
    await nextTick()
    state.menuCommand('delete')
    expect(state.folderDeletion).toEqual({ scopeId: '22', folder: 'other/中文目录/' })
    expect(state.folderDeletionScope?.scopeId).toBe('22')
    expect(state.scopeId).toBe('11')
  })
  it.each(['', 'top/'])(
    'returns to the parent after deleting the active directory below %s',
    async (parent) => {
      const state = await mount(`${parent}drop/nested/`)
      state.folderDeletion = { scopeId: '11', folder: `images/${parent}drop/` }
      await state.folderUpdated({ status: 'SUCCEEDED' } as StorageTask)
      expect(state.prefix).toBe(parent)
      expect(mock.replace).toHaveBeenCalledWith({
        query: { instance: '1', scope: '11', directory: parent || undefined },
      })
      expect(mock.listObjects).toHaveBeenLastCalledWith('11', parent, undefined, 20)
    },
  )
  it('keeps the current location after a partial delete and refreshes both views', async () => {
    const state = await mount('drop/nested/'),
      revision = state.treeRevision
    state.folderDeletion = { scopeId: '11', folder: 'images/drop/' }
    await state.folderUpdated({ status: 'PARTIAL' } as StorageTask)
    expect(state.prefix).toBe('drop/nested/')
    expect(mock.replace).not.toHaveBeenCalled()
    expect(state.treeRevision).toBe(revision + 1)
  })
  it('does not navigate away when a directory in another scope is deleted', async () => {
    const state = await mount('drop/')
    state.folderDeletion = { scopeId: '22', folder: 'other/drop/' }
    await state.folderUpdated({ status: 'SUCCEEDED' } as StorageTask)
    expect(state.prefix).toBe('drop/')
    expect(state.scopeId).toBe('11')
    expect(mock.replace).not.toHaveBeenCalled()
  })
  it('refreshes the directory tree together with files, and offers no root-delete menu', async () => {
    const state = await mount(),
      revision = state.treeRevision
    state.menuEntry = { scopeId: '11', full: 'images/previous/' }
    state.treeMenu({}, '11', '')
    expect(state.menuEntry).toBeNull()
    state.refreshFiles()
    expect(state.treeRevision).toBe(revision + 1)
    expect(mock.listObjects).toHaveBeenCalledTimes(2)
  })
})
