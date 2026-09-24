import { describe, expect, it } from 'vitest'
import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import AdminLayout from './AdminLayout.vue'

describe('AdminLayout', () => {
  it('renders the navigation and content without inventing an authenticated user', async () => {
    const html = await renderToString(
      createSSRApp({
        render: () => h(AdminLayout, null, { default: () => h('h1', '服务状态') }),
      }),
    )
    expect(html).toContain('首页')
    expect(html).toContain('未登录')
    expect(html).toContain('服务状态')
    expect(html).toContain('aria-controls="primary-sidebar"')
    expect(html).not.toContain('超级管理员')
  })

  it('escapes a supplied display name', async () => {
    const html = await renderToString(
      createSSRApp(AdminLayout, { displayName: '<script>user</script>' }),
    )
    expect(html).toContain('&lt;script&gt;user&lt;/script&gt;')
    expect(html).not.toContain('<script>')
    expect(html).not.toContain('未登录')
  })
})
