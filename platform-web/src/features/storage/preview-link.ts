import type { StorageScope } from '../../api/storage/types'

/** Platform links contain identity and location only; fresh read tokens stay in the active session. */
export function previewLocation(query: Record<string, unknown>, scopes: StorageScope[]) {
  const scope =
    typeof query.scope === 'string' ? scopes.find((row) => row.scopeId === query.scope) : scopes[0]
  if (!scope) return null
  const file = typeof query.file === 'string' ? query.file : undefined
  if (
    file &&
    (!file.startsWith(scope.prefix) ||
      file.endsWith('/') ||
      Array.from(file).some((char) => char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127))
  )
    return null
  const relative = file?.slice(scope.prefix.length)
  const directory =
    relative !== undefined
      ? relative.slice(0, relative.lastIndexOf('/') + 1)
      : typeof query.directory === 'string'
        ? query.directory
        : ''
  if (
    directory &&
    (!directory.endsWith('/') ||
      directory.startsWith('/') ||
      directory.includes('\\') ||
      directory
        .slice(0, -1)
        .split('/')
        .some((part) => !part || part === '.' || part === '..'))
  )
    return null
  return { scopeId: scope.scopeId, directory, file }
}
