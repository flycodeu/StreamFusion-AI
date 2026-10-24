import { describe, expect, it } from 'vitest'
import type { AccessAdapter } from '../../api/camera-access/types'
import { accessJobLabel, accessMethodLabel, connectionLabel } from './presentation'

describe('adapter metadata and job context', () => {
  it('prefers server labels and displays new adapter types without a client release', () => {
    const adapters = [
      { type: 'HIKVISION', label: '海康新版接入' },
      { type: 'NEW_VENDOR', label: '新品牌设备' },
    ] as AccessAdapter[]
    expect(accessMethodLabel('HIKVISION', adapters)).toBe('海康新版接入')
    expect(accessMethodLabel('NEW_VENDOR', adapters)).toBe('新品牌设备')
    expect(connectionLabel('NEW_VENDOR', 'DEVICE', adapters)).toBe('新品牌设备')
    expect(accessMethodLabel('HIKVISION')).toBe('海康设备')
    expect(accessMethodLabel('FUTURE')).toBe('FUTURE')
    expect(connectionLabel(null, 'PLATFORM')).toBe('平台连接')
  })
  it('distinguishes a catalog read, network scan and import with the same status', () => {
    expect(accessJobLabel({ kind: 'CATALOG', status: 'RUNNING' })).toBe('正在读取设备')
    expect(accessJobLabel({ kind: 'SCAN', status: 'RUNNING' })).toBe('正在搜索')
    expect(accessJobLabel({ kind: 'BULK_IMPORT', status: 'RUNNING' })).toBe('正在导入')
  })
})
