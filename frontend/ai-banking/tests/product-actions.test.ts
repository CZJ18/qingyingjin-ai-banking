import { describe, expect, it } from 'vitest'
import { createStableActions, isCurrentProductContext } from '../src/lib/product-actions'

describe('Product mutation recovery', () => {
  it('reuses an unknown-outcome request key and allocates a new key only after confirmed success', async () => {
    let sequence = 0
    const actions = createStableActions(() => `key-${++sequence}`),
      seen: string[] = []
    const body = { text: '规律饮食三个月', expected_version: 0 }
    await expect(
      actions.run('goal-create', body, async (key) => {
        seen.push(key)
        throw new Error('response lost after commit')
      }),
    ).rejects.toThrow('response lost')
    const result = await actions.run('goal-create', body, async (key) => {
      seen.push(key)
      return { id: '17' }
    })
    expect(result.id).toBe('17')
    await actions.run('goal-create', body, async (key) => {
      seen.push(key)
      return { id: '18' }
    })
    expect(seen).toEqual(['key-1', 'key-1', 'key-2'])
  })
  it('treats reordered object fields as the same input while binding scope and exact amount', async () => {
    let sequence = 0
    const actions = createStableActions(() => `key-${++sequence}`),
      seen: string[] = []
    for (const [scope, body] of [
      ['goal:17:compile', { expected_version: 3, budget_id: '7' }],
      ['goal:17:compile', { budget_id: '7', expected_version: 3 }],
      ['goal:18:compile', { budget_id: '7', expected_version: 3 }],
      ['goal:17:compile', { budget_id: '8', expected_version: 3 }],
    ] as const) {
      try {
        await actions.run(scope, body, async (key) => {
          seen.push(key)
          throw new Error('unknown outcome')
        })
      } catch {}
    }
    expect(seen).toEqual(['key-1', 'key-1', 'key-2', 'key-3'])
  })
  it('blocks late results after reconnect, logout or selecting a different goal', () => {
    const captured = { connected: true, sessionEpoch: 7, userId: '21', goalId: '17' }
    expect(isCurrentProductContext(captured, { ...captured })).toBe(true)
    expect(isCurrentProductContext(captured, { ...captured, sessionEpoch: 8 })).toBe(false)
    expect(isCurrentProductContext(captured, { ...captured, connected: false })).toBe(false)
    expect(isCurrentProductContext(captured, { ...captured, goalId: '18' })).toBe(false)
    expect(isCurrentProductContext(captured, { ...captured, userId: '22' })).toBe(false)
  })
})
