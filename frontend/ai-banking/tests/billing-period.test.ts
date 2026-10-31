import {describe,it,expect} from 'vitest'
import {billDates,validBillRange} from '../src/lib/billing-period'
import * as periods from '../src/lib/queries'

describe('monthly and yearly bill range',()=>{
 it('uses full leap year dates and Shanghai half-open boundaries',()=>{
  const p=billDates('year','2024-02',2024)
  expect(p).toEqual({start:'2024-01-01',end:'2024-12-31'})
  expect(validBillRange('year','2024-02',2024,p.start,p.end)).toBe(true)
 })
 it('keeps custom dates within selected year or selected month',()=>{
  expect(validBillRange('year','2026-10',2026,'2026-01-01','2027-01-01')).toBe(false)
  expect(validBillRange('month','2026-10',2026,'2026-10-01','2026-11-01')).toBe(false)
  expect(billDates('month','2026-02',2026)).toEqual({start:'2026-02-01',end:'2026-02-28'})
  expect(validBillRange('year','2026-10',2026,'2026-02-30','2026-03-01')).toBe(false)
 })
})

describe('assistant analysis month binding',()=>{
 it('keeps the selected September month in Shanghai half-open boundaries',()=>{
  expect(periods.period('2026-09')).toEqual({from:'2026-08-31T16:00:00.000Z',to:'2026-09-30T16:00:00.000Z',timezone:'Asia/Shanghai'})
 })
 it('rejects empty and rollover months instead of silently analysing another period',()=>{
  for(const value of ['', '2026-00','2026-13','2026-9','26-09','2026-09-01','0001-09'])
   expect(()=>periods.period(value),value).toThrow()
 })
 it('labels persisted scope instead of substituting the current composer month',()=>{
  expect(periods).toHaveProperty('analysisPeriodLabel')
  expect(periods.analysisPeriodLabel({from:'2026-08-31T16:00:00Z',to:'2026-09-30T16:00:00Z'})).toBe('2026-09')
  expect(periods.analysisPeriodLabel(undefined)).toBe('未提供分析区间')
  expect(periods.analysisPeriodLabel({from:'invalid',to:'2026-09-30T16:00:00Z'})).toBe('未提供分析区间')
  expect(periods.analysisPeriodLabel({from:'2026-09-02T16:00:00Z',to:'2026-09-01T16:00:00Z'})).toBe('未提供分析区间')
  const custom=periods.analysisPeriodLabel({from:'2026-09-02T16:00:00Z',to:'2026-09-03T16:00:00Z'})
  expect(custom).toContain('2026-09-03');expect(custom).toContain('2026-09-04')
  expect(custom).toContain('结束时间不含')
 })
})
