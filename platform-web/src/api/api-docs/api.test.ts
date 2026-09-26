import { readFileSync } from 'node:fs'
import { createContext, runInContext } from 'node:vm'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiRequestError } from '../../lib/http/error'
import type { RawHttpResponse } from '../../lib/http/types'
import { apiDocsFrameUrl, getOpenApiDocument } from './api'

const api = vi.hoisted(() => ({ request: vi.fn(), send: vi.fn() }))
vi.mock('../client', () => ({ request: api.request }))
vi.mock('../../lib/http/transport', () => ({ send: api.send }))

const script = readFileSync(new URL('../../../public/api-docs.js', import.meta.url), 'utf8')
afterEach(() => vi.unstubAllEnvs())
beforeEach(() => vi.clearAllMocks())

describe('embedded API documentation', () => {
  it.each([true, false])('uses the ordinary API prefix when development is %s', (dev) => {
    vi.stubEnv('DEV', dev)
    vi.stubEnv('BASE_URL', 'https://baidu.com/')
    expect(apiDocsFrameUrl()).toBe(
      `/${dev ? 'api-docs.html?apiPrefix=%2Fapi' : 'api-docs.html?apiPrefix='}`,
    )
  })

  it.each([
    ['?apiPrefix=%2Fapi', '/api'],
    ['?apiPrefix=', ''],
    ['?apiPrefix=https%3A%2F%2Fother.invalid', ''],
    ['?apiPrefix=%2F%2Fbaidu.com&url=https://baidu.com&configUrl=https://baidu.com', ''],
    ['?apiPrefix=%2Fapi&url=https://baidu.com&configUrl=https://baidu.com', '/api'],
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
      location: {
        search,
        href: `http://127.0.0.1:8090/api-docs.html${search}`,
        origin: 'http://127.0.0.1:8090',
      },
      URL,
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
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({
      credentials: 'same-origin',
      redirect: 'error',
    })
    expect(scripts[0]?.src).toBe(`${prefix}/swagger-ui/swagger-ui-bundle.js`)
    expect(style.href).toBe(`${prefix}/swagger-ui/swagger-ui.css`)
    expect(swagger.mock.calls[0]?.[0]).toMatchObject({
      spec: { servers: [{ url: prefix || '/' }] },
      queryConfigEnabled: false,
      configUrl: null,
      url: '',
      supportedSubmitMethods: [],
      validatorUrl: null,
    })
    const intercept = swagger.mock.calls[0]?.[0].requestInterceptor
    expect(intercept({ url: `${prefix}/v3/api-docs` })).toMatchObject({
      credentials: 'same-origin',
      redirect: 'error',
    })
    for (const url of [
      'https://baidu.com',
      '//baidu.com',
      '/auth/me',
      `${prefix}/v3/api-docs?url=https://baidu.com`,
    ]) {
      expect(() => intercept({ url })).toThrow('固定文档地址')
    }
    expect(status.hidden).toBe(true)
  })
})

function documentResponse(value: unknown): RawHttpResponse {
  return {
    status: 200,
    headers: {
      contentType: 'application/json',
      traceId: 'b'.repeat(32),
      etag: null,
      location: null,
      retryAfter: null,
    },
    bodyText: JSON.stringify(value),
    requestTraceId: 'a'.repeat(32),
    receivedAt: '2026-09-26T10:00:00Z',
  }
}

describe('OpenAPI export', () => {
  it('rechecks the PAGE grant and downloads the fixed JSON without rewriting its contents', async () => {
    const spec = {
      openapi: '3.1.0',
      info: { title: '平台 API' },
      paths: {},
      servers: [{ url: '/' }],
    }
    api.request.mockResolvedValue({ data: { enabled: true } })
    api.send.mockResolvedValue(documentResponse(spec))
    const signal = new AbortController().signal
    expect(JSON.parse(await getOpenApiDocument(signal))).toEqual(spec)
    expect(api.request).toHaveBeenCalledWith(
      expect.objectContaining({ path: '/api-docs/status', method: 'GET', signal }),
    )
    expect(api.send).toHaveBeenCalledWith({
      path: '/v3/api-docs',
      method: 'GET',
      signal,
      timeoutMs: 15000,
    })
    expect(api.request.mock.invocationCallOrder[0]).toBeLessThan(
      api.send.mock.invocationCallOrder[0]!,
    )
  })

  it('does not fetch a document after the PAGE grant is revoked', async () => {
    const denied = new ApiRequestError('FORBIDDEN', '无权访问', { status: 403 })
    api.request.mockRejectedValue(denied)
    await expect(getOpenApiDocument()).rejects.toBe(denied)
    expect(api.send).not.toHaveBeenCalled()
  })

  it('does not fetch a disabled document', async () => {
    api.request.mockResolvedValue({ data: { enabled: false } })
    await expect(getOpenApiDocument()).rejects.toMatchObject({ code: 'API_DOCS_DISABLED' })
    expect(api.send).not.toHaveBeenCalled()
  })

  it.each([
    { openapi: '3.1.0', paths: [] },
    { openapi: true, paths: {} },
    { code: 'SUCCESS', data: {} },
  ])('rejects invalid document data and retains the request identifier', async (value) => {
    api.request.mockResolvedValue({ data: { enabled: true } })
    api.send.mockResolvedValue(documentResponse(value))
    await expect(getOpenApiDocument()).rejects.toMatchObject({
      code: 'INVALID_RESPONSE',
      traceId: 'b'.repeat(32),
    })
  })
})
