import type { CameraAddress, CameraProfile } from '../../api/camera/types'

export function addressLabel(address: CameraAddress | null | undefined): string {
  return address?.kind === 'PLATFORM' ? '平台地址' : 'IP / 主机'
}

export function addressText(
  address: Pick<CameraAddress, 'host' | 'port'> | null | undefined,
): string {
  if (!address?.host) return '未获取'
  const host = address.host.includes(':')
    ? `[${address.host.replace(/^\[|\]$/g, '')}]`
    : address.host
  return address.port == null ? host : `${host}:${address.port}`
}

export function streamAddress(
  profile: CameraProfile,
  source: CameraAddress | null | undefined,
): string {
  const locator = profile.locatorSummary
  if (locator.host) return addressText({ host: locator.host, port: locator.port })
  if (locator.hostMode === 'SOURCE' && source?.kind === 'RTSP') return addressText(source)
  return '未获取'
}
