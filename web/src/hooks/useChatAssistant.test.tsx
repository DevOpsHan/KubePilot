import type { PropsWithChildren } from 'react'
import { act, renderHook, waitFor } from '@testing-library/react'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppI18n } from '@/i18n'
import { apiFetch } from '@/lib/api'
import { useChatAssistant } from './useChatAssistant'

vi.mock('@/lib/api', () => ({
  apiFetch: vi.fn(),
}))

const originalRequestAnimationFrame = window.requestAnimationFrame
const originalCancelAnimationFrame = window.cancelAnimationFrame
let nextFrameId = 1
let frameCallbacks = new Map<number, FrameRequestCallback>()

function flushAnimationFrames() {
  let timestamp = performance.now()
  for (let frame = 0; frame < 100 && frameCallbacks.size > 0; frame++) {
    const callbacks = Array.from(frameCallbacks.values())
    frameCallbacks.clear()
    timestamp += 1000
    callbacks.forEach((callback) => callback(timestamp))
  }
  if (frameCallbacks.size > 0) throw new Error('动画帧未在预期次数内完成')
}

function jsonResponse(body: object, status: number, statusText = ''): Response {
  return new Response(JSON.stringify(body), {
    status,
    statusText,
    headers: { 'Content-Type': 'application/json' },
  })
}

function sseResponse(events: ReadonlyArray<Record<string, unknown>>): Response {
  const payload = events.map((event) => `data: ${JSON.stringify(event)}\n\n`).join('')
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(new TextEncoder().encode(payload))
      controller.close()
    },
  })
  return new Response(stream, {
    status: 200,
    headers: { 'Content-Type': 'text/event-stream' },
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  nextFrameId = 1
  frameCallbacks = new Map()
  Object.defineProperty(window, 'requestAnimationFrame', {
    configurable: true,
    value: vi.fn((callback: FrameRequestCallback) => {
      const id = nextFrameId++
      frameCallbacks.set(id, callback)
      return id
    }),
  })
  Object.defineProperty(window, 'cancelAnimationFrame', {
    configurable: true,
    value: vi.fn((id: number) => frameCallbacks.delete(id)),
  })
})

afterEach(() => {
  frameCallbacks.clear()
  if (originalRequestAnimationFrame) {
    Object.defineProperty(window, 'requestAnimationFrame', {
      configurable: true,
      value: originalRequestAnimationFrame,
    })
  } else delete (window as Partial<Window>).requestAnimationFrame
  if (originalCancelAnimationFrame) {
    Object.defineProperty(window, 'cancelAnimationFrame', {
      configurable: true,
      value: originalCancelAnimationFrame,
    })
  } else delete (window as Partial<Window>).cancelAnimationFrame
})

