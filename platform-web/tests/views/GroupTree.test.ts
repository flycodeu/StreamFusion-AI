// @vitest-environment vue-renderer
import { describe, expect, it, vi } from 'vitest'
import { node, renderer, text } from '../support/renderer'
import GroupTree from '../../src/features/camera-groups/GroupTree.vue'
import type { CameraGroup } from '../../src/api/camera-groups/types'

vi.mock('element-plus', async () => {
  const { defineComponent, h } = await import('vue')
  const control = defineComponent({
    setup(_, { attrs, slots }) {
      return () => h('control', attrs, slots.default?.())
    },
  })
  return {
    ElButton: control,
    ElInput: control,
    ElTree: defineComponent({
      props: ['data'],
      setup(props, { slots }) {
        return () =>
          h(
            'tree',
            props.data.map((data: unknown) => slots.default?.({ data })),
          )
      },
    }),
  }
})

vi.mock('../../src/features/camera-groups/GroupNodeActions.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    default: defineComponent({
      setup:
        (_, { slots }) =>
        () =>
          h('actions', slots.default?.()),
    }),
  }
})

const group: CameraGroup = {
  groupId: '1',
  parentId: null,
  name: '厂区',
  sortOrder: 0,
  remark: null,
  version: '0',
  hasChildren: false,
  visibleCameraCount: 16,
  visibleDeviceCount: 1,
  countObservedAt: null,
}

describe('management group device counts', () => {
  it.each([false, true])('shows one NVR as one camera (manageable: %s)', (manageable) => {
    const root = node('root')
    const app = renderer.createApp(GroupTree, { groups: [group], modelValue: null, manageable })
    app.mount(root)
    expect(text(root)).toContain('1 台相机')
    expect(text(root)).not.toContain('16 通道')
    app.unmount()
  })

  it('does not present the channel count as a device count when unavailable', () => {
    const root = node('root')
    const app = renderer.createApp(GroupTree, {
      groups: [{ ...group, visibleDeviceCount: undefined }],
      modelValue: null,
      placements: [{ groupId: '1', channelCount: 16 }],
    })
    app.mount(root)
    expect(text(root)).toContain('— 台相机')
    expect(text(root)).toContain('16 通道')
    expect(text(root)).not.toContain('16 台相机')
    app.unmount()
  })
})
