import { describe, expect, it } from 'vitest'
import { parseMenuRoutes } from './types'

function directory(id: number, children: unknown[] = []): unknown {
  return {
    id: String(id),
    name: `目录${id}`,
    type: 'DIRECTORY',
    visible: true,
    icon: null,
    routeName: null,
    path: null,
    componentKey: null,
    moduleKey: null,
    children,
  }
}

describe('menu response parsing', () => {
  it('keeps sibling indexes independent of nested directory depth', () => {
    const routes = Array.from({ length: 10 }, (_, i) =>
      directory(i + 1, [directory(100 + i, [directory(200 + i)])]),
    )
    const parsed = parseMenuRoutes(routes)
    expect(parsed).toHaveLength(10)
    expect(parsed[9]?.children[0]?.children[0]?.id).toBe('209')
  })
  it('bounds unexpectedly deep responses', () => {
    let route = directory(1)
    for (let i = 2; i <= 8; i++) route = directory(i, [route])
    expect(() => parseMenuRoutes([route])).toThrow('route depth')
  })
})
