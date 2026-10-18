// @vitest-environment vue-renderer
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import type { App } from 'vue'
import type { AuthUser } from '../../src/api/auth/types'
import { sessionState } from '../../src/session/state'
import {
  readSavedViews,
  savedViewsKey,
  writeSavedViews,
  type SavedView,
} from '../../src/features/preview/saved-views'
import Panel from '../../src/features/preview/PreviewSavedViews.vue'
import { node, nodes, renderer, text, type Host } from '../support/renderer'

vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    ElInput: defineComponent({
      setup:
        (_, { attrs }) =>
        () =>
          h('input', attrs),
    }),
    ElButton: defineComponent({
      setup:
        (_, { attrs, slots }) =>
        () =>
          h('button', attrs, slots.default?.()),
    }),
    ElPopover: defineComponent({
      setup:
        (_, { attrs, slots }) =>
        () =>
          h('popover', attrs, [slots.reference?.(), slots.default?.()]),
    }),
  }
})
let app: App | null = null
let values: Map<string, string>
let listeners: Record<string, (event: unknown) => void>
const events = {
  open: vi.fn(),
  current: vi.fn(() => ({
    layout: '36' as const,
    slots: [{ index: 35, cameraId: '1', profileId: '12' }],
  })),
}
const saved = (): SavedView => ({
  id: 'v-1',
  name: '旧收藏',
  layout: '36',
  slots: [{ index: 0, cameraId: '1', profileId: '11' }],
  updatedAt: '2026-10-18T00:00:00Z',
})
const button = (root: Host, label: string) =>
  nodes(root).find((item) => item.type === 'button' && text(item) === label)!
async function mount(busy = false) {
  const root = node('root')
  app = renderer.createApp(Panel, { busy, current: events.current, onOpen: events.open })
  app.mount(root)
  await nextTick()
  return root
}
beforeEach(() => {
  vi.clearAllMocks()
  values = new Map()
  listeners = {}
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
  })
  vi.stubGlobal('addEventListener', (name: string, listener: (event: unknown) => void) => {
    listeners[name] = listener
  })
  vi.stubGlobal('removeEventListener', vi.fn())
  sessionState.me = { user: { id: '1' } } as AuthUser
})
afterEach(() => {
  app?.unmount()
  app = null
  sessionState.me = null
  vi.unstubAllGlobals()
})

it('reads without opening, explicitly updates positions, renames, opens and deletes', async () => {
  writeSavedViews('1', [saved()])
  const root = await mount()
  expect(events.open).not.toHaveBeenCalled()
  expect(events.current).not.toHaveBeenCalled()
  await (button(root, '更新画面').props.onClick as () => void)()
  await nextTick()
  expect(readSavedViews('1')[0]!.slots).toEqual([{ index: 35, cameraId: '1', profileId: '12' }])
  const name = nodes(root).find((item) => item.props['aria-label'] === '旧收藏 的名称')!
  ;(name.props['onUpdate:modelValue'] as (value: string) => void)('巡看六屏')
  await (button(root, '重命名').props.onClick as () => void)()
  await nextTick()
  expect(readSavedViews('1')[0]!.name).toBe('巡看六屏')
  await (button(root, '打开').props.onClick as () => void)()
  expect(events.open).toHaveBeenCalledWith(expect.objectContaining({ name: '巡看六屏' }))
  await (button(root, '删除').props.onClick as () => void)()
  await nextTick()
  expect(readSavedViews('1')).toEqual([])
  expect(text(root)).toContain('还没有收藏')
})
it('saves only on explicit submit and reports storage failure without emitting open', async () => {
  const root = await mount()
  const name = nodes(root).find((item) => item.props['aria-label'] === '新收藏名称')!
  ;(name.props['onUpdate:modelValue'] as (value: string) => void)('巡看')
  expect(readSavedViews('1')).toEqual([])
  const submit = nodes(root).find((item) => item.type === 'form')!
  await (submit.props.onSubmit as (event: unknown) => void)({ preventDefault: vi.fn() })
  await nextTick()
  expect(readSavedViews('1')).toHaveLength(1)
  vi.stubGlobal('localStorage', {
    getItem: () => {
      throw new Error('存储被禁用')
    },
  })
  await (submit.props.onSubmit as (event: unknown) => void)({ preventDefault: vi.fn() })
  await nextTick()
  expect(text(root)).toContain('存储被禁用')
  expect(text(root)).toContain('现有预览不受影响')
  expect(events.open).not.toHaveBeenCalled()
})
it('reacts to storage and identity changes without starting streams and disables actions while opening', async () => {
  const root = await mount(true)
  writeSavedViews('1', [saved()])
  listeners.storage?.({ key: savedViewsKey('1') })
  await nextTick()
  expect(nodes(root).some((item) => item.props['aria-label'] === '旧收藏 的名称')).toBe(true)
  expect(button(root, '打开').props.disabled).toBe(true)
  expect(button(root, '收藏视图').props.disabled).toBe(true)
  await (button(root, '打开').props.onClick as () => void)()
  await (button(root, '删除').props.onClick as () => void)()
  expect(readSavedViews('1')).toHaveLength(1)
  expect(events.open).not.toHaveBeenCalled()
  sessionState.me = { user: { id: '2' } } as AuthUser
  await nextTick()
  expect(nodes(root).some((item) => item.props['aria-label'] === '旧收藏 的名称')).toBe(false)
  expect(text(root)).toContain('还没有收藏')
  expect(events.open).not.toHaveBeenCalled()
})
