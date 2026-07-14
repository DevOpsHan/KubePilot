import { describe, expect, it } from 'vitest'
import { createAppI18n } from './index'
import { localizeStatus, type StatusCategory } from './status'

const englishStatuses: ReadonlyArray<{
  category: StatusCategory
  value: string
  expected: string
}> = [
  { category: 'cluster', value: 'unknown', expected: 'Unknown' },
  { category: 'cluster', value: 'imported', expected: 'Imported' },
  { category: 'cluster', value: 'syncing', expected: 'Syncing' },
  { category: 'cluster', value: 'ready', expected: 'Ready' },
  { category: 'cluster', value: 'degraded', expected: 'Degraded' },
  { category: 'cluster', value: 'disconnected', expected: 'Disconnected' },
  { category: 'cluster', value: 'error', expected: 'Error' },
  { category: 'import', value: 'imported', expected: 'Imported' },
  { category: 'import', value: 'success', expected: 'Success' },
  { category: 'import', value: 'failed', expected: 'Failed' },
  { category: 'import', value: 'error', expected: 'Error' },
  { category: 'sync', value: 'success', expected: 'Success' },
  { category: 'sync', value: 'failed', expected: 'Failed' },
  { category: 'sync', value: 'running', expected: 'Running' },
  { category: 'sync', value: 'n/a', expected: 'Not applicable' },
  { category: 'sync', value: 'not_run', expected: 'Not run' },
  { category: 'alert', value: 'active', expected: 'Active' },
  { category: 'alert', value: 'acknowledged', expected: 'Acknowledged' },
  { category: 'alert', value: 'resolved', expected: 'Resolved' },
  { category: 'severity', value: 'critical', expected: 'Critical' },
  { category: 'severity', value: 'warning', expected: 'Warning' },
  { category: 'severity', value: 'info', expected: 'Info' },
  { category: 'approval', value: 'pending', expected: 'Pending' },
  { category: 'approval', value: 'approved', expected: 'Approved' },
  { category: 'approval', value: 'rejected', expected: 'Rejected' },
  { category: 'approval', value: 'executed', expected: 'Executed' },
  { category: 'approval', value: 'failed', expected: 'Failed' },
  { category: 'execution', value: 'pending', expected: 'Pending' },
  { category: 'execution', value: 'running', expected: 'Running' },
  { category: 'execution', value: 'success', expected: 'Success' },
  { category: 'execution', value: 'failed', expected: 'Failed' },
  { category: 'verification', value: 'pending', expected: 'Pending' },
  { category: 'verification', value: 'healthy', expected: 'Healthy' },
  { category: 'verification', value: 'failing', expected: 'Failing' },
  { category: 'verification', value: 'skipped', expected: 'Skipped' },
  { category: 'role', value: 'global_admin', expected: 'Global admin' },
  { category: 'role', value: 'cluster_admin', expected: 'Cluster admin' },
  { category: 'role', value: 'inspector', expected: 'Inspector' },
  { category: 'userStatus', value: 'active', expected: 'Active' },
  { category: 'userStatus', value: 'disabled', expected: 'Disabled' },
  { category: 'model', value: 'ready', expected: 'Ready' },
  { category: 'model', value: 'unavailable', expected: 'Unavailable' },
  { category: 'model', value: 'enabled', expected: 'Enabled' },
  { category: 'model', value: 'disabled', expected: 'Disabled' },
  { category: 'model', value: 'default', expected: 'Default' },
  { category: 'ruleSource', value: 'builtin', expected: 'Built-in' },
  { category: 'ruleSource', value: 'prometheus', expected: 'Prometheus' },
  { category: 'ruleSource', value: 'custom', expected: 'Custom' },
  { category: 'source', value: 'manual', expected: 'Manual' },
  { category: 'source', value: 'auto', expected: 'Automatic' },
  { category: 'source', value: 'inspect', expected: 'Inspection' },
  { category: 'source', value: 'import', expected: 'Import' },
  { category: 'source', value: 'database', expected: 'Database' },
  { category: 'source', value: 'runtime', expected: 'Runtime' },
  { category: 'source', value: 'file', expected: 'File' },
  { category: 'source', value: 'cluster', expected: 'Cluster' },
  { category: 'source', value: 'global', expected: 'Global' },
  { category: 'source', value: 'none', expected: 'None' },
]

describe('localizeStatus', () => {
  it('翻译所有公共状态类别和值', async () => {
    const instance = await createAppI18n('en')
    const t = instance.getFixedT('en', 'common')

    for (const { category, value, expected } of englishStatuses) {
      expect(localizeStatus(t, category, value), `${category}.${value}`).toBe(expected)
    }
  })

  it('中文使用词典中的已知状态', async () => {
    const instance = await createAppI18n('zh')
    const t = instance.getFixedT('zh', 'common')

    expect(localizeStatus(t, 'cluster', 'ready')).toBe('就绪')
    expect(localizeStatus(t, 'role', 'global_admin')).toBe('全局管理员')
  })

  it('未知状态保持原样且不伪造翻译', async () => {
    const instance = await createAppI18n('en')
    const t = instance.getFixedT('en', 'common')

    expect(localizeStatus(t, 'cluster', 'vendor-new-state')).toBe('vendor-new-state')
  })

  it.each([undefined, null, ''])('空状态使用公共不可用文案：%s', async (value) => {
    const instance = await createAppI18n('en')
    const t = instance.getFixedT('en', 'common')

    expect(localizeStatus(t, 'cluster', value)).toBe('Not available')
  })
})
