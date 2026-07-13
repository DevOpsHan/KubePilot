import axios, { type AxiosError } from 'axios'

export type AuthExpiredReason = 'unauthorized'

interface AuthExpiredDetail {
  reason: AuthExpiredReason
}

const authenticationPaths = new Set([
  '/auth/login',
  '/auth/change-password',
])

function isAuthenticationRequest(url: string): boolean {
  try {
    const parsedUrl = new URL(url, 'http://localhost')
    const normalizedPath = parsedUrl.pathname.replace(/\/+$/u, '') || '/'
    const apiRelativePath = normalizedPath.startsWith('/api/')
      ? normalizedPath.slice('/api'.length)
      : normalizedPath
    return authenticationPaths.has(apiRelativePath)
  } catch {
    return false
  }
}

function requestUrl(input: RequestInfo | URL): string {
  if (typeof input === 'string') return input
  return input instanceof URL ? input.href : input.url
}

// baseURL=/api,dev 由 vite 代理到后端 :8080,生产与后端同源
const http = axios.create({ baseURL: '/api', timeout: 120000, withCredentials: true })

http.interceptors.response.use(
  (response) => response,
  (error: AxiosError) => {
    const status = error.response?.status
    const url = error.config?.url || ''
    if (status === 401 && !isAuthenticationRequest(url)) {
      dispatchAuthExpired('unauthorized')
    }
    return Promise.reject(error)
  },
)

export function dispatchAuthExpired(reason: AuthExpiredReason = 'unauthorized'): void {
  window.dispatchEvent(new CustomEvent<AuthExpiredDetail>(
    'aiops-auth-expired',
    { detail: { reason } },
  ))
}

// 兼容 SSE/原生 fetch:401 也触发统一会话过期
export async function apiFetch(input: RequestInfo | URL, init: RequestInit = {}) {
  const response = await fetch(input, { credentials: 'same-origin', ...init })
  if (response.status === 401 && !isAuthenticationRequest(requestUrl(input))) {
    dispatchAuthExpired('unauthorized')
  }
  return response
}

type Dict = Record<string, unknown>
function withCluster(clusterId?: number | string) {
  return clusterId ? { params: { cluster_id: clusterId } } : {}
}
function compactParams(params: Dict) {
  return Object.fromEntries(
    Object.entries(params || {}).filter(
      ([, v]) => v !== undefined && v !== null && String(v).trim() !== '',
    ),
  )
}

// ---- 集群 ----
export async function listClusters() { return (await http.get('/clusters')).data }
export async function createCluster(payload: Dict) { return (await http.post('/clusters', payload)).data }
export async function updateCluster(id: number, payload: Dict) { return (await http.put(`/clusters/${id}`, payload)).data }
export async function deleteCluster(id: number) { return (await http.delete(`/clusters/${id}`)).data }
export async function syncAllClusters() { return (await http.post('/clusters/sync')).data }
export async function syncCluster(id: number) { return (await http.post(`/clusters/${id}/sync`)).data }
export async function getClusterSyncStatus() { return (await http.get('/clusters/sync/status')).data }
export async function listClusterSyncResources(id: number) { return (await http.get(`/clusters/${id}/sync-resources`)).data }
export async function listClusterResourceSnapshots(id: number, resource?: string) {
  return (await http.get(`/clusters/${id}/resource-snapshots`, resource ? { params: { resource } } : undefined)).data
}
export async function listClusterSyncTasks(id: number) { return (await http.get(`/clusters/${id}/sync-tasks`)).data }
export async function getClusterResourceObject(
  id: number,
  params: { apiVersion: string; resource: string; namespace?: string; name: string },
) {
  return (await http.get(`/clusters/${id}/resource-object`, {
    params: compactParams({
      api_version: params.apiVersion,
      resource: params.resource,
      namespace: params.namespace,
      name: params.name,
    }),
  })).data
}
export async function listClusterDeletedResources(id: number, resource?: string) {
  return (await http.get(`/clusters/${id}/deleted-resources`, resource ? { params: { resource } } : undefined)).data
}

