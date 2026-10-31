// Browser-only contract fixtures. No fixture adapter is shipped in the application.
import { chromium, expect } from '@playwright/test'
import assert from 'node:assert/strict'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
const birthdayOnly = process.argv.includes('--birthday-only')
const base = process.env.BANKING_TEST_BASE_URL || 'http://127.0.0.1:5178'
assert.equal(new URL(base).hostname, '127.0.0.1')
assert.equal(new URL(base).protocol, 'http:')
let calendar = { version: 1, enabled: true, lead_days: 30, entries: [
  { id: '00000000-0000-4000-8000-000000000042', name: '契约测试生日', month_day: '10-30' },
] }
let rejectNextBirthdaySave = false
let fixtureLoggedIn = false
const calendarView = () => ({ ...calendar, timezone: 'Asia/Shanghai', leap_day_policy: 'next_leap_year', upcoming: calendar.enabled ? calendar.entries.map(e => ({ ...e, next_date: '2026-10-30', days_until: 3 })) : [] })
const browser = await chromium.launch({
  headless: true,
  executablePath: process.env.BANKING_TEST_BROWSER,
})
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } })
const pageErrors = []
page.on('pageerror', (e) => pageErrors.push(e.message))
const calls = []
let operation
let finalStatus = 'executed'
const accounts = [
  {
    id: '101',
    display_name: '契约测试付款账户',
    masked_account_number: '**** 0101',
    balance_minor: 500000,
    status: 'active',
    account_type: 'checking',
    version: 1,
    is_simulated: true,
  },
  {
    id: '102',
    display_name: '契约测试收款账户',
    masked_account_number: '**** 0102',
    balance_minor: 0,
    status: 'active',
    account_type: 'checking',
    version: 1,
    is_simulated: true,
  },
]
const subscription = {
  id: '201',
  merchant_name: '契约测试订阅',
  category: 'education',
  amount_minor: 3900,
  billing_interval: 'monthly',
  next_billing_at: 1791657600000,
  status: 'active',
  version: 3,
  is_simulated: true,
}
const forecast = {
  monthly_minor: 3900,
  three_month_minor: 11700,
  annual_minor: 46800,
  available_balance_increase_minor: 0,
}
const report = {
  total_income: 250000,
  total_expense: 12000,
  category_breakdown: [{ category: 'education', expense_minor: 12000, transaction_ids: ['301'] }],
  budget_status: [],
  anomalies: [],
  daily: [{ label: '10-01', income_minor: 250000, expense_minor: 12000 }],
  findings: [{ finding_id: 'f1', description: '契约测试分析结果', transaction_ids: ['301'] }],
  transactions: [],
}
const opId = '00000000-0000-4000-8000-000000000001'
await page.route(/\/(?:product\/)?api\/v[12]\//, async (route) => {
  const req = route.request(),
    url = new URL(req.url()),
    path = url.pathname,
    body = req.postDataJSON()
  calls.push({ path, method: req.method(), body, headers: req.headers() })
  let data
  if (path === '/api/v1/auth/refresh' && !fixtureLoggedIn) return route.fulfill({ status: 401, json: { code: 'AUTH_INVALID_CREDENTIALS', message: '测试尚未登录' } })
  if (path === '/api/v1/auth/capabilities') data = { sms_enabled: false, sms_mode: 'disabled', registration_verification_required: false, code_expires_in: 300, resend_after: 60 }
  else if (path === '/api/v1/auth/login' || path === '/api/v1/auth/refresh') { fixtureLoggedIn = true; data = { access_token: 'browser-contract-test-token', expires_in: 900, user: { id: '42', display_name: '契约测试用户' }, phone_masked: '138****8000', phone_verified: false, phone_verification_method: '' } }
  else if (path === '/api/v1/users/me') data = { id: '42', display_name: '契约测试用户' }
  else if (path === '/product/api/v1/profile') data = { user_id: '42', safety_reserve_minor: 0, revision: 1, timezone: 'Asia/Shanghai' }
  else if (path === '/product/api/v1/recommendations') data = []
  else if (path === '/api/v1/accounts') data = accounts
  else if (path === '/api/v1/savings/goals') data = []
  else if (path === '/api/v1/billing/generate_billing_report') data = report
  else if (path === '/api/v1/transactions') data = { items: [], total: 0, offset: 0, limit: 10 }
  else if (path === '/api/v1/execution/risk-rules') data = []
  else if (path === '/api/v1/execution/scenarios') data = { operations: [], products: [], positions: [] }
  else if (path === '/api/v1/notifications/birthdays') {
    if (req.method() === 'PUT') {
      if (rejectNextBirthdaySave) {
        rejectNextBirthdaySave = false
        return route.fulfill({ status: 409, json: { code: 'VERSION_CONFLICT', message: '契约测试：版本已变化，请核对后重试', request_id: 'test' } })
      }
      assert.equal(body.expected_version, calendar.version)
      calendar = { version: calendar.version + 1, enabled: body.enabled, lead_days: body.lead_days, entries: body.entries }
    }
    data = calendarView()
  }
  else if (
    path === '/api/v1/subscriptions' ||
    path === '/api/v1/subscriptions/analyze_subscription_cost'
  )
    data = {
      subscriptions: [{ subscription }],
      duplicate_groups: [],
      active_recurring_forecast: forecast,
      cancelled_avoided_spend: { ...forecast, monthly_minor: 0 },
    }
  else if (path === '/api/v1/execution/operations' && req.method() === 'POST') {
    operation = {
      id: opId,
      kind: body.kind,
      parameters: body.parameters,
      snapshot: { source_account: accounts[0], target_account: accounts[1] },
      source_account_id: body.parameters.source_account_id || '0',
      target_account_id: body.parameters.target_account_id || '0',
      amount_minor: body.parameters.amount_minor || 0,
      currency: 'CNY',
      risk_level: 'high',
      status: 'pending_authorization',
      version: 1,
      intent_digest: 'server-digest',
      confirmation_nonce: 'server-nonce',
      expires_at: Date.now() + 300000,
      resource_id: '201',
      result: {},
    }
    data = { operation, authorizations: [], risk_events: [] }
  } else if (path === '/api/v1/execution/operations') data = operation ? [operation] : []
  else if (path.endsWith('/authorize')) {
    assert.equal(body.intent_digest, 'server-digest')
    assert.equal(body.confirmation_nonce, 'server-nonce')
    assert.equal(body.decision, 'approve')
    operation = {
      ...operation,
      status: 'authorized',
      version: 2,
      authorization_id: 'auth-server-id',
    }
    data = { operation, authorizations: [], risk_events: [] }
  } else if (path.endsWith('/execute')) {
    assert.equal(body.expected_version, 2)
    assert.equal(body.authorization_id, 'auth-server-id')
    assert.ok(req.headers()['idempotency-key'])
    operation = { ...operation, status: finalStatus, version: 3, result: { transfer_id: '701' } }
    data = { operation, authorizations: [], risk_events: [] }
  } else if (path.startsWith('/api/v1/execution/operations/'))
    data = { operation, authorizations: [], risk_events: [] }
  else
    return route.fulfill({
      status: 404,
      json: { code: 'NOT_FOUND', message: 'Contract fixture has no endpoint', request_id: 'test' },
    })
  return route.fulfill({ json: { code: 'OK', message: 'ok', request_id: 'test', data } })
})
await page.goto(base + (birthdayOnly ? '/scenarios' : '/'))
await page.getByRole('button', { name: '登录 / 注册', exact: true }).click()
await page.getByLabel('手机号', { exact: true }).fill('13800138000')
await page.getByLabel('密码', { exact: true }).fill('browser-contract-test-password')
await page.getByRole('button', { name: '登录', exact: true }).click()
await page.getByText('已登录', { exact: true }).waitFor()
if (birthdayOnly) {
  // Explicit browser API fixtures only: no SQL, real model, or fee calls.
  const results = []
  const artifacts = process.env.BANKING_TEST_ARTIFACT_DIR
  if (artifacts) mkdirSync(artifacts, { recursive: true })
  async function check(name, fn) {
    try { await fn(); results.push({ name, status: 'PASS' }) }
    catch (error) { results.push({ name, status: 'FAIL', error: error.message }) }
    console.log(JSON.stringify(results.at(-1)))
    if (artifacts) await page.screenshot({ path: join(artifacts, `case-${results.length}.png`), fullPage: true })
  }
  const panel = page.locator('.birthday-panel')
  await panel.getByRole('button', { name: '保存这个生日', exact: true }).waitFor()
  await check('birthday changes do not submit unsaved reminder settings', async () => {
    await panel.getByRole('checkbox', { name: '开启生日站内提醒' }).uncheck()
    await panel.getByRole('spinbutton', { name: '生日提醒提前天数' }).fill('7')
    await panel.getByRole('textbox', { name: '生日对象称呼' }).fill('契约新增生日')
    await panel.getByRole('textbox', { name: '生日月日' }).fill('10-31')
    await panel.getByRole('button', { name: '保存这个生日', exact: true }).click()
    await expect(panel.getByRole('list', { name: '已保存的生日' })).toContainText('契约新增生日')
    assert.equal(calendar.enabled, true)
    assert.equal(calendar.lead_days, 30)
    await expect(panel.getByRole('checkbox', { name: '开启生日站内提醒' })).not.toBeChecked()
    await expect(panel.getByRole('spinbutton', { name: '生日提醒提前天数' })).toHaveValue('7')
    await expect(panel.getByRole('button', { name: '保存生日提醒设置' })).toBeEnabled()
    await panel.getByRole('button', { name: '删除 契约新增生日 的生日', exact: true }).click()
    await expect(panel.getByRole('list', { name: '已保存的生日' })).not.toContainText('契约新增生日')
    assert.equal(calendar.enabled, true); assert.equal(calendar.lead_days, 30)
    await expect(panel.getByRole('button', { name: '保存生日提醒设置' })).toBeEnabled()
    rejectNextBirthdaySave = true
    await panel.getByRole('button', { name: '保存生日提醒设置' }).click()
    await expect(page.getByText('契约测试：版本已变化，请核对后重试', { exact: false })).toBeVisible()
    await expect(panel.getByRole('button', { name: '保存生日提醒设置' })).toBeEnabled()
    await expect(panel.getByRole('spinbutton', { name: '生日提醒提前天数' })).toHaveValue('7')
    await panel.getByRole('button', { name: '保存生日提醒设置' }).click()
    await expect(panel.locator('.birthday-upcoming')).toContainText('提醒已关闭')
    assert.equal(calendar.enabled, false); assert.equal(calendar.lead_days, 7)
  })
  // Reset only the test fixture between independent assertions.
  calendar = { ...calendar, enabled: true, lead_days: 30, entries: calendar.entries.filter(e => e.name === '契约测试生日') }
  await page.reload(); await panel.getByRole('button', { name: '填写庆祝计划' }).waitFor()
  for (const viewport of [{ width: 1440, height: 1000 }, { width: 390, height: 844 }]) {
    await check(`prefill preserves choices and reveals form ${viewport.width}`, async () => {
      await page.setViewportSize(viewport)
      await page.getByRole('combobox', { name: '场景资金账户' }).selectOption('101')
      await page.getByLabel('预留金额 / 元', { exact: true }).fill('750')
      const form = page.getByRole('region', { name: '庆祝跨场景计划' })
      await form.getByRole('checkbox', { name: '鲜花 ¥200.00', exact: true }).check()
      await form.getByRole('checkbox', { name: '蛋糕 ¥200.00', exact: true }).uncheck()
      await page.getByLabel('订购时间（北京时间）', { exact: true }).fill('11:15')
      await panel.getByRole('button', { name: '填写庆祝计划', exact: true }).click()
      const input = page.getByLabel('庆祝对象', { exact: true })
      await expect(input).toHaveValue('契约测试生日')
      await expect(page.getByLabel('预留金额 / 元', { exact: true })).toHaveValue('750')
      await expect(form.getByRole('checkbox', { name: '蛋糕 ¥200.00', exact: true })).not.toBeChecked()
      await expect(page.getByLabel('订购时间（北京时间）', { exact: true })).toHaveValue('11:15')
      await expect(page.getByRole('combobox', { name: '场景资金账户' })).toHaveValue('101')
      await expect(input).toBeInViewport(); await expect(input).toBeFocused()
      assert.equal(calls.filter(c => c.path.startsWith('/api/v1/execution/operations') && c.method === 'POST').length, 0)
    })
  }
  console.log(JSON.stringify({ mode: 'ISOLATED_BROWSER_API_FIXTURES_ONLY', results, pageErrors, real_model_requests: 0, real_database_writes: 0 }, null, 2))
  if (artifacts) writeFileSync(join(artifacts, 'results.json'), JSON.stringify({ mode: 'ISOLATED_BROWSER_API_FIXTURES_ONLY', results, pageErrors, real_model_requests: 0, real_database_writes: 0 }, null, 2))
  await browser.close()
  assert.deepEqual(pageErrors, [])
  assert.equal(results.filter(r => r.status === 'FAIL').length, 0)
} else {
await page.getByText('契约测试分析结果', { exact: true }).waitFor()
await page.getByRole('link', { name: '可信执行与安全', exact: true }).click()
await page.locator('.security-layout select').nth(1).selectOption('102')
await page.getByLabel('转账金额 / 元', { exact: true }).fill('0.29')
await page.getByLabel('转账用途', { exact: true }).fill('契约测试用途')
await page.getByRole('button', { name: '创建转账意向', exact: true }).click()
await page.getByRole('dialog').waitFor()
assert.equal(operation.amount_minor, 29)
assert.equal(calls.filter((c) => c.path.endsWith('/authorize')).length, 0)
assert.equal(calls.filter((c) => c.path.endsWith('/execute')).length, 0)
await page.getByRole('checkbox').check()
await page.getByRole('button', { name: '授权并执行', exact: true }).dblclick()
await page.getByRole('dialog').getByText('已执行', { exact: true }).waitFor()
assert.equal(calls.filter((c) => c.path.endsWith('/execute')).length, 1)
await page.getByRole('button', { name: '关闭', exact: true }).first().click()
console.log(
  'PASS: prepare → explicit confirmation → authorize → execute; exact cents, server version, duplicate prevention',
)
await page.getByRole('link', { name: '订阅与代扣', exact: true }).click()
await page.getByText('契约测试订阅', { exact: true }).first().waitFor()
await page.getByRole('button', { name: '取消订阅', exact: true }).click()
await page.getByRole('dialog').waitFor()
assert.equal(operation.kind, 'cancel_subscription')
assert.deepEqual(operation.parameters, { subscription_id: '201', expected_version: 3 })
assert.equal(operation.status, 'pending_authorization')
console.log(
  'PASS: subscription report adaptation and cancellation prepares intent before authorization',
)
await page.getByRole('checkbox').check()
finalStatus = 'failed'
await page.getByRole('button', { name: '授权并执行', exact: true }).click()
await page.getByRole('dialog').getByText('失败', { exact: true }).waitFor()
assert.equal(await page.getByRole('dialog').getByText('已执行', { exact: true }).count(), 0)
console.log('PASS: HTTP 200 with failed final operation never appears as successful transfer')
assert.deepEqual(pageErrors, [])
console.log('PASS: no browser runtime errors')
await browser.close()
}
