import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createRenderer, nextTick, ssrContextKey } from 'vue'
import type { StorageInstance, StorageObject, StorageScope } from '../../api/storage/types'
import { ApiRequestError } from '../../lib/http/error'
import StorageFileDetails from './StorageFileDetails.vue'

const api = vi.hoisted(() => ({ getObject: vi.fn(), objectContentUrl: vi.fn() }))
vi.mock('../../api/storage/api', () => api)
vi.mock('../../components/feedback/RequestError.vue', () => ({ default: { render: () => null } }))
vi.mock('element-plus', () => ({
  ElButton: {},
  ElDialog: {},
  ElIcon: {},
  ElSkeleton: {},
}))
const instance = { instanceId: '1', name: '测试图床', version: '2' } as StorageInstance
const scope = { scopeId: '3', bucket: 'evidence', version: '4' } as StorageScope
const file: StorageObject = {
  objectKey: '目录/原图.png',
  name: '原图.png',
  sizeBytes: '17761',
  contentType: 'image/png',
  lastModified: '2026-10-22T04:33:41Z',
  etag: 'fresh-etag',
  objectVersionId: null,
  objectToken: 'fresh-token',
}
interface State {
  file: StorageObject | null
  error: unknown
  errorAdvice: string | undefined
  busy: boolean
  previewKind: 'image' | 'video' | null
  contentUrl: string
  exactBytes: string
  mediaFailed: boolean
  load: () => Promise<void>
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
async function mount() {
  const app = renderer.createApp(
    { ...StorageFileDetails, render: () => null },
    {
      instance,
      scope,
      objectKey: file.objectKey,
      downloading: false,
    },
  )
  app.provide(ssrContextKey, { modules: new Set<string>() })
  app.mount({})
  dispose = () => app.unmount()
  await Promise.resolve()
  await nextTick()
  return (app._instance as unknown as { setupState: State }).setupState
}
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}
beforeEach(() => {
  vi.resetAllMocks()
  api.getObject.mockResolvedValue({ ...file })
  api.objectContentUrl.mockReturnValue('/authenticated-image')
})
afterEach(() => dispose())

describe('file detail lifecycle', () => {
  it('reads current metadata and token for the selected scope, including exact byte size', async () => {
    const state = await mount()
    expect(api.getObject).toHaveBeenCalledWith(
      scope.scopeId,
      file.objectKey,
      expect.any(AbortSignal),
    )
    expect(state.file).toEqual(file)
    expect(state.exactBytes).toBe('17,761 字节')
    expect(state.previewKind).toBe('image')
    expect(state.contentUrl).toBe('/authenticated-image')
    expect(api.objectContentUrl).toHaveBeenCalledWith('3', file.objectKey, 'fresh-token')
  })
  it.each(['application/pdf', 'text/plain', 'application/zip', 'text/html', 'image/svg+xml', null])(
    'keeps metadata available for %s without requesting an unsafe or unsupported inline preview',
    async (contentType) => {
      api.getObject.mockResolvedValue({ ...file, contentType })
      const state = await mount()
      expect(state.file?.sizeBytes).toBe('17761')
      expect(state.previewKind).toBeNull()
      expect(state.contentUrl).toBe('')
      expect(api.objectContentUrl).not.toHaveBeenCalled()
    },
  )
  it('uses the video preview for the existing MP4 content contract', async () => {
    api.getObject.mockResolvedValue({ ...file, contentType: 'video/mp4; charset=binary' })
    const state = await mount()
    expect(state.previewKind).toBe('video')
    expect(state.contentUrl).toBe('/authenticated-image')
  })
  it('keeps a large byte count exact without converting it to a floating point integer', async () => {
    api.getObject.mockResolvedValue({ ...file, sizeBytes: '9007199254740993' })
    const state = await mount()
    expect(state.exactBytes).toBe('9,007,199,254,740,993 字节')
  })
  it('refreshes the read token after a media failure and clears the old preview', async () => {
    const state = await mount()
    state.mediaFailed = true
    const next = deferred<StorageObject>()
    api.getObject.mockReturnValueOnce(next.promise)
    const pending = state.load()
    expect(state.file).toBeNull()
    expect(state.busy).toBe(true)
    expect(state.mediaFailed).toBe(false)
    next.resolve({ ...file, objectToken: 'new-token' })
    await pending
    expect(state.file?.objectToken).toBe('new-token')
  })
  it('shows a metadata request failure without leaving a previous file visible', async () => {
    const state = await mount()
    const error = new Error('object no longer exists')
    api.getObject.mockRejectedValueOnce(error)
    await state.load()
    expect(state.file).toBeNull()
    expect(state.error).toBe(error)
    expect(state.busy).toBe(false)
    expect(state.contentUrl).toBe('')
  })
  it('guides an expired file link back to the library without hiding its request diagnostic', async () => {
    const error = new ApiRequestError('NOT_FOUND', '请求的资源不存在', { status: 404 })
    api.getObject.mockRejectedValueOnce(error)
    const state = await mount()
    expect(state.error).toBe(error)
    expect(state.errorAdvice).toContain('关闭详情并刷新文件库')
  })
  it('retains the shared sign-in guidance for an expired session', async () => {
    api.getObject.mockRejectedValueOnce(
      new ApiRequestError('UNAUTHORIZED', '需要登录', { status: 401 }),
    )
    const state = await mount()
    expect(state.errorAdvice).toBeUndefined()
  })
  it('aborts outstanding reads when closed and ignores a late response', async () => {
    const pending = deferred<StorageObject>()
    api.getObject.mockReturnValueOnce(pending.promise)
    const state = await mount()
    const signal = api.getObject.mock.calls[0]![2] as AbortSignal
    dispose()
    dispose = () => {}
    expect(signal.aborted).toBe(true)
    pending.resolve(file)
    await Promise.resolve()
    await nextTick()
    expect(state.file).toBeNull()
  })
  it('ignores an older response after a second metadata read', async () => {
    const old = deferred<StorageObject>()
    api.getObject.mockReturnValueOnce(old.promise)
    const state = await mount()
    await state.load()
    old.resolve({ ...file, etag: 'outdated', objectToken: 'outdated-token' })
    await Promise.resolve()
    await nextTick()
    expect(state.file?.etag).toBe('fresh-etag')
    expect(state.file?.objectToken).toBe('fresh-token')
  })
})
