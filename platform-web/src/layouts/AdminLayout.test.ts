import { describe, expect, it } from 'vitest'
import { createSSRApp, h } from 'vue'
import type { App } from 'vue'
import { createRouter, createMemoryHistory } from 'vue-router'
import { renderToString } from 'vue/server-renderer'
import AdminLayout from './AdminLayout.vue'

async function withRouter(app: App): Promise<void> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/home', component: { render: () => null }, meta: { title: '首页' } }],
  })
  app.use(router)
  await router.push('/home')
  await router.isReady()
}

describe('AdminLayout', () => {
  it('renders the navigation and content without inventing an authenticated user', async () => {
    const app = createSSRApp({
      render: () => h(AdminLayout, null, { default: () => h('h1', '服务状态') }),
    })
    await withRouter(app)
    const html = await renderToString(app)
    expect(html).toContain('首页')
    expect(html).toContain('未登录')
    expect(html).toContain('服务状态')
    expect(html).toContain('aria-controls="primary-sidebar"')
    expect(html).not.toContain('超级管理员')
  })

  it('escapes a supplied display name', async () => {
    const app = createSSRApp(AdminLayout, { displayName: '<script>user</script>' })
    await withRouter(app)
    const html = await renderToString(app)
    expect(html).toContain('&lt;script&gt;user&lt;/script&gt;')
    expect(html).not.toContain('<script>')
    expect(html).not.toContain('未登录')
  })
})
