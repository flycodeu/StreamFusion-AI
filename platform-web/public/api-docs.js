const status = globalThis.document.getElementById('document-status')
// Accept only the existing development prefix; production APIs retain their root paths.
const apiPrefix =
  new globalThis.URLSearchParams(globalThis.location.search).get('apiPrefix') === '/api'
    ? '/api'
    : ''
globalThis.document.getElementById('swagger-style').href = `${apiPrefix}/swagger-ui/swagger-ui.css`

async function loadScript() {
  await new Promise((resolve, reject) => {
    const script = globalThis.document.createElement('script')
    const timeout = globalThis.setTimeout(() => {
      script.remove()
      reject(new Error('接口文档资源加载超时，请刷新重试。'))
    }, 15000)
    script.src = `${apiPrefix}/swagger-ui/swagger-ui-bundle.js`
    script.onload = () => {
      globalThis.clearTimeout(timeout)
      resolve()
    }
    script.onerror = () => {
      globalThis.clearTimeout(timeout)
      reject(new Error('接口文档资源加载失败，请刷新重试。'))
    }
    globalThis.document.head.append(script)
  })
}

async function loadDocument() {
  const response = await fetch(`${apiPrefix}/v3/api-docs`, {
    credentials: 'same-origin',
    signal: AbortSignal.timeout(15000),
    headers: { Accept: 'application/json' },
  })
  if (!response.ok) {
    const trace = response.headers.get('X-Trace-Id')
    throw new Error(
      `接口文档加载失败（HTTP ${response.status}），请刷新重试。${trace ? `\n请求标识 ${trace}` : ''}`,
    )
  }
  const spec = await response.json()
  if (!spec.openapi || !spec.paths) throw new Error('接口文档格式无效，请检查后端文档服务。')
  return spec
}

async function start() {
  try {
    const [, spec] = await Promise.all([loadScript(), loadDocument()])
    globalThis.SwaggerUIBundle({
      spec: { ...spec, servers: [{ url: apiPrefix || '/', description: '当前平台 API' }] },
      dom_id: '#swagger-ui',
      deepLinking: true,
      filter: true,
      docExpansion: 'none',
      defaultModelsExpandDepth: -1,
      supportedSubmitMethods: [],
      validatorUrl: null,
      presets: [globalThis.SwaggerUIBundle.presets.apis],
      layout: 'BaseLayout',
    })
    status.hidden = true
  } catch (error) {
    status.setAttribute('role', 'alert')
    status.textContent = `${error instanceof Error ? error.message : '接口文档加载失败，请刷新重试。'}\n${new Date().toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' })}`
  }
}

void start()
