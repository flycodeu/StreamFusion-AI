// @vitest-environment vue-renderer
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import type { App } from 'vue'
import { node, nodes, renderer } from '../support/renderer'
import type { Host } from '../support/renderer'
import GroupGrantTree from '../../src/features/camera-scopes/GroupGrantTree.vue'

const api = vi.hoisted(() => ({ getScopeGroupTree: vi.fn(), getScopeCameras: vi.fn() }))
vi.mock('../../src/api/camera-scopes/api', () => api)
vi.mock('../../src/composables/usePageScope', () => ({ usePageScope: () => () => () => true }))
vi.mock('../../src/components/feedback/RequestError.vue', () => ({
  default: { render: () => null },
}))
vi.mock('element-plus', async () => {
  const { defineComponent, h, ref, watch } = await import('vue')
  interface TreeData {
    key: string
    children: TreeData[]
  }
  interface TreeNode {
    expanded: boolean
    checked: boolean
    indeterminate: boolean
    data: TreeData
    parent?: TreeNode
    expand: (callback?: unknown, autoExpandParent?: boolean) => void
    collapse: () => void
  }
  const controls = Object.fromEntries(
    ['ElButton', 'ElCheckbox', 'ElInput', 'ElTag'].map((name) => [
      name,
      defineComponent({
        inheritAttrs: false,
        setup(_, { attrs, slots }) {
          return () => h(name, attrs, slots.default?.())
        },
      }),
    ]),
  )
  return {
    ...controls,
    vLoading: {},
    ElTree: defineComponent({
      props: ['data', 'defaultExpandedKeys', 'autoExpandParent'],
      emits: ['node-expand', 'node-collapse', 'check'],
      setup(props, { emit, expose, slots }) {
        const treeNodes = ref<Record<string, TreeNode>>({})
        function expandDefaults() {
          for (const key of props.defaultExpandedKeys ?? []) {
            treeNodes.value[key]?.expand(undefined, props.autoExpandParent)
          }
        }
        // Element Plus rebuilds its node objects when the root data array changes.
        watch(
          () => props.data,
          (data: TreeData[]) => {
            const next: Record<string, TreeNode> = {}
            const visit = (items: TreeData[], parent?: TreeNode) => {
              for (const item of items) {
                const current: TreeNode = {
                  expanded: false,
                  checked: false,
                  indeterminate: false,
                  data: item,
                  parent,
                  expand(_callback, autoExpandParent = false) {
                    treeNodes.value[item.key]!.expanded = true
                    if (autoExpandParent) parent?.expand(undefined, true)
                  },
                  collapse() {
                    treeNodes.value[item.key]!.expanded = false
                  },
                }
                next[item.key] = current
                visit(item.children, current)
              }
            }
            visit(data ?? [])
            treeNodes.value = next
            expandDefaults()
          },
          { immediate: true },
        )
        watch(() => props.defaultExpandedKeys, expandDefaults)
        expose({
          getNode: (key: string) => treeNodes.value[key],
          setCheckedKeys: (keys: string[]) => {
            for (const [key, item] of Object.entries(treeNodes.value))
              item.checked = keys.includes(key)
          },
          filter: (value: string) => {
            if (value) {
              for (const item of Object.values(treeNodes.value)) {
                if (item.data.children.length) item.expand()
              }
            }
          },
        })
        return () =>
          h(
            'ElTree',
            {},
            Object.values(treeNodes.value).map((item) =>
              h(
                'TreeNode',
                {
                  groupKey: item.data.key,
                  expanded: item.expanded,
                  checked: item.checked,
                  indeterminate: item.indeterminate,
                  onCheck: (checked: boolean) =>
                    emit('check', item.data, { checkedKeys: checked ? [item.data.key] : [] }),
                  onExpand: () => {
                    item.expand()
                    emit('node-expand', item.data)
                  },
                  onCollapse: () => {
                    item.collapse()
                    emit('node-collapse', item.data)
                  },
                },
                slots.default?.({ data: item.data }),
              ),
            ),
          )
      },
    }),
  }
})

