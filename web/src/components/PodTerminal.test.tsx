import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const apiMocks = vi.hoisted(() => ({
  listPodContainers: vi.fn(),
  openPodExecStream: vi.fn(),
}))
const terminalMocks = vi.hoisted(() => ({ writes: [] as string[] }))
let language = 'zh'

vi.mock('@/lib/api', () => apiMocks)
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => `${language}:${key}`,
  }),
}))
vi.mock('@/components/ui/select', () => ({
  Select: ({ value, onValueChange, children }: {
    value: string
    onValueChange: (value: string) => void
    children: React.ReactNode
  }) => <select aria-label="container" value={value} onChange={(event) => onValueChange(event.target.value)}>{children}</select>,
  SelectTrigger: () => null,
  SelectValue: () => null,
  SelectContent: ({ children }: { children: React.ReactNode }) => children,
  SelectItem: ({ value, children }: { value: string; children: React.ReactNode }) => <option value={value}>{children}</option>,
}))
vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    cols = 80
    rows = 24
    loadAddon() {}
    open() {}
    focus() {}
    write(text: string) { terminalMocks.writes.push(text) }
    onData() { return { dispose() {} } }
    dispose() {}
  },
}))
vi.mock('@xterm/addon-fit', () => ({
  FitAddon: class { fit() {} },
}))

import { PodTerminal } from './PodTerminal'

interface TestSocket {
  binaryType: BinaryType
  readyState: number
  onopen: (() => void) | null
  onmessage: ((event: MessageEvent) => void) | null
  onclose: (() => void) | null
  onerror: (() => void) | null
  send: ReturnType<typeof vi.fn>
  close: ReturnType<typeof vi.fn>
}

function createSocket(): TestSocket {
  return {
    binaryType: 'blob',
    readyState: 1,
    onopen: null,
    onmessage: null,
    onclose: null,
    onerror: null,
    send: vi.fn(),
    close: vi.fn(),
  }
}

describe('PodTerminal', () => {
  const sockets: TestSocket[] = []

  afterEach(() => vi.unstubAllGlobals())

  beforeEach(() => {
    language = 'zh'
    sockets.length = 0
    terminalMocks.writes.length = 0
    apiMocks.listPodContainers.mockReset().mockResolvedValue({ containers: ['app', 'sidecar', 'init-db'] })
    apiMocks.openPodExecStream.mockReset().mockImplementation(() => {
      const socket = createSocket()
      sockets.push(socket)
      return socket as unknown as WebSocket
    })
    vi.stubGlobal('WebSocket', { OPEN: 1 })
    vi.stubGlobal('ResizeObserver', class {
      observe() {}
      disconnect() {}
    })
  })

  it('按后端顺序默认选择普通容器，并允许切换目标容器', async () => {
    render(<PodTerminal clusterId={1} namespace="default" pod="demo" />)

    await waitFor(() => expect(apiMocks.openPodExecStream).toHaveBeenCalledWith(
      1,
      'default',
      'demo',
      { container: 'app', command: undefined },
    ))
    expect(screen.getByRole('combobox')).toHaveValue('app')

    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'sidecar' } })

    await waitFor(() => expect(apiMocks.openPodExecStream).toHaveBeenLastCalledWith(
      1,
      'default',
      'demo',
      { container: 'sidecar', command: undefined },
    ))
    expect(sockets[0].close).toHaveBeenCalledOnce()
  })

  it('流式解码跨 WebSocket 消息拆分的 UTF-8 字符', async () => {
    render(<PodTerminal clusterId={1} namespace="default" pod="demo" />)
    await waitFor(() => expect(apiMocks.openPodExecStream).toHaveBeenCalledOnce())

    const bytes = new TextEncoder().encode('日志')
    act(() => {
      sockets[0].onmessage?.(new MessageEvent('message', { data: bytes.slice(0, 2).buffer }))
      sockets[0].onmessage?.(new MessageEvent('message', { data: bytes.slice(2).buffer }))
    })

    expect(terminalMocks.writes.join('')).toBe('日志')
    expect(terminalMocks.writes.join('')).not.toContain('\uFFFD')
  })

  it('切换界面语言不会重建终端连接', async () => {
    const { rerender } = render(<PodTerminal clusterId={1} namespace="default" pod="demo" />)
    await waitFor(() => expect(apiMocks.openPodExecStream).toHaveBeenCalledOnce())

    language = 'en'
    rerender(<PodTerminal clusterId={1} namespace="default" pod="demo" />)

    expect(apiMocks.openPodExecStream).toHaveBeenCalledOnce()
    expect(sockets[0].close).not.toHaveBeenCalled()
  })

  it('切换容器后旧 socket 的回调不会影响新终端', async () => {
    render(<PodTerminal clusterId={1} namespace="default" pod="demo" />)
    await waitFor(() => expect(apiMocks.openPodExecStream).toHaveBeenCalledOnce())
    const staleOpen = sockets[0].onopen
    const staleMessage = sockets[0].onmessage

    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'sidecar' } })
    await waitFor(() => expect(apiMocks.openPodExecStream).toHaveBeenCalledTimes(2))

    expect(sockets[0].onopen).toBeNull()
    expect(sockets[0].onmessage).toBeNull()
    act(() => {
      staleOpen?.()
      staleMessage?.(new MessageEvent('message', {
        data: new TextEncoder().encode('old socket output').buffer,
      }))
    })

    expect(screen.getByText('zh:terminal.connecting')).toBeInTheDocument()
    expect(terminalMocks.writes.join('')).not.toContain('old socket output')
  })

  it('切换 Pod 后会等待新容器列表再建立连接', async () => {
    let resolveContainers!: (value: { containers: string[] }) => void
    const nextContainers = new Promise<{ containers: string[] }>((resolve) => {
      resolveContainers = resolve
    })
    apiMocks.listPodContainers
      .mockReset()
      .mockResolvedValueOnce({ containers: ['app-a'] })
      .mockImplementationOnce(() => nextContainers)
    const { rerender } = render(<PodTerminal clusterId={1} namespace="default" pod="pod-a" />)
    await waitFor(() => expect(apiMocks.openPodExecStream).toHaveBeenCalledOnce())

    rerender(<PodTerminal clusterId={1} namespace="default" pod="pod-b" />)

    expect(apiMocks.openPodExecStream).toHaveBeenCalledOnce()
    await act(async () => resolveContainers({ containers: ['app-b'] }))
    await waitFor(() => expect(apiMocks.openPodExecStream).toHaveBeenLastCalledWith(
      1,
      'default',
      'pod-b',
      { container: 'app-b', command: undefined },
    ))
  })
})
