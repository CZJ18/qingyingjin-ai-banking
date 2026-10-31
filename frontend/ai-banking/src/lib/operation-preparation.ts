import type { OperationSuggestion } from '@/components/agents/task-view'

const uuid = '[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}'
const taskIdentity = new RegExp(`^(?:${uuid}|lg-${uuid}\\.[A-Za-z0-9][A-Za-z0-9_-]{0,63})$`)

function canonical(value: unknown, depth = 0): unknown {
  if (depth > 24) throw new Error('操作建议过于复杂，请重新查询任务')
  if (value === null || typeof value === 'string' || typeof value === 'boolean') return value
  if (typeof value === 'number' && Number.isSafeInteger(value)) return value
  if (Array.isArray(value)) return value.map(item => canonical(item, depth + 1))
  if (value && typeof value === 'object')
    return Object.fromEntries(Object.keys(value).sort().map(key => [key, canonical((value as Record<string, unknown>)[key], depth + 1)]))
  throw new Error('操作建议包含无效数据，请重新查询任务')
}

// A stable replay key for one persisted proposal, not an authorization or grant.
// The existing server still validates ownership, risk and exact request binding.
export async function operationPreparationKey(taskId: string, proposal: OperationSuggestion): Promise<string> {
  if (!taskIdentity.test(taskId) || !proposal || typeof proposal.kind !== 'string' ||
    !proposal.parameters || typeof proposal.parameters !== 'object' || Array.isArray(proposal.parameters))
    throw new Error('操作建议缺少完整任务来源，请重新查询任务')
  const body = { kind: proposal.kind, parameters: proposal.parameters,
    ...(proposal.expires_in_seconds !== undefined ? { expires_in_seconds: proposal.expires_in_seconds } : {}) }
  const encodedBody = JSON.stringify(canonical(body))
  if (new TextEncoder().encode(encodedBody).byteLength > 65536) throw new Error('操作建议过长，请重新查询任务')
  const encoded = new TextEncoder().encode(JSON.stringify([taskId, encodedBody]))
  const hash = await crypto.subtle.digest('SHA-256', encoded)
  return 'agent-op-' + Array.from(new Uint8Array(hash), byte => byte.toString(16).padStart(2, '0')).join('')
}
