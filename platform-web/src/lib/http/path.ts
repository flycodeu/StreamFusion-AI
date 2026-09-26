const reservedRoots = new Set([
  'api',
  'actuator',
  'assets',
  'src',
  'public',
  'node_modules',
  '__vite',
  '__open-in-editor',
])

/** API paths are canonical endpoint segments, never URLs or static resource paths. */
export function isBusinessApiPath(path: unknown): path is string {
  if (typeof path !== 'string' || !/^\/[A-Za-z][A-Za-z0-9_-]*(?:\/[A-Za-z0-9_-]+)*$/.test(path))
    return false
  const root = path.split('/')[1]
  return root !== undefined && !reservedRoots.has(root.toLowerCase())
}

export function isTransportApiPath(path: unknown): path is string {
  return path === '/actuator/health' || isBusinessApiPath(path)
}
