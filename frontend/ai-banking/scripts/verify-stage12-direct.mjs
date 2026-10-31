// Actual retained Vue + new Go/Worker/gateway/SQL. Provider explicitly simulated.
import assert from 'node:assert/strict'
import { readFile, writeFile } from 'node:fs/promises'
import { chromium } from '@playwright/test'
const base = 'http://127.0.0.1:29878'
const state = JSON.parse(await readFile(process.env.STAGE12_STATE, 'utf8'))
const evidence = JSON.parse(await readFile(process.env.STAGE12_TASK, 'utf8'))
const browser = await chromium.launch({ headless: true, executablePath: 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe' })
const context = await browser.newContext({ storageState: { cookies: state.cookies, origins: [] }, viewport: { width: 1440, height: 1000 } })
await context.route('**/*', route => new URL(route.request().url()).origin === base ? route.continue() : route.abort())
const page = await context.newPage(), errors = [], requests = []
page.on('pageerror', e => errors.push(e.message))
page.on('request', r => requests.push({ path: new URL(r.url()).pathname, method: r.method() }))
try {
  const refreshed = page.waitForResponse(r => new URL(r.url()).pathname === '/api/v1/auth/refresh')
  await page.goto(base + '/assistant')
  assert.equal((await refreshed).status(), 200)
  await page.getByText('已登录', { exact: true }).waitFor()
  await page.getByRole('button', { name: /任务历史/ }).click()
  const task = page.locator('#task-history > button').filter({ hasText: evidence.task_id })
  await task.waitFor()
  const responsePromise = page.waitForResponse(r => new URL(r.url()).pathname.endsWith('/agent-tasks/' + evidence.task_id))
  const count = requests.length
  await task.click()
  const response = await (await responsePromise).json()
  assert.equal(response.data.status, 'succeeded')
  const outputs = response.data.result.outputs
  assert(outputs.some(x => x.workflow_execute_id === 'direct_provider:synthetic-stage12-1'))
  const billing = outputs.find(x => x.agent_type === 'billing').result
  assert.equal(billing.total_income, 100000)
  assert.equal(billing.total_expense, 24000)
  await page.locator('.business-result').filter({ hasText: '财务分析摘要' }).waitFor()
  assert.match(await page.locator('.business-result').filter({ hasText: '财务分析摘要' }).innerText(), /240/)
  assert.deepEqual(errors, [])
  assert.equal(requests.slice(count).filter(r => r.method === 'POST' && /\/(run|resume|authorize|execute|agent-tasks)$/.test(r.path)).length, 0)
  await page.screenshot({ path: process.env.STAGE12_SCREENSHOT, fullPage: true })
  await writeFile(process.env.STAGE12_RESULT, JSON.stringify({ status: 'PASS_SYNTHETIC_PROVIDER_ONLY', task: evidence.task_id,
    actual_browser: true, income_minor: billing.total_income, expense_minor: billing.total_expense,
    model_execution_id: 'direct_provider:synthetic-stage12-1', requests, runtime_errors: errors, real_supplier_requests: 0 }, null, 2))
  console.log('PASS Stage12 browser: actual SQL-backed billing result, explicit synthetic direct model identity, no replay or financial execution')
} finally { await browser.close() }
