import { describe, expect, it } from 'vitest'
import {
  agentRows,
  preparedOperationId,
  preparationCurrent,
  taskControls,
  transferPreparation,
} from '../src/components/agents/task-view'

const base = {
  task_id: 'lg-11111111-1111-4111-8111-111111111111',
  owner_engine: 'langgraph',
  status: 'succeeded',
  version: 3,
  request: { tasks: [], instruction: '分析消费' },
  result: { outputs: [] },
  error_code: '',
  confirmation: null,
}
describe('Agent workbench evidence', () => {
  it('uses the returned node, role and financial result without inventing a task plan', () => {
    const output = {
      task_id: base.task_id + '.billing_report',
      agent_type: 'billing',
      status: 'succeeded',
      result: { tool: 'analyze_spending', total_expense: 2930 },
      evidence: [{ type: 'transaction', id: '10' }],
    }
    const rows = agentRows({ ...base, result: { outputs: [output] } }, [])
    expect(rows).toHaveLength(1)
    expect(rows[0]).toMatchObject({
      key: 'billing_report',
      agent_type: 'billing',
      tool: 'analyze_spending',
      output,
      status: 'succeeded',
      dependencies_known: false,
    })
    expect(rows[0]?.duration_ms).toBeUndefined()
  })
  it('does not display output from a different task', () => {
    expect(
      agentRows(
        {
          ...base,
          result: {
            outputs: [
              { task_id: 'lg-22222222-2222-4222-8222-222222222222.billing', agent_type: 'billing' },
            ],
          },
        },
        [],
      ),
    ).toEqual([])
  })
  it('shows explicit dependencies and does not call an unreturned node succeeded', () => {
    const spec = {
      key: 'savings',
      agent_type: 'savings',
      tool: 'get_financial_plan',
      skill_id: 'savings_planning',
      skill_version: 1,
      execution_mode: 'read_only',
      parameters: { plan_id: '9' },
      dependencies: ['billing'],
    }
    expect(
      agentRows(
        { ...base, status: 'failed', error_code: 'TOOL_TIMEOUT', request: { tasks: [spec] } },
        [],
      )[0],
    ).toMatchObject({ dependencies: ['billing'], dependencies_known: true, status: 'pending' })
  })
  it('only offers recovery for queued, failed and interrupted tasks', () => {
    expect(taskControls('running')).toEqual({ run: false, cancel: true })
    expect(taskControls('waiting_confirmation')).toEqual({ run: false, cancel: true })
    expect(taskControls('interrupted')).toEqual({ run: true, cancel: true })
    expect(taskControls('succeeded')).toEqual({ run: false, cancel: false })
  })
  it('opens only a native prepared intent and never offers financial execution from task data', () => {
    const out = {
      skill_id: 'operation_preparation',
      financial_execution_enabled: false,
      result: {
        id: '33333333-3333-4333-8333-333333333333',
        kind: 'transfer',
        status: 'pending_authorization',
      },
    }
    expect(preparedOperationId(out)).toBe(out.result.id)
    expect(preparedOperationId({ ...out, financial_execution_enabled: true })).toBe('')
    expect(
      preparedOperationId({ ...out, result: { ...out.result, id: 'https://malicious.example/' } }),
    ).toBe('')
    expect(preparedOperationId({ ...out, skill_id: 'billing_analysis' })).toBe('')
  })
  it('does not allow expired or differently scoped preparation confirmation', () => {
    const waiting = {
      ...base,
      status: 'waiting_confirmation',
      confirmation: {
        node_key: 'prepare',
        digest: 'a'.repeat(64),
        nonce: 'n'.repeat(32),
        confirmation_scope: 'PREPARE_ONLY',
        expires_at: '2026-10-14T12:00:00Z',
      },
    }
    expect(preparationCurrent(waiting, Date.parse('2026-10-14T11:59:59Z'))).toBe(true)
    expect(preparationCurrent(waiting, Date.parse('2026-10-14T12:00:00Z'))).toBe(false)
    expect(
      preparationCurrent(
        { ...waiting, confirmation: { ...waiting.confirmation, confirmation_scope: 'EXECUTE' } },
        Date.parse('2026-10-14T11:59:59Z'),
      ),
    ).toBe(false)
  })
  it('prepares a registered transfer task using exact cents and no execution authority', () => {
    const body = transferPreparation('1', '2', '0.29', '模拟准备')
    expect(body.tasks[0]).toMatchObject({
      agent_type: 'supervisor',
      skill_id: 'operation_preparation',
      execution_mode: 'intent',
      tool: 'require_gateway',
      required_permissions: ['READ', 'PREPARE'],
      parameters: {
        kind: 'transfer',
        parameters: { amount_minor: 29, source_account_id: '1', target_account_id: '2' },
      },
    })
    expect(JSON.stringify(body)).not.toContain('confirmed')
    expect(() => transferPreparation('1', '1', '1', '')).toThrow()
    expect(() => transferPreparation('1', '2', '0', '')).toThrow()
    expect(() => transferPreparation('1', '2', '0.001', '')).toThrow()
  })
})
