import { createContext, useCallback, useContext, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter, DialogDescription } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'

interface ConfirmOptions {
  title?: string
  message?: string
  confirmText?: string
  cancelText?: string
  tone?: 'primary' | 'danger'
}

type ConfirmFn = (opts: ConfirmOptions) => Promise<boolean>
const Ctx = createContext<ConfirmFn>(async () => false)

export function ConfirmProvider({ children }: { children: ReactNode }) {
  const { t } = useTranslation('common')
  const [open, setOpen] = useState(false)
  const [opts, setOpts] = useState<ConfirmOptions>({})
  const resolver = useRef<(v: boolean) => void>(() => {})

  const confirm = useCallback<ConfirmFn>((o) => {
    setOpts(o)
    setOpen(true)
    return new Promise<boolean>((resolve) => { resolver.current = resolve })
  }, [])

  function done(v: boolean) {
    setOpen(false)
    resolver.current(v)
  }

  return (
    <Ctx.Provider value={confirm}>
      {children}
      <Dialog open={open} onOpenChange={(o) => { if (!o) done(false) }}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{opts.title ?? t('confirm.title')}</DialogTitle>
            {opts.message && <DialogDescription className="pt-1 leading-relaxed">{opts.message}</DialogDescription>}
          </DialogHeader>
          <DialogFooter className="mt-2">
            <Button variant="outline" onClick={() => done(false)}>
              {opts.cancelText ?? t('actions.cancel')}
            </Button>
            <Button variant={opts.tone === 'danger' ? 'destructive' : 'default'} onClick={() => done(true)}>
              {opts.confirmText ?? t('actions.confirm')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Ctx.Provider>
  )
}

export const useConfirm = () => useContext(Ctx)
