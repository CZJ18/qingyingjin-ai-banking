// Local-only UI fixtures. Every API response is intercepted; no model, bank or database is called.
import assert from 'node:assert/strict'
import { chromium } from '@playwright/test'
import { mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'

const origin = process.env.BANKING_UI_ORIGIN || 'http://127.0.0.1:5193'
assert.equal(new URL(origin).hostname, '127.0.0.1')
const artifacts = process.env.MONTHLY_GOAL_ARTIFACTS || 'test-results/monthly-goal'
await mkdir(artifacts, { recursive: true })
const browser = await chromium.launch({ headless: true, executablePath: process.env.BANKING_TEST_BROWSER ||
  'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe' })
const context = await browser.newContext({ viewport: { width: 1440, height: 1050 }, timezoneId: 'America/Los_Angeles' })
const page = await context.newPage()
await page.clock.setFixedTime(new Date('2026-10-31T16:05:00Z'))
const errors = [], calls = [], submissions = [], resumes = [], tasks = new Map(), checks = []
page.on('pageerror', (error) => errors.push(error.stack || error.message))
page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()) })
let userId = '42', sequence = 0, latestId = '', refreshedReads = 0
const taskId = (n) => `lg-12345678-1234-4234-9234-${String(n).padStart(12, '0')}`
const account = { id: '7', user_id: '42', display_name: '生活账户', masked_account_number: '****0007',
  balance_minor: 650000, status: 'active', currency: 'CNY', version: 1, is_simulated: true }
