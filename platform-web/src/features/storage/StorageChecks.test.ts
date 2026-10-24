// @vitest-environment vue-renderer
import { describe, expect, it, vi } from 'vitest'
import { defineComponent, h, inject, provide, ssrContextKey } from 'vue'
import type { StorageInstance } from '../../api/storage/types'
import { node, nodes, renderer, text } from '../../../tests/support/renderer'
import StorageChecks from './StorageChecks.vue'

vi.mock('./StorageRecords.vue', () => ({ default: {} }))
vi.mock('../../components/feedback/RequestError.vue', () => ({ default: {} }))
vi.mock('../../components/feedback/JsonInspector.vue', () => ({ default: {} }))
vi.mock('./display', async (original) => ({
  ...(await original<typeof import('./display')>()),
  checkResultLabel: (status: string) => `显示文案-${status}`,
}))
vi.mock('element-plus', () => ({
  ElButton: {},
  ElTag: {},
  ElDialog: {},
  ElTable: defineComponent({
    props: ['data'],
    setup(props, { slots }) {
      provide('rows', () => props.data)
      return () => h('div', slots.default?.())
    },
  }),
  ElTableColumn: defineComponent({
    setup(_, { slots }) {
      const rows = inject<() => { status: string }[]>('rows')!
      return () =>
        h(
          'div',
          rows().flatMap((row) => slots.default?.({ row }) ?? []),
        )
    },
  }),
}))

describe('storage check results', () => {
  it('uses protocol codes for styling even when translated wording changes', () => {
    const root = node('root')
    const instance = {
      instanceId: '1',
      accessStatus: 'ACCESSIBLE',
      freshness: 'FRESH',
      checkedAt: null,
      scopes: [
        {
          bucket: 'images',
          observation: {
            steps: [
              { stage: 'HEAD', status: 'PASSED' },
              { stage: 'HEAD', status: 'FAILED' },
              { stage: 'HEAD', status: 'FUTURE' },
            ],
          },
        },
      ],
    } as unknown as StorageInstance
    const app = renderer.createApp(StorageChecks, { instance })
    app.provide(ssrContextKey, { modules: new Set<string>() })
    app.mount(root)
    try {
      const states = nodes(root).filter((item) => String(item.props.class).includes('check-state'))
      expect(states).toHaveLength(3)
      expect(states[0]?.props.class).toContain('passed')
      expect(states[1]?.props.class).toContain('failed')
      expect(states[2]?.props.class).not.toMatch(/passed|failed/)
      expect(states.map(text)).toEqual(['✓显示文案-PASSED', '!显示文案-FAILED', '○显示文案-FUTURE'])
    } finally {
      app.unmount()
    }
  })
})
