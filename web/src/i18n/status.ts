import type { TFunction } from 'i18next'

const statusTranslationKeys = {
  cluster: {
    unknown: 'status.cluster.unknown',
    imported: 'status.cluster.imported',
    syncing: 'status.cluster.syncing',
    ready: 'status.cluster.ready',
    degraded: 'status.cluster.degraded',
    disconnected: 'status.cluster.disconnected',
    error: 'status.cluster.error',
  },
  import: {
    imported: 'status.import.imported',
    success: 'status.import.success',
    failed: 'status.import.failed',
    error: 'status.import.error',
  },
  sync: {
    success: 'status.sync.success',
    failed: 'status.sync.failed',
    running: 'status.sync.running',
    'n/a': 'status.sync.notApplicable',
    not_run: 'status.sync.notRun',
  },
  alert: {
    active: 'status.alert.active',
    acknowledged: 'status.alert.acknowledged',
    resolved: 'status.alert.resolved',
  },
  severity: {
    critical: 'status.severity.critical',
    warning: 'status.severity.warning',
    info: 'status.severity.info',
  },
  approval: {
    pending: 'status.approval.pending',
    approved: 'status.approval.approved',
    rejected: 'status.approval.rejected',
    executed: 'status.approval.executed',
    failed: 'status.approval.failed',
  },
  execution: {
    pending: 'status.execution.pending',
    running: 'status.execution.running',
    success: 'status.execution.success',
    failed: 'status.execution.failed',
  },
  verification: {
    pending: 'status.verification.pending',
    healthy: 'status.verification.healthy',
    failing: 'status.verification.failing',
    skipped: 'status.verification.skipped',
  },
  role: {
    global_admin: 'status.role.globalAdmin',
    cluster_admin: 'status.role.clusterAdmin',
  },
  userStatus: {
    active: 'status.userStatus.active',
    disabled: 'status.userStatus.disabled',
  },
  model: {
    ready: 'status.model.ready',
    unavailable: 'status.model.unavailable',
    enabled: 'status.model.enabled',
    disabled: 'status.model.disabled',
    default: 'status.model.default',
  },
  ruleSource: {
    builtin: 'status.ruleSource.builtin',
    prometheus: 'status.ruleSource.prometheus',
    custom: 'status.ruleSource.custom',
  },
  source: {
    manual: 'status.source.manual',
    auto: 'status.source.auto',
    inspect: 'status.source.inspect',
    import: 'status.source.import',
    database: 'status.source.database',
    runtime: 'status.source.runtime',
    file: 'status.source.file',
    cluster: 'status.source.cluster',
    global: 'status.source.global',
    none: 'status.source.none',
  },
} as const

export type StatusCategory = keyof typeof statusTranslationKeys

type StatusTranslationKey = {
  [Category in StatusCategory]:
    (typeof statusTranslationKeys)[Category][keyof (typeof statusTranslationKeys)[Category]]
}[StatusCategory]

const translationKeysByCategory: Readonly<
  Record<StatusCategory, Readonly<Record<string, StatusTranslationKey>>>
> = statusTranslationKeys

export function localizeStatus(
  t: TFunction<'common'>,
  category: StatusCategory,
  value?: string | null,
): string {
  if (value === null || value === undefined || value.trim() === '') {
    return t('common.notAvailable')
  }

  const translationKey = translationKeysByCategory[category][value]
  return translationKey ? t(translationKey) : value
}
