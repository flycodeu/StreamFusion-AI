import { describe, expect, it, vi } from 'vitest'
import type { StorageObject, StorageTask } from '../../api/storage/types'
import { ApiRequestError } from '../../lib/http/error'
import { deleteFileSelection } from './file-selection'

function file(name: string): StorageObject {
  return {
    objectKey: `images/${name}.png`,
    name: `${name}.png`,
    sizeBytes: '12',
    etag: `"${name}"`,
    lastModified: '2026-10-22T00:00:00Z',
    contentType: 'image/png',
    objectVersionId: null,
  }
}
function receipt(value: StorageObject, status = 'SUCCEEDED'): StorageTask {
  return {
    taskId: '123',
    runId: '124',
    instanceId: '1',
    type: 'FILE_DELETE',
    status,
    phase: 'FINISHED',
    version: '1',
    cancelRequested: false,
    traceId: null,
    acceptedAt: '2026-10-22T00:00:00Z',
    startedAt: '2026-10-22T00:00:00Z',
    finishedAt: '2026-10-22T00:00:01Z',
    result: { objectKey: value.objectKey, deleted: true },
  }
}
function store(files: StorageObject[]) {
  const values = new Map(files.map((value) => [value.objectKey, value]))
  let reads = 0
  const read = vi.fn(async (key: string) => {
    const token = `fresh-${++reads}`,
      value = values.get(key)
    if (!value) throw new ApiRequestError('NOT_FOUND', '文件不存在', { status: 404 })
    return { ...value, objectToken: token }
  })
  const remove = vi.fn(async (value: StorageObject) => {
    values.delete(value.objectKey)
    return receipt(value)
  })
  return { values, read, remove }
}

describe('manual current-page file selection', () => {
  it('confirms the receipt and absence of each file before counting success and continuing', async () => {
    const files = [file('a'), file('b')],
      remote = store(files)
    const result = await deleteFileSelection(
      files,
      remote.read,
      remote.remove,
      () => false,
      vi.fn(),
    )
    expect(result.deleted).toEqual(files.map((f) => f.objectKey))
    expect(remote.read).toHaveBeenCalledTimes(6)
    expect(remote.remove.mock.calls.map(([f]) => f.objectToken)).toEqual(['fresh-3', 'fresh-5'])
    expect(files[0]?.objectToken).toBeUndefined()
    expect(remote.values.size).toBe(0)
  })
  it('sends no delete if any selected file changed during the full preflight', async () => {
    const files = [file('a'), file('b')],
      remove = vi.fn()
    const result = await deleteFileSelection(
      files,
      async (key) => ({
        ...files.find((f) => f.objectKey === key)!,
        etag: key.endsWith('b.png') ? 'changed' : '"a"',
      }),
      remove,
      () => false,
      vi.fn(),
    )
    expect(remove).not.toHaveBeenCalled()
    expect(result.deleted).toEqual([])
    expect(result.error).toMatchObject({ code: 'STORAGE_OBJECT_CHANGED' })
  })
  it('stops after confirmed partial success when the next file changes before its delete', async () => {
    const files = [file('a'), file('b')],
      remote = store(files)
    let bReads = 0
    const result = await deleteFileSelection(
      files,
      async (key) => {
        const value = await remote.read(key)
        if (key.endsWith('b.png') && ++bReads === 2) value.etag = 'changed'
        return value
      },
      remote.remove,
      () => false,
      vi.fn(),
    )
    expect(result.deleted).toEqual(['images/a.png'])
    expect(remote.remove).toHaveBeenCalledTimes(1)
    expect(result.failed).toBe('images/b.png')
  })
  it('never retries an unknown response or continues with the next selected file', async () => {
    const files = [file('a'), file('b')],
      remote = store(files)
    remote.remove.mockRejectedValueOnce(new Error('unknown response'))
    const result = await deleteFileSelection(
      files,
      remote.read,
      remote.remove,
      () => false,
      vi.fn(),
    )
    expect(remote.remove).toHaveBeenCalledTimes(1)
    expect(result.failed).toBe('images/a.png')
    expect(result.deleted).toEqual([])
    expect(result.error).toBeDefined()
  })
  it.each(['QUEUED', 'FAILED', 'UNKNOWN'])(
    'does not count a %s receipt as deleted',
    async (status) => {
      const files = [file('a'), file('b')],
        remote = store(files)
      remote.remove.mockResolvedValueOnce(receipt(files[0]!, status))
      const result = await deleteFileSelection(
        files,
        remote.read,
        remote.remove,
        () => false,
        vi.fn(),
      )
      expect(result.deleted).toEqual([])
      expect(result.error).toMatchObject({ code: 'STORAGE_DELETE_NOT_CONFIRMED' })
      expect(remote.remove).toHaveBeenCalledTimes(1)
      expect(remote.values.size).toBe(2)
    },
  )
  it('does not claim success when the server returns success but the file is still readable', async () => {
    const files = [file('a'), file('b')],
      remote = store(files)
    remote.remove.mockResolvedValueOnce(receipt(files[0]!))
    const result = await deleteFileSelection(
      files,
      remote.read,
      remote.remove,
      () => false,
      vi.fn(),
    )
    expect(result.deleted).toEqual([])
    expect(result.failed).toBe(files[0]!.objectKey)
    expect(result.error).toMatchObject({ code: 'STORAGE_DELETE_NOT_CONFIRMED' })
    expect(remote.remove).toHaveBeenCalledTimes(1)
  })
  it('rejects a receipt referring to a different object', async () => {
    const files = [file('a')],
      remote = store(files)
    remote.remove.mockResolvedValueOnce(receipt(file('unrelated')))
    const result = await deleteFileSelection(
      files,
      remote.read,
      remote.remove,
      () => false,
      vi.fn(),
    )
    expect(result.deleted).toEqual([])
    expect(result.error).toMatchObject({ code: 'STORAGE_DELETE_NOT_CONFIRMED' })
  })
  it('preserves a same-key arrival after delete and never repeats the deletion', async () => {
    const files = [file('a'), file('b')],
      remote = store(files)
    remote.remove.mockImplementationOnce(async (selected) => {
      remote.values.set(selected.objectKey, { ...selected, etag: 'new-upload' })
      return receipt(selected)
    })
    const result = await deleteFileSelection(
      files,
      remote.read,
      remote.remove,
      () => false,
      vi.fn(),
    )
    expect(result.deleted).toEqual([])
    expect(remote.values.get(files[0]!.objectKey)?.etag).toBe('new-upload')
    expect(remote.remove).toHaveBeenCalledTimes(1)
  })
  it('honors cancellation after a read without sending the pending delete', async () => {
    let stop = false,
      reads = 0
    const remove = vi.fn(),
      selected = file('a')
    const result = await deleteFileSelection(
      [selected],
      async () => {
        if (++reads === 2) stop = true
        return { ...selected, objectToken: 'fresh' }
      },
      remove,
      () => stop,
      vi.fn(),
    )
    expect(remove).not.toHaveBeenCalled()
    expect(result.stopped).toBe(true)
  })
  it('rejects empty, duplicate, directory and over-limit selections without remote calls', async () => {
    for (const files of [
      [],
      [file('a'), file('a')],
      [{ ...file('a'), objectKey: 'images/a/' }],
      Array.from({ length: 101 }, (_, i) => file(String(i))),
    ]) {
      const read = vi.fn(),
        remove = vi.fn()
      const result = await deleteFileSelection(files, read, remove, () => false, vi.fn())
      expect(result.error).toBeDefined()
      expect(read).not.toHaveBeenCalled()
      expect(remove).not.toHaveBeenCalled()
    }
  })
})
