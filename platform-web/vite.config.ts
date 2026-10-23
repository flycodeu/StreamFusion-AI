import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'

// Keep same-origin API-document frames working while rejecting foreign frame ancestors.
const browserHeaders = {
  'Content-Security-Policy': "frame-ancestors 'self'",
  'X-Frame-Options': 'SAMEORIGIN',
  'X-Content-Type-Options': 'nosniff',
  'Referrer-Policy': 'strict-origin-when-cross-origin',
}

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  return {
    plugins: [vue()],
    build: { sourcemap: false },
    preview: { headers: browserHeaders },
    server: {
      host: '127.0.0.1',
      port: 8090,
      strictPort: true,
      headers: browserHeaders,
      proxy: {
        '^/api(?:/|$)': {
          target: env.API_TARGET || 'http://127.0.0.1:8080',
          xfwd: true,
          configure(proxy) {
            proxy.on('proxyReq', (outgoing, incoming) => {
              // This is the edge proxy: discard client-supplied forwarding chains.
              outgoing.setHeader('X-Forwarded-For', incoming.socket.remoteAddress || '127.0.0.1')
              outgoing.removeHeader('Forwarded')
              outgoing.removeHeader('X-Real-IP')
            })
          },
          rewrite: (path) => path.replace(/^\/api(?=\/|$)/, '') || '/',
        },
      },
    },
  }
})
