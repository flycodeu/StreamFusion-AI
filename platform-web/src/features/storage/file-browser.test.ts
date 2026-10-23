import { expect, it, vi } from 'vitest'
import {
  createThumbnailQueue,
  sameObject,
  thumbnailCandidate,
  thumbnailLimit,
  libraryFiles,
} from './file-browser'
import type { StorageObject } from '../../api/storage/types'

const file: StorageObject = {
  objectKey: '中文/+%25.png',
  name: '图片.png',
  etag: '"one"',
  sizeBytes: '1024',
  lastModified: '2026-10-22T00:00:00Z',
  contentType: null,
  objectVersionId: null,
}
it('hides S3 directory markers while preserving ordinary empty files', () => {
  expect(
    libraryFiles([
      file,
      { ...file, objectKey: '中文/', sizeBytes: '0' },
      { ...file, objectKey: '中文/empty.txt', sizeBytes: '0' },
    ]).map((item) => item.objectKey),
  ).toEqual([file.objectKey, '中文/empty.txt'])
})
it('only reads small raster candidates and rejects active, huge or invalid content', () => {
  expect(thumbnailCandidate(file)).toBe(true)
  expect(thumbnailCandidate({ ...file, sizeBytes: String(thumbnailLimit + 1) })).toBe(false)
  expect(thumbnailCandidate({ ...file, sizeBytes: '9007199254740993' })).toBe(false)
  expect(thumbnailCandidate({ ...file, sizeBytes: '-1' })).toBe(false)
  expect(thumbnailCandidate({ ...file, contentType: 'image/svg+xml' })).toBe(false)
  expect(thumbnailCandidate({ ...file, name: 'file.html' })).toBe(false)
})
it('does not use a new object as the old card thumbnail', () => {
  expect(sameObject(file, { ...file })).toBe(true)
  for (const patch of [
    { etag: '"two"' },
    { sizeBytes: '999' },
    { objectKey: 'different.png' },
    { lastModified: '2026-10-23T00:00:00Z' },
  ])
    expect(sameObject(file, { ...file, ...patch })).toBe(false)
})
it('bounds active requests to two, skips cancelled waiting work and aborts on leave', async () => {
  const queue = createThumbnailQueue(),
    started: number[] = [],
    signals: AbortSignal[] = []
  const completions: (() => void)[] = []
  const enqueue = (id: number) =>
    queue.add((signal) => {
      started.push(id)
      signals.push(signal)
      return new Promise<void>((resolve) => {
        completions.push(resolve)
        signal.addEventListener('abort', () => resolve(), { once: true })
      })
    })
  enqueue(1)
  enqueue(2)
  const cancel = enqueue(3)
  enqueue(4)
  await vi.waitFor(() => expect(started).toEqual([1, 2]))
  cancel()
  completions[0]!()
  await vi.waitFor(() => expect(started).toEqual([1, 2, 4]))
  queue.clear()
  expect(signals[0]?.aborted).toBe(false)
  expect(signals.slice(1).every((signal) => signal.aborted)).toBe(true)
})
it('keeps waiting work bounded even when many cards are queued', async () => {
  const queue = createThumbnailQueue(),
    run = vi.fn(
      (signal: AbortSignal) =>
        new Promise<void>((resolve) =>
          signal.addEventListener('abort', () => resolve(), { once: true }),
        ),
    )
  for (let i = 0; i < 200; i++) queue.add(run)
  await vi.waitFor(() => expect(run).toHaveBeenCalledTimes(2))
  queue.clear()
  await Promise.resolve()
  expect(run).toHaveBeenCalledTimes(2)
})