// ---- 鉴权 / 用户 ----
export async function login(username: string, password: string) { return (await http.post('/auth/login', { username, password })).data }
export async function changePassword(username: string, currentPassword: string, newPassword: string) {
  return (await http.post('/auth/change-password', { username, current_password: currentPassword, new_password: newPassword })).data
}
export async function getCurrentUser() { return (await http.get('/auth/me')).data }
export async function logout() { return (await http.post('/auth/logout')).data }
export async function listUsers() { return (await http.get('/users')).data }
export async function createUser(payload: Dict) { return (await http.post('/users', payload)).data }
export async function updateUser(id: number, payload: Dict) { return (await http.put(`/users/${id}`, payload)).data }
export async function resetUserPassword(id: number) { return (await http.post(`/users/${id}/reset-password`)).data }
export async function deleteUser(id: number) { return (await http.delete(`/users/${id}`)).data }

// ---- 审计 ----
export async function listAudits(clusterId?: number, filters: { operator?: string; action?: string } = {}) {
  return (await http.get('/audits', { params: compactParams({ cluster_id: clusterId, operator: filters.operator, action: filters.action }) })).data
}

// ---- 模型 / 安全 / 看板 ----
export async function listModels() { return (await http.get('/models')).data }
export async function getModelStatus() { return (await http.get('/models/status')).data }
export async function getSecurityStatus() { return (await http.get('/security/status')).data }
export async function getDashboardSummary() { return (await http.get('/dashboard/summary')).data }
export async function createModel(payload: Dict) { return (await http.post('/models', payload)).data }
export async function updateModel(id: number, payload: Dict) { return (await http.put(`/models/${id}`, payload)).data }
export async function setDefaultModel(id: number) { return (await http.post(`/models/${id}/default`)).data }
export async function testModel(id: number) { return (await http.post(`/models/${id}/test`)).data }
export async function deleteModel(id: number) { return (await http.delete(`/models/${id}`)).data }

// ---- 集群只读 ----
export async function getOverview(clusterId?: number) { return (await http.get('/cluster/overview', withCluster(clusterId))).data }
export async function listNodes(clusterId?: number) { return (await http.get('/cluster/nodes', withCluster(clusterId))).data }
export async function listNodeMetrics(clusterId?: number) { return (await http.get('/cluster/node-metrics', withCluster(clusterId))).data }

// ---- Pod 交互(列表 / 容器 / 日志 / 终端) ----
export async function listPods(clusterId?: number, namespace?: string) {
  return (await http.get('/cluster/pods', { params: compactParams({ cluster_id: clusterId, namespace }) })).data
}
export async function listPodContainers(clusterId: number, namespace: string, pod: string) {
  return (await http.get(`/clusters/${clusterId}/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(pod)}/containers`)).data
}
// 一次性拉取日志(follow=false),返回纯文本
export async function fetchPodLogs(
  clusterId: number, namespace: string, pod: string,
  opts: { container?: string; tail?: number; timestamps?: boolean; previous?: boolean } = {},
): Promise<string> {
  const query = compactParams({
    container: opts.container, tail: opts.tail,
    timestamps: opts.timestamps ? 'true' : undefined,
    previous: opts.previous ? 'true' : undefined,
  })
  const search = new URLSearchParams(query as Record<string, string>).toString()
  const url = `/api/clusters/${clusterId}/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(pod)}/logs${search ? `?${search}` : ''}`
  const response = await apiFetch(url)
  if (!response.ok) {
    let message = `HTTP ${response.status}`
    try { const data = await response.json(); if (data?.error) message = data.error } catch { /* 非 JSON 响应,保留状态码 */ }
    throw new Error(message)
  }
  return response.text()
}

// 把 /api 相对路径拼成同源 WebSocket URL(ws/wss 随页面协议),依赖同源 cookie 鉴权
export function buildWsUrl(path: string, params: Record<string, string | number | undefined> = {}): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const search = new URLSearchParams(compactParams(params) as Record<string, string>).toString()
  return `${proto}//${window.location.host}${path}${search ? `?${search}` : ''}`
}
// 打开日志跟随流(follow=true):后端升级为 WebSocket,持续推送日志字节
export function openPodLogStream(
  clusterId: number, namespace: string, pod: string,
  opts: { container?: string; tail?: number; timestamps?: boolean } = {},
): WebSocket {
  const url = buildWsUrl(
    `/api/clusters/${clusterId}/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(pod)}/logs`,
    { container: opts.container, tail: opts.tail, follow: 'true', timestamps: opts.timestamps ? 'true' : undefined },
  )
  return new WebSocket(url)
}
// 打开 Pod 交互式终端(exec)WebSocket。command 支持多段 argv。
export function openPodExecStream(
  clusterId: number, namespace: string, pod: string,
  opts: { container?: string; command?: string[] } = {},
): WebSocket {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const search = new URLSearchParams()
  if (opts.container) search.set('container', opts.container)
  for (const arg of opts.command || []) search.append('command', arg)
  const qs = search.toString()
  const path = `/api/clusters/${clusterId}/pods/${encodeURIComponent(namespace)}/${encodeURIComponent(pod)}/exec`
  return new WebSocket(`${proto}//${window.location.host}${path}${qs ? `?${qs}` : ''}`)
}

