import { describe, expect, it, vi } from 'vitest'
import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { ID_INJECTION_KEY, ZINDEX_INJECTION_KEY } from 'element-plus'
import HelpTip from './HelpTip.vue'
import PageHeader from './PageHeader.vue'
import FilterBar from './FilterBar.vue'
import StandardPagination from '../table/StandardPagination.vue'
import CursorPagination from '../table/CursorPagination.vue'

function createProvidedApp(
  root: Parameters<typeof createSSRApp>[0],
  props?: Record<string, unknown>,
) {
  const app = createSSRApp(root, props)
  app.provide(ID_INJECTION_KEY, { prefix: 1, current: 0 })
  app.provide(ZINDEX_INJECTION_KEY, { current: 0 })
  return app
}

describe('shared UI primitives', () => {
  it('renders HelpTip as an accessible help trigger button', async () => {
    const html = await renderToString(
      createProvidedApp(HelpTip, { content: '测试提示内容', label: '测试说明' }),
    )
    expect(html).toContain('sf-help-tip')
    expect(html).toContain('aria-label="测试说明"')
  })

  it('renders PageHeader with title, count pill, HelpTip, and actions slot', async () => {
    const html = await renderToString(
      createProvidedApp({
        render: () =>
          h(
            PageHeader,
            { title: '预览节点', count: '3 台已登记', help: '节点说明', headingTag: 'h2' },
            { actions: () => h('button', '刷新') },
          ),
      }),
    )
    expect(html).toContain('<h2')
    expect(html).toContain('预览节点')
    expect(html).toContain('3 台已登记')
    expect(html).toContain('sf-help-tip')
    expect(html).toContain('刷新')
  })

  it('renders FilterBar with unified search and reset buttons', async () => {
    const html = await renderToString(createProvidedApp(FilterBar, { loading: false }))
    expect(html).toContain('search-form')
    expect(html).toContain('查询')
    expect(html).toContain('重置')
  })

  it('renders StandardPagination with full default layout and total count', async () => {
    const html = await renderToString(
      createProvidedApp(StandardPagination, { page: 1, size: 20, total: 42 }),
    )
    expect(html).toContain('sf-standard-pagination')
    expect(html).toContain('Total 42')
    expect(html).toContain('el-pagination__sizes')
    expect(html).toContain('el-pagination__jump')
  })

  it('renders CursorPagination with summary, page indicator, and prev/next controls', async () => {
    const onPrev = vi.fn()
    const onNext = vi.fn()
    const html = await renderToString(
      createProvidedApp(CursorPagination, {
        page: 2,
        pageSize: 50,
        hasPrev: true,
        hasNext: false,
        summary: '共 80 条',
        onPrev,
        onNext,
      }),
    )
    expect(html).toContain('sf-cursor-pagination')
    expect(html).toContain('共 80 条')
    expect(html).toContain('第 2 页')
    expect(html).toContain('上一页')
    expect(html).toContain('下一页')
  })
})
