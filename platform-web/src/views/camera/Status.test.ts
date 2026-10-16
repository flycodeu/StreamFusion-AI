import { afterEach, describe, expect, it, vi } from 'vitest'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { ID_INJECTION_KEY } from 'element-plus'
import type { StatusChannel, StatusSearch } from '../../api/camera-status/types'
import type { Ref } from 'vue'
import Status from './Status.vue'
import { ApiRequestError } from '../../lib/http/error'

const mock = vi.hoisted(() => ({
  rows: [] as StatusChannel[],
  error: null as unknown,
  route: { query: {} as Record<string, string> },
  query: null as StatusSearch | null,
}))
vi.mock('vue-router', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-router')>()),
  useRoute: () => mock.route,
}))
vi.mock('../../features/camera-status/useStatusPage', async () => {
  const { ref } = await import('vue')
  return {
    useStatusPage: (query: Ref<StatusSearch>) => {
      mock.query = query.value
      return {
        rows: ref(mock.rows),
        result: ref({
          total: 17,
          summary: {
            total: 1,
            accessible: 0,
            failed: 0,
            checking: 0,
            stale: 1,
            unknown: 0,
            disabled: 0,
          },
        }),
        total: ref(17),
        loading: ref(false),
        paused: ref(false),
        error: ref(mock.error),
        refresh: vi.fn(),
      }
    },
  }
})
afterEach(() => {
  mock.rows = []
  mock.error = null
  mock.route.query = {}
  vi.restoreAllMocks()
})
function channel(): StatusChannel {
  return {
    cameraId: '1',
    groupKey: 'c1',
    defaultPreviewProfileId: null,
    name: '装卸通道',
    deviceName: null,
    lifecycle: 'ENABLED',
    profiles: [
      {
        streamProfileId: '2',
        name: null,
        streamKind: 'SUB',
        enabled: true,
        control: {
          layer: 'CONTROL',
          probeKind: 'RTSP_DESCRIBE',
          freshness: 'STALE',
          configurationCurrent: true,
          canCheck: true,
          blockingReasonCode: null,
          latestCheck: null,
          observation: {
            value: 'ACCESSIBLE',
            reasonCode: 'RTSP_ACCESSIBLE',
            observedAt: '2026-10-13T01:00:00Z',
            readAt: '2026-10-13T01:00:00Z',
            validUntil: '2026-10-13T01:05:00Z',
            checkId: '100',
            mediaEvidence: null,
          },
          timeline: null,
        },
        media: {
          layer: 'MEDIA',
          probeKind: 'ZLM_RTP_FRAMES',
          observation: null,
          freshness: 'NEVER',
          configurationCurrent: true,
          latestCheck: null,
          canCheck: true,
          blockingReasonCode: null,
          timeline: null,
        },
      },
    ],
  }
}
async function renderPage() {
  const app = createSSRApp(Status)
  app.provide(ID_INJECTION_KEY, { prefix: 1, current: 0 })
  return renderToString(app)
}
describe('independent camera status page presentation', () => {
  it('shows official upstream facts without implying media reception or displaying RTSP state', async () => {
    const row = channel(),
      profile = row.profiles[0]!
    profile.control = {
      ...profile.control,
      canCheck: false,
      blockingReasonCode: 'STATUS_LAYER_NOT_APPLICABLE',
    }
    profile.media = {
      ...profile.media,
      canCheck: false,
      blockingReasonCode: 'STATUS_LAYER_NOT_APPLICABLE',
    }
    profile.upstream = {
      ...profile.control,
      layer: 'UPSTREAM',
      probeKind: 'HIK_PLATFORM_ONLINE',
      freshness: 'CURRENT',
      canCheck: true,
      blockingReasonCode: null,
      observation: { ...profile.control.observation!, reasonCode: 'PLATFORM_ONLINE' },
    }
    mock.rows = [row]
    const html = await renderPage(),
      card = html.slice(html.indexOf('<article'), html.indexOf('</article>'))
    expect(card).toContain('平台在线')
    expect(card).not.toContain('采样收帧正常')
    expect(card).not.toContain('代表流可达')
    expect(card).not.toContain('connection-state')
  })
  it('uses backend summary for the selected layer instead of recalculating counts from cards', async () => {
    const row = channel()
    row.profiles[0]!.media = {
      ...row.profiles[0]!.media,
      freshness: 'CURRENT',
      observation: { ...row.profiles[0]!.control.observation!, reasonCode: 'MEDIA_RECEIVING' },
    }
    mock.rows = [row]
    const html = await renderPage()
    expect(html).toContain('采样收帧正常')
    expect(html).toContain('检查通过 0')
    expect(html).toContain('结果过期 1')
    expect(html).toContain('相机检查总览')
    expect(mock.query?.layer).toBe('MEDIA')
  })
  it('shows failure observation time without extending sampled duration to the present', async () => {
    const row = channel()
    const profile = row.profiles[0]!.media
    profile.observation = { ...row.profiles[0]!.control.observation!, value: 'FAILED' }
    profile.timeline = {
      stateSince: '2026-10-12T23:00:00Z',
      lastAccessibleAt: '2026-10-12T22:00:00Z',
      lastFailedAt: profile.observation.observedAt,
    }
    mock.rows = [row]
    const html = await renderPage()
    expect(html).toContain('异常首检')
    expect(html).toContain('2026/10/13 07:00:00')
    expect(html).toContain('2 小时 0 分钟')
    expect(html).not.toContain('已离线')
    profile.configurationCurrent = false
    expect(await renderPage()).not.toContain('异常首检')
  })
  it('shows graphical current-page counts rather than pretending the filtered total is online', async () => {
    mock.rows = [channel()]
    const html = await renderPage()
    expect(html).toContain('当前页 1 个通道')
    expect(html).toContain('status-distribution')
    expect(html).toContain('role="img"')
    expect(html).toContain('结果过期 1')
    expect(html).toContain('检查通过 0')
    expect(html).toContain('创建 / 管理任务')
    expect(html).not.toContain('定时检查 · 全量范围 · 启停管理')
    expect(html).not.toContain('<h2>相机状态</h2>')
    expect(html).not.toMatch(/在线率|视频正常率|历史趋势/)
  })
  it('uses the enabled default preview as the card representative without displaying profile lists', async () => {
    const row = channel()
    const base = row.profiles[0]!
    row.defaultPreviewProfileId = '5'
    row.profiles = [
      {
        ...base,
        streamProfileId: '2',
        streamKind: 'MAIN',
        media: {
          ...base.media,
          freshness: 'CURRENT',
          observation: { ...base.control.observation! },
        },
      },
      {
        ...base,
        streamProfileId: '5',
        streamKind: 'SUB',
        media: {
          ...base.media,
          freshness: 'CURRENT',
          observation: { ...base.control.observation!, value: 'FAILED' },
        },
      },
    ]
    mock.rows = [row]
    const html = await renderPage()
    expect(html).toContain('采样收帧异常')
    expect(html).toContain('检查通过 0')
    expect(html).toContain('state-dot')
    expect(html).not.toContain('stream-row')
  })
  it('shows the authorized address on the card and distinguishes platform endpoints', async () => {
    const row = channel()
    row.accessAddress = { kind: 'DEVICE', host: '2001:db8::1', port: 80 }
    mock.rows = [row]
    expect(await renderPage()).toContain('[2001:db8::1]:80')
    row.accessAddress = { kind: 'PLATFORM', host: 'isc.example', port: 443 }
    mock.rows = [row]
    const html = await renderPage()
    expect(html).toContain('平台地址')
    expect(html).toContain('isc.example:443')
  })
  it('keeps media untested and stale CONTROL facts distinct from camera configuration', async () => {
    mock.rows = [channel()]
    const fetchMock = vi.spyOn(globalThis, 'fetch')
    const html = await renderPage()
    expect(html).toContain('17 个通道')
    expect(html).toContain('视频收帧')
    expect(html).toContain('尚未检查')
    expect(html).toContain('最近检查结果')
    expect(html).toContain('结果过期')
    expect(html).not.toContain('辅码流')
    expect(html).not.toContain('el-tag--success')
    expect(html).not.toContain('在线状态')
    expect(html).toContain('最近检查')
    expect(html).not.toMatch(/>在线<|>离线<|首帧成功|流畅/)
    expect(html).not.toContain('媒体未检测；控制诊断仅验证 RTSP DESCRIBE。')
    expect(html).toContain('camera-card-grid')
    expect(html).not.toContain('channel-summary')
    expect(fetchMock).not.toHaveBeenCalled()
  })
  it('presents a no-Profile channel without inventing a stream or observation', async () => {
    const row = channel()
    row.profiles = []
    mock.rows = [row]
    const html = await renderPage()
    expect(html).toContain('无可用码流')
    expect(html).not.toContain('码流 ID')
    expect(html).not.toContain('stream-row')
  })
  it('renders only one stream summary without exposing profile names or adapter notes', async () => {
    const row = channel()
    const base = row.profiles[0]!
    row.profiles = ['SUB', 'MAIN', 'THIRD'].map((kind, index) => ({
      ...base,
      streamProfileId: String(index + 2),
      name: `${kind.toLowerCase()}Stream`,
      streamKind: kind,
      control: {
        ...base.control,
        observation: null,
        freshness: 'NEVER',
        canCheck: false,
        blockingReasonCode: 'UNSUPPORTED_LOCATOR',
      },
    }))
    mock.rows = [row]
    const html = await renderPage()
    expect(html.match(/class="video-state"/g)).toHaveLength(1)
    expect(html.match(/>详情</g)).toHaveLength(1)
    expect(html).not.toMatch(/mainStream|subStream|thirdStream|stream-row/)
    const card = html.slice(html.indexOf('<article'), html.indexOf('</article>'))
    expect(card).toContain('尚未检查')
    expect(card).not.toContain('检测待适配')
  })
  it('does not claim video normality from a current accessible CONTROL observation', async () => {
    const row = channel()
    row.profiles[0]!.control.freshness = 'CURRENT'
    mock.rows = [row]
    const html = await renderPage()
    expect(html).toContain('代表流可达')
    expect(html).toContain('尚未检查')
    expect(html).not.toMatch(/>在线<|>正常<|>离线</)
  })
  it('keeps disabled configuration separate from offline state', async () => {
    const row = channel()
    row.lifecycle = 'DISABLED'
    row.profiles[0]!.enabled = false
    mock.rows = [row]
    const html = await renderPage()
    expect(html).toContain('已停用')
    expect(html).not.toMatch(/>在线<|>离线<|>详情</)
  })
  it('initializes the search input from the management status link', async () => {
    mock.route.query = { name: '北门相机' }
    await renderPage()
    expect(mock.query?.name).toBe('北门相机')
    expect(mock.query?.layer).toBe('MEDIA')
  })
  it('distinguishes a failed page read from an empty filtered page', async () => {
    mock.error = new ApiRequestError('CONFLICT', '状态页码流数量超过限制', {
      status: 409,
      traceId: 'a'.repeat(32),
    })
    const html = await renderPage()
    expect(html).toContain('状态未读取，请重试刷新')
    expect(html).toContain('请缩小设备/通道搜索范围')
    expect(html).not.toContain('暂无符合条件的通道')
  })
})
