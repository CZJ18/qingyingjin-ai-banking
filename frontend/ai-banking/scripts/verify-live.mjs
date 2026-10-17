import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'
import { chromium } from '@playwright/test'

// No response interception: every business assertion reads the Go service.
const base = process.env.BANKING_LIVE_URL || 'http://127.0.0.1:5178'
const token = process.env.BANKING_LIVE_JWT
const userId = process.env.BANKING_LIVE_USER_ID
const phone = process.env.BANKING_LIVE_PHONE
const password = process.env.BANKING_LIVE_PASSWORD
const expectCoze = process.env.BANKING_LIVE_EXPECT_COZE === '1'
assert.ok(token && userId, 'Set BANKING_LIVE_JWT and BANKING_LIVE_USER_ID for a seeded test user')
assert.ok(phone && password, 'Set BANKING_LIVE_PHONE and BANKING_LIVE_PASSWORD bound to the same isolated test user; the browser no longer accepts pasted JWTs')
assert.equal(process.env.BANKING_LIVE_ALLOW_WRITES, 'simulated-test-user', 'This test creates persisted simulated records; explicitly select an isolated test user')
assert.ok(['127.0.0.1', 'localhost'].includes(new URL(base).hostname), 'Live write tests only support localhost')
const out = process.env.BANKING_QA_DIR || 'test-results/live'
await mkdir(out, { recursive: true })
const checks = [], runtimeErrors = [], requests = []
const stamp = Date.now().toString()
const now = new Date(), month = now.toISOString().slice(0, 7)
const from = `${month}-01T00:00:00Z`
const to = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() + 1, 1)).toISOString()
async function api(path, body, method = body === undefined ? 'GET' : 'POST', key) {
  const response = await fetch(base + path, { method, headers: {
    Authorization: `Bearer ${token}`, 'Content-Type': 'application/json',
    ...(key ? { 'Idempotency-Key': key } : {}),
  }, body: body === undefined ? undefined : JSON.stringify(body) })
  const envelope = await response.json()
  assert.ok(response.ok && envelope.code === 'OK', `${method} ${path}: ${response.status} ${envelope.code} ${envelope.message}`)
  return envelope.data
}
async function executeIntent(kind, parameters) {
  const prepared = await api('/api/v1/execution/operations', { kind, parameters }, 'POST', crypto.randomUUID())
  const op = prepared.operation
  assert.equal(op.status, 'pending_authorization')
  const authorized = await api(`/api/v1/execution/operations/${op.id}/authorize`, { decision: 'approve', intent_digest: op.intent_digest, confirmation_nonce: op.confirmation_nonce })
  const current = authorized.operation
  const result = await api(`/api/v1/execution/operations/${op.id}/execute`, { authorization_id: current.authorization_id, intent_digest: current.intent_digest, expected_version: current.version }, 'POST', crypto.randomUUID())
  assert.equal(result.operation.status, 'succeeded', `Server refused ${kind}: ${result.operation.status}`)
  return result
}
function pass(name) { checks.push(name); console.log(`PASS: ${name}`) }
async function eventually(read, check, label) {
  for (let i = 0; i < (expectCoze ? 480 : 40); i++) { const value = await read(); if (check(value)) return value; await new Promise(r => setTimeout(r, 250)) }
  throw new Error(`Timed out: ${label}`)
}
assert.equal((await api('/api/v1/users/me')).id, userId)
const accounts = await api('/api/v1/accounts?limit=100')
assert.ok(accounts.length && accounts.every(a => a.is_simulated && a.user_id === userId))
const source = accounts.find(a => a.account_type === 'checking' && a.balance_minor > 100000)
assert.ok(source, 'Isolated test user needs a seeded, funded checking account')
const savings = await api('/api/v1/savings/goals?limit=100')
assert.ok(savings.length, 'Seed the isolated test user before running')
pass('JWT identity, real account ownership and simulated funds')

