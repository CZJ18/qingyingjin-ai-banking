import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useSession } from '../src/lib/store'
import { setToken } from '../src/api/client'

let values: Map<string, string>
beforeEach(() => {
  setActivePinia(createPinia())
  values = new Map()
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
    removeItem: (key: string) => values.delete(key),
  })
})
afterEach(() => {
  vi.unstubAllGlobals()
  setToken('')
})
function identity(id: string) {
  vi.stubGlobal(
    'fetch',
    vi
      .fn()
      .mockImplementation(
        async () => new Response(JSON.stringify({ code: 'OK', data: { id, display_name: id } })),
      ),
  )
}
describe('Authenticated reference isolation', () => {
  it('keeps legacy jobs, LangGraph tasks and numeric goal references in their own namespaces', async () => {
    identity('a')
    const s = useSession()
    await s.connect('a')
    s.saveAgentTask('12345678-1234-4234-9234-123456789abc')
    s.saveJob('lg-12345678-1234-4234-9234-123456789abc')
    s.saveLifestyleGoal('lg-12345678-1234-4234-9234-123456789abc')
    s.saveLifestylePlan('01')
    expect(s.agentTaskIds).toEqual([])
    expect(s.jobIds).toEqual([])
    expect(s.lifestyleGoalIds).toEqual([])
    expect(s.lifestylePlanId).toBe('')
  })
  it('clears a stale lifestyle plan durably without losing a valid restored plan', async () => {
    identity('a')
    const s = useSession()
    await s.connect('a')
    s.saveLifestyleGoal('17')
    s.saveLifestylePlan('19')
    s.disconnect()
    await s.connect('a')
    expect(s.lifestyleGoalId).toBe('17')
    expect(s.lifestylePlanId).toBe('19')
    s.clearLifestylePlan()
    s.disconnect()
    await s.connect('a')
    expect(s.lifestylePlanId).toBe('')
  })
  it('ignores legacy references without a verified owner', () => {
    values.set('qingyingjin.jobs', '["foreign-task"]')
    values.set('qingyingjin.plan', 'foreign-plan')
    const s = useSession()
    expect(s.jobIds).toEqual([])
    expect(s.planId).toBe('')
  })
  it('restores only the verified user references and never persists credentials', async () => {
    const s = useSession()
    identity('a')
    await s.connect('private-token-a')
    s.saveJob('12345678-1234-4234-9234-123456789abc')
    s.saveAgentTask('lg-12345678-1234-4234-9234-123456789abc')
    s.saveGoal('17')
    s.saveLifestyleGoal('18')
    s.savePlan('19')
    s.disconnect()
    identity('b')
    await s.connect('private-token-b')
    expect(s.jobIds).toEqual([])
    expect(s.agentTaskIds).toEqual([])
    expect(s.goalIds).toEqual([])
    expect(s.planId).toBe('')
    identity('a')
    await s.connect('private-token-a')
    expect(s.jobIds).toEqual(['12345678-1234-4234-9234-123456789abc'])
    expect(s.agentTaskIds).toEqual(['lg-12345678-1234-4234-9234-123456789abc'])
    expect(s.goalIds).toEqual(['17'])
    expect(s.lifestyleGoalIds).toEqual(['18'])
    expect(s.planId).toBe('19')
    expect(JSON.stringify([...values])).not.toContain('private-token')
  })
  it('clears previous identity and sensitive references after failed connection', async () => {
    const s = useSession()
    identity('a')
    await s.connect('a')
    s.saveAgentTask('lg-12345678-1234-4234-9234-123456789abc')
    s.accountId = 'account-a'
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ code: 'UNAUTHORIZED', message: 'invalid' }), {
          status: 401,
        }),
      ),
    )
    await expect(s.connect('invalid')).rejects.toThrow()
    expect(s.user).toBeNull()
    expect(s.connected).toBe(false)
    expect(s.accountId).toBe('')
    expect(s.agentTaskIds).toEqual([])
  })
  it('does not reconnect after logout while authentication is in flight', async () => {
    let complete!: (value: Response) => void
    vi.stubGlobal(
      'fetch',
      vi.fn(
        () =>
          new Promise<Response>((r) => {
            complete = r
          }),
      ),
    )
    const s = useSession(),
      pending = s.connect('a')
    s.disconnect()
    complete(new Response(JSON.stringify({ code: 'OK', data: { id: 'a' } })))
    await expect(pending).rejects.toThrow()
    expect(s.user).toBeNull()
    expect(s.connected).toBe(false)
  })
  it('a superseded connection cannot clear the newer user session', async () => {
    let complete!: (value: Response) => void
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockImplementationOnce(
          () =>
            new Promise<Response>((r) => {
              complete = r
            }),
        )
        .mockResolvedValueOnce(new Response(JSON.stringify({ code: 'OK', data: { id: 'b' } }))),
    )
    const s = useSession(),
      pending = s.connect('a')
    await s.connect('b')
    complete(new Response(JSON.stringify({ code: 'OK', data: { id: 'a' } })))
    await expect(pending).rejects.toThrow()
    expect(s.user?.id).toBe('b')
    expect(s.connected).toBe(true)
  })
})
