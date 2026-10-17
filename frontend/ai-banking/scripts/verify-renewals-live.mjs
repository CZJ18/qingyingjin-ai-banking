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
const base = process.env.BANKING_RENEWAL_URL || 'http://127.0.0.1:8088'
assert.ok(['127.0.0.1', 'localhost'].includes(new URL(base).hostname))
const out = resolve(root, 'output/renewal-reminders/live')
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
    { phone, password, display_name: '续费提醒隔离验收', challenge_id: challenge.challenge_id, code },
    201,
    false,
  )
  access = registered.access_token
  userId = registered.user.id
  const account = await operation('open_account', {
    display_name: '续费提醒验收账户',
    account_type: 'checking',
    currency: 'CNY',
    initial_balance_minor: 0,
    confirm_simulated_funds: true,
  })
  accountId = account.id
  assert.ok(accountId)
  assert.equal((await api('/api/v1/notifications/renewals')).items.length, 0)
  checks.push(
    'actual registered local development user and zero-balance simulated account',
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

  // Operator-only fixture: initialize one simulated subscription for this NEW user.
  // All reminder reads/settings/acknowledgments below use real HTTP; no route mocks.
  assert.match(userId, /^[1-9][0-9]*$/)
  assert.match(accountId, /^[1-9][0-9]*$/)
  const chargeAt = Date.now() + 2 * 86400000, now = Date.now()
  const fixture = await execute('docker', ['exec', 'qingrongjin-agent-banking-agent-postgres-1', 'psql', '-U', 'postgres', '-d', 'qingrongjin_finance', '-At', '-v', 'ON_ERROR_STOP=1', '-c',
    `INSERT INTO banking_subscriptions(user_id,bank_account_id,fingerprint,merchant_name,category,billing_interval,amount_minor,currency,next_billing_at,confidence_bps,status,version,is_simulated,created_at,updated_at) VALUES(${userId},${accountId},'${randomUUID()}','续费提醒验收服务','subscription','monthly',2900,'CNY',${chargeAt},10000,'active',1,TRUE,${now},${now}) RETURNING id;`], { windowsHide: true, timeout: 10000 })
  const subId = fixture.stdout.trim().split(/\r?\n/)[0]
  assert.match(subId, /^[1-9][0-9]*$/)
  // No configure/scan API call: wait for the production background worker.
  const deadline = Date.now() + 80000
  let notice
  while (Date.now() < deadline) {
    const v = await api('/api/v1/notifications/renewals')
    notice = v.items.find(n => n.subscription_id === subId)
    if (notice) break
    await new Promise(r => setTimeout(r, 1000))
  }
  assert.ok(notice, 'background worker must generate the notice')
  assert.equal(notice.amount_minor, 2900)
  assert.equal(notice.status, 'unread')
  checks.push('production background worker persists a renewal notice without a configure or scan request')
  await page.goto(base + '/subscriptions')
  const panel = page.locator('.renewal-panel')
  await panel.getByRole('heading', { name: '续费提醒', exact: true }).waitFor()
  await panel.getByText('续费提醒验收服务', { exact: true }).waitFor()
  await page.getByRole('button', { name: '通知', exact: true }).click()
  const popover = page.locator('.notification-popover')
  await popover.getByText('续费提醒验收服务', { exact: true }).waitFor()
  await popover.getByRole('button', { name: '标为已读', exact: true }).click()
  await popover.getByText('暂无未读续费提醒。', { exact: true }).waitFor()
  await page.keyboard.press('Escape')
  await page.reload()
  await panel.getByText('已读', { exact: true }).waitFor()
  checks.push('bell shows actual reminder and acknowledgment survives browser reload')
  await panel.getByLabel('续费提醒提前天数', { exact: true }).fill('7')
  await panel.getByRole('button', { name: '保存提醒设置', exact: true }).click()
  await page.reload()
  assert.equal(await panel.getByLabel('续费提醒提前天数', { exact: true }).inputValue(), '7')
  checks.push('seven-day preference persists through HTTP and reload')
  await panel.getByLabel('开启站内续费提醒').uncheck()
  await panel.getByRole('button', { name: '保存提醒设置', exact: true }).click()
  await panel.getByText('已撤回', { exact: true }).waitFor()
  let v = await api('/api/v1/notifications/renewals')
  assert.equal(v.settings.enabled, false)
  assert.equal(v.unread_count, 0)
  checks.push('disabling reminders withdraws the existing notice')
  await panel.getByLabel('开启站内续费提醒').check()
  await panel.getByRole('button', { name: '保存提醒设置', exact: true }).click()
  await panel.getByText('已读', { exact: true }).waitFor()
  await operation('cancel_subscription', { subscription_id: subId, expected_version: 1 })
  v = await api('/api/v1/notifications/renewals')
  // Settings update reconciles immediately; also covered by the periodic worker test.
  const response = await fetch(base + '/api/v1/notifications/renewals/settings', {
    method: 'PUT', headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + access },
    body: JSON.stringify({ enabled: true, lead_days: 7, expected_version: v.settings.version })
  })
  assert.equal(response.status, 200)
  await page.reload()
  await panel.getByText('已撤回 · 订阅已取消或变更', { exact: true }).waitFor()
  checks.push('gateway-authorized simulated cancellation withdraws the reminder on reconciliation')
  const balance = await api('/api/v1/accounts/' + accountId + '/balance')
  assert.equal(balance.balance_minor, 0)
  assert.equal(balance.ledger_balance_minor, 0)
  assert.equal((await api('/api/v1/accounts/' + accountId + '/ledger')).length, 0)
  checks.push('reminders and cancellation do not credit future savings or change cash and ledger')
  await page.screenshot({ path: resolve(out, 'desktop.png'), fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.screenshot({ path: resolve(out, 'mobile.png'), fullPage: true })
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
  assert.deepEqual(errors, [])
  checks.push('mobile layout has no overflow or uncaught browser errors')
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
if (failure) throw new Error('Renewal live acceptance failed: ' + failure)
console.log(
  checks.length + ' live renewal acceptance groups passed; no real SMS or banking services called',
)
