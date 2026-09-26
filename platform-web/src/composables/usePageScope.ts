import { onBeforeUnmount } from 'vue'
import { sessionState } from '../session/state'

/** A delayed confirmation or response must still belong to this page and signed-in account. */
export function usePageScope() {
  let active = true
  onBeforeUnmount(() => {
    active = false
  })
  return () => {
    const epoch = sessionState.epoch
    const userId = sessionState.me?.user.id
    return () => active && sessionState.epoch === epoch && sessionState.me?.user.id === userId
  }
}
