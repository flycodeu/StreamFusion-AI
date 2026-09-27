// @vitest-environment vue-renderer
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import type { App } from 'vue'
import { node, nodes, renderer, text } from '../support/renderer'
import type { Host } from '../support/renderer'
import { clearIdentity } from '../../src/session/state'
import ApiDocsView from '../../src/views/monitor/ApiDocs.vue'

const api = vi.hoisted(() => ({ status: vi.fn(), document: vi.fn() }))
vi.mock('../../src/api/api-docs/api', () => ({
  apiDocsFrameUrl: () => '/api-docs.html?apiPrefix=%2Fapi',
  getApiDocsStatus: api.status,
  getOpenApiDocument: api.document,
}))
vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    vLoading: {},
    ElButton: defineComponent({
      setup(_, { attrs, slots }) {
        return () => h('button', attrs, slots.default?.())
      },
    }),
    ElEmpty: defineComponent({
      setup(_, { attrs }) {
        return () => h('empty', attrs)
      },
    }),
  }
})

const button = (root: Host, label: string) =>
  nodes(root).find((item) => item.type === 'button' && text(item).includes(label))!
let app: App | null = null
async function mount() {
  const root = node('root')
  app = renderer.createApp(ApiDocsView)
  app.mount(root)
  await vi.waitFor(() => expect(api.status).toHaveBeenCalledOnce())
  await nextTick()
  return root
}
beforeEach(() => {
  vi.clearAllMocks()
  clearIdentity()
  api.status.mockResolvedValue({ enabled: true })
})
afterEach(() => {
  app?.unmount()
  app = null
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('API documentation toolbar', () => {
  it('keeps only compact actions and the fixed document frame', async () => {
    const root = await mount()
    expect(nodes(root).some((item) => item.type === 'h1')).toBe(false)
    expect(button(root, '导出 OpenAPI JSON').props.disabled).toBe(false)
    expect(nodes(root).find((item) => item.type === 'iframe')?.props.src).toBe(
      '/api-docs.html?apiPrefix=%2Fapi',
    )
  })

  it('keeps export disabled when documentation is not enabled', async () => {
    api.status.mockResolvedValue({ enabled: false })
    const root = await mount()
    expect(button(root, '导出 OpenAPI JSON').props.disabled).toBe(true)
    expect(nodes(root).some((item) => item.type === 'iframe')).toBe(false)
    await (button(root, '导出 OpenAPI JSON').props.onClick as () => Promise<void>)()
    expect(api.document).not.toHaveBeenCalled()
  })

  it('downloads JSON with a stable filename and releases the temporary URL', async () => {
    const root = await mount()
    const content = '{"openapi":"3.1.0","paths":{}}'
    api.document.mockResolvedValue(content)
    const link = { href: '', download: '', click: vi.fn(), remove: vi.fn() }
    vi.stubGlobal('document', { createElement: () => link, body: { append: vi.fn() } })
    const create = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:local-document')
    const revoke = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
    await (button(root, '导出 OpenAPI JSON').props.onClick as () => Promise<void>)()
    expect(link.download).toBe('streamfusion-openapi.json')
    expect(link.click).toHaveBeenCalledOnce()
    expect(link.remove).toHaveBeenCalledOnce()
    expect(await (create.mock.calls[0]?.[0] as Blob).text()).toBe(content)
    await vi.waitFor(() => expect(revoke).toHaveBeenCalledWith('blob:local-document'))
  })

  it.each(['unmount', 'identity change'])('discards a delayed export after %s', async (change) => {
    const root = await mount()
    let finish!: (value: string) => void
    api.document.mockReturnValue(
      new Promise<string>((resolve) => {
        finish = resolve
      }),
    )
    const create = vi.spyOn(URL, 'createObjectURL')
    const click = button(root, '导出 OpenAPI JSON').props.onClick as () => Promise<void>
    const pending = click()
    await click()
    expect(api.document).toHaveBeenCalledOnce()
    if (change === 'unmount') app?.unmount()
    else clearIdentity()
    finish('{"openapi":"3.1.0","paths":{}}')
    await pending
    expect(create).not.toHaveBeenCalled()
    if (change === 'unmount') expect(api.document.mock.calls[0]?.[0].aborted).toBe(true)
  })
})
