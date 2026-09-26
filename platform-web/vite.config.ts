import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  return {
    plugins: [vue()],
    server: {
      host: '127.0.0.1',
      port: 8090,
      strictPort: true,
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
