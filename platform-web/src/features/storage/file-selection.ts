import type { StorageObject, StorageTask } from '../../api/storage/types'
import { ApiRequestError } from '../../lib/http/error'
import { sameObject } from './file-browser'

export interface FileDeleteProgress {
  deleted: string[]
  current: string | null
  stopped: boolean
  failed?: string | null
  error?: unknown
}

/** A manual selection is a bounded, sequential series of existing single-file operations. */
export async function deleteFileSelection(
  files: readonly StorageObject[],
  read: (key: string) => Promise<StorageObject>,
  remove: (file: StorageObject) => Promise<StorageTask>,
  stopped: () => boolean,
  progress: (state: FileDeleteProgress) => void,
): Promise<FileDeleteProgress> {
  const selection = files.map((file) => ({ ...file }))
  const result: FileDeleteProgress = { deleted: [], current: null, stopped: false }
  const deadline = Date.now() + 180_000
  function check() {
    if (Date.now() > deadline)
      throw new ApiRequestError('STORAGE_SELECTION_EXPIRED', '操作超过时限，请刷新后重新选择')
    if (stopped()) {
      result.stopped = true
      return false
    }
    return true
  }
  function publish() {
    progress({ ...result, deleted: [...result.deleted] })
  }
  try {
    if (
      !selection.length ||
      selection.length > 100 ||
      selection.some((file) => !file.objectKey || file.objectKey.endsWith('/')) ||
      new Set(selection.map((file) => file.objectKey)).size !== selection.length
    )
      throw new ApiRequestError('INVALID_REQUEST', '请选择本页 1～100 个文件')
    // A changed selection prevents the first delete. Tokens are refreshed again before each send.
    for (const file of selection) {
      if (!check()) return result
      const fresh = await read(file.objectKey)
      if (!sameObject(file, fresh))
        throw new ApiRequestError('STORAGE_OBJECT_CHANGED', '选中文件已变化，请刷新后重新选择')
    }
    for (const file of selection) {
      if (!check()) break
      result.current = file.objectKey
      publish()
      const fresh = await read(file.objectKey)
      if (!check()) break
      if (!sameObject(file, fresh))
        throw new ApiRequestError('STORAGE_OBJECT_CHANGED', '文件在删除前发生变化，已停止剩余删除')
      const receipt = await remove(fresh)
      if (
        receipt.type !== 'FILE_DELETE' ||
        receipt.status !== 'SUCCEEDED' ||
        receipt.result.deleted !== true ||
        receipt.result.objectKey !== file.objectKey
      )
        throw new ApiRequestError(
          'STORAGE_DELETE_NOT_CONFIRMED',
          `删除结果尚未确认，请核对任务 ${receipt.taskId}`,
          {
            traceId: receipt.traceId ?? undefined,
          },
        )
      let absent = false
      try {
        await read(file.objectKey)
      } catch (e) {
        if (e instanceof ApiRequestError && e.status === 404) absent = true
        else throw e
      }
      if (!absent)
        throw new ApiRequestError(
          'STORAGE_DELETE_NOT_CONFIRMED',
          `删除后文件仍可读取，请核对任务 ${receipt.taskId}`,
          {
            traceId: receipt.traceId ?? undefined,
          },
        )
      result.deleted.push(file.objectKey)
      publish()
    }
  } catch (error) {
    result.error = error
    result.failed = result.current
  } finally {
    result.current = null
    publish()
  }
  return result
}
