// Explicit browser contract fixtures only; no SMS, bank, or model is called.
import assert from 'node:assert/strict'
import { chromium } from '@playwright/test'
import { mkdir, writeFile } from 'node:fs/promises'
const browser = await chromium.launch({
  headless: true,
  executablePath:
    process.env.BANKING_TEST_BROWSER ||
    'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',
})
const origin = process.env.BANKING_UI_ORIGIN || 'http://127.0.0.1:5178'
const checks = []
const errors = []
try {
  const page = await browser.newPage()
  page.on('pageerror', (error) => errors.push(error.message))
  await page.clock.install()
  let signedIn = false,
    verified = false,
    challenges = 0,
    resetCalls = 0,
    logins = 0
  const user = { id: '42', display_name: '开发验证契约用户' }
  const session = () => ({
    access_token: 'fixture-sms-access-token',
    expires_in: 900,
    user,
    phone_masked: '138****8000',
    phone_verified: verified,
    phone_verification_method: verified ? 'development' : '',
  })
  await page.route(/\/(?:product\/)?api\/v[12]\//, async (route) => {
    const request = route.request(),
      path = new URL(request.url()).pathname,
      body = request.postDataJSON()
    let data = []
    if (path === '/api/v1/auth/capabilities')
      data = {
        sms_enabled: true,
        sms_mode: 'development',
        registration_verification_required: true,
        code_expires_in: 300,
        resend_after: 60,
      }
    else if (path === '/api/v1/auth/refresh') {
      if (!signedIn)
        return route.fulfill({
          status: 401,
          json: { code: 'AUTH_INVALID_CREDENTIALS', message: '未登录' },
        })
      data = session()
    } else if (path.startsWith('/api/v1/auth/sms/')) {
      challenges++
      if (path.endsWith('phone-verification')) assert.deepEqual(body, {})
      data = { challenge_id: 'fixture-challenge-' + challenges, expires_in: 300, retry_after: 60 }
    } else if (path === '/api/v1/auth/register') {
      assert.equal(body.challenge_id, 'fixture-challenge-2')
      assert.equal(body.code, '123456')
      signedIn = true
      verified = true
      data = session()
    } else if (path === '/api/v1/auth/login') {
      logins++
      signedIn = true
      data = session()
    } else if (path === '/api/v1/auth/logout') {
      signedIn = false
      data = null
    } else if (path === '/api/v1/auth/password/reset') {
      assert.equal(body.code, '123456')
      assert.ok(body.challenge_id)
      assert.equal(body.new_password, 'new fixture password')
      resetCalls++
      signedIn = false
      verified = false
      data = null
    } else if (path === '/api/v1/auth/phone/verify') {
      assert.equal(body.current_password, 'new fixture password')
      assert.equal(body.code, '123456')
      assert.equal(request.headers().authorization, 'Bearer fixture-sms-access-token')
      verified = true
      data = null
    } else if (path === '/api/v1/users/me') data = user
    else if (path === '/product/api/v1/profile')
      data = { user_id: '42', safety_reserve_minor: 0, revision: 1, timezone: 'Asia/Shanghai' }
    else if (path === '/api/v1/subscriptions') data = { subscriptions: [] }
    await route.fulfill({ json: { code: 'OK', data } })
  })
  await page.goto(origin + '/security')
  await page.getByRole('button', { name: '登录 / 注册', exact: true }).click()
  await page.getByRole('button', { name: '立即注册', exact: true }).click()
  await page
    .getByText('开发短信不会发送到手机，请使用本机开发短信收件箱读取验证码。', { exact: true })
    .waitFor()
  await mkdir('../../output/sms-auth', { recursive: true })
  await page.screenshot({ path: '../../output/sms-auth/frontend-registration-desktop.png' })
  await page.setViewportSize({ width: 390, height: 844 })
  assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1))
  await page.screenshot({ path: '../../output/sms-auth/frontend-registration-mobile.png' })
  await page.setViewportSize({ width: 1280, height: 720 })
  await page.getByLabel('手机号', { exact: true }).fill('13800138000')
  await page.getByLabel('密码', { exact: true }).fill('fixture password')
  await page.getByLabel('确认密码', { exact: true }).fill('fixture password')
  await page.getByRole('button', { name: '发送验证码', exact: true }).click()
  await page.getByRole('button', { name: /60 秒后重发/ }).waitFor()
  await page.getByLabel('验证码', { exact: true }).fill('123456')
  await page.getByLabel('手机号', { exact: true }).fill('13900139000')
  assert.equal(await page.getByLabel('验证码', { exact: true }).inputValue(), '')
  assert.ok(await page.getByRole('button', { name: '注册并登录', exact: true }).isDisabled())
  await page.getByRole('button', { name: '发送验证码', exact: true }).click()
  await page.getByRole('button', { name: /60 秒后重发/ }).waitFor()
  await page.clock.runFor(60000)
  assert.ok(await page.getByRole('button', { name: '发送验证码', exact: true }).isEnabled())
  await page.getByLabel('验证码', { exact: true }).fill('123456')
  await page.getByRole('button', { name: '注册并登录', exact: true }).click()
  await page.getByText('已登录', { exact: true }).waitFor()
  await page.getByRole('button', { name: '账户资料', exact: true }).click()
  await page.getByText('开发验证（非真实手机号认证）', { exact: true }).waitFor()
  checks.push(
    'registration challenge, 60-second cooldown, phone-change invalidation, and explicit development status',
  )
  await page.getByRole('dialog').getByRole('button', { name: '退出登录', exact: true }).click()
  await page.getByRole('button', { name: '登录 / 注册', exact: true }).click()
  await page.getByRole('button', { name: '忘记密码', exact: true }).click()
  await page.getByLabel('手机号', { exact: true }).fill('13900139000')
  await page.getByRole('button', { name: '发送验证码', exact: true }).click()
  await page.getByLabel('验证码', { exact: true }).fill('123456')
  await page.getByLabel('新密码', { exact: true }).fill('new fixture password')
  await page.getByLabel('确认新密码', { exact: true }).fill('new fixture password')
  await page.getByRole('button', { name: '重置密码', exact: true }).click()
  await page.getByRole('heading', { name: '登录轻融金', exact: true }).waitFor()
  assert.equal(resetCalls, 1)
  assert.equal(logins, 0)
  checks.push(
    'forgot-password challenge resets password and returns to login without automatic authentication',
  )
  await page.getByLabel('手机号', { exact: true }).fill('13900139000')
  await page.getByLabel('密码', { exact: true }).fill('new fixture password')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await page.getByText('已登录', { exact: true }).waitFor()
  await page.getByRole('button', { name: '账户资料', exact: true }).click()
  await page.getByText('未验证', { exact: true }).waitFor()
  await page.getByLabel('当前密码', { exact: true }).fill('new fixture password')
  await page.getByRole('button', { name: '发送验证码', exact: true }).click()
  await page.getByLabel('验证码', { exact: true }).fill('123456')
  await page.getByRole('button', { name: '确认验证手机号', exact: true }).click()
  await page.getByText('开发验证（非真实手机号认证）', { exact: true }).waitFor()
  checks.push(
    'bound-phone verification requires password and Bearer, then refreshes the same owner status',
  )
  const local = await page.evaluate(() => JSON.stringify({ ...localStorage }))
  assert.ok(!/123456|fixture password|fixture-sms-access-token|13900139000/.test(local))
  checks.push('verification codes, passwords, phone and access tokens are absent from localStorage')
  await page.close()
  const disabled = await browser.newPage()
  await disabled.route(/\/(?:product\/)?api\/v[12]\//, (route) => {
    const path = new URL(route.request().url()).pathname
    return route.fulfill(
      path.endsWith('/capabilities')
        ? {
            json: {
              code: 'OK',
              data: {
                sms_enabled: false,
                sms_mode: 'disabled',
                registration_verification_required: false,
                code_expires_in: 300,
                resend_after: 60,
              },
            },
          }
        : { status: 401, json: { code: 'AUTH_INVALID_CREDENTIALS', message: '未登录' } },
    )
  })
  await disabled.goto(origin + '/security')
  await disabled.getByRole('button', { name: '登录 / 注册', exact: true }).click()
  assert.ok(await disabled.getByRole('button', { name: '忘记密码', exact: true }).isDisabled())
  await disabled.getByText('短信验证未启用，暂不能找回密码。', { exact: true }).waitFor()
  await disabled.getByRole('button', { name: '立即注册', exact: true }).click()
  assert.equal(await disabled.getByRole('button', { name: '发送验证码', exact: true }).count(), 0)
  checks.push(
    'disabled SMS retains registration and clearly disables password recovery without fake code controls',
  )
  assert.deepEqual(errors, [])
  await mkdir('../../output/sms-auth', { recursive: true })
  await writeFile(
    '../../output/sms-auth/frontend-browser-contract.json',
    JSON.stringify({ passed: true, fixture_only: true, checks, runtime_errors: errors }, null, 2),
  )
  console.log(JSON.stringify({ passed: true, fixture_only: true, checks }))
} finally {
  await browser.close()
}
