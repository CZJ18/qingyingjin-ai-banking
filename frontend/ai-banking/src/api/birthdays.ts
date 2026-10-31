import { request, type Schema } from './client'

export type BirthdayEntry = Schema<'BirthdayEntry'>
export type BirthdayUpcoming = Schema<'BirthdayUpcoming'>
export type BirthdayCalendar = Schema<'BirthdayCalendarView'>
export type BirthdaySettings = Schema<'BirthdayCalendarInput'>
export const birthdayApi = {
  read: (signal?: AbortSignal) => request<BirthdayCalendar>('/api/v1/notifications/birthdays', { signal }),
  save: (body: BirthdaySettings) => request<BirthdayCalendar>('/api/v1/notifications/birthdays', { method: 'PUT', body }),
}
