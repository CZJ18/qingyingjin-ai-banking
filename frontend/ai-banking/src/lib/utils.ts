import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}
export function money(value: number | undefined | null) {
  return value == null
    ? '—'
    : new Intl.NumberFormat('zh-CN', { style: 'currency', currency: 'CNY' }).format(value / 100)
}
export function minor(text: string) {
  if (!/^\d+(\.\d{1,2})?$/.test(text)) throw new Error('金额最多保留两位小数')
  const [a, b = ''] = text.split('.')
  const v = Number(a) * 100 + Number(b.padEnd(2, '0'))
  if (!Number.isSafeInteger(v) || v > 1e12) throw new Error('金额超出范围')
  return v
}
export function date(value: number | undefined) {
  return value
    ? new Intl.DateTimeFormat('zh-CN', {
        timeZone: 'Asia/Shanghai',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        hour12: false,
      }).format(value)
    : '—'
}
export const statusLabels: Record<string, string> = {
  queued: '等待执行',
  pending: '待处理',
  running: '执行中',
  succeeded: '成功',
  success: '成功',
  executed: '已执行',
  failed: '失败',
  cancelled: '已取消',
  paused: '已暂停',
  awaiting_confirmation: '需要确认',
  pending_authorization: '待授权',
  waiting_confirmation: '需要确认',
  pending_approval: '待授权',
  prepared: '待授权',
  authorized: '已授权',
  expired: '已过期',
  active: '使用中',
  candidate: '待核实',
  rejected: '已拒绝',
  blocked: '被阻塞',
}