function planTask(body) {
  const id = taskId(++sequence)
  latestId = id
  const specs = [
    { key: 'billing', agent_type: 'billing', tool: 'analyze_spending', skill_id: 'billing_analysis',
      skill_version: 1, execution_mode: 'read_only', parameters: { account_id: '7', from: body.bindings.from, to: body.bindings.to },
      dependencies: [], priority: 100, use_coze: false, blocked: false, required_permissions: ['READ'] },
    { key: 'savings', agent_type: 'savings', tool: 'get_financial_plan', skill_id: 'savings_planning',
      skill_version: 1, execution_mode: 'read_only', parameters: { plan_id: '9', constraints: ['保留日常生活开支'] },
      dependencies: ['billing'], priority: 100, use_coze: false, blocked: false, required_permissions: ['READ'] },
  ]
  const result = { tool: 'get_financial_plan', result: {
    currency: 'CNY', period: body.monthly_goal?.period || '2026-11', financial_execution_enabled: false,
    suggested_savings_minor: 200000, necessary_reserve_minor: 400000, disposable_minor: 250000,
    options: [{ name: '本次实际建议方案', explanation: '保留日常生活开支，每月安排储蓄。', savings_minor: 200000,
      retained_minor: 450000, feasible: true, allocations: [] }],
    review_notes: ['真实返回的约束说明'], blockers: [],
  } }
  const suggestions = Object.fromEntries(specs.map((spec) => [spec.key, {
    task_id: `${id}.${spec.key}`, agent_type: spec.agent_type, skill_id: spec.skill_id, skill_version: 1,
    status: 'succeeded', result: spec.key === 'savings' ? result : { total_expense: 300000, note: '本次账单实际结果' },
    evidence: [], errors: [], next_actions: [], financial_execution_enabled: false,
  }]))
  return { task_id: id, owner_engine: 'langgraph', status: 'queued', version: 1,
    request: { ...body, tasks: [], max_attempts: 2 }, result: null, confirmation: null, error_code: '',
    financial_execution_enabled: false, created_at: '2026-10-31T16:05:00Z', updated_at: '2026-10-31T16:05:00Z',
    fixture: { specs, suggestions } }
}
const publicTask = ({ fixture, ...task }) => task
try {
  await page.route('**/*', async (route) => {
    const req = route.request(), url = new URL(req.url()), pathname = url.pathname
    if (url.origin !== new URL(origin).origin) {
      errors.push('Unexpected external request: ' + url.origin)
      return route.abort()
    }
    if (!/^\/(?:product\/)?api\//.test(pathname)) return route.continue()
    calls.push({ path: pathname, method: req.method() })
    const body = req.postData() ? req.postDataJSON() : null
    let data = []
    if (pathname === '/api/v1/auth/capabilities') data = { sms_enabled: false, sms_mode: 'disabled',
      registration_verification_required: false, code_expires_in: 300, resend_after: 60 }
    else if (pathname === '/api/v1/auth/logout') data = null
    else if (pathname.startsWith('/api/v1/auth/')) data = { access_token: 'explicit-local-monthly-fixture-' + userId,
      expires_in: 900, user: { id: userId, display_name: '月度目标验收用户' }, phone_masked: '138****0000',
      phone_verified: false, phone_verification_method: '' }
    else if (pathname === '/api/v1/users/me') data = { id: userId, display_name: '月度目标验收用户' }
    else if (pathname === '/api/v1/accounts') data = [{ ...account, user_id: userId }]
    else if (pathname === '/api/v1/notifications/renewals') data = { items: [], settings: null }
    else if (pathname === '/product/api/v1/agent-tasks' && req.method() === 'POST') {
      assert.equal(req.headers().authorization, 'Bearer explicit-local-monthly-fixture-' + userId)
      assert.ok(req.headers()['idempotency-key'])
      submissions.push(body)
      const task = planTask(body)
      tasks.set(task.task_id, task)
      data = publicTask(task)
    } else if (pathname.startsWith('/product/api/v1/agent-tasks/')) {
      const [, id, action] = pathname.match(/\/agent-tasks\/([^/]+)(?:\/([^/]+))?/) || []
      const task = tasks.get(id)
      assert.ok(task, 'Fixture task exists')
      if (action === 'events') data = []
      else if (action === 'run') {
        assert.equal(body.expected_version, task.version)
        task.version = 3
        if (task.request.monthly_goal) {
          task.status = 'waiting_confirmation'
          task.confirmation = { status: 'pending', node_key: 'monthly_plan_acceptance', digest: 'a'.repeat(64),
            nonce: `fixture-only-${id}-nonce`, expires_at: task.request.instruction.includes('过期测试')
              ? '2026-10-31T16:04:00Z' : task.request.instruction.includes('跨月测试')
                ? '2026-11-30T16:09:00Z' : '2026-11-01T16:05:00Z', confirmation_scope: 'PLAN_ACCEPTANCE',
            parameters_view: { confirmation_scope: 'PLAN_ACCEPTANCE', monthly_goal: task.request.monthly_goal,
              goal_text: task.request.instruction, plan_tasks: task.fixture.specs,
              suggestions: task.fixture.suggestions, source_digest: 'b'.repeat(64) } }
        } else { task.status = 'succeeded'; task.result = { outputs: [] } }
        data = publicTask(task)
      } else if (action === 'resume') {
        assert.equal(req.headers().authorization, 'Bearer explicit-local-monthly-fixture-' + userId)
        assert.deepEqual(body, { expected_version: task.version, node_key: task.confirmation.node_key,
          digest: task.confirmation.digest, nonce: task.confirmation.nonce, decision: body.decision })
        resumes.push({ taskId: id, decision: body.decision, expectedVersion: body.expected_version })
        task.version++
        task.status = body.decision === 'approve' ? 'succeeded' : 'cancelled'
        task.confirmation = null
        task.result = { outputs: Object.values(task.fixture.suggestions), financial_execution_enabled: false }
        data = publicTask(task)
      } else data = { ...publicTask(task), updated_at: new Date(Date.UTC(2026, 9, 31, 16, 5) + ++refreshedReads).toISOString() }
    }
    await route.fulfill({ status: 200, json: { code: 'OK', data, message: 'Explicit isolated UI fixture',
      request_id: 'fixture', trace_id: 'fixture' } })
  })
  await page.goto(origin + '/assistant')
  await page.getByRole('button', { name: '退出登录', exact: true }).waitFor()
  const mode = page.getByRole('button', { name: '本月目标', exact: true })
  assert.equal(await mode.getAttribute('aria-pressed'), 'true')
  const input = page.getByRole('textbox', { name: '本月目标或方案回复', exact: true })
  const finalPlan = page.getByRole('region', { name: '本月方案最终确认', exact: true })
  async function newMonthly(goal) {
    await page.getByRole('button', { name: '新任务', exact: true }).click()
    await mode.click()
    await input.fill(goal)
    await page.getByRole('button', { name: '发送', exact: true }).click()
    await finalPlan.waitFor()
    await page.getByRole('button', { name: '发送', exact: true }).waitFor()
    return latestId
  }
  const goal = '本月存下 2000 元，保留日常生活开支'
  await newMonthly(goal)
  assert.deepEqual(submissions[0].monthly_goal, { period: '2026-11', currency: 'CNY', timezone: 'Asia/Shanghai' })
  assert.equal(submissions[0].bindings.from, '2026-10-31T16:00:00.000Z')
  assert.equal(submissions[0].bindings.to, '2026-11-30T16:00:00.000Z')
  assert.equal(submissions[0].instruction, goal)
  assert.equal(Object.keys(submissions[0]).some((key) => /cost|fee|budget_cap/.test(key)), false)
  assert.match(await finalPlan.innerText(), /本次实际建议方案/)
  assert.match(await finalPlan.innerText(), /真实返回的约束说明/)
  assert.match(await finalPlan.innerText(), /2026-11/)
  assert.equal(await finalPlan.getByRole('button', { name: '确认方案', exact: true }).isDisabled(), true)
  await page.screenshot({ path: path.join(artifacts, 'desktop-final-plan.png'), fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))
  await page.screenshot({ path: path.join(artifacts, 'mobile-final-plan.png'), fullPage: true })
  await page.setViewportSize({ width: 1440, height: 1050 })
  const before = submissions.length
  await input.fill('确认方案')
  await page.getByRole('button', { name: '发送', exact: true }).click()
  await finalPlan.waitFor({ state: 'hidden' })
  assert.equal(submissions.length, before)
  assert.equal(resumes.at(-1).decision, 'approve')
  checks.push('Shanghai month boundary in a Los Angeles browser', 'actual frozen suggestions displayed', 'typed accept uses only bound resume')

  await newMonthly(goal + '，拒绝测试')
  await input.fill('拒绝方案')
  await page.getByRole('button', { name: '发送', exact: true }).click()
  await finalPlan.waitFor({ state: 'hidden' })
  assert.equal(resumes.at(-1).decision, 'reject')
  checks.push('typed reject cancels the current plan')

  const oldId = await newMonthly(goal + '，修改前')
  const resumeCount = resumes.length
  await finalPlan.getByRole('checkbox').check()
  await finalPlan.getByRole('button', { name: '修改本月目标', exact: true }).click()
  await finalPlan.waitFor({ state: 'hidden' })
  assert.equal(await input.inputValue(), goal + '，修改前')
  await input.fill(goal + '，修改后')
  await page.getByRole('button', { name: '发送', exact: true }).click()
  await finalPlan.waitFor()
  assert.notEqual(latestId, oldId)
  assert.equal(resumes.length, resumeCount)
  assert.equal(await finalPlan.getByRole('checkbox').isChecked(), false)
  checks.push('editing creates a new task with fresh unchecked confirmation')

  // A version refresh must revoke a checkbox acknowledgement and a previously typed reply.
  await finalPlan.getByRole('checkbox').check()
  await page.waitForResponse((response) => response.url() === `${origin}/product/api/v1/agent-tasks/${latestId}` &&
    response.request().method() === 'GET')
  await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))))
  assert.equal(await finalPlan.getByRole('checkbox').isChecked(), true)
  checks.push('same-bound task polling preserves acknowledgement')
  await input.fill('确认方案')
  tasks.get(latestId).version++
  await page.waitForFunction(() => !document.querySelector('[aria-label="本月方案最终确认"] input[type="checkbox"]')?.checked,
    null, { timeout: 10000 })
  await page.getByRole('button', { name: '发送', exact: true }).click()
  await page.getByRole('alert').filter({ hasText: '内容已更新' }).waitFor()
  assert.equal(resumes.length, resumeCount)
  checks.push('version changes revoke checkbox and typed approval')

  await input.fill('')
  await input.fill('确认方案')
  await finalPlan.getByRole('checkbox').check()
  tasks.get(latestId).confirmation.nonce += '-replaced'
  await page.waitForResponse((response) => response.url() === `${origin}/product/api/v1/agent-tasks/${latestId}` &&
    response.request().method() === 'GET')
  await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))))
  assert.equal(await finalPlan.getByRole('checkbox').isChecked(), false)
  await page.getByRole('button', { name: '发送', exact: true }).click()
  await page.getByRole('alert').filter({ hasText: '内容已更新' }).waitFor()
  assert.equal(resumes.length, resumeCount)
  checks.push('nonce replacement revokes checkbox and typed approval')

  await newMonthly(goal + '，过期测试')
  assert.equal(await finalPlan.getByRole('button', { name: '确认方案', exact: true }).isDisabled(), true)
  assert.equal(await finalPlan.getByRole('button', { name: '拒绝方案', exact: true }).isDisabled(), true)
  await input.fill('确认方案')
  await page.getByRole('button', { name: '发送', exact: true }).click()
  await page.getByRole('alert').filter({ hasText: '已失效或内容已更新' }).waitFor()
  assert.equal(resumes.length, resumeCount)
  checks.push('expired plan cannot be approved by button or dialogue')

  await newMonthly(goal + '，跨月测试')
  await finalPlan.getByRole('checkbox').check()
  await input.fill('确认方案')
  await page.clock.setFixedTime(new Date('2026-11-30T16:01:00Z'))
  await finalPlan.getByRole('alert').filter({ hasText: '已失效' }).waitFor()
  assert.equal(await finalPlan.getByRole('button', { name: '确认方案', exact: true }).isDisabled(), true)
  await page.getByRole('button', { name: '发送', exact: true }).click()
  await page.getByRole('alert').filter({ hasText: '已失效或内容已更新' }).waitFor()
  assert.equal(resumes.length, resumeCount)
  await page.clock.setFixedTime(new Date('2026-10-31T16:05:00Z'))
  checks.push('Shanghai month rollover invalidates a still-unexpired previous-month plan')

  await newMonthly(goal + '，一般任务隔离')
  await page.getByRole('button', { name: '一般任务', exact: true }).click()
  await page.getByRole('textbox', { name: '金融任务指令', exact: true }).fill('  确认  ')
  await page.getByRole('button', { name: '发送', exact: true }).click()
  await finalPlan.waitFor({ state: 'hidden' })
  assert.equal(submissions.at(-1).instruction, '确认')
  assert.equal('monthly_goal' in submissions.at(-1), false)
  assert.deepEqual(Object.keys(submissions.at(-1)).sort(), ['bindings', 'instruction', 'timeout_ms'])
  assert.equal(resumes.length, resumeCount)
  checks.push('general tasks retain their original body and never infer plan approval')

  await newMonthly(goal + '，会话切换')
  await input.fill('确认方案')
  await page.getByRole('button', { name: '退出登录', exact: true }).click()
  await finalPlan.waitFor({ state: 'hidden' })
  assert.equal(await input.inputValue(), '')
  userId = '43'
  await page.goto(origin + '/assistant')
  await page.getByRole('button', { name: '退出登录', exact: true }).waitFor()
  assert.equal(await finalPlan.count(), 0)
  assert.equal(resumes.length, resumeCount)
  checks.push('logout and account switch discard pending dialogue and approval')

  assert.equal(calls.some((call) => call.method !== 'GET' && /execution\/operations|\/authorize$|\/execute$/.test(call.path)), false)
  assert.deepEqual(errors, [])
  await writeFile(path.join(artifacts, 'acceptance.json'), JSON.stringify({ passed: true,
    scope: 'local intercepted API fixtures only; no model, bank or database calls', checks,
    submissions: submissions.length, decisions: resumes, consoleErrors: errors }, null, 2))
  console.log(JSON.stringify({ passed: true, checks: checks.length, artifacts: path.resolve(artifacts) }))
} catch (error) {
  await page.screenshot({ path: path.join(artifacts, 'failure.png'), fullPage: true }).catch(() => {})
  await writeFile(path.join(artifacts, 'failure.json'), JSON.stringify({ message: String(error), url: page.url(), errors, checks }, null, 2))
  throw error
} finally {
  await context.close()
  await browser.close()
}
