import type { Camera } from '../../api/camera/types'
import type { PreviewChannel } from '../../api/preview/types'
import { getCamera } from '../../api/camera/api'
import { getPreviewLayout, layoutCount, previewLayouts, type PreviewLayout } from './layouts'

export const MAX_SAVED_VIEWS = 20
const MAX_BYTES = 131072
export interface SavedSlot {
  index: number
  cameraId: string
  profileId: string
}
export interface SavedView {
  id: string
  name: string
  layout: PreviewLayout
  updatedAt: string
  slots: SavedSlot[]
}
const id = (value: unknown): value is string =>
  typeof value === 'string' && /^[1-9][0-9]{0,18}$/.test(value)
export function savedViewsKey(userId: string): string {
  if (!id(userId)) throw new Error('请登录后使用收藏')
  return `streamfusion:preview-views:v1:${userId}`
}
export function viewName(value: string): string {
  const name = value.trim()
  if (
    !name ||
    [...name].length > 40 ||
    [...value].some((char) => char.codePointAt(0)! < 32 || char.codePointAt(0) === 127)
  )
    throw new Error('收藏名称需为 1～40 个字符，不含控制字符')
  return name
}
function validate(value: unknown): SavedView[] {
  if (!value || typeof value !== 'object') throw new Error('收藏数据格式无效')
  const document = value as Record<string, unknown>
  if (document.schemaVersion !== 1 || !Array.isArray(document.views))
    throw new Error('收藏版本不支持，请删除无效的本地收藏数据后重试')
  if (document.views.length > MAX_SAVED_VIEWS) throw new Error('本地收藏数量超出上限')
  const used = new Set<string>()
  return document.views.map((value): SavedView => {
    if (!value || typeof value !== 'object') throw new Error('收藏数据格式无效')
    const row = value as Record<string, unknown>
    if (
      typeof row.id !== 'string' ||
      !/^[a-zA-Z0-9-]{1,64}$/.test(row.id) ||
      used.has(row.id) ||
      typeof row.name !== 'string' ||
      !previewLayouts.some((layout) => layout.value === row.layout) ||
      typeof row.updatedAt !== 'string' ||
      !Number.isFinite(Date.parse(row.updatedAt)) ||
      !Array.isArray(row.slots) ||
      row.slots.length > 64
    )
      throw new Error('收藏数据格式无效')
    used.add(row.id)
    const layout = row.layout as PreviewLayout,
      occupied = new Set<number>()
    const slots = row.slots.map((value): SavedSlot => {
      if (!value || typeof value !== 'object') throw new Error('收藏窗口格式无效')
      const slot = value as Record<string, unknown>
      if (
        !Number.isInteger(slot.index) ||
        (slot.index as number) < 0 ||
        (slot.index as number) >= layoutCount(layout) ||
        occupied.has(slot.index as number) ||
        !id(slot.cameraId) ||
        !id(slot.profileId)
      )
        throw new Error('收藏窗口格式无效')
      occupied.add(slot.index as number)
      return { index: slot.index as number, cameraId: slot.cameraId, profileId: slot.profileId }
    })
    return { id: row.id, name: viewName(row.name), layout, updatedAt: row.updatedAt, slots }
  })
}
export function readSavedViews(userId: string, storage = globalThis.localStorage): SavedView[] {
  const raw = storage.getItem(savedViewsKey(userId))
  if (!raw) return []
  if (new TextEncoder().encode(raw).byteLength > MAX_BYTES) throw new Error('本地收藏数据过大')
  return validate(JSON.parse(raw))
}
export function writeSavedViews(
  userId: string,
  views: SavedView[],
  storage = globalThis.localStorage,
): SavedView[] {
  // Build the allowlisted document again; never persist a whole slot, camera, lease or locator.
  const safe = validate({ schemaVersion: 1, views })
  const encoded = JSON.stringify({ schemaVersion: 1, views: safe })
  if (new TextEncoder().encode(encoded).byteLength > MAX_BYTES) throw new Error('本地收藏空间不足')
  storage.setItem(savedViewsKey(userId), encoded)
  return safe
}
export function captureView(
  layout: PreviewLayout,
  slots: readonly {
    channel: { cameraId: string } | null
    profile: { streamProfileId: string } | null
  }[],
): Pick<SavedView, 'layout' | 'slots'> {
  getPreviewLayout(layout)
  return {
    layout,
    slots: slots
      .slice(0, layoutCount(layout))
      .flatMap((slot, index) =>
        slot.channel && slot.profile
          ? [{ index, cameraId: slot.channel.cameraId, profileId: slot.profile.streamProfileId }]
          : [],
      ),
  }
}
function previewChannel(camera: Camera): PreviewChannel {
  return {
    cameraId: camera.cameraId,
    name: camera.name,
    deviceGroupKey: camera.deviceId ? `d${camera.deviceId}` : `c${camera.cameraId}`,
    deviceName: null,
    groupId: camera.groupId,
    groupPath: camera.groupPath,
    lifecycle: camera.lifecycle,
    profiles: camera.profiles.map((profile) => ({
      streamProfileId: profile.streamProfileId,
      label: profile.label,
      usage: ['MAIN', 'SUB', 'THIRD', 'CUSTOM'].includes(profile.usageHint)
        ? (profile.usageHint as 'MAIN' | 'SUB' | 'THIRD' | 'CUSTOM')
        : 'UNKNOWN',
      enabled: profile.enabled,
      configured: profile.locatorSummary.configured,
      videoCodec: profile.videoCodec,
      width: profile.width,
      height: profile.height,
      frameRate: profile.frameRate,
      bitrateKbps: profile.bitrateKbps,
    })),
  }
}
export type ResolvedSavedSlot = SavedSlot & { channel: PreviewChannel | null; failure: unknown }
export async function resolveSavedView(
  view: SavedView,
  signal: AbortSignal,
  lookup = getCamera,
): Promise<ResolvedSavedSlot[]> {
  const ids = [...new Set(view.slots.map((slot) => slot.cameraId))]
  const found = new Map<string, { channel: PreviewChannel | null; failure: unknown }>()
  let cursor = 0
  await Promise.all(
    Array.from({ length: Math.min(4, ids.length) }, async () => {
      while (!signal.aborted && cursor < ids.length) {
        const cameraId = ids[cursor++]!
        try {
          const camera = await lookup(cameraId, signal)
          if (!signal.aborted)
            found.set(cameraId, { channel: previewChannel(camera), failure: null })
        } catch (failure) {
          if (!signal.aborted) found.set(cameraId, { channel: null, failure })
        }
      }
    }),
  )
  if (signal.aborted) return []
  return view.slots.map((slot) => ({
    ...slot,
    ...found.get(slot.cameraId)!,
  }))
}
