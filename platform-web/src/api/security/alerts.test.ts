import { describe, expect, it } from 'vitest'
import { parseSecurityAlert, parseSecurityFeed, securityDegraded } from './alerts'
import { securityAlert, securityFeed } from '../../../tests/support/securityAlerts'

describe('security evidence boundary', () => {
  it('parses bounded evidence and rejects unknown rules and injected trace IDs', () => {
    expect(parseSecurityAlert(securityAlert())).toEqual(securityAlert())
    for (const field of [
      { rule: '__proto__' },
      { traceIds: ['<script>'] },
      { sourceIp: 'host\nforged' },
      { eventCount: 13 },
      { actorIds: [1] },
      { distinctPaths: 30 },
    ])
      expect(() => parseSecurityAlert({ ...securityAlert(), ...field })).toThrow()
  })
  it('validates feed counters and monitor limits', () => {
    expect(parseSecurityFeed(securityFeed())).toEqual(securityFeed())
    for (const field of [
      { revision: -1 },
      { droppedEvents: '0' },
      { retainedAlerts: 1001 },
      { windowSeconds: 0 },
      { instanceId: 'invalid' },
    ])
      expect(() => parseSecurityFeed({ ...securityFeed(), ...field })).toThrow()
  })
  it('shows lost coverage and persistence failures instead of a healthy empty state', () => {
    expect(securityDegraded(securityFeed())).toBe(false)
    for (const field of [
      'droppedEvents',
      'evictedSources',
      'truncatedWindows',
      'processingFailures',
    ])
      expect(securityDegraded({ ...securityFeed(), [field]: 1 })).toBe(true)
    expect(securityDegraded({ ...securityFeed(), persistenceHealthy: false })).toBe(true)
  })
})
