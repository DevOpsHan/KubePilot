import type { LocaleShape } from '../../types'
import type { zhDashboard } from '../zh/dashboard'

export const enDashboard = {
  page: {
    title: 'Operations overview',
    subtitle: 'Cluster health, pending work, and synchronization at a glance',
    refresh: 'Refresh',
    refreshing: 'Refreshing…',
  },
  statistics: {
    connectedClusters: 'Connected clusters',
    healthyClusters: 'Healthy clusters',
    nodes: 'Nodes',
    pods: 'Pods',
    pendingApprovals: 'Pending approvals',
    activeAlerts: 'Active alerts',
  },
  health: {
    title: 'Cluster health',
    viewAll: 'View all',
    clusterCount_one: '{{count}} Cluster',
    clusterCount_other: '{{count}} Clusters',
    healthy: 'Healthy',
    problem: 'Unhealthy',
    unknown: 'Unknown',
    empty: 'No clusters',
  },
  audit: {
    title: 'Recent activity',
    openCenter: 'Open audit log',
    empty: 'No recent activity',
  },
  sync: {
    title: 'Synchronization',
    running: 'Synchronization in progress',
    idle: 'Idle',
    lastFinished: 'Last completed',
    nextScheduled: 'Next scheduled',
  },
  quickActions: {
    title: 'Quick actions',
  },
  security: {
    title: 'Security status',
    configured: 'Configured',
    notConfigured: 'Not configured',
  },
  model: {
    title: 'AI model',
    manage: 'Manage',
    configured: 'Configured',
    notConfigured: 'No model configured',
    unavailable: 'Model status unavailable',
  },
  inspection: {
    title: 'Inspection summary',
    reports: 'Recent reports',
    anomalies: 'Recent anomalies',
    empty: 'No inspection records',
  },
} as const satisfies LocaleShape<typeof zhDashboard>
