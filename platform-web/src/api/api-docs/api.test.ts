import { readFileSync } from 'node:fs'
import { createContext, runInContext } from 'node:vm'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiDocsFrameUrl } from './api'

const script = readFileSync(new URL('../../../public/api-docs.js', import.meta.url), 'utf8')
afterEach(() => vi.unstubAllEnvs())

describe('embedded API documentation', () => {
  it.each([true, false])('uses the ordinary API prefix when development is %s', (dev) => {
    vi.stubEnv('DEV', dev)
    vi.stubEnv('BASE_URL', '/')
    expect(apiDocsFrameUrl()).toBe(
      `/${dev ? 'api-docs.html?apiPrefix=%2Fapi' : 'api-docs.html?apiPrefix='}`,
    )
  })

  it.each([
    ['?apiPrefix=%2Fapi', '/api'],
    ['?apiPrefix=', ''],
    ['?apiPrefix=https%3A%2F%2Fother.invalid', ''],
  ])('loads the specification and local resources through %s', async (search, prefix) => {
    const status = { hidden: false, textContent: '', setAttribute: vi.fn() }
    const style = { href: '' }
    const scripts: { src: string; onload: () => void }[] = []
    const swagger = Object.assign(vi.fn(), { presets: { apis: {} } })
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ openapi: '3.1.0', paths: {} }),
    })
    const context = createContext({
      document: {
        getElementById: (id: string) => (id === 'document-status' ? status : style),
        createElement: () => ({ remove: vi.fn() }),
        head: {
          append: (element: { src: string; onload: () => void }) => {
            scripts.push(element)
            element.onload()
          },
        },
      },
      location: { search },
      URLSearchParams,
      AbortSignal,
      fetch: fetchMock,
      setTimeout,
      clearTimeout,
      SwaggerUIBundle: swagger,
    })
    runInContext(script, context)
    await vi.waitFor(() => expect(swagger).toHaveBeenCalledTimes(1))
    expect(fetchMock.mock.calls[0]?.[0]).toBe(`${prefix}/v3/api-docs`)
    expect(scripts[0]?.src).toBe(`${prefix}/swagger-ui/swagger-ui-bundle.js`)
    expect(style.href).toBe(`${prefix}/swagger-ui/swagger-ui.css`)
    expect(swagger.mock.calls[0]?.[0]).toMatchObject({
      spec: { servers: [{ url: prefix || '/' }] },
    })
    expect(status.hidden).toBe(true)
  })
})
