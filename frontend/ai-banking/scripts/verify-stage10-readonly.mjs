// Targeted check of the retained business release, no task/financial writes.
import assert from 'node:assert/strict'
import { readFile, writeFile } from 'node:fs/promises'
import { chromium } from '@playwright/test'

const base = 'http://127.0.0.1:29778'
const state = JSON.parse(await readFile(process.env.BANKING_STAGE10_TEST_STATE, 'utf8'))
const browser = await chromium.launch({ headless: true, executablePath: 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe' })
const context = await browser.newContext({ storageState: { cookies: state.cookies, origins: [] }, viewport: { width: 1440, height: 1000 } })
const page = await context.newPage()
const errors = [], requests = [], blocked = []
await context.route('**/*', route => {
  const url = new URL(route.request().url())
  if (url.origin !== base) { blocked.push(url.origin); return route.abort() }
  return route.continue()
})
page.on('pageerror', e => errors.push(e.message))
page.on('response', r => {
  const url = new URL(r.url())
  if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/product/api/')) requests.push({ path: url.pathname, status: r.status() })
})
try {
  const refreshed = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/auth/refresh')
  await page.goto(base + '/assistant')
  const response = await refreshed
  assert.equal(response.status(), 200)
  await page.getByText('已登录', { exact: true }).waitFor()
  await page.getByRole('heading', { name: '智能金融任务工作台', exact: true }).waitFor()
  // Query history from persisted records; selecting/creating a task is unnecessary.
  const history = page.waitForResponse(r => new URL(r.url()).pathname.endsWith('/agent-tasks') && r.status() === 200)
  await page.getByRole('button', { name: /任务历史/ }).click()
  await history
  await page.screenshot({ path: process.env.BANKING_STAGE10_SCREENSHOT, fullPage: true })
  assert.deepEqual(errors, [])
  assert.deepEqual(blocked, [])
  assert(requests.some(r => r.path === '/api/v1/accounts' && r.status === 200))
  const result = { status: 'PASS', url: base, requests, page_errors: errors, external_requests_blocked: blocked,
    scope: 'retained release: session restoration, account query, persistent task history, rendered assistant; no model/task/financial dispatch' }
  await writeFile(process.env.BANKING_STAGE10_BROWSER_RESULT, JSON.stringify(result, null, 2))
  console.log(JSON.stringify(result))
} finally {
  await browser.close()
}
