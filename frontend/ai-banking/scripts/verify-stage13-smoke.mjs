// Real deployed browser, no mocked responses. The provider is explicitly synthetic.
import assert from 'node:assert/strict'
import { readFile, writeFile } from 'node:fs/promises'
import { chromium } from '@playwright/test'
const base = 'http://127.0.0.1:29978'
const dir = process.env.STAGE13_EVIDENCE
if (!dir) throw new Error('explicit Stage13 evidence directory required')
const state = JSON.parse(await readFile(dir + '/private/browser-state.json', 'utf8'))
const evidence = JSON.parse(await readFile(dir + '/worker-success.json', 'utf8'))
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
  const before = requests.length
  await task.click()
  const response = await (await responsePromise).json()
  assert.equal(response.data.status, 'succeeded')
  const expected = evidence.result.outputs.find(x => x.result?.smoke_only === true)
  const output = response.data.result.outputs.find(x => x.workflow_execute_id === expected.workflow_execute_id)
  assert.match(output.workflow_execute_id, /^direct_provider:synthetic-stage13-/)
  assert.equal(output.result.model_response, 'BANKING_SMOKE_OK')
  assert.equal(output.result.smoke_only, true)
  assert.equal(output.financial_execution_enabled, false)
  const card = page.locator('.business-result').filter({ hasText: 'model_smoke' })
  await card.waitFor()
  await card.getByText('查看业务详情与完整返回', { exact: true }).click()
  await card.getByText(/BANKING_SMOKE_OK/).waitFor()
  assert.match(await card.innerText(), /synthetic-stage13/)
  assert.deepEqual(errors, [])
  assert.equal(requests.slice(before).filter(r => r.method === 'POST' && /\/(run|resume|authorize|execute|agent-tasks)$/.test(r.path)).length, 0)
  await page.screenshot({ path: dir + '/browser-smoke.png', fullPage: true })
  await writeFile(dir + '/browser-results.json', JSON.stringify({ status: 'PASS_SYNTHETIC_FIXED_SMOKE_ONLY', task: evidence.task_id, actual_browser: true, model_execution_id: output.workflow_execute_id, response: output.result, requests, runtime_errors: errors, replay_or_execution_requests: 0, real_supplier_requests: 0 }, null, 2))
  console.log('PASS actual browser: persisted fixed smoke, explicit synthetic identity, no task replay or financial authorization')
} finally { await browser.close() }
