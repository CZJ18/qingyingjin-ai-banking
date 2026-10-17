import { request, type Schema } from './client'
export const renewalApi = {
  list: (signal?: AbortSignal) => request<Schema<'RenewalNoticeView'>>('/api/v1/notifications/renewals', { signal }),
  configure: (body: Schema<'RenewalSettingsInput'>) => request<Schema<'RenewalNoticeView'>>('/api/v1/notifications/renewals/settings', { method: 'PUT', body }),
  read: (id: string, version: number) => request<Schema<'RenewalNoticeView'>>(`/api/v1/notifications/renewals/${encodeURIComponent(id)}/read`, { method: 'POST', body: { expected_version: version } }),
}
