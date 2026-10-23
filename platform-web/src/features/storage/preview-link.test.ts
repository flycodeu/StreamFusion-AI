import { describe, expect, it } from 'vitest'
import { previewLocation } from './preview-link'
import type { StorageScope } from '../../api/storage/types'

const scopes = [
  { scopeId: '11', prefix: 'images/' },
  { scopeId: '12', prefix: 'other/' },
] as StorageScope[]
describe('authenticated preview location', () => {
  it('keeps encoded Chinese, plus and percent filenames under the selected server scope', () => {
    const params = new URLSearchParams({ scope: '11', file: 'images/中文目录/图片+%25.png' })
    expect(previewLocation(Object.fromEntries(params), scopes)).toEqual({
      scopeId: '11',
      directory: '中文目录/',
      file: 'images/中文目录/图片+%25.png',
    })
  })
  it('rejects another server scope, sibling prefix, folder key and escaped directories', () => {
    for (const query of [
      { scope: '99', file: 'images/a.png' },
      { scope: '11', file: 'images-other/a.png' },
      { scope: '11', file: 'images/a/' },
      { scope: '11', directory: '../' },
      { scope: '11', directory: 'a//b/' },
    ])
      expect(previewLocation(query, scopes)).toBeNull()
  })
  it('supports directory refresh and a root file without inventing a directory', () => {
    expect(previewLocation({ scope: '12', directory: 'a/b/' }, scopes)?.directory).toBe('a/b/')
    expect(previewLocation({ scope: '11', file: 'images/pic.png' }, scopes)?.directory).toBe('')
  })
})