describe('useChatAssistant 国际化', () => {
  it('保留非 2xx 状态并在语言切换后重新本地化', async () => {
    vi.mocked(apiFetch).mockResolvedValue(new Response(null, {
      status: 503,
      statusText: 'Service Unavailable',
    }))
    const i18n = await createAppI18n('zh')
    const wrapper = ({ children }: PropsWithChildren) => (
      <I18nextProvider i18n={i18n}>{children}</I18nextProvider>
    )
    const { result } = renderHook(() => useChatAssistant(), { wrapper })

    await act(async () => {
      await result.current.sendMessage('检查业务集群')
    })

    expect(result.current.messages[0].content).toBe('检查业务集群')
    expect(result.current.messages[1].content).toBe('⚠️ 出错了：服务暂时不可用')

    await act(() => i18n.changeLanguage('en'))

    expect(result.current.messages[0].content).toBe('检查业务集群')
    expect(result.current.messages[1].content).toBe('⚠️ Error: The service is temporarily unavailable')
  })

  it('保留原始超时异常并在语言切换后重新本地化', async () => {
    const timeoutError = Object.assign(new Error('timeout of 120000ms exceeded'), {
      code: 'ECONNABORTED',
      request: {},
    })
    vi.mocked(apiFetch).mockRejectedValue(timeoutError)
    const i18n = await createAppI18n('zh')
    const wrapper = ({ children }: PropsWithChildren) => (
      <I18nextProvider i18n={i18n}>{children}</I18nextProvider>
    )
    const { result } = renderHook(() => useChatAssistant(), { wrapper })

    await act(async () => {
      await result.current.sendMessage('检查超时')
    })
    expect(result.current.messages[1].content).toBe('⚠️ 出错了：请求超时')

    await act(() => i18n.changeLanguage('en'))
    expect(result.current.messages[1].content).toBe('⚠️ Error: The request timed out')
  })

  it('英文模式不会泄漏未知中文服务端错误', async () => {
    vi.mocked(apiFetch).mockResolvedValue(jsonResponse({ error: '上游发生未知故障' }, 500))
    const i18n = await createAppI18n('zh')
    const wrapper = ({ children }: PropsWithChildren) => (
      <I18nextProvider i18n={i18n}>{children}</I18nextProvider>
    )
    const { result } = renderHook(() => useChatAssistant(), { wrapper })

    await act(async () => {
      await result.current.sendMessage('检查未知错误')
    })
    expect(result.current.messages[1].content).toBe('⚠️ 出错了：上游发生未知故障')

    await act(() => i18n.changeLanguage('en'))
    expect(result.current.messages[1].content).toBe('⚠️ Error: The operation failed')
    expect(result.current.messages[1].content).not.toContain('上游发生未知故障')
  })

  it('历史消息和服务端错误详情保持原文，仅重译本地错误前缀', async () => {
    vi.mocked(apiFetch)
      .mockResolvedValueOnce(jsonResponse({
        messages: [{ role: 'assistant', content: '原始历史结论：Pod 正常' }],
      }, 200))
      .mockResolvedValueOnce(jsonResponse({
        error: 'upstream diagnostic code 42',
      }, 500))
    const i18n = await createAppI18n('zh')
    const wrapper = ({ children }: PropsWithChildren) => (
      <I18nextProvider i18n={i18n}>{children}</I18nextProvider>
    )
    const { result } = renderHook(() => useChatAssistant(), { wrapper })

    act(() => result.current.ensureSession())
    await act(async () => {
      await result.current.loadHistory()
    })
    expect(result.current.messages[0].content).toBe('原始历史结论：Pod 正常')

    await act(async () => {
      await result.current.sendMessage('检查上游')
    })
    expect(result.current.messages[2].content).toBe('⚠️ 出错了：upstream diagnostic code 42')

    await act(() => i18n.changeLanguage('en'))
    await waitFor(() => {
      expect(result.current.messages[0].content).toBe('原始历史结论：Pod 正常')
      expect(result.current.messages[2].content).toBe('⚠️ Error: upstream diagnostic code 42')
    })
  })

  it('真实 SSE 流保留独立空格和换行，并原样保存工具数据与 Markdown', async () => {
    vi.mocked(apiFetch).mockResolvedValue(sseResponse([
      {
        type: 'step',
        tool: ' kubectl ',
        command: 'kubectl get pods -n 业务空间',
        args: { namespace: '业务空间', output: 'wide' },
      },
      { type: 'step_done', duration_ms: 37 },
      { type: 'token', content: '**状态**' },
      { type: 'token', content: ' ' },
      { type: 'token', content: '\n' },
      { type: 'token', content: '正常' },
      { type: 'answer', duration_ms: 1250 },
      { type: 'done' },
    ]))
    const i18n = await createAppI18n('en')
    const wrapper = ({ children }: PropsWithChildren) => (
      <I18nextProvider i18n={i18n}>{children}</I18nextProvider>
    )
    const { result } = renderHook(() => useChatAssistant(), { wrapper })

    await act(async () => {
      await result.current.sendMessage('检查 SSE')
    })
    act(() => flushAnimationFrames())

    const assistant = result.current.messages[1]
    expect(assistant.content).toBe('**状态** \n正常')
    expect(assistant.totalMs).toBe(1250)
    expect(assistant.steps).toEqual([{
      tool: ' kubectl ',
      command: 'kubectl get pods -n 业务空间',
      args: { namespace: '业务空间', output: 'wide' },
      duration: 37,
    }])
    const rendered = result.current.renderMd(assistant.content)
    expect(rendered).toContain('<strong>状态</strong>')
    expect(rendered).toContain('<br>')
    expect(rendered).toContain('正常')
  })
})
