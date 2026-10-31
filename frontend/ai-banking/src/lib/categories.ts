// Read-side display aliases mirror the existing Go category contract. Never
// rewrite original transaction categories or use these labels for accounting.
const groups: Record<string, { label: string; aliases: string[] }> = {
  dining: { label: '餐饮', aliases: ['dining','food','餐饮','饮食','groceries','食品'] },
  transport: { label: '交通', aliases: ['transport','transportation','交通'] },
  shopping: { label: '购物', aliases: ['shopping','购物'] },
  sports: { label: '运动', aliases: ['sports','exercise','fitness','运动'] },
  subscriptions: { label: '订阅', aliases: ['subscription','subscriptions','订阅'] },
  housing: { label: '住房', aliases: ['housing','住房','rent'] },
  medical: { label: '医疗', aliases: ['medical','healthcare','医疗'] },
  education: { label: '教育', aliases: ['education','教育'] },
  other: { label: '其他', aliases: ['other','其他'] },
  uncategorized: { label: '未分类', aliases: ['uncategorized'] },
}
function group(value?: string) {
  const normalized = value?.trim().toLowerCase() || ''
  return Object.entries(groups).find(([, group]) => group.aliases.includes(normalized))
}
export function canonicalCategory(value?: string): string { return group(value)?.[0] || 'uncategorized' }
const balanceLabels: Record<string,string> = { opening: '期初余额', salary: '工资收入', internal_transfer: '账户内部转账' }
export function categoryLabel(value?: string): string {
  return group(value)?.[1].label || balanceLabels[value?.trim().toLowerCase() || ''] || value?.trim() || '未分类'
}
export function transactionKindLabel(value?: string): string {
  const labels: Record<string,string> = {
    opening: '期初余额（不计入收支）',
    internal_transfer: '账户内部转账（不计入收支）',
    external: '外部收支',
  }
  return labels[value || ''] || value || '未知类型'
}
