import { RouterProvider } from 'react-router-dom'
import { Loader2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { ThemeProvider } from '@/providers/ThemeProvider'
import { AuthProvider, useAuth, type SessionReason } from '@/providers/AuthProvider'
import { ClusterProvider } from '@/providers/ClusterProvider'
import { ConfirmProvider } from '@/providers/ConfirmProvider'
import { LoginPage } from '@/pages/LoginPage'
import { router } from '@/router'
import { Toaster } from '@/components/ui/sonner'
import { TooltipProvider } from '@/components/ui/tooltip'
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'

// 会话过期弹窗:空闲超时或 401 时展示,确认后回登录页
function sessionReasonKey(reason: SessionReason): 'session.idle' | 'session.unauthorized' {
  return reason === 'idle' ? 'session.idle' : 'session.unauthorized'
}

function SessionExpiredDialog() {
  const { expiredReason, dismissExpired } = useAuth()
  const { t } = useTranslation('auth')
  return (
    <Dialog open={Boolean(expiredReason)} onOpenChange={(o) => { if (!o) dismissExpired() }}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('session.title')}</DialogTitle>
          <DialogDescription className="pt-1">
            {expiredReason ? t(sessionReasonKey(expiredReason)) : ''}
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button onClick={dismissExpired}>{t('session.relogin')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// 鉴权门:加载中显示启动态,未登录显示登录页,已登录进入外壳路由
function AppGate() {
  const { authenticated, loading } = useAuth()
  const { t } = useTranslation('common')
  if (loading) {
    return (
      <div className="grid min-h-screen place-items-center bg-background text-muted-foreground">
        <div className="flex items-center gap-2 text-sm">
          <Loader2 className="size-4 animate-spin" /> {t('common.loading')}
        </div>
      </div>
    )
  }
  if (!authenticated) return <LoginPage />
  return (
    <>
      <RouterProvider router={router} />
      <SessionExpiredDialog />
    </>
  )
}

function App() {
  return (
    <ThemeProvider>
      <AuthProvider>
        <ClusterProvider>
          <ConfirmProvider>
            <TooltipProvider>
              <AppGate />
            </TooltipProvider>
            <Toaster position="top-center" richColors closeButton />
          </ConfirmProvider>
        </ClusterProvider>
      </AuthProvider>
    </ThemeProvider>
  )
}

export default App
