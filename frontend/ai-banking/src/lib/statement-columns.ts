// Header selection only. The Go preview remains authoritative for row parsing,
// decimal amounts, provenance and all import authorization.
export const statementFields = [
  { key: 'external_id', label: '交易编号', required: true, aliases: ['id','external_id','编号','交易编号'] },
  { key: 'occurred_at', label: '交易时间', required: true, aliases: ['occurred_at','date','日期','交易时间'] },
  { key: 'amount', label: '金额', required: true, aliases: ['amount','金额'] },
  { key: 'direction', label: '收支方向', required: true, aliases: ['direction','方向','收支方向'] },
  { key: 'currency', label: '币种', required: true, aliases: ['currency','币种'] },
  { key: 'status', label: '入账状态', required: true, aliases: ['status','状态','入账状态'] },
  { key: 'merchant', label: '商户名称', required: false, aliases: ['merchant','merchant_name','商户','商户名称'] },
  { key: 'category', label: '消费分类', required: false, aliases: ['category','分类','消费分类'] },
  { key: 'kind', label: '交易类型', required: false, aliases: ['kind','交易类型'] },
]
export function statementHeaders(csv: string): string[] {
  const input = csv.replace(/^\ufeff/, ''), headers: string[] = []
  let field = '', quoted = false, closed = false
  const push = () => {
    const name = field.trim()
    if (!name || headers.includes(name) || headers.length >= 32) throw new Error('表头不能为空、重复或超过 32 列')
    headers.push(name); field = ''; closed = false
  }
  for (let i = 0; i < input.length; i++) {
    const c = input[i]!
    if (quoted) {
      if (c === '"') {
        if (input[i + 1] === '"') { field += '"'; i++ }
        else { quoted = false; closed = true }
      } else field += c
    } else if (c === ',') push()
    else if (c === '\r' || c === '\n') { push(); return headers }
    else if (closed) throw new Error('CSV 表头引号后的格式无效')
    else if (c === '"') {
      if (field) throw new Error('CSV 表头引号格式无效')
      quoted = true
    } else field += c
  }
  if (quoted) throw new Error('CSV 表头引号未闭合')
  push(); return headers
}
export function suggestColumns(headers: string[]): Record<string, string> {
  return Object.fromEntries(statementFields.map(field => {
    const found = headers.filter(h => field.aliases.includes(h.toLowerCase()))
    return [field.key, found.length === 1 ? found[0]! : '']
  }))
}
export function validateColumns(headers: string[], columns: Record<string, unknown>): string {
  if (Object.keys(columns).some(k => !statementFields.some(f => f.key === k))) return '字段映射包含不支持的项目'
  const used = new Set<string>()
  for (const field of statementFields) {
    const name = columns[field.key]
    if (name === undefined || name === '') {
      if (field.required) return `请为「${field.label}」选择对应的账单列`
      continue
    }
    if (typeof name !== 'string' || !headers.includes(name)) return `「${field.label}」选择的列不存在`
    if (used.has(name)) return '同一账单列不能重复用于多个字段'
    used.add(name)
  }
  return ''
}
