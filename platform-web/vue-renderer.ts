import type { Environment } from 'vitest/environments'

// Component interaction tests supply their own Vue host renderer, so no browser shim is needed.
export default {
  name: 'vue-renderer',
  viteEnvironment: 'client',
  setup: () => ({ teardown() {} }),
} satisfies Environment
