import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loader2, Pause, Play, RotateCw } from 'lucide-react'
import { fetchPodLogs, listPodContainers, openPodLogStream } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from '@/components/ui/select'
import { cn } from '@/lib/utils'

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
  const [containers, setContainers] = useState<string[]>([])
  const [container, setContainer] = useState('')
  const [tail, setTail] = useState(500)
  const [follow, setFollow] = useState(false)
  const [content, setContent] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const scrollRef = useRef<HTMLDivElement | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const decoderRef = useRef(new TextDecoder())

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
    listPodContainers(clusterId, namespace, pod)
      .then((res) => {
        if (cancelled) return
        const names: string[] = res.containers || []
        setContainers(names)
        setContainer((prev) => prev || names[0] || '')
      })
      .catch(() => { /* 容器列表拉取失败不阻断日志:留空走默认容器 */ })
    return () => { cancelled = true }
  }, [clusterId, namespace, pod])

  // 一次性加载
  const loadOnce = useCallback(async () => {
    setLoading(true); setError('')
    try {
      const text = await fetchPodLogs(clusterId, namespace, pod, { container, tail })
      setContent(text || t('logs.empty'))
      requestAnimationFrame(scrollToBottom)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
    }
  }, [clusterId, namespace, pod, container, tail, t, scrollToBottom])

  // 跟随模式:先拉一段历史尾部,再打开 WS 追加
  useEffect(() => {
    if (!follow) { closeStream(); return }
    setError('')
    const ws = openPodLogStream(clusterId, namespace, pod, { container, tail })
    ws.binaryType = 'arraybuffer'
    wsRef.current = ws
    ws.onmessage = (ev) => {
      const chunk = typeof ev.data === 'string' ? ev.data : decoderRef.current.decode(ev.data as ArrayBuffer)
      setContent((prev) => prev + chunk)
      requestAnimationFrame(scrollToBottom)
    }
    ws.onerror = () => setError(t('logs.streamError'))
    return () => { closeStream() }
  }, [follow, clusterId, namespace, pod, container, tail, closeStream, scrollToBottom, t])

  // 非跟随时,首次与参数变化即加载
  useEffect(() => {
    if (follow) return
    setContent('')
    loadOnce()
  }, [follow, loadOnce])

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
