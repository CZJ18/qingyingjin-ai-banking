export interface ProductContext {
  connected: boolean
  sessionEpoch: number
  userId: string
  goalId?: string
  planId?: string
}
export function isCurrentProductContext(expected: ProductContext, current: ProductContext) {
  return (
    expected.connected &&
    current.connected &&
    expected.sessionEpoch === current.sessionEpoch &&
    expected.userId === current.userId &&
    (expected.goalId === undefined || expected.goalId === current.goalId) &&
    (expected.planId === undefined || expected.planId === current.planId)
  )
}
function canonical(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonical)
  if (value && typeof value === 'object')
    return Object.fromEntries(
      Object.entries(value)
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([k, v]) => [k, canonical(v)]),
    )
  return value
}
// Keep uncertain mutations retryable in memory; completed actions may be intentionally repeated.
export function createStableActions(randomId = () => crypto.randomUUID()) {
  const keys = new Map<string, string>()
  return {
    async run<T>(scope: string, body: unknown, work: (key: string) => Promise<T>): Promise<T> {
      const signature = JSON.stringify([scope, canonical(body)])
      let key = keys.get(signature)
      if (!key) {
        key = randomId()
        keys.set(signature, key)
      }
      const result = await work(key)
      if (keys.get(signature) === key) keys.delete(signature)
      return result
    },
  }
}
