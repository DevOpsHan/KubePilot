import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { openPodExecStream } from '@/lib/api'

// PodTerminal 在 Pod 容器内开交互式终端:xterm 渲染 + WebSocket 双向流。
// - 服务端 → 客户端:二进制帧写入终端;文本帧为控制消息(error / exit)。
// - 客户端 → 服务端:onData 发 stdin 帧;尺寸变化发 resize 帧。
interface PodTerminalProps {
  clusterId: number
  namespace: string
  pod: string
  container?: string
  command?: string[]
}

type ConnState = 'connecting' | 'open' | 'closed'

export function PodTerminal({ clusterId, namespace, pod, container, command }: PodTerminalProps) {
  const { t } = useTranslation('clusters')
  const containerRef = useRef<HTMLDivElement | null>(null)
  const [state, setState] = useState<ConnState>('connecting')

  useEffect(() => {
    const host = containerRef.current
    if (!host) return

    const term = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
      theme: { background: '#0b0f14' },
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(host)
    try { fit.fit() } catch { /* 容器尺寸未就绪时忽略 */ }

    const encoder = new TextEncoder()
    const decoder = new TextDecoder()
    const ws = openPodExecStream(clusterId, namespace, pod, { container, command })
    ws.binaryType = 'arraybuffer'

    // 把当前终端尺寸发给服务端,触发远端 PTY resize。
    const sendResize = () => {
      if (ws.readyState !== WebSocket.OPEN) return
      ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
    }

    ws.onopen = () => {
      setState('open')
      term.focus()
      sendResize()
    }
    ws.onmessage = (ev) => {
      if (typeof ev.data === 'string') {
        // 文本帧:控制消息
        try {
          const msg = JSON.parse(ev.data)
          if (msg.type === 'error') term.write(`\r\n\x1b[31m${msg.message || 'error'}\x1b[0m\r\n`)
          else if (msg.type === 'exit') term.write(`\r\n\x1b[90m${t('terminal.sessionEnded')}\x1b[0m\r\n`)
        } catch { /* 非 JSON 文本,直接写入 */ term.write(ev.data) }
        return
      }
      // 二进制帧:stdout/stderr 原始字节
      term.write(decoder.decode(ev.data as ArrayBuffer))
    }
    ws.onclose = () => setState('closed')
    ws.onerror = () => setState('closed')

    // 键入 → stdin 帧
    const dataSub = term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'stdin', data }))
      }
    })

    // 容器尺寸变化 → 重新 fit + resize 帧
    const resizeObserver = new ResizeObserver(() => {
      try { fit.fit(); sendResize() } catch { /* 忽略 */ }
    })
    resizeObserver.observe(host)
    void encoder // 预留:未来若改用二进制 stdin 可用

    return () => {
      resizeObserver.disconnect()
      dataSub.dispose()
      try { ws.close() } catch { /* 忽略 */ }
      term.dispose()
    }
  }, [clusterId, namespace, pod, container, command, t])

  return (
    <div className="flex h-full flex-col gap-2">
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <span className={`size-2 rounded-full ${state === 'open' ? 'bg-success' : state === 'connecting' ? 'bg-warning' : 'bg-muted-foreground'}`} />
        <span>
          {state === 'open' ? t('terminal.connected') : state === 'connecting' ? t('terminal.connecting') : t('terminal.disconnected')}
        </span>
        <span className="ml-auto font-mono">{namespace}/{pod}{container ? `:${container}` : ''}</span>
      </div>
      <div ref={containerRef} className="min-h-[360px] flex-1 overflow-hidden rounded-lg bg-[#0b0f14] p-2" />
    </div>
  )
}
