import { describe, expect, it } from 'vitest'
import { labelOf } from './display'
import { validationFieldLabel } from '../lib/http/presentation'

describe('display-only dictionaries', () => {
  it('keeps unknown codes visible without interpreting prototype properties', () => {
    const labels = { ACTIVE: '运行中', 0: '待改密' }
    expect(labelOf(labels, 'ACTIVE')).toBe('运行中')
    expect(labelOf(labels, 0)).toBe('待改密')
    for (const code of ['FUTURE', '__proto__', 'constructor', 'toString'])
      expect(labelOf(labels, code)).toBe(code)
    expect(labelOf(labels, null)).toBe('—')
    expect(labelOf(labels, '', '未提供')).toBe('未提供')
  })
  it('allows domain field names without changing generic validation wording', () => {
    expect(validationFieldLabel('host', { host: '设备地址' })).toBe('设备地址')
    expect(validationFieldLabel('host')).toBe('主机地址')
    expect(validationFieldLabel('unknownField')).toBe('unknownField')
    expect(validationFieldLabel('__proto__')).toBe('__proto__')
  })
})
