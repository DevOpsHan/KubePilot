import { toast } from 'sonner'

// 与旧 useToast 一致的语义封装
export const notify = {
  success: (msg: string) => toast.success(msg),
  error: (msg: string) => toast.error(msg),
  info: (msg: string) => toast(msg),
}
