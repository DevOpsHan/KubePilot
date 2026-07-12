import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import {
  CommandDialog, CommandInput, CommandList, CommandEmpty, CommandGroup, CommandItem,
} from '@/components/ui/command'
import { navItems, navTranslationKeys } from './nav'
import { useAuth } from '@/providers/AuthProvider'

export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const navigate = useNavigate()
  const { can } = useAuth()
  const { t } = useTranslation('common')

  return (
    <CommandDialog open={open} onOpenChange={onOpenChange}>
      <CommandInput placeholder={t('cmd.placeholder')} />
      <CommandList>
        <CommandEmpty>{t('cmd.empty')}</CommandEmpty>
        <CommandGroup heading={t('cmd.heading')}>
          {navItems.filter((i) => !i.permission || can(i.permission)).map((item) => {
            const Icon = item.icon
            const label = t(navTranslationKeys[item.key])
            return (
              <CommandItem key={item.to} value={label} onSelect={() => { navigate(item.to); onOpenChange(false) }}>
                <Icon className="size-4" /> {label}
              </CommandItem>
            )
          })}
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  )
}
