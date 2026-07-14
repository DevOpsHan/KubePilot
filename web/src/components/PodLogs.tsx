import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2, Pause, Play, RotateCw } from 'lucide-react'
import { fetchPodLogs, listPodContainers, openPodLogStream } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from '@/components/ui/select'
import { cn } from '@/lib/utils'
import { appendBoundedText, decodeUtf8Chunk, flushUtf8Decoder } from '@/lib/stream-text'

// PodLogs 查看 Pod 日志:容器选择 + 尾部行数 + 跟随开关。
// - 非跟随:一次性 fetchPodLogs 拉取文本。
// - 跟随:openPodLogStream 打开 WebSocket 持续追加,自动贴底。
// 组件自获取容器列表(多容器 Pod 时显示选择器),对调用方自包含。
interface PodLogsProps {
  clusterId: number
  namespace: string
  pod: string
}

const TAIL_OPTIONS = [100, 500, 1000, 5000]

export function PodLogs({ clusterId, namespace, pod }: PodLogsProps) {
  const { t } = useTranslation('clusters')
  const podKey = `${clusterId}\u0000${namespace}\u0000${pod}`
  const [containers, setContainers] = useState<string[]>([])
  const [container, setContainer] = useState('')
  const [containersReadyFor, setContainersReadyFor] = useState('')
  const [tail, setTail] = useState(500)
  const [follow, setFollow] = useState(false)
  const [content, setContent] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const scrollRef = useRef<HTMLDivElement | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const loadSeq = useRef(0)
  const textRef = useRef({ empty: '', streamError: '' })
  textRef.current = { empty: t('logs.empty'), streamError: t('logs.streamError') }
  const containersReady = containersReadyFor === podKey

  const scrollToBottom = useCallback(() => {
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [])

  // 关闭跟随流(切换容器 / 关跟随 / 卸载时调用)
  const closeStream = useCallback(() => {
    if (wsRef.current) {
      try { wsRef.current.close() } catch { /* 忽略 */ }
      wsRef.current = null
    }
  }, [])

  // 拉取容器列表:多容器 Pod 时显示选择器,默认选第一个
  useEffect(() => {
    let cancelled = false
    loadSeq.current += 1
    setContainers([])
    setContainer('')
    setContainersReadyFor('')
    listPodContainers(clusterId, namespace, pod)
      .then((res) => {
        if (cancelled) return
        const names: string[] = res.containers || []
        setContainers(names)
        setContainer(names[0] || '')
      })
      .catch(() => { /* 容器列表拉取失败不阻断日志:留空走默认容器 */ })
      .finally(() => {
        if (!cancelled) setContainersReadyFor(podKey)
      })
    return () => { cancelled = true }
  }, [clusterId, namespace, pod, podKey])

  // 一次性加载
  const loadOnce = useCallback(async () => {
    if (!containersReady) return
    const seq = ++loadSeq.current
    setLoading(true); setError('')
    try {
      const text = await fetchPodLogs(clusterId, namespace, pod, { container, tail })
      if (seq !== loadSeq.current) return
      setContent(appendBoundedText('', text || textRef.current.empty))
      requestAnimationFrame(scrollToBottom)
    } catch (e) {
      if (seq === loadSeq.current) setError(e instanceof Error ? e.message : String(e))
    } finally {
      if (seq === loadSeq.current) setLoading(false)
    }
  }, [clusterId, namespace, pod, container, tail, containersReady, scrollToBottom])

  // 跟随流首批包含指定历史尾部，因此先清空一次性快照再持续追加。
  useEffect(() => {
    if (!follow || !containersReady) { closeStream(); return }
    setError('')
    setContent('')
    const decoder = new TextDecoder()
    const ws = openPodLogStream(clusterId, namespace, pod, { container, tail })
    let active = true
    ws.binaryType = 'arraybuffer'
    wsRef.current = ws
    ws.onmessage = (ev) => {
      if (!active) return
      const chunk = typeof ev.data === 'string' ? ev.data : decodeUtf8Chunk(decoder, ev.data as ArrayBuffer)
      setContent((prev) => appendBoundedText(prev, chunk))
      requestAnimationFrame(scrollToBottom)
    }
    ws.onclose = () => {
      if (!active) return
      active = false
      const tailChunk = flushUtf8Decoder(decoder)
      if (tailChunk) setContent((prev) => appendBoundedText(prev, tailChunk))
      if (wsRef.current === ws) wsRef.current = null
      setFollow(false)
    }
    ws.onerror = () => {
      if (active) setError(textRef.current.streamError)
    }
    return () => {
      active = false
      ws.onclose = null
      ws.onmessage = null
      ws.onerror = null
      closeStream()
    }
  }, [follow, clusterId, namespace, pod, container, tail, containersReady, closeStream, scrollToBottom])

  // 非跟随时,首次与参数变化即加载
  useEffect(() => {
    if (follow || !containersReady) return
    setContent('')
    void loadOnce()
    return () => { loadSeq.current += 1 }
  }, [follow, containersReady, loadOnce])

  useEffect(() => () => closeStream(), [closeStream])

  return (
    <div className="flex h-full flex-col gap-3">
      <div className="flex flex-wrap items-center gap-3">
        {containers.length > 1 && (
          <Select value={container} onValueChange={(v) => setContainer(v)}>
            <SelectTrigger className="h-8 w-44"><SelectValue placeholder={t('logs.selectContainer')} /></SelectTrigger>
            <SelectContent>
              {containers.map((name) => <SelectItem key={name} value={name}>{name}</SelectItem>)}
            </SelectContent>
          </Select>
        )}
        <Select value={String(tail)} onValueChange={(v) => setTail(Number(v))}>
          <SelectTrigger className="h-8 w-32"><SelectValue /></SelectTrigger>
          <SelectContent>
            {TAIL_OPTIONS.map((n) => <SelectItem key={n} value={String(n)}>{t('logs.tailLines', { count: n })}</SelectItem>)}
          </SelectContent>
        </Select>
        <label className="flex items-center gap-2 text-sm">
          <Switch checked={follow} onCheckedChange={setFollow} />
          <span className="flex items-center gap-1">
            {follow ? <Pause className="size-3.5" /> : <Play className="size-3.5" />}
            {t('logs.follow')}
          </span>
        </label>
        {!follow && (
          <Button variant="outline" size="sm" onClick={loadOnce} disabled={loading}>
            <RotateCw className={cn('size-4', loading && 'animate-spin')} /> {t('logs.refresh')}
          </Button>
        )}
        {loading && <Loader2 className="size-4 animate-spin text-muted-foreground" />}
      </div>
      {error && <div className="rounded-lg bg-danger-weak px-3 py-2 text-sm text-danger-fg">{error}</div>}
      <div
        ref={scrollRef}
        className="min-h-[360px] flex-1 overflow-auto rounded-lg bg-[#0b0f14] p-3 font-mono text-xs leading-relaxed text-slate-200"
      >
        <pre className="whitespace-pre-wrap break-all">{content}</pre>
      </div>
    </div>
  )
}
