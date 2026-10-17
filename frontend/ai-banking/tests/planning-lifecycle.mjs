// Browser fixtures only: every API request is intercepted; no funds or models are called.
import { chromium } from '@playwright/test'
import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'

const browser = await chromium.launch({
  headless: true,
  executablePath: process.env.BANKING_TEST_BROWSER || 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',
})
const origin = process.env.BANKING_UI_ORIGIN || 'http://127.0.0.1:5178'
const results = []
async function exercise(name, work) {
  const context = await browser.newContext(), page = await context.newPage()
  let profileRoute, createRoute
  const reply = (route, data) => route.fulfill({
    status: 200, contentType: 'application/json',
    body: JSON.stringify({ code: 'OK', message: 'success', data }),
  })
  await page.route(/\/(?:product\/)?api\/v[12]\//, async route => {
    const path = new URL(route.request().url()).pathname
    if (path === '/api/v1/auth/capabilities') return reply(route, { sms_enabled: false, sms_mode: 'disabled', registration_verification_required: false, code_expires_in: 300, resend_after: 60 })
    if (path === '/api/v1/auth/refresh') return route.fulfill({ status: 401, json: { code: 'AUTH_INVALID_CREDENTIALS', message: '测试尚未登录' } })
    if (path === '/api/v1/auth/login') return reply(route, { access_token: 'fixture-planning-lifecycle-token', expires_in: 900, user: { id: '42', display_name: '规划生命周期测试用户' }, phone_masked: '138****8000', phone_verified: false, phone_verification_method: '' })
    if (path === '/product/api/v1/profile') { profileRoute = route; return }
    if (path === '/api/v1/financial-plans' && route.request().method() === 'POST') { createRoute = route; return }
    let data = []
    if (path === '/api/v1/users/me') data = { id: '42', display_name: '规划生命周期测试用户' }
    if (path === '/api/v1/accounts') data = [{ id: '101', display_name: '测试账户', account_type: 'checking', status: 'active', currency: 'CNY', balance_minor: 100000 }]
    if (path === '/api/v1/savings/goals') data = [{ id: '71', name: '测试健康目标金', target_minor: 60000 }]
    if (/\/financial-plans\/\d+$/.test(path)) data = {
      plan: { id: '901', name: '测试计划', current_version: 1, settings: { account_ids: [101], savings_goal_ids: [71], life_goal_ids: [], safety_reserve_minor: 10000, cash_buffer_minor: 1000, baseline_income_minor: 100000, cashflows: [] } },
      version: { id: '1', version: 1 }, result: { options: [], input_snapshot: {} }, decisions: [],
    }
    await reply(route, data)
  })
  try {
    await page.goto(origin + '/planning')
    await page.getByRole('button', { name: '登录 / 注册', exact: true }).click()
    await page.getByLabel('手机号', { exact: true }).fill('13800138000')
    await page.getByLabel('密码', { exact: true }).fill('fixture-planning-lifecycle-password')
    await page.getByRole('button', { name: '登录', exact: true }).click()
    await page.getByRole('dialog').waitFor({ state: 'hidden' })
    await work({ page, reply, getProfile: () => profileRoute, getCreate: () => createRoute })
    results.push({ name, passed: true })
  } catch (error) {
    results.push({ name, passed: false, error: error.message })
  } finally { await context.close() }
}
try {
  await exercise('late profile response does not notify after leaving Planning', async ({ page, reply, getProfile }) => {
    await page.getByRole('button', { name: '应用资金安全偏好', exact: true }).click()
    await page.getByRole('link', { name: '多智能体协同', exact: true }).click()
    await page.getByRole('heading', { name: '多智能体协同中心', exact: true }).waitFor()
    await reply(getProfile(), { safety_reserve_minor: 10029, revision: 1, timezone: 'Asia/Shanghai' })
    await page.waitForTimeout(100)
    assert.equal(await page.getByText('资金安全偏好已填入表单，保存计划后生效', { exact: true }).count(), 0)
  })
  await exercise('lookup is locked and a late create response cannot replace selection after navigation', async ({ page, reply, getCreate }) => {
    await page.getByLabel('正常生活开支预留 / 元').fill('300')
    await page.getByLabel('必要支出预留 / 元').fill('600')
    await page.getByLabel('安全储备 / 元', { exact: true }).fill('100')
    await page.getByLabel('现金缓冲 / 元').fill('100')
    await page.getByLabel('基准月收入 / 元').fill('1000')
    await page.getByRole('checkbox', { name: /测试健康目标金/ }).check()
    const requested = page.waitForRequest(r => new URL(r.url()).pathname === '/api/v1/financial-plans' && r.method() === 'POST')
    await page.getByRole('button', { name: '保存并计算方案', exact: true }).click()
    await requested
    assert.equal(await page.locator('.plan-lookup input').isDisabled(), true)
    assert.equal(await page.locator('.plan-lookup button').isDisabled(), true)
    await page.getByRole('link', { name: '多智能体协同', exact: true }).click()
    await page.getByRole('heading', { name: '多智能体协同中心', exact: true }).waitFor()
    await reply(getCreate(), { plan: { id: '901' } })
    await page.waitForTimeout(100)
    const references = await page.evaluate(() => JSON.parse(localStorage.getItem('qingrongjin.refs.v1.42') || '{}'))
    assert(!references.plan, 'Unmounted Planning must not update the current plan reference')
    assert.equal(await page.getByText('资金规划已保存，未执行资金操作', { exact: true }).count(), 0)
  })
  await exercise('a create response received after navigation cannot update the selected plan', async ({ page, reply, getCreate }) => {
    await page.getByLabel('正常生活开支预留 / 元').fill('300')
    await page.getByLabel('必要支出预留 / 元').fill('600')
    await page.getByLabel('安全储备 / 元', { exact: true }).fill('100')
    await page.getByLabel('现金缓冲 / 元').fill('100')
    await page.getByLabel('基准月收入 / 元').fill('1000')
    await page.getByRole('checkbox', { name: /测试健康目标金/ }).check()
    const requested = page.waitForRequest(r => new URL(r.url()).pathname === '/api/v1/financial-plans' && r.method() === 'POST')
    await page.getByRole('button', { name: '保存并计算方案', exact: true }).click()
    await requested
    await page.getByRole('link', { name: '多智能体协同', exact: true }).click()
    await page.getByRole('heading', { name: '多智能体协同中心', exact: true }).waitFor()
    await reply(getCreate(), { plan: { id: '901' } })
    await page.waitForTimeout(100)
    const references = await page.evaluate(() => JSON.parse(localStorage.getItem('qingrongjin.refs.v1.42') || '{}'))
    assert(!references.plan, 'Unmounted Planning must not update the current plan reference')
    assert.equal(await page.getByText('资金规划已保存，未执行资金操作', { exact: true }).count(), 0)
  })
} finally { await browser.close() }
await mkdir('test-results', { recursive: true })
await writeFile('test-results/planning-lifecycle.json', JSON.stringify({ fixture_only: true, results }, null, 2))
console.log(JSON.stringify(results, null, 2))
assert(results.every(result => result.passed), 'Planning lifecycle regressions failed')
