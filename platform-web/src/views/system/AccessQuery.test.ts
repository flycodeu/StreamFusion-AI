import { describe, expect, it, vi } from 'vitest'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { ID_INJECTION_KEY, ZINDEX_INJECTION_KEY } from 'element-plus'
import AccessQuery from './AccessQuery.vue'

describe('permission query initial page', () => {
  it('starts with role and page overviews without a mandatory selector or mutations', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch')
    const app = createSSRApp(AccessQuery)
    app.provide(ID_INJECTION_KEY, { prefix: 1, current: 0 })
    app.provide(ZINDEX_INJECTION_KEY, { current: 0 })
    const html = await renderToString(app)
    expect(html).toContain('按角色查看')
    expect(html).toContain('按页面查看')
    expect(html).toContain('角色授权概览')
    expect(html).toContain('不是访问行为记录')
    expect(html).not.toContain('所有数据只读')
    expect(html).not.toContain('对象级保护和其他业务限制')
    expect(html).not.toContain('先选择一个页面或用户')
    expect(html).not.toMatch(/授予权限|回收权限|编辑授权|保存修改/)
    expect(fetchMock).not.toHaveBeenCalled()
    fetchMock.mockRestore()
  })
})