// ---- 巡检 / 报告 ----
export async function runInspect(clusterId?: number) { return (await http.post('/inspect/run', null, withCluster(clusterId))).data }
export async function listReports(clusterId?: number) { return (await http.get('/inspect/reports', withCluster(clusterId))).data }
export async function getReport(id: number, clusterId?: number) { return (await http.get(`/inspect/reports/${id}`, withCluster(clusterId))).data }

// ---- 对话 ----
export async function sendChat(sessionId: string, message: string, clusterId?: number) {
  return (await http.post('/chat', { session_id: sessionId, message, cluster_id: clusterId })).data
}

// ---- 告警 ----
export async function listAlerts(filters: { status?: string; severity?: string; clusterId?: number | string } = {}) {
  return (await http.get('/alerts', { params: compactParams({ status: filters.status, severity: filters.severity, cluster_id: filters.clusterId }) })).data
}
export async function getAlertSummary() { return (await http.get('/alerts/summary')).data }
export async function getAlertTrends(days = 7, clusterId?: number | string) {
  return (await http.get('/alerts/trends', { params: compactParams({ days, cluster_id: clusterId }) })).data
}
export async function acknowledgeAlert(id: number, clusterId?: number) { return (await http.post(`/alerts/${id}/acknowledge`, null, withCluster(clusterId))).data }
export async function resolveAlert(id: number, clusterId?: number) { return (await http.post(`/alerts/${id}/resolve`, null, withCluster(clusterId))).data }
export async function diagnoseAlert(id: number, clusterId?: number) { return (await http.post(`/alerts/${id}/diagnose`, null, withCluster(clusterId))).data }
export async function remediateAlert(id: number, clusterId?: number) { return (await http.post(`/alerts/${id}/remediate`, null, withCluster(clusterId))).data }

// ---- 静默 ----
export async function listSilences() { return (await http.get('/silences')).data }
export async function createSilence(payload: Dict) { return (await http.post('/silences', payload)).data }
export async function deleteSilence(id: number) { return (await http.delete(`/silences/${id}`)).data }

// ---- 通知设置 ----
export async function getNotificationSettings() { return (await http.get('/notifications/settings')).data }
export async function saveNotificationSettings(payload: Dict) { return (await http.put('/notifications/settings', payload)).data }
export async function testNotification() { return (await http.post('/notifications/test')).data }

// ---- 巡检规则(三层) ----
export async function getInspectionRules(clusterId?: number) { return (await http.get('/inspection/rules', withCluster(clusterId))).data }
export async function updateInspectionRule(name: string, payload: Dict, clusterId?: number) { return (await http.put(`/inspection/rules/${name}`, payload, withCluster(clusterId))).data }
export async function resetInspectionRule(name: string, clusterId?: number) { return (await http.delete(`/inspection/rules/${name}`, withCluster(clusterId))).data }
export async function updateClusterPrometheus(clusterId: number, url: string) { return (await http.put('/inspection/prometheus', { url }, withCluster(clusterId))).data }

// ---- 自定义指标规则 ----
export async function listCustomRules(clusterId?: number) { return (await http.get('/inspection/custom-rules', withCluster(clusterId))).data }
export async function createCustomRule(payload: Dict, clusterId?: number) { return (await http.post('/inspection/custom-rules', payload, withCluster(clusterId))).data }
export async function updateCustomRule(id: number, payload: Dict) { return (await http.put(`/inspection/custom-rules/${id}`, payload)).data }
export async function deleteCustomRule(id: number) { return (await http.delete(`/inspection/custom-rules/${id}`)).data }
export async function testCustomRule(payload: Dict, clusterId?: number) { return (await http.post('/inspection/custom-rules/test', payload, withCluster(clusterId))).data }
