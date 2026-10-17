// Operator acceptance: actual local HTTP/DB/browser, no response interception.
import assert from 'node:assert/strict'
import { randomBytes, randomUUID } from 'node:crypto'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { mkdir, writeFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { resolve } from 'node:path'
import { chromium } from '@playwright/test'
const execute = promisify(execFile)
const root = fileURLToPath(new URL('../../../', import.meta.url))
const base = process.env.BANKING_CARD_URL || 'http://127.0.0.1:8088'
assert.ok(['127.0.0.1', 'localhost'].includes(new URL(base).hostname))
const out = resolve(root, 'output/card-management/live')
await mkdir(out, { recursive: true })
const checks = [],
  errors = []
let access = '',
  cookie = '',
  userId = '',
  accountId = '',
  failure = ''
async function api(path, body, expected = 200, authenticated = true) {
  const r = await fetch(base + path, {
    method: body === undefined ? 'GET' : 'POST',
    headers: {
      'Content-Type': 'application/json',
      Origin: base,
      'Idempotency-Key': randomUUID(),
      ...(authenticated && access ? { Authorization: 'Bearer ' + access } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const data = await r.json()
  if (r.status !== expected || data.code !== 'OK')
    throw new Error(path + ' HTTP ' + r.status + ' ' + data.code)
  if (path === '/api/v1/auth/register')
    cookie = r.headers.getSetCookie().find((c) => c.startsWith('qingrongjin_refresh=')) || ''
  return data.data
}
async function operation(kind, parameters) {
  let v = await api('/api/v1/execution/operations', { kind, parameters }, 201)
  v = await api('/api/v1/execution/operations/' + v.operation.id + '/authorize', {
    decision: 'approve',
    intent_digest: v.operation.intent_digest,
    confirmation_nonce: v.operation.confirmation_nonce,
  })
  v = await api('/api/v1/execution/operations/' + v.operation.id + '/execute', {
    authorization_id: v.operation.authorization_id,
    intent_digest: v.operation.intent_digest,
    expected_version: v.operation.version,
  })
  assert.equal(v.operation.status, 'succeeded')
  return v.operation.result
}
const browser = await chromium.launch({
  headless: true,
  executablePath:
    process.env.BANKING_TEST_BROWSER ||
    'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',
})
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
const page = await context.newPage()
page.setDefaultTimeout(30000)
page.on('pageerror', (error) => errors.push(error.message))
const dialog = page.getByRole('dialog')
async function confirm() {
  await dialog.getByRole('checkbox').check()
  await dialog.getByRole('button', { name: '授权并执行', exact: true }).click()
  await dialog.getByRole('button', { name: '关闭', exact: true }).last().click()
}
try {
  const capabilities = await api('/api/v1/auth/capabilities', undefined, 200, false)
  assert.equal(capabilities.sms_mode, 'development')
  const phone = '188' + String(randomBytes(4).readUInt32BE() % 100000000).padStart(8, '0'),
    password = randomBytes(24).toString('base64url')
  const challenge = await api('/api/v1/auth/sms/registration', { phone }, 200, false)
  const inbox = await execute(
    resolve(root, 'output/sms-auth/sms-inbox.exe'),
    [
      '-env-file',
      resolve(root, 'deploy/banking-postgres/finance.app.env.local'),
      '-env-file',
      resolve(root, 'deploy/banking/sms.env.local'),
      '-phone',
      phone,
      '-purpose',
      'register',
      '-allow-development-code',
    ],
    { timeout: 10000, windowsHide: true },
  )
  const code = JSON.parse(inbox.stdout).code
  assert.ok(/^[0-9]{6}$/.test(code))
  const registered = await api(
    '/api/v1/auth/register',
    { phone, password, display_name: '银行卡隔离验收', challenge_id: challenge.challenge_id, code },
    201,
    false,
  )
  access = registered.access_token
  userId = registered.user.id
  const account = await operation('open_account', {
    display_name: '银行卡验收账户',
    account_type: 'checking',
    currency: 'CNY',
    initial_balance_minor: 0,
    confirm_simulated_funds: true,
  })
  accountId = account.id
  assert.ok(accountId)
  assert.equal((await api('/api/v1/cards')).length, 0)
  checks.push(
    'actual verified local development user; new zero-balance simulated account; empty cards',
  )
  const pair = cookie.split(';')[0],
    separator = pair.indexOf('=')
  assert.ok(separator > 0)
  await context.addCookies([
    {
      name: pair.slice(0, separator),
      value: pair.slice(separator + 1),
      domain: new URL(base).hostname,
      path: '/api/v1/auth',
      httpOnly: true,
      secure: false,
      sameSite: 'Strict',
    },
  ])
  await page.goto(base + '/security')
  const manager = page.locator('.card-manager')
  await manager.getByRole('heading', { name: '我的银行卡', exact: true }).waitFor()
  async function bind(name, type, suffix) {
    if (!(await manager.getByLabel('卡片名称', { exact: true }).count()))
      await manager.getByRole('button', { name: '绑定模拟卡', exact: true }).click()
    await manager.getByLabel('关联模拟账户', { exact: true }).selectOption(accountId)
    await manager.getByLabel('卡片名称', { exact: true }).fill(name)
    await manager.getByLabel('发行机构（模拟）', { exact: true }).fill('模拟发卡行')
    await manager.getByLabel('卡片类型', { exact: true }).selectOption(type)
    await manager.getByLabel('模拟尾号（仅四位）', { exact: true }).fill(suffix)
    if (type === 'credit')
      await manager.getByLabel('模拟信用额度 / 元', { exact: true }).fill('1000')
    await manager.getByRole('button', { name: '创建绑定意向', exact: true }).click()
    await dialog.getByText('管理模拟银行卡', { exact: true }).waitFor()
    assert.equal(
      await dialog.getByRole('button', { name: '授权并执行', exact: true }).isDisabled(),
      true,
    )
    assert.ok((await dialog.innerText()).includes(suffix))
    const before = await api('/api/v1/cards')
    assert.ok(!before.some((c) => c.name === name))
    await confirm()
    await manager.getByRole('heading', { name, exact: true }).waitFor()
  }
  await bind('验收借记卡', 'debit', '2468')
  checks.push(
    'real UI debit binding requires explicit authorization and does not post before confirmation',
  )
  await bind('验收信用卡', 'credit', '1357')
  let portfolio = await api('/api/v1/cards')
  assert.equal(portfolio.length, 2)
  const credit = portfolio.find((c) => c.card_type === 'credit')
  assert.equal(credit.credit_available_minor, 100000)
  assert.equal(credit.credit_used_minor, 0)
  checks.push('persisted credit limit and deterministic available amount; separate from cash')
  const creditArticle = manager
    .locator('article')
    .filter({ has: page.getByRole('heading', { name: '验收信用卡', exact: true }) })
  await creditArticle.getByRole('button', { name: '设为默认', exact: true }).click()
  await confirm()
  await creditArticle.getByText('默认卡', { exact: true }).waitFor()
  assert.equal((await api('/api/v1/cards')).filter((c) => c.is_default).length, 1)
  checks.push('authorized default card persisted with one default')
  await page.screenshot({ path: resolve(out, 'desktop.png'), fullPage: true })
  await creditArticle.getByRole('button', { name: '解绑', exact: true }).click()
  await confirm()
  await creditArticle.getByText('已解绑 · 保留历史', { exact: true }).waitFor()
  await page.reload()
  await manager.getByText('已解绑 · 保留历史', { exact: true }).waitFor()
  portfolio = await api('/api/v1/cards')
  assert.equal(portfolio.find((c) => c.id === credit.id).status, 'unbound')
  assert.equal(portfolio.filter((c) => c.is_default).length, 0)
  checks.push('authorized unbind retains historical card across actual browser reload')
  const balance = await api('/api/v1/accounts/' + accountId + '/balance')
  assert.equal(balance.balance_minor, 0)
  assert.equal(balance.ledger_balance_minor, 0)
  assert.equal((await api('/api/v1/accounts/' + accountId + '/ledger')).length, 0)
  checks.push('all card operations leave cash and ledger unchanged')
  await page.setViewportSize({ width: 390, height: 844 })
  await page.screenshot({ path: resolve(out, 'mobile.png'), fullPage: true })
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
  assert.deepEqual(errors, [])
  checks.push('mobile layout and no uncaught browser errors')
} catch (error) {
  failure = error.message
  await page.screenshot({ path: resolve(out, 'failure.png'), fullPage: true }).catch(() => {})
} finally {
  if (access)
    await fetch(base + '/api/v1/auth/logout', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Origin: base, Cookie: cookie.split(';')[0] },
      body: '{}',
    }).catch(() => {})
  await writeFile(
    resolve(out, 'acceptance.json'),
    JSON.stringify(
      {
        passed: !failure,
        scope: 'actual local simulated account and browser; no API interception; no real SMS',
        user_id: userId,
        account_id: accountId,
        checks,
        errors,
        failure,
      },
      null,
      2,
    ),
  )
  await browser.close()
}
if (failure) throw new Error('Card live acceptance failed: ' + failure)
console.log(
  checks.length + ' live card acceptance groups passed; no real SMS or banking services called',
)
