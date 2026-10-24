// @vitest-environment vue-renderer
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import type { App } from 'vue'
import { node, nodes, renderer, text } from '../../../tests/support/renderer'
import type { Host } from '../../../tests/support/renderer'
import PlanTargetSettings from './PlanTargetSettings.vue'
import { clearIdentity } from '../../session/state'

const api = vi.hoisted(() => ({
  getCamera: vi.fn(),
  getCameraStatus: vi.fn(),
  getDeviceChannels: vi.fn(),
}))
vi.mock('../../api/camera/api', () => api)
vi.mock('../../api/camera-status/api', () => api)
vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  return Object.fromEntries(
    ['ElButton', 'ElSelect', 'ElOption', 'ElPagination', 'ElAlert'].map((name) => [
      name,
      defineComponent({
        inheritAttrs: false,
        setup(_, { attrs, slots }) {
          return () => h(name, attrs, slots.default?.())
        },
      }),
    ]),
  )
})

const target = {
  groupKey: 'd7',
  name: '录像机',
  reasonCode: null,
  cameraId: '60',
  channelName: '通道60',
  streamProfileId: '600',
  profileName: '主码流',
  videoSupported: true,
}
function camera(id: string) {
  return {
    cameraId: id,
    name: `通道${id}`,
    profiles: [
      { streamProfileId: `${id}0`, label: '主码流', usageHint: 'MAIN', enabled: true },
      { streamProfileId: `${id}1`, label: '子码流', usageHint: 'SUB', enabled: true },
    ],
  }
}
let app: App
let root: Host
const changed = vi.fn()
async function settle() {
  for (let index = 0; index < 8; index++) {
    await Promise.resolve()
    await nextTick()
  }
}
async function mount() {
  root = node('root')
  app = renderer.createApp(PlanTargetSettings, {
    targets: [target],
    disabled: false,
    onChange: changed,
  })
  app.mount(root)
  await settle()
}
const button = (label: string) =>
  nodes(root).find((item) => item.type === 'ElButton' && text(item) === label)!
const select = (label: string) =>
  nodes(root).find((item) => item.type === 'ElSelect' && item.props['aria-label'] === label)!
async function click(label: string) {
  await (button(label).props.onClick as () => unknown)()
  await settle()
}
async function channel(id: string) {
  const control = select('巡检检测通道')
  ;(control.props['onUpdate:modelValue'] as (value: string) => void)(id)
  ;(control.props.onChange as () => void)()
  await settle()
}
beforeEach(() => {
  vi.resetAllMocks()
  clearIdentity()
  api.getDeviceChannels.mockResolvedValue({ items: [camera('1')], page: 1, total: 60 })
  api.getCamera.mockImplementation(async (id) => camera(id))
  api.getCameraStatus.mockImplementation(async (id) => ({
    profiles: [{ streamProfileId: `${id}1` }, { streamProfileId: `${id}0` }],
  }))
})
afterEach(() => app?.unmount())

describe('per-device scheduled stream selection', () => {
  it('retains the current channel outside the first page and discards cancelled drafts', async () => {
    await mount()
    await click('调整')
    expect(
      nodes(root).some(
        (item) =>
          item.type === 'ElOption' && item.props.value === '60' && item.props.label === '通道60',
      ),
    ).toBe(true)
    expect(select('巡检检测码流').props.modelValue).toBe('600')
    expect(button('加载更多通道')).toBeDefined()
    await channel('1')
    expect(select('巡检检测码流').props.modelValue).toBe('11')
    await click('取消调整')
    expect(changed).not.toHaveBeenCalled()
    await click('调整')
    expect(select('巡检检测码流').props.modelValue).toBe('600')
  })
  it('ignores an old channel response after choosing another channel', async () => {
    await mount()
    await click('调整')
    let finish!: (value: unknown) => void
    api.getCamera.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve
        }),
    )
    await channel('1')
    await channel('2')
    expect(select('巡检检测码流').props.modelValue).toBe('21')
    finish(camera('1'))
    await settle()
    expect(select('巡检检测码流').props.modelValue).toBe('21')
    await click('应用')
    expect(changed).toHaveBeenCalledWith({ groupKey: 'd7', cameraId: '2', streamProfileId: '21' })
  })
  it('blocks failed reads, preserves the configured profile on retry, and clears edits on logout', async () => {
    api.getCameraStatus.mockRejectedValueOnce(new Error('unavailable'))
    await mount()
    await click('调整')
    expect(button('应用').props.disabled).toBe(true)
    expect(changed).not.toHaveBeenCalled()
    await click('重新加载码流')
    expect(select('巡检检测码流').props.modelValue).toBe('600')
    clearIdentity()
    await settle()
    expect(button('应用')).toBeUndefined()
    expect(changed).not.toHaveBeenCalled()
  })
})
