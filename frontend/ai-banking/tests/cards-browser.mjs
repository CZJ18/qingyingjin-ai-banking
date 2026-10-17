// Explicit UI contract fixtures. This does not claim real banking execution.
import assert from 'node:assert/strict'
import { chromium } from '@playwright/test'
import { mkdir, writeFile } from 'node:fs/promises'
const browser = await chromium.launch({
  headless: true,
  executablePath:
    process.env.BANKING_TEST_BROWSER ||
    'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',
})
const artifact = 'test-results/cards'
await mkdir(artifact, { recursive: true })
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
const errors = [],
  calls = [],
  changes = []
page.on('pageerror', (error) => errors.push(error.message))
const account = {
  id: '5',
  user_id: '42',
  display_name: '模拟生活账户',
  masked_account_number: '****0005',
  balance_minor: 50000,
  status: 'active',
  currency: 'CNY',
  version: 1,
  is_simulated: true,
}
const initial = {
  id: '94b27a56-5355-400e-8c17-fce6643da4b4',
  account_id: '5',
  issuer: '示例银行',
  name: '日常生活卡',
  last_four: '1234',
  card_type: 'credit',
  currency: 'CNY',
  status: 'active',
  is_default: false,
  credit_limit_minor: 100000,
  credit_used_minor: 0,
  credit_available_minor: 100000,
  is_simulated: true,
  created_at: Date.now(),
  updated_at: Date.now(),
}
let portfolio = [initial],
  operation
try {
  await page.route(/\/(?:product\/)?api\/v[12]\//, async (route) => {
    const req = route.request(),
      path = new URL(req.url()).pathname,
      body = req.postDataJSON()
    calls.push({ path, method: req.method() })
    let data = []
    if (path.startsWith('/api/v1/auth/'))
      data = path.endsWith('/capabilities')
        ? {
            sms_enabled: false,
            sms_mode: 'disabled',
            registration_verification_required: false,
            code_expires_in: 300,
            resend_after: 60,
          }
        : {
            access_token: 'fixture-card-session',
            expires_in: 900,
            user: { id: '42', display_name: '卡片验收用户' },
            phone_masked: '138****0000',
            phone_verified: false,
            phone_verification_method: '',
          }
    else if (path === '/api/v1/users/me') data = { id: '42', display_name: '卡片验收用户' }
    else if (path === '/api/v1/accounts') data = [account]
    else if (path === '/api/v1/cards') {
      assert.equal(req.headers().authorization, 'Bearer fixture-card-session')
      data = portfolio
    } else if (path === '/api/v1/execution/operations' && req.method() === 'POST') {
      assert.equal(body.kind, 'card_manage')
      assert.ok(req.headers()['idempotency-key'])
      changes.push(body.parameters)
      operation = {
        id: '8a71cd17-184c-4b55-a595-b436d9fd9074',
        kind: 'card_manage',
        status: 'pending_authorization',
        parameters: body.parameters,
        snapshot: { cards: portfolio },
        result: {},
        intent_digest: 'a'.repeat(64),
        confirmation_nonce: 'fixture-confirmation-only',
        authorization_id: '',
        version: 1,
        expires_at: Date.now() + 600000,
        amount_minor: 0,
        currency: 'CNY',
        risk_level: 'high',
        user_id: '42',
      }
      data = { operation, authorizations: [], risk_events: [] }
    } else if (path.endsWith('/authorize')) {
      assert.equal(body.decision, 'approve')
      assert.equal(body.confirmation_nonce, 'fixture-confirmation-only')
      operation = {
        ...operation,
        status: 'authorized',
        authorization_id: 'fixture-authorization',
        version: 2,
      }
      data = { operation, authorizations: [], risk_events: [] }
    } else if (path.endsWith('/execute')) {
      assert.equal(body.authorization_id, 'fixture-authorization')
      const p = operation.parameters
      if (p.action === 'set_default')
        portfolio = portfolio.map((c) => ({ ...c, is_default: c.id === p.card_id }))
      if (p.action === 'unbind')
        portfolio = portfolio.map((c) =>
          c.id === p.card_id ? { ...c, status: 'unbound', is_default: false } : c,
        )
      operation = { ...operation, status: 'succeeded', version: 3, result: portfolio }
      data = { operation, authorizations: [], risk_events: [] }
    }
    await route.fulfill({
      status: 200,
      json: {
        code: 'OK',
        message: 'explicit test fixture',
        request_id: 'fixture',
        trace_id: 'fixture',
        data,
      },
    })
  })
  await page.goto(process.env.BANKING_UI_ORIGIN || 'http://127.0.0.1:5178/security')
  await page.getByRole('heading', { name: '我的银行卡', exact: true }).waitFor()
  const manager = page.locator('.card-manager')
  await manager.getByText('日常生活卡', { exact: true }).waitFor()
  assert.equal(await manager.locator('.credit-summary').count(), 1)
  assert.ok((await manager.innerText()).includes('尚未接入信用卡消费与还款'))
  await manager.getByRole('button', { name: '设为默认', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByText('管理模拟银行卡', { exact: true }).waitFor()
  assert.equal(
    await dialog.getByRole('button', { name: '授权并执行', exact: true }).isDisabled(),
    true,
  )
  assert.equal(
    calls.some((c) => c.path.endsWith('/execute')),
    false,
  )
  await dialog.getByRole('checkbox').check()
  await dialog.getByRole('button', { name: '授权并执行', exact: true }).click()
  await dialog.getByRole('button', { name: '关闭', exact: true }).last().click()
  await manager.getByText('默认卡', { exact: true }).waitFor()
  await manager.getByRole('button', { name: '解绑', exact: true }).click()
  await dialog.getByRole('checkbox').check()
  await dialog.getByRole('button', { name: '授权并执行', exact: true }).click()
  await dialog.getByRole('button', { name: '关闭', exact: true }).last().click()
  await manager.getByText('已解绑 · 保留历史', { exact: true }).waitFor()
  await manager.getByRole('button', { name: '绑定模拟卡', exact: true }).click()
  await manager.getByLabel('关联模拟账户', { exact: true }).selectOption('5')
  await manager.getByLabel('卡片名称', { exact: true }).fill('新卡')
  await manager.getByLabel('发行机构（模拟）', { exact: true }).fill('模拟发行机构')
  await manager.getByLabel('模拟尾号（仅四位）', { exact: true }).fill('5678')
  await manager.getByRole('button', { name: '创建绑定意向', exact: true }).click()
  await dialog.getByText('管理模拟银行卡', { exact: true }).waitFor()
  assert.equal(changes.at(-1).last_four, '5678')
  assert.equal(changes.at(-1).credit_limit_minor, 0)
  assert.ok(!('card_number' in changes.at(-1)))
  await dialog.getByRole('button', { name: '关闭', exact: true }).last().click()
  await page.screenshot({ path: artifact + '/desktop.png', fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.screenshot({ path: artifact + '/mobile.png', fullPage: true })
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth))
  assert.deepEqual(errors, [])
  await writeFile(
    artifact + '/acceptance.json',
    JSON.stringify(
      {
        passed: true,
        scope: 'explicit browser fixtures only',
        checks: [
          'card and credit display',
          'no execution before human authorization',
          'default card then unbind history',
          'bind only four-digit metadata',
          'mobile overflow',
        ],
        errors,
      },
      null,
      2,
    ),
  )
  console.log('5 card browser fixture groups passed; no real financial execution claimed')
} finally {
  await browser.close()
}
