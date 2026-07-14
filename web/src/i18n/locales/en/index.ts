import type { LocaleShape } from '../../types'
import type { zhResource } from '../zh'
import { enAuth } from './auth'
import { enClusters } from './clusters'
import { enCommon } from './common'
import { enDashboard } from './dashboard'
import { enHome } from './home'
import { enOperations } from './operations'
import { enSettings } from './settings'

export const enResource = {
  common: enCommon,
  auth: enAuth,
  home: enHome,
  dashboard: enDashboard,
  clusters: enClusters,
  operations: enOperations,
  settings: enSettings,
} as const satisfies LocaleShape<typeof zhResource>
