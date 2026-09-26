import { describe, expect, it } from 'vitest'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { ApiRequestError } from '../../lib/http/error'
import RequestError from './RequestError.vue'

describe('request error feedback', () => {
  it('explains hidden menu module validation without exposing submitted values', async () => {
    const error = new ApiRequestError('VALIDATION_ERROR', '请检查请求参数', {
      status: 400,
      traceId: 'a'.repeat(32),
      data: {
        fieldErrors: [
          {
            field: 'moduleKey',
            code: 'INVALID',
            msg: '字段格式不正确',
            rejectedValue: 'private-value',
          },
          { field: 'phone', code: 'INVALID', msg: 'private-error-detail' },
        ],
      },
    })
    const html = await renderToString(createSSRApp(RequestError, { error }))
    expect(html).toContain('新建页面的唯一标识须对应已实现的模块')
    expect(html).toContain('电话：格式不正确')
    expect(html).toContain('a'.repeat(32))
    expect(html).not.toContain('private-value')
    expect(html).not.toContain('private-error-detail')
  })

  it('does not suggest refreshing a duplicate account or exhausted version', async () => {
    for (const code of ['USERNAME_TAKEN', 'VERSION_EXHAUSTED']) {
      const html = await renderToString(
        createSSRApp(RequestError, {
          error: new ApiRequestError(code, '请求被拒绝', { status: 409 }),
        }),
      )
      expect(html).not.toContain('数据已变化')
      expect(html).toContain(code === 'USERNAME_TAKEN' ? '请更换账号' : '请联系管理员维护')
    }
  })
})