let app: App | undefined
async function settle() {
  for (let index = 0; index < 8; index++) await nextTick()
}
function treeNode(root: Host, key: string) {
  const result = nodes(root).find(
    (entry) => entry.type === 'TreeNode' && entry.props.groupKey === key,
  )
  if (!result) throw new Error(`missing tree node ${key}`)
  return result
}
function fire(target: Host, event: string, value?: unknown) {
  ;(target.props[event] as (value?: unknown) => void)(value)
}
async function mount(props: Record<string, unknown> = {}) {
  const root = node('root')
  app = renderer.createApp(GroupGrantTree, {
    modelValue: [],
    cameraIds: [],
    disabled: false,
    ...props,
  })
  app.mount(root)
  await settle()
  return root
}
beforeEach(() => {
  vi.clearAllMocks()
  api.getScopeGroupTree.mockResolvedValue([
    {
      groupId: '1',
      parentId: null,
      name: '厂区',
      sortOrder: 0,
      version: '0',
      visibleCameraCount: 1,
    },
    {
      groupId: '2',
      parentId: '1',
      name: '车间',
      sortOrder: 0,
      version: '0',
      visibleCameraCount: 1,
    },
    {
      groupId: '3',
      parentId: '2',
      name: '产线',
      sortOrder: 0,
      version: '0',
      visibleCameraCount: 1,
    },
  ])
  api.getScopeCameras.mockResolvedValue({ items: [], total: 0 })
})
afterEach(() => {
  app?.unmount()
  app = undefined
})

describe('grant tree expansion', () => {
  it('keeps a group open when loading its cameras rebuilds the tree', async () => {
    const root = await mount()
    let resolve!: (value: unknown) => void
    api.getScopeCameras.mockReturnValueOnce(
      new Promise((done) => {
        resolve = done
      }),
    )
    fire(treeNode(root, '1'), 'onExpand')
    await settle()
    expect(treeNode(root, '1').props.expanded).toBe(true)
    resolve({
      items: [{ cameraId: '10', name: '炉前', groupId: '1', path: '厂区', lifecycle: 'ENABLED' }],
      total: 1,
    })
    await settle()
    expect(treeNode(root, 'camera:10')).toBeDefined()
    expect(treeNode(root, '1').props.expanded).toBe(true)
  })

  it('keeps a collapsed parent closed even when its child expansion is remembered', async () => {
    const root = await mount()
    fire(treeNode(root, '1'), 'onExpand')
    fire(treeNode(root, '2'), 'onExpand')
    await settle()
    fire(treeNode(root, '1'), 'onCollapse')
    await settle()
    expect(treeNode(root, '1').props.expanded).toBe(false)
    expect(treeNode(root, '2').props.expanded).toBe(true)
  })

  it('restores manual expansion after clearing a search that expands matching groups', async () => {
    const root = await mount()
    fire(treeNode(root, '1'), 'onExpand')
    await settle()
    const input = nodes(root).find((entry) => entry.type === 'ElInput')!
    fire(input, 'onUpdate:modelValue', '产线')
    await settle()
    expect(treeNode(root, '2').props.expanded).toBe(true)
    fire(input, 'onUpdate:modelValue', '')
    await settle()
    expect(treeNode(root, '1').props.expanded).toBe(true)
    expect(treeNode(root, '2').props.expanded).toBe(false)
  })
})

