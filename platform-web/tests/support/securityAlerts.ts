import type { SecurityAlert, SecurityFeed } from '../../src/api/security/alerts'
import type { AuthUser } from '../../src/api/auth/types'

export const securityFeed = (revision = 0): SecurityFeed => ({
  enabled: true,
  instanceId: '11111111-1111-4111-a111-111111111111',
  revision,
  retainedAlerts: revision,
  windowSeconds: 300,
  cooldownSeconds: 300,
  droppedEvents: 0,
  evictedSources: 0,
  truncatedWindows: 0,
  processingFailures: 0,
  persistenceHealthy: true,
})
export const securityAlert = (): SecurityAlert => ({
  id: '22222222-2222-4222-a222-222222222222',
  rule: 'PATH_SCAN',
  sourceIp: '192.0.2.1',
  firstSeen: '2026-10-22T00:00:00Z',
  lastSeen: '2026-10-22T00:01:00Z',
  windowSeconds: 300,
  eventCount: 12,
  distinctPaths: 10,
  categories: { PATH_PROBE: 12 },
  actorIds: [],
  traceIds: ['a'.repeat(32)],
})
export const securityAdmin = (): AuthUser => ({
  isSuperAdmin: true,
  modules: ['audit', 'diagnostics'],
  routes: [],
  roles: [],
  user: {
    id: '1',
    username: 'admin',
    nickname: null,
    avatarKey: null,
    phone: null,
    email: null,
    gender: 0,
    status: 1,
    mustChangePassword: false,
    version: '1',
  },
})
