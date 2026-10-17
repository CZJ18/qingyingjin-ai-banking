// Real local LAN gateway acceptance. No mocked routes, no credential artifacts.
import assert from 'node:assert/strict'
import { randomBytes, randomUUID } from 'node:crypto'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { mkdir, writeFile } from 'node:fs/promises'
import { chromium } from '@playwright/test'

const base = process.env.BANKING_MOBILE_URL
assert.ok(base, 'Set BANKING_MOBILE_URL to the private LAN mobile entry')
const host = new URL(base)
assert.equal(host.protocol, 'http:')
assert.match(host.hostname, /^(192\.168\.|10\.|172\.(1[6-9]|2[0-9]|3[01])\.)/)
const root = fileURLToPath(new URL('../../../', import.meta.url))
const out = resolve(root, 'output/android/acceptance')
await mkdir(out, { recursive: true })
const checks = [], errors = []
let cookie = '', token = '', browser, context
async function api(path, body, status = 200, authenticated = true) {
  const r = await fetch(base + path, { method: body === undefined ? 'GET' : 'POST', headers: {
    Origin: base, 'Content-Type': 'application/json', 'Idempotency-Key': randomUUID(),
    ...(authenticated && token ? { Authorization: `Bearer ${token}` } : {}),
  }, body: body === undefined ? undefined : JSON.stringify(body) })
  const env = await r.json()
  assert.equal(r.status, status, path + ': ' + env.code)
  if (status < 400) assert.equal(env.code, 'OK', path)
  if (path.endsWith('/register')) cookie = r.headers.getSetCookie().find(c => c.startsWith('qingrongjin_refresh=')) || ''
  return env.data
}
try {
  const ready = await api('/readyz')
  assert.equal(ready.dependencies.postgres, 'ready')
  checks.push('LAN gateway reaches the actual PostgreSQL/Redis banking service')
  await api('/api/v1/users/me', undefined, 401, false)
  checks.push('LAN gateway retains authentication; anonymous account access denied')
  const phone = '188' + String(randomBytes(4).readUInt32BE() % 100000000).padStart(8, '0')
  const password = randomBytes(24).toString('base64url')
  const challenge = await api('/api/v1/auth/sms/registration', { phone })
  const { stdout } = await promisify(execFile)(resolve(root, 'output/sms-auth/sms-inbox.exe'), [
    '-env-file', resolve(root, 'deploy/banking-postgres/finance.app.env.local'),
    '-env-file', resolve(root, 'deploy/banking/sms.env.local'),
    '-phone', phone, '-purpose', 'register', '-allow-development-code',
  ], { timeout: 10000, windowsHide: true })
  const registered = await api('/api/v1/auth/register', { phone, password, display_name: '安卓入口验收', challenge_id: challenge.challenge_id, code: JSON.parse(stdout).code }, 201)
  token = registered.access_token
  assert.ok(cookie.includes('HttpOnly') && cookie.includes('SameSite=Strict'))
  assert.equal((await api('/api/v1/users/me')).id, registered.user.id)
  checks.push('actual development SMS registration and authenticated identity through LAN origin')
  const denied = await fetch(base + '/api/v1/auth/refresh', { method: 'POST', headers: { Origin: 'http://evil.invalid', Cookie: cookie.split(';')[0], 'Content-Type': 'application/json' }, body: '{}' })
  assert.equal(denied.status, 403)
  checks.push('cross-origin refresh remains forbidden; no relaxed CORS/auth workaround')
  let prepared = await api('/api/v1/execution/operations', { kind: 'open_account', parameters: { display_name: 'Android模拟验收账户', account_type: 'checking', currency: 'CNY', initial_balance_minor: 12345, confirm_simulated_funds: true } }, 201)
  assert.equal(prepared.operation.status, 'pending_authorization')
  const authorized = await api(`/api/v1/execution/operations/${prepared.operation.id}/authorize`, { decision: 'approve', intent_digest: prepared.operation.intent_digest, confirmation_nonce: prepared.operation.confirmation_nonce })
  const op = authorized.operation
  const executed = await api(`/api/v1/execution/operations/${op.id}/execute`, { authorization_id: op.authorization_id, intent_digest: op.intent_digest, expected_version: op.version })
  assert.equal(executed.operation.status, 'succeeded')
  checks.push('simulated account opening still uses bound intent, human authorization and execution gateway')
  browser = await chromium.launch({ headless: true, executablePath: 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe' })
  context = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })
  const pair = cookie.split(';')[0], at = pair.indexOf('=')
  await context.addCookies([{ name: pair.slice(0, at), value: pair.slice(at + 1), domain: host.hostname, path: '/api/v1/auth', httpOnly: true, secure: false, sameSite: 'Strict' }])
  const page = await context.newPage()
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(base + '/security')
  await page.getByRole('heading', { name: '找人转账', exact: true }).waitFor()
  await page.reload()
  await page.getByRole('heading', { name: '找人转账', exact: true }).waitFor()
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))
  await page.screenshot({ path: resolve(out, 'lan-mobile-browser.png'), fullPage: true })
  assert.deepEqual(errors, [])
  checks.push('390px mobile browser LAN login/refresh and existing transfer UI; no horizontal overflow or JS errors (not Android device acceptance)')
  console.log(JSON.stringify({ passed: checks.length, checks }, null, 2))
} finally {
  await writeFile(resolve(out, 'lan.json'), JSON.stringify({ passed: checks.length, checks, browser_errors: errors, android_device_tested: false }, null, 2))
  if (context) {
    const current = (await context.cookies()).find(c => c.name === 'qingrongjin_refresh')
    if (current) await fetch(base + '/api/v1/auth/logout', { method: 'POST', headers: { Origin: base, Cookie: `${current.name}=${current.value}`, 'Content-Type': 'application/json' }, body: '{}' })
  }
  await browser?.close()
}
