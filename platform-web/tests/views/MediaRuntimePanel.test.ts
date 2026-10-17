// @vitest-environment vue-renderer
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import type { App } from 'vue'
import { node, nodes, renderer, text } from '../support/renderer'
import type { Host } from '../support/renderer'
import RuntimePanel from '../../src/features/media-runtime/RuntimePanel.vue'
import type { MediaRuntimeSnapshot } from '../../src/api/media-runtime/types'

const api = vi.hoisted(() => ({
  getMediaRuntime: vi.fn(),
  detectMediaRuntime: vi.fn(),
  selectMediaRuntime: vi.fn(),
  success: vi.fn(),
}))
vi.mock('../../src/api/media-runtime/api', () => api)
vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    ...Object.fromEntries(
      ['ElDialog', 'ElButton', 'ElEmpty', 'ElOption', 'ElSelect', 'ElTag'].map((name) => [
        name,
        defineComponent({
          inheritAttrs: false,
          setup(_, { attrs, slots }) {
            return () => h(name, attrs, slots.default?.())
          },
        }),
      ]),
    ),
    ElMessage: { success: api.success },
    vLoading: {},
  }
})
let app: App | null = null
function snapshot(): MediaRuntimeSnapshot {
  return {
    os: 'WINDOWS',
    version: '2',
    checkedAt: '2026-10-13T06:00:00Z',
    selected: { zlmId: 'zlm-current', ffmpegId: 'ffmpeg-current' },
    scanDiagnostics: [],
    candidates: ['ZLM', 'FFMPEG'].map((kind) => ({
      id: `${kind.toLowerCase()}-current`,
      kind: kind as 'ZLM' | 'FFMPEG',
      executablePath: `D:/Tools/${kind}/program.exe`,
      configPath: null,
      apiBase: null,
      softwareVersion: 'test-version',
      status: 'READY',
      processRunning: kind === 'ZLM',
      pids: kind === 'ZLM' ? [7] : [],
      ports: [],
      checkedAt: '2026-10-13T06:00:00Z',
      diagnostic: null,
    })),
  }
}
async function settle() {
  for (let i = 0; i < 6; i++) await nextTick()
}
async function mount() {
  const root = node('root')
  app = renderer.createApp(RuntimePanel)
  app.mount(root)
  await settle()
  return root
}
function button(root: Host, label: string) {
  return nodes(root).find((item) => item.type === 'ElButton' && text(item) === label)!
}
async function choose(root: Host, index: number, value: string | null) {
  const select = nodes(root).filter((item) => item.type === 'ElSelect')[index]!
  ;(select.props['onUpdate:modelValue'] as (value: string | null) => void)(value)
  await settle()
}
beforeEach(() => {
  vi.clearAllMocks()
  api.getMediaRuntime.mockResolvedValue(snapshot())
  api.selectMediaRuntime.mockImplementation(async (_version, selected) => ({
    ...snapshot(),
    selected,
  }))
})
afterEach(() => {
  app?.unmount()
  app = null
  vi.useRealTimers()
})
describe('media runtime selection and display', () => {
  it('retains a choice edited while a background runtime read is pending', async () => {
    vi.useFakeTimers()
    const root = await mount()
    let complete!: (value: MediaRuntimeSnapshot) => void
    api.getMediaRuntime.mockReturnValueOnce(
      new Promise((resolve) => {
        complete = resolve
      }),
    )
    await vi.advanceTimersByTimeAsync(30000)
    expect(api.getMediaRuntime).toHaveBeenCalledTimes(2)
    await choose(root, 1, null)
    complete(snapshot())
    await settle()
    expect(nodes(root).filter((item) => item.type === 'ElSelect')[1]!.props.modelValue).toBeNull()
    expect(button(root, '保存选择').props.disabled).toBe(false)
    await (button(root, '保存选择').props.onClick as () => Promise<void>)()
    expect(api.selectMediaRuntime).toHaveBeenCalledWith('2', {
      zlmId: 'zlm-current',
      ffmpegId: null,
    })
  })
  it('shows an available idle FFmpeg as on demand, not an offline service', async () => {
    const root = await mount()
    expect(text(root)).toContain('调用状态')
    expect(text(root)).toContain('按需运行（当前空闲）')
    expect(text(root)).not.toContain('未运行')
    expect(api.selectMediaRuntime).not.toHaveBeenCalled()
  })
  it('namespaces the teleported multiline dropdown and constrains its width', async () => {
    const root = await mount()
    for (const select of nodes(root).filter((item) => item.type === 'ElSelect')) {
      expect(select.props['popper-class']).toBe('media-runtime-options')
      expect(select.props['fit-input-width']).toBe(true)
    }
  })
  it('does not call an unavailable FFmpeg idle or ready', async () => {
    const result = snapshot()
    result.candidates[1]!.status = 'UNAVAILABLE'
    api.getMediaRuntime.mockResolvedValue(result)
    const root = await mount()
    expect(text(root)).toContain('不可用')
    expect(text(root)).toContain('暂无进程')
    expect(text(root)).not.toContain('当前空闲')
  })
  it('marks an undiscovered saved choice and prevents submitting it invisibly', async () => {
    const result = snapshot()
    result.selected.ffmpegId = 'ffmpeg-missing'
    api.getMediaRuntime.mockResolvedValue(result)
    const root = await mount()
    await choose(root, 0, 'zlm-current')
    expect(text(root)).toContain('选择已失效')
    expect(text(root)).toContain('重新选择或清空后保存')
    expect(button(root, '保存选择').props.disabled).toBe(true)
    await (button(root, '保存选择').props.onClick as () => Promise<void>)()
    expect(api.selectMediaRuntime).not.toHaveBeenCalled()
  })
  it('normalizes a cleared stale selection to null and saves only explicit changes', async () => {
    const result = snapshot()
    result.selected.ffmpegId = 'ffmpeg-missing'
    api.getMediaRuntime.mockResolvedValue(result)
    const root = await mount()
    await choose(root, 1, '')
    expect(button(root, '保存选择').props.disabled).toBe(false)
    await (button(root, '保存选择').props.onClick as () => Promise<void>)()
    expect(api.selectMediaRuntime).toHaveBeenCalledWith('2', {
      zlmId: 'zlm-current',
      ffmpegId: null,
    })
  })
  it('preserves an explicit unsaved choice through manual redetection', async () => {
    const root = await mount()
    await choose(root, 1, null)
    api.detectMediaRuntime.mockResolvedValue(snapshot())
    await (button(root, '重新检测').props.onClick as () => Promise<void>)()
    expect(nodes(root).filter((item) => item.type === 'ElSelect')[1]!.props.modelValue).toBeNull()
    expect(api.selectMediaRuntime).not.toHaveBeenCalled()
  })
})
