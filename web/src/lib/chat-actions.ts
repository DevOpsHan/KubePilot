import { LayoutDashboard, Zap, Box, Server } from 'lucide-react'

// 对话快捷动作(总览页与对话页共用)
export const quickActions = [
  { key: 'overview', icon: LayoutDashboard },
  { key: 'inspect', icon: Zap },
  { key: 'unhealthyPods', icon: Box },
  { key: 'nodeStatus', icon: Server },
] as const
