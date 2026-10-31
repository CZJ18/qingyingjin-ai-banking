import { expect, it } from 'vitest'
import { statementHeaders, suggestColumns, validateColumns } from '../src/lib/statement-columns'

it('recognizes standard and Chinese headers without parsing or altering financial rows', () => {
  const headers = statementHeaders('\ufeff编号,日期,金额,方向,币种,状态,"商户,名称"\r\na,2026-10-20,15.00,expense,CNY,posted,Cafe')
  expect(headers).toEqual(['编号','日期','金额','方向','币种','状态','商户,名称'])
  const columns = suggestColumns(headers)
  expect(columns).toMatchObject({external_id:'编号',occurred_at:'日期',amount:'金额',direction:'方向',currency:'币种',status:'状态'})
  expect(validateColumns(headers, columns)).toBe('')
  expect(suggestColumns(['id','external_id']).external_id).toBe('')
})
it('rejects malformed headers and missing or duplicate mappings before preview', () => {
  for (const csv of ['id,id\na,b','id,,amount\na,b,c','"unterminated','"id"oops,amount','']) expect(() => statementHeaders(csv)).toThrow()
  const headers=['id','occurred_at','amount','direction','currency','status']
  const columns=suggestColumns(headers)
  expect(validateColumns(headers, {...columns,status:''})).toContain('状态')
  expect(validateColumns(headers, {...columns,amount:'id'})).toContain('重复')
  expect(validateColumns(headers, {...columns,amount:'missing'})).toContain('不存在')
  expect(validateColumns(headers, {...columns,merchant:null})).toContain('不存在')
})