const initialCamera = {
  cameraId: '10',
  name: '炉前',
  groupId: '1',
  path: '厂区',
  lifecycle: 'ENABLED',
}
describe('additive grant editing', () => {
  it('excludes a deeply nested group without retaining an ancestor that grants it again', async () => {
    const base = await api.getScopeGroupTree()
    api.getScopeGroupTree.mockResolvedValue([
      ...base,
      { ...base[0], groupId: '4', parentId: '2', name: '另一产线' },
      { ...base[0], groupId: '5', parentId: '1', name: '另一车间' },
    ])
    const groupsChanged = vi.fn(),
      camerasChanged = vi.fn()
    const root = await mount({
      modelValue: ['1'],
      'onUpdate:modelValue': groupsChanged,
      'onUpdate:cameraIds': camerasChanged,
    })
    api.getScopeCameras.mockResolvedValue({
      items: [initialCamera, { ...initialCamera, cameraId: '30', groupId: '3' }],
      total: 2,
    })
    fire(treeNode(root, '3'), 'onCheck', false)
    await settle()
    expect(groupsChanged).toHaveBeenCalledExactlyOnceWith(['4', '5'])
    expect(camerasChanged).toHaveBeenCalledExactlyOnceWith(['10'])
  })

  it('does not restore grants from a delayed inherited removal after the parent is unchecked', async () => {
    let finish!: (value: unknown) => void
    const groupsChanged = vi.fn()
    const root = await mount({ modelValue: ['1'], 'onUpdate:modelValue': groupsChanged })
    api.getScopeCameras.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve
        }),
    )
    fire(treeNode(root, '3'), 'onCheck', false)
    fire(treeNode(root, '1'), 'onCheck', false)
    await settle()
    expect(groupsChanged).toHaveBeenCalledExactlyOnceWith([])
    finish({ items: [initialCamera], total: 1 })
    await settle()
    expect(groupsChanged).toHaveBeenCalledExactlyOnceWith([])
  })

  it.each(['failed', 'oversized'])(
    'preserves the original grant range when inherited removal is %s',
    async (outcome) => {
      const groupsChanged = vi.fn(),
        camerasChanged = vi.fn(),
        devicesChanged = vi.fn()
      const root = await mount({
        modelValue: ['1'],
        'onUpdate:modelValue': groupsChanged,
        'onUpdate:cameraIds': camerasChanged,
        'onUpdate:deviceIds': devicesChanged,
      })
      if (outcome === 'failed')
        api.getScopeCameras.mockRejectedValueOnce(new Error('network unavailable'))
      else api.getScopeCameras.mockResolvedValueOnce({ items: [initialCamera], total: 2001 })
      fire(treeNode(root, '3'), 'onCheck', false)
      await settle()
      expect(groupsChanged).not.toHaveBeenCalled()
      expect(camerasChanged).not.toHaveBeenCalled()
      expect(devicesChanged).not.toHaveBeenCalled()
    },
  )
  it('never changes grants when expanding an already granted group', async () => {
    const groupsChanged = vi.fn(),
      camerasChanged = vi.fn()
    const root = await mount({
      modelValue: ['1'],
      'onUpdate:modelValue': groupsChanged,
      'onUpdate:cameraIds': camerasChanged,
    })
    api.getScopeCameras.mockResolvedValueOnce({ items: [initialCamera], total: 1 })
    fire(treeNode(root, '1'), 'onExpand')
    await settle()
    expect(treeNode(root, 'camera:10')).toBeDefined()
    expect(groupsChanged).not.toHaveBeenCalled()
    expect(camerasChanged).not.toHaveBeenCalled()
  })

  it('adds only a group grant when checking a group', async () => {
    const groupsChanged = vi.fn(),
      camerasChanged = vi.fn()
    const root = await mount({
      initialCameras: [initialCamera],
      'onUpdate:modelValue': groupsChanged,
      'onUpdate:cameraIds': camerasChanged,
    })
    fire(treeNode(root, '1'), 'onCheck', true)
    await settle()
    expect(groupsChanged).toHaveBeenCalledExactlyOnceWith(['1'])
    expect(camerasChanged).not.toHaveBeenCalled()
  })

  it('allows inherited camera and child-group removal by materializing the remaining scope', async () => {
    const groupsChanged = vi.fn(),
      camerasChanged = vi.fn()
    const root = await mount({
      modelValue: ['1'],
      cameraIds: ['999'],
      initialCameras: [initialCamera],
      'onUpdate:modelValue': groupsChanged,
      'onUpdate:cameraIds': camerasChanged,
    })
    const checkbox = nodes(root).find(
      (entry) => entry.type === 'ElCheckbox' && entry.props['aria-label'] === '选择 炉前',
    )!
    expect(checkbox.props.disabled).toBe(false)
    fire(checkbox, 'onUpdate:modelValue', false)
    fire(treeNode(root, '2'), 'onCheck', false)
    await settle()
    expect(groupsChanged).toHaveBeenCalled()
    expect(camerasChanged).toHaveBeenCalled()
  })

  it('removes only the explicit group, retaining separate direct and subgroup grants', async () => {
    const groupsChanged = vi.fn(),
      camerasChanged = vi.fn()
    const root = await mount({
      modelValue: ['1', '2'],
      cameraIds: ['10', '999'],
      initialCameras: [initialCamera],
      'onUpdate:modelValue': groupsChanged,
      'onUpdate:cameraIds': camerasChanged,
    })
    fire(treeNode(root, '1'), 'onCheck', false)
    await settle()
    expect(groupsChanged).toHaveBeenCalledExactlyOnceWith(['2'])
    expect(camerasChanged).not.toHaveBeenCalled()
  })

  it('keeps a group partially checked when all currently loaded cameras have direct grants', async () => {
    const root = await mount({ cameraIds: ['10'], initialCameras: [initialCamera] })
    expect(treeNode(root, '1').props.checked).toBe(false)
    expect(treeNode(root, '1').props.indeterminate).toBe(true)
  })
})

