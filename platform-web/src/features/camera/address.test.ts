import { describe, expect, it } from 'vitest'
import { parseProfile } from '../../api/camera/types'
import { addressLabel, addressText, streamAddress } from './address'

describe('camera address presentation', () => {
  it('formats IP, hostname, IPv6 and missing addresses without resolving names', () => {
    expect(addressText({ host: '10.0.1.5', port: 80 })).toBe('10.0.1.5:80')
    expect(addressText({ host: 'camera.example', port: null })).toBe('camera.example')
    expect(addressText({ host: '2001:db8::1', port: 554 })).toBe('[2001:db8::1]:554')
    expect(addressText({ host: '[2001:db8::1]', port: 554 })).toBe('[2001:db8::1]:554')
    expect(addressText(null)).toBe('未获取')
    expect(addressLabel({ kind: 'PLATFORM', host: 'platform.example', port: 443 })).toBe('平台地址')
  })
  it('never substitutes an HTTP management or platform address for the stream endpoint', () => {
    const profile = parseProfile({
      streamProfileId: '1',
      label: 'main',
      usageHint: 'MAIN',
      usageOrigin: 'MANUAL',
      enabled: true,
      version: '0',
      locatorSummary: { locatorKind: 'RTSP', configured: true, hostMode: 'SOURCE' },
    })
    expect(streamAddress(profile, { kind: 'RTSP', host: '10.0.1.5', port: 554 })).toBe(
      '10.0.1.5:554',
    )
    expect(streamAddress(profile, { kind: 'DEVICE', host: '10.0.1.5', port: 80 })).toBe('未获取')
    expect(streamAddress(profile, { kind: 'PLATFORM', host: 'platform.example', port: 443 })).toBe(
      '未获取',
    )
    profile.locatorSummary.host = '10.0.3.7'
    profile.locatorSummary.port = 8554
    expect(streamAddress(profile, null)).toBe('10.0.3.7:8554')
  })
})