const browser = await chromium.launch({ headless: true, executablePath: process.env.BANKING_TEST_BROWSER })
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } })
page.on('pageerror', e => runtimeErrors.push(e.message))
page.on('response', async response => {
  if (response.status() >= 400 && new URL(response.url()).pathname.startsWith('/api/v1/'))
    console.log(`Service error ${response.status()} ${new URL(response.url()).pathname}: ${await response.text()}`)
})
page.on('request', req => { if (new URL(req.url()).pathname.startsWith('/api/v1/')) requests.push({ path: new URL(req.url()).pathname, method: req.method() }) })
async function nav(name) {
  if (await page.getByRole('button', { name: '打开导航', exact: true }).isVisible())
    await page.getByRole('button', { name: '打开导航', exact: true }).click()
  await page.getByRole('link', { name, exact: true }).click()
  await page.locator('h1').waitFor()
}
async function confirmDialog() {
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('checkbox').waitFor()
  assert.ok(await dialog.getByRole('button', { name: '授权并执行', exact: true }).isDisabled())
  await dialog.getByRole('checkbox').check()
  await dialog.getByRole('button', { name: '授权并执行', exact: true }).dblclick()
  await dialog.locator('.status.succeeded').waitFor()
  await dialog.getByText('查看服务器执行结果', { exact: true }).waitFor()
  await dialog.getByRole('button', { name: '关闭', exact: true }).last().click()
}
try {
  await page.goto(base)
  await page.getByRole('button', { name: '登录 / 注册', exact: true }).click()
  await page.getByLabel('手机号', { exact: true }).fill(phone)
  await page.getByLabel('密码', { exact: true }).fill(password)
  const loginResponse = page.waitForResponse(response => new URL(response.url()).pathname === '/api/v1/auth/login' && response.request().method() === 'POST')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  const login = await (await loginResponse).json()
  assert.equal(login.code, 'OK', 'Live browser phone login failed')
  assert.equal(login.data.user.id, userId, 'Phone credentials must belong to the seeded isolated user')
  assert.equal(JSON.parse(Buffer.from(login.data.access_token.split('.')[1], 'base64url').toString()).sub, userId)
  await page.getByText('已登录', { exact: true }).waitFor()
  await page.locator('.toolbar-actions select').selectOption(source.id)
  await page.locator('.balance-amount').filter({ hasText: (source.balance_minor / 100).toLocaleString('zh-CN', { minimumFractionDigits: 2 }) }).waitFor()
  pass('Browser connects through Vite proxy and displays server account balance')

  await nav('可信执行与安全')
  await page.getByRole('button', { name: '创建模拟账户', exact: true }).click()
  const accountName = `联调收款账户 ${stamp}`
  await page.getByLabel('账户名称', { exact: true }).fill(accountName)
  await page.getByLabel('模拟期初余额 / 元', { exact: true }).fill('0')
  await page.getByRole('button', { name: '创建开户意向', exact: true }).click()
  await confirmDialog()
  const target = (await api('/api/v1/accounts?limit=100')).find(a => a.display_name === accountName)
  assert.ok(target)
  pass('Browser account creation persists only after explicit authorization')

  const before = (await api('/api/v1/accounts?limit=100')).find(a => a.id === source.id)
  await page.locator('.security-layout select').nth(1).selectOption(target.id)
  await page.getByLabel('转账金额 / 元', { exact: true }).fill('0.29')
  await page.getByLabel('转账用途', { exact: true }).fill('独立模拟用户联调')
  const count = requests.length
  await page.getByRole('button', { name: '创建转账意向', exact: true }).click()
  await page.getByRole('dialog').getByRole('checkbox').waitFor()
  assert.equal(requests.slice(count).filter(r => r.path.endsWith('/execute') || r.path.endsWith('/authorize')).length, 0)
  await page.screenshot({ path: `${out}/transfer-confirmation.png` })
  await confirmDialog()
  const after = await api('/api/v1/accounts?limit=100')
  assert.equal(after.find(a => a.id === source.id).balance_minor, before.balance_minor - 29)
  assert.equal(after.find(a => a.id === target.id).balance_minor, 29)
  assert.equal(requests.slice(count).filter(r => r.path.endsWith('/execute')).length, 1)
  const operations = await api('/api/v1/execution/operations?kind=transfer')
  const transfer = operations.find(o => o.parameters.description === '独立模拟用户联调' && o.target_account_id === target.id)
  assert.equal(transfer.status, 'succeeded')
  assert.ok((await api(`/api/v1/execution/operations/${transfer.id}/audits`)).length)
  pass('0.29 yuan transfer: exact cents, one execution, real debit/credit and persisted audit')

  await nav('智能账单分析')
  await page.getByPlaceholder('搜索商户名称').fill('模拟日常餐食')
  await page.getByRole('button', { name: '搜索', exact: true }).click()
  await page.locator('.transaction-table tbody tr').filter({ hasText: '模拟日常餐食' }).waitFor()
  await page.getByRole('button', { name: /^查看交易 / }).first().click()
  await page.locator('.transaction-detail .detail-grid').waitFor()
  pass('Real billing charts, merchant search and transaction detail')

  await nav('动态资金规划')
  await page.locator('.planning-goal-selection input[type="checkbox"]').first().check()
  for (const [label, value] of [['正常生活开支预留 / 元', '100'], ['必要支出预留 / 元', '200'], ['安全储备 / 元', '100'], ['现金缓冲 / 元', '100'], ['基准月收入 / 元', '1000']]) await page.getByLabel(label, { exact: true }).fill(value)
  await page.getByRole('button', { name: '保存并计算方案', exact: true }).click()
  await page.locator('.planning-options .plan-option').first().waitFor()
  const planId = await page.evaluate(() => localStorage.getItem('qingyingjin.plan'))
  const planBefore = await api(`/api/v1/financial-plans/${planId}`)
  await executeIntent('transfer', { source_account_id: source.id, target_account_id: target.id, amount_minor: 31, currency: 'CNY', description: '独立联调动态规划事件' })
  const planAfter = await eventually(() => api(`/api/v1/financial-plans/${planId}`), p => p.plan.current_version > planBefore.plan.current_version, 'automatic financial replan')
  assert.ok(planAfter.result.balance_minor < planBefore.result.balance_minor)
  await page.getByText(`规划版本 ${planAfter.plan.current_version - 1} → ${planAfter.plan.current_version}`, { exact: true }).waitFor()
  pass('Financial plan persists; ledger event automatically updates plan version and browser timeline')

  await nav('健康目标与目标金')
  await page.getByLabel('用自然语言描述你的健康生活计划').fill(expectCoze ? '未来3个月每月储蓄100元，保持正常饮食与规律运动，每月必要开支200元，每月生活预算300元，每月收入1000元。' : `正常饮食与规律运动，三个月储蓄计划 ${stamp}`)
  await page.getByRole('button', { name: '保存生活目标', exact: true }).click()
  await page.getByRole('heading', { name: '目标解析与约束', exact: true }).waitFor()
  await page.getByRole('button', { name: '解析目标', exact: true }).click()
  if (expectCoze) {
    await page.locator('.parsed-goal').waitFor({ timeout: 90000 })
    const lifeId = await page.evaluate(() => JSON.parse(localStorage.getItem('qingyingjin.goals'))[0])
    const parsed = (await api(`/api/v1/goals/${lifeId}`)).version.parsed
    assert.equal(parsed.period_months, 3)
    assert.equal(parsed.savings_monthly_minor, 10000)
    assert.equal(parsed.monthly_income_minor, 100000)
    pass('Life goal is parsed by the published real-model Coze workflow')
  } else {
    await page.locator('.toast').filter({ hasText: /Coze|workflow|COZE|配置/i }).waitFor()
    pass('Life goal persists; absent Coze workflow produces an explicit service error')
  }

  const merchant = `联调模拟订阅 ${stamp}`
  for (let i = 3; i >= 1; i--) await executeIntent('import_transaction', {
    bank_account_id: source.id, external_transaction_id: `${stamp}-sub-${i}`, direction: 'expense',
    amount_minor: 1900, currency: 'CNY', category: 'sports', merchant_name: merchant,
    occurred_at: Date.UTC(now.getUTCFullYear(), now.getUTCMonth() - i, 5, 12), effect_mode: 'record_only',
  })
  await api('/api/v1/subscriptions/detect_subscriptions', { from: new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() - 4, 1)).toISOString(), to })
  let subscription = (await api('/api/v1/subscriptions')).subscriptions.find(v => v.subscription.merchant_name === merchant)?.subscription
  assert.ok(subscription, 'Recorded recurring payments should produce a real subscription candidate')
  await executeIntent('subscription_manage', { subscription_id: subscription.id, input: { expected_version: subscription.version, confirm_local_simulation: true } })
  await nav('订阅与代扣')
  await page.getByLabel('搜索订阅', { exact: true }).fill(merchant)
  await page.getByRole('button', { name: '取消订阅', exact: true }).click()
  await confirmDialog()
  subscription = (await api(`/api/v1/subscriptions/${subscription.id}`)).subscription
  assert.equal(subscription.status, 'cancelled')
  pass('Persisted recurring-payment evidence, subscription forecast and authorized local cancellation')

  const orchestration = await api('/api/v1/orchestrations', { strategy: 'parallel_read', use_coze: expectCoze, timeout_ms: 60000, tasks: [
    { key: 'billing', agent_type: 'billing', tool: 'analyze_spending', parameters: { from, to, account_id: source.id }, dependencies: [], priority: 20 },
    { key: 'subscriptions', agent_type: 'subscription', tool: 'analyze_subscription_cost', parameters: {}, dependencies: [], priority: 10 },
    { key: 'savings', agent_type: 'savings', tool: 'get_financial_plan', parameters: { plan_id: planId }, dependencies: ['billing', 'subscriptions'], priority: 0 },
  ] }, 'POST', crypto.randomUUID())
  const jobId = orchestration.job.id
  const finalJob = await eventually(() => api(`/api/v1/orchestrations/${jobId}`), j => ['succeeded', 'failed'].includes(j.job.status), 'real worker orchestration')
  assert.equal(finalJob.job.status, 'succeeded')
  if (expectCoze) assert.ok(finalJob.tasks.every(t => t.output?.workflow_execute_id && t.output?.explanation), 'Each specialist must return a real model execution ID and explanation')
  const stream = await fetch(`${base}/api/v1/orchestrations/${jobId}/events`, { headers: { Authorization: `Bearer ${token}`, 'Last-Event-ID': '0' }, signal: AbortSignal.timeout(5000) })
  assert.equal(stream.status, 200)
  const frames = await stream.text()
  assert.ok(frames.includes('event:') && frames.includes('id:'), 'Real SSE must replay persisted events')
  await nav('多智能体协同')
  await page.getByLabel('任务编号', { exact: true }).fill(jobId)
  await page.getByRole('button', { name: '查询任务', exact: true }).click()
  await page.locator('.flow-node').first().waitFor()
  await page.locator('.flow-node').first().click()
  await page.getByText('真实工具返回结果', { exact: true }).waitFor()
  pass('Real worker executes parallel/dependent tasks; SSE replay and browser node detail')

  await nav('AI 智能金融助手')
  await page.getByLabel('金融任务指令', { exact: true }).fill(expectCoze ? '分析本月消费，只需一个账单分析任务，不执行金融操作。' : '分析我的消费，制定三个月健康储蓄计划。')
  const naturalLanguageResponse = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/orchestrations' && r.request().method() === 'POST')
  await page.getByRole('button', { name: '发送', exact: true }).click()
  const naturalLanguage = await naturalLanguageResponse
  const naturalEnvelope = await naturalLanguage.json()
  if (expectCoze) {
    assert.equal(naturalLanguage.status(), 201)
    const final = await eventually(() => api(`/api/v1/orchestrations/${naturalEnvelope.data.job.id}`), j => ['succeeded', 'failed'].includes(j.job.status), 'real Coze model orchestration')
    assert.equal(final.job.status, 'succeeded', JSON.stringify(final.tasks.map(t => ({ status: t.status, error: t.error_code, message: t.error_message }))))
    assert.ok(final.tasks.some(t => t.agent_type === 'billing' && t.status === 'succeeded'))
    const modelResult = await api(`/api/v1/orchestrations/${naturalEnvelope.data.job.id}/result`)
    await writeFile(`${out}/coze-orchestration.json`, JSON.stringify(modelResult, null, 2))
    await page.screenshot({ path: `${out}/coze-assistant.png`, fullPage: true })
    pass('Real model compiles a task; bank worker executes it and browser receives SSE state')
  } else if (naturalLanguage.status() === 503) {
    assert.equal(naturalEnvelope.code, 'COZE_UNAVAILABLE')
    await page.locator('.composer .form-error').filter({ hasText: /Supervisor|Coze/i }).waitFor()
  } else {
    assert.equal(naturalLanguage.status(), 201)
    const failed = await eventually(() => api(`/api/v1/orchestrations/${naturalEnvelope.data.job.id}`), j => j.job.status === 'failed', 'unconfigured Coze worker error')
    assert.ok(failed.tasks.some(t => t.error_code === 'COZE_UNAVAILABLE'))
    await page.locator('.agent-message').filter({ hasText: 'COZE_UNAVAILABLE' }).waitFor()
  }
  if (!expectCoze) pass('Natural-language assistant reports real COZE_UNAVAILABLE without fabricated model output')

  for (const width of [1440, 1024, 768, 375]) {
   await page.setViewportSize({ width, height: 1000 })
   for (const [name, label] of [['overview', 'AI Banking 总览'], ['assistant', 'AI 智能金融助手'], ['agents', '多智能体协同'], ['billing', '智能账单分析'], ['subscriptions', '订阅与代扣'], ['goals', '健康目标与目标金'], ['planning', '动态资金规划'], ['security', '可信执行与安全']]) {
    await nav(label); await page.waitForTimeout(250)
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
    assert.equal(await page.locator('.error-state, vite-error-overlay').count(), 0, `${name} rendered an API error`)
    await page.screenshot({ path: `${out}/${name}-${width}.png`, fullPage: true })
   }
  }
  assert.deepEqual(runtimeErrors, [])
  pass('Eight real-data routes at 1440/1024/768/375px: no overflow, API error states or browser runtime errors')
  await writeFile(`${out}/results.json`, JSON.stringify({ base, userId, checks, runtimeErrors, planId, jobId }, null, 2))
} catch (error) {
  await page.screenshot({ path: `${out}/failure.png`, fullPage: true })
  console.error('Visible service error:', await page.locator('.form-error, .toast').allTextContents())
  throw error
} finally { await browser.close() }
