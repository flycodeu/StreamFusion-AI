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
        '^/(user|auth|roles|menus|departments)(?:[/?]|$)': {
          target: env.API_TARGET || 'http://127.0.0.1:8080',
        },
        '/actuator': { target: env.API_TARGET || 'http://127.0.0.1:8080' },
      },
    },
  }
})
