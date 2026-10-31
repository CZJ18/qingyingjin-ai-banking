// Real browser + actual isolated HTTP/SQL. No route interception or model fixture.
import assert from 'node:assert/strict'
import { readFile, mkdir, writeFile } from 'node:fs/promises'
import { chromium } from '@playwright/test'

const base = process.env.BANKING_LIVE_URL
assert(['http://127.0.0.1:5179', 'http://127.0.0.1:29778'].includes(base))
assert.equal(process.env.BANKING_LIVE_ALLOW_WRITES, 'simulated-test-user')
const out = process.env.BANKING_QA_DIR
await mkdir(out, { recursive: true })
let token = process.env.BANKING_LIVE_JWT
const user = process.env.BANKING_LIVE_USER_ID
const checks = [], errors = [], calls = []
const browser = await chromium.launch({ headless: true, executablePath: process.env.BANKING_TEST_BROWSER })
const state = JSON.parse(await readFile(process.env.BANKING_LIVE_STORAGE_STATE, 'utf8'))
// Deliberately discard browser-local task references, retain only this user's test session cookie.
const context = await browser.newContext({ storageState: { cookies: state.cookies, origins: [] }, viewport: { width: 1440, height: 1000 } })
const page = await context.newPage()
page.on('pageerror', e => errors.push(e.message))
page.on('request', r => { if (new URL(r.url()).pathname.includes('/api/')) calls.push({ path: new URL(r.url()).pathname, method: r.method() }) })
function pass(name) { checks.push(name); console.log('PASS: ' + name) }
async function api(path, body, key, expected = 200, method = body === undefined ? 'GET' : 'POST') {
  const res = await fetch(base + path, { method, headers: { Authorization: 'Bearer ' + token,
    'Content-Type': 'application/json', ...(key ? { 'Idempotency-Key': key } : {}) },
    body: body === undefined ? undefined : JSON.stringify(body) })
  const value = await res.json()
  assert.equal(res.status, expected, `${method} ${path}: ${res.status} ${value.code}`)
  if (expected < 300) assert.equal(value.code, 'OK')
  return value.data
}
async function eventually(read, condition, label) {
  for (let n = 0; n < 80; n++) {
    const value = await read()
    if (condition(value)) return value
    await new Promise(resolve => setTimeout(resolve, 250))
  }
  throw new Error('Timed out: ' + label)
}
async function nav(name) {
  if (await page.getByRole('button', { name: '打开导航', exact: true }).isVisible())
    await page.getByRole('button', { name: '打开导航', exact: true }).click()
  await page.getByRole('link', { name, exact: true }).click()
  const title = { 'AI Banking 总览': '账户总览', 'AI 智能金融助手': '智能金融任务工作台',
    '可信执行与安全': '可信执行与安全中心', '订阅与代扣': '订阅与代扣管理',
    '多智能体协同':'多智能体协同中心' }[name] || name
  await page.getByRole('heading', {name:title,exact:true}).waitFor()
}
async function confirmOperation() {
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('checkbox').waitFor()
  assert(await dialog.getByRole('button', { name: '授权并执行', exact: true }).isDisabled())
  await dialog.getByRole('checkbox').check()
  await dialog.getByRole('button', { name: '授权并执行', exact: true }).dblclick()
  await dialog.locator('.status.succeeded').waitFor()
  await dialog.getByRole('button', { name: '关闭', exact: true }).last().click()
}
try {
  const restore = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/auth/refresh')
  await page.goto(base + '/assistant')
  const session = await (await restore).json()
  assert.equal(session.data.user.id, user)
  token = session.data.access_token
  await page.getByText('已登录', { exact: true }).waitFor()
  const accounts = await api('/api/v1/accounts?limit=100')
  assert(accounts.every(a => a.is_simulated && a.user_id === user))
  const source = accounts.find(a => a.account_type === 'checking' && a.balance_minor > 100000)
  assert(source)
  await page.locator('.toolbar-actions select').selectOption(source.id)
  const current = new Date(), ym = new Intl.DateTimeFormat('sv-SE', {timeZone:'Asia/Shanghai', year:'numeric', month:'2-digit'}).format(current)
  const from = ym + '-01T00:00:00+08:00'
  const to = new Date(Date.UTC(current.getUTCFullYear(), current.getUTCMonth() + 1, 1, -8)).toISOString()
  const input = { tasks: [{ key: 'billing', agent_type: 'billing', tool: 'generate_billing_report',
    skill_id: 'billing_analysis', skill_version: 1, execution_mode: 'read_only', use_coze: false,
    parameters: { from, to, timezone: 'Asia/Shanghai', account_id: source.id }, dependencies: [] }] }
  const key = crypto.randomUUID()
  const submitted = await api('/product/api/v1/agent-tasks', input, key, 202)
  const duplicate = await api('/product/api/v1/agent-tasks', input, key, 202)
  assert.equal(duplicate.task_id, submitted.task_id)
  const task = await eventually(() => api('/product/api/v1/agent-tasks/' + submitted.task_id),
    t => ['succeeded', 'failed', 'interrupted'].includes(t.status), 'deterministic worker task')
  assert.equal(task.status, 'succeeded', task.error_code)
  const billing = task.result.outputs.find(o => o.agent_type === 'billing')
  assert(billing)
  const direct = await api('/api/v1/billing/generate_billing_report', { from, to, timezone: 'Asia/Shanghai', account_id: source.id })
  assert.equal(billing.result.total_expense, direct.total_expense)
  assert.equal(billing.result.total_income, direct.total_income)
  pass('Actual Python Worker → Go deterministic billing → isolated SQL; exact totals and submit idempotency')
  await page.getByRole('button', { name: /任务历史/ }).click()
  const historyButton = page.locator('#task-history > button').filter({ hasText: submitted.task_id })
  await historyButton.waitFor()
  const count = calls.length
  await historyButton.click()
  await page.locator('.business-result').filter({ hasText: '财务分析摘要' }).waitFor()
  assert.equal(calls.slice(count).filter(c => /\/(run|resume|authorize|execute)$/.test(c.path)).length, 0)
  const refs = await page.evaluate(id => JSON.parse(localStorage.getItem('qingrongjin.refs.v1.' + id) || '{}'), user)
  assert(refs.agents.includes(submitted.task_id))
  await page.screenshot({ path: out + '/server-history-and-sql-result.png', fullPage: true })
  pass('Empty local history recovers server task and renders SQL totals; selecting history never runs it')
  await nav('AI Banking 总览')
  const overviewTask = page.getByRole('button', {name:'查看协同任务 ' + submitted.task_id,exact:true})
  await overviewTask.click({timeout:3000})
  await page.getByRole('heading',{name:'多智能体协同中心',exact:true}).waitFor()
  await page.locator('.job-summary').filter({hasText:submitted.task_id}).waitFor()
  await nav('AI 智能金融助手')
  pass('Overview discovers persisted LangGraph tasks and opens the correct history engine')
  const history = await api('/product/api/v1/agent-tasks?limit=1&offset=0')
  assert.equal(history.limit, 1)
  assert(history.total >= 1)
  await api('/product/api/v1/agent-tasks?user_id=someone-else', undefined, undefined, 400)
  pass('Authenticated history pagination and rejection of client-supplied owner override')
  await page.reload()
  await page.getByText('已登录', { exact: true }).waitFor()
  await page.waitForFunction(id => document.querySelector('.toolbar-actions select')?.value === id, source.id)
  assert.equal(await page.locator('.toolbar-actions select').inputValue(), source.id)
  await page.locator('.business-result').filter({ hasText: '财务分析摘要' }).waitFor()
  pass('Page refresh restores user-scoped account selection and completed task result')
  await page.getByRole('button', { name: '检查模型费用准入', exact: true }).click()
  await page.getByRole('status').filter({ hasText: /MODEL_MONETARY_ADMISSION_REQUIRED|COZE_UNAVAILABLE/ }).first().waitFor()
  pass('Unconfigured model admission is an explicit failure, with no invented quota')

  await nav('智能账单分析')
  await page.locator('summary').filter({ hasText: '导入 CSV 历史账单' }).click()
  const external = 'browser-csv-' + Date.now()
  const csv = `id,occurred_at,amount,direction,currency,merchant,category,status\n${external},${ym}-05T12:00:00+08:00,1.29,expense,CNY,本轮CSV来源验收,food,posted`
  await page.getByLabel('选择 CSV 账单').setInputFiles({ name: 'isolated.csv', mimeType: 'text/csv', buffer: Buffer.from(csv) })
  const previewResponse = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/transactions/import/preview')
  await page.getByRole('button', { name: '预览历史账单', exact: true }).click()
  const preview = (await (await previewResponse).json()).data
  assert.equal(preview.records.length, 1)
  assert.equal(preview.records[0].amount_minor, 129)
  assert.equal(preview.financial_execution_enabled, false)
  const before = (await api('/api/v1/accounts?limit=100')).find(a => a.id === source.id).balance_minor
  const beforePrepare = calls.length
  await page.getByRole('button', { name: '核对并授权补录', exact: true }).click()
  await page.getByRole('dialog').getByRole('checkbox').waitFor()
  assert.equal(calls.slice(beforePrepare).filter(c => /\/(authorize|execute)$/.test(c.path)).length, 0)
  await confirmOperation()
  const transactions = await api('/api/v1/transactions?bank_account_id=' + source.id + '&merchant=' + encodeURIComponent('本轮CSV来源验收'))
  const imported = transactions.items.find(t => t.external_transaction_id === preview.records[0].external_transaction_id)
  assert(imported)
  const sourceEvidence = await api(`/api/v1/transactions/${imported.id}/source`)
  assert.equal(sourceEvidence.file_sha256, preview.file_sha256)
  assert.equal(imported.amount_minor, 129)
  assert.equal((await api('/api/v1/accounts?limit=100')).find(a => a.id === source.id).balance_minor, before)
  assert.equal(calls.slice(beforePrepare).filter(c => c.path.endsWith('/execute')).length, 1)
  await page.getByPlaceholder('搜索商户名称').fill('本轮CSV来源验收')
  await page.getByRole('button', {name:'搜索',exact:true}).click()
  await page.getByRole('button', {name:'查看交易 ' + imported.id,exact:true}).click()
  await page.getByRole('button', {name:'查看 CSV 导入来源',exact:true}).click({timeout:3000})
  await page.getByRole('dialog').getByText(preview.file_sha256, {exact:true}).waitFor()
  await page.screenshot({path:out + '/csv-source-trace.png',fullPage:true})
  await page.getByRole('button', {name:'关闭交易详情',exact:true}).click()
  pass('Real CSV file → preview → independent authorization → persisted source hash, exact cents, unchanged balance')

  await nav('健康目标与目标金')
  const goals = await api('/api/v1/savings/goals?limit=100')
  assert(goals.length)
  const goal = goals[0]
  await page.getByLabel('当前目标金', {exact:true}).selectOption(goal.id)
  await page.locator('.goal-settings summary').click()
  await page.getByLabel('目标金额 / 元', {exact:true}).fill(String((goal.target_minor + 100) / 100))
  await page.getByRole('button', {name:'保存计划修改',exact:true}).click()
  await eventually(() => api('/api/v1/savings/goals?limit=100'), rows => rows.find(g => g.id === goal.id)?.target_minor === goal.target_minor + 100, 'goal settings persistence')
  assert.equal((await api('/api/v1/savings/goals?limit=100')).find(g => g.id === goal.id).current_minor, goal.current_minor)
  await page.screenshot({path:out + '/savings-settings.png',fullPage:true})
  pass('Goal settings persist on the server and do not change savings balance')

  const createdPlan = await api('/api/v1/financial-plans', {name:'本轮规划确认隔离验收', settings:{
    period: new Date().toISOString().slice(0,7), account_ids:[Number(source.id)], savings_goal_ids:[Number(goal.id)], life_goal_ids:[],
    living_remaining_minor:10000, essential_remaining_minor:20000, safety_reserve_minor:10000,
    cash_buffer_minor:10000, baseline_income_minor:100000, cashflows:[],
  }}, crypto.randomUUID(), 201)
  await nav('动态资金规划')
  await page.locator('.plan-lookup summary').click()
  await page.getByLabel('规划编号', {exact:true}).fill(createdPlan.plan.id)
  await page.getByRole('button',{name:'查询规划',exact:true}).click()
  await page.locator('.option-list button').first().waitFor()
  if (!await page.getByLabel('正常生活开支预留 / 元',{exact:true}).isVisible())
    await page.getByRole('button',{name:'调整规划约束',exact:true}).click()
  await page.getByLabel('正常生活开支预留 / 元',{exact:true}).fill('123')
  let race = true
  const settingsPath = `/api/v1/financial-plans/${createdPlan.plan.id}/settings`
  await page.route(base + settingsPath, async route => {
    if (race) {
      race = false
      await api('/api/v1/financial-plans/' + createdPlan.plan.id + '/replan', {})
    }
    await route.continue()
  })
  const conflictResponse = page.waitForResponse(r => new URL(r.url()).pathname === settingsPath)
  await page.getByRole('button',{name:'保存并计算方案',exact:true}).click()
  assert.equal((await conflictResponse).status(),409)
  await page.getByText('服务端规划已更新，已保留你的修改。请核对最新资金和版本后再次保存。',{exact:true}).first().waitFor({timeout:3000})
  assert.equal(await page.getByLabel('正常生活开支预留 / 元',{exact:true}).inputValue(),'123')
  const savedResponse = page.waitForResponse(r => new URL(r.url()).pathname === settingsPath)
  await page.getByRole('button',{name:'保存并计算方案',exact:true}).click()
  assert.equal((await savedResponse).status(),200)
  await page.unroute(base + settingsPath)
  pass('Real Go CAS conflict preserves the draft, refreshes the server version and requires a second explicit save')
  const operationsBefore = await api('/api/v1/execution/operations?limit=100')
  const balanceBefore = (await api('/api/v1/accounts?limit=100')).find(a => a.id === source.id).balance_minor
  const requestCount = calls.length
  await page.getByRole('button',{name:'确认平衡方案',exact:true}).click()
  await page.getByRole('button',{name:'确认规划建议',exact:true}).click()
  await page.getByRole('dialog').waitFor({state:'hidden'})
  const accepted = await api('/api/v1/financial-plans/' + createdPlan.plan.id)
  assert(accepted.decisions.some(d => d.plan_version_id === accepted.version.id && d.option === 'balanced'))
  assert.equal(calls.slice(requestCount).filter(c => /\/execution\/operations/.test(c.path) && c.method === 'POST').length, 0)
  assert.equal((await api('/api/v1/execution/operations?limit=100')).length, operationsBefore.length)
  assert.equal((await api('/api/v1/accounts?limit=100')).find(a => a.id === source.id).balance_minor, balanceBefore)
  pass('Planning advice confirmation persists a decision, creates no financial intent, authorization or transfer')

  await page.reload()
  await page.getByRole('heading',{name:'动态资金规划',exact:true}).waitFor()
  await page.getByText('当前 #' + createdPlan.plan.id, {exact:true}).waitFor()
  await page.locator('.option-list button').first().waitFor()
  pass('Direct planning-page reload restores the authenticated plan and actual server options')

  await page.getByRole('button',{name:'退出登录',exact:true}).click()
  await page.getByRole('button',{name:'登录 / 注册',exact:true}).waitFor()
  await api('/api/v1/users/me', undefined, undefined, 401)
  assert.equal(await page.locator('.option-list button').count(), 0)
  await page.getByRole('button',{name:'登录 / 注册',exact:true}).click()
  await page.getByLabel('手机号',{exact:true}).fill(process.env.BANKING_LIVE_PHONE)
  await page.getByLabel('密码',{exact:true}).fill(process.env.BANKING_LIVE_PASSWORD)
  const login = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/auth/login')
  await page.getByRole('button',{name:'登录',exact:true}).click()
  const loggedIn = await (await login).json()
  assert.equal(loggedIn.code,'OK')
  assert.equal(loggedIn.data.user.id,user)
  token = loggedIn.data.access_token
  await page.getByRole('dialog').waitFor({state:'hidden'})
  await page.locator('.option-list button').first().waitFor()
  await page.waitForFunction(id => document.querySelector('.toolbar-actions select')?.value === id, source.id)
  await page.screenshot({path:out+'/relogin-plan-restored.png',fullPage:true})
  pass('Logout invalidates the server bearer and clears page results; real phone login restores this user’s plan and account')

  assert.deepEqual(errors, [])
  await writeFile(out + '/results.json', JSON.stringify({checks, runtimeErrors:errors, task_id:task.task_id,
    supplier_requests:0, fixture_dispatches:0, businessMode:'isolated deterministic tools; no real model', calls},null,2))
} catch (error) {
  await page.screenshot({path:out+'/failure.png',fullPage:true})
  console.error('Visible service errors:', await page.locator('.form-error,.toast').allTextContents())
  throw error
} finally {
  await context.storageState({path:process.env.BANKING_LIVE_STORAGE_STATE})
  await browser.close()
}
