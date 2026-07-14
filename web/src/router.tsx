import { createHashRouter, Navigate } from 'react-router-dom'
import type { ReactNode } from 'react'
import { AppLayout } from '@/components/layout/AppLayout'
import { useAuth } from '@/providers/AuthProvider'
import { HomePage } from '@/pages/HomePage'
import { DashboardPage } from '@/pages/DashboardPage'
import { ChatPage } from '@/pages/ChatPage'
import { ClustersPage } from '@/pages/ClustersPage'
import { ClusterDetailPage } from '@/pages/ClusterDetailPage'
import { NodePage } from '@/pages/NodePage'
import { ModelsPage } from '@/pages/ModelsPage'
import { AlertsPage } from '@/pages/AlertsPage'
import { InspectReportPage } from '@/pages/InspectReportPage'
import { ApprovalPage } from '@/pages/ApprovalPage'
import { AuditPage } from '@/pages/AuditPage'
import { UsersPage } from '@/pages/UsersPage'
import { InspectionSettingsPage } from '@/pages/InspectionSettingsPage'

// 权限守卫:无权限则回到总览
function Guard({ permission, children }: { permission?: string; children: ReactNode }) {
  const { can } = useAuth()
  if (permission && !can(permission)) return <Navigate to="/home" replace />
  return <>{children}</>
}

export const router = createHashRouter([
  {
    path: '/',
    element: <AppLayout />,
    children: [
      { index: true, element: <Navigate to="/home" replace /> },
      { path: 'home', element: <HomePage /> },
      { path: 'dashboard', element: <DashboardPage /> },
      { path: 'chat', element: <ChatPage /> },
      { path: 'clusters', element: <ClustersPage /> },
      { path: 'clusters/:id', element: <ClusterDetailPage /> },
      { path: 'clusters/:id/nodes', element: <NodePage /> },
      { path: 'models', element: <ModelsPage /> },
      { path: 'alerts', element: <Guard permission="alerts:view"><AlertsPage /></Guard> },
      { path: 'inspect-report', element: <Guard permission="inspect:run"><InspectReportPage /></Guard> },
      { path: 'approval', element: <ApprovalPage /> },
      { path: 'audit', element: <Guard permission="approvals:view"><AuditPage /></Guard> },
      { path: 'users', element: <Guard permission="users:manage"><UsersPage /></Guard> },
      { path: 'inspection-settings', element: <Guard permission="inspection:manage"><InspectionSettingsPage /></Guard> },
      { path: '*', element: <Navigate to="/home" replace /> },
    ],
  },
])
