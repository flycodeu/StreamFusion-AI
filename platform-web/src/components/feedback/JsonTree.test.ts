import { expect, it } from 'vitest'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'
import JsonTree from './JsonTree.vue'

it('renders diagnostic strings as text rather than executable markup', async () => {
  const html = await renderToString(
    createSSRApp(JsonTree, { value: { '<script>': '<img src=x onerror=alert(1)>' } }),
  )
  expect(html).not.toContain('<script>')
  expect(html).not.toContain('<img')
  expect(html).toContain('&lt;img')
  expect(html).toContain('json-string')
})

it('preserves zero, false, null and array entries as distinct JSON types', async () => {
  const html = await renderToString(
    createSSRApp(JsonTree, { value: [0, false, null, '9007199254740993'] }),
  )
  expect(html).toMatch(/class="json-number"[^>]*>0</)
  expect(html).toMatch(/class="json-boolean"[^>]*>false</)
  expect(html).toMatch(/class="json-null"[^>]*>null</)
  expect(html).toContain('9007199254740993')
})

it('provides collapsible groups with a bounded default expansion depth', async () => {
  const value = { one: { two: { three: { four: 'detail' } } } }
  const html = await renderToString(createSSRApp(JsonTree, { value }))
  expect((html.match(/ open/g) ?? []).length).toBe(3)
  const collapsed = await renderToString(createSSRApp(JsonTree, { value, expanded: false }))
  expect(collapsed).not.toContain(' open')
  expect(collapsed).toMatch(/<summary[ >]/)
})
