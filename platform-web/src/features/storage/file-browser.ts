import type { StorageObject } from '../../api/storage/types'

export const thumbnailLimit = 2 * 1024 * 1024
const imageTypes = new Set(['image/jpeg', 'image/png', 'image/gif', 'image/webp'])
export function thumbnailCandidate(file: StorageObject) {
  return (
    /^\d+$/.test(file.sizeBytes) &&
    BigInt(file.sizeBytes) <= BigInt(thumbnailLimit) &&
    (file.contentType
      ? imageTypes.has(file.contentType)
      : /\.(?:jpe?g|png|gif|webp)$/i.test(file.name))
  )
}
export function sameObject(a: StorageObject, b: StorageObject) {
  return (
    a.objectKey === b.objectKey &&
    a.etag === b.etag &&
    a.sizeBytes === b.sizeBytes &&
    a.lastModified === b.lastModified
  )
}
export function folderName(prefix: string) {
  return prefix.replace(/\/$/, '').split('/').pop() || prefix
}
export function libraryFiles(items: StorageObject[]) {
  return items.filter((item) => !item.objectKey.endsWith('/'))
}

/** One bounded queue per visible library. Cancelling removes waiting work and aborts active reads. */
export function createThumbnailQueue() {
  type Job = { controller: AbortController; run: (signal: AbortSignal) => Promise<void> }
  const pending: Job[] = []
  const active = new Set<Job>()
  function drain() {
    while (active.size < 2 && pending.length) {
      const job = pending.shift()!
      if (job.controller.signal.aborted) continue
      active.add(job)
      void Promise.resolve()
        .then(() => job.run(job.controller.signal))
        .catch(() => {})
        .finally(() => {
          active.delete(job)
          drain()
        })
    }
  }
  return {
    add(run: Job['run']) {
      if (pending.length >= 50) return () => {}
      const job = { controller: new AbortController(), run }
      pending.push(job)
      drain()
      return () => {
        job.controller.abort()
        const index = pending.indexOf(job)
        if (index >= 0) pending.splice(index, 1)
      }
    },
    clear() {
      const waiting = pending.splice(0)
      for (const job of [...waiting, ...active]) job.controller.abort()
    },
  }
}
export type ThumbnailQueue = ReturnType<typeof createThumbnailQueue>
