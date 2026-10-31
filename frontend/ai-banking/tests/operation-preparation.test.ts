import { expect, it } from 'vitest'
import { operationPreparationKey } from '../src/lib/operation-preparation'
import type { OperationSuggestion } from '../src/components/agents/task-view'

const source = 'lg-11111111-1111-4111-8111-111111111111.review'
const proposal: OperationSuggestion = { kind: 'card_manage', parameters: { action: 'lock', card_id: '22222222-2222-4222-8222-222222222222' } }

it('reuses a bounded replay key after serializing and restoring the same persisted proposal', async () => {
  const key = await operationPreparationKey(source, proposal)
  expect(key).toMatch(/^agent-op-[a-f0-9]{64}$/)
  expect(await operationPreparationKey(source, JSON.parse(JSON.stringify(proposal)))).toBe(key)
  expect(key).not.toContain('22222222')
})
it('ignores JSON object ordering while retaining array and field values', async () => {
  const reordered: OperationSuggestion = { parameters: { card_id: '22222222-2222-4222-8222-222222222222', action: 'lock' }, kind: 'card_manage' }
  expect(await operationPreparationKey(source, reordered)).toBe(await operationPreparationKey(source, proposal))
})
it('keeps independent tasks, changed parameters and expiry scopes separate', async () => {
  const original = await operationPreparationKey(source, proposal)
  expect(await operationPreparationKey(source.replace('.review', '.another'), proposal)).not.toBe(original)
  expect(await operationPreparationKey(source, { ...proposal, parameters: { action: 'unlock', card_id: '22222222-2222-4222-8222-222222222222' } })).not.toBe(original)
  expect(await operationPreparationKey(source, { ...proposal, expires_in_seconds: 300 })).not.toBe(original)
})
it.each(['', 'synthetic-free-text', 'lg-11111111-1111-4111-8111-111111111111', source + '/execute'])('rejects incomplete or altered task identity %s without a random fallback', async id => {
  await expect(operationPreparationKey(id, proposal)).rejects.toThrow('任务来源')
})
it('retains replay safety for the existing canonical legacy task identity', async () => {
  const id = '11111111-1111-4111-8111-111111111111'
  expect(await operationPreparationKey(id, proposal)).toBe(await operationPreparationKey(id, proposal))
})
