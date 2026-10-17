import { chromium } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
const artifacts = process.env.BANKING_QA_DIR || 'test-results/visual'
await mkdir(artifacts, { recursive: true })
const browser = await chromium.launch({
  headless: true,
  executablePath: process.env.BANKING_TEST_BROWSER,
})
const page = await browser.newPage({ viewport: { width: 1440, height: 1080 } })
const errors = []
page.on('pageerror', (e) => errors.push(e.message))
// Negative-login fixtures live only in this test; no real account is submitted.
await page.route('**/api/v1/auth/**', route => route.fulfill(new URL(route.request().url()).pathname.endsWith('/capabilities') ? {
  json: { code: 'OK', data: { sms_enabled: false, sms_mode: 'disabled', registration_verification_required: false, code_expires_in: 300, resend_after: 60 } },
} : {
  status: 401,
  json: { code: 'AUTH_INVALID_CREDENTIALS', message: '手机号或密码错误，或登录已失效' },
}))
await page.goto('http://127.0.0.1:5178/')
await page.waitForTimeout(1200)
await page.screenshot({ path: `${artifacts}/desktop.png`, fullPage: true })
for (const route of [
  'assistant',
  'agents',
  'billing',
  'subscriptions',
  'goals',
  'planning',
  'security',
]) {
  await page.goto('http://127.0.0.1:5178/' + route)
  await page.waitForTimeout(250)
  console.log(route, await page.locator('h1').innerText())
  if (route === 'assistant')
    await page.screenshot({ path: `${artifacts}/assistant.png`, fullPage: true })
}
await page.goto('http://127.0.0.1:5178/')
await page.getByRole('button', { name: '登录 / 注册', exact: true }).click()
await page.getByLabel('手机号', { exact: true }).fill('13800138000')
await page.getByLabel('密码', { exact: true }).fill('invalid-fixture-password')
await page.getByRole('button', { name: '登录', exact: true }).click()
await page.getByRole('alert').waitFor({ timeout: 25000 })
console.log('phone login error visible', await page.getByRole('alert').innerText())
await page.getByRole('button', { name: '取消', exact: true }).click()
await page.setViewportSize({ width: 390, height: 844 })
await page.screenshot({ path: `${artifacts}/mobile.png`, fullPage: true })
for (const route of [
  '',
  'assistant',
  'agents',
  'billing',
  'subscriptions',
  'goals',
  'planning',
  'security',
]) {
  await page.goto('http://127.0.0.1:5178/' + route)
  await page.waitForTimeout(200)
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)
  console.log('mobile', route || 'overview', 'overflow', overflow)
  if (overflow) errors.push('Overflow ' + route)
}
await page.getByRole('button', { name: '打开导航' }).click()
await page.getByRole('link', { name: '智能账单分析', exact: true }).click()
await page.waitForURL('**/billing')
console.log('mobile navigation', page.url())
console.log('PAGE ERRORS', JSON.stringify(errors))
await browser.close()
if (errors.length) process.exitCode = 1
