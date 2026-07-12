import { zhAuth } from './auth'
import { zhClusters } from './clusters'
import { zhCommon } from './common'
import { zhDashboard } from './dashboard'
import { zhOperations } from './operations'
import { zhSettings } from './settings'

export const zhResource = {
  common: zhCommon,
  auth: zhAuth,
  dashboard: zhDashboard,
  clusters: zhClusters,
  operations: zhOperations,
  settings: zhSettings,
} as const