describe('paged group candidates', () => {
  it('can load and grant an ungrouped device beyond the first global page', async () => {
    api.getScopeCameras.mockResolvedValueOnce({
      items: Array.from({ length: 100 }, (_, index) => ({
        ...initialCamera,
        cameraId: String(index + 10),
      })),
      total: 101,
    })
    const devicesChanged = vi.fn()
    const root = await mount({ 'onUpdate:deviceIds': devicesChanged })
    api.getScopeCameras.mockResolvedValueOnce({
      items: [
        {
          ...initialCamera,
          cameraId: '300',
          deviceId: '90',
          name: '未分组 NVR',
          groupId: null,
          groupIds: [],
          path: null,
        },
      ],
      total: 101,
    })
    const moreButton = nodes(treeNode(root, 'action:')).find((entry) => entry.type === 'ElButton')!
    fire(moreButton, 'onClick', { stopPropagation() {} })
    await settle()
    expect(api.getScopeCameras).toHaveBeenLastCalledWith({ page: 2, size: 100 })
    expect(treeNode(root, 'device:90')).toBeDefined()
    const checkbox = nodes(root).find(
      (entry) => entry.type === 'ElCheckbox' && entry.props['aria-label'] === '选择 未分组 NVR',
    )!
    fire(checkbox, 'onUpdate:modelValue', true)
    await settle()
    expect(devicesChanged).toHaveBeenCalledExactlyOnceWith(['90'])
    expect(nodes(root).some((entry) => entry.props.groupKey === 'action:')).toBe(false)
  })

  it('loads only one page on expansion and retries the next page without losing earlier cameras', async () => {
    const root = await mount()
    const firstPage = Array.from({ length: 100 }, (_, index) => ({
      ...initialCamera,
      cameraId: String(index + 10),
      name: `相机 ${index}`,
    }))
    api.getScopeCameras.mockResolvedValueOnce({ items: firstPage, total: 101 })
    fire(treeNode(root, '1'), 'onExpand')
    await settle()
    expect(api.getScopeCameras).toHaveBeenLastCalledWith({ page: 1, size: 100, groupId: '1' })
    expect(api.getScopeCameras).toHaveBeenCalledTimes(2)
    const moreButton = () =>
      nodes(treeNode(root, 'action:1')).find((entry) => entry.type === 'ElButton')!
    api.getScopeCameras.mockRejectedValueOnce(new Error('network unavailable'))
    fire(moreButton(), 'onClick', { stopPropagation() {} })
    await settle()
    expect(api.getScopeCameras).toHaveBeenLastCalledWith({ page: 2, size: 100, groupId: '1' })
    expect(treeNode(root, 'camera:10')).toBeDefined()
    api.getScopeCameras.mockResolvedValueOnce({
      items: [{ ...initialCamera, cameraId: '110' }],
      total: 101,
    })
    fire(moreButton(), 'onClick', { stopPropagation() {} })
    await settle()
    expect(api.getScopeCameras).toHaveBeenLastCalledWith({ page: 2, size: 100, groupId: '1' })
    expect(treeNode(root, 'camera:110')).toBeDefined()
    expect(nodes(root).some((entry) => entry.props.groupKey === 'action:1')).toBe(false)
    expect(treeNode(root, '1').props.expanded).toBe(true)
  })
})

