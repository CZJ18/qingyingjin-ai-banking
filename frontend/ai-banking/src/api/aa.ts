import { request, type Schema } from './client'
export const aaApi = {
  reviewManual: (owner: string, id: string, body: Schema<'AAManualReviewInput'>) => request<Schema<'AAManualReview'>>(`/api/v1/aa/wechat/${encodeURIComponent(owner)}/${encodeURIComponent(id)}/review`, { method: 'POST', body }),
  confirmManual: (owner: string, id: string, body: Schema<'AAManualConfirmInput'>) => request<Schema<'AABillView'>>(`/api/v1/aa/wechat/${encodeURIComponent(owner)}/${encodeURIComponent(id)}/confirm`, { method: 'POST', body }),
  list: (offset = 0, signal?: AbortSignal) => request<Schema<'AABillView'>[]>(`/api/v1/aa/bills?limit=50&offset=${offset}`, { signal }),
  create: (body: Schema<'AABillInput'>, key: string) => request<Schema<'AABillView'>>('/api/v1/aa/bills', { method: 'POST', body, key }),
  preview: (invitation: string) => request<Schema<'AABillView'>>('/api/v1/aa/invitations/preview', { method: 'POST', body: { invitation } }),
  join: (invitation: string, participant_id: string) => request<Schema<'AABillView'>>('/api/v1/aa/join', { method: 'POST', body: { invitation, participant_id } }),
  cancel: (id: string, expected_version: number) => request<Schema<'AABillView'>>(`/api/v1/aa/bills/${encodeURIComponent(id)}/cancel`, { method: 'POST', body: { expected_version } }),
}
