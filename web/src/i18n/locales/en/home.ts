import type { LocaleShape } from '../../types'
import type { zhHome } from '../zh/home'

export const enHome = {
  welcome: 'Welcome to the KubePilot AIOps platform!',
  subtitle: 'Multi-cluster management, AI-driven diagnosis, and smart inspection — all in one place.',
  brand: 'KubePilot · Kubernetes AIOps Platform',
} as const satisfies LocaleShape<typeof zhHome>
