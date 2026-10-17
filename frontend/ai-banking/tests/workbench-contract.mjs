// Browser contract fixtures only. Every API request is intercepted; no bank/model calls.
import { chromium } from '@playwright/test'
import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'

const browser = await chromium.launch({
  headless: true,
  executablePath:
    process.env.BANKING_TEST_BROWSER ||
    'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',
})
const origin = process.env.BANKING_UI_ORIGIN || 'http://127.0.0.1:5178'
const context = await browser.newContext({ viewport: { width: 1440, height: 1050 } })
const page = await context.newPage(),
  errors = [],
  calls = [],
  checks = {}
page.on('pageerror', (error) => errors.push(error.message))
const account = (id) => ({
  id,
  user_id: '42',
  display_name: id === '101' ? '契约付款账户' : '契约收款账户',
  account_type: 'checking',
  status: 'active',
  currency: 'CNY',
  balance_minor: id === '101' ? 500000 : 0,
  version: 1,
  is_simulated: true,
  masked_account_number: 'SIM-****',
})
const rootId = 'lg-11111111-1111-4111-8111-111111111111',
  operationId = '22222222-2222-4222-8222-222222222222'
let current,
  submitted,
  mode = 'normal'
const challenge = () => ({
  node_key: 'prepare_transfer',
  digest: 'a'.repeat(64),
  nonce: 'fixture-nonce-32-characters-00000',
  status: 'pending',
  confirmation_scope: 'PREPARE_ONLY',
  expires_at:
    mode === 'expired' ? '2000-01-01T00:00:00Z' : new Date(Date.now() + 600000).toISOString(),
  parameters_view: submitted.tasks[0],
})
function task(status, version, result = null, confirmation = null, error_code = '') {
  return {
    task_id: rootId,
    owner_engine: 'langgraph',
    status,
    version,
    request: submitted,
    result,
    confirmation,
    error_code,
    financial_execution_enabled: false,
    created_at: '2026-10-14T06:00:00Z',
    updated_at: '2026-10-14T06:00:01Z',
  }
}
await page.route(/\/(?:product\/)?api\/v[12]\//, async (route) => {
  const request = route.request(),
    url = new URL(request.url()),
    path = url.pathname,
    body = request.postDataJSON()
  calls.push({ path, method: request.method(), body, url: request.url() })
  assert(!url.search.includes('fixture-user-token'))
  let data = [],
    code = 'OK',
    status = 200,
    message = 'success'
  if (path === '/api/v1/auth/refresh') return route.fulfill({ status: 401, json: { code: 'AUTH_INVALID_CREDENTIALS', message: '测试尚未登录' } })
  if (path === '/api/v1/auth/capabilities') data = { sms_enabled: false, sms_mode: 'disabled', registration_verification_required: false, code_expires_in: 300, resend_after: 60 }
  else if (path === '/api/v1/auth/login') data = { access_token: 'fixture-user-token', expires_in: 900, user: { id: '42', display_name: '工作台契约用户' }, phone_masked: '138****8000', phone_verified: false, phone_verification_method: '' }
  else if (path === '/api/v1/users/me') data = { id: '42', display_name: '工作台契约用户' }
  else if (path === '/api/v1/accounts') data = [account('101'), account('102')]
  else if (path === '/api/v1/notifications/renewals') data = {
    settings: { enabled: true, lead_days: 3, version: 1 },
    items: [], last_checked_at: 0, capacity_limited: false, unread_count: 0,
  }
  else if (path === '/product/api/v1/agent-tasks' && request.method() === 'POST') {
    submitted = body
    current = task('queued', 1)
    data = current
    status = 202
  } else if (path.endsWith('/run')) {
    assert.equal(body.expected_version, 1)
    if (mode === 'unknown') {
      current = task('interrupted', 3, null, null, 'TOOL_OUTCOME_UNKNOWN')
      code = 'DEPENDENCY_UNAVAILABLE'
      message = '服务响应未确认，请查询任务状态'
      status = 502
    } else current = task('waiting_confirmation', 3, null, challenge())
    data = current
  } else if (path.endsWith('/resume')) {
    assert.deepEqual(Object.keys(body).sort(), [
      'decision',
      'digest',
      'expected_version',
      'node_key',
      'nonce',
    ])
    assert.equal(body.node_key, 'prepare_transfer')
    assert.equal(body.digest, 'a'.repeat(64))
    assert.equal(body.expected_version, 3)
    assert.equal(body.nonce, 'fixture-nonce-32-characters-00000')
    current =
      body.decision === 'reject'
        ? task('cancelled', 6)
        : task('succeeded', 6, {
            outputs: [
              {
                task_id: rootId + '.prepare_transfer',
                agent_type: 'supervisor',
                skill_id: 'operation_preparation',
                skill_version: 1,
                status: 'succeeded',
                result: {
                  id: operationId,
                  user_id: '42',
                  kind: 'transfer',
                  amount_minor: 29,
                  source_account_id: '101',
                  target_account_id: '102',
                  status: 'pending_authorization',
                },
                evidence: [],
                financial_execution_enabled: false,
              },
            ],
          })
    data = current
  } else if (path.endsWith('/events/stream')) {
    await route.fulfill({
      status: 200,
      contentType: 'text/event-stream',
      body: ': fixture-only heartbeat\n\n',
    })
    return
  } else if (path.endsWith('/events')) data = []
  else if (path === '/product/api/v1/agent-tasks/' + rootId) data = current
  else if (path === '/api/v1/execution/operations/' + operationId)
    data = {
      operation: {
        id: operationId,
        kind: 'transfer',
        status: 'pending_authorization',
        amount_minor: 29,
        source_account_id: '101',
        target_account_id: '102',
        resource_id: '0',
        parameters: submitted.tasks[0].parameters.parameters,
        snapshot: {},
        risk_level: 'low',
        expires_at: mode === 'financial_expired' ? Date.now()-1000 : Date.now() + 600000,
        intent_digest: 'b'.repeat(64),
        confirmation_nonce: 'fixture-native-authorization-nonce',
        version: 1,
      },
      risk_events: [],
    }
  else if (/\/(authorize|execute)$/.test(path))
    assert.fail('A task preparation test attempted financial authorization or execution')
  await route.fulfill({
    status,
    contentType: 'application/json',
    body: JSON.stringify({
      code,
      message,
      request_id: 'fixture-request',
      trace_id: 'fixture-trace',
      data,
    }),
  })
})
try {
  await page.goto(origin + '/agents')
  await page.getByRole('button', { name: '登录 / 注册', exact: true }).click()
  await page.getByLabel('手机号', { exact: true }).fill('13800138000')
  await page.getByLabel('密码', { exact: true }).fill('fixture-workbench-password')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByRole('dialog').waitFor({ state: 'hidden' })
  async function prepare() {
    await page.getByRole('button', { name: '准备模拟转账', exact: true }).click()
    await page.getByLabel('付款账户', { exact: true }).selectOption('101', { timeout: 5000 })
    await page.getByLabel('收款账户', { exact: true }).selectOption('102', { timeout: 5000 })
    await page.getByLabel('转账金额（元）', { exact: true }).fill('0.29')
    await page.getByRole('button', { name: '创建待确认准备任务', exact: true }).click()
  }
  await prepare()
  await page.getByRole('heading', { name: '请确认准备范围', exact: true }).waitFor()
  assert.equal(submitted.tasks[0].parameters.parameters.amount_minor, 29)
  assert.equal(submitted.tasks[0].execution_mode, 'intent')
  assert.equal(
    await page.getByRole('button', { name: '确认准备意向', exact: true }).isDisabled(),
    true,
  )
  await page.getByRole('checkbox', { name: '我已核对准备范围，理解金融执行仍需单独授权' }).check()
  await page.getByRole('button', { name: '确认准备意向', exact: true }).click()
  await page.getByRole('button', { name: '核对金融意向', exact: true }).waitFor()
  assert.equal(
    calls.filter((c) => c.path === '/api/v1/execution/operations/' + operationId).length,
    0,
  )
  await page.getByRole('button', { name: '核对金融意向', exact: true }).click()
  await page.getByRole('dialog').waitFor()
  assert.equal(
    await page
      .getByRole('dialog')
      .getByRole('button', { name: '授权并执行', exact: true })
      .isDisabled(),
    true,
  )
  await page.getByRole('button', { name: '关闭', exact: true }).click()
  checks.bound_prepare_then_separate_financial_dialog = true
  mode='financial_expired'
  await prepare()
  await page.getByRole('checkbox', { name: '我已核对准备范围，理解金融执行仍需单独授权' }).check()
  await page.getByRole('button', { name: '确认准备意向', exact: true }).click()
  await page.getByRole('button', { name: '核对金融意向', exact: true }).click()
  await page.getByText('该金融操作意向已过期，请关闭后重新准备。',{exact:true}).waitFor()
  assert(await page.getByRole('dialog').getByRole('button',{name:'授权并执行',exact:true}).isDisabled())
  assert(await page.getByRole('dialog').getByRole('button',{name:'拒绝此操作',exact:true}).isDisabled())
  await page.getByRole('button', { name: '关闭', exact: true }).click()
  checks.expired_financial_authorization_disabled=true
  mode='normal'
  await prepare()
  await page.getByRole('heading', { name: '请确认准备范围', exact: true }).waitFor()
  await page.getByRole('button', { name: '拒绝并结束任务', exact: true }).click()
  await page.getByText('已取消', { exact: true }).first().waitFor()
  assert.equal(current.status, 'cancelled')
  checks.reject_without_intent = true
  mode = 'expired'
  await prepare()
  await page.getByText('该确认已失效或不完整，请重新查询任务状态。', { exact: true }).waitFor()
  assert(await page.getByRole('button', { name: '确认准备意向', exact: true }).isDisabled())
  assert(await page.getByRole('button', { name: '拒绝并结束任务', exact: true }).isDisabled())
  checks.expired_confirmation_disabled = true
  mode = 'unknown'
  await prepare()
  await page.getByText('TOOL_OUTCOME_UNKNOWN', { exact: true }).waitFor()
  await page.getByRole('button', { name: '恢复任务', exact: true }).waitFor()
  assert.equal(await page.getByRole('button', { name: '核对金融意向', exact: true }).count(), 0)
  checks.unknown_outcome_not_success = true
  assert.equal(calls.filter((c) => /\/(authorize|execute)$/.test(c.path)).length, 0)
  assert.deepEqual(errors, [])
  checks.no_financial_execution = true
  assert(calls.some((call) => call.path === '/api/v1/notifications/renewals'))
  checks.renewal_notification_contract = true
  checks.no_browser_runtime_errors = true
  await mkdir('test-results', { recursive: true })
  await writeFile(
    'test-results/p6-workbench-contract.json',
    JSON.stringify(
      { passed: true, fixture_only: true, real_bank_or_model_calls: false, checks },
      null,
      2,
    ),
  )
  console.log(JSON.stringify({ passed: true, fixture_only: true, checks }))
} catch (error) {
  console.error(
    JSON.stringify({
      fixture_only: true,
      pageErrors: errors,
      visible_fixture_text: (await page.locator('body').innerText()).slice(-5000),
    }),
  )
  throw error
} finally {
  await browser.close()
}
