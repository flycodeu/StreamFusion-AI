import { describe, expect, it } from 'vitest'
import { connectionError, connectionInput, initialSelections, newConnection } from './form'
import type { AccessCandidate } from '../../api/camera-access/types'

describe('camera access connection and catalog selection', () => {
  it.each([
    ['RTMP', 'rtmp://camera:1935/live/main'],
    ['FLV', 'https://camera/live/main.flv'],
    ['HLS', 'https://camera/live/index.m3u8?sig=secret'],
    ['CRTSP', 'rtsp://camera/sf_ingress/0123456789abcdef0123456789abcdef'],
    ['GB28181', 'gb28181://camera/34020000001320000001/34020000001320000002'],
  ])('registers %s with additive streamUrls rather than rtspUrls', (method, url) => {
    const draft = { ...newConnection(), method: method!, name: '模拟相机', rtspUrls: [url!] }
    expect(connectionError(draft)).toBe('')
    expect(connectionInput(draft)).toEqual({ method, name: '模拟相机', streamUrls: [url] })
  })
  it.each([
    ['RTSP', 'rtsp://camera:0/live/main'],
    ['RTMP', 'rtmp://camera'],
    ['RTMP', 'rtmp://camera:65536/live/main'],
    ['GB28181', 'gb28181://camera:0/34020000001320000001/34020000001320000002'],
  ])('rejects %s origins that backend URL policy rejects', (method, url) => {
    expect(connectionError({ ...newConnection(), method, name: '相机', rtspUrls: [url] })).not.toBe(
      '',
    )
  })
  it('preserves complete RTSP encoding and uses no manual profile metadata', () => {
    const draft = {
      ...newConnection(),
      method: 'RTSP' as const,
      name: ' 炉前 ',
      rtspUrls: ['rtsp://user:p%40ss@cam.test/channel%2F1?a=2%26b'],
    }
    expect(connectionError(draft)).toBe('')
    expect(connectionInput(draft)).toEqual({
      method: 'RTSP',
      name: '炉前',
      rtspUrls: draft.rtspUrls,
    })
  })
  it.each(['', 'http://cam.test/stream', 'rtsp://cam.test/stream#token', 'rtsp://cam.test/a\nb'])(
    'rejects malformed RTSP input without echoing its contents: %s',
    (url) => {
      const message = connectionError({
        ...newConnection(),
        method: 'RTSP',
        name: '相机',
        rtspUrls: [url],
      })
      expect(message).not.toBe('')
      if (url) expect(message).not.toContain(url)
    },
  )
  it('requires a name for manual RTSP but permits device discovery to provide its name', () => {
    expect(
      connectionError({ ...newConnection(), method: 'RTSP', rtspUrls: ['rtsp://cam.test/main'] }),
    ).toContain('名称')
    expect(connectionError({ ...newConnection(), host: 'cam.test' })).toBe('')
  })
  it('requires AppKey and AppSecret for a new Hikvision platform connection', () => {
    const draft = { ...newConnection(), method: 'HIK_PLATFORM' as const, host: 'platform.test' }
    expect(connectionError(draft)).toContain('AppKey 和 AppSecret')
    expect(connectionError({ ...draft, username: 'app-key' })).toContain('AppKey 和 AppSecret')
    expect(connectionError({ ...draft, username: 'app-key', password: 'app-secret' })).toBe('')
  })
  it.each(['AUTO', 'ONVIF', 'HIKVISION', 'DAHUA', 'HIK_PLATFORM'] as const)(
    'reuses %s without sending a name override, host or credentials',
    (method) => {
      const draft = {
        ...newConnection(),
        method,
        name: 'stale local name',
        reuse: true,
        sourceId: '1',
        sourceVersion: '2',
        host: 'new.test',
        username: 'private-user',
        password: 'private-password',
      }
      expect(connectionError(draft)).toBe('')
      expect(connectionInput(draft)).toEqual({
        method,
        sourceId: '1',
        sourceVersion: '2',
      })
    },
  )
  it('still requires full RTSP URLs when reusing saved RTSP credentials', () => {
    const draft = {
      ...newConnection(),
      method: 'RTSP' as const,
      reuse: true,
      sourceId: '1',
      sourceVersion: '2',
      name: '新通道',
    }
    expect(connectionError(draft)).toContain('rtsp://')
    draft.rtspUrls = ['rtsp://cam.test/new']
    expect(connectionInput(draft)).toEqual({
      method: 'RTSP',
      name: '新通道',
      sourceId: '1',
      sourceVersion: '2',
      rtspUrls: draft.rtspUrls,
    })
  })
  it('keeps identified channels without profiles while excluding ambiguous channels', () => {
    const candidates = [
      {
        candidateId: 'opaque-channel',
        mappingRequired: false,
        name: '通道一',
        profiles: [
          { profileId: 'opaque-sub', usageHint: 'SUB' },
          { profileId: 'opaque-main', usageHint: 'MAIN' },
        ],
      },
      { candidateId: 'ambiguous', mappingRequired: true, profiles: [{ profileId: 'x' }] },
      { candidateId: 'empty', mappingRequired: false, profiles: [] },
    ] as AccessCandidate[]
    expect(initialSelections(candidates)).toEqual([
      {
        candidateId: 'opaque-channel',
        profileIds: ['opaque-sub', 'opaque-main'],
        defaultProfileId: 'opaque-main',
      },
      { candidateId: 'empty', profileIds: [] },
    ])
  })
})
