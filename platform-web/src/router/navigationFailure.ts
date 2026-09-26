import { shallowRef } from 'vue'

/** Keep the last failed navigation in memory so recovery retains its safe diagnostics. */
export const navigationFailure = shallowRef<{ error: unknown; path: string } | null>(null)
