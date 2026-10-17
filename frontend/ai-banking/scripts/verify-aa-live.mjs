// Actual local multi-user browser/API acceptance. No intercepted responses or SQL fixtures.
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
const base = process.env.BANKING_AA_URL || 'http://127.0.0.1:8088'
assert.ok(['127.0.0.1', 'localhost'].includes(new URL(base).hostname))
const out = resolve(root, 'output/aa-bills/live')
await mkdir(out, { recursive: true })
const checks = [], errors = [], users = []
let failure = ''
async function api(user, path, body, status = 200) {
 const r = await fetch(base + path, { method: body === undefined ? 'GET' : 'POST', headers: { 'Content-Type': 'application/json', Origin: base, 'Idempotency-Key': randomUUID(), ...(user?.access ? { Authorization: 'Bearer ' + user.access } : {}) }, body: body === undefined ? undefined : JSON.stringify(body) })
 const env = await r.json()
 if (r.status !== status) throw new Error(path + ' HTTP ' + r.status + ' ' + env.code)
 if (status >= 400) return env
 assert.equal(env.code, 'OK', path)
 if (path === '/api/v1/auth/register') user.cookie = r.headers.getSetCookie().find(c => c.startsWith('qingrongjin_refresh=')) || ''
 return env.data
}
async function operation(user, kind, parameters) {
 let v = await api(user, '/api/v1/execution/operations', { kind, parameters }, 201)
 v = await api(user, `/api/v1/execution/operations/${v.operation.id}/authorize`, { decision: 'approve', intent_digest: v.operation.intent_digest, confirmation_nonce: v.operation.confirmation_nonce })
 v = await api(user, `/api/v1/execution/operations/${v.operation.id}/execute`, { authorization_id: v.operation.authorization_id, intent_digest: v.operation.intent_digest, expected_version: v.operation.version })
 assert.equal(v.operation.status, 'succeeded')
 return v.operation.result
}
const browser = await chromium.launch({ headless: true, executablePath: process.env.BANKING_TEST_BROWSER || 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe' })
async function register(index) {
 const user = {}, phone = '188' + String(randomBytes(4).readUInt32BE() % 100000000).padStart(8, '0'), password = randomBytes(24).toString('base64url')
 const challenge = await api(user, '/api/v1/auth/sms/registration', { phone })
 const inbox = await execute(resolve(root, 'output/sms-auth/sms-inbox.exe'), ['-env-file', resolve(root, 'deploy/banking-postgres/finance.app.env.local'), '-env-file', resolve(root, 'deploy/banking/sms.env.local'), '-phone', phone, '-purpose', 'register', '-allow-development-code'], { timeout: 10000, windowsHide: true })
 const code = JSON.parse(inbox.stdout).code
 assert.match(code, /^[0-9]{6}$/)
 const r = await api(user, '/api/v1/auth/register', { phone, password, display_name: 'AA隔离验收' + index, challenge_id: challenge.challenge_id, code }, 201)
 user.id = r.user.id; user.access = r.access_token
 const a = await operation(user, 'open_account', { display_name: index === 0 ? 'AA收款账户' : 'AA付款账户', account_type: 'checking', currency: 'CNY', initial_balance_minor: index === 0 ? 0 : 20000, confirm_simulated_funds: true })
 user.account = a.id
 user.context = await browser.newContext({ viewport: { width: 1440, height: 1100 } })
 const pair = user.cookie.split(';')[0], at = pair.indexOf('=')
 await user.context.addCookies([{ name: pair.slice(0, at), value: pair.slice(at + 1), domain: new URL(base).hostname, path: '/api/v1/auth', httpOnly: true, secure: false, sameSite: 'Strict' }])
 user.page = await user.context.newPage(); user.page.setDefaultTimeout(30000)
 user.page.on('pageerror', e => errors.push(e.message))
 users.push(user)
 await user.page.goto(base + '/security')
 await user.page.locator('.aa-manager').getByRole('heading', { name: 'AA分摊转账', exact: true }).waitFor()
 return user
}
async function confirm(user) {
 const d = user.page.getByRole('dialog')
 await d.getByText('支付本人AA分摊', { exact: true }).waitFor()
 assert.equal(await d.getByRole('button', { name: '授权并执行', exact: true }).isDisabled(), true)
 await d.getByRole('checkbox').check()
 await d.getByRole('button', { name: '授权并执行', exact: true }).click()
 await d.getByText('成功', { exact: true }).waitFor()
 await d.getByRole('button', { name: '关闭', exact: true }).last().click()
}
try {
 const caps = await api(null, '/api/v1/auth/capabilities')
 assert.equal(caps.sms_mode, 'development')
 for (let i=0; i<4; i++) await register(i)
 checks.push('four independently authenticated development users; funded simulated accounts via authorized gateway')
 const owner = users[0], manager = owner.page.locator('.aa-manager')
 await manager.getByLabel('AA账单名称', { exact: true }).fill('四人聚餐AA验收')
 await manager.getByLabel('AA总金额', { exact: true }).fill('360')
 await manager.getByLabel('AA其他参与人', { exact: true }).fill('小林\n小陈\n小周')
 await manager.getByLabel('我确认已垫付，这里只创建分摊应收').check()
 await manager.getByRole('button', { name: '创建AA账单', exact: false }).click()
 await manager.getByRole('heading', { name: '四人聚餐AA验收', exact: true }).waitFor()
 let bill = (await api(owner, '/api/v1/aa/bills')).find(b => b.title === '四人聚餐AA验收')
 assert.ok(bill); assert.equal(bill.total_minor, 36000)
 assert.deepEqual(bill.participants.map(p => p.amount_minor), [9000, 9000, 9000, 9000])
 assert.equal(bill.outstanding_minor, 27000)
 assert.equal((await api(owner, `/api/v1/accounts/${owner.account}/balance`)).balance_minor, 0)
 checks.push('browser creates a four-person 360 CNY bill; server allocates 90 each and 270 receivable without crediting cash')
 for (let i=1; i<4; i++) {
  const user=users[i], panel=user.page.locator('.aa-manager')
  await panel.getByLabel('AA邀请码', { exact: true }).fill(bill.invitation)
  await panel.getByRole('button', { name: '查看邀请账单', exact: true }).click()
  await panel.getByLabel('AA参与位置', { exact: true }).selectOption(bill.participants[i].id)
  await panel.getByRole('button', { name: '确认加入，暂不付款', exact: true }).click()
  const article=panel.locator(`[data-bill-id="${bill.id}"]`)
  await article.getByRole('button', { name: '确认本人的付款', exact: true }).waitFor()
  assert.equal(await article.getByLabel('分享AA邀请码').count(), 0)
  assert.equal((await api(user, `/api/v1/accounts/${user.account}/balance`)).balance_minor, 20000)
  await article.getByRole('button', { name: '确认本人的付款', exact: true }).click()
  const dialog=user.page.getByRole('dialog')
  await dialog.getByText('支付本人AA分摊', { exact: true }).waitFor()
  assert.equal(await dialog.getByRole('button', { name: '授权并执行', exact: true }).isDisabled(), true)
  assert.equal((await api(user, `/api/v1/accounts/${user.account}/balance`)).balance_minor, 20000)
  await confirm(user)
  await article.getByText('已付款', { exact: true }).first().waitFor()
  assert.equal((await api(user, `/api/v1/accounts/${user.account}/balance`)).balance_minor, 11000)
 }
 checks.push('each participant joins through the actual UI; joining/preparing do not debit; three separate human confirmations settle payments')
 await manager.getByRole('button', { name: '刷新进度', exact: true }).click()
 const settled=manager.locator(`[data-bill-id="${bill.id}"]`)
 await settled.getByText('已结清', { exact: true }).waitFor()
 bill=await api(owner, `/api/v1/aa/bills/${owner.id}/${bill.id}`)
 assert.equal(bill.collected_minor,27000);assert.equal(bill.outstanding_minor,0)
 assert.equal((await api(owner, `/api/v1/accounts/${owner.account}/balance`)).balance_minor,27000)
 const foreign=await api(users[1], '/api/v1/execution/operations', { kind:'aa_payment', parameters:{ owner_id:owner.id,bill_id:bill.id,participant_id:bill.participants[2].id,source_account_id:users[1].account } },409)
 assert.equal(foreign.code,'STATE_CONFLICT') // Settled bills cannot be paid again, regardless of slot.
 checks.push('owner sees settled status and 270 CNY actual received; settled bill rejects further payment intents')
 await manager.getByLabel('AA账单名称', { exact: true }).fill('终止未付AA验收')
 await manager.getByLabel('AA总金额', { exact: true }).fill('100')
 await manager.getByLabel('AA其他参与人', { exact: true }).fill('小林')
 await manager.getByLabel('我确认已垫付，这里只创建分摊应收').check()
 await manager.getByRole('button', { name: '创建AA账单', exact: false }).click()
 await manager.getByRole('heading', { name:'终止未付AA验收',exact:true }).waitFor()
 let pending=(await api(owner,'/api/v1/aa/bills')).find(b=>b.title==='终止未付AA验收')
 await api(users[1],'/api/v1/aa/join',{invitation:pending.invitation,participant_id:pending.participants[1].id})
 let intent=await api(users[1],'/api/v1/execution/operations',{kind:'aa_payment',parameters:{owner_id:owner.id,bill_id:pending.id,participant_id:pending.participants[1].id,source_account_id:users[1].account}},201)
 intent=await api(users[1],`/api/v1/execution/operations/${intent.operation.id}/authorize`,{decision:'approve',intent_digest:intent.operation.intent_digest,confirmation_nonce:intent.operation.confirmation_nonce})
 await manager.getByRole('button',{name:'刷新进度',exact:true}).click()
 // Reload guarantees the cancellation uses the current joined version, not a stale UI snapshot.
 await owner.page.reload()
 const cancelled=manager.locator(`[data-bill-id="${pending.id}"]`)
 await cancelled.getByLabel('确认终止未付分摊，已付款不退款').check()
 await cancelled.getByRole('button',{name:'终止未付分摊',exact:true}).click()
 await cancelled.getByText('已终止',{exact:true}).waitFor()
 intent=await api(users[1],`/api/v1/execution/operations/${intent.operation.id}/execute`,{authorization_id:intent.operation.authorization_id,intent_digest:intent.operation.intent_digest,expected_version:intent.operation.version})
 assert.equal(intent.operation.status,'failed');assert.equal(intent.operation.error_code,'STATE_CONFLICT')
 assert.equal((await api(users[1],`/api/v1/accounts/${users[1].account}/balance`)).balance_minor,11000)
 checks.push('UI cancellation invalidates an already authorized unpaid intent; no debit after cancellation')
 await manager.getByLabel('AA账单名称',{exact:true}).fill('自定义AA验收')
 await manager.getByLabel('AA总金额',{exact:true}).fill('101')
 await manager.getByLabel('AA其他参与人',{exact:true}).fill('项目甲\n项目乙')
 await manager.getByLabel('AA分摊方式',{exact:true}).selectOption('custom')
 await manager.getByLabel('AA本人分摊金额',{exact:true}).fill('1')
 await manager.getByLabel('AA项目甲分摊金额',{exact:true}).fill('33.33')
 await manager.getByLabel('AA项目乙分摊金额',{exact:true}).fill('66.67')
 await manager.getByLabel('我确认已垫付，这里只创建分摊应收').check()
 await manager.getByRole('button',{name:'创建AA账单',exact:false}).click()
 await manager.getByRole('heading',{name:'自定义AA验收',exact:true}).waitFor()
 const custom=(await api(owner,'/api/v1/aa/bills')).find(b=>b.title==='自定义AA验收')
 assert.deepEqual(custom.participants.map(p=>p.amount_minor),[100,3333,6667])
 checks.push('browser custom allocation persists exact cents: 1 + 33.33 + 66.67 = 101 CNY')
 let totalCash=0
 for(const user of users) {const b=await api(user,`/api/v1/accounts/${user.account}/balance`);assert.equal(b.reconciled,true);totalCash+=b.balance_minor}
 assert.equal(totalCash,60000)
 checks.push('all four actual account balances reconcile to ledger and total simulated cash remains 600 CNY')
 await manager.scrollIntoViewIfNeeded()
 await owner.page.screenshot({path:resolve(out,'desktop.png'),fullPage:true,mask:[owner.page.locator('textarea')]})
 await owner.page.setViewportSize({width:390,height:844})
 await owner.page.screenshot({path:resolve(out,'mobile.png'),fullPage:true,mask:[owner.page.locator('textarea')]})
 assert.ok(await owner.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth))
 assert.deepEqual(errors,[])
 checks.push('mobile has no horizontal overflow; no uncaught browser errors; private invitation screenshots masked')
} catch(e) {
 failure=e.message
 for(let i=0;i<users.length;i++) await users[i].page?.screenshot({path:resolve(out,`failure-${i}.png`),fullPage:true,mask:[users[i].page.locator('textarea')]}).catch(()=>{})
} finally {
 for(const user of users) {
  const cookies=await user.context.cookies(base+'/api/v1/auth/refresh').catch(()=>[])
  const refresh=cookies.find(c=>c.name==='qingrongjin_refresh')
  if(refresh) await fetch(base+'/api/v1/auth/logout',{method:'POST',headers:{'Content-Type':'application/json',Origin:base,Cookie:refresh.name+'='+refresh.value},body:'{}'}).catch(()=>{})
 }
 await writeFile(resolve(out,'acceptance.json'),JSON.stringify({passed:!failure,scope:'actual local four-user browser/API; no interception or SQL fixtures; no real SMS or banking',checks,errors,failure},null,2))
 await browser.close()
}
if(failure) throw new Error('AA live acceptance failed: '+failure)
console.log(checks.length+' live AA acceptance groups passed')
