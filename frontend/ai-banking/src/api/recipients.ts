import { ApiError, request, type Schema } from './client'
function settings(v: Schema<'RecipientSettings'>) {
  if (!v || typeof v.enabled !== 'boolean' || !Number.isSafeInteger(v.version) || v.version < 1 || typeof v.account_id !== 'string' || !/^(0|[1-9][0-9]*)$/.test(v.account_id)) throw new ApiError('INVALID_RESPONSE', '收款设置响应无效，请刷新重试')
  return v
}
export const recipientApi = {
  settings: async (signal?: AbortSignal) => settings(await request<Schema<'RecipientSettings'>>('/api/v1/recipients/settings', { signal })),
  configure: async (body: Schema<'RecipientSettingsInput'>) => settings(await request<Schema<'RecipientSettings'>>('/api/v1/recipients/settings', { method: 'PUT', body })),
  search: async (body: Schema<'RecipientSearchInput'>) => {
    const v = await request<Schema<'RecipientSearchResult'>>('/api/v1/recipients/search', { method: 'POST', body })
    if (!v || !Array.isArray(v.items) || v.items.length > 20 || typeof v.limited !== 'boolean' || v.items.some(r => !r || typeof r.id !== 'string' || !/^[1-9][0-9]*$/.test(r.id) || !Number.isSafeInteger(r.version) || r.version < 1 || ['display_name','phone_masked','account_name','account_masked_number','phone_verification_method'].some(k => typeof r[k as keyof typeof r] !== 'string'))) throw new ApiError('INVALID_RESPONSE', '收款人查询响应无效，请重新查找')
    return v
  },
}