describe('device-level grants', () => {
  const recorder = {
    ...initialCamera,
    name: '厂区 NVR',
    deviceId: '90',
    groupIds: ['1', '3'],
  }

  it('removes every inherited path and the direct grant when excluding a cross-group device', async () => {
    const base = await api.getScopeGroupTree()
    api.getScopeGroupTree.mockResolvedValue([
      ...base,
      { ...base[0], groupId: '9', parentId: null, name: '另一厂区' },
    ])
    const groupsChanged = vi.fn(),
      devicesChanged = vi.fn()
    const both = { ...recorder, groupIds: ['3', '9'] }
    const root = await mount({
      modelValue: ['1', '9'],
      deviceIds: ['90', '99'],
      initialCameras: [both],
      'onUpdate:modelValue': groupsChanged,
      'onUpdate:deviceIds': devicesChanged,
    })
    api.getScopeCameras.mockResolvedValue({ items: [both], total: 1 })
    const checkbox = nodes(root).find(
      (entry) => entry.type === 'ElCheckbox' && entry.props['aria-label'] === '选择 厂区 NVR',
    )!
    fire(checkbox, 'onUpdate:modelValue', false)
    await settle()
    expect(groupsChanged).toHaveBeenCalledExactlyOnceWith([])
    expect(devicesChanged).toHaveBeenCalledExactlyOnceWith(['99'])
  })

  it('merges channels of the same device and selects the stable device identity', async () => {
    const devicesChanged = vi.fn(),
      camerasChanged = vi.fn()
    const root = await mount({
      initialCameras: [recorder, { ...recorder, cameraId: '11' }],
      'onUpdate:deviceIds': devicesChanged,
      'onUpdate:cameraIds': camerasChanged,
    })
    expect(treeNode(root, 'device:1:90')).toBeDefined()
    expect(treeNode(root, 'device:3:90')).toBeDefined()
    const checkboxes = nodes(root).filter(
      (entry) => entry.type === 'ElCheckbox' && entry.props['aria-label'] === '选择 厂区 NVR',
    )
    expect(checkboxes).toHaveLength(2)
    fire(checkboxes[0]!, 'onUpdate:modelValue', true)
    await settle()
    expect(devicesChanged).toHaveBeenCalledExactlyOnceWith(['90'])
    expect(camerasChanged).not.toHaveBeenCalled()
  })

  it('allows removing a device appearance covered by a sibling group', async () => {
    const devicesChanged = vi.fn()
    const root = await mount({
      modelValue: ['2'],
      initialCameras: [recorder],
      'onUpdate:deviceIds': devicesChanged,
    })
    const checkboxes = nodes(root).filter(
      (entry) => entry.type === 'ElCheckbox' && entry.props['aria-label'] === '选择 厂区 NVR',
    )
    expect(checkboxes).toHaveLength(2)
    for (const checkbox of checkboxes) {
      expect(checkbox.props['model-value']).toBe(true)
      expect(checkbox.props.disabled).toBe(false)
      fire(checkbox, 'onUpdate:modelValue', false)
    }
    await settle()
    expect(devicesChanged).toHaveBeenCalled()
  })

  it('keeps device grants independent and groups partially selected', async () => {
    const groupsChanged = vi.fn(),
      devicesChanged = vi.fn()
    const root = await mount({
      deviceIds: ['90'],
      initialCameras: [recorder],
      'onUpdate:modelValue': groupsChanged,
      'onUpdate:deviceIds': devicesChanged,
    })
    expect(treeNode(root, '1').props.indeterminate).toBe(true)
    expect(treeNode(root, '1').props.checked).toBe(false)
    fire(treeNode(root, '1'), 'onCheck', true)
    await settle()
    expect(groupsChanged).toHaveBeenCalledExactlyOnceWith(['1'])
    expect(devicesChanged).not.toHaveBeenCalled()
  })

  it('enforces the total grant limit across devices and standalone cameras', async () => {
    const devicesChanged = vi.fn()
    const root = await mount({
      cameraIds: Array.from({ length: 2000 }, (_, index) => String(1000 + index)),
      initialCameras: [recorder],
      'onUpdate:deviceIds': devicesChanged,
    })
    const checkbox = nodes(root).find(
      (entry) => entry.type === 'ElCheckbox' && entry.props['aria-label'] === '选择 厂区 NVR',
    )!
    expect(checkbox.props.disabled).toBe(true)
    fire(checkbox, 'onUpdate:modelValue', true)
    await settle()
    expect(devicesChanged).not.toHaveBeenCalled()
  })
})
