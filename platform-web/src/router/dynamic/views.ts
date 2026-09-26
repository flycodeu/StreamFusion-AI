import type { Component } from 'vue'

export type PageLoader = () => Promise<{ default: Component }>

// Only backend-menu business pages live under views/<module>/.
// Fixed application pages belong in pages/; shared widgets belong in components/.
const files = import.meta.glob<{ default: Component }>('../../views/*/**/*.vue')

// Compatibility for existing databases; new menus store a file path directly.
const legacyPaths: Record<string, string> = {
  SYSTEM_USERS: '/system/User',
  SYSTEM_ROLES: '/system/Role',
  SYSTEM_MENUS: '/system/Menu',
  SYSTEM_DEPARTMENTS: '/system/Department',
}

export function normalizeViewPath(value: string | null): string | null {
  if (!value) return null
  const candidate = legacyPaths[value] ?? (value.startsWith('/') ? value : `/${value}`)
  if (!/^\/[A-Za-z0-9_-]+(?:\/[A-Za-z0-9_-]+)*$/.test(candidate)) return null
  return candidate
}

export function createPageResolver(manifest: Record<string, PageLoader>) {
  const pages = new Map<string, PageLoader>()
  for (const [file, loader] of Object.entries(manifest)) {
    const marker = file.indexOf('/views/')
    if (marker === -1 || !file.endsWith('.vue')) continue
    const path = file.slice(marker + '/views'.length, -'.vue'.length)
    if (normalizeViewPath(path) === path) pages.set(path, loader)
  }
  return (path: string | null): PageLoader | undefined => {
    const normalized = normalizeViewPath(path)
    return normalized ? pages.get(normalized) : undefined
  }
}

export const resolvePage = createPageResolver(files)

export const availablePagePaths = Object.keys(files)
  .map((file) => file.slice(file.indexOf('/views/') + '/views'.length, -'.vue'.length))
  .filter((path) => normalizeViewPath(path) === path)
  .sort()
