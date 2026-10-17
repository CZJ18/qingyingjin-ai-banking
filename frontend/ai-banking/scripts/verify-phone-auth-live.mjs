import assert from 'node:assert/strict'
import { randomBytes, randomUUID } from 'node:crypto'
import { mkdir, writeFile } from 'node:fs/promises'
import { chromium } from '@playwright/test'

// Real UI and APIs only. Credentials remain in memory and never enter receipts.
const base = process.env.BANKING_AUTH_URL || 'http://127.0.0.1:8088'
assert.ok(['127.0.0.1', 'localhost'].includes(new URL(base).hostname))
const out = 'test-results/phone-auth'
await mkdir(out, { recursive: true })
const phone = () => '189' + String(randomBytes(4).readUInt32BE() % 100000000).padStart(8, '0')
const number = phone(), password = randomBytes(24).toString('base64url')
const checks = [], errors = []
const pass = (name) => { checks.push(name); console.log('PASS: ' + name) }
const browser = await chromium.launch({ headless: true, executablePath: process.env.BANKING_TEST_BROWSER || 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe' })
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
const page = await context.newPage()
page.setDefaultTimeout(20000)
page.on('pageerror', (e) => errors.push(e.message))
async function api(path, token, body, method = body === undefined ? 'GET' : 'POST') {
  const r = await fetch(base + path, { method, headers: { Authorization: 'Bearer ' + token, 'Content-Type': 'application/json', 'Idempotency-Key': randomUUID() }, body: body === undefined ? undefined : JSON.stringify(body) })
  return { status: r.status, data: await r.json() }
}
let failure = ''
try {
  await page.goto(base + '/security')
  await page.getByRole('button', { name: '登录 / 注册', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('heading', { name: '登录轻融金' }).waitFor()
  assert.equal(await dialog.locator('input[type=password]').count(), 1)
  assert.equal(await page.getByLabel('用户访问令牌').count(), 0)
  await page.screenshot({ path: out + '/login-desktop.png', fullPage: true })
  await dialog.getByRole('button', { name: '立即注册', exact: true }).click()
  await dialog.getByLabel('手机号', { exact: true }).fill(number)
  await dialog.getByLabel('昵称（选填）', { exact: true }).fill('手机号登录验收')
  await dialog.getByLabel('密码', { exact: true }).fill(password)
  await dialog.getByLabel('确认密码', { exact: true }).fill(password)
  const registered = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/v1/auth/register' && r.request().method() === 'POST')
  await dialog.getByRole('button', { name: '注册并登录', exact: true }).click()
  const registerResponse = await registered
  assert.equal(registerResponse.status(), 201)
  const session = (await registerResponse.json()).data
  assert.ok(session.user.id && session.access_token && !session.refresh_token && !session.password_hash)
  await page.getByText('已登录', { exact: true }).waitFor()
  pass('Real phone/password registration and identity verification; no JWT input')

  const cookies = await context.cookies()
  const cookie = cookies.find((c) => c.name === 'qingrongjin_refresh')
  assert.ok(cookie?.httpOnly && cookie.path === '/api/v1/auth' && cookie.sameSite === 'Strict')
  assert.equal(await page.evaluate(() => document.cookie.includes('qingrongjin_refresh')), false)
  const local = await page.evaluate(() => JSON.stringify({ ...localStorage }))
  for (const secret of [number, password, session.access_token, cookie.value]) assert.ok(!local.includes(secret))
  pass('HttpOnly path-restricted cookie; no credentials or tokens in localStorage')

  const me = await api('/api/v1/users/me', session.access_token)
  const product = await api('/product/api/v1/me', session.access_token)
  assert.equal(me.status, 200)
  assert.equal(me.data.data.id, session.user.id)
  assert.equal(product.status, 200)
  assert.equal(String(product.data.data.user_id), session.user.id)
  assert.deepEqual((await api('/api/v1/accounts', session.access_token)).data.data, [])
  pass('Go and Agent service share the real new user; registration credits no money')

  await page.locator('details.account-administration > summary').click()
  await page.getByRole('button', { name: '创建模拟账户', exact: true }).click()
  await page.getByLabel('账户名称', { exact: true }).fill('手机号验收零余额账户')
  await page.getByLabel('模拟期初余额 / 元', { exact: true }).fill('0')
  const preparing = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/v1/execution/operations' && r.request().method() === 'POST')
  await page.getByRole('button', { name: '创建开户意向', exact: true }).click()
  const op = (await (await preparing).json()).data.operation
  await dialog.getByRole('checkbox').waitFor()
  assert.ok(await dialog.getByRole('button', { name: '授权并执行', exact: true }).isDisabled())
  assert.equal(op.status, 'pending_authorization')
  assert.deepEqual((await api('/api/v1/accounts', session.access_token)).data.data, [])
  await dialog.getByRole('checkbox').check()
  await dialog.getByRole('button', { name: '授权并执行', exact: true }).click()
  await dialog.locator('.status.succeeded').waitFor()
  const accounts = (await api('/api/v1/accounts', session.access_token)).data.data
  assert.equal(accounts.length, 1)
  assert.equal(String(accounts[0].balance_minor), '0')
  await dialog.getByRole('button', { name: '关闭', exact: true }).last().click()
  pass('Financial action remains intent → independent user confirmation → gateway execution')

  await page.reload()
  await page.getByText('已登录', { exact: true }).waitFor()
  await page.locator('details.account-administration > summary').click()
  await page.getByRole('cell', { name: '手机号验收零余额账户', exact: true }).waitFor()
  await page.getByRole('button', { name: '账户资料', exact: true }).click()
  await dialog.getByText(session.phone_masked, { exact: true }).waitFor()
  await page.screenshot({ path: out + '/profile-desktop.png', fullPage: true })
  await dialog.getByRole('button', { name: '关闭', exact: true }).last().click()
  pass('Reload restores the persistent session, masked profile and original account')

  await page.getByRole('button', { name: '退出登录', exact: true }).click()
  await page.getByRole('button', { name: '登录 / 注册', exact: true }).waitFor()
  assert.equal((await api('/api/v1/users/me', session.access_token)).status, 401)
  assert.ok(!(await context.cookies()).some((c) => c.name === 'qingrongjin_refresh'))
  await page.reload()
  await page.getByRole('button', { name: '登录 / 注册', exact: true }).click()
  await dialog.getByLabel('手机号', { exact: true }).fill(number)
  await dialog.getByLabel('密码', { exact: true }).fill('incorrect-password')
  await dialog.getByRole('button', { name: '登录', exact: true }).click()
  await dialog.getByRole('alert').filter({ hasText: '手机号或密码错误' }).waitFor()
  await dialog.getByLabel('密码', { exact: true }).fill(password)
  const loggingIn = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/v1/auth/login' && r.request().method() === 'POST')
  await dialog.getByRole('button', { name: '登录', exact: true }).click()
  const login = (await (await loggingIn).json()).data
  await page.getByText('已登录', { exact: true }).waitFor()
  assert.equal(login.user.id, session.user.id)
  assert.equal((await api('/api/v1/accounts/' + accounts[0].id, login.access_token)).status, 200)
  pass('Logout revokes access immediately; wrong password fails; re-login preserves accounts')

  const foreignContext = await browser.newContext()
  const foreign = await foreignContext.request.post(base + '/api/v1/auth/register', {
    headers: { Origin: base }, data: { phone: phone(), password },
  })
  assert.equal(foreign.status(), 201)
  const other = (await foreign.json()).data
  assert.ok([403, 404].includes((await api('/api/v1/accounts/' + accounts[0].id, other.access_token)).status))
  await foreignContext.request.post(base + '/api/v1/auth/logout', { headers: { Origin: base }, data: {} })
  await foreignContext.close()
  await page.getByRole('button', { name: '退出登录', exact: true }).click()
  await page.getByRole('button', { name: '登录 / 注册', exact: true }).waitFor()
  assert.equal((await api('/api/v1/users/me', other.access_token)).status, 401)
  assert.equal((await api('/api/v1/users/me', login.access_token)).status, 401)
  pass('A separate registered identity cannot read the first user’s account')

  await page.setViewportSize({ width: 390, height: 844 })
  await page.getByRole('button', { name: '登录 / 注册', exact: true }).click()
  await dialog.getByRole('heading', { name: '登录轻融金' }).waitFor()
  await dialog.getByLabel('手机号', { exact: true }).fill('')
  await page.screenshot({ path: out + '/login-mobile.png', fullPage: true })
  const dimensions = await page.evaluate(() => ({ viewport: innerWidth, page: document.documentElement.scrollWidth }))
  assert.ok(dimensions.page <= dimensions.viewport)
  assert.deepEqual(errors, [])
  pass('Mobile phone/password form renders without page errors or horizontal overflow')
} catch (error) {
  // Fixed assertion metadata only: never archive auth payloads or cookie state.
  failure = error?.name === 'TimeoutError' ? 'Browser timeout: ' + checks.length + ' completed checks' : 'Assertion failed after ' + checks.length + ' completed checks'
  console.error(failure)
  await page.locator('input[type="tel"]').fill('').catch(() => {})
  await page.screenshot({ path: out + '/failure.png', fullPage: true }).catch(() => {})
} finally {
  await context.request.post(base + '/api/v1/auth/logout', { headers: { Origin: base }, data: {} }).catch(() => {})
  await writeFile(out + '/live.json', JSON.stringify({ passed: !failure, checks, failure, browser_errors: errors, response_interception: false, fixture_only: false, simulation_only: true }, null, 2) + '\n')
  await browser.close()
}
if (failure) process.exitCode = 1
