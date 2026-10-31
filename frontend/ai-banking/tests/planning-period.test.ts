import { afterEach, expect, it, vi } from 'vitest'
import { currentMonth, planningMonth } from '../src/lib/queries'

afterEach(() => vi.useRealTimers())
it('keeps the financial planning period aligned with the existing Go UTC-month contract at Shanghai month boundaries', () => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-10-31T20:00:00Z'))
  expect(currentMonth()).toBe('2026-11')
  expect(planningMonth()).toBe('2026-10')
  vi.setSystemTime(new Date('2026-11-01T00:00:00Z'))
  expect(planningMonth()).toBe('2026-11')
})
