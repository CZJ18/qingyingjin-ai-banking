import { expect, it } from 'vitest'
import { categoryLabel, canonicalCategory, transactionKindLabel } from '../src/lib/categories'

it('shows shared banking aliases consistently without changing stored source categories', () => {
  for (const value of ['dining','food','groceries','餐饮',' 食品 ']) {
    expect(categoryLabel(value)).toBe('餐饮')
    expect(canonicalCategory(value)).toBe('dining')
  }
  for (const value of ['sports','exercise','fitness','运动']) expect(categoryLabel(value)).toBe('运动')
  expect(categoryLabel('uncategorized')).toBe('未分类')
  expect(categoryLabel('custom-source')).toBe('custom-source')
  expect(canonicalCategory('custom-source')).toBe('uncategorized')
})
it('explains balance and salary labels without changing accounting categories',()=>{
  expect(categoryLabel('opening')).toBe('期初余额')
  expect(categoryLabel('salary')).toBe('工资收入')
  expect(canonicalCategory('salary')).toBe('uncategorized')
  expect(transactionKindLabel('opening')).toBe('期初余额（不计入收支）')
  expect(transactionKindLabel('internal_transfer')).toBe('账户内部转账（不计入收支）')
  expect(transactionKindLabel('external')).toBe('外部收支')
})
